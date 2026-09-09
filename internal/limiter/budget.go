package limiter

import (
	"errors"
	"hash/fnv"
	"sync"

	"github.com/pestit/9gateway/internal/accounting"
)

// Bifrost provenance notice: budget and admission paths at commit
// 03ab391865710462302bbcf52dca2f32682b91b5 (branch dev) were inspected in
// .references/bifrost/plugins/governance/store.go (CheckBudget, BumpBudgetUsage)
// and .references/bifrost/plugins/governance/tracker.go (UsageTracker's
// process-local billing state). The reference repository is Apache-2.0; its
// THIRD_PARTY_NOTICES.md was checked for the dependency/license chain. No
// Bifrost source, data, architecture, or dependency is adapted here. The
// checked single-process admission and separate-key state in this file is an
// independent implementation using exact gateway Money values and reservation
// ownership semantics that the reference does not provide.

var (
	// ErrBudgetCapacity means that a valid candidate does not fit in the
	// configured lifetime budget. It is distinct from invalid input/state so a
	// future HTTP layer can make a 429 decision without exposing details.
	ErrBudgetCapacity = errors.New("budget capacity unavailable")
	// ErrBudgetInvalid means that admission input or a reservation identity is
	// not safe to use. Error text intentionally contains no key or amount.
	ErrBudgetInvalid = errors.New("invalid budget admission")
	// ErrBudgetState means that an internal budget state or checked arithmetic
	// invariant was not safe to use. It fails closed and never partially admits.
	ErrBudgetState = errors.New("invalid budget state")
)

// BudgetPolicy is the narrow total-budget input consumed by the limiter.
// Limited must be true for Total to be used. A false Limited value is the
// explicit unlimited policy; an unknown Total is never silently interpreted as
// a zero limit.
type BudgetPolicy struct {
	Total   accounting.Money
	Limited bool
}

// UnlimitedBudgetPolicy returns the explicit policy for a key without a total
// budget. Unlimited admission retains no limiter state.
func UnlimitedBudgetPolicy() BudgetPolicy { return BudgetPolicy{} }

// LimitedBudgetPolicy constructs a total-budget policy. Reserve still checks
// the value and rejects an unknown or otherwise invalid Money input.
func LimitedBudgetPolicy(total accounting.Money) BudgetPolicy {
	return BudgetPolicy{Total: total, Limited: true}
}

// BudgetSpent is an initialization value for committed lifetime spend. It is
// deliberately separate from active reservations. Loading is intended for
// startup/test setup and does not persist anything.
type BudgetSpent struct {
	KeyID string
	Spent accounting.Money
}

// CommittedBudget is a descriptive alias for initialization callers.
type CommittedBudget = BudgetSpent

type budgetShard struct {
	mu     sync.Mutex
	states map[string]*budgetState
}

type budgetState struct {
	spent    accounting.Money
	active   accounting.Money
	total    accounting.Money
	hasLimit bool
}

const budgetShardCount = 32

// BudgetLimiter atomically reserves lifetime budget per stable gateway key ID.
// Shards allow unrelated keys to proceed independently while preserving a
// single lock for all state belonging to one key. lifecycle prevents loading
// initial state while a reservation or release is inspecting a shard.
type BudgetLimiter struct {
	lifecycle sync.RWMutex
	shards    [budgetShardCount]budgetShard
}

// BudgetReservation is an immutable, copy-safe ownership handle for one
// admitted amount. Copying the value copies only a pointer to shared ownership;
// it cannot duplicate the reservation or expose limiter counters.
type BudgetReservation struct {
	ownership *budgetReservationOwnership
}

type budgetReservationOwnership struct {
	mu         sync.Mutex
	limiter    *BudgetLimiter
	keyID      string
	state      *budgetState
	amount     accounting.Money
	released   bool
	releaseErr error
}

// NewBudgetLimiter creates an empty process-local lifetime budget limiter.
func NewBudgetLimiter() *BudgetLimiter {
	limiter := &BudgetLimiter{}
	for index := range limiter.shards {
		limiter.shards[index].states = make(map[string]*budgetState)
	}
	return limiter
}

// LoadSpent initializes committed lifetime spend. It is strict and atomic:
// every key must be non-empty and every amount known/non-negative, no key may
// be duplicated, and no initialized key may already have limiter state.
// Active reservations are never imported.
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
		// Zero committed spend needs no retained state. This also keeps an
		// initialized zero from becoming indistinguishable from a live budget.
		if isZeroMoney(spent) {
			continue
		}
		limiter.shard(keyID).states[keyID] = &budgetState{
			spent:  spent,
			active: knownZeroMoney(),
		}
	}
	return nil
}

// LoadCommittedSpent is an explicit alias for LoadSpent.
func (limiter *BudgetLimiter) LoadCommittedSpent(values []BudgetSpent) error {
	return limiter.LoadSpent(values)
}

// LoadCommitted is a descriptive alias for LoadSpent.
func (limiter *BudgetLimiter) LoadCommitted(values []CommittedBudget) error {
	return limiter.LoadSpent(values)
}

// Reserve atomically admits candidate against spent plus active reservations.
// A zero candidate is a valid inert reservation: it validates the key and
// policy, returns immutable ownership, and does not create or modify state.
// Unlimited policy is explicit and never creates active state.
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
		if state.hasLimit && !policy.Limited {
			return nil, ErrBudgetInvalid
		}
		if state.hasLimit && !sameMoney(state.total, policy.Total) {
			return nil, ErrBudgetInvalid
		}
	}

	if !policy.Limited {
		// Unlimited admission is always inert, including when committed spend
		// was initialized for this key for a later limited policy.
		return newBudgetReservation(limiter, keyID, nil, candidate), nil
	}
	if shard.states == nil {
		shard.states = make(map[string]*budgetState)
	}

	// Compute against local values first. In particular, binding a policy or
	// creating a map entry happens only after every checked admission operation
	// succeeds, so rejection cannot change spent or active state.
	spent := knownZeroMoney()
	active := knownZeroMoney()
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
		// A known zero candidate is admitted as an inert reservation. It still
		// observes an over-budget spent/active total above, but never owns a
		// mutable state entry or changes counters.
		return newBudgetReservation(limiter, keyID, nil, candidate), nil
	}
	newActive, err := active.Add(candidate)
	if err != nil || !newActive.Known() {
		return nil, ErrBudgetState
	}
	if state == nil {
		state = &budgetState{spent: spent, active: newActive, total: policy.Total, hasLimit: true}
		shard.states[keyID] = state
	} else {
		if !state.hasLimit {
			state.total = policy.Total
			state.hasLimit = true
		}
		state.active = newActive
	}
	return newBudgetReservation(limiter, keyID, state, candidate), nil
}

// TryReserve is a descriptive alias for Reserve.
func (limiter *BudgetLimiter) TryReserve(keyID string, policy BudgetPolicy, candidate accounting.Money) (*BudgetReservation, error) {
	return limiter.Reserve(keyID, policy, candidate)
}

// Amount returns the exact admitted amount. The returned Money is immutable.
func (reservation *BudgetReservation) Amount() accounting.Money {
	if reservation == nil || reservation.ownership == nil {
		return accounting.UnknownMoney()
	}
	return reservation.ownership.amount
}

// KeyID returns the stable key identity associated with the reservation. It is
// not a raw API key and is safe to copy.
func (reservation *BudgetReservation) KeyID() string {
	if reservation == nil || reservation.ownership == nil {
		return ""
	}
	return reservation.ownership.keyID
}

// Release returns an active reservation exactly once. Repeated and concurrent
// calls return the first terminal result. A corrupt state fails closed without
// subtracting or underflowing active capacity.
func (reservation *BudgetReservation) Release() error {
	if reservation == nil || reservation.ownership == nil {
		return nil
	}
	ownership := reservation.ownership
	ownership.mu.Lock()
	defer ownership.mu.Unlock()
	if ownership.released {
		return ownership.releaseErr
	}
	ownership.released = true
	if ownership.limiter == nil || ownership.state == nil {
		return nil
	}

	limiter := ownership.limiter
	limiter.lifecycle.RLock()
	shard := limiter.shard(ownership.keyID)
	shard.mu.Lock()
	state := shard.states[ownership.keyID]
	if state != ownership.state {
		shard.mu.Unlock()
		limiter.lifecycle.RUnlock()
		ownership.releaseErr = ErrBudgetState
		return ownership.releaseErr
	}
	if err := validateBudgetState(state); err != nil {
		shard.mu.Unlock()
		limiter.lifecycle.RUnlock()
		ownership.releaseErr = err
		return err
	}
	active, err := state.active.Subtract(ownership.amount)
	if err != nil || !active.Known() {
		shard.mu.Unlock()
		limiter.lifecycle.RUnlock()
		ownership.releaseErr = ErrBudgetState
		return ownership.releaseErr
	}
	state.active = active
	if isZeroMoney(state.spent) && isZeroMoney(state.active) {
		delete(shard.states, ownership.keyID)
	}
	shard.mu.Unlock()
	limiter.lifecycle.RUnlock()
	return nil
}

// Len reports retained key state. Unlimited and released reservations do not
// retain state; it is intended for tests and idle-state observability only.
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

func newBudgetReservation(limiter *BudgetLimiter, keyID string, state *budgetState, amount accounting.Money) *BudgetReservation {
	return &BudgetReservation{ownership: &budgetReservationOwnership{limiter: limiter, keyID: keyID, state: state, amount: amount}}
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

func knownZeroMoney() accounting.Money {
	value, _ := accounting.NewMoneyMicros(0)
	return value
}

func isZeroMoney(value accounting.Money) bool {
	micros, known := value.Micros()
	return known && micros == 0
}

func sameMoney(first, second accounting.Money) bool {
	equal, err := first.Equal(second)
	return err == nil && equal
}

func validateBudgetState(state *budgetState) error {
	if state == nil || !validKnownMoney(state.spent) || !validKnownMoney(state.active) {
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
			// Committed spend may legitimately be over the limit (debt), but
			// active reservations are never allowed to exceed the configured
			// total and therefore indicate corrupt state.
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
