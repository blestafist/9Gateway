package limiter

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/accounting"
)

func TestBudgetLimiterDailyUTCResetAndAdmittingDay(t *testing.T) {
	current := time.Date(2024, 2, 28, 23, 59, 59, 500_000_000, time.UTC)
	clock := func() time.Time { return current }
	limiter := NewBudgetLimiter(clock)
	policy := DailyBudgetPolicy(budgetMoney(t, 10))
	reservation, err := limiter.Reserve("daily", policy, budgetMoney(t, 10))
	if err != nil {
		t.Fatal(err)
	}
	current = time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)
	if _, err := limiter.Reserve("daily", policy, budgetMoney(t, 1)); err != nil {
		t.Fatalf("leap-day reset = %v", err)
	}
	if err := reservation.Commit(budgetMoney(t, 10)); err != nil {
		sh := limiter.shard("daily")
		sh.mu.Lock()
		s := sh.states["daily"]
		t.Fatalf("%v spent=%v active=%v days=%#v ownership=%#v total=%v daylimit=%v", err, s.spent, s.active, s.dayBuckets, reservation.ownership, s.total, s.dayLimit)
	}
	// The reservation was admitted on Feb 28; its settlement must not charge Feb 29.
	state := limiter.shard("daily")
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.states["daily"].dayBuckets) != 2 {
		t.Fatalf("day buckets = %d", len(state.states["daily"].dayBuckets))
	}
}

func TestBudgetLimiterCalendarMonthBoundariesAndCrossMonthSettlement(t *testing.T) {
	current := time.Date(2024, 1, 31, 23, 59, 59, 999999999, time.UTC)
	limiter := NewBudgetLimiter(func() time.Time { return current })
	policy := MonthlyBudgetPolicy(budgetMoney(t, 10))
	reservation, err := limiter.Reserve("month", policy, budgetMoney(t, 10))
	if err != nil {
		t.Fatal(err)
	}
	current = time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	if next, err := limiter.Reserve("month", policy, budgetMoney(t, 10)); err != nil || next == nil {
		t.Fatalf("February admission = %v", err)
	} else {
		_ = next.Release()
	}
	if err := reservation.Commit(budgetMoney(t, 10)); err != nil {
		t.Fatal(err)
	}
	state := limiter.shard("month")
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.states["month"].monthBuckets) != 2 {
		t.Fatalf("month buckets = %#v", state.states["month"].monthBuckets)
	}
	for _, date := range []time.Time{time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC), time.Date(2024, 3, 31, 12, 0, 0, 0, time.UTC), time.Date(2024, 4, 30, 12, 0, 0, 0, time.UTC), time.Date(2024, 5, 31, 12, 0, 0, 0, time.UTC), time.Date(2025, 1, 31, 12, 0, 0, 0, time.UTC)} {
		if got := currentMonth(date); got.Day() != 1 || got.Location() != time.UTC {
			t.Fatalf("month start %v", got)
		}
	}
}

func TestBudgetLimiterMonthlyRetryAndAtomicCombinedLimits(t *testing.T) {
	current := time.Date(2024, 12, 15, 0, 0, 0, 0, time.UTC)
	limiter := NewBudgetLimiter(func() time.Time { return current })
	policy := TotalDailyMonthlyBudgetPolicy(budgetMoney(t, 20), budgetMoney(t, 20), budgetMoney(t, 10))
	r, err := limiter.Reserve("combined", policy, budgetMoney(t, 10))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Commit(budgetMoney(t, 10)); err != nil {
		t.Fatal(err)
	}
	_, err = limiter.Reserve("combined", policy, budgetMoney(t, 1))
	var capacity *BudgetCapacityError
	if !errors.As(err, &capacity) || !capacity.ResetAt.Equal(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("monthly reset = %v/%v", err, capacity)
	}
	if got := limiter.RetryAfterSeconds(capacity.ResetAt); got != 1468800 {
		t.Fatalf("retry after = %d", got)
	}
	current = time.Date(2024, 12, 31, 23, 0, 0, 0, time.UTC)
	if _, err := limiter.Reserve("combined", policy, budgetMoney(t, 11)); !errors.Is(err, ErrBudgetCapacity) {
		t.Fatalf("atomic overage = %v", err)
	}
}

func TestBudgetLimiterDailyRejectionCarriesResetAndTotalSuppressesIt(t *testing.T) {
	current := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	limiter := NewBudgetLimiter(func() time.Time { return current })
	day := DailyBudgetPolicy(budgetMoney(t, 2))
	r, err := limiter.Reserve("daily-reset", day, budgetMoney(t, 2))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Commit(budgetMoney(t, 2)); err != nil {
		t.Fatal(err)
	}
	_, err = limiter.Reserve("daily-reset", day, budgetMoney(t, 1))
	var capacity *BudgetCapacityError
	if !errors.As(err, &capacity) || capacity.ResetAt != time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC) {
		t.Fatalf("daily rejection = %v/%v", err, capacity)
	}
	totalDay := TotalAndDailyBudgetPolicy(budgetMoney(t, 2), budgetMoney(t, 2))
	if _, err := limiter.Reserve("total-day", totalDay, budgetMoney(t, 3)); !errors.As(err, &capacity) || capacity.ResetAt.IsZero() == false && capacity.TotalRejected == false {
		t.Fatalf("combined rejection = %v", err)
	}
}

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

func TestBudgetPolicyReplacementPreservesSpendAcrossAmountChanges(t *testing.T) {
	limiter := NewBudgetLimiter()
	old := LimitedBudgetPolicy(budgetMoney(t, 10))
	limiter.RegisterPolicy("replace", old)
	reservation, err := limiter.Reserve("replace", old, budgetMoney(t, 7))
	if err != nil {
		t.Fatal(err)
	}
	if err := reservation.Commit(budgetMoney(t, 7)); err != nil {
		t.Fatal(err)
	}
	lower := LimitedBudgetPolicy(budgetMoney(t, 5))
	if err := limiter.ReplacePolicy("replace", old, lower, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := limiter.Reserve("replace", lower, budgetMoney(t, 1)); !errors.Is(err, ErrBudgetCapacity) {
		t.Fatalf("lowered policy admitted against debt: %v", err)
	}
	higher := LimitedBudgetPolicy(budgetMoney(t, 10))
	if err := limiter.ReplacePolicy("replace", lower, higher, nil); err != nil {
		t.Fatal(err)
	}
	available, err := limiter.Reserve("replace", higher, budgetMoney(t, 3))
	if err != nil {
		t.Fatal(err)
	}
	_ = available.ReleaseBeforeUpstream()
	unlimited := UnlimitedBudgetPolicy()
	if err := limiter.ReplacePolicy("replace", higher, unlimited, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := limiter.Reserve("replace", unlimited, budgetMoney(t, 100)); err != nil {
		t.Fatal(err)
	}
	readded := LimitedBudgetPolicy(budgetMoney(t, 8))
	if err := limiter.ReplacePolicy("replace", unlimited, readded, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := limiter.Reserve("replace", readded, budgetMoney(t, 2)); !errors.Is(err, ErrBudgetCapacity) {
		t.Fatalf("re-added policy reset historical spend: %v", err)
	}
}

func TestBudgetPolicyReplacementFailureAndCapturedReservation(t *testing.T) {
	limiter := NewBudgetLimiter()
	old := TotalDailyMonthlyBudgetPolicy(budgetMoney(t, 20), budgetMoney(t, 20), budgetMoney(t, 20))
	limiter.RegisterPolicy("atomic", old)
	active, err := limiter.Reserve("atomic", old, budgetMoney(t, 5))
	if err != nil {
		t.Fatal(err)
	}
	newPolicy := TotalDailyMonthlyBudgetPolicy(budgetMoney(t, 10), budgetMoney(t, 10), budgetMoney(t, 10))
	commitErr := errors.New("repository failed")
	if err := limiter.ReplacePolicy("atomic", old, newPolicy, func() error { return commitErr }); !errors.Is(err, commitErr) {
		t.Fatalf("replacement failure = %v", err)
	}
	if _, err := limiter.Reserve("atomic", old, budgetMoney(t, 16)); !errors.Is(err, ErrBudgetCapacity) {
		t.Fatalf("failed replacement changed old policy = %v", err)
	}
	if err := active.Commit(budgetMoney(t, 5)); err != nil {
		t.Fatal(err)
	}
	if err := limiter.ReplacePolicy("atomic", old, newPolicy, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := limiter.Reserve("atomic", newPolicy, budgetMoney(t, 6)); !errors.Is(err, ErrBudgetCapacity) {
		t.Fatalf("replacement moved or erased captured spend = %v", err)
	}
}

func TestBudgetPolicyReplacementSettlesCapturedDayAndMonthBuckets(t *testing.T) {
	now := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	limiter := NewBudgetLimiter(func() time.Time { return now })
	old := TotalDailyMonthlyBudgetPolicy(budgetMoney(t, 20), budgetMoney(t, 20), budgetMoney(t, 20))
	limiter.RegisterPolicy("periods", old)
	reservation, err := limiter.Reserve("periods", old, budgetMoney(t, 5))
	if err != nil {
		t.Fatal(err)
	}
	newPolicy := TotalDailyMonthlyBudgetPolicy(budgetMoney(t, 6), budgetMoney(t, 6), budgetMoney(t, 6))
	if err := limiter.ReplacePolicy("periods", old, newPolicy, nil); err != nil {
		t.Fatal(err)
	}
	if err := reservation.Commit(budgetMoney(t, 5)); err != nil {
		t.Fatal(err)
	}
	if _, err := limiter.Reserve("periods", newPolicy, budgetMoney(t, 2)); !errors.Is(err, ErrBudgetCapacity) {
		t.Fatalf("captured total/day/month spend was moved or reset: %v", err)
	}
	shard := limiter.shard("periods")
	shard.mu.Lock()
	state := shard.states["periods"]
	day := state.dayBuckets[currentDay(now)]
	month := state.monthBuckets[currentMonth(now)]
	shard.mu.Unlock()
	if state.spent != budgetMoney(t, 5) || day.spent != budgetMoney(t, 5) || month.spent != budgetMoney(t, 5) || !isZeroMoney(state.active) || !isZeroMoney(day.active) || !isZeroMoney(month.active) {
		t.Fatalf("replacement settlement state = total %v, day %#v, month %#v", state.spent, day, month)
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

func TestBudgetDailyKnownOverflowFallsBackAtomically(t *testing.T) {
	now := time.Date(2024, 7, 15, 12, 0, 0, 0, time.UTC)
	limiter := NewBudgetLimiter(func() time.Time { return now })
	dayStart := currentDay(now)
	prior := budgetMoney(t, accounting.MaxMoneyMicros-1)
	policy := DailyBudgetPolicy(accounting.MaxMoney())
	if err := limiter.LoadSpent([]BudgetSpent{{KeyID: "daily-overflow", Spent: prior, Period: BudgetPeriodDay, PeriodStart: dayStart}}); err != nil {
		t.Fatal(err)
	}
	reservation, err := limiter.Reserve("daily-overflow", policy, budgetMoney(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := reservation.Commit(budgetMoney(t, 2)); !errors.Is(err, ErrBudgetArithmetic) {
		t.Fatalf("overflow settlement = %v, want typed arithmetic error", err)
	}
	state := limiter.shard("daily-overflow")
	state.mu.Lock()
	current := state.states["daily-overflow"]
	var day budgetDayState
	if current != nil {
		day = current.dayBuckets[dayStart]
	}
	state.mu.Unlock()
	if current == nil || current.spent != budgetMoney(t, 0) || !isZeroMoney(current.active) || day.spent != accounting.MaxMoney() || !isZeroMoney(day.active) {
		t.Fatalf("daily fallback state = total %v/%v, day %#v", current.spent, current.active, day)
	}
	if _, err := limiter.Reserve("daily-overflow", policy, budgetMoney(t, 1)); !errors.Is(err, ErrBudgetCapacity) {
		t.Fatalf("post-fallback admission = %v, want daily capacity rejection", err)
	}
	result, finalized := reservation.Result()
	if !finalized || result.Kind != BudgetSettlementConservative || result.Charged != budgetMoney(t, 1) {
		t.Fatalf("settlement result = %#v, finalized=%v", result, finalized)
	}
	for range 4 {
		if err := reservation.Commit(budgetMoney(t, 3)); !errors.Is(err, ErrBudgetArithmetic) {
			t.Fatalf("repeated settlement = %v", err)
		}
	}
}

func TestBudgetDailyKnownOverflowConcurrentFinalizationIsOneShot(t *testing.T) {
	now := time.Date(2024, 8, 15, 12, 0, 0, 0, time.UTC)
	limiter := NewBudgetLimiter(func() time.Time { return now })
	dayStart := currentDay(now)
	if err := limiter.LoadSpent([]BudgetSpent{{KeyID: "daily-race", Spent: budgetMoney(t, accounting.MaxMoneyMicros-1), Period: BudgetPeriodDay, PeriodStart: dayStart}}); err != nil {
		t.Fatal(err)
	}
	policy := DailyBudgetPolicy(accounting.MaxMoney())
	reservation, err := limiter.Reserve("daily-race", policy, budgetMoney(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	const attempts = 64
	var wait sync.WaitGroup
	wait.Add(attempts)
	for range attempts {
		go func() {
			defer wait.Done()
			if err := reservation.Commit(budgetMoney(t, 2)); !errors.Is(err, ErrBudgetArithmetic) {
				t.Errorf("concurrent settlement = %v", err)
			}
		}()
	}
	wait.Wait()
	state := limiter.shard("daily-race")
	state.mu.Lock()
	current := state.states["daily-race"]
	var day budgetDayState
	if current != nil {
		day = current.dayBuckets[dayStart]
	}
	state.mu.Unlock()
	if current == nil || !isZeroMoney(current.active) || day.spent != accounting.MaxMoney() || !isZeroMoney(day.active) {
		t.Fatalf("concurrent settlement state = total %v/%v, day %#v", current.spent, current.active, day)
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
