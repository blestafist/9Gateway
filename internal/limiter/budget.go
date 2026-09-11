package limiter

import (
	"errors"
	"hash/fnv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pestit/9gateway/internal/accounting"
	"github.com/pestit/9gateway/internal/auth"
)

// Bifrost provenance notice: the budget and usage paths at commit
// 03ab391865710462302bbcf52dca2f32682b91b5 (branch dev) were inspected in
// .references/bifrost/plugins/governance/store.go (CheckBudget, BumpBudgetUsage)
// and .references/bifrost/plugins/governance/tracker.go (UsageTracker's
// process-local billing state). The reference repository is Apache-2.0; its
// LICENSE and THIRD_PARTY_NOTICES.md were verified. No Bifrost source, data,
// architecture, or dependency is copied or adapted here. The implementation
// below is independent and adds exact Money reservations, conservative
// settlement, and generation-bound one-shot adjustment tickets, none of which
// are supplied by the reference.

var (
	ErrBudgetCapacity = errors.New("budget capacity unavailable")
	ErrBudgetInvalid  = errors.New("invalid budget admission")
	ErrBudgetState    = errors.New("invalid budget state")
	// ErrBudgetInvalidActual identifies an unknown or otherwise invalid actual
	// cost. Such a cost is conservatively charged when that is representable.
	ErrBudgetInvalidActual = errors.New("invalid budget actual cost")
	ErrBudgetArithmetic    = errors.New("budget arithmetic failure")
)

// BudgetCapacityError retains the daily reset which can make an otherwise
// rejected admission possible. TotalRejected suppresses it when time alone is
// insufficient.
type BudgetCapacityError struct {
	ResetAt       time.Time
	TotalRejected bool
}

func (err *BudgetCapacityError) Error() string { return ErrBudgetCapacity.Error() }
func (err *BudgetCapacityError) Unwrap() error { return ErrBudgetCapacity }

// BudgetSettlementError is returned when a terminal operation had to choose a
// safe fallback. Conservative reports whether the reservation's estimate was
// committed. The value is immutable once returned and is safe to inspect with
// errors.As/errors.Is.
type BudgetSettlementError struct {
	Cause        error
	Conservative bool
}

func (err *BudgetSettlementError) Error() string {
	if err == nil || err.Cause == nil {
		return ""
	}
	return err.Cause.Error()
}

func (err *BudgetSettlementError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

// BudgetSettlementKind records the first terminal transition. It is separate
// from the returned error: invalid actual cost is a conservative completion,
// not a partially applied known-cost commit.
type BudgetSettlementKind uint8

const (
	BudgetSettlementKnown BudgetSettlementKind = iota + 1
	BudgetSettlementConservative
	BudgetSettlementPreUpstreamRelease
	BudgetSettlementDeferred
)

// BudgetSettlementResult is a copy-only description of the first terminal
// operation. Money itself is immutable. Error is the same terminal error that
// the operation returned, if any; callers should not mutate a pointed error.
type BudgetSettlementResult struct {
	KeyID          string
	Kind           BudgetSettlementKind
	Reserved       accounting.Money
	Actual         accounting.Money
	Charged        accounting.Money
	CommittedDelta int64
	Error          error
}

// CommittedBudgetDelta is the only data emitted to the optional committed
// spend sink. Delta is a checked signed change in integer USD micros and is
// always in [-MaxMoneyMicros, MaxMoneyMicros]. CommittedDelta is retained as a
// descriptive alias for callers that mirror token persistence naming.
//
// The sink must be nonblocking. It is called after state publication, outside
// all limiter locks, and its result is never needed for limiter correctness.
type CommittedBudgetDelta struct {
	KeyID          string
	Delta          int64
	CommittedDelta int64
	Period         BudgetPeriod
	PeriodStart    time.Time
}

// BudgetSpendDelta is a descriptive alias for CommittedBudgetDelta.
type BudgetSpendDelta = CommittedBudgetDelta

// BudgetPolicy is the narrow total-budget input consumed by the limiter.
type BudgetPolicy struct {
	Total      accounting.Money
	Limited    bool
	Day        accounting.Money
	DayLimited bool
}

type BudgetPeriod = auth.BudgetPeriod

const BudgetPeriodTotal = auth.BudgetPeriodTotal
const BudgetPeriodDay = auth.BudgetPeriodDay

func UnlimitedBudgetPolicy() BudgetPolicy { return BudgetPolicy{} }
func LimitedBudgetPolicy(total accounting.Money) BudgetPolicy {
	return BudgetPolicy{Total: total, Limited: true}
}
func LimitedBudgetPolicyWithDay(total accounting.Money, day accounting.Money, totalLimited bool) BudgetPolicy {
	return BudgetPolicy{Total: total, Limited: totalLimited, Day: day, DayLimited: true}
}
func DailyBudgetPolicy(day accounting.Money) BudgetPolicy {
	return BudgetPolicy{Day: day, DayLimited: true}
}
func TotalAndDailyBudgetPolicy(total, day accounting.Money) BudgetPolicy {
	return LimitedBudgetPolicyWithDay(total, day, true)
}

type BudgetSpent struct {
	KeyID       string
	Spent       accounting.Money
	Period      BudgetPeriod
	PeriodStart time.Time
}

type CommittedBudget = BudgetSpent

type budgetShard struct {
	mu     sync.Mutex
	states map[string]*budgetState
}

type budgetState struct {
	spent       accounting.Money
	active      accounting.Money
	dayBuckets  map[time.Time]budgetDayState
	total       accounting.Money
	hasLimit    bool
	dayLimit    accounting.Money
	hasDayLimit bool
	totalLoaded bool
	generation  uint64
}
type budgetDayState struct{ spent, active accounting.Money }

const budgetShardCount = 32

type BudgetLimiter struct {
	lifecycle sync.RWMutex
	shards    [budgetShardCount]budgetShard
	sequence  atomic.Uint64
	sinkMu    sync.RWMutex
	deltaSink func(CommittedBudgetDelta)
	now       Clock
	timeMu    sync.Mutex
	lastNow   time.Time
}

// BudgetReservation is a copy-safe ownership handle. Copies share private
// terminal control and cannot duplicate the active reservation.
type BudgetReservation struct {
	ownership *budgetReservationOwnership
}

type budgetReservationOwnership struct {
	mu         sync.Mutex
	limiter    *BudgetLimiter
	keyID      string
	state      *budgetState
	generation uint64
	amount     accounting.Money
	dayStart   time.Time
	finalized  bool
	finalErr   error
	result     BudgetSettlementResult
	ticket     *BudgetAdjustmentTicket
}

// BudgetAdjustmentTicket owns only stable limiter identity and generation. It
// deliberately does not retain a reservation or composite lease.
type BudgetAdjustmentTicket struct {
	mu         sync.Mutex
	limiter    *BudgetLimiter
	keyID      string
	generation uint64
	reserved   accounting.Money
	dayStart   time.Time
	consumed   bool
	resultErr  error
}

func NewBudgetLimiter(clocks ...Clock) *BudgetLimiter {
	var now Clock
	if len(clocks) != 0 {
		now = clocks[0]
	}
	limiter := &BudgetLimiter{now: now}
	for index := range limiter.shards {
		limiter.shards[index].states = make(map[string]*budgetState)
	}
	return limiter
}

// SetCommittedDeltaSink installs a narrow, process-owned notification hook.
// The contract forbids blocking and treats panics as isolated sink failures;
// neither can corrupt or roll back already-published limiter state.
func (limiter *BudgetLimiter) SetCommittedDeltaSink(sink func(CommittedBudgetDelta)) {
	if limiter == nil {
		return
	}
	limiter.sinkMu.Lock()
	limiter.deltaSink = sink
	limiter.sinkMu.Unlock()
}

func (limiter *BudgetLimiter) sink() func(CommittedBudgetDelta) {
	limiter.sinkMu.RLock()
	defer limiter.sinkMu.RUnlock()
	return limiter.deltaSink
}

func (limiter *BudgetLimiter) nowTime() time.Time {
	if limiter == nil {
		return time.Now().UTC()
	}
	limiter.timeMu.Lock()
	defer limiter.timeMu.Unlock()
	now := time.Now().UTC()
	if limiter.now != nil {
		now = limiter.now().UTC()
	}
	if !limiter.lastNow.IsZero() && now.Before(limiter.lastNow) {
		return limiter.lastNow
	}
	limiter.lastNow = now
	return now
}
func (limiter *BudgetLimiter) RetryAfterSeconds(resetAt time.Time) int {
	return RetryAfterSecondsAt(limiter.nowTime(), resetAt)
}
func currentDay(now time.Time) time.Time {
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}
func validDayStart(start time.Time) bool { return !start.IsZero() && start.Equal(currentDay(start)) }

// LoadSpent initializes committed lifetime spend without importing active
// reservations. It is strict and atomic.
func (limiter *BudgetLimiter) LoadSpent(values []BudgetSpent) error {
	if limiter == nil {
		return ErrBudgetInvalid
	}
	pending := make(map[string]accounting.Money, len(values))
	for _, value := range values {
		if !validKeyID(value.KeyID) || !validKnownMoney(value.Spent) || (value.Period != "" && value.Period != BudgetPeriodTotal && value.Period != BudgetPeriodDay) {
			return ErrBudgetInvalid
		}
		if value.Period == BudgetPeriodDay && !validDayStart(value.PeriodStart) {
			return ErrBudgetInvalid
		}
		period := value.Period
		if period == "" {
			period = BudgetPeriodTotal
		}
		identity := value.KeyID + "\x00" + string(period) + "\x00" + value.PeriodStart.UTC().Format(time.RFC3339Nano)
		if _, duplicate := pending[identity]; duplicate {
			return ErrBudgetInvalid
		}
		pending[identity] = value.Spent
	}
	limiter.lifecycle.Lock()
	defer limiter.lifecycle.Unlock()
	limiter.lockAllShards()
	defer limiter.unlockAllShards()
	for index := range limiter.shards {
		if limiter.shards[index].states == nil {
			limiter.shards[index].states = make(map[string]*budgetState)
		}
	}
	for identity, spent := range pending {
		parts := strings.Split(identity, "\x00")
		keyID := parts[0]
		period := BudgetPeriod(parts[1])
		start := time.Time{}
		if period == BudgetPeriodDay {
			start, _ = time.Parse(time.RFC3339Nano, parts[2])
		}
		state := limiter.shard(keyID).states[keyID]
		_, loadedDay := stateDay(state, start)
		if state != nil && ((period == BudgetPeriodDay && loadedDay) || period != BudgetPeriodDay && state.totalLoaded) {
			return ErrBudgetInvalid
		}
		if isZeroMoney(spent) {
			continue
		}
		generation, ok := limiter.nextGeneration()
		if !ok {
			return ErrBudgetState
		}
		if state == nil {
			state = &budgetState{spent: knownZeroMoney(), active: knownZeroMoney(), dayBuckets: make(map[time.Time]budgetDayState), generation: generation}
			limiter.shard(keyID).states[keyID] = state
		}
		if period == BudgetPeriodDay {
			if state.dayBuckets == nil {
				state.dayBuckets = make(map[time.Time]budgetDayState)
			}
			state.dayBuckets[start] = budgetDayState{spent: spent, active: knownZeroMoney()}
		} else {
			state.spent, state.totalLoaded = spent, true
		}
	}
	return nil
}

func stateDay(state *budgetState, start time.Time) (budgetDayState, bool) {
	if state == nil || state.dayBuckets == nil {
		return budgetDayState{}, false
	}
	bucket, ok := state.dayBuckets[start]
	return bucket, ok
}

func (limiter *BudgetLimiter) LoadCommittedSpent(values []BudgetSpent) error {
	return limiter.LoadSpent(values)
}
func (limiter *BudgetLimiter) LoadCommitted(values []CommittedBudget) error {
	return limiter.LoadSpent(values)
}

// Reserve admits candidate against spent plus active reservations.
func (limiter *BudgetLimiter) Reserve(keyID string, policy BudgetPolicy, candidate accounting.Money) (*BudgetReservation, error) {
	if limiter == nil || !validKeyID(keyID) || !validKnownMoney(candidate) || !validPolicy(policy) {
		return nil, ErrBudgetInvalid
	}
	limiter.lifecycle.RLock()
	defer limiter.lifecycle.RUnlock()
	shard := limiter.shard(keyID)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	state := shard.states[keyID]
	if state != nil {
		if err := validateBudgetState(state); err != nil {
			return nil, err
		}
		if state.hasLimit && (!policy.Limited || !sameMoney(state.total, policy.Total)) {
			return nil, ErrBudgetInvalid
		}
		if state.hasDayLimit && (!policy.DayLimited || !sameMoney(state.dayLimit, policy.Day)) {
			return nil, ErrBudgetInvalid
		}
	}
	if !policy.Limited && !policy.DayLimited {
		return newBudgetReservation(limiter, keyID, nil, 0, candidate), nil
	}
	if shard.states == nil {
		shard.states = make(map[string]*budgetState)
	}
	spent, active := knownZeroMoney(), knownZeroMoney()
	daySpent, dayActive := knownZeroMoney(), knownZeroMoney()
	dayStart := currentDay(limiter.nowTime())
	if state != nil {
		for start, bucket := range state.dayBuckets {
			if start.Before(dayStart) && isZeroMoney(bucket.active) {
				delete(state.dayBuckets, start)
			}
		}
		spent, active = state.spent, state.active
		if state.dayBuckets != nil {
			bucket := state.dayBuckets[dayStart]
			if bucket.spent.Known() {
				daySpent = bucket.spent
			}
			if bucket.active.Known() {
				dayActive = bucket.active
			}
		}
	}
	used, err := spent.Add(active)
	if err != nil || !used.Known() {
		return nil, ErrBudgetState
	}
	totalRejected := false
	if policy.Limited {
		remaining, subErr := policy.Total.Subtract(used)
		if subErr != nil {
			if errors.Is(subErr, accounting.ErrMoneyUnderflow) {
				totalRejected = true
			} else {
				return nil, ErrBudgetState
			}
		} else {
			fits, cmpErr := candidate.LessOrEqual(remaining)
			if cmpErr != nil {
				return nil, ErrBudgetState
			}
			totalRejected = !fits
		}
	}
	dayUsed, addErr := daySpent.Add(dayActive)
	if addErr != nil {
		return nil, ErrBudgetState
	}
	dayRejected := false
	if policy.DayLimited {
		remaining, subErr := policy.Day.Subtract(dayUsed)
		if subErr != nil {
			if errors.Is(subErr, accounting.ErrMoneyUnderflow) {
				dayRejected = true
			} else {
				return nil, ErrBudgetState
			}
		} else {
			fits, cmpErr := candidate.LessOrEqual(remaining)
			if cmpErr != nil {
				return nil, ErrBudgetState
			}
			dayRejected = !fits
		}
	}
	if totalRejected || dayRejected {
		reset := time.Time{}
		if dayRejected && !totalRejected {
			reset = dayStart.Add(24 * time.Hour)
		}
		return nil, &BudgetCapacityError{ResetAt: reset, TotalRejected: totalRejected}
	}
	if isZeroMoney(candidate) {
		return newBudgetReservation(limiter, keyID, nil, 0, candidate), nil
	}
	newActive, err := active.Add(candidate)
	if err != nil || !newActive.Known() {
		return nil, ErrBudgetState
	}
	newDayActive := dayActive
	if policy.DayLimited {
		newDayActive, err = dayActive.Add(candidate)
		if err != nil {
			return nil, ErrBudgetState
		}
	}
	if state == nil {
		generation, ok := limiter.nextGeneration()
		if !ok {
			return nil, ErrBudgetState
		}
		state = &budgetState{spent: spent, active: newActive, dayBuckets: make(map[time.Time]budgetDayState), total: policy.Total, hasLimit: policy.Limited, dayLimit: policy.Day, hasDayLimit: policy.DayLimited, generation: generation}
		state.totalLoaded = policy.Limited
		if policy.DayLimited {
			state.dayBuckets[dayStart] = budgetDayState{spent: daySpent, active: newDayActive}
		}
		shard.states[keyID] = state
	} else {
		if policy.Limited {
			state.total, state.hasLimit = policy.Total, true
		}
		state.active = newActive
		if policy.DayLimited {
			if state.dayBuckets == nil {
				state.dayBuckets = make(map[time.Time]budgetDayState)
			}
			state.dayBuckets[dayStart] = budgetDayState{spent: daySpent, active: newDayActive}
		}
		if policy.Limited {
			state.total, state.hasLimit = policy.Total, true
		}
		if policy.DayLimited {
			state.dayLimit, state.hasDayLimit = policy.Day, true
		}
	}
	if !policy.DayLimited {
		dayStart = time.Time{}
	}
	return newBudgetReservation(limiter, keyID, state, state.generation, candidate, dayStart), nil
}

func (limiter *BudgetLimiter) TryReserve(keyID string, policy BudgetPolicy, candidate accounting.Money) (*BudgetReservation, error) {
	return limiter.Reserve(keyID, policy, candidate)
}

func (reservation *BudgetReservation) Amount() accounting.Money {
	if reservation == nil || reservation.ownership == nil {
		return accounting.UnknownMoney()
	}
	return reservation.ownership.amount
}

func (reservation *BudgetReservation) KeyID() string {
	if reservation == nil || reservation.ownership == nil {
		return ""
	}
	return reservation.ownership.keyID
}

// Result returns the immutable first terminal result. The boolean is false
// until a terminal method has been called.
func (reservation *BudgetReservation) Result() (BudgetSettlementResult, bool) {
	if reservation == nil || reservation.ownership == nil {
		return BudgetSettlementResult{}, false
	}
	ownership := reservation.ownership
	ownership.mu.Lock()
	defer ownership.mu.Unlock()
	return ownership.result, ownership.finalized
}

func (reservation *BudgetReservation) SettlementResult() (BudgetSettlementResult, bool) {
	return reservation.Result()
}

func (reservation *BudgetReservation) Commit(actual accounting.Money) error {
	return reservation.finalize(BudgetSettlementKnown, actual)
}

// CommitKnown is the explicit spelling for a cost known to be actual.
func (reservation *BudgetReservation) CommitKnown(actual accounting.Money) error {
	return reservation.Commit(actual)
}

func (reservation *BudgetReservation) CompleteConservative() error {
	return reservation.finalize(BudgetSettlementConservative, accounting.UnknownMoney())
}

func (reservation *BudgetReservation) ConservativeComplete() error {
	return reservation.CompleteConservative()
}

func (reservation *BudgetReservation) AbortConservative() error {
	return reservation.CompleteConservative()
}

// ReleaseBeforeUpstream is the only zero-cost release. Release retains the
// historical API and is intentionally mapped to this proven pre-start path.
func (reservation *BudgetReservation) ReleaseBeforeUpstream() error {
	return reservation.finalize(BudgetSettlementPreUpstreamRelease, knownZeroMoney())
}

func (reservation *BudgetReservation) Release() error {
	return reservation.ReleaseBeforeUpstream()
}

// CommitDeferred first commits the conservative estimate, then returns a
// one-shot ticket that may replace that charge with a known actual cost.
func (reservation *BudgetReservation) CommitDeferred() (*BudgetAdjustmentTicket, error) {
	if reservation == nil || reservation.ownership == nil {
		return nil, nil
	}
	return reservation.finalizeTicket()
}

func (reservation *BudgetReservation) Defer() (*BudgetAdjustmentTicket, error) {
	return reservation.CommitDeferred()
}

func (reservation *BudgetReservation) finalize(kind BudgetSettlementKind, actual accounting.Money) error {
	_, err := reservation.finalizeInternal(kind, actual, false)
	return err
}

func (reservation *BudgetReservation) finalizeTicket() (*BudgetAdjustmentTicket, error) {
	result, err := reservation.finalizeInternal(BudgetSettlementDeferred, accounting.UnknownMoney(), true)
	if reservation == nil || reservation.ownership == nil {
		return nil, err
	}
	reservation.ownership.mu.Lock()
	ticket := reservation.ownership.ticket
	reservation.ownership.mu.Unlock()
	_ = result
	return ticket, err
}

func (reservation *BudgetReservation) finalizeInternal(kind BudgetSettlementKind, actual accounting.Money, deferred bool) (BudgetSettlementResult, error) {
	if reservation == nil || reservation.ownership == nil {
		return BudgetSettlementResult{}, nil
	}
	ownership := reservation.ownership
	ownership.mu.Lock()
	if ownership.finalized {
		result, err := ownership.result, ownership.finalErr
		ownership.mu.Unlock()
		return result, err
	}
	if ownership.limiter == nil {
		result := BudgetSettlementResult{KeyID: ownership.keyID, Kind: kind, Reserved: ownership.amount, Actual: actual, Charged: knownZeroMoney(), Error: ErrBudgetState}
		ownership.result, ownership.finalErr, ownership.finalized = result, ErrBudgetState, true
		ownership.mu.Unlock()
		return result, ErrBudgetState
	}
	result, err, deltas, sink, ticket := ownership.limiter.settleReservation(ownership, kind, actual, deferred)
	ownership.result, ownership.finalErr, ownership.ticket, ownership.finalized = result, err, ticket, true
	ownership.mu.Unlock()
	for _, delta := range deltas {
		notifyBudgetDelta(sink, &delta)
	}
	return result, err
}

func (limiter *BudgetLimiter) settleReservation(ownership *budgetReservationOwnership, kind BudgetSettlementKind, actual accounting.Money, deferred bool) (BudgetSettlementResult, error, []CommittedBudgetDelta, func(CommittedBudgetDelta), *BudgetAdjustmentTicket) {
	result := BudgetSettlementResult{KeyID: ownership.keyID, Kind: kind, Reserved: ownership.amount, Actual: actual, Charged: knownZeroMoney()}
	if limiter == nil {
		result.Error = ErrBudgetState
		return result, result.Error, nil, nil, nil
	}
	if ownership.state == nil || isZeroMoney(ownership.amount) {
		if kind == BudgetSettlementKnown && !validKnownMoney(actual) {
			result.Kind = BudgetSettlementConservative
			result.Error = &BudgetSettlementError{Cause: ErrBudgetInvalidActual, Conservative: true}
		}
		if kind == BudgetSettlementDeferred {
			return result, result.Error, nil, nil, &BudgetAdjustmentTicket{reserved: ownership.amount, dayStart: ownership.dayStart}
		}
		return result, result.Error, nil, nil, nil
	}
	limiter.lifecycle.RLock()
	shard := limiter.shard(ownership.keyID)
	shard.mu.Lock()
	state := shard.states[ownership.keyID]
	if state != ownership.state || state.generation != ownership.generation {
		shard.mu.Unlock()
		limiter.lifecycle.RUnlock()
		result.Error = ErrBudgetState
		return result, result.Error, nil, nil, nil
	}
	if err := validateBudgetState(state); err != nil {
		shard.mu.Unlock()
		limiter.lifecycle.RUnlock()
		result.Error = err
		return result, err, nil, nil, nil
	}
	charge := ownership.amount
	if kind == BudgetSettlementPreUpstreamRelease {
		charge = knownZeroMoney()
	} else if kind == BudgetSettlementKnown && validKnownMoney(actual) {
		charge = actual
	} else if kind == BudgetSettlementKnown {
		result.Kind, result.Error = BudgetSettlementConservative, &BudgetSettlementError{Cause: ErrBudgetInvalidActual, Conservative: true}
	}
	newActive, subErr := state.active.Subtract(ownership.amount)
	newSpent, addErr := state.spent, error(nil)
	if state.hasLimit {
		newSpent, addErr = state.spent.Add(charge)
	}
	day := budgetDayState{spent: knownZeroMoney(), active: knownZeroMoney()}
	if state.dayBuckets != nil {
		if existing, ok := state.dayBuckets[ownership.dayStart]; ok {
			day = existing
		}
	}
	newDayActive, daySubErr := day.active, error(nil)
	newDaySpent, dayAddErr := day.spent, error(nil)
	if state.hasDayLimit {
		newDayActive, daySubErr = day.active.Subtract(ownership.amount)
		newDaySpent, dayAddErr = day.spent.Add(charge)
	}
	if !state.hasDayLimit {
		newDayActive, newDaySpent, daySubErr, dayAddErr = day.active, day.spent, nil, nil
	}
	if subErr != nil || daySubErr != nil || !newActive.Known() || !newDayActive.Known() {
		// The active counter cannot be safely released if its exact admitted
		// amount is absent. Do not partially mutate a corrupt state.
		shard.mu.Unlock()
		limiter.lifecycle.RUnlock()
		result.Error = &BudgetSettlementError{Cause: ErrBudgetState, Conservative: false}
		return result, result.Error, nil, nil, nil
	}
	if addErr != nil || dayAddErr != nil || !newSpent.Known() || !newDaySpent.Known() {
		// A failed known-cost transition gets one conservative attempt. If even
		// that cannot be represented, retaining active state is safer than
		// pretending a charge was accounted for.
		if state.hasLimit && (!isZeroMoney(charge) || kind == BudgetSettlementKnown) {
			newSpent, addErr = state.spent.Add(ownership.amount)
		}
		if subErr != nil || addErr != nil || !newSpent.Known() {
			shard.mu.Unlock()
			limiter.lifecycle.RUnlock()
			result.Error = &BudgetSettlementError{Cause: ErrBudgetArithmetic, Conservative: true}
			return result, result.Error, nil, nil, nil
		}
		result.Kind, result.Error = BudgetSettlementConservative, &BudgetSettlementError{Cause: ErrBudgetArithmetic, Conservative: true}
		charge = ownership.amount
	}
	state.active, state.spent = newActive, newSpent
	if state.hasDayLimit && !ownership.dayStart.IsZero() {
		if state.dayBuckets == nil {
			state.dayBuckets = make(map[time.Time]budgetDayState)
		}
		state.dayBuckets[ownership.dayStart] = budgetDayState{spent: newDaySpent, active: newDayActive}
	}
	result.Charged = charge
	if micros, known := charge.Micros(); known {
		result.CommittedDelta = micros
	}
	var deltas []CommittedBudgetDelta
	if !isZeroMoney(charge) {
		if state.hasLimit {
			deltas = append(deltas, *checkedBudgetDelta(ownership.keyID, result.CommittedDelta, BudgetPeriodTotal, time.Time{}))
		}
		if state.hasDayLimit && !ownership.dayStart.IsZero() {
			deltas = append(deltas, *checkedBudgetDelta(ownership.keyID, result.CommittedDelta, BudgetPeriodDay, ownership.dayStart))
		}
	}
	if isZeroMoney(state.spent) && isZeroMoney(state.active) && len(state.dayBuckets) == 0 {
		delete(shard.states, ownership.keyID)
	}
	sink := limiter.sink()
	var adjustment *BudgetAdjustmentTicket
	if deferred && result.Error == nil {
		adjustment = &BudgetAdjustmentTicket{limiter: limiter, keyID: ownership.keyID, generation: ownership.generation, reserved: ownership.amount, dayStart: ownership.dayStart}
	}
	shard.mu.Unlock()
	limiter.lifecycle.RUnlock()
	return result, result.Error, deltas, sink, adjustment
}

func checkedBudgetDelta(keyID string, delta int64, period BudgetPeriod, start time.Time) *CommittedBudgetDelta {
	return &CommittedBudgetDelta{KeyID: keyID, Delta: delta, CommittedDelta: delta, Period: period, PeriodStart: start}
}

func notifyBudgetDelta(sink func(CommittedBudgetDelta), delta *CommittedBudgetDelta) {
	if sink == nil || delta == nil || delta.Delta == 0 {
		return
	}
	// A sink is required to be nonblocking; recover is an additional boundary
	// so a programming error in persistence cannot damage accounting state.
	func() {
		defer func() { _ = recover() }()
		sink(*delta)
	}()
}

// Adjust atomically replaces the ticket's conservative charge. Invalid input,
// stale identity, and checked arithmetic failure consume the ticket while
// retaining the conservative charge.
func (ticket *BudgetAdjustmentTicket) Adjust(actual accounting.Money) error {
	if ticket == nil {
		return nil
	}
	ticket.mu.Lock()
	if ticket.consumed {
		err := ticket.resultErr
		ticket.mu.Unlock()
		return err
	}
	ticket.consumed = true
	if !validKnownMoney(actual) {
		ticket.resultErr = &BudgetSettlementError{Cause: ErrBudgetInvalidActual, Conservative: true}
		err := ticket.resultErr
		ticket.mu.Unlock()
		return err
	}
	limiter, keyID, generation, reserved := ticket.limiter, ticket.keyID, ticket.generation, ticket.reserved
	if limiter == nil || !validKeyID(keyID) || generation == 0 {
		ticket.resultErr = ErrBudgetState
		err := ticket.resultErr
		ticket.mu.Unlock()
		return err
	}
	limiter.lifecycle.RLock()
	shard := limiter.shard(keyID)
	shard.mu.Lock()
	state := shard.states[keyID]
	if state == nil || state.generation != generation || !validKnownMoney(state.spent) || !validKnownMoney(state.active) {
		ticket.resultErr = ErrBudgetState
		err := ticket.resultErr
		shard.mu.Unlock()
		limiter.lifecycle.RUnlock()
		ticket.mu.Unlock()
		return err
	}
	var newSpent accounting.Money
	var newDaySpent accounting.Money
	var delta int64
	var dayDelta int64
	var ok bool
	comparison, cmpErr := actual.Compare(reserved)
	if cmpErr != nil {
		ticket.resultErr = ErrBudgetState
	} else if comparison == accounting.Greater {
		increase, subErr := actual.Subtract(reserved)
		var addErr error
		newSpent, addErr = state.spent, nil
		if state.hasLimit {
			newSpent, addErr = state.spent.Add(increase)
		}
		if subErr != nil || addErr != nil || !newSpent.Known() {
			ticket.resultErr = &BudgetSettlementError{Cause: ErrBudgetArithmetic, Conservative: true}
		} else {
			delta, ok = moneyMicros(increase)
			if !ticket.dayStart.IsZero() {
				day := state.dayBuckets[ticket.dayStart]
				var dayErr error
				newDaySpent, dayErr = day.spent.Add(increase)
				if dayErr != nil || !newDaySpent.Known() {
					ok = false
				} else {
					dayDelta = delta
				}
			}
		}
	} else if comparison == accounting.Less {
		refund, subErr := reserved.Subtract(actual)
		var subErr2 error
		newSpent, subErr2 = state.spent, nil
		if state.hasLimit {
			newSpent, subErr2 = state.spent.Subtract(refund)
		}
		if subErr != nil || subErr2 != nil || !newSpent.Known() {
			ticket.resultErr = &BudgetSettlementError{Cause: ErrBudgetArithmetic, Conservative: true}
		} else {
			r, _ := moneyMicros(refund)
			delta = -r
			ok = true
			if !ticket.dayStart.IsZero() {
				day := state.dayBuckets[ticket.dayStart]
				newDaySpent, subErr2 = day.spent.Subtract(refund)
				if subErr2 != nil || !newDaySpent.Known() {
					ok = false
				} else {
					dayDelta = delta
				}
			}
		}
	} else {
		newSpent, ok = state.spent, true
		if !ticket.dayStart.IsZero() {
			newDaySpent = state.dayBuckets[ticket.dayStart].spent
		}
	}
	if ticket.resultErr == nil && ok {
		state.spent = newSpent
		if !ticket.dayStart.IsZero() {
			day := state.dayBuckets[ticket.dayStart]
			day.spent = newDaySpent
			state.dayBuckets[ticket.dayStart] = day
		}
		if isZeroMoney(state.spent) && isZeroMoney(state.active) {
			delete(shard.states, keyID)
		} else if delta != 0 {
			shard.states[keyID] = state
		}
	}
	err := ticket.resultErr
	var deltaValue *CommittedBudgetDelta
	if err == nil && delta != 0 && state.hasLimit {
		deltaValue = checkedBudgetDelta(keyID, delta, BudgetPeriodTotal, time.Time{})
	}
	sink := limiter.sink()
	shard.mu.Unlock()
	limiter.lifecycle.RUnlock()
	ticket.mu.Unlock()
	notifyBudgetDelta(sink, deltaValue)
	if err == nil && dayDelta != 0 {
		notifyBudgetDelta(sink, checkedBudgetDelta(keyID, dayDelta, BudgetPeriodDay, ticket.dayStart))
	}
	return err
}

func (ticket *BudgetAdjustmentTicket) Commit(actual accounting.Money) error {
	return ticket.Adjust(actual)
}
func (ticket *BudgetAdjustmentTicket) Apply(actual accounting.Money) error {
	return ticket.Adjust(actual)
}

func (ticket *BudgetAdjustmentTicket) Invalidate() {
	if ticket == nil {
		return
	}
	ticket.mu.Lock()
	ticket.consumed = true
	ticket.limiter, ticket.keyID, ticket.generation = nil, "", 0
	ticket.reserved = accounting.UnknownMoney()
	ticket.mu.Unlock()
}

func (ticket *BudgetAdjustmentTicket) Discard() { ticket.Invalidate() }

func (limiter *BudgetLimiter) Len() int {
	if limiter == nil {
		return 0
	}
	limiter.lifecycle.RLock()
	defer limiter.lifecycle.RUnlock()
	limiter.lockAllShards()
	defer limiter.unlockAllShards()
	count := 0
	for index := range limiter.shards {
		count += len(limiter.shards[index].states)
	}
	return count
}

func (limiter *BudgetLimiter) nextGeneration() (uint64, bool) {
	generation := limiter.sequence.Add(1)
	return generation, generation != 0
}

func validPolicy(policy BudgetPolicy) bool {
	if policy.Limited != validKnownMoney(policy.Total) {
		return false
	}
	if !policy.DayLimited {
		return !policy.Day.Known()
	}
	return validKnownMoney(policy.Day)
}
func validKeyID(keyID string) bool { return keyID != "" }
func validKnownMoney(value accounting.Money) bool {
	micros, known := value.Micros()
	return known && micros >= 0
}
func knownZeroMoney() accounting.Money { value, _ := accounting.NewMoneyMicros(0); return value }
func isZeroMoney(value accounting.Money) bool {
	micros, known := value.Micros()
	return known && micros == 0
}
func sameMoney(first, second accounting.Money) bool {
	equal, err := first.Equal(second)
	return err == nil && equal
}
func moneyMicros(value accounting.Money) (int64, bool) { return value.Micros() }

func validateBudgetState(state *budgetState) error {
	if state == nil || state.generation == 0 || !validKnownMoney(state.spent) || !validKnownMoney(state.active) {
		return ErrBudgetState
	}
	if state.hasLimit && !validKnownMoney(state.total) {
		return ErrBudgetState
	}
	if _, err := state.spent.Add(state.active); err != nil {
		return ErrBudgetState
	}
	if state.hasLimit {
		fits, err := state.active.LessOrEqual(state.total)
		if err != nil || !fits {
			return ErrBudgetState
		}
	}
	if state.hasDayLimit && !validKnownMoney(state.dayLimit) {
		return ErrBudgetState
	}
	for start, bucket := range state.dayBuckets {
		if !validDayStart(start) || !validKnownMoney(bucket.spent) || !validKnownMoney(bucket.active) {
			return ErrBudgetState
		}
		if _, err := bucket.spent.Add(bucket.active); err != nil {
			return ErrBudgetState
		}
		if state.hasDayLimit {
			fits, err := bucket.active.LessOrEqual(state.dayLimit)
			if err != nil || !fits {
				return ErrBudgetState
			}
		}
	}
	return nil
}

func (limiter *BudgetLimiter) shard(keyID string) *budgetShard {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(keyID))
	return &limiter.shards[hash.Sum32()%budgetShardCount]
}
func (limiter *BudgetLimiter) lockAllShards() {
	for index := range limiter.shards {
		limiter.shards[index].mu.Lock()
	}
}
func (limiter *BudgetLimiter) unlockAllShards() {
	for index := len(limiter.shards) - 1; index >= 0; index-- {
		limiter.shards[index].mu.Unlock()
	}
}
func newBudgetReservation(limiter *BudgetLimiter, keyID string, state *budgetState, generation uint64, amount accounting.Money, dayStart ...time.Time) *BudgetReservation {
	start := time.Time{}
	if len(dayStart) != 0 {
		start = dayStart[0]
	}
	return &BudgetReservation{ownership: &budgetReservationOwnership{limiter: limiter, keyID: keyID, state: state, generation: generation, amount: amount, dayStart: start}}
}
