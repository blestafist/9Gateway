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

func TestUsageTimeseriesQueryPlan(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer database.Close()

	buckets := []string{"five_minutes", "hour", "day", "week", "month"}
	for _, b := range buckets {
		var bucketExpr string
		switch b {
		case "five_minutes":
			bucketExpr = "(finished_at / 300000000) * 300000000"
		case "hour":
			bucketExpr = "(finished_at / 3600000000) * 3600000000"
		case "day":
			bucketExpr = "(finished_at / 86400000000) * 86400000000"
		case "week":
			bucketExpr = "(((finished_at / 1000000 - 345600) / 604800) * 604800 + 345600) * 1000000"
		case "month":
			bucketExpr = "CAST(strftime('%s', date(finished_at / 1000000, 'unixepoch', 'start of month')) AS INTEGER) * 1000000"
		}

		query := fmt.Sprintf(`EXPLAIN QUERY PLAN
			SELECT
				%s AS b_micros,
				COUNT(*),
				COALESCE(SUM(CASE WHEN terminal_outcome IN ('complete', 'custom_dispatch') THEN 1 ELSE 0 END), 0),
				COALESCE(SUM(CASE WHEN terminal_outcome IS NULL OR terminal_outcome NOT IN ('complete', 'custom_dispatch', 'pre_upstream') THEN 1 ELSE 0 END), 0),
				COALESCE(SUM(CASE WHEN terminal_outcome = 'pre_upstream' THEN 1 ELSE 0 END), 0),
				CASE WHEN COUNT(input_tokens) < COUNT(*) THEN NULL ELSE SUM(input_tokens) END,
				CASE WHEN COUNT(cached_input_tokens) < COUNT(*) THEN NULL ELSE SUM(cached_input_tokens) END,
				CASE WHEN COUNT(output_tokens) < COUNT(*) THEN NULL ELSE SUM(output_tokens) END,
				CASE WHEN COUNT(cost_micros) < COUNT(*) THEN NULL ELSE SUM(cost_micros) END,
				COUNT(cost_micros),
				ROUND(AVG(total_micros)),
				COUNT(total_micros),
				ROUND(AVG(time_to_first_byte_micros)),
				COUNT(time_to_first_byte_micros),
				ROUND(AVG(time_to_upstream_headers_micros)),
				COUNT(time_to_upstream_headers_micros)
			FROM requests
			WHERE finished_at >= ? AND finished_at <= ?
			GROUP BY b_micros
			ORDER BY b_micros ASC`, bucketExpr)

		rows, err := database.QueryContext(context.Background(), query, 1000, 2000)
		if err != nil {
			t.Fatalf("explain query plan failed for bucket %s: %v", b, err)
		}

		var (
			id, parent, notused int
			detail              string
			usedIndex           bool
			plan                []string
		)
		for rows.Next() {
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				rows.Close()
				t.Fatalf("scan plan row: %v", err)
			}
			plan = append(plan, detail)
			if strings.Contains(detail, "USING INDEX") && strings.Contains(detail, "idx_requests_finished") {
				usedIndex = true
			}
		}
		rows.Close()

		if !usedIndex {
			t.Fatalf("expected bucket %s query to use idx_requests_finished, plan was: %v", b, plan)
		}
	}
}

func TestUsageBreakdownQueryPlan(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer database.Close()

	// 1. Untruncated total query
	totalQuery := `EXPLAIN QUERY PLAN
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

	rows, err := database.QueryContext(context.Background(), totalQuery, 1000, 2000)
	if err != nil {
		t.Fatalf("explain total query failed: %v", err)
	}
	var usedIndex bool
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if strings.Contains(detail, "USING INDEX") && strings.Contains(detail, "idx_requests_finished") {
			usedIndex = true
		}
	}
	rows.Close()
	if !usedIndex {
		t.Fatalf("expected total query to use idx_requests_finished")
	}

	// 2. Model group by query
	modelQuery := `EXPLAIN QUERY PLAN
		SELECT
			COALESCE(model, '') AS model_name,
			COUNT(*) AS total_reqs,
			COALESCE(SUM(CASE WHEN terminal_outcome IN ('complete', 'custom_dispatch') THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN terminal_outcome IS NULL OR terminal_outcome NOT IN ('complete', 'custom_dispatch', 'pre_upstream') THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN terminal_outcome = 'pre_upstream' THEN 1 ELSE 0 END), 0),
			CASE WHEN COUNT(input_tokens) < COUNT(*) THEN NULL ELSE SUM(input_tokens) END,
			CASE WHEN COUNT(cached_input_tokens) < COUNT(*) THEN NULL ELSE SUM(cached_input_tokens) END,
			CASE WHEN COUNT(output_tokens) < COUNT(*) THEN NULL ELSE SUM(output_tokens) END,
			CASE WHEN COUNT(cost_micros) < COUNT(*) THEN NULL ELSE SUM(cost_micros) END
		FROM requests
		WHERE finished_at >= ? AND finished_at <= ?
		GROUP BY model_name
		ORDER BY total_reqs DESC, model_name ASC
		LIMIT 20`

	rows, err = database.QueryContext(context.Background(), modelQuery, 1000, 2000)
	if err != nil {
		t.Fatalf("explain model query failed: %v", err)
	}
	usedIndex = false
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if strings.Contains(detail, "USING INDEX") && strings.Contains(detail, "idx_requests_finished") {
			usedIndex = true
		}
	}
	rows.Close()
	if !usedIndex {
		t.Fatalf("expected model query to use idx_requests_finished")
	}
}

func TestUsageTimeseriesUTCAndCalendarBoundaries(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	history := NewRequestHistoryRepository(database)
	repo := NewAPIKeyRepository(database)

	// Test leap year 2024 (Feb 28, Feb 29, Mar 01)
	feb28 := time.Date(2024, 2, 28, 12, 0, 0, 0, time.UTC)
	feb29 := time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC)
	mar01 := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)

	for i, ts := range []time.Time{feb28, feb29, mar01} {
		rec := HistoryRecord{
			RequestID:       fmt.Sprintf("000000000000000000000000000000%02x", i),
			TerminalOutcome: "complete",
			UpstreamStarted: true,
			CostMicros:      KnownInt64(100),
			FinishedAt:      KnownInt64(ts.UnixMicro()),
		}
		if err := history.Persist(context.Background(), rec, nil); err != nil {
			t.Fatalf("persist leap %d: %v", i, err)
		}
	}

	after := time.Date(2024, 2, 27, 0, 0, 0, 0, time.UTC)
	before := time.Date(2024, 3, 2, 0, 0, 0, 0, time.UTC)

	// Daily query
	data, err := repo.GetUsageTimeseries(context.Background(), &after, before, "day")
	if err != nil {
		t.Fatalf("daily timeseries: %v", err)
	}

	// Should contain Feb 27, Feb 28, Feb 29, Mar 01, Mar 02
	if len(data.Buckets) != 5 {
		t.Fatalf("expected 5 buckets for leap days, got %d", len(data.Buckets))
	}
	if data.Buckets[1].BucketStart.Day() != 28 || data.Buckets[1].TotalRequests != 1 {
		t.Errorf("bucket 1 should be Feb 28 with 1 request")
	}
	if data.Buckets[2].BucketStart.Day() != 29 || data.Buckets[2].TotalRequests != 1 {
		t.Errorf("bucket 2 should be Feb 29 with 1 request")
	}
	if data.Buckets[3].BucketStart.Day() != 1 || data.Buckets[3].TotalRequests != 1 {
		t.Errorf("bucket 3 should be Mar 01 with 1 request")
	}

	// Monthly query across leap year
	afterMonth := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	beforeMonth := time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)
	monthData, err := repo.GetUsageTimeseries(context.Background(), &afterMonth, beforeMonth, "month")
	if err != nil {
		t.Fatalf("monthly timeseries: %v", err)
	}
	// Buckets: Jan, Feb, Mar, Apr
	if len(monthData.Buckets) != 4 {
		t.Fatalf("expected 4 monthly buckets, got %d", len(monthData.Buckets))
	}
	// Feb has 2 requests (Feb 28, 29)
	if monthData.Buckets[1].TotalRequests != 2 {
		t.Errorf("Feb should have 2 requests, got %d", monthData.Buckets[1].TotalRequests)
	}
	// Mar has 1 request (Mar 01)
	if monthData.Buckets[2].TotalRequests != 1 {
		t.Errorf("Mar should have 1 request, got %d", monthData.Buckets[2].TotalRequests)
	}
}

func TestUsageTimeseriesMissingBucketsAndNullableCost(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	history := NewRequestHistoryRepository(database)
	repo := NewAPIKeyRepository(database)

	baseTime := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	// Request 1: at 12:10 with Known Cost = 500
	rec1 := HistoryRecord{
		RequestID:             "00000000000000000000000000000001",
		TerminalOutcome:       "complete",
		UpstreamStarted:       true,
		InputTokens:           KnownInt64(4),
		CachedInputTokens:     KnownInt64(1),
		OutputTokens:          KnownInt64(5),
		CostMicros:            KnownInt64(500),
		TotalMicros:           KnownInt64(20000),
		TimeToFirstByteMicros: KnownInt64(10000),
		FinishedAt:            KnownInt64(baseTime.Add(10 * time.Minute).UnixMicro()),
	}
	if err := history.Persist(context.Background(), rec1, nil); err != nil {
		t.Fatal(err)
	}

	// Request 2: at 14:10 with Unknown Cost (NULL) and No Latency (pre_upstream)
	rec2 := HistoryRecord{
		RequestID:       "00000000000000000000000000000002",
		TerminalOutcome: "pre_upstream",
		UpstreamStarted: false,
		FinishedAt:      KnownInt64(baseTime.Add(130 * time.Minute).UnixMicro()),
	}
	if err := history.Persist(context.Background(), rec2, nil); err != nil {
		t.Fatal(err)
	}

	after := baseTime
	before := baseTime.Add(3 * time.Hour) // 12:00 to 15:00

	data, err := repo.GetUsageTimeseries(context.Background(), &after, before, "hour")
	if err != nil {
		t.Fatalf("GetUsageTimeseries: %v", err)
	}

	// Expected hourly buckets: 12:00, 13:00, 14:00, 15:00
	if len(data.Buckets) != 4 {
		t.Fatalf("expected 4 buckets, got %d", len(data.Buckets))
	}

	// Bucket 0 (12:00 - 13:00): has rec1
	b0 := data.Buckets[0]
	if b0.TotalRequests != 1 || b0.SuccessfulRequests != 1 {
		t.Errorf("b0 total=%d succ=%d, want 1, 1", b0.TotalRequests, b0.SuccessfulRequests)
	}
	if b0.InputTokens == nil || *b0.InputTokens != 4 || b0.CachedInputTokens == nil || *b0.CachedInputTokens != 1 || b0.OutputTokens == nil || *b0.OutputTokens != 5 {
		t.Errorf("b0 tokens input=%v cached=%v output=%v, want 4, 1, 5", b0.InputTokens, b0.CachedInputTokens, b0.OutputTokens)
	}
	if b0.CostMicros == nil || *b0.CostMicros != 500 {
		t.Errorf("b0 cost=%v, want 500", b0.CostMicros)
	}
	if b0.AvgTotalLatencyMicros == nil || *b0.AvgTotalLatencyMicros != 20000 || b0.TotalLatencySamples != 1 {
		t.Errorf("b0 latency=%v samples=%d, want 20000, 1", b0.AvgTotalLatencyMicros, b0.TotalLatencySamples)
	}

	// Bucket 1 (13:00 - 14:00): empty bucket (missing in data, explicitly filled)
	b1 := data.Buckets[1]
	if b1.TotalRequests != 0 {
		t.Errorf("b1 should be empty, got %d", b1.TotalRequests)
	}
	if b1.CostMicros == nil || *b1.CostMicros != 0 {
		t.Errorf("b1 cost should be known zero, got %v", b1.CostMicros)
	}
	if b1.AvgTotalLatencyMicros != nil || b1.TotalLatencySamples != 0 {
		t.Errorf("b1 latency should be nil with 0 samples, got %v, %d", b1.AvgTotalLatencyMicros, b1.TotalLatencySamples)
	}

	// Bucket 2 (14:00 - 15:00): has rec2 (pre_upstream, null cost, null latency)
	b2 := data.Buckets[2]
	if b2.TotalRequests != 1 || b2.RejectedRequests != 1 || b2.ErrorRequests != 0 {
		t.Errorf("b2 total=%d error=%d rej=%d, want 1, 0, 1", b2.TotalRequests, b2.ErrorRequests, b2.RejectedRequests)
	}
	if b2.InputTokens != nil || b2.CachedInputTokens != nil || b2.OutputTokens != nil {
		t.Errorf("b2 token sums should be nil for unknown usage, got input=%v cached=%v output=%v", b2.InputTokens, b2.CachedInputTokens, b2.OutputTokens)
	}
	if b2.CostMicros != nil {
		t.Errorf("b2 cost should be nil (unknown), got %v", *b2.CostMicros)
	}
	if b2.AvgTotalLatencyMicros != nil || b2.TotalLatencySamples != 0 {
		t.Errorf("b2 latency should be nil with 0 samples, got %v, %d", b2.AvgTotalLatencyMicros, b2.TotalLatencySamples)
	}
}

func TestUsageTimeseriesAutoBucketAndValidation(t *testing.T) {
	// Test SelectAutoBucket
	if b := SelectAutoBucket(2 * time.Hour); b != "five_minutes" {
		t.Errorf("2h auto bucket = %s, want five_minutes", b)
	}
	if b := SelectAutoBucket(24 * time.Hour); b != "five_minutes" {
		t.Errorf("24h auto bucket = %s, want five_minutes", b)
	}
	if b := SelectAutoBucket(7 * 24 * time.Hour); b != "hour" {
		t.Errorf("7d auto bucket = %s, want hour", b)
	}
	if b := SelectAutoBucket(31 * 24 * time.Hour); b != "hour" {
		t.Errorf("31d auto bucket = %s, want hour", b)
	}
	if b := SelectAutoBucket(90 * 24 * time.Hour); b != "day" {
		t.Errorf("90d auto bucket = %s, want day", b)
	}
	if b := SelectAutoBucket(3 * 365 * 24 * time.Hour); b != "week" {
		t.Errorf("3y auto bucket = %s, want week", b)
	}
	if b := SelectAutoBucket(15 * 365 * 24 * time.Hour); b != "month" {
		t.Errorf("15y auto bucket = %s, want month", b)
	}

	// Test ValidateBucketRange over-detailed rejection
	if err := ValidateBucketRange(25*time.Hour, "five_minutes"); err == nil {
		t.Errorf("expected error for five_minutes over 24h")
	}
	if err := ValidateBucketRange(32*24*time.Hour, "hour"); err == nil {
		t.Errorf("expected error for hour over 31d")
	}
	if err := ValidateBucketRange(3*366*24*time.Hour, "day"); err == nil {
		t.Errorf("expected error for day over 2y")
	}
	if err := ValidateBucketRange(11*366*24*time.Hour, "week"); err == nil {
		t.Errorf("expected error for week over 10y")
	}
	if err := ValidateBucketRange(10*time.Hour, "invalid_bucket"); err != ErrInvalidBucket {
		t.Errorf("expected ErrInvalidBucket, got %v", err)
	}
}

func TestUsageBreakdownTop20AndOther(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	history := NewRequestHistoryRepository(database)
	repo := NewAPIKeyRepository(database)

	baseTime := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	after := baseTime.Add(-1 * time.Hour)
	before := baseTime.Add(1 * time.Hour)

	// Insert requests across 25 different models
	for i := 0; i < 25; i++ {
		modelName := fmt.Sprintf("model-%02d", i)
		count := i + 1 // model-24 has 25 requests, model-00 has 1 request
		for j := 0; j < count; j++ {
			rec := HistoryRecord{
				RequestID:       fmt.Sprintf("%02d%02d0000000000000000000000000000", i, j),
				Model:           modelName,
				TerminalOutcome: "complete",
				UpstreamStarted: true,
				InputTokens:     KnownInt64(10),
				OutputTokens:    KnownInt64(5),
				CostMicros:      KnownInt64(100),
				FinishedAt:      KnownInt64(baseTime.UnixMicro() + int64(i*100+j)),
			}
			if err := history.Persist(context.Background(), rec, nil); err != nil {
				t.Fatal(err)
			}
		}
	}

	data, err := repo.GetUsageBreakdown(context.Background(), &after, before, "model")
	if err != nil {
		t.Fatalf("GetUsageBreakdown: %v", err)
	}

	// Must return exactly 20 rows
	if len(data.Rows) != 20 {
		t.Fatalf("expected 20 rows, got %d", len(data.Rows))
	}

	// Top row should be model-24 with 25 requests
	if data.Rows[0].ID != "model-24" || data.Rows[0].TotalRequests != 25 {
		t.Errorf("top row is %s (%d requests), want model-24 (25)", data.Rows[0].ID, data.Rows[0].TotalRequests)
	}

	// Row 19 should be model-05 with 6 requests
	if data.Rows[19].ID != "model-05" || data.Rows[19].TotalRequests != 6 {
		t.Errorf("row 19 is %s (%d requests), want model-05 (6)", data.Rows[19].ID, data.Rows[19].TotalRequests)
	}

	// The remaining 5 models (model-00 to model-04) should be in Other
	// Sum of requests for model-00 through model-04: 1 + 2 + 3 + 4 + 5 = 15
	if data.Other.TotalRequests != 15 {
		t.Errorf("other requests = %d, want 15", data.Other.TotalRequests)
	}

	// Verify that sum(rows) + other equals untruncated Total!
	var (
		sumReq   int64
		sumInput int64
		sumCost  int64
	)
	for _, r := range data.Rows {
		sumReq += r.TotalRequests
		if r.InputTokens != nil {
			sumInput += *r.InputTokens
		}
		if r.CostMicros != nil {
			sumCost += *r.CostMicros
		}
	}
	sumReq += data.Other.TotalRequests
	if data.Other.InputTokens != nil {
		sumInput += *data.Other.InputTokens
	}
	if data.Other.CostMicros != nil {
		sumCost += *data.Other.CostMicros
	}

	if sumReq != data.Total.TotalRequests {
		t.Errorf("sumReq %d != total %d", sumReq, data.Total.TotalRequests)
	}
	if data.Total.InputTokens == nil || sumInput != *data.Total.InputTokens {
		t.Errorf("sumInput %d != total %v", sumInput, data.Total.InputTokens)
	}
	if data.Total.CostMicros == nil || sumCost != *data.Total.CostMicros {
		t.Errorf("sumCost %d != total %v", sumCost, data.Total.CostMicros)
	}
}

func TestUsageBreakdownKeyLabelsAndDeletion(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	keys := storageNewAPIKeyRepository(database)
	history := NewRequestHistoryRepository(database)

	baseTime := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	after := baseTime.Add(-1 * time.Hour)
	before := baseTime.Add(1 * time.Hour)

	// Create 2 keys, then delete the second one
	activeKeyID := "key-0123456789abcdef0123456789abcdef"
	deletedKeyID := "key-9999999999abcdef0123456789abcdef"
	activeKey := APIKeyRecord{
		ID:            activeKeyID,
		Name:          "active-key",
		Digest:        bytesOf(1),
		DisplayPrefix: "prefix1",
		Enabled:       true,
		CreatedAt:     time.Unix(baseTime.Unix(), 0).UTC(),
		UpdatedAt:     time.Unix(baseTime.Unix(), 0).UTC(),
	}
	deletedKey := APIKeyRecord{
		ID:            deletedKeyID,
		Name:          "old-deleted-key",
		Digest:        bytesOf(2),
		DisplayPrefix: "prefix2",
		Enabled:       true,
		CreatedAt:     time.Unix(baseTime.Unix(), 0).UTC(),
		UpdatedAt:     time.Unix(baseTime.Unix(), 0).UTC(),
	}
	if err := keys.Insert(context.Background(), activeKey); err != nil {
		t.Fatal(err)
	}
	if err := keys.Insert(context.Background(), deletedKey); err != nil {
		t.Fatal(err)
	}

	// 1. Request with active key
	rec1 := HistoryRecord{
		RequestID:       "00000000000000000000000000000001",
		APIKeyID:        activeKey.ID,
		KeyName:         activeKey.Name,
		TerminalOutcome: "complete",
		UpstreamStarted: true,
		FinishedAt:      KnownInt64(baseTime.UnixMicro() + 1),
	}
	if err := history.Persist(context.Background(), rec1, nil); err != nil {
		t.Fatal(err)
	}

	// 2. Request with deleted key (no key ID, but has key name)
	rec2 := HistoryRecord{
		RequestID:       "00000000000000000000000000000002",
		APIKeyID:        deletedKeyID,
		KeyName:         "old-deleted-key",
		TerminalOutcome: "complete",
		UpstreamStarted: true,
		FinishedAt:      KnownInt64(baseTime.UnixMicro() + 2),
	}
	if err := history.Persist(context.Background(), rec2, nil); err != nil {
		t.Fatal(err)
	}

	// Now delete the deletedKey directly from database.
	// In SQLite with foreign keys, requests.api_key_id is ON DELETE SET NULL, so requests.api_key_id becomes NULL,
	// and requests.key_name retains "old-deleted-key".
	if _, err := database.Exec(`DELETE FROM api_keys WHERE id = ?`, deletedKeyID); err != nil {
		t.Fatal(err)
	}

	// 3. Request with unknown key (unauthenticated)
	rec3 := HistoryRecord{
		RequestID:       "00000000000000000000000000000003",
		TerminalOutcome: "pre_upstream",
		UpstreamStarted: false,
		FinishedAt:      KnownInt64(baseTime.UnixMicro() + 3),
	}
	if err := history.Persist(context.Background(), rec3, nil); err != nil {
		t.Fatal(err)
	}

	data, err := keys.GetUsageBreakdown(context.Background(), &after, before, "key")
	if err != nil {
		t.Fatalf("GetUsageBreakdown: %v", err)
	}

	if len(data.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(data.Rows))
	}

	for _, r := range data.Rows {
		switch r.ID {
		case activeKey.ID:
			if r.Name != "active-key" || r.IsDeleted || r.IsUnknown || r.KeyID == nil || *r.KeyID != activeKey.ID {
				t.Errorf("active key row mismatch: %+v", r)
			}
		case "deleted:old-deleted-key":
			if r.Name != "old-deleted-key" || !r.IsDeleted || r.IsUnknown || r.KeyID != nil {
				t.Errorf("deleted key row mismatch: %+v", r)
			}
		case "unknown":
			if r.Name != "unknown" || !r.IsUnknown || r.IsDeleted || r.KeyID != nil {
				t.Errorf("unknown key row mismatch: %+v", r)
			}
		default:
			t.Errorf("unexpected row ID: %s", r.ID)
		}
	}
}

func storageNewAPIKeyRepository(db dbQueries) *APIKeyRepository {
	return NewAPIKeyRepository(db)
}

func TestUsageRetentionLimitedMetadata(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	history := NewRequestHistoryRepository(database)
	repo := NewAPIKeyRepository(database)

	t0 := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		rec := HistoryRecord{
			RequestID:       fmt.Sprintf("000000000000000000000000000000%02x", i),
			TerminalOutcome: "complete",
			UpstreamStarted: true,
			FinishedAt:      KnownInt64(t0.Add(time.Duration(i) * time.Hour).UnixMicro()),
		}
		if err := history.Persist(context.Background(), rec, nil); err != nil {
			t.Fatal(err)
		}
	}

	// 1. All retained query before any deletion
	data1, err := repo.GetUsageTimeseries(context.Background(), nil, t0.Add(12*time.Hour), "hour")
	if err != nil {
		t.Fatal(err)
	}
	if data1.RetentionLimited {
		t.Errorf("expected RetentionLimited = false before retention runs")
	}

	// Run retention delete
	cutoff := t0.Add(4 * time.Hour)
	deleted, err := history.DeleteMetadataBefore(context.Background(), cutoff, 100)
	if err != nil || deleted == 0 {
		t.Fatalf("retention delete failed: deleted=%d, err=%v", deleted, err)
	}

	// 2. All retained query after retention
	data2, err := repo.GetUsageTimeseries(context.Background(), nil, t0.Add(12*time.Hour), "hour")
	if err != nil {
		t.Fatal(err)
	}
	if !data2.RetentionLimited {
		t.Errorf("expected RetentionLimited = true after retention has deleted rows")
	}
}

func TestUsageConcurrentWrites(t *testing.T) {
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

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 40; i++ {
			select {
			case <-ctx.Done():
				return
			default:
				rec := HistoryRecord{
					RequestID:       fmt.Sprintf("cw-%02d0000000000000000000000000000", i),
					TerminalOutcome: "complete",
					UpstreamStarted: true,
					FinishedAt:      KnownInt64(t0.UnixMicro() + int64(i)),
				}
				_ = history.Persist(ctx, rec, nil)
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			select {
			case <-ctx.Done():
				return
			default:
				_, _ = repo.GetUsageTimeseries(ctx, &after, before, "hour")
				_, _ = repo.GetUsageBreakdown(ctx, &after, before, "model")
			}
		}
	}()

	wg.Wait()
}

func TestUsageContextCancellation(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	repo := NewAPIKeyRepository(database)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled

	now := time.Now().UTC()
	after := now.Add(-24 * time.Hour)

	_, err = repo.GetUsageTimeseries(ctx, &after, now, "hour")
	if err == nil {
		t.Fatalf("expected error on cancelled context")
	}

	_, err = repo.GetUsageBreakdown(ctx, &after, now, "model")
	if err == nil {
		t.Fatalf("expected error on cancelled context")
	}
}

func TestUsagePerformanceBudget(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	t0 := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

	// Populate 10,000 rows across 1 year
	tx, err := database.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	stmt, err := tx.Prepare(`INSERT INTO requests (
		request_id, model, terminal_outcome, upstream_started, input_tokens, output_tokens, cost_micros, started_at, finished_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer stmt.Close()

	for i := 0; i < 10000; i++ {
		id := fmt.Sprintf("%032x", i)
		model := fmt.Sprintf("model-%d", i%10)
		fin := t0.Add(-time.Duration(i*50) * time.Minute).UnixMicro()
		outcome := "complete"
		upstreamStarted := 1
		if i%20 == 0 {
			outcome = "pre_upstream"
			upstreamStarted = 0
		}
		if _, err := stmt.Exec(id, model, outcome, upstreamStarted, 100, 50, 1000, fin-500, fin); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	repo := NewAPIKeyRepository(database)

	// Test 1: 1-year daily query
	oneYearAgo := t0.AddDate(-1, 0, 0)
	var mBefore, mAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&mBefore)

	start := time.Now()
	dataYear, err := repo.GetUsageTimeseries(context.Background(), &oneYearAgo, t0, "day")
	duration := time.Since(start)

	runtime.ReadMemStats(&mAfter)
	allocBytes := mAfter.TotalAlloc - mBefore.TotalAlloc

	if err != nil {
		t.Fatalf("1-year daily query failed: %v", err)
	}
	if len(dataYear.Buckets) == 0 {
		t.Fatalf("expected non-empty 1-year daily buckets")
	}
	if duration > 250*time.Millisecond {
		t.Fatalf("1-year daily query took %v, want < 250ms", duration)
	}
	if allocBytes > 12*1024*1024 {
		t.Fatalf("1-year daily allocation %d bytes, want < 12 MiB", allocBytes)
	}

	// Test 2: 31-day hourly query
	monthAgo := t0.Add(-31 * 24 * time.Hour)
	runtime.GC()
	runtime.ReadMemStats(&mBefore)
	start = time.Now()
	dataMonth, err := repo.GetUsageTimeseries(context.Background(), &monthAgo, t0, "hour")
	duration = time.Since(start)
	runtime.ReadMemStats(&mAfter)
	allocBytes = mAfter.TotalAlloc - mBefore.TotalAlloc

	if err != nil {
		t.Fatalf("31-day hourly query failed: %v", err)
	}
	if len(dataMonth.Buckets) == 0 {
		t.Fatalf("expected non-empty 31-day hourly buckets")
	}
	if duration > 250*time.Millisecond {
		t.Fatalf("31-day hourly query took %v, want < 250ms", duration)
	}
	if allocBytes > 12*1024*1024 {
		t.Fatalf("31-day hourly allocation %d bytes, want < 12 MiB", allocBytes)
	}

	// Test 3: All-retained monthly query
	runtime.GC()
	runtime.ReadMemStats(&mBefore)
	start = time.Now()
	dataAll, err := repo.GetUsageTimeseries(context.Background(), nil, t0, "month")
	duration = time.Since(start)
	runtime.ReadMemStats(&mAfter)
	allocBytes = mAfter.TotalAlloc - mBefore.TotalAlloc

	if err != nil {
		t.Fatalf("all-retained monthly query failed: %v", err)
	}
	if len(dataAll.Buckets) == 0 {
		t.Fatalf("expected non-empty all-retained monthly buckets")
	}
	if duration > 250*time.Millisecond {
		t.Fatalf("all-retained monthly query took %v, want < 250ms", duration)
	}
	if allocBytes > 12*1024*1024 {
		t.Fatalf("all-retained monthly allocation %d bytes, want < 12 MiB", allocBytes)
	}

	// Test 4: Breakdown query performance
	runtime.GC()
	runtime.ReadMemStats(&mBefore)
	start = time.Now()
	dataBreakdown, err := repo.GetUsageBreakdown(context.Background(), &oneYearAgo, t0, "model")
	duration = time.Since(start)
	runtime.ReadMemStats(&mAfter)
	allocBytes = mAfter.TotalAlloc - mBefore.TotalAlloc

	if err != nil {
		t.Fatalf("breakdown query failed: %v", err)
	}
	if len(dataBreakdown.Rows) == 0 {
		t.Fatalf("expected non-empty breakdown rows")
	}
	if duration > 250*time.Millisecond {
		t.Fatalf("breakdown query took %v, want < 250ms", duration)
	}
	if allocBytes > 12*1024*1024 {
		t.Fatalf("breakdown allocation %d bytes, want < 12 MiB", allocBytes)
	}
}
