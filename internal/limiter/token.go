package limiter

import (
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
// 03ab391865710462302bbcf52dca2f32682b91b5 (branch dev), specifically
// .references/bifrost/plugins/governance/store.go,
// .references/bifrost/plugins/governance/ratelimitreset_test.go, and
// .references/bifrost/plugins/governance/storeconcurrency_test.go. Those files
// were compared for mutable usage counters, reset behavior, and concurrent
// updates. Bifrost is Apache-2.0 under .references/bifrost/LICENSE; its
// .references/bifrost/THIRD_PARTY_NOTICES.md was also reviewed. No Bifrost
// source or dependency is adapted here: this limiter is an independent
// implementation because gateway admission must atomically reserve all fixed
// windows and retain bucket identities for later reconciliation, unlike
// Bifrost's mutable post-use governance counters.
type TokenLimiter struct {
	now     Clock
	mu      sync.Mutex
	buckets map[tokenBucketKey]tokenBucket
	// lastNow is guarded by mu. Holding time steady on a backwards clock move
	// prevents a new earlier bucket from replacing an active reservation.
	lastNow time.Time
}

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

// TokenReservation is an admitted estimate. T088 only supplies conservative
// active-reservation release; actual reconciliation belongs to T089. Release
// is useful for work proven not to have started and is idempotent.
type TokenReservation struct {
	limiter *TokenLimiter
	buckets []tokenBucketKey
	amount  int64
	once    sync.Once
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

// Release removes this reservation's active amount without committing usage.
// It is safe to call repeatedly and concurrently. T089 will add actual-usage
// and conservative-commit finalization against the same captured identities.
func (reservation *TokenReservation) Release() {
	if reservation == nil {
		return
	}
	reservation.once.Do(func() {
		if reservation.limiter == nil || reservation.amount <= 0 {
			return
		}
		limiter := reservation.limiter
		limiter.mu.Lock()
		defer limiter.mu.Unlock()
		for _, key := range reservation.buckets {
			bucket, exists := limiter.buckets[key]
			if !exists {
				continue
			}
			// A healthy bucket always has at least reservation.amount active.
			// Do not underflow if a future finalizer has already consumed it.
			if bucket.active < reservation.amount {
				bucket.active = 0
			} else {
				bucket.active -= reservation.amount
			}
			if bucket.active == 0 && bucket.committed == 0 {
				delete(limiter.buckets, key)
			} else {
				limiter.buckets[key] = bucket
			}
		}
		limiter.discardExpiredLocked(limiter.currentTimeLocked())
	})
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
