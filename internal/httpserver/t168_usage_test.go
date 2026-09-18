package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/storage"
	"github.com/pestit/9gateway/internal/transport"
)

func TestUsageAuthenticationAndMethods(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	keys := storage.NewAPIKeyRepository(database)
	handler, err := NewHandlerWithAdmin(transport.NewClient(), "http://127.0.0.1:1", "upstream", "admin-secret", "pepper", keys)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	client := server.Client()

	endpoints := []string{
		"/admin/v1/usage/timeseries",
		"/admin/v1/usage/breakdown?group_by=model",
	}

	for _, ep := range endpoints {
		// 1. Missing Authorization -> 401
		req, _ := http.NewRequest(http.MethodGet, server.URL+ep, nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s: expected 401 for unauthenticated request, got %d", ep, resp.StatusCode)
		}

		// 2. Wrong Bearer -> 401
		req, _ = http.NewRequest(http.MethodGet, server.URL+ep, nil)
		req.Header.Set("Authorization", "Bearer wrong-token")
		resp, err = client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s: expected 401 for wrong token, got %d", ep, resp.StatusCode)
		}

		// 3. Valid Bearer -> 200
		req, _ = http.NewRequest(http.MethodGet, server.URL+ep, nil)
		req.Header.Set("Authorization", "Bearer admin-secret")
		resp, err = client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: expected 200 for valid token, got %d", ep, resp.StatusCode)
		}

		// 4. Method not allowed (POST, PUT, DELETE, PATCH) -> 405
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
			req, _ = http.NewRequest(method, server.URL+ep, nil)
			req.Header.Set("Authorization", "Bearer admin-secret")
			resp, err = client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("%s: expected 405 for %s, got %d", ep, method, resp.StatusCode)
			}
		}
	}
}

func TestUsageTimeseriesValidation(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	keys := storage.NewAPIKeyRepository(database)
	handler, err := NewHandlerWithAdmin(transport.NewClient(), "http://127.0.0.1:1", "upstream", "admin-secret", "pepper", keys)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	client := server.Client()
	get := func(query string) (*http.Response, map[string]any) {
		req, _ := http.NewRequest(http.MethodGet, server.URL+"/admin/v1/usage/timeseries"+query, nil)
		req.Header.Set("Authorization", "Bearer admin-secret")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return resp, body
	}

	// 1. Invalid RFC3339 after
	resp, body := get("?after=not-rfc3339")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid after status = %d, want 400", resp.StatusCode)
	}
	if body["error"].(map[string]any)["code"] != "invalid_request" {
		t.Fatalf("expected code invalid_request, got %v", body)
	}

	// 2. Invalid RFC3339 before
	resp, _ = get("?before=2026-13-45T99:99:99Z")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid before status = %d, want 400", resp.StatusCode)
	}

	// 3. after >= before
	resp, _ = get("?after=2026-09-02T00:00:00Z&before=2026-09-01T00:00:00Z")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("after >= before status = %d, want 400", resp.StatusCode)
	}
	resp, _ = get("?after=2026-09-01T00:00:00Z&before=2026-09-01T00:00:00Z")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("after == before status = %d, want 400", resp.StatusCode)
	}

	// 4. Unsupported query param
	resp, _ = get("?unknown_param=val")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unsupported param status = %d, want 400", resp.StatusCode)
	}

	// 5. Duplicate query param
	resp, _ = get("?bucket=hour&bucket=day")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("duplicate param status = %d, want 400", resp.StatusCode)
	}

	// 6. Invalid bucket name
	resp, _ = get("?bucket=second")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid bucket status = %d, want 400", resp.StatusCode)
	}

	// 7. Over-detailed combinations
	// five_minutes > 24 hours
	resp, _ = get("?after=2026-09-01T00:00:00Z&before=2026-09-02T01:00:00Z&bucket=five_minutes")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("five_minutes > 24h status = %d, want 400", resp.StatusCode)
	}

	// hour > 31 days
	resp, _ = get("?after=2026-08-01T00:00:00Z&before=2026-09-02T00:00:00Z&bucket=hour")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("hour > 31d status = %d, want 400", resp.StatusCode)
	}

	// day > 2 years
	resp, _ = get("?after=2024-01-01T00:00:00Z&before=2026-09-01T00:00:00Z&bucket=day")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("day > 2y status = %d, want 400", resp.StatusCode)
	}

	// week > 10 years
	resp, _ = get("?after=2015-01-01T00:00:00Z&before=2026-09-01T00:00:00Z&bucket=week")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("week > 10y status = %d, want 400", resp.StatusCode)
	}
}

func TestUsageBreakdownValidation(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	keys := storage.NewAPIKeyRepository(database)
	handler, err := NewHandlerWithAdmin(transport.NewClient(), "http://127.0.0.1:1", "upstream", "admin-secret", "pepper", keys)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	client := server.Client()
	get := func(query string) (*http.Response, map[string]any) {
		req, _ := http.NewRequest(http.MethodGet, server.URL+"/admin/v1/usage/breakdown"+query, nil)
		req.Header.Set("Authorization", "Bearer admin-secret")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return resp, body
	}

	// 1. Missing group_by
	resp, body := get("")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing group_by status = %d, want 400", resp.StatusCode)
	}
	if body["error"].(map[string]any)["code"] != "invalid_request" {
		t.Fatalf("expected code invalid_request, got %v", body)
	}

	// 2. Invalid group_by
	resp, _ = get("?group_by=client")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid group_by status = %d, want 400", resp.StatusCode)
	}

	// 3. Unsupported query param
	resp, _ = get("?group_by=model&extra=1")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unsupported param status = %d, want 400", resp.StatusCode)
	}

	// 4. Duplicate query param
	resp, _ = get("?group_by=model&group_by=key")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("duplicate group_by status = %d, want 400", resp.StatusCode)
	}

	// 5. Invalid after / before
	resp, _ = get("?group_by=model&after=bad")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad after status = %d, want 400", resp.StatusCode)
	}
	resp, _ = get("?group_by=model&after=2026-09-02T00:00:00Z&before=2026-09-01T00:00:00Z")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("after >= before status = %d, want 400", resp.StatusCode)
	}
}

func TestUsageSuccessResponsesAndBoundedMetadata(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	// Seed test data: 1 api key and 10 requests spanning various outcomes/models/latencies
	t0 := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	keysRepo := storage.NewAPIKeyRepository(database)

	activeKeyID := "key-prod-1"
	gen, err := auth.GenerateGatewayKey([]byte("secret-" + activeKeyID))
	if err != nil {
		t.Fatal(err)
	}

	keyRecord := storage.APIKeyRecord{
		ID:            activeKeyID,
		Name:          "Production Key",
		Digest:        gen.Digest,
		DisplayPrefix: gen.DisplayPrefix,
		CreatedAt:     time.Unix(t0.Add(-2*time.Hour).Unix(), 0).UTC(),
		UpdatedAt:     time.Unix(t0.Add(-2*time.Hour).Unix(), 0).UTC(),
		Enabled:       true,
		PolicyJSON:    `{}`,
	}
	if err := keysRepo.Insert(context.Background(), keyRecord); err != nil {
		t.Fatalf("insert key: %v", err)
	}

	// Insert requests
	insertReq := `INSERT INTO requests (
		request_id, api_key_id, key_name, model, terminal_outcome, upstream_started,
		input_tokens, cached_input_tokens, output_tokens, cost_micros,
		total_micros, time_to_first_byte_micros, time_to_upstream_headers_micros,
		started_at, finished_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	for i := 0; i < 10; i++ {
		fin := t0.Add(-time.Duration(i*5) * time.Minute).UnixMicro()
		start := fin - 100000 // 100ms
		model := "gpt-4o"
		if i%2 == 1 {
			model = "claude-3-5-sonnet"
		}
		outcome := "complete"
		keyID := activeKeyID
		keyName := "Production Key"
		upstreamStarted := 1
		var cost *int64
		if i%3 != 0 {
			c := int64(1000 + i*100)
			cost = &c
		}

		if i == 8 {
			outcome = "pre_upstream"
			upstreamStarted = 0
			keyID = ""
			keyName = "Old Deleted Key"
		} else if i == 9 {
			outcome = "upstream_error"
			keyID = ""
			keyName = ""
		}

		var costVal any
		if cost != nil {
			costVal = *cost
		}

		var keyIDVal any
		if keyID != "" {
			keyIDVal = keyID
		}
		var keyNameVal any
		if keyName != "" {
			keyNameVal = keyName
		}

		_, err := database.ExecContext(context.Background(), insertReq,
			fmt.Sprintf("%032x", i+1),
			keyIDVal,
			keyNameVal,
			model,
			outcome,
			upstreamStarted,
			100+i*10,
			10,
			50+i*5,
			costVal,
			100000,
			40000,
			50000,
			start,
			fin,
		)
		if err != nil {
			t.Fatalf("insert req %d: %v", i, err)
		}
	}

	handler, err := NewHandlerWithAdmin(transport.NewClient(), "http://127.0.0.1:1", "upstream", "admin-secret", "pepper", keysRepo)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	client := server.Client()
	get := func(urlPath string) (*http.Response, map[string]any) {
		req, _ := http.NewRequest(http.MethodGet, server.URL+urlPath, nil)
		req.Header.Set("Authorization", "Bearer admin-secret")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return resp, body
	}

	// 1. Timeseries success with default (all-retained)
	resp, body := get("/admin/v1/usage/timeseries")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("timeseries status = %d, want 200", resp.StatusCode)
	}
	if body["requested_after"] != nil {
		t.Fatalf("expected requested_after to be null for all-retained, got %v", body["requested_after"])
	}
	if body["effective_after"] == nil || body["before"] == nil {
		t.Fatalf("expected effective_after and before timestamps, got %v", body)
	}
	if body["bucket"] != "five_minutes" {
		t.Fatalf("expected auto bucket five_minutes for ~45m duration, got %v", body["bucket"])
	}
	buckets, ok := body["buckets"].([]any)
	if !ok || len(buckets) == 0 {
		t.Fatalf("expected non-empty buckets array, got %v", body["buckets"])
	}
	if len(buckets) > 1000 {
		t.Fatalf("buckets length %d exceeds 1000 cap", len(buckets))
	}

	// Verify bucket structure on first non-empty bucket
	var foundSampledBucket bool
	for _, b := range buckets {
		bMap := b.(map[string]any)
		if bMap["total_requests"].(float64) > 0 {
			foundSampledBucket = true
			if bMap["bucket_start"] == nil || bMap["bucket_end"] == nil {
				t.Fatalf("missing bucket start/end: %v", bMap)
			}
			if bMap["total_latency_samples"].(float64) <= 0 && bMap["rejected_requests"].(float64) == 0 {
				t.Fatalf("expected total latency samples > 0, got %v", bMap)
			}
			break
		}
	}
	if !foundSampledBucket {
		t.Fatalf("expected at least one bucket with requests")
	}

	// 2. Breakdown group_by=model
	resp, body = get("/admin/v1/usage/breakdown?group_by=model")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("breakdown model status = %d, want 200", resp.StatusCode)
	}
	if body["group_by"] != "model" {
		t.Fatalf("expected group_by=model, got %v", body["group_by"])
	}
	rows := body["rows"].([]any)
	if len(rows) > 20 {
		t.Fatalf("breakdown rows %d exceeds 20 cap", len(rows))
	}
	total := body["total"].(map[string]any)
	other := body["other"].(map[string]any)
	if total["total_requests"].(float64) != 10 {
		t.Fatalf("expected total_requests = 10, got %v", total["total_requests"])
	}

	// Verify sum of rows + other == total
	var sumReqs float64
	var sumInputTokens float64
	var sumOutputTokens float64
	for _, r := range rows {
		rMap := r.(map[string]any)
		sumReqs += rMap["total_requests"].(float64)
		sumInputTokens += rMap["input_tokens"].(float64)
		sumOutputTokens += rMap["output_tokens"].(float64)
	}
	sumReqs += other["total_requests"].(float64)
	sumInputTokens += other["input_tokens"].(float64)
	sumOutputTokens += other["output_tokens"].(float64)

	if sumReqs != total["total_requests"].(float64) {
		t.Fatalf("sum of requests %v != total %v", sumReqs, total["total_requests"])
	}
	if sumInputTokens != total["input_tokens"].(float64) {
		t.Fatalf("sum of input tokens %v != total %v", sumInputTokens, total["input_tokens"])
	}
	if sumOutputTokens != total["output_tokens"].(float64) {
		t.Fatalf("sum of output tokens %v != total %v", sumOutputTokens, total["output_tokens"])
	}

	// 3. Breakdown group_by=key
	resp, body = get("/admin/v1/usage/breakdown?group_by=key")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("breakdown key status = %d, want 200", resp.StatusCode)
	}
	keyRows := body["rows"].([]any)
	var foundActive, foundDeleted, foundUnknown bool
	for _, r := range keyRows {
		rMap := r.(map[string]any)
		if rMap["id"] == activeKeyID {
			foundActive = true
			if rMap["is_deleted"] != false || rMap["is_unknown"] != false {
				t.Fatalf("active key flags unexpected: %v", rMap)
			}
		}
		if rMap["is_deleted"] == true {
			foundDeleted = true
		}
		if rMap["is_unknown"] == true {
			foundUnknown = true
		}
	}
	if !foundActive {
		t.Fatalf("expected active key-1 in breakdown rows")
	}
	if !foundDeleted {
		t.Fatalf("expected deleted key in breakdown rows")
	}
	if !foundUnknown {
		t.Fatalf("expected unknown key in breakdown rows")
	}

	// 4. Breakdown group_by=outcome
	resp, body = get("/admin/v1/usage/breakdown?group_by=outcome")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("breakdown outcome status = %d, want 200", resp.StatusCode)
	}
	outcomeRows := body["rows"].([]any)
	var foundComplete, foundPreUpstream bool
	for _, r := range outcomeRows {
		rMap := r.(map[string]any)
		if rMap["id"] == "complete" {
			foundComplete = true
		}
		if rMap["id"] == "pre_upstream" {
			foundPreUpstream = true
		}
	}
	if !foundComplete || !foundPreUpstream {
		t.Fatalf("expected complete and pre_upstream in outcome rows, got: %v", outcomeRows)
	}
}

type blockingUsageRepository struct {
	storage.UsageRepository
	storage.OverviewRepository
	inFlight atomic.Int32
	gateHold chan struct{}
}

func (b *blockingUsageRepository) GetOverview(ctx context.Context, after, before time.Time, active int64) (*storage.OverviewData, error) {
	b.inFlight.Add(1)
	defer b.inFlight.Add(-1)
	select {
	case <-b.gateHold:
		return &storage.OverviewData{CurrentRangeStart: after, CurrentRangeEnd: before}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *blockingUsageRepository) GetUsageTimeseries(ctx context.Context, reqAfter *time.Time, before time.Time, bucket string) (*storage.UsageTimeseriesData, error) {
	b.inFlight.Add(1)
	defer b.inFlight.Add(-1)
	select {
	case <-b.gateHold:
		return &storage.UsageTimeseriesData{Before: before, Bucket: bucket}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *blockingUsageRepository) GetUsageBreakdown(ctx context.Context, reqAfter *time.Time, before time.Time, groupBy string) (*storage.UsageBreakdownData, error) {
	b.inFlight.Add(1)
	defer b.inFlight.Add(-1)
	select {
	case <-b.gateHold:
		return &storage.UsageBreakdownData{Before: before, GroupBy: groupBy}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *blockingUsageRepository) List(ctx context.Context) ([]storage.APIKeyRecord, error) {
	return nil, nil
}
func (b *blockingUsageRepository) Insert(ctx context.Context, r storage.APIKeyRecord) error {
	return nil
}

func TestUsageSharedGateCapacityAndRetryAfter(t *testing.T) {
	gateHold := make(chan struct{})
	mockRepo := &blockingUsageRepository{gateHold: gateHold}

	handler, err := NewHandlerWithAdmin(transport.NewClient(), "http://127.0.0.1:1", "upstream", "admin-secret", "pepper", mockRepo)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	client := server.Client()

	var wg sync.WaitGroup
	wg.Add(2)

	// Query 1: Overview
	go func() {
		defer wg.Done()
		req, _ := http.NewRequest(http.MethodGet, server.URL+"/admin/v1/overview?after=2026-09-01T00:00:00Z&before=2026-09-02T00:00:00Z", nil)
		req.Header.Set("Authorization", "Bearer admin-secret")
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}()

	// Query 2: Timeseries
	go func() {
		defer wg.Done()
		req, _ := http.NewRequest(http.MethodGet, server.URL+"/admin/v1/usage/timeseries?after=2026-09-03T00:00:00Z&before=2026-09-04T00:00:00Z&bucket=hour", nil)
		req.Header.Set("Authorization", "Bearer admin-secret")
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}()

	// Wait for both to be in flight
	deadline := time.Now().Add(1 * time.Second)
	for mockRepo.inFlight.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if mockRepo.inFlight.Load() != 2 {
		t.Fatalf("inFlight = %d, want 2", mockRepo.inFlight.Load())
	}

	// 3rd query: Breakdown (shared gate should be saturated, must return 503 + Retry-After: 1)
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/admin/v1/usage/breakdown?group_by=model&after=2026-09-05T00:00:00Z&before=2026-09-06T00:00:00Z", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for shared capacity exceeded, got %d", resp.StatusCode)
	}
	retryAfter := resp.Header.Get("Retry-After")
	if retryAfter != "1" {
		t.Fatalf("expected Retry-After: 1, got %q", retryAfter)
	}

	close(gateHold)
	wg.Wait()
}
