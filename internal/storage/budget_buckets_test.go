package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/limiter"
)

func insertBudgetTestKey(t *testing.T, database *DB, id string) {
	t.Helper()
	_, err := database.Exec(`INSERT INTO api_keys (id,name,prefix,key_hash,enabled,created_at,updated_at,policy_json) VALUES (?, ?, ?, randomblob(32), 1, 1, 1, '{}')`, id, id, id)
	if err != nil {
		t.Fatal(err)
	}
}

func TestBudgetBucketRepositoryAppliesAtomicSignedDeltas(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	insertBudgetTestKey(t, database, "a")
	insertBudgetTestKey(t, database, "b")
	repository := NewBudgetBucketRepository(database)
	if err := repository.ApplyDeltas(context.Background(), []BudgetBucketDelta{{APIKeyID: "a", SpentDelta: 9}, {APIKeyID: "b", SpentDelta: 4}}); err != nil {
		t.Fatal(err)
	}
	if err := repository.ApplyDelta(context.Background(), BudgetBucketDelta{APIKeyID: "a", SpentDelta: -3}); err != nil {
		t.Fatal(err)
	}
	if err := repository.ApplyDeltas(context.Background(), []BudgetBucketDelta{{APIKeyID: "a", SpentDelta: 2}, {APIKeyID: "b", SpentDelta: -99}}); !errors.Is(err, ErrBudgetBucketUnderflow) {
		t.Fatalf("atomic underflow = %v", err)
	}
	buckets, err := repository.LoadTotal(context.Background())
	if err != nil || len(buckets) != 2 || buckets[0].APIKeyID != "a" || buckets[0].SpentMicros != 6 || buckets[1].APIKeyID != "b" || buckets[1].SpentMicros != 4 {
		t.Fatalf("buckets = %#v/%v, want stable unchanged rows", buckets, err)
	}
}

func TestBudgetBucketRepositoryRequiresZeroTimeForTotalIdentity(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	insertBudgetTestKey(t, database, "key")
	repository := NewBudgetBucketRepository(database)

	if err := repository.ApplyDelta(context.Background(), BudgetBucketDelta{
		APIKeyID: "key", SpentDelta: 4, Period: limiter.BudgetPeriodTotal,
	}); err != nil {
		t.Fatalf("zero-value total start: %v", err)
	}
	if err := repository.ApplyDelta(context.Background(), BudgetBucketDelta{
		APIKeyID: "key", SpentDelta: 1, Period: limiter.BudgetPeriodTotal,
		PeriodStart: time.Unix(0, 0).UTC(),
	}); !errors.Is(err, ErrInvalidBudgetBucket) {
		t.Fatalf("epoch total start = %v, want invalid bucket", err)
	}
	buckets, err := repository.LoadTotal(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 1 || !buckets[0].PeriodStart.IsZero() || buckets[0].SpentMicros != 4 {
		t.Fatalf("total buckets = %#v, want one zero-start bucket with four micros", buckets)
	}
}

func TestBudgetBucketRepositoryValidatesAllDurableRowsBeforeFiltering(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	insertBudgetTestKey(t, database, "key")
	// Simulate a manual/constraint-bypass corruption. The malformed day is
	// expired relative to the loader below, so a current-only query must not be
	// allowed to hide it from startup validation.
	if _, err := database.Exec(`PRAGMA ignore_check_constraints = ON`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO budget_buckets
		(api_key_id, period_kind, period_start, spent_micros, created_at, updated_at)
		VALUES ('key', 'day', 1, 99, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	repository := NewBudgetBucketRepository(database)
	if _, err := repository.LoadDay(context.Background(), time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)); !errors.Is(err, ErrInvalidBudgetBucket) {
		t.Fatalf("expired malformed row load = %v, want invalid bucket", err)
	}
}

func TestBudgetBucketRepositoryRejectsCorruptUnknownKeyAndTimestampRows(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`PRAGMA ignore_check_constraints = ON; PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO budget_buckets
		(api_key_id, period_kind, period_start, spent_micros, created_at, updated_at)
		VALUES ('missing', 'total', 0, 1, -1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := NewBudgetBucketRepository(database).LoadTotal(context.Background()); !errors.Is(err, ErrInvalidBudgetBucket) {
		t.Fatalf("corrupt unknown-key/timestamp row load = %v, want invalid bucket", err)
	}
}

func TestBudgetBucketRepositoryRejectsUnknownAndUnrepresentableDeltas(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	repository := NewBudgetBucketRepository(database)
	for _, delta := range []BudgetBucketDelta{{APIKeyID: "missing", SpentDelta: 1}, {APIKeyID: "missing", SpentDelta: -1}} {
		if err := repository.ApplyDelta(context.Background(), delta); !errors.Is(err, ErrBudgetBucketUnknownKey) {
			t.Fatalf("unknown key delta %#v = %v", delta, err)
		}
	}
	insertBudgetTestKey(t, database, "known")
	if err := repository.ApplyDelta(context.Background(), BudgetBucketDelta{APIKeyID: "known", SpentDelta: -1}); !errors.Is(err, ErrBudgetBucketUnknownKey) {
		t.Fatalf("missing total bucket = %v", err)
	}
	if err := repository.ApplyDelta(context.Background(), BudgetBucketDelta{APIKeyID: "known", SpentDelta: -1 << 63}); !errors.Is(err, ErrBudgetBucketUnderflow) {
		t.Fatalf("minimum delta = %v", err)
	}
}

func TestBudgetAccumulatorCoalescesAndRestoresAfterWriterRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "budget.db")
	database, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	insertBudgetTestKey(t, database, "key")
	repository := NewBudgetBucketRepository(database)
	accumulator := NewBudgetAccumulator(repository)
	budgetLimiter := limiter.NewBudgetLimiter()
	budgetLimiter.SetCommittedDeltaSink(accumulator.Sink)
	for i := 0; i < 10; i++ {
		accumulator.Sink(limiter.CommittedBudgetDelta{KeyID: "key", Delta: 3})
	}
	waitForBudgetSpend(t, repository, 30)
	if err := accumulator.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered := NewBudgetAccumulator(NewBudgetBucketRepository(reopened))
	recovered.Sink(limiter.CommittedBudgetDelta{KeyID: "key", Delta: 7})
	waitForBudgetSpend(t, recovered.repository, 37)
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := recovered.Shutdown(shutdown); err != nil {
		t.Fatal(err)
	}
}

func waitForBudgetSpend(t *testing.T, repository *BudgetBucketRepository, want int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		buckets, err := repository.LoadTotal(context.Background())
		if err == nil && len(buckets) == 1 && buckets[0].SpentMicros == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	buckets, err := repository.LoadTotal(context.Background())
	t.Fatalf("budget spend = %#v/%v, want %d", buckets, err, want)
}

func TestBudgetAccumulatorShutdownStopsLateNotifications(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	insertBudgetTestKey(t, database, "key")
	accumulator := NewBudgetAccumulator(NewBudgetBucketRepository(database))
	if err := accumulator.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	accumulator.Sink(limiter.CommittedBudgetDelta{KeyID: "key", Delta: 10})
	buckets, err := NewBudgetBucketRepository(database).LoadTotal(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 0 {
		t.Fatalf("late notification persisted %#v", buckets)
	}
}
