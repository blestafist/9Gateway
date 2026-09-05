package limiter

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/pestit/9gateway/internal/auth"
)

// TokenWindow is the normalized token-window policy type.
type TokenWindow = auth.TokenWindow

// TokenLimiter reserves token estimates in epoch-aligned fixed windows. The
// key contains the stable gateway key ID, never the credential itself.
//
// Bifrost provenance review: reference commit
// 03ab391865710462302bbcf52dca2f32682b91b5 (branch dev). Usage accounting and
// finalization were compared in .references/bifrost/plugins/governance/tracker.go
// (terminal RequestID+AttemptNumber claim and partial-usage-on-cancellation
// lifecycle), .references/bifrost/plugins/governance/accounting_test.go
// (success/cancellation deduplication), and
// .references/bifrost/plugins/governance/storeconcurrency_test.go (atomic
// counter updates). Reset and rate-limit behavior were also inspected in
// .references/bifrost/plugins/governance/store.go and
// .references/bifrost/plugins/governance/ratelimitreset_test.go. Bifrost is
// Apache-2.0 under .references/bifrost/LICENSE; its
// .references/bifrost/THIRD_PARTY_NOTICES.md was reviewed. No Bifrost source
// or dependency is adapted here: this gateway needs admission-time
// reservation identities, conservative cancellation, a distinct pre-start
// release, and one-shot deferred adjustment. Those semantics are stricter and
// materially different from Bifrost's post-use asynchronous mutable counters,
// so this is an independent implementation.
type TokenLimiter struct {
	now     Clock
	mu      sync.Mutex
	buckets map[tokenBucketKey]tokenBucket
	// lastNow is guarded by mu. Holding time steady on a backwards clock move
	// prevents a new earlier bucket from replacing an active reservation.
	lastNow time.Time
}

var (
	// ErrInvalidTokenUsage means that a final usage value is negative.
	ErrInvalidTokenUsage = errors.New("invalid token usage")
	// ErrTokenAccountingOverflow means that the actual usage could not be
	// represented in a bucket. The reservation is still settled
	// conservatively, so this error never leaves active capacity behind.
	ErrTokenAccountingOverflow = errors.New("token accounting overflow")
	// ErrTokenReservationState means that a reservation's internal bucket was
	// not in the state established by admission. It is deliberately separate
	// from arithmetic errors so callers can report an internal accounting fault.
	ErrTokenReservationState = errors.New("invalid token reservation state")
)

type tokenWindowKey struct {
	amount   int64
	duration time.Duration
}

// tokenBucketKey is an exact identity. A reservation stores these values so a
// later reconciliation can address the admission buckets even after a window
// boundary has passed.
type tokenBucketKey struct {
	keyID  string
	window tokenWindowKey
	start  time.Time
}

type tokenBucket struct {
	committed int64
	active    int64
}

// TokenReservation is an admitted estimate. Its bucket identities are
// immutable: finalization always settles the windows that admitted the work,
// even if the clock has crossed a window boundary in the meantime.
type TokenReservation struct {
	limiter   *TokenLimiter
	buckets   []tokenBucketKey
	amount    int64
	finalMu   sync.Mutex
	finalized bool
	finalErr  error
	ticket    *TokenAdjustmentTicket
}

// TokenAdjustmentTicket is returned by CommitDeferred. It contains only the
// original admission bucket identities and can be consumed once. If it is
// dropped, the conservative reservation charge remains in place.
type TokenAdjustmentTicket struct {
	limiter  *TokenLimiter
	buckets  []tokenBucketKey
	reserved int64
	once     sync.Once
	finalErr error
}

// NewTokenLimiter creates an empty token limiter. A nil clock uses UTC wall
// time; callers and tests may inject a clock through the existing Clock type.
func NewTokenLimiter(now Clock) *TokenLimiter {
	return &TokenLimiter{now: now, buckets: make(map[tokenBucketKey]tokenBucket)}
}

// Reserve atomically reserves amount in every configured window. A nil
// reservation means rejection or invalid input; resetAt is the latest reset of
// the windows that currently prevent admission, and is zero when there is no
// useful reset (including an amount larger than a window's capacity). Empty
// windows are unlimited and return a harmless no-op reservation.
func (limiter *TokenLimiter) Reserve(keyID string, windows []TokenWindow, amount int64) (*TokenReservation, bool, time.Time) {
	if limiter == nil || amount <= 0 {
		return nil, false, time.Time{}
	}

	normalized, valid := normalizeTokenWindows(windows)
	if !valid {
		return nil, false, time.Time{}
	}
	if len(normalized) == 0 {
		return &TokenReservation{amount: amount}, true, time.Time{}
	}

	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := limiter.currentTimeLocked()
	if limiter.buckets == nil {
		limiter.buckets = make(map[tokenBucketKey]tokenBucket)
	}
	limiter.discardExpiredLocked(now)

	keys := make([]tokenBucketKey, 0, len(normalized))
	updated := make([]tokenBucket, 0, len(normalized))
	resetAt := time.Time{}
	rejected := false
	for _, window := range normalized {
		start, ok := fixedWindowStartChecked(now, window.duration)
		if !ok {
			return nil, false, time.Time{}
		}
		reset, ok := checkedWindowReset(start, window.duration)
		if !ok {
			return nil, false, time.Time{}
		}
		key := tokenBucketKey{keyID: keyID, window: window, start: start}
		bucket := limiter.buckets[key]
		used, ok := checkedNonNegativeAdd(bucket.committed, bucket.active)
		if !ok || amount > window.amount || used > window.amount || amount > window.amount-used {
			// An amount larger than the window can never become admissible, so
			// reporting its boundary would not be a useful retry time.
			// A committed overage, however, can become admissible at the next
			// reset and should still return that useful boundary.
			if amount <= window.amount && ok {
				if resetAt.IsZero() || reset.After(resetAt) {
					resetAt = reset
				}
			}
			rejected = true
		}
		keys = append(keys, key)
		active, activeOK := checkedNonNegativeAdd(bucket.active, amount)
		if !activeOK || active > window.amount {
			rejected = true
		}
		bucket.active = active
		updated = append(updated, bucket)
	}
	if rejected {
		return nil, false, resetAt
	}

	// The validation pass above has completed for every window before this
	// mutation pass starts, making multi-window admission atomic.
	for index, key := range keys {
		limiter.buckets[key] = updated[index]
	}
	return &TokenReservation{limiter: limiter, buckets: keys, amount: amount}, true, time.Time{}
}

// TryReserve is an explicit spelling of Reserve for callers that prefer a
// predicate-style method name.
func (limiter *TokenLimiter) TryReserve(keyID string, windows []TokenWindow, amount int64) (*TokenReservation, bool, time.Time) {
	return limiter.Reserve(keyID, windows, amount)
}

// Commit replaces the active reservation with actual total usage in every
// captured bucket. Actual usage may be lower or higher than the estimate; an
// over-estimate refund and an over-capacity debt are both intentional. Calls
// after the first (including concurrent calls) return the first result without
// changing accounting again.
func (reservation *TokenReservation) Commit(actual int64) error {
	if reservation == nil {
		return nil
	}
	return reservation.finalizeOnce(actual, true)
}

// AbortConservative settles an ambiguous request with its reserved estimate.
// It is safe to call repeatedly and concurrently.
func (reservation *TokenReservation) AbortConservative() error {
	if reservation == nil {
		return nil
	}
	return reservation.finalizeOnce(reservation.amount, false)
}

// ReleaseBeforeUpstream releases a reservation for work proven never to have
// started upstream. It is distinct from AbortConservative: zero usage is
// committed rather than the estimate.
func (reservation *TokenReservation) ReleaseBeforeUpstream() error {
	if reservation == nil {
		return nil
	}
	return reservation.finalizeOnce(0, false)
}

// Release retains the T088 API and means the pre-upstream zero-usage release.
// Callers that may have started upstream work must use AbortConservative.
func (reservation *TokenReservation) Release() {
	_ = reservation.ReleaseBeforeUpstream()
}

// CommitDeferred conservatively settles the reservation and returns a ticket
// for a later observed total. The ticket is nil only for a nil reservation.
func (reservation *TokenReservation) CommitDeferred() (*TokenAdjustmentTicket, error) {
	if reservation == nil {
		return nil, nil
	}
	reservation.finalMu.Lock()
	defer reservation.finalMu.Unlock()
	if reservation.finalized {
		return reservation.ticket, reservation.finalErr
	}
	err := reservation.finalize(reservation.amount, false)
	reservation.finalErr = err
	reservation.finalized = true
	reservation.ticket = &TokenAdjustmentTicket{
		limiter:  reservation.limiter,
		buckets:  append([]tokenBucketKey(nil), reservation.buckets...),
		reserved: reservation.amount,
	}
	return reservation.ticket, err
}

// Defer is a concise alias for CommitDeferred.
func (reservation *TokenReservation) Defer() (*TokenAdjustmentTicket, error) {
	return reservation.CommitDeferred()
}

// ReleaseNoUpstream is an explicit alias for ReleaseBeforeUpstream.
func (reservation *TokenReservation) ReleaseNoUpstream() error {
	return reservation.ReleaseBeforeUpstream()
}

func (reservation *TokenReservation) finalizeOnce(usage int64, actual bool) error {
	reservation.finalMu.Lock()
	defer reservation.finalMu.Unlock()
	if !reservation.finalized {
		reservation.finalErr = reservation.finalize(usage, actual)
		reservation.finalized = true
	}
	return reservation.finalErr
}

func (reservation *TokenReservation) finalize(usage int64, actual bool) error {
	var result error
	if usage < 0 {
		// An invalid result must still settle safely. The conservative amount is
		// the only valid charge available to us.
		usage = reservation.amount
		actual = false
		result = ErrInvalidTokenUsage
	}
	if reservation.limiter == nil || len(reservation.buckets) == 0 || reservation.amount <= 0 {
		return result
	}

	limiter := reservation.limiter
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	// Validate every captured bucket before mutating any of them. A result
	// overflow falls back to a conservative settlement, which is representable
	// for any admitted reservation and guarantees active removal.
	settle := usage
	if actual && result == nil {
		for _, key := range reservation.buckets {
			bucket, exists := limiter.buckets[key]
			if !exists || bucket.active < reservation.amount {
				result = ErrTokenReservationState
				break
			}
			if _, ok := checkedNonNegativeAdd(bucket.committed, usage); !ok {
				settle = reservation.amount
				result = ErrTokenAccountingOverflow
				break
			}
		}
	}
	if settle < 0 {
		settle = reservation.amount
	}
	if result != nil && settle == usage {
		settle = reservation.amount
	}

	// Even a corrupted/missing bucket must not retain an active reservation.
	// Missing buckets are not recreated: this is important for deferred tickets
	// crossing a reset, which must never touch a newer bucket.
	for _, key := range reservation.buckets {
		bucket, exists := limiter.buckets[key]
		if !exists {
			continue
		}
		if bucket.active < reservation.amount {
			bucket.active = 0
		} else {
			bucket.active -= reservation.amount
		}
		committed, ok := checkedNonNegativeAdd(bucket.committed, settle)
		if !ok {
			// This can only occur for a malformed pre-existing bucket after the
			// validation fallback. Keep its existing charge and still release.
			result = ErrTokenAccountingOverflow
		} else {
			bucket.committed = committed
		}
		if bucket.active == 0 && bucket.committed == 0 {
			delete(limiter.buckets, key)
		} else {
			limiter.buckets[key] = bucket
		}
	}
	limiter.discardExpiredLocked(limiter.currentTimeLocked())
	return result
}

// Adjust replaces the conservative charge with actual usage. It never
// recreates an expired bucket and never affects a bucket created after
// admission. A failed adjustment leaves the conservative charge in place.
func (ticket *TokenAdjustmentTicket) Adjust(actual int64) error {
	if ticket == nil {
		return nil
	}
	ticket.once.Do(func() {
		if actual < 0 {
			ticket.finalErr = ErrInvalidTokenUsage
			return
		}
		if ticket.limiter == nil || len(ticket.buckets) == 0 {
			return
		}
		limiter := ticket.limiter
		limiter.mu.Lock()
		defer limiter.mu.Unlock()
		var delta int64
		if actual >= ticket.reserved {
			increase := actual - ticket.reserved
			if increase < 0 { // defensive: both operands are non-negative.
				ticket.finalErr = ErrTokenAccountingOverflow
				return
			}
			delta = increase
		} else {
			delta = actual - ticket.reserved
		}
		if delta > 0 {
			for _, key := range ticket.buckets {
				bucket, exists := limiter.buckets[key]
				if !exists {
					continue
				}
				if _, ok := checkedNonNegativeAdd(bucket.committed, delta); !ok {
					ticket.finalErr = ErrTokenAccountingOverflow
					return
				}
			}
		} else if delta < 0 {
			refund := ticket.reserved - actual
			for _, key := range ticket.buckets {
				bucket, exists := limiter.buckets[key]
				if exists && bucket.committed < refund {
					ticket.finalErr = ErrTokenReservationState
					return
				}
			}
		}
		for _, key := range ticket.buckets {
			bucket, exists := limiter.buckets[key]
			if !exists {
				continue
			}
			if delta > 0 {
				bucket.committed += delta
			} else if delta < 0 {
				bucket.committed += delta
			}
			if bucket.active == 0 && bucket.committed == 0 {
				delete(limiter.buckets, key)
			} else {
				limiter.buckets[key] = bucket
			}
		}
		limiter.discardExpiredLocked(limiter.currentTimeLocked())
	})
	return ticket.finalErr
}

// Commit is an alias for Adjust on a deferred ticket.
func (ticket *TokenAdjustmentTicket) Commit(actual int64) error {
	return ticket.Adjust(actual)
}

// Apply is an alias for Adjust for callers that prefer settlement terminology.
func (ticket *TokenAdjustmentTicket) Apply(actual int64) error {
	return ticket.Adjust(actual)
}

// Amount reports the exact estimate retained by this reservation.
func (reservation *TokenReservation) Amount() int64 {
	if reservation == nil {
		return 0
	}
	return reservation.amount
}

// Len reports retained buckets after lazy cleanup. Expired buckets with an
// active reservation remain until that reservation is released/finalized.
func (limiter *TokenLimiter) Len() int {
	if limiter == nil {
		return 0
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	limiter.discardExpiredLocked(limiter.currentTimeLocked())
	return len(limiter.buckets)
}

func (limiter *TokenLimiter) currentTimeLocked() time.Time {
	var now time.Time
	if limiter.now == nil {
		now = time.Now().UTC()
	} else {
		now = limiter.now().UTC()
	}
	if !limiter.lastNow.IsZero() && now.Before(limiter.lastNow) {
		return limiter.lastNow
	}
	limiter.lastNow = now
	return now
}

func (limiter *TokenLimiter) discardExpiredLocked(now time.Time) {
	for key, bucket := range limiter.buckets {
		reset, ok := checkedWindowReset(key.start, key.window.duration)
		if ok && !reset.After(now) && bucket.active == 0 {
			delete(limiter.buckets, key)
		}
	}
}

func normalizeTokenWindows(windows []TokenWindow) ([]tokenWindowKey, bool) {
	if len(windows) == 0 {
		return nil, true
	}
	result := make([]tokenWindowKey, 0, len(windows))
	seen := make(map[tokenWindowKey]struct{}, len(windows))
	for _, window := range windows {
		if window.Amount <= 0 || window.Duration <= 0 {
			return nil, false
		}
		key := tokenWindowKey{amount: window.Amount, duration: window.Duration}
		if _, exists := seen[key]; exists {
			return nil, false
		}
		seen[key] = struct{}{}
		result = append(result, key)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].duration != result[right].duration {
			return result[left].duration < result[right].duration
		}
		return result[left].amount < result[right].amount
	})
	return result, true
}

func checkedNonNegativeAdd(left, right int64) (int64, bool) {
	if left < 0 || right < 0 || left > maxInt64-right {
		return 0, false
	}
	return left + right, true
}

const (
	maxInt64 = int64(^uint64(0) >> 1)
	minInt64 = -maxInt64 - 1
)

// fixedWindowStartChecked performs floor division from Unix epoch without
// relying on UnixNano for dates outside its documented range. Real clocks are
// comfortably inside this range, while malformed injected times fail closed.
func fixedWindowStartChecked(now time.Time, duration time.Duration) (time.Time, bool) {
	if duration <= 0 {
		return time.Time{}, false
	}
	seconds := now.Unix()
	nanoseconds := int64(now.Nanosecond())
	if seconds > (maxInt64-nanoseconds)/int64(time.Second) || seconds < (minInt64-nanoseconds)/int64(time.Second) {
		return time.Time{}, false
	}
	nanos := seconds*int64(time.Second) + nanoseconds
	if nanos == minInt64 {
		return time.Time{}, false
	}
	width := int64(duration)
	remainder := nanos % width
	if remainder < 0 {
		remainder += width
	}
	start, ok := checkedSignedAdd(nanos, -remainder)
	if !ok {
		return time.Time{}, false
	}
	return time.Unix(0, start).UTC(), true
}

func checkedWindowReset(start time.Time, duration time.Duration) (time.Time, bool) {
	nanos := start.UnixNano()
	added, ok := checkedSignedAdd(nanos, int64(duration))
	if !ok {
		return time.Time{}, false
	}
	return time.Unix(0, added).UTC(), true
}

func checkedSignedAdd(left, right int64) (int64, bool) {
	if right > 0 && left > maxInt64-right {
		return 0, false
	}
	if right < 0 && left < minInt64-right {
		return 0, false
	}
	return left + right, true
}
