package storage

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOverviewQueryPlan(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer database.Close()

	query := `EXPLAIN QUERY PLAN
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN terminal_outcome IN ('complete', 'custom_dispatch') THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN terminal_outcome IS NULL OR terminal_outcome NOT IN ('complete', 'custom_dispatch', 'pre_upstream') THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN terminal_outcome = 'pre_upstream' THEN 1 ELSE 0 END), 0),
			CASE WHEN COUNT(input_tokens) < COUNT(*) THEN NULL ELSE SUM(input_tokens) END,
			CASE WHEN COUNT(cached_input_tokens) < COUNT(*) THEN NULL ELSE SUM(cached_input_tokens) END,
			CASE WHEN COUNT(output_tokens) < COUNT(*) THEN NULL ELSE SUM(output_tokens) END,
			CASE WHEN COUNT(cost_micros) < COUNT(*) THEN NULL ELSE SUM(cost_micros) END
		FROM requests
		WHERE finished_at >= ? AND finished_at <= ?`

	rows, err := database.QueryContext(context.Background(), query, 1000, 2000)
	if err != nil {
		t.Fatalf("explain query plan failed: %v", err)
	}
	defer rows.Close()

	var (
		id, parent, notused int
		detail              string
		plan                []string
	)
	var usedIndex bool
	for rows.Next() {
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan plan row: %v", err)
		}
		plan = append(plan, detail)
		if strings.Contains(detail, "USING INDEX") {
			usedIndex = true
		}
	}
	if !usedIndex {
		t.Fatalf("expected query to use index, plan was: %v", plan)
	}
}

func TestOverviewEmptyData(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	repo := NewAPIKeyRepository(database)
	now := time.Now().UTC()
	after := now.Add(-24 * time.Hour)
	before := now

	data, err := repo.GetOverview(context.Background(), after, before, 5)
	if err != nil {
		t.Fatalf("GetOverview failed: %v", err)
	}

	if data.Current.TotalRequests != 0 || data.Current.SuccessfulRequests != 0 ||
		data.Current.ErrorRequests != 0 || data.Current.RejectedRequests != 0 {
		t.Fatalf("expected 0 requests, got %+v", data.Current)
	}
	if data.Current.InputTokens != nil || data.Current.CachedInputTokens != nil ||
		data.Current.OutputTokens != nil || data.Current.CostMicros != nil {
		t.Fatalf("expected nil token/cost sums on empty data, got %+v", data.Current)
	}
	if data.Previous.TotalRequests != 0 {
		t.Fatalf("expected 0 previous requests, got %d", data.Previous.TotalRequests)
	}
	if data.ActiveRequests != 5 {
		t.Fatalf("active requests = %d, want 5", data.ActiveRequests)
	}
	if len(data.RecentRequests) != 0 {
		t.Fatalf("expected 0 recent requests, got %d", len(data.RecentRequests))
	}
	if data.KeyCounts.Total != 0 || data.KeyCounts.Enabled != 0 {
		t.Fatalf("expected 0 key counts, got %+v", data.KeyCounts)
	}
}

func TestOverviewAllTerminalOutcomesAndTotals(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	history := NewRequestHistoryRepository(database)
	baseTime := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	after := baseTime.Add(-1 * time.Hour)
	before := baseTime.Add(1 * time.Hour)

	// Insert requests for each terminal outcome, including NULL terminal outcome
	outcomes := []struct {
		id      string
		outcome string
		errCode string
		status  int64
		upStart bool
	}{
		{"00000000000000000000000000000001", "complete", "", 200, true},
		{"00000000000000000000000000000002", "custom_dispatch", "", 200, true},
		{"00000000000000000000000000000003", "pre_upstream", "invalid_api_key", 401, false},
		{"00000000000000000000000000000004", "upstream_error", "upstream_timeout", 504, true},
		{"00000000000000000000000000000005", "response_error", "response_transport_error", 502, true},
		{"00000000000000000000000000000006", "cancelled", "cancelled", 499, true},
		{"00000000000000000000000000000007", "", "", 500, true}, // Absent / NULL outcome classified as error
	}

	for i, o := range outcomes {
		rec := HistoryRecord{
			RequestID:        o.id,
			TerminalOutcome:  o.outcome,
			ErrorCode:        o.errCode,
			DownstreamStatus: KnownInt64(o.status),
			UpstreamStarted:  o.upStart,
			InputTokens:      KnownInt64(10),
			OutputTokens:     KnownInt64(5),
			CostMicros:       KnownInt64(100),
			StartedAt:        KnownInt64(baseTime.UnixMicro() - 1000 + int64(i)),
			FinishedAt:       KnownInt64(baseTime.UnixMicro() + int64(i)),
		}
		if err := history.Persist(context.Background(), rec, nil); err != nil {
			t.Fatalf("persist %s failed: %v", o.id, err)
		}
	}

	repo := NewAPIKeyRepository(database)
	data, err := repo.GetOverview(context.Background(), after, before, 0)
	if err != nil {
		t.Fatalf("GetOverview failed: %v", err)
	}

	// 7 total requests:
	// successful: 2 (complete, custom_dispatch)
	// rejected: 1 (pre_upstream)
	// error: 4 (upstream_error, response_error, cancelled, plus absent/NULL outcome)
	// Total must match sum and never double count!
	if data.Current.TotalRequests != 7 {
		t.Fatalf("total requests = %d, want 7", data.Current.TotalRequests)
	}
	if data.Current.SuccessfulRequests != 2 {
		t.Fatalf("successful requests = %d, want 2", data.Current.SuccessfulRequests)
	}
	if data.Current.RejectedRequests != 1 {
		t.Fatalf("rejected requests = %d, want 1", data.Current.RejectedRequests)
	}
	if data.Current.ErrorRequests != 4 {
		t.Fatalf("error requests = %d, want 4", data.Current.ErrorRequests)
	}

	sum := data.Current.SuccessfulRequests + data.Current.ErrorRequests + data.Current.RejectedRequests
	if sum != data.Current.TotalRequests {
		t.Fatalf("partition sum %d != total %d", sum, data.Current.TotalRequests)
	}
}

func TestOverviewNullableUsage(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	history := NewRequestHistoryRepository(database)
	baseTime := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	after := baseTime.Add(-1 * time.Hour)
	before := baseTime.Add(1 * time.Hour)

	// Record 1: unknown tokens and cost (NULL)
	rec1 := HistoryRecord{
		RequestID:       "00000000000000000000000000000001",
		TerminalOutcome: "complete",
		UpstreamStarted: true,
		FinishedAt:      KnownInt64(baseTime.UnixMicro() + 1),
	}
	if err := history.Persist(context.Background(), rec1, nil); err != nil {
		t.Fatalf("persist rec1: %v", err)
	}

	repo := NewAPIKeyRepository(database)
	// Case 1: Range contains only rec1 (all unknown)
	data, err := repo.GetOverview(context.Background(), after, before, 0)
	if err != nil {
		t.Fatalf("GetOverview: %v", err)
	}
	if data.Current.InputTokens != nil || data.Current.CostMicros != nil {
		t.Fatalf("expected nil tokens and cost for unknown, got tokens=%v, cost=%v", data.Current.InputTokens, data.Current.CostMicros)
	}

	// Record 2: known 0 tokens and cost
	rec2 := HistoryRecord{
		RequestID:         "00000000000000000000000000000002",
		TerminalOutcome:   "complete",
		UpstreamStarted:   true,
		InputTokens:       KnownInt64(0),
		CachedInputTokens: KnownInt64(0),
		OutputTokens:      KnownInt64(0),
		CostMicros:        KnownInt64(0),
		FinishedAt:        KnownInt64(baseTime.UnixMicro() + 2),
	}
	if err := history.Persist(context.Background(), rec2, nil); err != nil {
		t.Fatalf("persist rec2: %v", err)
	}

	// Case 2: Range contains rec1 (unknown) and rec2 (known 0) -> mixed known+unknown must be nil!
	data, err = repo.GetOverview(context.Background(), after, before, 0)
	if err != nil {
		t.Fatalf("GetOverview: %v", err)
	}
	if data.Current.InputTokens != nil || data.Current.CostMicros != nil {
		t.Fatalf("expected nil tokens and cost for mixed known+unknown, got tokens=%v, cost=%v", data.Current.InputTokens, data.Current.CostMicros)
	}

	// Case 3: Range contains only rec2 (known 0)
	t2 := time.UnixMicro(baseTime.UnixMicro() + 2).UTC()
	dataOnlyRec2, err := repo.GetOverview(context.Background(), t2, t2.Add(time.Microsecond), 0)
	if err != nil {
		t.Fatalf("GetOverview rec2: %v", err)
	}
	if dataOnlyRec2.Current.InputTokens == nil || *dataOnlyRec2.Current.InputTokens != 0 {
		t.Fatalf("expected known 0 input tokens, got %v", dataOnlyRec2.Current.InputTokens)
	}
	if dataOnlyRec2.Current.CostMicros == nil || *dataOnlyRec2.Current.CostMicros != 0 {
		t.Fatalf("expected known 0 cost micros, got %v", dataOnlyRec2.Current.CostMicros)
	}

	// Record 3: positive tokens and cost
	rec3 := HistoryRecord{
		RequestID:         "00000000000000000000000000000003",
		TerminalOutcome:   "complete",
		UpstreamStarted:   true,
		InputTokens:       KnownInt64(50),
		CachedInputTokens: KnownInt64(20),
		OutputTokens:      KnownInt64(30),
		CostMicros:        KnownInt64(500),
		FinishedAt:        KnownInt64(baseTime.UnixMicro() + 3),
	}
	if err := history.Persist(context.Background(), rec3, nil); err != nil {
		t.Fatalf("persist rec3: %v", err)
	}

	// Case 4: Range covering rec2 and rec3 (both known) -> known sum!
	tKnownStart := time.UnixMicro(baseTime.UnixMicro() + 2).UTC()
	tKnownEnd := time.UnixMicro(baseTime.UnixMicro() + 4).UTC()
	dataKnown, err := repo.GetOverview(context.Background(), tKnownStart, tKnownEnd, 0)
	if err != nil {
		t.Fatalf("GetOverview known: %v", err)
	}
	if dataKnown.Current.InputTokens == nil || *dataKnown.Current.InputTokens != 50 {
		t.Fatalf("expected 50 input tokens, got %v", dataKnown.Current.InputTokens)
	}
	if dataKnown.Current.CachedInputTokens == nil || *dataKnown.Current.CachedInputTokens != 20 {
		t.Fatalf("expected 20 cached tokens, got %v", dataKnown.Current.CachedInputTokens)
	}
	if dataKnown.Current.CostMicros == nil || *dataKnown.Current.CostMicros != 500 {
		t.Fatalf("expected 500 cost micros, got %v", dataKnown.Current.CostMicros)
	}

	// Case 5: Full range covering rec1, rec2, rec3 -> mixed known+unknown must be nil!
	dataFull, err := repo.GetOverview(context.Background(), after, before, 0)
	if err != nil {
		t.Fatalf("GetOverview full: %v", err)
	}
	if dataFull.Current.InputTokens != nil || dataFull.Current.CostMicros != nil {
		t.Fatalf("expected nil for full mixed range, got tokens=%v, cost=%v", dataFull.Current.InputTokens, dataFull.Current.CostMicros)
	}
}

func TestOverviewExactBoundariesAndAdjacentPeriod(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	history := NewRequestHistoryRepository(database)
	t0 := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	after := t0
	before := t0.Add(1 * time.Hour)

	// Range length = 1 hour.
	// Current range: [t0, t0 + 1h]
	// Previous range: [t0 - 1h, t0 - 1us]
	currentStart := after.UnixMicro()
	currentEnd := before.UnixMicro()
	prevStart := after.Add(-1 * time.Hour).UnixMicro()
	prevEnd := currentStart - 1

	pts := []struct {
		id       string
		finished int64
	}{
		{"00000000000000000000000000000001", prevStart - 1},  // Before prev
		{"00000000000000000000000000000002", prevStart},      // Prev start boundary
		{"00000000000000000000000000000003", prevEnd},        // Prev end boundary
		{"00000000000000000000000000000004", currentStart},   // Current start boundary
		{"00000000000000000000000000000005", currentEnd},     // Current end boundary
		{"00000000000000000000000000000006", currentEnd + 1}, // After current
	}

	for _, pt := range pts {
		rec := HistoryRecord{
			RequestID:       pt.id,
			TerminalOutcome: "complete",
			UpstreamStarted: true,
			FinishedAt:      KnownInt64(pt.finished),
		}
		if err := history.Persist(context.Background(), rec, nil); err != nil {
			t.Fatalf("persist %s: %v", pt.id, err)
		}
	}

	repo := NewAPIKeyRepository(database)
	data, err := repo.GetOverview(context.Background(), after, before, 0)
	if err != nil {
		t.Fatalf("GetOverview failed: %v", err)
	}

	if data.Current.TotalRequests != 2 {
		t.Fatalf("current total = %d, want 2", data.Current.TotalRequests)
	}
	if data.Previous.TotalRequests != 2 {
		t.Fatalf("previous total = %d, want 2", data.Previous.TotalRequests)
	}
	if data.PreviousRangeEnd.UnixMicro() != prevEnd {
		t.Fatalf("prev range end = %d, want %d", data.PreviousRangeEnd.UnixMicro(), prevEnd)
	}
	if data.PreviousRangeStart.UnixMicro() != prevStart {
		t.Fatalf("prev range start = %d, want %d", data.PreviousRangeStart.UnixMicro(), prevStart)
	}
}

func TestOverviewRetentionGaps(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	history := NewRequestHistoryRepository(database)
	t0 := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	after := t0.Add(-24 * time.Hour)
	before := t0

	for i := 0; i < 20; i++ {
		rec := HistoryRecord{
			RequestID:       fmt.Sprintf("000000000000000000000000000000%02d", i),
			TerminalOutcome: "complete",
			UpstreamStarted: true,
			FinishedAt:      KnownInt64(t0.Add(-time.Duration(i) * time.Hour).UnixMicro()),
		}
		if err := history.Persist(context.Background(), rec, nil); err != nil {
			t.Fatalf("persist: %v", err)
		}
	}

	// Retention deletes older rows
	cutoff := t0.Add(-10 * time.Hour)
	if _, err := history.DeleteMetadataBefore(context.Background(), cutoff, 100); err != nil {
		t.Fatalf("DeleteMetadataBefore failed: %v", err)
	}

	repo := NewAPIKeyRepository(database)
	data, err := repo.GetOverview(context.Background(), after, before, 0)
	if err != nil {
		t.Fatalf("GetOverview after retention failed: %v", err)
	}

	// Only rows newer than cutoff (11 rows: 0h to 10h inclusive) should remain
	if data.Current.TotalRequests != 11 {
		t.Fatalf("current total after retention = %d, want 11", data.Current.TotalRequests)
	}
}

func TestOverviewConcurrentWrites(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	history := NewRequestHistoryRepository(database)
	repo := NewAPIKeyRepository(database)
	t0 := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	after := t0.Add(-1 * time.Hour)
	before := t0.Add(1 * time.Hour)

	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Concurrent writer
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			select {
			case <-ctx.Done():
				return
			default:
				rec := HistoryRecord{
					RequestID:       fmt.Sprintf("000000000000000000000000000001%02d", i),
					TerminalOutcome: "complete",
					UpstreamStarted: true,
					FinishedAt:      KnownInt64(t0.UnixMicro() + int64(i)),
				}
				_ = history.Persist(ctx, rec, nil)
			}
		}
	}()

	// Concurrent reader
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			select {
			case <-ctx.Done():
				return
			default:
				_, _ = repo.GetOverview(ctx, after, before, 0)
			}
		}
	}()

	wg.Wait()
}

func TestOverviewPerformanceBudget(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	t0 := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	after := t0.Add(-24 * time.Hour)
	before := t0

	// Populate 10,000 rows in batches
	tx, err := database.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	stmt, err := tx.Prepare(`INSERT INTO requests (
		request_id, terminal_outcome, upstream_started, input_tokens, output_tokens, cost_micros, started_at, finished_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer stmt.Close()

	for i := 0; i < 10000; i++ {
		id := fmt.Sprintf("000000000000000000000000%08x", i)
		fin := t0.Add(-time.Duration(i) * time.Minute).UnixMicro()
		outcome := "complete"
		upstreamStarted := 1
		if i%10 == 0 {
			outcome = "pre_upstream"
			upstreamStarted = 0
		} else if i%25 == 0 {
			outcome = "upstream_error"
		}
		if _, err := stmt.Exec(id, outcome, upstreamStarted, 100, 50, 1000, fin-500, fin); err != nil {
			t.Fatalf("exec insert %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	repo := NewAPIKeyRepository(database)

	// Measure memory allocation and latency
	var mBefore, mAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&mBefore)

	start := time.Now()
	data, err := repo.GetOverview(context.Background(), after, before, 1)
	duration := time.Since(start)

	runtime.ReadMemStats(&mAfter)
	allocatedBytes := mAfter.TotalAlloc - mBefore.TotalAlloc

	if err != nil {
		t.Fatalf("GetOverview: %v", err)
	}
	if data.Current.TotalRequests == 0 {
		t.Fatalf("expected non-zero requests")
	}

	// Acceptance criteria: p95 under 150ms, peak temporary allocation below 8 MiB per query
	if duration > 150*time.Millisecond {
		t.Fatalf("query took %v, want < 150ms", duration)
	}
	if allocatedBytes > 8*1024*1024 {
		t.Fatalf("temporary allocation was %d bytes, want < 8 MiB", allocatedBytes)
	}
	t.Logf("Performance test passed: duration=%v, alloc=%d bytes", duration, allocatedBytes)
}
