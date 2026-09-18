package analytics

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCoordinatorConcurrencyLimit(t *testing.T) {
	coord := NewCoordinator[string](2, 32, 15*time.Second)

	started1 := make(chan struct{})
	started2 := make(chan struct{})
	unblock := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_, _ = coord.Do(context.Background(), "k1", func(ctx context.Context) (string, error) {
			close(started1)
			<-unblock
			return "v1", nil
		})
	}()

	go func() {
		defer wg.Done()
		_, _ = coord.Do(context.Background(), "k2", func(ctx context.Context) (string, error) {
			close(started2)
			<-unblock
			return "v2", nil
		})
	}()

	<-started1
	<-started2

	// Capacity is 2; third query must fail immediately with ErrCapacityExceeded
	_, err := coord.Do(context.Background(), "k3", func(ctx context.Context) (string, error) {
		return "v3", nil
	})
	if !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("expected ErrCapacityExceeded, got: %v", err)
	}

	close(unblock)
	wg.Wait()

	// After completion, active count is 0 and new queries succeed
	res, err := coord.Do(context.Background(), "k3", func(ctx context.Context) (string, error) {
		return "v3", nil
	})
	if err != nil || res != "v3" {
		t.Fatalf("expected v3, got: %v, %v", res, err)
	}
}

func TestCoordinatorSingleflight(t *testing.T) {
	coord := NewCoordinator[string](2, 32, 15*time.Second)

	started := make(chan struct{})
	unblock := make(chan struct{})
	var executions atomic.Int32

	var wg sync.WaitGroup
	wg.Add(3)

	results := make([]string, 3)
	errs := make([]error, 3)

	for i := 0; i < 3; i++ {
		idx := i
		go func() {
			defer wg.Done()
			results[idx], errs[idx] = coord.Do(context.Background(), "same-key", func(ctx context.Context) (string, error) {
				executions.Add(1)
				close(started)
				<-unblock
				return "shared-val", nil
			})
		}()
	}

	<-started
	close(unblock)
	wg.Wait()

	if executions.Load() != 1 {
		t.Fatalf("expected exactly 1 execution, got %d", executions.Load())
	}
	for i := 0; i < 3; i++ {
		if errs[i] != nil || results[i] != "shared-val" {
			t.Fatalf("result[%d] = %q, %v; want 'shared-val'", i, results[i], errs[i])
		}
	}
}

func TestCoordinatorCacheAndEviction(t *testing.T) {
	maxCache := 3
	ttl := 100 * time.Millisecond
	coord := NewCoordinator[string](2, maxCache, ttl)

	// Populate cache
	for i := 1; i <= 3; i++ {
		k := fmt.Sprintf("k%d", i)
		v := fmt.Sprintf("v%d", i)
		res, err := coord.Do(context.Background(), k, func(ctx context.Context) (string, error) {
			return v, nil
		})
		if err != nil || res != v {
			t.Fatalf("populate error: %v", err)
		}
	}

	if coord.CacheLen() != 3 {
		t.Fatalf("cache len = %d, want 3", coord.CacheLen())
	}

	// Read from cache (fn should not be called)
	called := false
	res, err := coord.Do(context.Background(), "k1", func(ctx context.Context) (string, error) {
		called = true
		return "different", nil
	})
	if err != nil || res != "v1" || called {
		t.Fatalf("cache hit failed: res=%q, called=%v", res, called)
	}

	// Insert 4th element: k2 should be evicted (k1 was updated to end of order)
	_, _ = coord.Do(context.Background(), "k4", func(ctx context.Context) (string, error) {
		return "v4", nil
	})

	if coord.CacheLen() != 3 {
		t.Fatalf("cache len after eviction = %d, want 3", coord.CacheLen())
	}

	// Test TTL expiration
	time.Sleep(150 * time.Millisecond)
	calledAfterExpiry := false
	_, _ = coord.Do(context.Background(), "k4", func(ctx context.Context) (string, error) {
		calledAfterExpiry = true
		return "v4-new", nil
	})
	if !calledAfterExpiry {
		t.Fatalf("expected fn to be called after TTL expiry")
	}
}

func TestCoordinatorCancellation(t *testing.T) {
	coord := NewCoordinator[string](2, 32, 15*time.Second)

	started := make(chan struct{})
	leaderCtx, leaderCancel := context.WithCancel(context.Background())

	go func() {
		_, _ = coord.Do(leaderCtx, "k-cancel", func(ctx context.Context) (string, error) {
			close(started)
			<-ctx.Done()
			return "", ctx.Err()
		})
	}()

	<-started
	// Cancel leader
	leaderCancel()

	// Wait for slot to be freed
	deadline := time.Now().Add(1 * time.Second)
	for coord.ActiveCount() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if coord.ActiveCount() != 0 {
		t.Fatalf("expected active count = 0 after cancellation, got %d", coord.ActiveCount())
	}
}

func TestSharedGateAcrossCoordinators(t *testing.T) {
	gate := NewGate(2)
	coordA := NewCoordinatorWithGate[string](gate, 32, 15*time.Second)
	coordB := NewCoordinatorWithGate[int](gate, 32, 15*time.Second)

	started1 := make(chan struct{})
	started2 := make(chan struct{})
	unblock := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_, _ = coordA.Do(context.Background(), "a1", func(ctx context.Context) (string, error) {
			close(started1)
			<-unblock
			return "valA", nil
		})
	}()

	go func() {
		defer wg.Done()
		_, _ = coordB.Do(context.Background(), "b1", func(ctx context.Context) (int, error) {
			close(started2)
			<-unblock
			return 42, nil
		})
	}()

	<-started1
	<-started2

	if gate.Active() != 2 {
		t.Fatalf("expected 2 active in shared gate, got %d", gate.Active())
	}
	if coordA.ActiveCount() != 2 || coordB.ActiveCount() != 2 {
		t.Fatalf("coordinators should reflect shared gate active count 2, got A=%d, B=%d",
			coordA.ActiveCount(), coordB.ActiveCount())
	}

	// Saturated gate: third query to coordA or coordB must immediately fail
	_, errA := coordA.Do(context.Background(), "a2", func(ctx context.Context) (string, error) {
		return "fail", nil
	})
	if !errors.Is(errA, ErrCapacityExceeded) {
		t.Fatalf("expected ErrCapacityExceeded on coordA, got %v", errA)
	}

	_, errB := coordB.Do(context.Background(), "b2", func(ctx context.Context) (int, error) {
		return 99, nil
	})
	if !errors.Is(errB, ErrCapacityExceeded) {
		t.Fatalf("expected ErrCapacityExceeded on coordB, got %v", errB)
	}

	close(unblock)
	wg.Wait()

	if gate.Active() != 0 {
		t.Fatalf("expected 0 active in gate after completion, got %d", gate.Active())
	}
}
