package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/storage"
	"github.com/pestit/9gateway/internal/transport"
)

func TestOverviewAuthenticationAndMethods(t *testing.T) {
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

	// 1. Missing Authorization -> 401
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/admin/v1/overview", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated request, got %d", resp.StatusCode)
	}

	// 2. Wrong Bearer -> 401
	req, _ = http.NewRequest(http.MethodGet, server.URL+"/admin/v1/overview", nil)
	req.Header.Set("Authorization", "Bearer wrong-secret")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong token, got %d", resp.StatusCode)
	}

	// 3. Valid Bearer -> 200
	req, _ = http.NewRequest(http.MethodGet, server.URL+"/admin/v1/overview", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for valid token, got %d", resp.StatusCode)
	}

	// 4. Method not allowed (POST, PUT, DELETE) -> 405
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req, _ = http.NewRequest(method, server.URL+"/admin/v1/overview", nil)
		req.Header.Set("Authorization", "Bearer admin-secret")
		resp, err = client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405 for %s, got %d", method, resp.StatusCode)
		}
	}
}

func TestOverviewRangeParsingAndValidation(t *testing.T) {
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
		req, _ := http.NewRequest(http.MethodGet, server.URL+"/admin/v1/overview"+query, nil)
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

	// 1. Default range: no query params -> 24h
	resp, body := get("")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("default range status = %d, want 200", resp.StatusCode)
	}
	startStr, _ := body["current_range_start"].(string)
	endStr, _ := body["current_range_end"].(string)
	start, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		t.Fatalf("parse start: %v", err)
	}
	end, err := time.Parse(time.RFC3339, endStr)
	if err != nil {
		t.Fatalf("parse end: %v", err)
	}
	if diff := end.Sub(start); diff != 24*time.Hour {
		t.Fatalf("default range duration = %v, want 24h", diff)
	}

	// 2. Valid explicit RFC3339 range
	t1 := "2026-09-01T00:00:00Z"
	t2 := "2026-09-07T00:00:00Z"
	resp, _ = get(fmt.Sprintf("?after=%s&before=%s", url.QueryEscape(t1), url.QueryEscape(t2)))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("valid range status = %d, want 200", resp.StatusCode)
	}

	// 3. Invalid RFC3339
	resp, _ = get("?after=not-a-date")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid RFC3339 status = %d, want 400", resp.StatusCode)
	}

	// 4. after >= before -> 400
	resp, _ = get(fmt.Sprintf("?after=%s&before=%s", url.QueryEscape(t2), url.QueryEscape(t1)))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("inverted range status = %d, want 400", resp.StatusCode)
	}
	resp, _ = get(fmt.Sprintf("?after=%s&before=%s", url.QueryEscape(t1), url.QueryEscape(t1)))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("equal range status = %d, want 400", resp.StatusCode)
	}

	// 5. range > 1 year -> 400
	tTooOld := "2024-01-01T00:00:00Z"
	resp, _ = get(fmt.Sprintf("?after=%s&before=%s", url.QueryEscape(tTooOld), url.QueryEscape(t2)))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("range > 1y status = %d, want 400", resp.StatusCode)
	}

	// 6. Unsupported query parameter -> 400
	resp, _ = get("?unsupported=1")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unsupported param status = %d, want 400", resp.StatusCode)
	}

	// 7. Duplicate query parameters -> 400
	resp, _ = get(fmt.Sprintf("?after=%s&after=%s", url.QueryEscape(t1), url.QueryEscape(t1)))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("duplicate param status = %d, want 400", resp.StatusCode)
	}
}

func TestOverviewPayloadAndSafety(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	// Insert API keys
	keys := storage.NewAPIKeyRepository(database)
	when := time.Now().UTC().Truncate(time.Second)
	for i, enabled := range []bool{true, true, false} {
		id := fmt.Sprintf("key-%d", i)
		gen, err := auth.GenerateGatewayKey([]byte("key-secret-" + id))
		if err != nil {
			t.Fatal(err)
		}
		if err := keys.Insert(context.Background(), storage.APIKeyRecord{
			ID:            id,
			Name:          id,
			DisplayPrefix: gen.DisplayPrefix,
			Digest:        gen.Digest,
			Enabled:       enabled,
			CreatedAt:     when,
			UpdatedAt:     when,
			PolicyJSON:    `{}`,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Insert request records
	history := storage.NewRequestHistoryRepository(database)
	t0 := time.Now().UTC()
	outcomes := []string{"complete", "custom_dispatch", "pre_upstream", "upstream_error", "response_error", "cancelled"}

	for i, oc := range outcomes {
		id := fmt.Sprintf("0000000000000000000000000000000%d", i)
		upStart := oc != "pre_upstream"
		rec := storage.HistoryRecord{
			RequestID:         id,
			APIKeyID:          "key-0",
			TerminalOutcome:   oc,
			UpstreamStarted:   upStart,
			InputTokens:       storage.KnownInt64(100),
			CachedInputTokens: storage.KnownInt64(25),
			OutputTokens:      storage.KnownInt64(50),
			CostMicros:        storage.KnownInt64(1000),
			StartedAt:         storage.KnownInt64(t0.Add(-time.Duration(i)*time.Minute).UnixMicro() - 100),
			FinishedAt:        storage.KnownInt64(t0.Add(-time.Duration(i) * time.Minute).UnixMicro()),
		}
		if err := history.Persist(context.Background(), rec, nil); err != nil {
			t.Fatalf("persist %s: %v", id, err)
		}
	}

	handler, err := NewHandlerWithAdmin(transport.NewClient(), "http://127.0.0.1:1", "upstream", "admin-secret", "pepper", keys)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	client := server.Client()
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/admin/v1/overview", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	rawBody := make(map[string]any)
	if err := json.NewDecoder(resp.Body).Decode(&rawBody); err != nil {
		t.Fatal(err)
	}

	// Verify key counts: 3 total, 2 enabled
	keyCounts, ok := rawBody["key_counts"].(map[string]any)
	if !ok {
		t.Fatalf("missing key_counts")
	}
	if keyCounts["total"] != float64(3) || keyCounts["enabled"] != float64(2) {
		t.Fatalf("key_counts = %+v, want total:3, enabled:2", keyCounts)
	}

	// Verify current aggregates: 6 total, 2 success, 1 rejected, 3 error
	current, ok := rawBody["current"].(map[string]any)
	if !ok {
		t.Fatalf("missing current aggregate")
	}
	if current["total_requests"] != float64(6) {
		t.Fatalf("total_requests = %v, want 6", current["total_requests"])
	}
	if current["successful_requests"] != float64(2) {
		t.Fatalf("successful_requests = %v, want 2", current["successful_requests"])
	}
	if current["rejected_requests"] != float64(1) {
		t.Fatalf("rejected_requests = %v, want 1", current["rejected_requests"])
	}
	if current["error_requests"] != float64(3) {
		t.Fatalf("error_requests = %v, want 3", current["error_requests"])
	}
	if current["input_tokens"] != float64(600) {
		t.Fatalf("input_tokens = %v, want 600", current["input_tokens"])
	}

	// Verify safety: response contains no bodies, raw keys, credentials, policy JSON, SQL, or unbounded labels
	bodyBytes, _ := json.Marshal(rawBody)
	bodyStr := string(bodyBytes)
	for _, forbidden := range []string{"do_not_leak", "key-secret", "pepper", "SELECT", "INSERT"} {
		if strings.Contains(bodyStr, forbidden) {
			t.Fatalf("response leaked sensitive string: %s", forbidden)
		}
	}

	// Verify recent requests: <=10 items
	recent, ok := rawBody["recent_requests"].([]any)
	if !ok || len(recent) != 6 {
		t.Fatalf("recent requests length = %d, want 6", len(recent))
	}
}

type blockingOverviewRepository struct {
	storage.OverviewRepository
	inFlight atomic.Int32
	unblock  chan struct{}
}

func (b *blockingOverviewRepository) GetOverview(ctx context.Context, after, before time.Time, active int64) (*storage.OverviewData, error) {
	b.inFlight.Add(1)
	defer b.inFlight.Add(-1)
	select {
	case <-b.unblock:
		return &storage.OverviewData{
			CurrentRangeStart:  after,
			CurrentRangeEnd:    before,
			PreviousRangeStart: after.Add(-before.Sub(after)),
			PreviousRangeEnd:   after.Add(-time.Microsecond),
			DataTimestamp:      time.Now().UTC(),
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *blockingOverviewRepository) List(ctx context.Context) ([]storage.APIKeyRecord, error) {
	return nil, nil
}
func (b *blockingOverviewRepository) Insert(ctx context.Context, r storage.APIKeyRecord) error {
	return nil
}

func TestOverviewLimiterMax2AndRetryable503(t *testing.T) {
	unblock := make(chan struct{})
	mockRepo := &blockingOverviewRepository{unblock: unblock}

	service, err := newAdminKeyService(mockRepo, []byte("pepper"))
	if err != nil {
		t.Fatal(err)
	}
	adminHandler, err := newAdminHandler("admin-secret", service)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(adminHandler)
	defer server.Close()

	client := server.Client()

	var wg sync.WaitGroup
	wg.Add(2)

	// Query 1
	go func() {
		defer wg.Done()
		req, _ := http.NewRequest(http.MethodGet, server.URL+"/admin/v1/overview?after=2026-09-01T00:00:00Z&before=2026-09-02T00:00:00Z", nil)
		req.Header.Set("Authorization", "Bearer admin-secret")
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}()

	// Query 2 (different range so different cache key)
	go func() {
		defer wg.Done()
		req, _ := http.NewRequest(http.MethodGet, server.URL+"/admin/v1/overview?after=2026-09-03T00:00:00Z&before=2026-09-04T00:00:00Z", nil)
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

	// 3rd query with different range: must return immediate 503 with Retry-After header!
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/admin/v1/overview?after=2026-09-05T00:00:00Z&before=2026-09-06T00:00:00Z", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for capacity exceeded, got %d", resp.StatusCode)
	}
	retryAfter := resp.Header.Get("Retry-After")
	if retryAfter != "1" {
		t.Fatalf("expected Retry-After: 1, got %q", retryAfter)
	}

	close(unblock)
	wg.Wait()
}

type countingOverviewRepository struct {
	storage.OverviewRepository
	queryCount atomic.Int32
}

func (c *countingOverviewRepository) GetOverview(ctx context.Context, after, before time.Time, active int64) (*storage.OverviewData, error) {
	c.queryCount.Add(1)
	return &storage.OverviewData{
		CurrentRangeStart:  after,
		CurrentRangeEnd:    before,
		PreviousRangeStart: after.Add(-before.Sub(after)),
		PreviousRangeEnd:   after.Add(-time.Microsecond),
		DataTimestamp:      time.Now().UTC(),
	}, nil
}
func (c *countingOverviewRepository) List(ctx context.Context) ([]storage.APIKeyRecord, error) {
	return nil, nil
}
func (c *countingOverviewRepository) Insert(ctx context.Context, r storage.APIKeyRecord) error {
	return nil
}

func TestOverviewCacheAndSingleflight(t *testing.T) {
	mockRepo := &countingOverviewRepository{}
	service, err := newAdminKeyService(mockRepo, []byte("pepper"))
	if err != nil {
		t.Fatal(err)
	}
	adminHandler, err := newAdminHandler("admin-secret", service)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(adminHandler)
	defer server.Close()

	client := server.Client()

	queryURL := server.URL + "/admin/v1/overview?after=2026-09-01T00:00:00Z&before=2026-09-02T00:00:00Z"

	// 1. Initial request -> executes query (count = 1)
	req, _ := http.NewRequest(http.MethodGet, queryURL, nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if mockRepo.queryCount.Load() != 1 {
		t.Fatalf("query count after 1st = %d, want 1", mockRepo.queryCount.Load())
	}

	// 2. Immediate second request for same range -> served from cache (count still 1)
	req, _ = http.NewRequest(http.MethodGet, queryURL, nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if mockRepo.queryCount.Load() != 1 {
		t.Fatalf("query count after 2nd (cached) = %d, want 1", mockRepo.queryCount.Load())
	}

	// 3. Different range -> executes query (count becomes 2)
	req, _ = http.NewRequest(http.MethodGet, server.URL+"/admin/v1/overview?after=2026-09-03T00:00:00Z&before=2026-09-04T00:00:00Z", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if mockRepo.queryCount.Load() != 2 {
		t.Fatalf("query count after 3rd = %d, want 2", mockRepo.queryCount.Load())
	}
}

func TestOverviewClientCancellation(t *testing.T) {
	unblock := make(chan struct{})
	mockRepo := &blockingOverviewRepository{unblock: unblock}
	service, err := newAdminKeyService(mockRepo, []byte("pepper"))
	if err != nil {
		t.Fatal(err)
	}
	adminHandler, err := newAdminHandler("admin-secret", service)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(adminHandler)
	defer server.Close()

	client := server.Client()

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/admin/v1/overview?after=2026-09-01T00:00:00Z&before=2026-09-02T00:00:00Z", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	resp, err := client.Do(req)
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil && resp.StatusCode == http.StatusOK {
		t.Fatalf("expected request to fail or be cancelled")
	}

	// Coordinator should have freed the active slot after cancellation
	close(unblock)
	deadline := time.Now().Add(1 * time.Second)
	for adminHandler.analyticsCoordinator.ActiveCount() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if adminHandler.analyticsCoordinator.ActiveCount() != 0 {
		t.Fatalf("active count after cancel = %d, want 0", adminHandler.analyticsCoordinator.ActiveCount())
	}
}
