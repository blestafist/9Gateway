package httpserver

import (
	"bytes"
	"context"
	"database/sql"
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
	retentionNow := time.Now().UTC().Truncate(time.Microsecond)
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
		{"capture", "capture-key", `{"log_request_body":true,"log_response_body":true,"allowed_models":["t140"]}`, keyWithCapture},
		{"plain", "plain-key", `{"allowed_models":["t140"]}`, keyWithoutCapture},
	} {
		if err := keys.Insert(ctx, storage.APIKeyRecord{ID: value.id, Name: value.name, DisplayPrefix: value.key.DisplayPrefix, Digest: value.key.Digest, Enabled: true, CreatedAt: created, UpdatedAt: created, PolicyJSON: value.policy}); err != nil {
			t.Fatal(err)
		}
	}

	start := func(database *storage.DB) (*httptest.Server, *CompletionLogger, *HistoryPersistenceWorker, *UsageObservationWorker, <-chan slog.Record) {
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
		history := NewHistoryPersistenceWorker(HistoryPersistenceWorkerOptions{Repository: storage.NewRequestHistoryRepository(database), Capacity: 16, RetentionEveryJobs: 1, RequestRetention: 24 * time.Hour, BodyRetention: time.Second, Clock: func() time.Time { return retentionNow }})
		usage := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: 16})
		handler := NewHandlerWithAuthenticatorAndLimitersAndTokenLimiterAndUsageObservationWorkerAndHistory(
			transport.NewClient(), upstream.URL, "upstream-secret", authenticator,
			limiter.NewRequestLimiter(nil), limiter.NewConcurrencyLimiter(), logger,
			nil, TokenAdmissionConfig{MaxCapturedBodyBytes: 1024}, usage, history)
		return httptest.NewServer(handler), logger, history, usage, logHandler.records
	}

	database.Close()
	database, err = storage.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	gateway, logger, history, usage, logs := start(database)
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
	firstLogs := receiveT140Logs(t, logs, 2)
	for _, want := range []struct {
		keyName   string
		requestID string
		bodyCount int
	}{
		{keyName: "capture-key", requestID: firstLogs["capture-key"]["request_id"].(string), bodyCount: 3},
		{keyName: "plain-key", requestID: firstLogs["plain-key"]["request_id"].(string), bodyCount: 0},
	} {
		assertT140CompletionScalars(t, firstLogs[want.keyName])
		row := readT140HistoryRow(t, database, want.requestID)
		assertT140PersistedScalars(t, row, firstLogs[want.keyName])
		var bodies int
		if err := database.QueryRowContext(ctx, "SELECT count(*) FROM request_bodies WHERE request_id = ?", want.requestID).Scan(&bodies); err != nil {
			t.Fatal(err)
		}
		if bodies != want.bodyCount {
			t.Fatalf("%s body rows = %d, want %d", want.keyName, bodies, want.bodyCount)
		}
		if want.bodyCount != 0 {
			var bodyKinds, bodyPayloads []string
			rows, queryErr := database.QueryContext(ctx, "SELECT body_kind, body FROM request_bodies WHERE request_id = ? ORDER BY body_kind", want.requestID)
			if queryErr != nil {
				t.Fatal(queryErr)
			}
			for rows.Next() {
				var kind string
				var payload []byte
				if scanErr := rows.Scan(&kind, &payload); scanErr != nil {
					t.Fatal(scanErr)
				}
				bodyKinds = append(bodyKinds, kind)
				bodyPayloads = append(bodyPayloads, string(payload))
			}
			rows.Close()
			if len(bodyKinds) != 3 || bodyKinds[0] != "client_request" || bodyKinds[1] != "response" || bodyKinds[2] != "upstream_request" || bodyPayloads[0] != `{"model":"t140","stream":false}` || bodyPayloads[1] != `{"id":"t140","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}` || bodyPayloads[2] != `{"model":"t140","stream":false}` {
				t.Fatalf("body rows = %#v/%#v", bodyKinds, bodyPayloads)
			}
		}
	}
	// Make the first captured request old enough for body retention, while its
	// metadata remains within the longer request-retention window. The restarted
	// worker's startup pass must remove only those old body rows.
	oldRequestID := firstLogs["capture-key"]["request_id"].(string)
	oldFinished := retentionNow.Add(-2 * time.Second).UnixMicro()
	if _, err := database.ExecContext(ctx, "UPDATE requests SET started_at = ?, upstream_started_at = ?, upstream_headers_at = ?, first_byte_at = ?, finished_at = ? WHERE request_id = ?", oldFinished, oldFinished+1, oldFinished+2, oldFinished+3, oldFinished+4, oldRequestID); err != nil {
		t.Fatal(err)
	}
	gateway.Close()
	if err := history.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := usage.Shutdown(ctx); err != nil {
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
	gateway, logger, history, usage, logs = start(database)
	<-history.startupDone
	response := request(keyWithCapture)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("restart status = %d, want 200", response.StatusCode)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	waitForHistory(1)
	waitForLogs(1)
	restartLogs := receiveT140Logs(t, logs, 1)
	assertT140CompletionScalars(t, restartLogs["capture-key"])
	gateway.Close()
	if err := history.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := usage.Shutdown(ctx); err != nil {
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
	if requestCount != 3 || bodyCount != 3 {
		t.Fatalf("persisted rows = requests %d, bodies %d; want 3, 3 after independent body retention", requestCount, bodyCount)
	}
	var oldCount int
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM requests WHERE request_id = ?", oldRequestID).Scan(&oldCount); err != nil {
		t.Fatal(err)
	}
	if oldCount != 1 {
		t.Fatalf("intermediate-age request rows = %d, want metadata to survive body retention", oldCount)
	}
	var oldBodyCount int
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM request_bodies WHERE request_id = ?", oldRequestID).Scan(&oldBodyCount); err != nil {
		t.Fatal(err)
	}
	if oldBodyCount != 0 {
		t.Fatalf("expired request body rows = %d, want 0", oldBodyCount)
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

type t140HistoryRow struct {
	requestID, keyID, keyName, method, path, route, model                  string
	requestedMode, upstreamMode, deliveredMode, terminalOutcome, errorCode sql.NullString
	downstreamStatus, upstreamStatus                                       sql.NullInt64
	upstreamStarted                                                        sql.NullInt64
	clientBytes, upstreamBytes, deliveredBytes                             sql.NullInt64
	inputTokens, outputTokens, totalTokens, costMicros                     sql.NullInt64
	totalMicros, headersMicros, firstByteMicros, closeDelayMicros          sql.NullInt64
}

func receiveT140Logs(t *testing.T, records <-chan slog.Record, count int) map[string]map[string]any {
	t.Helper()
	result := make(map[string]map[string]any, count)
	for index := 0; index < count; index++ {
		select {
		case record := <-records:
			values := make(map[string]any)
			record.Attrs(func(attribute slog.Attr) bool {
				values[attribute.Key] = attribute.Value.Any()
				return true
			})
			keyName, ok := values["key_name"].(string)
			if !ok || keyName == "" {
				t.Fatalf("completion log has no key identity: %#v", values)
			}
			result[keyName] = values
		case <-time.After(time.Second):
			t.Fatal("completion record was not written")
		}
	}
	return result
}

func assertT140CompletionScalars(t *testing.T, values map[string]any) {
	t.Helper()
	for key, want := range map[string]any{
		"method": "POST", "path": "/v1/chat/completions", "route": "chat_completions",
		"model": "t140", "requested_mode": "json", "upstream_mode": "json", "delivered_mode": "json",
		"status": int64(200), "downstream_status": int64(200), "upstream_status": int64(200),
		"terminal_outcome": "complete", "upstream_started": true,
		"usage_input": int64(1), "usage_output": int64(1), "usage_total": int64(2),
	} {
		if values[key] != want {
			t.Errorf("completion %s = %#v, want %#v", key, values[key], want)
		}
	}
	for _, key := range []string{"delivered_bytes", "total_micros", "time_to_upstream_headers_micros", "time_to_first_byte_micros"} {
		value, ok := values[key].(int64)
		if !ok || value < 0 {
			t.Errorf("completion %s = %#v, want known non-negative scalar", key, values[key])
		}
	}
	if _, ok := values["cost_micros"]; ok {
		t.Errorf("completion cost_micros = %#v, want unknown cost omitted", values["cost_micros"])
	}
	for _, key := range []string{"client_bytes", "upstream_bytes"} {
		if _, ok := values[key]; ok {
			t.Errorf("completion %s = %#v, want unknown omitted", key, values[key])
		}
	}
}

func readT140HistoryRow(t *testing.T, database *storage.DB, requestID string) t140HistoryRow {
	t.Helper()
	var row t140HistoryRow
	err := database.QueryRowContext(context.Background(), `SELECT request_id, api_key_id, key_name, method, path, route, model,
        requested_mode, upstream_mode, delivered_mode, downstream_status, upstream_status,
        terminal_outcome, upstream_started, error_code, client_bytes, upstream_bytes,
        delivered_bytes, input_tokens, output_tokens, total_tokens, cost_micros,
        total_micros, time_to_upstream_headers_micros, time_to_first_byte_micros,
        stream_close_delay_micros FROM requests WHERE request_id = ?`, requestID).Scan(
		&row.requestID, &row.keyID, &row.keyName, &row.method, &row.path, &row.route, &row.model,
		&row.requestedMode, &row.upstreamMode, &row.deliveredMode, &row.downstreamStatus, &row.upstreamStatus,
		&row.terminalOutcome, &row.upstreamStarted, &row.errorCode, &row.clientBytes, &row.upstreamBytes,
		&row.deliveredBytes, &row.inputTokens, &row.outputTokens, &row.totalTokens, &row.costMicros,
		&row.totalMicros, &row.headersMicros, &row.firstByteMicros, &row.closeDelayMicros)
	if err != nil {
		t.Fatalf("read persisted record %s: %v", requestID, err)
	}
	return row
}

func assertT140PersistedScalars(t *testing.T, row t140HistoryRow, log map[string]any) {
	t.Helper()
	for name, got := range map[string]string{
		"key_name": row.keyName, "method": row.method, "path": row.path,
		"route": row.route, "model": row.model, "requested_mode": row.requestedMode.String,
		"upstream_mode": row.upstreamMode.String, "delivered_mode": row.deliveredMode.String,
		"terminal_outcome": row.terminalOutcome.String,
	} {
		if got != log[name] {
			t.Errorf("persisted %s = %q, log = %#v", name, got, log[name])
		}
	}
	if row.keyID == "" {
		t.Error("persisted key_id is empty")
	}
	for name, value := range map[string]sql.NullInt64{
		"downstream_status": row.downstreamStatus, "upstream_status": row.upstreamStatus,
		"delivered_bytes": row.deliveredBytes,
		"input_tokens":    row.inputTokens, "output_tokens": row.outputTokens, "total_tokens": row.totalTokens,
		"total_micros": row.totalMicros, "time_to_upstream_headers_micros": row.headersMicros,
		"time_to_first_byte_micros": row.firstByteMicros,
	} {
		if !value.Valid || value.Int64 < 0 {
			t.Errorf("persisted %s = %#v, want known non-negative scalar", name, value)
		}
	}
	if !row.upstreamStarted.Valid || row.upstreamStarted.Int64 != 1 || row.costMicros.Valid {
		t.Errorf("persisted upstream_started/cost = %#v/%#v", row.upstreamStarted, row.costMicros)
	}
	if row.clientBytes.Valid || row.upstreamBytes.Valid {
		t.Errorf("persisted unknown request byte counts = %#v/%#v", row.clientBytes, row.upstreamBytes)
	}
}
