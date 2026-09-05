package limiter

import "sync"

// ConcurrencyLimiter tracks admitted work independently for each stable key ID.
// A zero maximum means that the key is unrestricted and therefore does not
// retain state in the limiter.
type ConcurrencyLimiter struct {
	mu     sync.Mutex
	states map[string]*concurrencyState
}

type concurrencyState struct {
	active int
}

// Lease represents one admitted concurrency slot. Release is idempotent and
// may safely be called from multiple goroutines.
type Lease struct {
	limiter  *ConcurrencyLimiter
	keyID    string
	state    *concurrencyState
	mu       sync.Mutex
	released bool
	claimed  bool
	admitted bool
}

// NewConcurrencyLimiter creates an empty per-key concurrency limiter.
func NewConcurrencyLimiter() *ConcurrencyLimiter {
	return &ConcurrencyLimiter{states: make(map[string]*concurrencyState)}
}

// Acquire immediately admits one slot for keyID when max is zero (unlimited)
// or when the key has capacity remaining. A positive maximum is enforced
// atomically; rejected acquisitions do not create or alter state.
func (limiter *ConcurrencyLimiter) Acquire(keyID string, max int) (*Lease, bool) {
	if limiter == nil || max < 0 {
		return nil, false
	}
	if max == 0 {
		// Keep the admitted identity on an unlimited no-op lease so promotion
		// can still reject a lease supplied for a different key.
		return &Lease{keyID: keyID, admitted: true}, true
	}

	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.states == nil {
		limiter.states = make(map[string]*concurrencyState)
	}
	state := limiter.states[keyID]
	if state == nil {
		state = &concurrencyState{}
		limiter.states[keyID] = state
	}
	if state.active >= max {
		// Do not retain an empty state if this limiter was concurrently
		// initialized or otherwise supplied an empty entry.
		if state.active == 0 {
			delete(limiter.states, keyID)
		}
		return nil, false
	}
	state.active++
	return &Lease{limiter: limiter, keyID: keyID, state: state, admitted: true}, true
}

// Release returns the lease's slot exactly once. A nil or unlimited lease is a
// no-op, which keeps cleanup safe on all admission paths.
func (lease *Lease) Release() {
	if lease == nil {
		return
	}
	lease.mu.Lock()
	if lease.released || lease.claimed || !lease.admitted {
		lease.mu.Unlock()
		return
	}
	lease.released = true
	lease.mu.Unlock()
	lease.releaseSlot()
}

// adopt transfers ownership to a composite request lease. Once claimed, a
// caller's provisional cleanup is intentionally a no-op; the composite lease
// becomes the only owner responsible for releasing the slot.
func (lease *Lease) adopt() bool {
	if lease == nil {
		return false
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.released || lease.claimed || !lease.admitted {
		return false
	}
	lease.claimed = true
	return true
}

// adoptFor transfers a provisional lease only when it belongs to the expected
// coordinator and key. Positive-limit promotion additionally requires a live
// slot in the limiter's current state; an unlimited no-op lease cannot be
// promoted into a configured limit.
func (lease *Lease) adoptFor(expected *ConcurrencyLimiter, keyID string, requireSlot bool) bool {
	if lease == nil {
		return false
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.released || lease.claimed || !lease.admitted {
		return false
	}
	if lease.limiter == nil {
		if requireSlot || (lease.keyID != "" && lease.keyID != keyID) {
			return false
		}
		lease.claimed = true
		return true
	}
	if expected == nil || lease.limiter != expected || lease.keyID != keyID || lease.state == nil {
		return false
	}
	expected.mu.Lock()
	defer expected.mu.Unlock()
	if expected.states == nil || expected.states[keyID] != lease.state || lease.state.active <= 0 {
		return false
	}
	lease.claimed = true
	return true
}

// releaseOwned releases a slot after ownership has been transferred to a
// composite lease. It is separate from Release so a provisional caller cannot
// accidentally release an adopted slot.
func (lease *Lease) releaseOwned() {
	if lease == nil {
		return
	}
	lease.mu.Lock()
	if lease.released {
		lease.mu.Unlock()
		return
	}
	lease.released = true
	lease.mu.Unlock()
	lease.releaseSlot()
}

func (lease *Lease) releaseSlot() {
	if lease.limiter == nil || lease.state == nil {
		return
	}
	lease.limiter.mu.Lock()
	defer lease.limiter.mu.Unlock()

	// The state is removed only while holding the same mutex used by Acquire.
	// Pointer identity prevents a stale lease from touching a newly created
	// state for the same key.
	if current := lease.limiter.states[lease.keyID]; current != lease.state {
		return
	}
	if lease.state.active > 0 {
		lease.state.active--
	}
	if lease.state.active == 0 {
		delete(lease.limiter.states, lease.keyID)
	}
}

// Len reports the number of key states retained for currently active leases.
// It is primarily useful for observing idle-state cleanup.
func (limiter *ConcurrencyLimiter) Len() int {
	if limiter == nil {
		return 0
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	return len(limiter.states)
}
