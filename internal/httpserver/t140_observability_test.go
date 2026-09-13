package httpserver

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/storage"
	"github.com/pestit/9gateway/internal/transport"
)

// TestT140ObservabilityLifecycleWiresOneRecordToBothSinks exercises the real
// HTTP boundary, including a restart. The preceding transport tests cover the
// individual JSON/SSE, conversion, coding, error, rejection, and cancellation
// paths; this scenario proves their shared completion handoff persists across
// process ownership boundaries without making history synchronous.
func TestT140ObservabilityLifecycleWiresOneRecordToBothSinks(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "history.db")
	pepper := []byte("t140-pepper")
	keyWithCapture, err := auth.GenerateGatewayKey(pepper)
	if err != nil {
		t.Fatal(err)
	}
	keyWithoutCapture, err := auth.GenerateGatewayKey(pepper)
	if err != nil {
		t.Fatal(err)
	}

	upstreamCalls := atomic.Uint64{}
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		upstreamCalls.Add(1)
		body, _ := io.ReadAll(request.Body)
		if request.URL.Path != "/v1/chat/completions" || !bytes.Contains(body, []byte(`"model":"t140"`)) {
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"id":"t140","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	t.Cleanup(upstream.Close)

	database, err := storage.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	keys := storage.NewAPIKeyRepository(database)
	created := time.Unix(1, 0).UTC()
	for _, value := range []struct {
		id, name, policy string
		key              auth.GeneratedGatewayKey
	}{
		{"capture", "capture-key", `{"log_request_body":true,"log_response_body":true}`, keyWithCapture},
		{"plain", "plain-key", `{}`, keyWithoutCapture},
	} {
		if err := keys.Insert(ctx, storage.APIKeyRecord{ID: value.id, Name: value.name, DisplayPrefix: value.key.DisplayPrefix, Digest: value.key.Digest, Enabled: true, CreatedAt: created, UpdatedAt: created, PolicyJSON: value.policy}); err != nil {
			t.Fatal(err)
		}
	}

	start := func(database *storage.DB) (*httptest.Server, *CompletionLogger, *HistoryPersistenceWorker, <-chan slog.Record) {
		authenticator, authErr := auth.NewAuthenticator(pepper, nil)
		if authErr != nil {
			t.Fatal(authErr)
		}
		records, listErr := storage.NewAPIKeyRepository(database).List(ctx)
		if listErr != nil {
			t.Fatal(listErr)
		}
		authRecords := make([]auth.Record, 0, len(records))
		for _, record := range records {
			authRecords = append(authRecords, auth.Record{ID: record.ID, Name: record.Name, DisplayPrefix: record.DisplayPrefix, Digest: record.Digest, Enabled: record.Enabled, PolicyJSON: []byte(record.PolicyJSON)})
		}
		if loadErr := authenticator.Load(authRecords); loadErr != nil {
			t.Fatal(loadErr)
		}
		logHandler := &completionRecordHandler{records: make(chan slog.Record, 4)}
		logger := NewCompletionLogger(slog.New(logHandler), 16)
		history := NewHistoryPersistenceWorker(HistoryPersistenceWorkerOptions{Repository: storage.NewRequestHistoryRepository(database), Capacity: 16})
		handler := NewHandlerWithAuthenticatorAndLimitersAndTokenLimiterAndUsageObservationWorkerAndHistory(
			transport.NewClient(), upstream.URL, "upstream-secret", authenticator,
			limiter.NewRequestLimiter(nil), limiter.NewConcurrencyLimiter(), logger,
			nil, TokenAdmissionConfig{MaxCapturedBodyBytes: 1024}, nil, history)
		return httptest.NewServer(handler), logger, history, logHandler.records
	}

	database.Close()
	database, err = storage.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	gateway, logger, history, logs := start(database)
	request := func(key auth.GeneratedGatewayKey) *http.Response {
		req, requestErr := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", bytes.NewBufferString(`{"model":"t140","stream":false}`))
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		req.Header.Set("Authorization", "Bearer "+key.RawKey)
		req.Header.Set("Content-Type", "application/json")
		response, doErr := http.DefaultClient.Do(req)
		if doErr != nil {
			t.Fatal(doErr)
		}
		return response
	}
	for _, key := range []auth.GeneratedGatewayKey{keyWithCapture, keyWithoutCapture} {
		response := request(key)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", response.StatusCode)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
	}
	waitForHistory := func(want uint64) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if history.Stats().Persisted >= want {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("history stats = %+v, want persisted >= %d", history.Stats(), want)
	}
	waitForLogs := func(want int) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if len(logs) >= want {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("log records = %d, want %d", len(logs), want)
	}
	waitForHistory(2)
	waitForLogs(2)
	if logger.Dropped() != 0 || upstreamCalls.Load() != 2 {
		t.Fatalf("sinks/upstream = dropped %d, calls %d", logger.Dropped(), upstreamCalls.Load())
	}
	gateway.Close()
	if err := history.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := logger.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	database, err = storage.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	gateway, logger, history, logs = start(database)
	response := request(keyWithCapture)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("restart status = %d, want 200", response.StatusCode)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	waitForHistory(1)
	waitForLogs(1)
	gateway.Close()
	if err := history.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := logger.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	var requestCount, bodyCount int
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM requests").Scan(&requestCount); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM request_bodies").Scan(&bodyCount); err != nil {
		t.Fatal(err)
	}
	if requestCount != 3 || bodyCount != 6 {
		t.Fatalf("persisted rows = requests %d, bodies %d; want 3, 6", requestCount, bodyCount)
	}
	var plainBodyCount int
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM request_bodies JOIN requests USING (request_id) WHERE api_key_id = ?", "plain").Scan(&plainBodyCount); err != nil {
		t.Fatal(err)
	}
	if plainBodyCount != 0 {
		t.Fatalf("plain-key body rows = %d, want 0", plainBodyCount)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
}
