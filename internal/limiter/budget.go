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
	// ErrBudgetPolicyReplacementConflict identifies an admission or replacement
	// made with a policy which is no longer the published policy for the key.
	// It is deliberately separate from capacity: stale principals must not be
	// retried as if they merely ran out of money.
	ErrBudgetPolicyReplacementConflict = errors.New("budget policy replacement conflict")
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

// BudgetPolicy is the narrow total/day/month-budget input consumed by the
// limiter. Month boundaries are UTC calendar boundaries, not a duration.
// T118 provenance: optional Bifrost inspection was limited to commit
// 03ab391865710462302bbcf52dca2f32682b91b5, paths
// .references/bifrost/plugins/governance/store.go and tracker.go, under the
// Apache-2.0 .references/bifrost/LICENSE; no Bifrost source or data is copied.
type BudgetPolicy struct {
	Total        accounting.Money
	Limited      bool
	Day          accounting.Money
	DayLimited   bool
	Month        accounting.Money
	MonthLimited bool
}

type BudgetPeriod = auth.BudgetPeriod

const BudgetPeriodTotal = auth.BudgetPeriodTotal
const BudgetPeriodDay = auth.BudgetPeriodDay
const BudgetPeriodMonth = auth.BudgetPeriodMonth

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
func MonthlyBudgetPolicy(month accounting.Money) BudgetPolicy {
	return BudgetPolicy{Month: month, MonthLimited: true}
}
func TotalDailyMonthlyBudgetPolicy(total, day, month accounting.Money) BudgetPolicy {
	return BudgetPolicy{Total: total, Limited: true, Day: day, DayLimited: true, Month: month, MonthLimited: true}
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
	spent         accounting.Money
	active        accounting.Money
	dayBuckets    map[time.Time]budgetDayState
	monthBuckets  map[time.Time]budgetDayState
	total         accounting.Money
	hasLimit      bool
	dayLimit      accounting.Money
	hasDayLimit   bool
	monthLimit    accounting.Money
	hasMonthLimit bool
	totalLoaded   bool
	generation    uint64
}
type budgetDayState struct{ spent, active accounting.Money }

const budgetShardCount = 32

type BudgetLimiter struct {
	lifecycle sync.RWMutex
	shards    [budgetShardCount]budgetShard
	sequence  atomic.Uint64
	policyMu  sync.RWMutex
	// policies is the last successfully published policy identity for each key.
	// Reserve checks this while holding policyMu, making an authenticated
	// principal from before a committed replacement fail closed.
	policies  map[string]BudgetPolicy
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
	mu            sync.Mutex
	limiter       *BudgetLimiter
	keyID         string
	state         *budgetState
	generation    uint64
	amount        accounting.Money
	dayStart      time.Time
	monthStart    time.Time
	totalCaptured bool
	dayCaptured   bool
	monthCaptured bool
	finalized     bool
	finalErr      error
	result        BudgetSettlementResult
	ticket        *BudgetAdjustmentTicket
}

// BudgetAdjustmentTicket owns only stable limiter identity and generation. It
// deliberately does not retain a reservation or composite lease.
type BudgetAdjustmentTicket struct {
	mu            sync.Mutex
	limiter       *BudgetLimiter
	keyID         string
	generation    uint64
	reserved      accounting.Money
	dayStart      time.Time
	monthStart    time.Time
	totalCaptured bool
	dayCaptured   bool
	monthCaptured bool
	consumed      bool
	resultErr     error
}

func NewBudgetLimiter(clocks ...Clock) *BudgetLimiter {
	var now Clock
	if len(clocks) != 0 {
		now = clocks[0]
	}
	limiter := &BudgetLimiter{now: now, policies: make(map[string]BudgetPolicy)}
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
func currentMonth(now time.Time) time.Time {
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}
func validMonthStart(start time.Time) bool {
	return !start.IsZero() && start.Equal(currentMonth(start))
}
func nextMonth(start time.Time) time.Time { return start.UTC().AddDate(0, 1, 0) }
func validDayStart(start time.Time) bool  { return !start.IsZero() && start.Equal(currentDay(start)) }

// LoadSpent initializes committed lifetime spend without importing active
// reservations. It is strict and atomic.
func (limiter *BudgetLimiter) LoadSpent(values []BudgetSpent) error {
	if limiter == nil {
		return ErrBudgetInvalid
	}
	pending := make(map[string]accounting.Money, len(values))
	for _, value := range values {
		if !validKeyID(value.KeyID) || !validKnownMoney(value.Spent) || (value.Period != "" && value.Period != BudgetPeriodTotal && value.Period != BudgetPeriodDay && value.Period != BudgetPeriodMonth) {
			return ErrBudgetInvalid
		}
		if value.Period == BudgetPeriodDay && !validDayStart(value.PeriodStart) {
			return ErrBudgetInvalid
		}
		if value.Period == BudgetPeriodMonth && !validMonthStart(value.PeriodStart) {
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
		if period == BudgetPeriodDay || period == BudgetPeriodMonth {
			start, _ = time.Parse(time.RFC3339Nano, parts[2])
		}
		state := limiter.shard(keyID).states[keyID]
		_, loadedDay := stateDay(state, start)
		loadedMonth := false
		if state != nil && state.monthBuckets != nil {
			_, loadedMonth = state.monthBuckets[start]
		}
		if state != nil && ((period == BudgetPeriodDay && loadedDay) || (period == BudgetPeriodMonth && loadedMonth) || (period != BudgetPeriodDay && period != BudgetPeriodMonth && state.totalLoaded)) {
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
			state = &budgetState{spent: knownZeroMoney(), active: knownZeroMoney(), dayBuckets: make(map[time.Time]budgetDayState), monthBuckets: make(map[time.Time]budgetDayState), generation: generation}
			limiter.shard(keyID).states[keyID] = state
		}
		if period == BudgetPeriodDay {
			if state.dayBuckets == nil {
				state.dayBuckets = make(map[time.Time]budgetDayState)
			}
			state.dayBuckets[start] = budgetDayState{spent: spent, active: knownZeroMoney()}
		} else if period == BudgetPeriodMonth {
			if state.monthBuckets == nil {
				state.monthBuckets = make(map[time.Time]budgetDayState)
			}
			state.monthBuckets[start] = budgetDayState{spent: spent, active: knownZeroMoney()}
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

// RegisterPolicy seeds the identity of a policy loaded before serving. It is
// runtime metadata only; committed spend remains in the period-keyed buckets.
func (limiter *BudgetLimiter) RegisterPolicy(keyID string, policy BudgetPolicy) {
	if limiter == nil || !validKeyID(keyID) || !validPolicy(policy) {
		return
	}
	limiter.policyMu.Lock()
	if limiter.policies == nil {
		limiter.policies = make(map[string]BudgetPolicy)
	}
	limiter.policies[keyID] = policy
	limiter.policyMu.Unlock()
}

// ReplacePolicy holds out admission while a durable policy update and its
// prepared authentication snapshot are committed. Existing state is retained;
// only the enforcement fields change, while reservations retain their captured
// bucket identities for later settlement.
func (limiter *BudgetLimiter) ReplacePolicy(keyID string, oldPolicy, newPolicy BudgetPolicy, commit func() error) error {
	if limiter == nil {
		if commit == nil {
			return nil
		}
		return commit()
	}
	if !validKeyID(keyID) || !validPolicy(oldPolicy) || !validPolicy(newPolicy) {
		return ErrBudgetInvalid
	}
	limiter.policyMu.Lock()
	defer limiter.policyMu.Unlock()
	if current, ok := limiter.policies[keyID]; ok && !sameBudgetPolicy(current, oldPolicy) {
		return ErrBudgetPolicyReplacementConflict
	}
	if commit != nil {
		if err := commit(); err != nil {
			return err
		}
	}
	if limiter.policies == nil {
		limiter.policies = make(map[string]BudgetPolicy)
	}
	limiter.policies[keyID] = newPolicy
	shard := limiter.shard(keyID)
	shard.mu.Lock()
	if state := shard.states[keyID]; state != nil {
		applyBudgetPolicyLocked(state, newPolicy)
	}
	shard.mu.Unlock()
	return nil
}

func (limiter *BudgetLimiter) AllowsPolicyReplacement(keyID string, oldPolicy, newPolicy BudgetPolicy) bool {
	if limiter == nil {
		return true
	}
	limiter.policyMu.RLock()
	defer limiter.policyMu.RUnlock()
	current, ok := limiter.policies[keyID]
	return !ok || sameBudgetPolicy(current, oldPolicy)
}

// PolicyCurrent reports whether the policy carried by an authenticated
// principal is still the committed policy. It is checked even for an
// unlimited replacement: otherwise a principal that carried a removed budget
// could skip Reserve entirely after the limit was removed.
func (limiter *BudgetLimiter) PolicyCurrent(keyID string, policy BudgetPolicy) bool {
	if limiter == nil {
		return true
	}
	limiter.policyMu.RLock()
	defer limiter.policyMu.RUnlock()
	current, ok := limiter.policies[keyID]
	return !ok || sameBudgetPolicy(current, policy)
}

// Reserve admits candidate against spent plus active reservations.
func (limiter *BudgetLimiter) Reserve(keyID string, policy BudgetPolicy, candidate accounting.Money) (*BudgetReservation, error) {
	if limiter == nil || !validKeyID(keyID) || !validKnownMoney(candidate) || !validPolicy(policy) {
		return nil, ErrBudgetInvalid
	}
	// The policy lock spans validation and the entire admission mutation. An
	// administrator therefore cannot publish a replacement between the
	// principal's policy check and reservation.
	limiter.policyMu.RLock()
	defer limiter.policyMu.RUnlock()
	limiter.lifecycle.RLock()
	defer limiter.lifecycle.RUnlock()
	if current, ok := limiter.policies[keyID]; ok && !sameBudgetPolicy(current, policy) {
		return nil, ErrBudgetPolicyReplacementConflict
	}
	shard := limiter.shard(keyID)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	state := shard.states[keyID]
	if state != nil {
		if err := validateBudgetState(state); err != nil {
			return nil, err
		}
		// Limiter-only callers may not have a startup policy registration. Keep
		// the historical safety check for those callers; the registered path
		// deliberately permits amount changes only through ReplacePolicy.
		if _, registered := limiter.policies[keyID]; !registered {
			if state.hasLimit && (!policy.Limited || !sameMoney(state.total, policy.Total)) ||
				state.hasDayLimit && (!policy.DayLimited || !sameMoney(state.dayLimit, policy.Day)) ||
				state.hasMonthLimit && (!policy.MonthLimited || !sameMoney(state.monthLimit, policy.Month)) {
				return nil, ErrBudgetInvalid
			}
		}
	}
	if !policy.Limited && !policy.DayLimited && !policy.MonthLimited {
		return newBudgetReservation(limiter, keyID, nil, 0, candidate, time.Time{}, time.Time{}), nil
	}
	if shard.states == nil {
		shard.states = make(map[string]*budgetState)
	}
	spent, active := knownZeroMoney(), knownZeroMoney()
	daySpent, dayActive := knownZeroMoney(), knownZeroMoney()
	dayStart := currentDay(limiter.nowTime())
	monthStart := currentMonth(dayStart)
	monthSpent, monthActive := knownZeroMoney(), knownZeroMoney()
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
		if state.monthBuckets != nil {
			for start, bucket := range state.monthBuckets {
				if start.Before(monthStart) && isZeroMoney(bucket.active) {
					delete(state.monthBuckets, start)
				}
			}
			bucket := state.monthBuckets[monthStart]
			if bucket.spent.Known() {
				monthSpent = bucket.spent
			}
			if bucket.active.Known() {
				monthActive = bucket.active
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
	monthRejected := false
	if policy.MonthLimited {
		monthUsed, addErr := monthSpent.Add(monthActive)
		if addErr != nil {
			return nil, ErrBudgetState
		}
		remaining, subErr := policy.Month.Subtract(monthUsed)
		if subErr != nil {
			if errors.Is(subErr, accounting.ErrMoneyUnderflow) {
				monthRejected = true
			} else {
				return nil, ErrBudgetState
			}
		} else {
			fits, cmpErr := candidate.LessOrEqual(remaining)
			if cmpErr != nil {
				return nil, ErrBudgetState
			}
			monthRejected = !fits
		}
	}
	if totalRejected || dayRejected || monthRejected {
		reset := time.Time{}
		if !totalRejected && monthRejected {
			reset = nextMonth(monthStart)
		} else if dayRejected && !totalRejected {
			reset = dayStart.Add(24 * time.Hour)
		}
		return nil, &BudgetCapacityError{ResetAt: reset, TotalRejected: totalRejected}
	}
	if isZeroMoney(candidate) {
		return newBudgetReservation(limiter, keyID, nil, 0, candidate, time.Time{}, time.Time{}), nil
	}
	// Each active counter is scoped to the exact budget identity captured by
	// this admission. In particular, a day-only reservation must not occupy
	// the lifetime-total active counter if a total limit is added later.
	newActive := active
	if policy.Limited {
		newActive, err = active.Add(candidate)
		if err != nil || !newActive.Known() {
			return nil, ErrBudgetState
		}
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
		state = &budgetState{spent: spent, active: newActive, dayBuckets: make(map[time.Time]budgetDayState), monthBuckets: make(map[time.Time]budgetDayState), generation: generation}
		applyBudgetPolicyLocked(state, policy)
		state.totalLoaded = policy.Limited
		if policy.DayLimited {
			state.dayBuckets[dayStart] = budgetDayState{spent: daySpent, active: newDayActive}
		}
		if policy.MonthLimited {
			newMonthActive, addErr := monthActive.Add(candidate)
			if addErr != nil {
				return nil, ErrBudgetState
			}
			state.monthBuckets[monthStart] = budgetDayState{spent: monthSpent, active: newMonthActive}
		}
		shard.states[keyID] = state
	} else {
		applyBudgetPolicyLocked(state, policy)
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
		if policy.MonthLimited {
			if state.monthBuckets == nil {
				state.monthBuckets = make(map[time.Time]budgetDayState)
			}
			newMonthActive, addErr := monthActive.Add(candidate)
			if addErr != nil {
				return nil, ErrBudgetState
			}
			state.monthBuckets[monthStart] = budgetDayState{spent: monthSpent, active: newMonthActive}
			state.monthLimit, state.hasMonthLimit = policy.Month, true
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
	if !policy.MonthLimited {
		monthStart = time.Time{}
	}
	return newBudgetReservation(limiter, keyID, state, state.generation, candidate, dayStart, monthStart, policy.Limited, policy.DayLimited, policy.MonthLimited), nil
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
			return result, result.Error, nil, nil, &BudgetAdjustmentTicket{reserved: ownership.amount, dayStart: ownership.dayStart, monthStart: ownership.monthStart}
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
	newActive, subErr := state.active, error(nil)
	if ownership.totalCaptured {
		newActive, subErr = state.active.Subtract(ownership.amount)
	}
	newSpent, addErr := state.spent, error(nil)
	if ownership.totalCaptured {
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
	if ownership.dayCaptured {
		newDayActive, daySubErr = day.active.Subtract(ownership.amount)
		newDaySpent, dayAddErr = day.spent.Add(charge)
	}
	if !ownership.dayCaptured {
		newDayActive, newDaySpent, daySubErr, dayAddErr = day.active, day.spent, nil, nil
	}
	month := budgetDayState{spent: knownZeroMoney(), active: knownZeroMoney()}
	if state.monthBuckets != nil && !ownership.monthStart.IsZero() {
		month = state.monthBuckets[ownership.monthStart]
	}
	newMonthActive, monthSubErr := month.active, error(nil)
	newMonthSpent, monthAddErr := month.spent, error(nil)
	if ownership.monthCaptured {
		newMonthActive, monthSubErr = month.active.Subtract(ownership.amount)
		newMonthSpent, monthAddErr = month.spent.Add(charge)
	}
	if subErr != nil || daySubErr != nil || monthSubErr != nil || !newActive.Known() || !newDayActive.Known() || !newMonthActive.Known() {
		// The active counter cannot be safely released if its exact admitted
		// amount is absent. Do not partially mutate a corrupt state.
		shard.mu.Unlock()
		limiter.lifecycle.RUnlock()
		result.Error = &BudgetSettlementError{Cause: ErrBudgetState, Conservative: false}
		return result, result.Error, nil, nil, nil
	}
	if addErr != nil || dayAddErr != nil || monthAddErr != nil || !newSpent.Known() || !newDaySpent.Known() || !newMonthSpent.Known() {
		// A failed known-cost transition gets one conservative attempt. If even
		// that cannot be represented, retaining active state is safer than
		// pretending a charge was accounted for.
		if ownership.totalCaptured && (!isZeroMoney(charge) || kind == BudgetSettlementKnown) {
			newSpent, addErr = state.spent.Add(ownership.amount)
		}
		if ownership.dayCaptured && (!isZeroMoney(charge) || kind == BudgetSettlementKnown) {
			newDaySpent, dayAddErr = day.spent.Add(ownership.amount)
		}
		if ownership.monthCaptured && (!isZeroMoney(charge) || kind == BudgetSettlementKnown) {
			newMonthSpent, monthAddErr = month.spent.Add(ownership.amount)
		}
		if subErr != nil || addErr != nil || dayAddErr != nil || monthAddErr != nil || !newSpent.Known() || !newDaySpent.Known() || !newMonthSpent.Known() {
			shard.mu.Unlock()
			limiter.lifecycle.RUnlock()
			result.Error = &BudgetSettlementError{Cause: ErrBudgetArithmetic, Conservative: true}
			return result, result.Error, nil, nil, nil
		}
		result.Kind, result.Error = BudgetSettlementConservative, &BudgetSettlementError{Cause: ErrBudgetArithmetic, Conservative: true}
		charge = ownership.amount
	}
	state.active, state.spent = newActive, newSpent
	if ownership.dayCaptured && !ownership.dayStart.IsZero() {
		if state.dayBuckets == nil {
			state.dayBuckets = make(map[time.Time]budgetDayState)
		}
		state.dayBuckets[ownership.dayStart] = budgetDayState{spent: newDaySpent, active: newDayActive}
	}
	if ownership.monthCaptured && !ownership.monthStart.IsZero() {
		if state.monthBuckets == nil {
			state.monthBuckets = make(map[time.Time]budgetDayState)
		}
		state.monthBuckets[ownership.monthStart] = budgetDayState{spent: newMonthSpent, active: newMonthActive}
	}
	result.Charged = charge
	if micros, known := charge.Micros(); known {
		result.CommittedDelta = micros
	}
	var deltas []CommittedBudgetDelta
	if !isZeroMoney(charge) {
		if ownership.totalCaptured {
			deltas = append(deltas, *checkedBudgetDelta(ownership.keyID, result.CommittedDelta, BudgetPeriodTotal, time.Time{}))
		}
		if ownership.dayCaptured && !ownership.dayStart.IsZero() {
			deltas = append(deltas, *checkedBudgetDelta(ownership.keyID, result.CommittedDelta, BudgetPeriodDay, ownership.dayStart))
		}
		if ownership.monthCaptured && !ownership.monthStart.IsZero() {
			deltas = append(deltas, *checkedBudgetDelta(ownership.keyID, result.CommittedDelta, BudgetPeriodMonth, ownership.monthStart))
		}
	}
	if isZeroMoney(state.spent) && isZeroMoney(state.active) && len(state.dayBuckets) == 0 && len(state.monthBuckets) == 0 {
		delete(shard.states, ownership.keyID)
	}
	sink := limiter.sink()
	var adjustment *BudgetAdjustmentTicket
	if deferred && result.Error == nil {
		adjustment = &BudgetAdjustmentTicket{limiter: limiter, keyID: ownership.keyID, generation: ownership.generation, reserved: ownership.amount, dayStart: ownership.dayStart, monthStart: ownership.monthStart, totalCaptured: ownership.totalCaptured, dayCaptured: ownership.dayCaptured, monthCaptured: ownership.monthCaptured}
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
	var monthDelta int64
	var newMonthSpent accounting.Money
	var ok bool
	comparison, cmpErr := actual.Compare(reserved)
	if cmpErr != nil {
		ticket.resultErr = ErrBudgetState
	} else if comparison == accounting.Greater {
		increase, subErr := actual.Subtract(reserved)
		var addErr error
		newSpent, addErr = state.spent, nil
		if ticket.totalCaptured {
			newSpent, addErr = state.spent.Add(increase)
		}
		if subErr != nil || addErr != nil || !newSpent.Known() {
			ticket.resultErr = &BudgetSettlementError{Cause: ErrBudgetArithmetic, Conservative: true}
		} else {
			delta, ok = moneyMicros(increase)
			if ticket.dayCaptured && !ticket.dayStart.IsZero() {
				day := state.dayBuckets[ticket.dayStart]
				var dayErr error
				newDaySpent, dayErr = day.spent.Add(increase)
				if dayErr != nil || !newDaySpent.Known() {
					ok = false
				} else {
					dayDelta = delta
				}
			}
			if ticket.monthCaptured && !ticket.monthStart.IsZero() {
				month := state.monthBuckets[ticket.monthStart]
				newMonthSpent, addErr = month.spent.Add(increase)
				if addErr != nil || !newMonthSpent.Known() {
					ok = false
				} else {
					monthDelta = delta
				}
			}
		}
	} else if comparison == accounting.Less {
		refund, subErr := reserved.Subtract(actual)
		var subErr2 error
		newSpent, subErr2 = state.spent, nil
		if ticket.totalCaptured {
			newSpent, subErr2 = state.spent.Subtract(refund)
		}
		if subErr != nil || subErr2 != nil || !newSpent.Known() {
			ticket.resultErr = &BudgetSettlementError{Cause: ErrBudgetArithmetic, Conservative: true}
		} else {
			r, _ := moneyMicros(refund)
			delta = -r
			ok = true
			if ticket.dayCaptured && !ticket.dayStart.IsZero() {
				day := state.dayBuckets[ticket.dayStart]
				newDaySpent, subErr2 = day.spent.Subtract(refund)
				if subErr2 != nil || !newDaySpent.Known() {
					ok = false
				} else {
					dayDelta = delta
				}
			}
			if ticket.monthCaptured && !ticket.monthStart.IsZero() {
				month := state.monthBuckets[ticket.monthStart]
				newMonthSpent, subErr2 = month.spent.Subtract(refund)
				if subErr2 != nil || !newMonthSpent.Known() {
					ok = false
				} else {
					monthDelta = delta
				}
			}
		}
	} else {
		newSpent, ok = state.spent, true
		if ticket.dayCaptured && !ticket.dayStart.IsZero() {
			newDaySpent = state.dayBuckets[ticket.dayStart].spent
		}
		if ticket.monthCaptured && !ticket.monthStart.IsZero() {
			newMonthSpent = state.monthBuckets[ticket.monthStart].spent
		}
	}
	if ticket.resultErr == nil && ok {
		state.spent = newSpent
		if ticket.dayCaptured && !ticket.dayStart.IsZero() {
			day := state.dayBuckets[ticket.dayStart]
			day.spent = newDaySpent
			state.dayBuckets[ticket.dayStart] = day
		}
		if ticket.monthCaptured && !ticket.monthStart.IsZero() {
			month := state.monthBuckets[ticket.monthStart]
			month.spent = newMonthSpent
			state.monthBuckets[ticket.monthStart] = month
		}
		if isZeroMoney(state.spent) && isZeroMoney(state.active) && len(state.dayBuckets) == 0 && len(state.monthBuckets) == 0 {
			delete(shard.states, keyID)
		} else if delta != 0 {
			shard.states[keyID] = state
		}
	}
	err := ticket.resultErr
	var deltaValue *CommittedBudgetDelta
	if err == nil && delta != 0 && ticket.totalCaptured {
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
	if err == nil && monthDelta != 0 {
		notifyBudgetDelta(sink, checkedBudgetDelta(keyID, monthDelta, BudgetPeriodMonth, ticket.monthStart))
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
	if policy.DayLimited != validKnownMoney(policy.Day) {
		return false
	}
	return policy.MonthLimited == validKnownMoney(policy.Month)
}
func sameBudgetPolicy(first, second BudgetPolicy) bool {
	return first.Limited == second.Limited && first.DayLimited == second.DayLimited && first.MonthLimited == second.MonthLimited &&
		(!first.Limited || sameMoney(first.Total, second.Total)) &&
		(!first.DayLimited || sameMoney(first.Day, second.Day)) &&
		(!first.MonthLimited || sameMoney(first.Month, second.Month))
}
func applyBudgetPolicyLocked(state *budgetState, policy BudgetPolicy) {
	state.total, state.hasLimit = policy.Total, policy.Limited
	state.dayLimit, state.hasDayLimit = policy.Day, policy.DayLimited
	state.monthLimit, state.hasMonthLimit = policy.Month, policy.MonthLimited
	state.totalLoaded = state.totalLoaded || policy.Limited
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
	if state.hasDayLimit && !validKnownMoney(state.dayLimit) {
		return ErrBudgetState
	}
	if state.hasMonthLimit && !validKnownMoney(state.monthLimit) {
		return ErrBudgetState
	}
	for start, bucket := range state.dayBuckets {
		if !validDayStart(start) || !validKnownMoney(bucket.spent) || !validKnownMoney(bucket.active) {
			return ErrBudgetState
		}
		if _, err := bucket.spent.Add(bucket.active); err != nil {
			return ErrBudgetState
		}
	}
	for start, bucket := range state.monthBuckets {
		if !validMonthStart(start) || !validKnownMoney(bucket.spent) || !validKnownMoney(bucket.active) {
			return ErrBudgetState
		}
		if _, err := bucket.spent.Add(bucket.active); err != nil {
			return ErrBudgetState
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
func newBudgetReservation(limiter *BudgetLimiter, keyID string, state *budgetState, generation uint64, amount accounting.Money, dayStart, monthStart time.Time, captured ...bool) *BudgetReservation {
	ownership := &budgetReservationOwnership{limiter: limiter, keyID: keyID, state: state, generation: generation, amount: amount, dayStart: dayStart, monthStart: monthStart}
	if len(captured) > 0 {
		ownership.totalCaptured = captured[0]
	}
	if len(captured) > 1 {
		ownership.dayCaptured = captured[1]
	}
	if len(captured) > 2 {
		ownership.monthCaptured = captured[2]
	}
	return &BudgetReservation{ownership: ownership}
}
