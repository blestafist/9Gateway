package limiter

import (
	"errors"
	"math"
	"sync"
	"testing"

	"github.com/pestit/9gateway/internal/accounting"
)

func budgetMoney(t *testing.T, micros int64) accounting.Money {
	t.Helper()
	money, err := accounting.NewMoneyMicros(micros)
	if err != nil {
		t.Fatal(err)
	}
	return money
}

func TestBudgetLimiterExactCapacityAndOneMicroOver(t *testing.T) {
	limiter := NewBudgetLimiter()
	policy := LimitedBudgetPolicy(budgetMoney(t, 10))
	first, err := limiter.Reserve("key-a", policy, budgetMoney(t, 10))
	if err != nil || first == nil {
		t.Fatalf("exact reservation = %v, %v", first, err)
	}
	if _, err := limiter.Reserve("key-a", policy, budgetMoney(t, 1)); !errors.Is(err, ErrBudgetCapacity) {
		t.Fatalf("one-micro-over error = %v, want capacity rejection", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := limiter.Reserve("key-a", policy, budgetMoney(t, 10))
	if err != nil || second == nil {
		t.Fatalf("released capacity = %v, %v", second, err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestBudgetLimiterZeroSeparateKeysAndUnlimited(t *testing.T) {
	limiter := NewBudgetLimiter()
	policy := LimitedBudgetPolicy(budgetMoney(t, 10))
	zero, err := limiter.Reserve("key-a", policy, budgetMoney(t, 0))
	if err != nil || zero == nil || zero.Amount() != budgetMoney(t, 0) {
		t.Fatalf("zero reservation = %#v, %v", zero, err)
	}
	if got := limiter.Len(); got != 0 {
		t.Fatalf("zero reservation retained %d states", got)
	}
	if err := zero.Release(); err != nil {
		t.Fatal(err)
	}
	if err := zero.Release(); err != nil {
		t.Fatal(err)
	}

	first, err := limiter.Reserve("key-a", policy, budgetMoney(t, 10))
	if err != nil {
		t.Fatal(err)
	}
	other, err := limiter.Reserve("key-b", policy, budgetMoney(t, 10))
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	if err := other.Release(); err != nil {
		t.Fatal(err)
	}

	unlimited, err := limiter.Reserve("unlimited", UnlimitedBudgetPolicy(), accounting.UnknownMoney())
	if !errors.Is(err, ErrBudgetInvalid) || unlimited != nil {
		t.Fatalf("unknown unlimited candidate = %#v, %v", unlimited, err)
	}
	unlimited, err = limiter.Reserve("unlimited", UnlimitedBudgetPolicy(), budgetMoney(t, 99))
	if err != nil || unlimited == nil {
		t.Fatalf("unlimited reservation = %#v, %v", unlimited, err)
	}
	if got := limiter.Len(); got != 0 {
		t.Fatalf("unlimited reservation retained %d states", got)
	}
	if err := unlimited.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestBudgetLimiterAlreadySpentAndDebt(t *testing.T) {
	limiter := NewBudgetLimiter()
	if err := limiter.LoadSpent([]BudgetSpent{{KeyID: "key-a", Spent: budgetMoney(t, 7)}}); err != nil {
		t.Fatal(err)
	}
	policy := LimitedBudgetPolicy(budgetMoney(t, 10))
	reservation, err := limiter.Reserve("key-a", policy, budgetMoney(t, 3))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := limiter.Reserve("key-a", policy, budgetMoney(t, 1)); !errors.Is(err, ErrBudgetCapacity) {
		t.Fatalf("spent plus active overage = %v", err)
	}
	if err := reservation.Release(); err != nil {
		t.Fatal(err)
	}

	debt := NewBudgetLimiter()
	if err := debt.LoadSpent([]BudgetSpent{{KeyID: "key-a", Spent: budgetMoney(t, 11)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := debt.Reserve("key-a", policy, budgetMoney(t, 0)); !errors.Is(err, ErrBudgetCapacity) {
		t.Fatalf("zero reservation against debt = %v, want capacity rejection", err)
	}
	if _, err := debt.Reserve("key-a", policy, budgetMoney(t, 1)); !errors.Is(err, ErrBudgetCapacity) {
		t.Fatalf("debt admission = %v, want capacity rejection", err)
	}
}

func TestBudgetLimiterInitializationValidationIsAtomic(t *testing.T) {
	limiter := NewBudgetLimiter()
	if err := limiter.LoadSpent([]BudgetSpent{{KeyID: "key-a", Spent: budgetMoney(t, 1)}, {KeyID: "key-a", Spent: budgetMoney(t, 2)}}); !errors.Is(err, ErrBudgetInvalid) {
		t.Fatalf("duplicate initialization = %v", err)
	}
	if got := limiter.Len(); got != 0 {
		t.Fatalf("failed initialization retained %d states", got)
	}
	if err := limiter.LoadSpent([]BudgetSpent{{KeyID: "key-a", Spent: accounting.UnknownMoney()}}); !errors.Is(err, ErrBudgetInvalid) {
		t.Fatalf("unknown initialization = %v", err)
	}
	if err := limiter.LoadSpent([]BudgetSpent{{KeyID: "key-a", Spent: budgetMoney(t, 1)}}); err != nil {
		t.Fatal(err)
	}
	if err := limiter.LoadSpent([]BudgetSpent{{KeyID: "key-a", Spent: budgetMoney(t, 1)}}); !errors.Is(err, ErrBudgetInvalid) {
		t.Fatalf("duplicate load after initialization = %v", err)
	}

	corrupt := NewBudgetLimiter()
	corrupt.shard("key-a").states["key-a"] = &budgetState{spent: accounting.MaxMoney(), active: budgetMoney(t, 1)}
	if _, err := corrupt.Reserve("key-a", LimitedBudgetPolicy(accounting.MaxMoney()), budgetMoney(t, 1)); !errors.Is(err, ErrBudgetState) {
		t.Fatalf("overflowing state = %v", err)
	}
	if got := corrupt.shard("key-a").states["key-a"].active; got != budgetMoney(t, 1) {
		t.Fatalf("corrupt rejection changed active state = %v", got)
	}

	max := NewBudgetLimiter()
	if _, err := max.Reserve("key-a", LimitedBudgetPolicy(accounting.MaxMoney()), budgetMoney(t, math.MaxInt64)); err != nil {
		t.Fatalf("maximum exact candidate rejected = %v", err)
	}
	if _, err := max.Reserve("key-a", LimitedBudgetPolicy(accounting.MaxMoney()), budgetMoney(t, 1)); !errors.Is(err, ErrBudgetCapacity) {
		t.Fatalf("maximum overage = %v", err)
	}
}

func TestBudgetLimiterReleaseConcurrentAndReuse(t *testing.T) {
	limiter := NewBudgetLimiter()
	reservation, err := limiter.Reserve("key-a", LimitedBudgetPolicy(budgetMoney(t, 10)), budgetMoney(t, 10))
	if err != nil {
		t.Fatal(err)
	}
	const releases = 128
	var wait sync.WaitGroup
	wait.Add(releases)
	for index := 0; index < releases; index++ {
		go func() {
			defer wait.Done()
			if err := reservation.Release(); err != nil {
				t.Errorf("concurrent release = %v", err)
			}
		}()
	}
	wait.Wait()
	if got := limiter.Len(); got != 0 {
		t.Fatalf("state after release = %d", got)
	}
	reused, err := limiter.Reserve("key-a", LimitedBudgetPolicy(budgetMoney(t, 10)), budgetMoney(t, 10))
	if err != nil || reused == nil {
		t.Fatalf("immediate reuse = %#v, %v", reused, err)
	}
	if err := reused.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestBudgetLimiterBarrierAdmissionPerKeyAndParallelKeys(t *testing.T) {
	limiter := NewBudgetLimiter()
	policy := LimitedBudgetPolicy(budgetMoney(t, 32))
	const attempts = 256
	start := make(chan struct{})
	var wait sync.WaitGroup
	var mu sync.Mutex
	admitted := 0
	reservations := make([]*BudgetReservation, 0, attempts)
	wait.Add(attempts)
	for index := 0; index < attempts; index++ {
		go func() {
			defer wait.Done()
			<-start
			reservation, err := limiter.Reserve("same-key", policy, budgetMoney(t, 1))
			if err == nil {
				mu.Lock()
				admitted++
				reservations = append(reservations, reservation)
				mu.Unlock()
			} else if !errors.Is(err, ErrBudgetCapacity) {
				t.Errorf("same-key rejection = %v", err)
			}
		}()
	}
	close(start)
	wait.Wait()
	if admitted != 32 {
		t.Fatalf("same-key admitted %d, want 32", admitted)
	}
	for _, reservation := range reservations {
		if err := reservation.Release(); err != nil {
			t.Fatal(err)
		}
	}

	// Different shards have independent locks; this parallel admission must
	// still preserve exact per-key capacity without a global counter lock.
	var parallel sync.WaitGroup
	for _, keyID := range []string{"key-a", "key-b", "key-c", "key-d"} {
		parallel.Add(1)
		go func(keyID string) {
			defer parallel.Done()
			reservation, err := limiter.Reserve(keyID, policy, budgetMoney(t, 32))
			if err != nil {
				t.Errorf("separate-key admission %q = %v", keyID, err)
				return
			}
			if err := reservation.Release(); err != nil {
				t.Errorf("separate-key release = %v", err)
			}
		}(keyID)
	}
	parallel.Wait()
}

func TestBudgetReservationIdentityIsCopySafe(t *testing.T) {
	limiter := NewBudgetLimiter()
	policy := LimitedBudgetPolicy(budgetMoney(t, 3))
	first, err := limiter.Reserve("stable-id", policy, budgetMoney(t, 2))
	if err != nil {
		t.Fatal(err)
	}
	copyOfReservation := *first
	if first.KeyID() != "stable-id" || copyOfReservation.KeyID() != first.KeyID() || copyOfReservation.Amount() != first.Amount() {
		t.Fatal("reservation copy changed immutable identity")
	}
	if err := copyOfReservation.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := limiter.Reserve("stable-id", policy, budgetMoney(t, 3)); err != nil {
		t.Fatalf("copy release did not release once: %v", err)
	}
}

func budgetStateForTest(t *testing.T, limiter *BudgetLimiter, key string) (accounting.Money, accounting.Money) {
	t.Helper()
	shard := limiter.shard(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	state := shard.states[key]
	if state == nil {
		return budgetMoney(t, 0), budgetMoney(t, 0)
	}
	return state.spent, state.active
}

func TestBudgetReservationTerminalOutcomesAndInvalidActual(t *testing.T) {
	policy := LimitedBudgetPolicy(budgetMoney(t, 100))
	limiter := NewBudgetLimiter()
	refund, err := limiter.Reserve("refund", policy, budgetMoney(t, 40))
	if err != nil || refund.CommitKnown(budgetMoney(t, 10)) != nil {
		t.Fatalf("refund = %v, %v", refund, err)
	}
	spent, active := budgetStateForTest(t, limiter, "refund")
	if spent != budgetMoney(t, 10) || !isZeroMoney(active) {
		t.Fatalf("refund state = %v/%v", spent, active)
	}
	exact, err := limiter.Reserve("exact", policy, budgetMoney(t, 20))
	if err != nil || exact.Commit(budgetMoney(t, 20)) != nil {
		t.Fatalf("exact = %v, %v", exact, err)
	}
	zero, err := limiter.Reserve("zero", policy, budgetMoney(t, 20))
	if err != nil || zero.Commit(budgetMoney(t, 0)) != nil {
		t.Fatalf("zero = %v, %v", zero, err)
	}
	debt, err := limiter.Reserve("debt", policy, budgetMoney(t, 20))
	if err != nil || debt.Commit(budgetMoney(t, 130)) != nil {
		t.Fatalf("debt = %v, %v", debt, err)
	}
	if _, err := limiter.Reserve("debt", policy, budgetMoney(t, 1)); !errors.Is(err, ErrBudgetCapacity) {
		t.Fatalf("debt admission = %v", err)
	}
	invalid, err := limiter.Reserve("invalid", policy, budgetMoney(t, 20))
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(invalid.Commit(accounting.UnknownMoney()), ErrBudgetInvalidActual) {
		t.Fatal("unknown actual was not reported as typed safe error")
	}
	spent, active = budgetStateForTest(t, limiter, "invalid")
	if spent != budgetMoney(t, 20) || !isZeroMoney(active) {
		t.Fatalf("invalid conservative state = %v/%v", spent, active)
	}
}

func TestBudgetDeferredAdjustmentAndSinkOrdering(t *testing.T) {
	limiter := NewBudgetLimiter()
	policy := LimitedBudgetPolicy(budgetMoney(t, 100))
	var seen []CommittedBudgetDelta
	var sinkMu sync.Mutex
	expectedSpent := int64(40)
	limiter.SetCommittedDeltaSink(func(delta CommittedBudgetDelta) {
		sinkMu.Lock()
		defer sinkMu.Unlock()
		want := expectedSpent
		if delta.Delta == -30 {
			want = 10
		}
		spent, active := budgetStateForTest(t, limiter, delta.KeyID)
		spentMicros, known := spent.Micros()
		if !known || !isZeroMoney(active) || spentMicros != want {
			t.Errorf("sink observed stale state: %v/%v for delta %d", spent, active, delta.Delta)
		}
		seen = append(seen, delta)
		expectedSpent = want
	})
	reservation, err := limiter.Reserve("deferred", policy, budgetMoney(t, 40))
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := reservation.CommitDeferred()
	if err != nil || ticket == nil {
		t.Fatalf("deferred = %v, %v", ticket, err)
	}
	if spent, active := budgetStateForTest(t, limiter, "deferred"); spent != budgetMoney(t, 40) || !isZeroMoney(active) {
		t.Fatalf("conservative state = %v/%v", spent, active)
	}
	if err := ticket.Adjust(budgetMoney(t, 10)); err != nil {
		t.Fatal(err)
	}
	if spent, active := budgetStateForTest(t, limiter, "deferred"); spent != budgetMoney(t, 10) || !isZeroMoney(active) {
		t.Fatalf("adjusted state = %v/%v", spent, active)
	}
	sinkMu.Lock()
	if len(seen) != 2 || seen[0].Delta != 40 || seen[1].Delta != -30 {
		t.Fatalf("sink deltas = %+v", seen)
	}
	sinkMu.Unlock()
	if ticket.Adjust(budgetMoney(t, 90)) != nil {
		t.Fatal("repeated adjustment changed terminal result")
	}
}

func TestBudgetDeferredAdjustmentWithActiveSameKeyReservation(t *testing.T) {
	t.Run("equal retains spent and active state without adjustment", func(t *testing.T) {
		limiter := NewBudgetLimiter()
		policy := LimitedBudgetPolicy(budgetMoney(t, 100))
		var deltas []CommittedBudgetDelta
		var sinkMu sync.Mutex
		limiter.SetCommittedDeltaSink(func(delta CommittedBudgetDelta) {
			sinkMu.Lock()
			deltas = append(deltas, delta)
			sinkMu.Unlock()
		})
		deferred, err := limiter.Reserve("equal-key", policy, budgetMoney(t, 40))
		if err != nil {
			t.Fatal(err)
		}
		ticket, err := deferred.CommitDeferred()
		if err != nil {
			t.Fatal(err)
		}
		active, err := limiter.Reserve("equal-key", policy, budgetMoney(t, 10))
		if err != nil {
			t.Fatal(err)
		}
		if err := ticket.Adjust(budgetMoney(t, 40)); err != nil {
			t.Fatalf("equal adjustment = %v", err)
		}
		if err := ticket.Adjust(budgetMoney(t, 60)); err != nil {
			t.Fatalf("repeated equal adjustment = %v", err)
		}
		spent, currentActive := budgetStateForTest(t, limiter, "equal-key")
		if spent != budgetMoney(t, 40) || currentActive != budgetMoney(t, 10) {
			t.Fatalf("equal adjusted state = %v/%v, want 40/10", spent, currentActive)
		}
		sinkMu.Lock()
		if len(deltas) != 1 || deltas[0].Delta != 40 {
			t.Fatalf("equal adjustment deltas = %+v, want only initial +40", deltas)
		}
		sinkMu.Unlock()
		if limiter.Len() != 1 {
			t.Fatalf("equal active state count = %d, want 1", limiter.Len())
		}
		if err := active.Release(); err != nil {
			t.Fatal(err)
		}
		spent, currentActive = budgetStateForTest(t, limiter, "equal-key")
		if spent != budgetMoney(t, 40) || !isZeroMoney(currentActive) || limiter.Len() != 1 {
			t.Fatalf("equal post-release state = %v/%v, len %d; nonzero spent must retain state", spent, currentActive, limiter.Len())
		}
	})

	t.Run("zero refunds only after state update and cleans up after active release", func(t *testing.T) {
		limiter := NewBudgetLimiter()
		policy := LimitedBudgetPolicy(budgetMoney(t, 100))
		var deltas []CommittedBudgetDelta
		var sinkMu sync.Mutex
		var observedSpent, observedActive accounting.Money
		limiter.SetCommittedDeltaSink(func(delta CommittedBudgetDelta) {
			sinkMu.Lock()
			deltas = append(deltas, delta)
			sinkMu.Unlock()
			if delta.Delta == -40 {
				observedSpent, observedActive = budgetStateForTest(t, limiter, "zero-key")
			}
		})
		deferred, err := limiter.Reserve("zero-key", policy, budgetMoney(t, 40))
		if err != nil {
			t.Fatal(err)
		}
		ticket, err := deferred.CommitDeferred()
		if err != nil {
			t.Fatal(err)
		}
		active, err := limiter.Reserve("zero-key", policy, budgetMoney(t, 10))
		if err != nil {
			t.Fatal(err)
		}
		if err := ticket.Adjust(budgetMoney(t, 0)); err != nil {
			t.Fatalf("zero adjustment = %v", err)
		}
		if err := ticket.Adjust(budgetMoney(t, 10)); err != nil {
			t.Fatalf("repeated zero adjustment = %v", err)
		}
		if observedSpent != budgetMoney(t, 0) || observedActive != budgetMoney(t, 10) {
			t.Fatalf("zero sink observed %v/%v, want 0/10 after state update", observedSpent, observedActive)
		}
		sinkMu.Lock()
		if len(deltas) != 2 || deltas[0].Delta != 40 || deltas[1].Delta != -40 {
			t.Fatalf("zero adjustment deltas = %+v, want +40/-40", deltas)
		}
		sinkMu.Unlock()
		spent, currentActive := budgetStateForTest(t, limiter, "zero-key")
		if spent != budgetMoney(t, 0) || currentActive != budgetMoney(t, 10) || limiter.Len() != 1 {
			t.Fatalf("zero active state = %v/%v, len %d; state must await active release", spent, currentActive, limiter.Len())
		}
		if err := active.Release(); err != nil {
			t.Fatal(err)
		}
		spent, currentActive = budgetStateForTest(t, limiter, "zero-key")
		if !isZeroMoney(spent) || !isZeroMoney(currentActive) || limiter.Len() != 0 {
			t.Fatalf("zero cleaned state = %v/%v, len %d", spent, currentActive, limiter.Len())
		}
		capacity, err := limiter.Reserve("zero-key", policy, budgetMoney(t, 100))
		if err != nil {
			t.Fatalf("zero cleanup did not restore capacity: %v", err)
		}
		if err := capacity.Release(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("lower", func(t *testing.T) {
		limiter := NewBudgetLimiter()
		policy := LimitedBudgetPolicy(budgetMoney(t, 100))
		deferred, err := limiter.Reserve("stable-key", policy, budgetMoney(t, 40))
		if err != nil {
			t.Fatal(err)
		}
		ticket, err := deferred.CommitDeferred()
		if err != nil {
			t.Fatal(err)
		}
		active, err := limiter.Reserve("stable-key", policy, budgetMoney(t, 10))
		if err != nil {
			t.Fatal(err)
		}
		if err := ticket.Adjust(budgetMoney(t, 10)); err != nil {
			t.Fatalf("adjust with active reservation = %v", err)
		}
		spent, currentActive := budgetStateForTest(t, limiter, "stable-key")
		if spent != budgetMoney(t, 10) || currentActive != budgetMoney(t, 10) {
			t.Fatalf("adjusted state = %v/%v, want 10/10", spent, currentActive)
		}
		fits, err := limiter.Reserve("stable-key", policy, budgetMoney(t, 80))
		if err != nil {
			t.Fatalf("capacity after lower adjustment = %v", err)
		}
		if _, err := limiter.Reserve("stable-key", policy, budgetMoney(t, 1)); !errors.Is(err, ErrBudgetCapacity) {
			t.Fatalf("over-capacity after lower adjustment = %v", err)
		}
		if err := fits.Release(); err != nil {
			t.Fatal(err)
		}
		if err := active.Release(); err != nil {
			t.Fatal(err)
		}
		spent, currentActive = budgetStateForTest(t, limiter, "stable-key")
		if spent != budgetMoney(t, 10) || !isZeroMoney(currentActive) {
			t.Fatalf("cleaned lower state = %v/%v", spent, currentActive)
		}
	})

	t.Run("higher", func(t *testing.T) {
		limiter := NewBudgetLimiter()
		policy := LimitedBudgetPolicy(budgetMoney(t, 100))
		deferred, err := limiter.Reserve("stable-key", policy, budgetMoney(t, 40))
		if err != nil {
			t.Fatal(err)
		}
		ticket, err := deferred.CommitDeferred()
		if err != nil {
			t.Fatal(err)
		}
		active, err := limiter.Reserve("stable-key", policy, budgetMoney(t, 10))
		if err != nil {
			t.Fatal(err)
		}
		if err := ticket.Adjust(budgetMoney(t, 60)); err != nil {
			t.Fatalf("adjust with active reservation = %v", err)
		}
		spent, currentActive := budgetStateForTest(t, limiter, "stable-key")
		if spent != budgetMoney(t, 60) || currentActive != budgetMoney(t, 10) {
			t.Fatalf("adjusted state = %v/%v, want 60/10", spent, currentActive)
		}
		fits, err := limiter.Reserve("stable-key", policy, budgetMoney(t, 30))
		if err != nil {
			t.Fatalf("capacity after higher adjustment = %v", err)
		}
		if _, err := limiter.Reserve("stable-key", policy, budgetMoney(t, 1)); !errors.Is(err, ErrBudgetCapacity) {
			t.Fatalf("over-capacity after higher adjustment = %v", err)
		}
		if err := fits.Release(); err != nil {
			t.Fatal(err)
		}
		if err := active.Release(); err != nil {
			t.Fatal(err)
		}
		spent, currentActive = budgetStateForTest(t, limiter, "stable-key")
		if spent != budgetMoney(t, 60) || !isZeroMoney(currentActive) {
			t.Fatalf("cleaned higher state = %v/%v", spent, currentActive)
		}
	})
}

func TestBudgetDeferredConcurrentAdjustInvalidateIsOneShot(t *testing.T) {
	limiter := NewBudgetLimiter()
	reservation, err := limiter.Reserve("race-deferred", LimitedBudgetPolicy(budgetMoney(t, 100)), budgetMoney(t, 40))
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := reservation.CommitDeferred()
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	go func() { defer wait.Done(); <-start; _ = ticket.Adjust(budgetMoney(t, 10)) }()
	go func() { defer wait.Done(); <-start; ticket.Invalidate() }()
	close(start)
	wait.Wait()
	spent, active := budgetStateForTest(t, limiter, "race-deferred")
	if !isZeroMoney(active) || (spent != budgetMoney(t, 40) && spent != budgetMoney(t, 10)) {
		t.Fatalf("racing ticket state = %v/%v", spent, active)
	}
}
