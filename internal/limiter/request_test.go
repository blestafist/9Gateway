package limiter

import (
	"sync"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/auth"
)

type testClock struct {
	mu  sync.RWMutex
	now time.Time
}

func (clock *testClock) Now() time.Time {
	clock.mu.RLock()
	defer clock.mu.RUnlock()
	return clock.now
}

func (clock *testClock) Set(now time.Time) {
	clock.mu.Lock()
	clock.now = now
	clock.mu.Unlock()
}

func TestRequestLimiterCapacityAndBoundary(t *testing.T) {
	clock := &testClock{now: time.Unix(17, 0).UTC()}
	limiter := NewRequestLimiter(clock.Now)
	windows := []auth.RequestWindow{{Amount: 2, Duration: time.Minute}}

	if allowed, reset := limiter.Allow("key-a", windows); !allowed || !reset.IsZero() {
		t.Fatalf("first request = %v, %v", allowed, reset)
	}
	if allowed, reset := limiter.Allow("key-a", windows); !allowed || !reset.IsZero() {
		t.Fatalf("exact capacity request = %v, %v", allowed, reset)
	}
	allowed, reset := limiter.Allow("key-a", windows)
	wantReset := FixedWindowStart(clock.Now(), time.Minute).Add(time.Minute)
	if allowed || !reset.Equal(wantReset) {
		t.Fatalf("one-over request = %v, %v; want false, %v", allowed, reset, wantReset)
	}

	clock.Set(wantReset)
	if allowed, reset := limiter.Allow("key-a", windows); !allowed || !reset.IsZero() {
		t.Fatalf("boundary request = %v, %v", allowed, reset)
	}
}

func TestRequestLimiterMultipleWindowsIsAtomicAndPerKey(t *testing.T) {
	clock := &testClock{now: time.Unix(10, 0).UTC()}
	limiter := NewRequestLimiter(clock.Now)
	windows := []auth.RequestWindow{
		{Amount: 2, Duration: time.Minute},
		{Amount: 1, Duration: time.Hour},
	}

	if allowed, _ := limiter.Allow("key-a", windows); !allowed {
		t.Fatal("first request rejected")
	}
	allowed, reset := limiter.Allow("key-a", windows)
	wantReset := FixedWindowStart(clock.Now(), time.Hour).Add(time.Hour)
	if allowed || !reset.Equal(wantReset) {
		t.Fatalf("second request = %v, %v; want false, %v", allowed, reset, wantReset)
	}
	// The failed attempt above must not consume the minute window.
	if limiter.counts[counterKey{keyID: "key-a", window: requestWindowKey{amount: 2, duration: time.Minute}}].count != 1 {
		t.Fatal("rejected multi-window attempt consumed minute capacity")
	}
	if got := limiter.Len(); got != 2 {
		t.Fatalf("counter state after rejection = %d, want 2", got)
	}
	if allowed, _ := limiter.Allow("key-b", windows); !allowed {
		t.Fatal("different key incorrectly shared capacity")
	}
}

func TestRequestLimiterClockMovementAndCleanup(t *testing.T) {
	clock := &testClock{now: time.Unix(1, 0).UTC()}
	limiter := NewRequestLimiter(clock.Now)
	windows := []auth.RequestWindow{{Amount: 1, Duration: time.Minute}}
	if allowed, _ := limiter.Allow("key-a", windows); !allowed {
		t.Fatal("initial request rejected")
	}
	if got := limiter.Len(); got != 1 {
		t.Fatalf("state length = %d, want 1", got)
	}

	clock.Set(time.Unix(61, 0).UTC())
	if got := limiter.Len(); got != 0 {
		t.Fatalf("expired state length = %d, want 0", got)
	}
	if allowed, _ := limiter.Allow("key-a", windows); !allowed {
		t.Fatal("request after clock movement rejected")
	}
}

func TestRequestLimiterConcurrentAttemptsDoNotOversubscribe(t *testing.T) {
	clock := &testClock{now: time.Unix(100, 0).UTC()}
	limiter := NewRequestLimiter(clock.Now)
	windows := []auth.RequestWindow{{Amount: 32, Duration: time.Minute}}
	const attempts = 256
	var wait sync.WaitGroup
	var admittedCount int
	var admittedMu sync.Mutex
	wait.Add(attempts)
	for index := 0; index < attempts; index++ {
		go func() {
			defer wait.Done()
			allowed, _ := limiter.Allow("key-a", windows)
			if allowed {
				admittedMu.Lock()
				admittedCount++
				admittedMu.Unlock()
			}
		}()
	}
	wait.Wait()
	if admittedCount != windows[0].Amount {
		t.Fatalf("admitted %d concurrent requests, want %d", admittedCount, windows[0].Amount)
	}
}
