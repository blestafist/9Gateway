package limiter

import (
	"errors"
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

func tokenWindowForTest() []auth.TokenWindow {
	return []auth.TokenWindow{{Amount: 100, Duration: time.Minute}}
}

func bucketForTest(t *testing.T, limiter *TokenLimiter, key string) tokenBucket {
	t.Helper()
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := limiter.currentTimeLocked()
	identity := tokenBucketKey{keyID: key, window: tokenWindowKey{amount: 100, duration: time.Minute}, start: FixedWindowStart(now, time.Minute)}
	return limiter.buckets[identity]
}

func TestTokenReservationCommitRefundExactAndDebt(t *testing.T) {
	clock := &testClock{now: time.Unix(60, 0).UTC()}
	limiter := NewTokenLimiter(clock.Now)
	refund, allowed, _ := limiter.Reserve("refund", tokenWindowForTest(), 40)
	if !allowed || refund.Commit(10) != nil {
		t.Fatal("refund commit failed")
	}
	if got := bucketForTest(t, limiter, "refund").committed; got != 10 {
		t.Fatalf("refund committed %d, want 10", got)
	}
	exact, allowed, _ := limiter.Reserve("exact", tokenWindowForTest(), 10)
	if !allowed || exact.Commit(10) != nil {
		t.Fatal("exact commit failed")
	}
	debt, allowed, _ := limiter.Reserve("debt", tokenWindowForTest(), 20)
	if !allowed || debt.Commit(130) != nil {
		t.Fatal("over-estimate commit should reconcile debt")
	}
	if _, allowed, _ := limiter.Reserve("debt", tokenWindowForTest(), 1); allowed {
		t.Fatal("committed debt did not block later admission")
	}
}

func TestTokenReservationAbortAndPreUpstreamRelease(t *testing.T) {
	clock := &testClock{now: time.Unix(60, 0).UTC()}
	limiter := NewTokenLimiter(clock.Now)
	abort, allowed, _ := limiter.Reserve("abort", tokenWindowForTest(), 40)
	if !allowed || abort.AbortConservative() != nil {
		t.Fatal("conservative abort failed")
	}
	if got := bucketForTest(t, limiter, "abort").committed; got != 40 {
		t.Fatalf("abort committed %d, want 40", got)
	}
	release, allowed, _ := limiter.Reserve("release", tokenWindowForTest(), 40)
	if !allowed || release.ReleaseBeforeUpstream() != nil {
		t.Fatal("pre-upstream release failed")
	}
	if got := limiter.Len(); got != 1 {
		t.Fatalf("zero release retained %d buckets, want only abort bucket", got)
	}
}

func TestTokenReservationDeferredAdjustmentAndDrop(t *testing.T) {
	clock := &testClock{now: time.Unix(60, 0).UTC()}
	limiter := NewTokenLimiter(clock.Now)
	lower, allowed, _ := limiter.Reserve("lower", tokenWindowForTest(), 40)
	if !allowed {
		t.Fatal("lower reservation rejected")
	}
	ticket, err := lower.CommitDeferred()
	if err != nil || ticket == nil || ticket.Adjust(10) != nil {
		t.Fatal("lower deferred adjustment failed")
	}
	if got := bucketForTest(t, limiter, "lower").committed; got != 10 {
		t.Fatalf("lower adjustment committed %d, want 10", got)
	}
	higher, allowed, _ := limiter.Reserve("higher", tokenWindowForTest(), 10)
	if !allowed {
		t.Fatal("higher reservation rejected")
	}
	higherTicket, err := higher.CommitDeferred()
	if err != nil || higherTicket == nil || higherTicket.Adjust(30) != nil {
		t.Fatal("higher deferred adjustment failed")
	}
	if got := bucketForTest(t, limiter, "higher").committed; got != 30 {
		t.Fatalf("higher adjustment committed %d, want 30", got)
	}
	dropped, allowed, _ := limiter.Reserve("dropped", tokenWindowForTest(), 20)
	if !allowed {
		t.Fatal("dropped reservation rejected")
	}
	droppedTicket, err := dropped.CommitDeferred()
	if err != nil || droppedTicket == nil {
		t.Fatal("dropped ticket creation failed")
	}
	if got := bucketForTest(t, limiter, "dropped").committed; got != 20 {
		t.Fatalf("dropped ticket charge %d, want 20", got)
	}
}

func TestTokenReservationFinalizationConcurrentAndOneShot(t *testing.T) {
	clock := &testClock{now: time.Unix(60, 0).UTC()}
	limiter := NewTokenLimiter(clock.Now)
	reservation, allowed, _ := limiter.Reserve("race", tokenWindowForTest(), 40)
	if !allowed {
		t.Fatal("race reservation rejected")
	}
	var wait sync.WaitGroup
	for i := 0; i < 32; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_ = reservation.Commit(10)
		}()
	}
	wait.Wait()
	if got := bucketForTest(t, limiter, "race").committed; got != 10 {
		t.Fatalf("concurrent commit charged %d, want 10", got)
	}
	if reservation.Commit(30) != nil {
		t.Fatal("repeated commit did not return first result")
	}

	invalid, allowed, _ := limiter.Reserve("invalid", tokenWindowForTest(), 10)
	if !allowed {
		t.Fatal("invalid reservation rejected")
	}
	if !errors.Is(invalid.Commit(-1), ErrInvalidTokenUsage) {
		t.Fatal("negative actual did not report invalid usage")
	}
	if got := bucketForTest(t, limiter, "invalid").committed; got != 10 {
		t.Fatalf("invalid usage did not conservatively charge, got %d", got)
	}
}

func TestTokenAdjustmentConcurrentCallsRemainOneShot(t *testing.T) {
	clock := &testClock{now: time.Unix(60, 0).UTC()}
	limiter := NewTokenLimiter(clock.Now)
	reservation, allowed, _ := limiter.Reserve("adjust-once", tokenWindowForTest(), 40)
	if !allowed {
		t.Fatal("reservation rejected")
	}
	ticket, err := reservation.CommitDeferred()
	if err != nil || ticket == nil {
		t.Fatalf("deferred commit = (%v, %v)", ticket, err)
	}
	if err := ticket.Adjust(10); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for i := 0; i < 32; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_ = ticket.Adjust(30)
		}()
	}
	wait.Wait()
	if got := bucketForTest(t, limiter, "adjust-once").committed; got != 10 {
		t.Fatalf("concurrent repeated adjustment changed charge to %d, want 10", got)
	}
}

func TestTokenReservationBoundaryAdjustmentCannotTouchNewBucket(t *testing.T) {
	clock := &testClock{now: time.Unix(59, 0).UTC()}
	limiter := NewTokenLimiter(clock.Now)
	reservation, allowed, _ := limiter.Reserve("boundary-adjust", tokenWindowForTest(), 40)
	if !allowed {
		t.Fatal("boundary reservation rejected")
	}
	ticket, err := reservation.CommitDeferred()
	if err != nil || ticket == nil {
		t.Fatal("boundary ticket failed")
	}
	clock.Set(time.Unix(60, 0).UTC())
	newReservation, allowed, _ := limiter.Reserve("boundary-adjust", tokenWindowForTest(), 100)
	if !allowed {
		t.Fatal("new boundary reservation rejected")
	}
	if ticket.Adjust(0) != nil {
		t.Fatal("old adjustment failed")
	}
	if bucket := bucketForTest(t, limiter, "boundary-adjust"); bucket.committed != 0 || bucket.active != 100 {
		t.Fatalf("old ticket touched new bucket, bucket %+v", bucket)
	}
	_ = newReservation.AbortConservative()
}

func TestTokenLimiterClockForwardRollbackAndRecovery(t *testing.T) {
	clock := &testClock{now: time.Unix(59, 0).UTC()}
	limiter := NewTokenLimiter(clock.Now)
	windows := tokenWindowForTest()
	initial, allowed, _ := limiter.Reserve("clock", windows, 100)
	if !allowed {
		t.Fatal("initial reservation rejected")
	}

	clock.Set(time.Unix(60, 0).UTC())
	boundary, allowed, _ := limiter.Reserve("clock", windows, 100)
	if !allowed {
		t.Fatal("boundary reservation rejected")
	}
	// A rollback must not make the limiter revisit the previous bucket or
	// discard the capacity admitted at the forward boundary.
	clock.Set(time.Unix(59, 0).UTC())
	if _, allowed, _ := limiter.Reserve("clock", windows, 1); allowed {
		t.Fatal("clock rollback reset active boundary capacity")
	}
	if err := boundary.ReleaseBeforeUpstream(); err != nil {
		t.Fatal(err)
	}
	if err := initial.ReleaseBeforeUpstream(); err != nil {
		t.Fatal(err)
	}

	clock.Set(time.Unix(120, 0).UTC())
	if recovered, allowed, _ := limiter.Reserve("clock", windows, 100); !allowed || recovered == nil {
		t.Fatalf("clock recovery reservation = (%v, %v), want admitted", recovered, allowed)
	} else {
		recovered.ReleaseBeforeUpstream()
	}
}
