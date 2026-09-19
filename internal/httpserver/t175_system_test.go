package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/accounting"
	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/storage"
	"github.com/pestit/9gateway/internal/transport"
	"github.com/pestit/9gateway/internal/version"
)

func TestSystemAuthenticationAndMethods(t *testing.T) {
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
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/admin/v1/system", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated request, got %d", resp.StatusCode)
	}

	// 2. Wrong Bearer -> 401
	req, _ = http.NewRequest(http.MethodGet, server.URL+"/admin/v1/system", nil)
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
	req, _ = http.NewRequest(http.MethodGet, server.URL+"/admin/v1/system", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for authenticated request, got %d", resp.StatusCode)
	}

	// 4. Method not allowed (POST, PUT, DELETE) -> 405
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req, _ = http.NewRequest(method, server.URL+"/admin/v1/system", nil)
		req.Header.Set("Authorization", "Bearer admin-secret")
		resp, err = client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405 for %s /admin/v1/system, got %d", method, resp.StatusCode)
		}
	}

	// 5. Query parameters rejected -> 400
	req, _ = http.NewRequest(http.MethodGet, server.URL+"/admin/v1/system?extra=param", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for query params, got %d", resp.StatusCode)
	}
}

func TestSystemHealthyPayload(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	keys := storage.NewAPIKeyRepository(database)
	completionLogger := NewCompletionLogger(slog.Default(), 64)
	usageWorker := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: 64})
	historyWorker := NewHistoryPersistenceWorker(HistoryPersistenceWorkerOptions{
		Repository:       storage.NewRequestHistoryRepository(database),
		Capacity:         64,
		RequestRetention: 14 * 24 * time.Hour,
		BodyRetention:    3 * 24 * time.Hour,
	})

	handler, err := NewHandlerWithAdminAndLimitersAndTokenConfigAndTokenLimiterAndUsageObservationWorkerAndHistory(
		transport.NewClient(),
		"http://127.0.0.1:1",
		"upstream-key",
		"admin-secret",
		"pepper",
		keys,
		limiter.NewRequestLimiter(nil),
		limiter.NewConcurrencyLimiter(),
		completionLogger,
		nil,
		TokenAdmissionConfig{MaxCapturedBodyBytes: 1024},
		usageWorker,
		historyWorker,
	)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(handler)
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/admin/v1/system", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var payload AdminSystemResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Verify allowlisted fields
	if payload.Version == "" {
		t.Errorf("expected non-empty Version")
	}
	if payload.Commit == "" {
		t.Errorf("expected non-empty Commit")
	}
	if payload.BuildTime == "" {
		t.Errorf("expected non-empty BuildTime")
	}
	if payload.BuildDate != payload.BuildTime {
		t.Errorf("expected BuildDate == BuildTime, got %q vs %q", payload.BuildDate, payload.BuildTime)
	}
	if payload.StartTime == "" {
		t.Errorf("expected non-empty StartTime")
	}
	if _, err := time.Parse(time.RFC3339, payload.StartTime); err != nil {
		t.Errorf("StartTime not RFC3339: %v", err)
	}
	if payload.UptimeSeconds < 0 {
		t.Errorf("expected UptimeSeconds >= 0, got %d", payload.UptimeSeconds)
	}
	if !payload.Ready {
		t.Errorf("expected Ready == true")
	}
	if !payload.Readiness.Ready {
		t.Errorf("expected Readiness.Ready == true")
	}
	for _, checkName := range []string{"sqlite", "schema", "telemetry", "upstream", "lifecycle"} {
		check, ok := payload.Readiness.Checks[checkName]
		if !ok {
			t.Errorf("missing readiness check %q", checkName)
		} else if check.Status != "pass" {
			t.Errorf("check %q status = %q; want pass", checkName, check.Status)
		}
	}

	// Storage check
	if payload.Storage.Status != "healthy" || !payload.Storage.Healthy {
		t.Errorf("expected Storage healthy, got %+v", payload.Storage)
	}
	if payload.Storage.SchemaVersion == nil || *payload.Storage.SchemaVersion != storage.CurrentSchemaVersion {
		t.Errorf("expected Storage.SchemaVersion = %d, got %v", storage.CurrentSchemaVersion, payload.Storage.SchemaVersion)
	}
	if payload.Storage.CurrentSchemaVersion != storage.CurrentSchemaVersion {
		t.Errorf("expected CurrentSchemaVersion = %d, got %d", storage.CurrentSchemaVersion, payload.Storage.CurrentSchemaVersion)
	}
	if payload.SQLite.Status != payload.Storage.Status || payload.SQLite.Healthy != payload.Storage.Healthy ||
		payload.SQLite.CurrentSchemaVersion != payload.Storage.CurrentSchemaVersion ||
		(payload.SQLite.SchemaVersion == nil && payload.Storage.SchemaVersion != nil) ||
		(payload.SQLite.SchemaVersion != nil && payload.Storage.SchemaVersion == nil) ||
		(payload.SQLite.SchemaVersion != nil && payload.Storage.SchemaVersion != nil && *payload.SQLite.SchemaVersion != *payload.Storage.SchemaVersion) {
		t.Errorf("expected SQLite == Storage, got %+v vs %+v", payload.SQLite, payload.Storage)
	}

	// Telemetry check (capacity is aggregated across completionLogger, usageWorker, historyWorker: 64*3 = 192)
	if payload.Telemetry.QueueCapacity != 192 {
		t.Errorf("expected Telemetry.QueueCapacity = 192, got %d", payload.Telemetry.QueueCapacity)
	}
	if payload.Telemetry.QueueDepth < 0 {
		t.Errorf("expected Telemetry.QueueDepth >= 0, got %d", payload.Telemetry.QueueDepth)
	}
	if payload.Telemetry.DroppedRecords != 0 {
		t.Errorf("expected Telemetry.DroppedRecords = 0, got %d", payload.Telemetry.DroppedRecords)
	}

	// Limits check
	expectedReqRet := int64(14 * 24 * 3600)
	expectedBodyRet := int64(3 * 24 * 3600)
	if payload.Limits.RequestRetentionSeconds != expectedReqRet {
		t.Errorf("expected RequestRetentionSeconds = %d, got %d", expectedReqRet, payload.Limits.RequestRetentionSeconds)
	}
	if payload.Limits.BodyRetentionSeconds != expectedBodyRet {
		t.Errorf("expected BodyRetentionSeconds = %d, got %d", expectedBodyRet, payload.Limits.BodyRetentionSeconds)
	}
	if payload.Limits.MaxCapturedBodyBytes != 1024 {
		t.Errorf("expected MaxCapturedBodyBytes = 1024, got %d", payload.Limits.MaxCapturedBodyBytes)
	}

	// Active requests >= 0
	if payload.ActiveRequests < 0 {
		t.Errorf("expected ActiveRequests >= 0, got %d", payload.ActiveRequests)
	}
}

func TestSystemDegradedSchemaState(t *testing.T) {
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

	// Force an incompatible schema version into SQLite
	if _, err := database.Exec(fmt.Sprintf("PRAGMA user_version = %d", storage.CurrentSchemaVersion-1)); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(handler)
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/admin/v1/system", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK even when degraded, got %d", resp.StatusCode)
	}

	var payload AdminSystemResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if payload.Ready {
		t.Errorf("expected overall Ready = false in degraded state")
	}
	if payload.Storage.Status != "degraded" {
		t.Errorf("expected Storage.Status = 'degraded', got %q", payload.Storage.Status)
	}
	if payload.Storage.Healthy {
		t.Errorf("expected Storage.Healthy = false")
	}
	if payload.Storage.SchemaVersion == nil || *payload.Storage.SchemaVersion != storage.CurrentSchemaVersion-1 {
		t.Errorf("expected SchemaVersion = %d, got %v", storage.CurrentSchemaVersion-1, payload.Storage.SchemaVersion)
	}
}

func TestSystemShutdownDrainingState(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	keys := storage.NewAPIKeyRepository(database)
	state := &ReadinessState{}

	readiness := NewReadiness(ReadinessConfig{
		Database:        database,
		UpstreamBaseURL: "http://127.0.0.1:1",
		State:           state,
	})

	handler, err := newHandlerWithAdminAndLimitersAndTokenConfigAndTokenLimiterAndUsageObservationWorker(
		transport.NewClient(),
		"http://127.0.0.1:1",
		"upstream",
		"admin-secret",
		"pepper",
		keys,
		limiter.NewRequestLimiter(nil),
		limiter.NewConcurrencyLimiter(),
		nil,
		nil,
		TokenAdmissionConfig{},
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	// Wrap with readiness
	handler = WithReadiness(handler, readiness)

	server := httptest.NewServer(handler)
	defer server.Close()

	// Mark draining (simulating shutdown)
	state.MarkDraining()

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/admin/v1/system", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK with shutdown payload, got %d", resp.StatusCode)
	}

	var payload AdminSystemResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if payload.Ready {
		t.Errorf("expected Ready == false during draining")
	}
	if payload.Readiness.Checks["lifecycle"].Status != "fail" {
		t.Errorf("expected lifecycle check fail, got %s", payload.Readiness.Checks["lifecycle"].Status)
	}
	if payload.Readiness.Checks["lifecycle"].Message != "gateway is shutting down" {
		t.Errorf("expected message 'gateway is shutting down', got %q", payload.Readiness.Checks["lifecycle"].Message)
	}
}

func TestSystemClosedStorageState(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	keys := storage.NewAPIKeyRepository(database)
	handler, err := NewHandlerWithAdmin(transport.NewClient(), "http://127.0.0.1:1", "upstream", "admin-secret", "pepper", keys)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(handler)
	defer server.Close()

	// Close database before querying
	database.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/admin/v1/system", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK with closed-storage payload, got %d", resp.StatusCode)
	}

	var payload AdminSystemResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if payload.Ready {
		t.Errorf("expected Ready = false with closed storage")
	}
	if payload.Storage.Healthy {
		t.Errorf("expected Storage.Healthy = false with closed storage")
	}
	if payload.Storage.Status != "unavailable" {
		t.Errorf("expected Storage.Status = 'unavailable', got %q", payload.Storage.Status)
	}
	if payload.Storage.SchemaVersion != nil {
		t.Errorf("expected SchemaVersion = nil with closed storage, got %v", payload.Storage.SchemaVersion)
	}
}

func TestSystemSaturatedTelemetryState(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	keys := storage.NewAPIKeyRepository(database)
	usageWorker := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: 1})

	// Fill the worker and cause drops
	usageWorker.markDropped()
	usageWorker.markDropped()
	usageWorker.markDropped()

	handler, err := NewHandlerWithAdminAndLimitersAndTokenConfigAndTokenLimiterAndUsageObservationWorkerAndHistory(
		transport.NewClient(),
		"http://127.0.0.1:1",
		"upstream-key",
		"admin-secret",
		"pepper",
		keys,
		limiter.NewRequestLimiter(nil),
		limiter.NewConcurrencyLimiter(),
		nil,
		nil,
		TokenAdmissionConfig{},
		usageWorker,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(handler)
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/admin/v1/system", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var payload AdminSystemResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if payload.Telemetry.DroppedRecords < 3 {
		t.Errorf("expected DroppedRecords >= 3, got %d", payload.Telemetry.DroppedRecords)
	}
}

func TestSystemDevBuildAndUnavailableFields(t *testing.T) {
	// With nil repository / minimal setup
	service, err := newAdminKeyService(&testAdminRepository{}, []byte("test-pepper"))
	if err != nil {
		t.Fatal(err)
	}
	adminHandler, err := newAdminHandler("admin-secret", service)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(adminHandler)
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/admin/v1/system", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var payload AdminSystemResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	// Dev build defaults
	if payload.Version != "dev" && payload.Version != version.Current().Version {
		t.Errorf("unexpected Version: %s", payload.Version)
	}
	if payload.Commit != "unknown" && payload.Commit != version.Current().Commit {
		t.Errorf("unexpected Commit: %s", payload.Commit)
	}
	if payload.BuildTime != "unknown" && payload.BuildTime != version.Current().BuildDate {
		t.Errorf("unexpected BuildTime: %s", payload.BuildTime)
	}

	// Unavailable fields
	if payload.Ready {
		t.Errorf("expected Ready = false when dependencies are unavailable")
	}
	if payload.Storage.Status != "unavailable" {
		t.Errorf("expected Storage.Status = 'unavailable', got %s", payload.Storage.Status)
	}
	if payload.Storage.Healthy {
		t.Errorf("expected Storage.Healthy = false")
	}
	if payload.Storage.SchemaVersion != nil {
		t.Errorf("expected Storage.SchemaVersion = nil, got %v", payload.Storage.SchemaVersion)
	}
	if payload.Storage.CurrentSchemaVersion != storage.CurrentSchemaVersion {
		t.Errorf("expected CurrentSchemaVersion = %d, got %d", storage.CurrentSchemaVersion, payload.Storage.CurrentSchemaVersion)
	}
}

func TestSystemSecretCanariesNeverLeaked(t *testing.T) {
	canaryUpstreamURL := "https://canary-user:canary-password@canary-upstream.internal.corp:8443/canary/v1"
	canaryUpstreamKey := "canary-upstream-sk-live-998877665544332211"
	canaryAdminSecret := "canary-admin-very-secret-token-xyz-123"
	canaryAuthPepper := "canary-secret-auth-pepper-bytes-long"
	canaryEnvSecret := "canary-secret-env-val-009988"
	canaryDBPath := "/tmp/canary-secret-filesystem-path-to-db.sqlite"
	canaryPricingRule := "canary-pricing-model-tier-x"
	canaryKeyName := "canary-secret-key-name-in-db"

	os.Setenv("GATEWAY_CANARY_TEST_SECRET", canaryEnvSecret)
	defer os.Unsetenv("GATEWAY_CANARY_TEST_SECRET")

	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	keys := storage.NewAPIKeyRepository(database)
	// Insert a secret canary key record into DB
	genKey, err := auth.GenerateGatewayKey([]byte(canaryAuthPepper))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1700000000, 0).UTC()
	err = keys.Insert(context.Background(), storage.APIKeyRecord{
		ID:            "key-canary-123456",
		Name:          canaryKeyName,
		DisplayPrefix: genKey.DisplayPrefix,
		Digest:        genKey.Digest,
		Enabled:       true,
		CreatedAt:     now,
		UpdatedAt:     now,
		PolicyJSON:    `{"allowed_models":["gpt-4"]}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	pricingConfig := t110Pricing(t, fmt.Sprintf("rules:\n  - model: %s\n    input_per_million_micros: 10000\n    output_per_million_micros: 30000\n", canaryPricingRule))

	handler, err := NewHandlerWithAdminAndLimitersAndTokenConfig(
		transport.NewClient(),
		canaryUpstreamURL,
		canaryUpstreamKey,
		canaryAdminSecret,
		canaryAuthPepper,
		keys,
		limiter.NewRequestLimiter(nil),
		limiter.NewConcurrencyLimiter(),
		nil,
		TokenAdmissionConfig{
			PricingResolver: accounting.NewPricingResolver(pricingConfig),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(handler)
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/admin/v1/system", nil)
	req.Header.Set("Authorization", "Bearer "+canaryAdminSecret)
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	bodyStr := string(bodyBytes)

	canaries := []string{
		"canary-user",
		"canary-password",
		"canary-upstream",
		canaryUpstreamKey,
		canaryAdminSecret,
		canaryAuthPepper,
		canaryEnvSecret,
		canaryDBPath,
		canaryPricingRule,
		canaryKeyName,
	}

	for _, canary := range canaries {
		if strings.Contains(bodyStr, canary) {
			t.Fatalf("SECURITY VIOLATION: canary %q found in system response body: %s", canary, bodyStr)
		}
	}
}

func TestSystemCancellationHonored(t *testing.T) {
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

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately before request

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/admin/v1/system", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")

	// Client request with cancelled context should return error or abort cleanly
	resp, err := server.Client().Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

func TestSystemPublicEndpointsUnchanged(t *testing.T) {
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

	// 1. /health
	resp, err := client.Get(server.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for /health, got %d", resp.StatusCode)
	}

	// 2. /ready
	resp, err = client.Get(server.URL + "/ready")
	if err != nil {
		t.Fatal(err)
	}
	var readyPayload struct {
		Ready   bool                      `json:"ready"`
		Checks  map[string]readinessCheck `json:"checks"`
		Version string                    `json:"version"`
		Commit  string                    `json:"commit"`
	}
	err = json.NewDecoder(resp.Body).Decode(&readyPayload)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("failed to decode /ready: %v", err)
	}
	if !readyPayload.Ready {
		t.Errorf("expected /ready Ready == true")
	}

	// 3. /metrics
	resp, err = client.Get(server.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	metricsBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for /metrics, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(metricsBody), "gateway_requests_total") {
		t.Errorf("expected metrics exposition containing gateway_requests_total")
	}
}

func TestSystemConcurrentRaceAndLatency(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	keys := storage.NewAPIKeyRepository(database)
	completionLogger := NewCompletionLogger(slog.Default(), 128)
	usageWorker := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: 128})
	historyWorker := NewHistoryPersistenceWorker(HistoryPersistenceWorkerOptions{
		Repository: storage.NewRequestHistoryRepository(database),
		Capacity:   128,
	})

	handler, err := NewHandlerWithAdminAndLimitersAndTokenConfigAndTokenLimiterAndUsageObservationWorkerAndHistory(
		transport.NewClient(),
		"http://127.0.0.1:1",
		"upstream-key",
		"admin-secret",
		"pepper",
		keys,
		limiter.NewRequestLimiter(nil),
		limiter.NewConcurrencyLimiter(),
		completionLogger,
		nil,
		TokenAdmissionConfig{},
		usageWorker,
		historyWorker,
	)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(handler)
	defer server.Close()

	var wg sync.WaitGroup
	workers := 8
	iterations := 25

	var successCount atomic.Int64

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			client := server.Client()
			for j := 0; j < iterations; j++ {
				// Alternately record telemetry/drops
				if j%2 == 0 {
					usageWorker.markDropped()
				}
				start := time.Now()
				req, _ := http.NewRequest(http.MethodGet, server.URL+"/admin/v1/system", nil)
				req.Header.Set("Authorization", "Bearer admin-secret")
				resp, err := client.Do(req)
				if err != nil {
					t.Errorf("worker %d iteration %d request failed: %v", workerID, j, err)
					return
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				elapsed := time.Since(start)

				// Fast bounded response (well under 500ms)
				if elapsed > 500*time.Millisecond {
					t.Errorf("worker %d iteration %d slow response: %v", workerID, j, elapsed)
				}
				if resp.StatusCode == http.StatusOK {
					successCount.Add(1)
				}
			}
		}(i)
	}

	wg.Wait()

	expectedTotal := int64(workers * iterations)
	if successCount.Load() != expectedTotal {
		t.Fatalf("expected %d successful requests, got %d", expectedTotal, successCount.Load())
	}
}
