// Package integration contains the cross-subsystem gateway contract tests.
// The tests deliberately use real listeners and SQLite: httptest handlers are
// suitable for an external provider, but an in-process handler cannot expose
// flushing, EOF, cancellation, or listener shutdown behaviour.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/accounting"
	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/httpserver"
	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/storage"
	"github.com/pestit/9gateway/internal/transport"
	"gopkg.in/yaml.v3"
)

const (
	adminSecret    = "integration-admin-secret"
	upstreamSecret = "integration-upstream-secret"
	pepper         = "integration-auth-pepper"
)

type gatewayHarness struct {
	t         *testing.T
	database  *storage.DB
	keys      *storage.APIKeyRepository
	history   *storage.RequestHistoryRepository
	usage     *httpserver.UsageObservationWorker
	tokenAgg  *storage.UsageAggregateAccumulator
	budgetAgg *storage.BudgetAccumulator
	tokens    *limiter.TokenLimiter
	budget    *limiter.BudgetLimiter
	logger    *httpserver.CompletionLogger
	historyW  *httpserver.HistoryPersistenceWorker
	server    *http.Server
	listener  net.Listener
	baseURL   string
	upstream  *httptest.Server
	client    *http.Client
	closed    atomic.Bool
}

// newHarness wires the same repositories, limiters, observation worker,
// accounting accumulators, completion logger, and history worker used by the
// production entry point. Only the upstream provider is an HTTP test double.
func newHarness(t *testing.T, upstream http.Handler, completionCapacity, historyCapacity int, client *http.Client) *gatewayHarness {
	t.Helper()
	upstreamServer := httptest.NewServer(upstream)
	databasePath := filepath.Join(t.TempDir(), "gateway.db")
	database, err := storage.Open(context.Background(), databasePath)
	if err != nil {
		upstreamServer.Close()
		t.Fatal(err)
	}
	keys := storage.NewAPIKeyRepository(database)
	history := storage.NewRequestHistoryRepository(database)
	usage := httpserver.NewUsageObservationWorker(httpserver.UsageObservationWorkerOptions{Capacity: 32})
	tokenAgg := storage.NewUsageAggregateAccumulatorWithContext(context.Background(), storage.NewUsageBucketRepository(database))
	budgetAgg := storage.NewBudgetAccumulatorWithContext(context.Background(), storage.NewBudgetBucketRepository(database))
	tokens := limiter.NewTokenLimiter(nil)
	budget := limiter.NewBudgetLimiter()
	tokens.SetCommittedDeltaSink(tokenAgg.Sink)
	budget.SetCommittedDeltaSink(budgetAgg.Sink)
	logger := httpserver.NewCompletionLogger(slog.New(slog.NewTextHandler(io.Discard, nil)), completionCapacity)
	historyW := httpserver.NewHistoryPersistenceWorker(httpserver.HistoryPersistenceWorkerOptions{
		Repository: history, Capacity: historyCapacity,
		RequestRetention: time.Hour, BodyRetention: time.Hour,
	})
	pricing := integrationPricing(t)
	handler, err := httpserver.NewHandlerWithAdminAndLimitersAndTokenConfigAndTokenLimiterAndUsageObservationWorkerAndHistory(
		transport.NewClient(), upstreamServer.URL, upstreamSecret, adminSecret, pepper,
		keys, limiter.NewRequestLimiter(nil), limiter.NewConcurrencyLimiter(), logger, tokens,
		httpserver.TokenAdmissionConfig{
			MaxInspectedRequestBytes: 64 * 1024, MaxCapturedBodyBytes: 4096,
			FallbackUnknownInputTokens: 1, FallbackMaxOutputTokens: 1,
			PricingResolver: pricing, BudgetLimiter: budget,
		}, usage, historyW, auth.TokenModeEstimate)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	if client == nil {
		client = transport.NewClient()
	}
	gateway := &gatewayHarness{t: t, database: database, keys: keys, history: history, usage: usage, tokenAgg: tokenAgg, budgetAgg: budgetAgg, tokens: tokens, budget: budget, logger: logger, historyW: historyW, server: server, listener: listener, baseURL: "http://" + listener.Addr().String(), upstream: upstreamServer, client: client}
	t.Cleanup(func() { gateway.close() })
	return gateway
}

func integrationPricing(t *testing.T) accounting.PricingResolver {
	t.Helper()
	var pricing accounting.PricingConfig
	data := []byte("rules:\n  - model: '*'\n    input_per_million_micros: 1000000\n    output_per_million_micros: 1000000\n")
	if err := yaml.Unmarshal(data, &pricing); err != nil {
		t.Fatal(err)
	}
	return accounting.NewPricingResolver(pricing)
}

func (gateway *gatewayHarness) close() {
	if gateway == nil || !gateway.closed.CompareAndSwap(false, true) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = gateway.server.Shutdown(ctx)
	_ = gateway.usage.Drain(ctx)
	_ = gateway.tokenAgg.Shutdown(ctx)
	_ = gateway.budgetAgg.Shutdown(ctx)
	_ = gateway.logger.Shutdown(ctx)
	_ = gateway.historyW.Shutdown(ctx)
	_ = gateway.database.Close()
	gateway.listener.Close()
	gateway.upstream.Close()
	if closer, ok := gateway.client.Transport.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

type upstreamScript struct {
	calls       atomic.Int64
	active      atomic.Int64
	maxActive   atomic.Int64
	cancelled   atomic.Int64
	holdStarted chan struct{}
	holdRelease chan struct{}
}

func newUpstreamScript() *upstreamScript {
	return &upstreamScript{holdStarted: make(chan struct{}), holdRelease: make(chan struct{})}
}

func (script *upstreamScript) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	script.calls.Add(1)
	model := ""
	var data []byte
	if request.Body != nil {
		var body struct {
			Model string `json:"model"`
		}
		data, _ = io.ReadAll(request.Body)
		_ = json.Unmarshal(data, &body)
		model = body.Model
	}
	if request.URL.Path == "/v1/timeout" {
		<-request.Context().Done()
		return
	}
	if request.URL.Path == "/v1/hold" {
		active := script.active.Add(1)
		for {
			old := script.maxActive.Load()
			if active <= old || script.maxActive.CompareAndSwap(old, active) {
				break
			}
		}
		if active == 1 {
			close(script.holdStarted)
		}
		defer script.active.Add(-1)
		response.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := response.(http.Flusher)
		_, _ = io.WriteString(response, "data: hold\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		select {
		case <-script.holdRelease:
		case <-request.Context().Done():
			script.cancelled.Add(1)
			return
		}
	}
	if request.URL.Path == "/v1/error" {
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(response, `{"error":"provider failure"}`)
		return
	}
	stream := strings.Contains(request.URL.Path, "stream")
	if request.URL.Path == "/v1/chat/completions" {
		var body struct {
			Stream bool `json:"stream"`
		}
		_ = json.Unmarshal(data, &body)
		stream = body.Stream
		if request.Header.Get("X-Integration-Convert") == "1" {
			stream = true
		}
	}
	if model == "json" || request.URL.Path == "/v1/json" {
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"id":"json","usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2},"choices":[]}`)
		return
	}
	if stream || request.URL.Path == "/v1/sse" {
		response.Header().Set("Content-Type", "text/event-stream")
		response.Header().Set("Cache-Control", "no-cache")
		flusher, _ := response.(http.Flusher)
		_, _ = io.WriteString(response, "data: {\"choices\":[{\"delta\":{\"content\":\"fragment\"}}]}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		_, _ = io.WriteString(response, "data: {\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n")
		return // Deliberately no [DONE]: upstream EOF is the terminal event.
	}
	response.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(response, `{"id":"json","usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2},"choices":[]}`)
}

func adminRequest(t *testing.T, gateway *gatewayHarness, method, path string, body any) (int, []byte, http.Header) {
	t.Helper()
	var input io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		input = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, gateway.baseURL+path, input)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+adminSecret)
	request.Header.Set("Content-Type", "application/json")
	response, err := gateway.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, data, response.Header
}

func createKey(t *testing.T, gateway *gatewayHarness, name string) (string, string) {
	t.Helper()
	status, data, _ := adminRequest(t, gateway, http.MethodPost, "/admin/v1/keys", map[string]string{"name": name})
	if status != http.StatusCreated {
		t.Fatalf("create key status=%d body=%s", status, data)
	}
	var result struct{ ID, Key string }
	if err := json.Unmarshal(data, &result); err != nil || result.ID == "" || result.Key == "" {
		t.Fatalf("create key response=%s err=%v", data, err)
	}
	return result.ID, result.Key
}

func updatePolicy(t *testing.T, gateway *gatewayHarness, id string, policy string) {
	t.Helper()
	status, data, _ := adminRequest(t, gateway, http.MethodPut, "/admin/v1/keys/"+id+"/policy", map[string]any{"enabled": true, "policy": json.RawMessage(policy)})
	if status != http.StatusOK {
		t.Fatalf("update policy status=%d body=%s", status, data)
	}
}

func gatewayRequest(t *testing.T, gateway *gatewayHarness, method, path, rawKey string, body string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	request, err := http.NewRequest(method, gateway.baseURL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+rawKey)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := gateway.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, data
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("condition was not reached before deadline")
		}
	}
}

// TestT159Lifecycle exercises administrative publication, all three response
// representations, real token/budget admission, durable history, body capture,
// and a listener shutdown after the complete request lifecycle.
func TestT159Lifecycle(t *testing.T) {
	script := newUpstreamScript()
	gateway := newHarness(t, script, 32, 32, nil)
	id, key := createKey(t, gateway, "lifecycle")
	updatePolicy(t, gateway, id, `{"allowed_models":["alpha","json"],"log_request_body":true,"log_response_body":true}`)

	// JSON validates ordinary authenticated proxying and usage observation.
	jsonResponse, jsonBody := gatewayRequest(t, gateway, http.MethodPost, "/v1/chat/completions", key, `{"model":"json"}`, map[string]string{"Content-Type": "application/json"})
	if jsonResponse.StatusCode != http.StatusOK || !bytes.Contains(jsonBody, []byte(`"total_tokens":2`)) {
		t.Fatalf("authenticated JSON = %d %s", jsonResponse.StatusCode, jsonBody)
	}
	// Transparent SSE must flush chunks and close on upstream EOF without waiting
	// for [DONE], preserving the provider's wire bytes exactly.
	streamResponse, streamBody := gatewayRequest(t, gateway, http.MethodPost, "/v1/chat/completions", key, `{"model":"alpha","stream":true}`, map[string]string{"Content-Type": "application/json"})
	wantSSE := "data: {\"choices\":[{\"delta\":{\"content\":\"fragment\"}}]}\n\ndata: {\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n"
	if streamResponse.StatusCode != http.StatusOK || streamResponse.Header.Get("Content-Type") != "text/event-stream" || string(streamBody) != wantSSE {
		t.Fatalf("transparent SSE = %d %q", streamResponse.StatusCode, streamBody)
	}
	// A non-stream client is converted only because actual upstream Content-Type
	// is SSE; the client receives one OpenAI-compatible JSON document.
	convertedResponse, convertedBody := gatewayRequest(t, gateway, http.MethodPost, "/v1/chat/completions", key, `{"model":"alpha","stream":false}`, map[string]string{"Content-Type": "application/json", "X-Integration-Convert": "1"})
	if convertedResponse.StatusCode != http.StatusOK || !bytes.Contains(convertedBody, []byte(`"total_tokens":2`)) || convertedResponse.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("converted SSE = %d %q", convertedResponse.StatusCode, convertedBody)
	}

	// A second key proves token and budget reservations reject before upstream;
	// the first key's larger policy remains isolated from this exhaustion.
	limitedID, limitedKey := createKey(t, gateway, "limited")
	updatePolicy(t, gateway, limitedID, `{"allowed_models":["limited"],"token_windows":[{"amount":1000000,"duration":"1m"}],"token_mode":"usage_only"}`)
	limitedResponse, limitedBody := gatewayRequest(t, gateway, http.MethodPost, "/v1/chat/completions", limitedKey, `{"model":"limited"}`, map[string]string{"Content-Type": "application/json"})
	if limitedResponse.StatusCode != http.StatusTooManyRequests || (!bytes.Contains(limitedBody, []byte("token_limit_exceeded")) && !bytes.Contains(limitedBody, []byte("budget_limit_exceeded"))) {
		t.Fatalf("token/budget enforcement = %d %s", limitedResponse.StatusCode, limitedBody)
	}

	// History is asynchronous, so wait on the real SQLite worker rather than
	// sleeping. List, detail, and body download are all live admin HTTP calls.
	waitFor(t, 5*time.Second, func() bool { return gateway.historyW.Stats().Persisted >= 4 })
	status, data, _ := adminRequest(t, gateway, http.MethodGet, "/admin/v1/requests?limit=100", nil)
	if status != http.StatusOK {
		t.Fatalf("history list status=%d body=%s", status, data)
	}
	var listing struct {
		Requests []struct {
			RequestID string `json:"request_id"`
			Total     *int64 `json:"total_tokens"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(data, &listing); err != nil || len(listing.Requests) < 4 {
		t.Fatalf("history list = %s err=%v", data, err)
	}
	requestID := ""
	var detailBody []byte
	for _, request := range listing.Requests {
		status, candidate, _ := adminRequest(t, gateway, http.MethodGet, "/admin/v1/requests/"+request.RequestID, nil)
		if status == http.StatusOK && bytes.Contains(candidate, []byte("client_request")) {
			requestID, detailBody = request.RequestID, candidate
			break
		}
	}
	if requestID == "" {
		t.Fatalf("history detail did not expose captured request: %s", data)
	}
	if !bytes.Contains(detailBody, []byte("has_bodies")) {
		t.Fatalf("history detail = %s", detailBody)
	}
	status, bodyData, headers := adminRequest(t, gateway, http.MethodGet, "/admin/v1/requests/"+requestID+"/bodies/client_request", nil)
	if status != http.StatusOK || headers.Get("Content-Type") != "application/octet-stream" || len(bodyData) == 0 {
		t.Fatalf("body retrieval = %d %q", status, bodyData)
	}

	// Shutdown first stops accepting HTTP work, drains accounting/telemetry, and
	// closes SQLite only after every real worker has finished using it.
	gateway.close()
	if _, err := gateway.client.Get(gateway.baseURL + "/v1/models"); err == nil {
		t.Fatal("request unexpectedly succeeded after graceful shutdown")
	}
}

// TestT159ParallelPolicyIsolation sends independent live requests through two
// keys at once. Distinct model, token, and concurrency policies must not leak
// reservations or slots across principals.
func TestT159ParallelPolicyIsolation(t *testing.T) {
	script := newUpstreamScript()
	gateway := newHarness(t, script, 64, 64, nil)
	idA, keyA := createKey(t, gateway, "parallel-a")
	idB, keyB := createKey(t, gateway, "parallel-b")
	updatePolicy(t, gateway, idA, `{"allowed_models":["a"],"max_concurrent_requests":1}`)
	updatePolicy(t, gateway, idB, `{"allowed_models":["b"],"max_concurrent_requests":2}`)

	var requests sync.WaitGroup
	for _, rawKey := range []string{keyA, keyB, keyB} {
		requests.Add(1)
		go func(key string) {
			defer requests.Done()
			request, _ := http.NewRequest(http.MethodGet, gateway.baseURL+"/v1/hold", nil)
			request.Header.Set("Authorization", "Bearer "+key)
			response, err := gateway.client.Do(request)
			if err == nil {
				_, _ = io.ReadAll(response.Body)
				_ = response.Body.Close()
			}
		}(rawKey)
	}
	waitFor(t, 3*time.Second, func() bool { return script.active.Load() >= 3 })
	// Both B requests and A's one request are concurrently admitted. A second
	// A request is then rejected while B remains able to use its second slot.
	secondA, bodyA := gatewayRequest(t, gateway, http.MethodGet, "/v1/hold", keyA, "", nil)
	if secondA.StatusCode != http.StatusTooManyRequests || !bytes.Contains(bodyA, []byte("concurrency_limit_exceeded")) {
		t.Fatalf("A isolation rejection = %d %s", secondA.StatusCode, bodyA)
	}
	close(script.holdRelease)
	requests.Wait()
	if got := script.maxActive.Load(); got > 3 {
		t.Fatalf("unexpected upstream active count %d", got)
	}
}

// TestT159FailuresAndTelemetryDrop covers provider status/errors, bounded
// timeout and client cancellation, policy rejection, and a saturated bounded
// completion queue. Transport responses remain available while telemetry drops.
func TestT159FailuresAndTelemetryDrop(t *testing.T) {
	script := newUpstreamScript()
	client := &http.Client{Transport: &http.Transport{ResponseHeaderTimeout: 40 * time.Millisecond}}
	gateway := newHarness(t, script, 1, 64, client)
	id, key := createKey(t, gateway, "failures")
	updatePolicy(t, gateway, id, `{"allowed_models":["ok"]}`)
	if response, body := gatewayRequest(t, gateway, http.MethodGet, "/v1/error", key, "", nil); response.StatusCode != http.StatusBadGateway || !bytes.Contains(body, []byte("provider failure")) {
		t.Fatalf("upstream error = %d %s", response.StatusCode, body)
	}
	timeoutRequest, err := http.NewRequest(http.MethodGet, gateway.baseURL+"/v1/timeout", nil)
	if err != nil {
		t.Fatal(err)
	}
	timeoutRequest.Header.Set("Authorization", "Bearer "+key)
	if _, err := gateway.client.Do(timeoutRequest); err == nil {
		t.Fatal("timeout request unexpectedly completed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, gateway.baseURL+"/v1/hold", nil)
	request.Header.Set("Authorization", "Bearer "+key)
	clientResult := make(chan error, 1)
	go func() {
		response, requestErr := transport.NewClient().Do(request)
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		clientResult <- requestErr
	}()
	select {
	case <-script.holdStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation request did not reach upstream")
	}
	cancel()
	select {
	case <-clientResult:
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation client did not return")
	}
	waitFor(t, 3*time.Second, func() bool { return script.cancelled.Load() > 0 })
	if rejected, body := gatewayRequest(t, gateway, http.MethodPost, "/v1/chat/completions", key, `{"model":"denied"}`, map[string]string{"Content-Type": "application/json"}); rejected.StatusCode != http.StatusForbidden || !bytes.Contains(body, []byte("model_not_allowed")) {
		t.Fatalf("rejected request = %d %s", rejected.StatusCode, body)
	}

	// Saturate the real bounded completion queue directly. This is intentionally
	// a component-level assertion inside the end-to-end harness: the HTTP path
	// has already exercised the same logger, and enqueue must never block it.
	for i := 0; i < 1024; i++ {
		_ = gateway.logger.Enqueue(httpserver.CompletionRecord{})
	}
	if gateway.logger.Dropped() == 0 {
		t.Fatal("completion queue did not report a saturated telemetry drop")
	}
	waitFor(t, 5*time.Second, func() bool {
		status, candidate, _ := adminRequest(t, gateway, http.MethodGet, "/admin/v1/requests?limit=100", nil)
		if status != http.StatusOK {
			return false
		}
		return bytes.Contains(candidate, []byte("upstream_error")) && bytes.Contains(candidate, []byte("cancelled"))
	})
}

type blockingSlogHandler struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	count   atomic.Int64
}

func (handler *blockingSlogHandler) Enabled(context.Context, slog.Level) bool { return true }
func (handler *blockingSlogHandler) Handle(context.Context, slog.Record) error {
	handler.count.Add(1)
	handler.once.Do(func() { close(handler.started); <-handler.release })
	return nil
}
func (handler *blockingSlogHandler) WithAttrs([]slog.Attr) slog.Handler { return handler }
func (handler *blockingSlogHandler) WithGroup(string) slog.Handler      { return handler }

// TestT159StreamPerformanceRegression measures complete client-visible stream
// lifetimes against direct provider requests. It uses means and a generous
// local-CI tolerance rather than subtracting unrelated wall-clock timestamps;
// the hard regression guards are mean close <50ms and no request >10s.
func TestT159StreamPerformanceRegression(t *testing.T) {
	script := newUpstreamScript()
	gateway := newHarness(t, script, 256, 256, nil)
	_, key := createKey(t, gateway, "performance")

	type streamMeasurement struct {
		ttfb, closeDelay time.Duration
	}
	measure := func(target string, headers http.Header) streamMeasurement {
		request, _ := http.NewRequest(http.MethodGet, target, nil)
		request.Header = headers.Clone()
		started := time.Now()
		response, err := gateway.client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var firstByte [1]byte
		if _, err = io.ReadFull(response.Body, firstByte[:]); err != nil {
			t.Fatal(err)
		}
		first := time.Now()
		_, err = io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return streamMeasurement{ttfb: first.Sub(started), closeDelay: time.Since(first)}
	}
	direct := make([]streamMeasurement, 5)
	for i := range direct {
		direct[i] = measure(gateway.upstream.URL+"/v1/sse", nil)
	}
	through := make([]streamMeasurement, 5)
	for i := range through {
		request, _ := http.NewRequest(http.MethodGet, gateway.baseURL+"/v1/sse", nil)
		request.Header.Set("Authorization", "Bearer "+key)
		through[i] = measure(request.URL.String(), request.Header)
	}
	mean := func(values []streamMeasurement, selectValue func(streamMeasurement) time.Duration) time.Duration {
		var total time.Duration
		for _, value := range values {
			total += selectValue(value)
		}
		return total / time.Duration(len(values))
	}
	baselineTTFB := mean(direct, func(value streamMeasurement) time.Duration { return value.ttfb })
	gatewayTTFB := mean(through, func(value streamMeasurement) time.Duration { return value.ttfb })
	baselineClose := mean(direct, func(value streamMeasurement) time.Duration { return value.closeDelay })
	gatewayClose := mean(through, func(value streamMeasurement) time.Duration { return value.closeDelay })
	// 10ms is the target. The second clause avoids false failures when a busy
	// shared CI runner inflates both direct and gateway measurements similarly.
	ttfbOverhead := gatewayTTFB - baselineTTFB
	closeOverhead := gatewayClose - baselineClose
	if (ttfbOverhead > 10*time.Millisecond && ttfbOverhead > baselineTTFB/2+10*time.Millisecond) || (closeOverhead > 10*time.Millisecond && closeOverhead > baselineClose/2+10*time.Millisecond) {
		t.Fatalf("TTFB/close overhead=%s/%s (direct=%s/%s gateway=%s/%s)", ttfbOverhead, closeOverhead, baselineTTFB, baselineClose, gatewayTTFB, gatewayClose)
	}

	const concurrent = 100
	durations := make(chan time.Duration, concurrent)
	var wg sync.WaitGroup
	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			request, _ := http.NewRequest(http.MethodGet, gateway.baseURL+"/v1/sse", nil)
			request.Header.Set("Authorization", "Bearer "+key)
			response, err := gateway.client.Do(request)
			if err != nil {
				t.Errorf("parallel stream request: %v", err)
				return
			}
			started := time.Now()
			_, _ = io.ReadAll(response.Body)
			response.Body.Close()
			durations <- time.Since(started)
		}()
	}
	wg.Wait()
	close(durations)
	var total time.Duration
	count := 0
	for duration := range durations {
		total += duration
		count++
		if duration > 10*time.Second {
			t.Fatalf("parallel stream took %s", duration)
		}
	}
	if count != concurrent || total/time.Duration(count) >= 50*time.Millisecond {
		t.Fatalf("parallel stream mean=%s requests=%d want mean <50ms and %d requests", total/time.Duration(count), count, concurrent)
	}
}
