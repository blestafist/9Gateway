// Package limiter contains in-memory policy limiters.
package limiter

import (
	"sort"
	"sync"
	"time"

	"github.com/pestit/9gateway/internal/auth"
)

// Clock is the time source used by a RequestLimiter. A nil clock uses UTC
// wall-clock time. It is an alias of auth.Clock so policy and runtime limiting
// can use the same injectable time source.
type Clock = auth.Clock

// RequestWindow is the normalized request-count policy window.
type RequestWindow = auth.RequestWindow

// RequestLimiter enforces fixed request windows in one process. Counters are
// keyed by the stable key ID and the complete normalized window, so keys never
// share capacity.
type RequestLimiter struct {
	now    Clock
	mu     sync.Mutex
	counts map[counterKey]windowCounter
}

type counterKey struct {
	keyID  string
	window requestWindowKey
}

type requestWindowKey struct {
	amount   int
	duration time.Duration
}

type windowCounter struct {
	start time.Time
	count int
}

// NewRequestLimiter creates an empty fixed-window request limiter.
func NewRequestLimiter(now Clock) *RequestLimiter {
	limiter := &RequestLimiter{now: now, counts: make(map[counterKey]windowCounter)}
	return limiter
}

// NewLimiter is a concise constructor alias.
func NewLimiter(now Clock) *RequestLimiter {
	return NewRequestLimiter(now)
}

// Allow atomically checks every configured window and consumes one request
// from all of them only when every window admits it. On rejection, resetAt is
// the earliest time at which all exhausted windows admit the request. It is
// zero when the request is admitted or when the input contains no valid
// windows.
func (limiter *RequestLimiter) Allow(keyID string, windows []RequestWindow) (allowed bool, resetAt time.Time) {
	if limiter == nil {
		return false, time.Time{}
	}

	now := limiter.currentTime()
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.counts == nil {
		limiter.counts = make(map[counterKey]windowCounter)
	}

	// Cleanup is intentionally performed on the request path. This avoids a
	// background goroutine while ensuring an inactive key's state is removed
	// after its last window expires.
	limiter.discardExpiredLocked(now)

	normalized := normalizeWindows(windows)
	if len(normalized) != len(windows) {
		// Policies reject invalid windows before reaching the limiter. Treating
		// one defensively as non-admissible avoids accidentally granting an
		// invalid limit and leaves state untouched.
		return false, time.Time{}
	}
	if len(normalized) == 0 {
		return true, time.Time{}
	}

	type currentWindow struct {
		key   counterKey
		start time.Time
		reset time.Time
		count int
	}
	current := make([]currentWindow, len(normalized))
	for index, window := range normalized {
		start := FixedWindowStart(now, window.duration)
		key := counterKey{keyID: keyID, window: window}
		counter, exists := limiter.counts[key]
		count := 0
		if exists && counter.start.Equal(start) {
			count = counter.count
		}
		current[index] = currentWindow{
			key:   key,
			start: start,
			reset: start.Add(window.duration),
			count: count,
		}
	}

	for _, window := range current {
		if window.count >= window.key.window.amount {
			// Every exhausted window is a constraint. The first reset that is
			// useful is therefore the latest of their reset times; an earlier
			// reset would still leave another window full.
			if resetAt.IsZero() || window.reset.After(resetAt) {
				resetAt = window.reset
			}
		}
	}
	if !resetAt.IsZero() {
		return false, resetAt
	}

	for _, window := range current {
		limiter.counts[window.key] = windowCounter{start: window.start, count: window.count + 1}
	}
	return true, time.Time{}
}

// Check is the descriptive spelling of Allow.
func (limiter *RequestLimiter) Check(keyID string, windows []RequestWindow) (bool, time.Time) {
	return limiter.Allow(keyID, windows)
}

// RetryAfterSeconds returns the positive, rounded-up number of seconds from
// the limiter's clock until resetAt. A reset that is already due still needs a
// positive header value because HTTP Retry-After is a delay, not a timestamp.
func (limiter *RequestLimiter) RetryAfterSeconds(resetAt time.Time) int {
	if limiter == nil || resetAt.IsZero() {
		return 0
	}
	return RetryAfterSecondsAt(limiter.currentTime(), resetAt)
}

// RetryAfterSecondsAt converts a reset time into a positive delta-seconds
// value, rounding fractional seconds up.
func RetryAfterSecondsAt(now, resetAt time.Time) int {
	delta := resetAt.Sub(now)
	if delta <= 0 {
		return 1
	}
	return int((delta-1)/time.Second) + 1
}

// FixedWindowStart returns the UTC, Unix-epoch-aligned start of the fixed
// window containing now. Every duration therefore has explicit boundaries at
// integer multiples of that duration from 1970-01-01T00:00:00Z.
func FixedWindowStart(now time.Time, duration time.Duration) time.Time {
	if duration <= 0 {
		return time.Time{}
	}
	nanos := now.UnixNano()
	width := int64(duration)
	remainder := nanos % width
	if remainder < 0 {
		remainder += width
	}
	return time.Unix(0, nanos-remainder).UTC()
}

// Len reports the number of retained key/window counters. It is primarily
// useful for observing lazy cleanup and does not expose counter contents.
func (limiter *RequestLimiter) Len() int {
	if limiter == nil {
		return 0
	}
	now := limiter.currentTime()
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	limiter.discardExpiredLocked(now)
	return len(limiter.counts)
}

func (limiter *RequestLimiter) currentTime() time.Time {
	if limiter.now == nil {
		return time.Now().UTC()
	}
	return limiter.now().UTC()
}

func (limiter *RequestLimiter) discardExpiredLocked(now time.Time) {
	for key, counter := range limiter.counts {
		if !counter.start.Add(key.window.duration).After(now) {
			delete(limiter.counts, key)
		}
	}
}

func normalizeWindows(windows []RequestWindow) []requestWindowKey {
	if len(windows) == 0 {
		return nil
	}
	result := make([]requestWindowKey, 0, len(windows))
	seen := make(map[requestWindowKey]struct{}, len(windows))
	for _, window := range windows {
		if window.Amount <= 0 || window.Duration <= 0 {
			return nil
		}
		normalized := requestWindowKey{amount: window.Amount, duration: window.Duration}
		if _, exists := seen[normalized]; exists {
			return nil
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].duration != result[right].duration {
			return result[left].duration < result[right].duration
		}
		return result[left].amount < result[right].amount
	})
	return result
}
