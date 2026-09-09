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
