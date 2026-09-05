package limiter

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestConcurrencyLimiterUnlimited(t *testing.T) {
	limiter := NewConcurrencyLimiter()
	leases := make([]*Lease, 128)
	for index := range leases {
		var ok bool
		leases[index], ok = limiter.Acquire("key-a", 0)
		if !ok || leases[index] == nil {
			t.Fatalf("unlimited acquisition %d = (%v, %v)", index, leases[index], ok)
		}
	}
	if got := limiter.Len(); got != 0 {
		t.Fatalf("unlimited state length = %d, want 0", got)
	}
	for _, lease := range leases {
		lease.Release()
	}
}

func TestConcurrencyLimiterLimitsAndSeparateKeys(t *testing.T) {
	limiter := NewConcurrencyLimiter()
	first, ok := limiter.Acquire("key-a", 1)
	if !ok || first == nil {
		t.Fatal("limit-one first acquisition rejected")
	}
	if second, admitted := limiter.Acquire("key-a", 1); admitted || second != nil {
		t.Fatal("limit-one saturation admitted a second lease")
	}
	if other, admitted := limiter.Acquire("key-b", 2); !admitted || other == nil {
		t.Fatal("different key did not have independent capacity")
	} else {
		third, thirdAdmitted := limiter.Acquire("key-b", 2)
		if !thirdAdmitted || third == nil {
			t.Fatal("limit-many second lease rejected")
		}
		if fourth, fourthAdmitted := limiter.Acquire("key-b", 2); fourthAdmitted || fourth != nil {
			t.Fatal("limit-many saturation admitted an extra lease")
		}
		third.Release()
		other.Release()
	}
	if got := limiter.Len(); got != 1 {
		t.Fatalf("state length with key-a active = %d, want 1", got)
	}
	first.Release()
	if got := limiter.Len(); got != 0 {
		t.Fatalf("state length after final release = %d, want 0", got)
	}
}

func TestConcurrencyLeaseImmediateReuseAndIdempotentRelease(t *testing.T) {
	limiter := NewConcurrencyLimiter()
	lease, ok := limiter.Acquire("key-a", 1)
	if !ok {
		t.Fatal("initial acquisition rejected")
	}
	lease.Release()
	lease.Release()
	lease.Release()

	reused, ok := limiter.Acquire("key-a", 1)
	if !ok || reused == nil {
		t.Fatal("slot was not immediately reusable")
	}
	if got := limiter.states["key-a"].active; got != 1 {
		t.Fatalf("active after reuse = %d, want 1", got)
	}
	reused.Release()
	if got := limiter.Len(); got != 0 {
		t.Fatalf("state after reuse release = %d, want 0", got)
	}
}

func TestConcurrencyLeaseConcurrentRelease(t *testing.T) {
	limiter := NewConcurrencyLimiter()
	lease, ok := limiter.Acquire("key-a", 1)
	if !ok {
		t.Fatal("initial acquisition rejected")
	}
	const releases = 128
	var wait sync.WaitGroup
	wait.Add(releases)
	for index := 0; index < releases; index++ {
		go func() {
			defer wait.Done()
			lease.Release()
		}()
	}
	wait.Wait()
	if got := limiter.Len(); got != 0 {
		t.Fatalf("state after concurrent release = %d, want 0", got)
	}
	if next, admitted := limiter.Acquire("key-a", 1); !admitted || next == nil {
		t.Fatal("slot not reusable after concurrent release")
	} else {
		next.Release()
	}
}

func TestConcurrencyLimiterAcquireReleaseRace(t *testing.T) {
	limiter := NewConcurrencyLimiter()
	const workers = 32
	const rounds = 100
	var wait sync.WaitGroup
	var admitted atomic.Int64
	wait.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer wait.Done()
			for round := 0; round < rounds; round++ {
				lease, ok := limiter.Acquire("key-a", 1)
				if !ok {
					continue
				}
				admitted.Add(1)
				lease.Release()
			}
		}()
	}
	wait.Wait()
	if admitted.Load() == 0 {
		t.Fatal("acquire/release race admitted no work")
	}
	if got := limiter.Len(); got != 0 {
		t.Fatalf("state after acquire/release race = %d, want 0", got)
	}
}

func TestConcurrencyLimiterRejectsInvalidMaximumWithoutState(t *testing.T) {
	limiter := NewConcurrencyLimiter()
	if lease, ok := limiter.Acquire("key-a", -1); ok || lease != nil {
		t.Fatal("negative maximum was admitted")
	}
	if got := limiter.Len(); got != 0 {
		t.Fatalf("state after invalid acquisition = %d, want 0", got)
	}
}
