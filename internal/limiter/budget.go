package limiter

import (
	"errors"
	"hash/fnv"
	"sync"
	"sync/atomic"

	"github.com/pestit/9gateway/internal/accounting"
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
}

// BudgetSpendDelta is a descriptive alias for CommittedBudgetDelta.
type BudgetSpendDelta = CommittedBudgetDelta

// BudgetPolicy is the narrow total-budget input consumed by the limiter.
type BudgetPolicy struct {
	Total   accounting.Money
	Limited bool
}

func UnlimitedBudgetPolicy() BudgetPolicy { return BudgetPolicy{} }
func LimitedBudgetPolicy(total accounting.Money) BudgetPolicy {
	return BudgetPolicy{Total: total, Limited: true}
}

type BudgetSpent struct {
	KeyID string
	Spent accounting.Money
}

type CommittedBudget = BudgetSpent

type budgetShard struct {
	mu     sync.Mutex
	states map[string]*budgetState
}

type budgetState struct {
	spent      accounting.Money
	active     accounting.Money
	total      accounting.Money
	hasLimit   bool
	generation uint64
}

const budgetShardCount = 32

type BudgetLimiter struct {
	lifecycle sync.RWMutex
	shards    [budgetShardCount]budgetShard
	sequence  atomic.Uint64
	sinkMu    sync.RWMutex
	deltaSink func(CommittedBudgetDelta)
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
	consumed   bool
	resultErr  error
}

func NewBudgetLimiter() *BudgetLimiter {
	limiter := &BudgetLimiter{}
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

// LoadSpent initializes committed lifetime spend without importing active
// reservations. It is strict and atomic.
func (limiter *BudgetLimiter) LoadSpent(values []BudgetSpent) error {
	if limiter == nil {
		return ErrBudgetInvalid
	}
	pending := make(map[string]accounting.Money, len(values))
	for _, value := range values {
		if !validKeyID(value.KeyID) || !validKnownMoney(value.Spent) {
			return ErrBudgetInvalid
		}
		if _, duplicate := pending[value.KeyID]; duplicate {
			return ErrBudgetInvalid
		}
		pending[value.KeyID] = value.Spent
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
	for keyID := range pending {
		if limiter.shard(keyID).states[keyID] != nil {
			return ErrBudgetInvalid
		}
	}
	for keyID, spent := range pending {
		if isZeroMoney(spent) {
			continue
		}
		generation, ok := limiter.nextGeneration()
		if !ok {
			return ErrBudgetState
		}
		limiter.shard(keyID).states[keyID] = &budgetState{spent: spent, active: knownZeroMoney(), generation: generation}
	}
	return nil
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
	}
	if !policy.Limited {
		return newBudgetReservation(limiter, keyID, nil, 0, candidate), nil
	}
	if shard.states == nil {
		shard.states = make(map[string]*budgetState)
	}
	spent, active := knownZeroMoney(), knownZeroMoney()
	if state != nil {
		spent, active = state.spent, state.active
	}
	used, err := spent.Add(active)
	if err != nil || !used.Known() {
		return nil, ErrBudgetState
	}
	remaining, err := policy.Total.Subtract(used)
	if err != nil {
		if errors.Is(err, accounting.ErrMoneyUnderflow) {
			return nil, ErrBudgetCapacity
		}
		return nil, ErrBudgetState
	}
	fits, err := candidate.LessOrEqual(remaining)
	if err != nil {
		return nil, ErrBudgetState
	}
	if !fits {
		return nil, ErrBudgetCapacity
	}
	if isZeroMoney(candidate) {
		return newBudgetReservation(limiter, keyID, nil, 0, candidate), nil
	}
	newActive, err := active.Add(candidate)
	if err != nil || !newActive.Known() {
		return nil, ErrBudgetState
	}
	if state == nil {
		generation, ok := limiter.nextGeneration()
		if !ok {
			return nil, ErrBudgetState
		}
		state = &budgetState{spent: spent, active: newActive, total: policy.Total, hasLimit: true, generation: generation}
		shard.states[keyID] = state
	} else {
		if !state.hasLimit {
			state.total, state.hasLimit = policy.Total, true
		}
		state.active = newActive
	}
	return newBudgetReservation(limiter, keyID, state, state.generation, candidate), nil
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
	result, err, delta, sink, ticket := ownership.limiter.settleReservation(ownership, kind, actual, deferred)
	ownership.result, ownership.finalErr, ownership.ticket, ownership.finalized = result, err, ticket, true
	ownership.mu.Unlock()
	notifyBudgetDelta(sink, delta)
	return result, err
}

func (limiter *BudgetLimiter) settleReservation(ownership *budgetReservationOwnership, kind BudgetSettlementKind, actual accounting.Money, deferred bool) (BudgetSettlementResult, error, *CommittedBudgetDelta, func(CommittedBudgetDelta), *BudgetAdjustmentTicket) {
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
			return result, result.Error, nil, nil, &BudgetAdjustmentTicket{reserved: ownership.amount}
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
	newSpent, addErr := state.spent.Add(charge)
	if subErr != nil || !newActive.Known() {
		// The active counter cannot be safely released if its exact admitted
		// amount is absent. Do not partially mutate a corrupt state.
		shard.mu.Unlock()
		limiter.lifecycle.RUnlock()
		result.Error = &BudgetSettlementError{Cause: ErrBudgetState, Conservative: false}
		return result, result.Error, nil, nil, nil
	}
	if addErr != nil || !newSpent.Known() {
		// A failed known-cost transition gets one conservative attempt. If even
		// that cannot be represented, retaining active state is safer than
		// pretending a charge was accounted for.
		if !isZeroMoney(charge) || kind == BudgetSettlementKnown {
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
	result.Charged = charge
	if micros, known := charge.Micros(); known {
		result.CommittedDelta = micros
	}
	var delta *CommittedBudgetDelta
	if !isZeroMoney(charge) {
		delta = checkedBudgetDelta(ownership.keyID, result.CommittedDelta)
	}
	if isZeroMoney(state.spent) && isZeroMoney(state.active) {
		delete(shard.states, ownership.keyID)
	}
	sink := limiter.sink()
	var adjustment *BudgetAdjustmentTicket
	if deferred && result.Error == nil {
		adjustment = &BudgetAdjustmentTicket{limiter: limiter, keyID: ownership.keyID, generation: ownership.generation, reserved: ownership.amount}
	}
	shard.mu.Unlock()
	limiter.lifecycle.RUnlock()
	return result, result.Error, delta, sink, adjustment
}

func checkedBudgetDelta(keyID string, delta int64) *CommittedBudgetDelta {
	return &CommittedBudgetDelta{KeyID: keyID, Delta: delta, CommittedDelta: delta}
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
	var delta int64
	var ok bool
	comparison, cmpErr := actual.Compare(reserved)
	if cmpErr != nil {
		ticket.resultErr = ErrBudgetState
	} else if comparison == accounting.Greater {
		increase, subErr := actual.Subtract(reserved)
		var addErr error
		newSpent, addErr = state.spent.Add(increase)
		if subErr != nil || addErr != nil || !newSpent.Known() {
			ticket.resultErr = &BudgetSettlementError{Cause: ErrBudgetArithmetic, Conservative: true}
		} else {
			delta, ok = moneyMicros(increase)
		}
	} else if comparison == accounting.Less {
		refund, subErr := reserved.Subtract(actual)
		var subErr2 error
		newSpent, subErr2 = state.spent.Subtract(refund)
		if subErr != nil || subErr2 != nil || !newSpent.Known() {
			ticket.resultErr = &BudgetSettlementError{Cause: ErrBudgetArithmetic, Conservative: true}
		} else {
			r, _ := moneyMicros(refund)
			delta = -r
			ok = true
		}
	} else {
		newSpent, ok = state.spent, true
	}
	if ticket.resultErr == nil && ok {
		state.spent = newSpent
		if isZeroMoney(state.spent) && isZeroMoney(state.active) {
			delete(shard.states, keyID)
		} else if delta != 0 {
			shard.states[keyID] = state
		}
	}
	err := ticket.resultErr
	var deltaValue *CommittedBudgetDelta
	if err == nil && delta != 0 {
		deltaValue = checkedBudgetDelta(keyID, delta)
	}
	sink := limiter.sink()
	shard.mu.Unlock()
	limiter.lifecycle.RUnlock()
	ticket.mu.Unlock()
	notifyBudgetDelta(sink, deltaValue)
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
	if !policy.Limited {
		return !policy.Total.Known()
	}
	return validKnownMoney(policy.Total)
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
func newBudgetReservation(limiter *BudgetLimiter, keyID string, state *budgetState, generation uint64, amount accounting.Money) *BudgetReservation {
	return &BudgetReservation{ownership: &budgetReservationOwnership{limiter: limiter, keyID: keyID, state: state, generation: generation, amount: amount}}
}
