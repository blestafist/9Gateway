package limiter

import (
	"sync"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/auth"
)

func TestTokenLimiterExactCapacityAndRelease(t *testing.T) {
	clock := &testClock{now: time.Unix(17, 0).UTC()}
	limiter := NewTokenLimiter(clock.Now)
	windows := []auth.TokenWindow{{Amount: 10, Duration: time.Minute}}

	reservation, allowed, reset := limiter.Reserve("key-a", windows, 10)
	if !allowed || reservation == nil || !reset.IsZero() {
		t.Fatalf("exact capacity = reservation %v, allowed %v, reset %v", reservation, allowed, reset)
	}
	if _, allowed, _ := limiter.Reserve("key-a", windows, 1); allowed {
		t.Fatal("one-over reservation admitted")
	}
	reservation.Release()
	if _, allowed, _ := limiter.Reserve("key-a", windows, 10); !allowed {
		t.Fatal("released reservation did not restore capacity")
	}
}

func TestTokenLimiterMultipleWindowsAtomicAndPerKey(t *testing.T) {
	clock := &testClock{now: time.Unix(10, 0).UTC()}
	limiter := NewTokenLimiter(clock.Now)
	windows := []auth.TokenWindow{{Amount: 20, Duration: time.Minute}, {Amount: 10, Duration: time.Hour}}

	first, allowed, _ := limiter.Reserve("key-a", windows, 10)
	if !allowed {
		t.Fatal("first reservation rejected")
	}
	_, allowed, reset := limiter.Reserve("key-a", windows, 1)
	wantReset := FixedWindowStart(clock.Now(), time.Hour).Add(time.Hour)
	if allowed || !reset.Equal(wantReset) {
		t.Fatalf("second reservation = %v, %v; want false, %v", allowed, reset, wantReset)
	}
	// The rejected attempt must not consume the otherwise available hourly
	// amount or create another bucket.
	if got := limiter.Len(); got != 2 {
		t.Fatalf("bucket count after rejection = %d, want 2", got)
	}
	if other, admitted, _ := limiter.Reserve("key-b", windows, 10); !admitted || other == nil {
		t.Fatal("different key did not have independent capacity")
	}
	first.Release()
}

func TestTokenLimiterBoundaryResetAndLongReservationIdentity(t *testing.T) {
	clock := &testClock{now: time.Unix(59, 0).UTC()}
	limiter := NewTokenLimiter(clock.Now)
	windows := []auth.TokenWindow{{Amount: 1, Duration: time.Minute}}
	reservation, allowed, _ := limiter.Reserve("key", windows, 1)
	if !allowed {
		t.Fatal("initial reservation rejected")
	}
	clock.Set(time.Unix(60, 0).UTC())
	if _, allowed, _ := limiter.Reserve("key", windows, 1); !allowed {
		t.Fatal("new boundary bucket rejected")
	}
	// The old bucket is retained despite expiry because the reservation is
	// active. Releasing it then permits lazy removal of both old and active
	// state as appropriate.
	if got := limiter.Len(); got != 2 {
		t.Fatalf("cross-boundary bucket count = %d, want 2", got)
	}
	reservation.Release()
	if got := limiter.Len(); got != 1 {
		t.Fatalf("released old bucket count = %d, want 1", got)
	}
}

func TestTokenLimiterRejectsInvalidAndDuplicateWindowsWithoutState(t *testing.T) {
	clock := &testClock{now: time.Unix(1, 0).UTC()}
	limiter := NewTokenLimiter(clock.Now)
	window := auth.TokenWindow{Amount: 1, Duration: time.Minute}
	for _, windows := range [][]auth.TokenWindow{
		{{Amount: 0, Duration: time.Minute}},
		{window, window},
	} {
		if reservation, allowed, _ := limiter.Reserve("key", windows, 1); reservation != nil || allowed {
			t.Fatal("invalid windows were admitted")
		}
	}
	if got := limiter.Len(); got != 0 {
		t.Fatalf("invalid input retained %d buckets", got)
	}
	if reservation, allowed, _ := limiter.Reserve("key", nil, 1); !allowed || reservation == nil {
		t.Fatal("empty windows should be unlimited")
	}
}

func TestTokenLimiterConcurrentReservationsDoNotOversubscribe(t *testing.T) {
	clock := &testClock{now: time.Unix(100, 0).UTC()}
	limiter := NewTokenLimiter(clock.Now)
	windows := []auth.TokenWindow{{Amount: 32, Duration: time.Minute}}
	const attempts = 256
	var wait sync.WaitGroup
	var mu sync.Mutex
	admitted := 0
	reservations := make([]*TokenReservation, 0, attempts)
	wait.Add(attempts)
	for index := 0; index < attempts; index++ {
		go func() {
			defer wait.Done()
			reservation, allowed, _ := limiter.Reserve("key", windows, 1)
			if allowed {
				mu.Lock()
				admitted++
				reservations = append(reservations, reservation)
				mu.Unlock()
			}
		}()
	}
	wait.Wait()
	if admitted != 32 {
		t.Fatalf("admitted %d reservations, want 32", admitted)
	}
	for _, reservation := range reservations {
		reservation.Release()
	}
}

func TestTokenLimiterOverflowAndAmountTooLarge(t *testing.T) {
	clock := &testClock{now: time.Unix(1, 0).UTC()}
	limiter := NewTokenLimiter(clock.Now)
	window := []auth.TokenWindow{{Amount: 10, Duration: time.Minute}}
	if reservation, allowed, reset := limiter.Reserve("key", window, 11); reservation != nil || allowed || !reset.IsZero() {
		t.Fatalf("oversized reservation = %v, %v, %v", reservation, allowed, reset)
	}
	// Amount arithmetic must reject an estimate that would overflow rather
	// than wrapping to a small positive number.
	if reservation, allowed, _ := limiter.Reserve("key", []auth.TokenWindow{{Amount: maxInt64, Duration: time.Minute}}, maxInt64); !allowed || reservation == nil {
		t.Fatal("maximum representable amount should fit an empty bucket")
	}
	if _, allowed, _ := limiter.Reserve("key-2", []auth.TokenWindow{{Amount: maxInt64, Duration: time.Minute}}, maxInt64); !allowed {
		t.Fatal("maximum amount should remain independently admissible")
	}
	// Simulate a future reconciler's committed state at the arithmetic edge.
	// The active amount must not wrap when committed+active is checked.
	key := tokenBucketKey{keyID: "overflow", window: tokenWindowKey{amount: maxInt64, duration: time.Minute}, start: FixedWindowStart(clock.Now(), time.Minute)}
	limiter.mu.Lock()
	limiter.buckets[key] = tokenBucket{committed: maxInt64, active: 1}
	limiter.mu.Unlock()
	if _, allowed, _ := limiter.Reserve("overflow", []auth.TokenWindow{{Amount: maxInt64, Duration: time.Minute}}, 1); allowed {
		t.Fatal("overflowed committed plus active state was admitted")
	}
}
