// Package integration contains the cross-subsystem gateway contract tests.
// The tests deliberately use real listeners and SQLite: httptest handlers are
// suitable for an external provider, but an in-process handler cannot expose
// flushing, EOF, cancellation, or listener shutdown behaviour.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"go.uber.org/goleak"
	"gopkg.in/yaml.v3"
)

const (
	adminSecret    = "integration-admin-secret"
	upstreamSecret = "integration-upstream-secret"
	pepper         = "integration-auth-pepper"
)

type gatewayHarness struct {
	database     *storage.DB
	history      *storage.RequestHistoryRepository
	usage        *httpserver.UsageObservationWorker
	tokenAgg     *storage.UsageAggregateAccumulator
	budgetAgg    *storage.BudgetAccumulator
	budgetDeltas atomic.Int64
	logger       *httpserver.CompletionLogger
	historyW     *httpserver.HistoryPersistenceWorker
	server       *http.Server
	listener     net.Listener
	baseURL      string
	upstream     *httptest.Server
	client       *http.Client
	closed       atomic.Bool
	lifecycle    *httpserver.RequestLifecycle
}

// TestMain checks only goroutines owned by this package. The integration
// harness closes every listener, worker, and database itself, so no broad
// runtime or external-process ignores are needed here.
func TestMain(main *testing.M) {
	goleak.VerifyTestMain(main)
}

// newHarness wires the same repositories, limiters, observation worker,
// accounting accumulators, completion logger, and history worker used by the
// production entry point. Only the upstream provider is an HTTP test double.
func newHarness(t *testing.T, upstream http.Handler, completionCapacity, historyCapacity int, client *http.Client) *gatewayHarness {
	return newHarnessWithLogger(t, upstream, completionCapacity, historyCapacity, client, slog.NewTextHandler(io.Discard, nil))
}

// newHarnessWithLogger keeps the integration harness production-shaped while
// allowing the telemetry test to install a deliberately blocked sink.
func newHarnessWithLogger(t *testing.T, upstream http.Handler, completionCapacity, historyCapacity int, client *http.Client, logHandler slog.Handler) *gatewayHarness {
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
	gateway := &gatewayHarness{budgetDeltas: atomic.Int64{}}
	budget.SetCommittedDeltaSink(func(delta limiter.CommittedBudgetDelta) {
		gateway.budgetDeltas.Add(delta.Delta)
		budgetAgg.Sink(delta)
	})
	logger := httpserver.NewCompletionLogger(slog.New(logHandler), completionCapacity)
	historyW := httpserver.NewHistoryPersistenceWorker(httpserver.HistoryPersistenceWorkerOptions{
		Repository: history, Capacity: historyCapacity,
		RequestRetention: time.Hour, BodyRetention: time.Hour,
	})
	if err := historyW.WaitReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	pricing := integrationPricing(t)
	upstreamClient := transport.NewClient()
	if client != nil && client.Transport != nil {
		if clientTransport, ok := client.Transport.(*http.Transport); ok {
			if upstreamTransport, ok := upstreamClient.Transport.(*http.Transport); ok {
				upstreamTransport.ResponseHeaderTimeout = clientTransport.ResponseHeaderTimeout
			}
		}
	}
	handler, err := httpserver.NewHandlerWithAdminAndLimitersAndTokenConfigAndTokenLimiterAndUsageObservationWorkerAndHistory(
		upstreamClient, upstreamServer.URL, upstreamSecret, adminSecret, pepper,
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
	lifecycle := httpserver.NewRequestLifecycle()
	server := &http.Server{Handler: lifecycle.Handler(handler)}
	go func() { _ = server.Serve(listener) }()
	if client == nil {
		client = transport.NewClient()
	}
	gatewayClient := client
	if client.Transport != nil {
		// A short deadline supplied by a failure scenario belongs to the
		// gateway-to-upstream transport. The client-to-gateway transport must
		// remain independent so timeout responses can be observed and closed.
		if _, ok := client.Transport.(*http.Transport); ok {
			gatewayClient = transport.NewClient()
		}
	}
	gateway.database, gateway.history = database, history
	gateway.usage, gateway.tokenAgg, gateway.budgetAgg = usage, tokenAgg, budgetAgg
	gateway.logger, gateway.historyW = logger, historyW
	gateway.server, gateway.listener, gateway.baseURL = server, listener, "http://"+listener.Addr().String()
	gateway.lifecycle = lifecycle
	gateway.upstream, gateway.client = upstreamServer, gatewayClient
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
	gateway.lifecycle.StopAccepting()
	_ = gateway.lifecycle.Wait(ctx)
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
	requestIDs  chan string
}

func newUpstreamScript() *upstreamScript {
	return &upstreamScript{holdStarted: make(chan struct{}), holdRelease: make(chan struct{}), requestIDs: make(chan string, 128)}
}

func (script *upstreamScript) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	script.calls.Add(1)
	select {
	case script.requestIDs <- request.Header.Get("X-Gateway-Request-ID"):
	default:
	}
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
		script.cancelled.Add(1)
		return
	}
	hold := request.URL.Path == "/v1/hold" || model == "hold" || model == "shutdown"
	if hold {
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
		if model != "shutdown" {
			response.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := response.(http.Flusher)
			_, _ = io.WriteString(response, "data: hold\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		}
		select {
		case <-script.holdRelease:
		case <-request.Context().Done():
			script.cancelled.Add(1)
			return
		}
		if model == "shutdown" {
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"id":"shutdown","usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2},"choices":[]}`)
		} else {
			_, _ = io.WriteString(response, "data: {\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n")
		}
	}
	if request.URL.Path == "/v1/error" {
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(response, `{"error":"provider failure"}`)
		return
	}
	if model == "json" {
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"id":"json","usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2},"choices":[]}`)
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
	if request.URL.Path == "/v1/json" {
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

type integrationHistoryItem struct {
	RequestID        string  `json:"request_id"`
	Path             string  `json:"path"`
	DownstreamStatus *int64  `json:"downstream_status"`
	UpstreamStatus   *int64  `json:"upstream_status"`
	TerminalOutcome  *string `json:"terminal_outcome"`
	UpstreamStarted  bool    `json:"upstream_started"`
	TotalTokens      *int64  `json:"total_tokens"`
	CostMicros       *int64  `json:"cost_micros"`
}

func historyItems(t *testing.T, gateway *gatewayHarness) []integrationHistoryItem {
	t.Helper()
	records, _, err := gateway.history.ListRequests(context.Background(), storage.ListRequestsFilter{}, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	items := make([]integrationHistoryItem, 0, len(records))
	for _, record := range records {
		item := integrationHistoryItem{RequestID: record.RequestID, Path: record.Path, UpstreamStarted: record.UpstreamStarted}
		if record.DownstreamStatus.Known {
			item.DownstreamStatus = &record.DownstreamStatus.Value
		}
		if record.UpstreamStatus.Known {
			item.UpstreamStatus = &record.UpstreamStatus.Value
		}
		if record.TerminalOutcome != "" {
			outcome := record.TerminalOutcome
			item.TerminalOutcome = &outcome
		}
		if record.TotalTokens.Known {
			item.TotalTokens = &record.TotalTokens.Value
		}
		if record.CostMicros.Known {
			item.CostMicros = &record.CostMicros.Value
		}
		items = append(items, item)
	}
	return items
}

func waitForHistoryItem(t *testing.T, gateway *gatewayHarness, path string, outcome httpserver.TerminalOutcome) integrationHistoryItem {
	t.Helper()
	var found integrationHistoryItem
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		for _, item := range historyItems(t, gateway) {
			if item.Path == path && item.TerminalOutcome != nil && *item.TerminalOutcome == string(outcome) {
				found = item
				return found
			}
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("history item %s/%s not found; items=%+v", path, outcome, historyItems(t, gateway))
		}
	}
}

func waitForHistoryRequest(t *testing.T, gateway *gatewayHarness, requestID string, outcome httpserver.TerminalOutcome) integrationHistoryItem {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		for _, item := range historyItems(t, gateway) {
			if item.RequestID == requestID && item.TerminalOutcome != nil && *item.TerminalOutcome == string(outcome) {
				return item
			}
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			items := historyItems(t, gateway)
			details := make([]string, 0, len(items))
			for _, item := range items {
				details = append(details, fmt.Sprintf("%s path=%s outcome=%s upstream_started=%t downstream=%s upstream=%s tokens=%s cost=%s", item.RequestID, item.Path, optionalString(item.TerminalOutcome), item.UpstreamStarted, optionalInt(item.DownstreamStatus), optionalInt(item.UpstreamStatus), optionalInt(item.TotalTokens), optionalInt(item.CostMicros)))
			}
			t.Fatalf("history request %s/%s not found; items=%s stats=%+v", requestID, outcome, strings.Join(details, "; "), gateway.historyW.Stats())
		}
	}
}

func waitForBudgetBucket(t *testing.T, gateway *gatewayHarness, keyID string, want int64) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		var spent int64
		err := gateway.database.QueryRowContext(context.Background(), `SELECT spent_micros FROM budget_buckets WHERE api_key_id = ? AND period_kind = 'total'`, keyID).Scan(&spent)
		if err == nil && spent == want {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("budget bucket %s did not reach %d: spent=%d err=%v", keyID, want, spent, err)
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

// TestT159GracefulShutdownInFlight uses the production request lifecycle seam
// around a real held upstream request. Shutdown must close the listener first,
// let the admitted request finish, reconcile its usage, drain completion and
// history, and close SQLite only after all database users have stopped.
func TestT159GracefulShutdownInFlight(t *testing.T) {
	script := newUpstreamScript()
	completion := newCompletionCaptureHandler()
	gateway := newHarnessWithLogger(t, script, 16, 16, nil, completion)
	id, key := createKey(t, gateway, "shutdown-in-flight")
	updatePolicy(t, gateway, id, `{"allowed_models":["hold"],"budget_limits":[{"amount_micros":10,"period":"total"}]}`)

	request, err := http.NewRequest(http.MethodPost, gateway.baseURL+"/v1/chat/completions", strings.NewReader(`{"model":"hold","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")
	responseReady := make(chan *http.Response, 1)
	requestDone := make(chan error, 1)
	go func() {
		response, requestErr := gateway.client.Do(request)
		if response != nil {
			responseReady <- response
		}
		requestDone <- requestErr
	}()
	select {
	case <-script.holdStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight request did not reach upstream")
	}
	var response *http.Response
	select {
	case response = <-responseReady:
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight request did not receive headers")
	}
	requestID := response.Header.Get("X-Gateway-Request-ID")
	if requestID == "" {
		t.Fatal("in-flight request did not receive a gateway request ID")
	}
	shutdownDone := make(chan error, 1)
	shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		shutdownDone <- gateway.server.Shutdown(shutdownContext)
	}()
	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown completed before in-flight request: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	// The listener is closed while the admitted request remains held. A new
	// request must not cross the lifecycle accept boundary.
	newRequest, err := http.NewRequest(http.MethodGet, gateway.baseURL+"/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	newRequest.Header.Set("Authorization", "Bearer "+key)
	newResponse, newErr := gateway.client.Do(newRequest)
	if newResponse != nil {
		_ = newResponse.Body.Close()
	}
	if newErr == nil {
		t.Fatal("new request succeeded after shutdown stopped accepting")
	}

	close(script.holdRelease)
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal("in-flight response failed: ", err)
	}
	if !bytes.Contains(body, []byte(`"total_tokens":2`)) {
		t.Fatalf("in-flight response = %q", body)
	}
	if err := <-requestDone; err != nil {
		t.Fatal("in-flight request failed: ", err)
	}
	if err := <-shutdownDone; err != nil {
		t.Fatal("HTTP shutdown failed: ", err)
	}

	gateway.lifecycle.StopAccepting()
	if err := gateway.lifecycle.Wait(shutdownContext); err != nil {
		t.Fatal("request lifecycle did not drain: ", err)
	}
	if err := gateway.usage.Drain(shutdownContext); err != nil {
		t.Fatal("usage drain failed: ", err)
	}
	if err := gateway.tokenAgg.Shutdown(shutdownContext); err != nil {
		t.Fatal("token reconciliation drain failed: ", err)
	}
	if err := gateway.budgetAgg.Shutdown(shutdownContext); err != nil {
		t.Fatal("budget reconciliation drain failed: ", err)
	}
	if err := gateway.logger.Shutdown(shutdownContext); err != nil {
		t.Fatal("completion telemetry drain failed: ", err)
	}
	if err := gateway.historyW.Shutdown(shutdownContext); err != nil {
		t.Fatal("history drain failed: ", err)
	}
	completed := waitForHistoryRequest(t, gateway, requestID, httpserver.TerminalOutcomeComplete)
	if !completed.UpstreamStarted || completed.UpstreamStatus == nil || *completed.UpstreamStatus != http.StatusOK {
		t.Fatalf("in-flight history = %+v", completed)
	}
	waitForBudgetBucket(t, gateway, id, 1)
	if completion.requestID("/v1/chat/completions") != requestID {
		t.Fatalf("completion telemetry request ID = %q, want %q", completion.requestID("/v1/chat/completions"), requestID)
	}
	if err := gateway.database.Close(); err != nil {
		t.Fatal("storage close failed: ", err)
	}
	var probe int
	if err := gateway.database.QueryRowContext(context.Background(), "SELECT 1").Scan(&probe); err == nil {
		t.Fatal("database operation succeeded after storage close")
	}
	gateway.closed.Store(true)
	_ = gateway.listener.Close()
	gateway.upstream.Close()
}

// TestT159LiveBudgetReconciliation proves that a real HTTP response observes
// usage and cost, spends the reservation, rejects the next live request before
// upstream, and persists the reconciled total in SQLite and request history.
func TestT159LiveBudgetReconciliation(t *testing.T) {
	script := newUpstreamScript()
	gateway := newHarness(t, script, 16, 16, nil)
	id, key := createKey(t, gateway, "budget-reconciliation")
	updatePolicy(t, gateway, id, `{"allowed_models":["json"],"budget_limits":[{"amount_micros":3,"period":"total"}]}`)
	response, body := gatewayRequest(t, gateway, http.MethodPost, "/v1/chat/completions", key, `{"model":"json","max_tokens":2}`, map[string]string{"Content-Type": "application/json"})
	firstRequestID := response.Header.Get("X-Gateway-Request-ID")
	if firstRequestID == "" {
		t.Fatal("budget request did not return a gateway request ID")
	}
	if response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"total_tokens":2`)) {
		t.Fatalf("budget-limited live request = %d %s", response.StatusCode, body)
	}
	if got := script.calls.Load(); got != 1 {
		t.Fatalf("upstream calls after first budget request = %d, want 1", got)
	}
	select {
	case upstreamRequestID := <-script.requestIDs:
		if upstreamRequestID != firstRequestID {
			t.Fatalf("first request ID mismatch: response=%s upstream=%s", firstRequestID, upstreamRequestID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("upstream request ID was not observed")
	}
	// Wait for the asynchronous usage observation to reconcile the first
	// reservation before attempting the second admission; this makes the
	// reservation/rejection assertion deterministic without a timing sleep.
	waitFor(t, 5*time.Second, func() bool { return gateway.budgetDeltas.Load() == 2 })
	rejected, rejectedBody := gatewayRequest(t, gateway, http.MethodPost, "/v1/chat/completions", key, `{"model":"json","max_tokens":2}`, map[string]string{"Content-Type": "application/json"})
	if rejected.StatusCode != http.StatusTooManyRequests || !bytes.Contains(rejectedBody, []byte(`"code":"budget_exceeded"`)) {
		t.Fatalf("budget rejection = %d %s", rejected.StatusCode, rejectedBody)
	}
	if got := script.calls.Load(); got != 1 {
		t.Fatalf("rejected request reached upstream: calls=%d", got)
	}
	completed := waitForHistoryRequest(t, gateway, firstRequestID, httpserver.TerminalOutcomeComplete)
	if completed.UpstreamStatus == nil || *completed.UpstreamStatus != http.StatusOK || completed.DownstreamStatus == nil || *completed.DownstreamStatus != http.StatusOK || !completed.UpstreamStarted {
		t.Fatalf("completed budget history = %+v", completed)
	}
	if completed.TotalTokens == nil || *completed.TotalTokens != 2 || completed.CostMicros == nil || *completed.CostMicros != 2 {
		t.Fatalf("completed usage/cost = %+v", completed)
	}
	waitForBudgetBucket(t, gateway, id, 2)
	rejectedHistory := waitForHistoryItem(t, gateway, "/v1/chat/completions", httpserver.TerminalOutcomePreUpstream)
	if rejectedHistory.UpstreamStarted || rejectedHistory.UpstreamStatus != nil || rejectedHistory.TotalTokens != nil || rejectedHistory.CostMicros != nil {
		t.Fatalf("rejected budget history retained post-admission facts: %+v", rejectedHistory)
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
	completionCapture := newCompletionCaptureHandler()
	gateway := newHarnessWithLogger(t, script, 64, 64, client, completionCapture)
	id, key := createKey(t, gateway, "failures")
	updatePolicy(t, gateway, id, `{"allowed_models":["ok"]}`)
	errorResponse, errorBody := gatewayRequest(t, gateway, http.MethodGet, "/v1/error", key, "", nil)
	errorRequestID := errorResponse.Header.Get("X-Gateway-Request-ID")
	if errorResponse.StatusCode != http.StatusBadGateway || !bytes.Contains(errorBody, []byte("provider failure")) {
		t.Fatalf("upstream error = %d %s", errorResponse.StatusCode, errorBody)
	}
	timeoutRequest, err := http.NewRequest(http.MethodGet, gateway.baseURL+"/v1/timeout", nil)
	if err != nil {
		t.Fatal(err)
	}
	timeoutRequest.Header.Set("Authorization", "Bearer "+key)
	timeoutResponse, timeoutErr := gateway.client.Do(timeoutRequest)
	if timeoutErr != nil {
		t.Fatal("timeout request failed at gateway boundary: ", timeoutErr)
	}
	timeoutRequestID := timeoutResponse.Header.Get("X-Gateway-Request-ID")
	if timeoutResponse.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("timeout response status=%d", timeoutResponse.StatusCode)
	}
	_ = timeoutResponse.Body.Close()
	ctx, cancel := context.WithCancel(context.Background())
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, gateway.baseURL+"/v1/hold", nil)
	request.Header.Set("Authorization", "Bearer "+key)
	clientResult := make(chan error, 1)
	cancelBodyRelease := make(chan struct{})
	responseReady := make(chan string, 1)
	go func() {
		response, requestErr := transport.NewClient().Do(request)
		if response != nil {
			responseReady <- response.Header.Get("X-Gateway-Request-ID")
		}
		<-cancelBodyRelease
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
	cancelRequestID := ""
	select {
	case cancelRequestID = <-responseReady:
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation response headers were not committed")
	}
	cancel()
	close(cancelBodyRelease)
	select {
	case <-clientResult:
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation client did not return")
	}
	waitFor(t, 3*time.Second, func() bool { return script.cancelled.Load() > 0 })
	rejected, rejectedBody := gatewayRequest(t, gateway, http.MethodPost, "/v1/chat/completions", key, `{"model":"denied"}`, map[string]string{"Content-Type": "application/json"})
	rejectedRequestID := rejected.Header.Get("X-Gateway-Request-ID")
	if rejected.StatusCode != http.StatusForbidden || !bytes.Contains(rejectedBody, []byte("model_not_allowed")) {
		t.Fatalf("rejected request = %d %s", rejected.StatusCode, rejectedBody)
	}

	// Every failure is correlated through its durable request ID. Terminal
	// outcome, upstream boundary, statuses, and absent accounting are asserted
	// independently because each failure crosses a different transport boundary.
	errorHistory := waitForHistoryRequest(t, gateway, errorRequestID, httpserver.TerminalOutcomeUpstreamError)
	if !errorHistory.UpstreamStarted || errorHistory.DownstreamStatus == nil || *errorHistory.DownstreamStatus != http.StatusBadGateway || errorHistory.UpstreamStatus == nil || *errorHistory.UpstreamStatus != http.StatusBadGateway || errorHistory.TotalTokens != nil || errorHistory.CostMicros != nil {
		t.Fatalf("upstream error history = %+v", errorHistory)
	}
	timeoutHistory := waitForHistoryRequest(t, gateway, timeoutRequestID, httpserver.TerminalOutcomeUpstreamError)
	if !timeoutHistory.UpstreamStarted || timeoutHistory.DownstreamStatus == nil || *timeoutHistory.DownstreamStatus != http.StatusGatewayTimeout || timeoutHistory.UpstreamStatus != nil || timeoutHistory.TotalTokens != nil || timeoutHistory.CostMicros != nil {
		t.Fatalf("timeout history = %+v", timeoutHistory)
	}
	if cancelRequestID == "" {
		t.Fatal("cancellation response did not include a gateway request ID")
	}
	cancelHistory := waitForHistoryRequest(t, gateway, cancelRequestID, httpserver.TerminalOutcomeCancelled)
	if !cancelHistory.UpstreamStarted || cancelHistory.DownstreamStatus == nil || *cancelHistory.DownstreamStatus != http.StatusOK || cancelHistory.UpstreamStatus == nil || *cancelHistory.UpstreamStatus != http.StatusOK || cancelHistory.TotalTokens != nil || cancelHistory.CostMicros != nil {
		t.Fatalf("cancellation history = request=%s upstream_started=%t downstream=%s upstream=%s outcome=%s tokens=%v cost=%v", cancelHistory.RequestID, cancelHistory.UpstreamStarted, optionalInt(cancelHistory.DownstreamStatus), optionalInt(cancelHistory.UpstreamStatus), optionalString(cancelHistory.TerminalOutcome), optionalInt(cancelHistory.TotalTokens), optionalInt(cancelHistory.CostMicros))
	}
	rejectHistory := waitForHistoryRequest(t, gateway, rejectedRequestID, httpserver.TerminalOutcomePreUpstream)
	if rejectHistory.UpstreamStarted || rejectHistory.DownstreamStatus == nil || *rejectHistory.DownstreamStatus != http.StatusForbidden || rejectHistory.UpstreamStatus != nil || rejectHistory.TotalTokens != nil || rejectHistory.CostMicros != nil {
		t.Fatalf("policy rejection history = %+v", rejectHistory)
	}
}

func optionalInt(value *int64) string {
	if value == nil {
		return "null"
	}
	return fmt.Sprintf("%d", *value)
}

func optionalString(value *string) string {
	if value == nil {
		return "null"
	}
	return *value
}

// TestT159LiveTelemetryBackpressure uses only live HTTP requests to fill the
// completion queue while its sink is blocked. It proves response transport and
// durable accounting remain nonblocking even when detailed telemetry drops.
func TestT159LiveTelemetryBackpressure(t *testing.T) {
	blocked := &blockingSlogHandler{started: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(blocked.Release)
	script := newUpstreamScript()
	gateway := newHarnessWithLogger(t, script, 1, 256, nil, blocked)
	id, key := createKey(t, gateway, "telemetry-backpressure")
	updatePolicy(t, gateway, id, `{"allowed_models":["json"],"budget_limits":[{"amount_micros":100,"period":"total"}]}`)
	first, firstBody := gatewayRequest(t, gateway, http.MethodPost, "/v1/chat/completions", key, `{"model":"json"}`, map[string]string{"Content-Type": "application/json"})
	if first.StatusCode != http.StatusOK || !bytes.Contains(firstBody, []byte(`"total_tokens":2`)) {
		t.Fatalf("first live telemetry request = %d %s", first.StatusCode, firstBody)
	}
	select {
	case <-blocked.started:
	case <-time.After(2 * time.Second):
		t.Fatal("completion sink did not become blocked after a live request")
	}
	const requests = 12
	results := make(chan error, requests)
	started := time.Now()
	for i := 0; i < requests; i++ {
		go func() {
			response, body := gatewayRequest(t, gateway, http.MethodPost, "/v1/chat/completions", key, `{"model":"json"}`, map[string]string{"Content-Type": "application/json"})
			if response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"total_tokens":2`)) {
				results <- fmt.Errorf("live response = %d %s", response.StatusCode, body)
				return
			}
			results <- nil
		}()
	}
	for i := 0; i < requests; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(started); elapsed >= 500*time.Millisecond {
		t.Fatalf("live transport blocked by telemetry sink for %s", elapsed)
	}
	if gateway.logger.Dropped() == 0 {
		t.Fatal("live telemetry queue did not drop while sink was blocked")
	}
	waitFor(t, 5*time.Second, func() bool { return gateway.budgetDeltas.Load() == (requests+1)*2 })
	// History persistence has its own bounded queue and is independent from the
	// blocked completion sink. Wait on the worker's deterministic persistence
	// counter rather than repeatedly querying a moving admin page.
	// History is a best-effort detailed sink too: prove it persisted at least
	// one live record while allowing bounded drops under the blocked logger.
	waitFor(t, 5*time.Second, func() bool { return gateway.historyW.Stats().Persisted > 0 || gateway.historyW.Stats().Dropped > 0 })
	// Do not leave the detail sink blocked until t.Cleanup: shutdown must be
	// able to drain the logger and close all production-shaped resources.
	blocked.Release()
}

type blockingSlogHandler struct {
	started     chan struct{}
	release     chan struct{}
	once        sync.Once
	releaseOnce sync.Once
	count       atomic.Int64
}

type completionCaptureHandler struct {
	mu      sync.Mutex
	ids     map[string]string
	changed chan struct{}
}

func newCompletionCaptureHandler() *completionCaptureHandler {
	return &completionCaptureHandler{ids: make(map[string]string), changed: make(chan struct{}, 1)}
}

func (handler *completionCaptureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (handler *completionCaptureHandler) Handle(_ context.Context, record slog.Record) error {
	var path, requestID string
	record.Attrs(func(attribute slog.Attr) bool {
		switch attribute.Key {
		case "path":
			path = attribute.Value.String()
		case "request_id":
			requestID = attribute.Value.String()
		}
		return true
	})
	if path != "" && requestID != "" {
		handler.mu.Lock()
		handler.ids[path] = requestID
		handler.mu.Unlock()
		select {
		case handler.changed <- struct{}{}:
		default:
		}
	}
	return nil
}
func (handler *completionCaptureHandler) WithAttrs([]slog.Attr) slog.Handler { return handler }
func (handler *completionCaptureHandler) WithGroup(string) slog.Handler      { return handler }

func (handler *completionCaptureHandler) requestID(path string) string {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	return handler.ids[path]
}

func (handler *blockingSlogHandler) Enabled(context.Context, slog.Level) bool { return true }
func (handler *blockingSlogHandler) Handle(context.Context, slog.Record) error {
	handler.count.Add(1)
	handler.once.Do(func() { close(handler.started); <-handler.release })
	return nil
}
func (handler *blockingSlogHandler) Release() {
	handler.releaseOnce.Do(func() { close(handler.release) })
}
func (handler *blockingSlogHandler) WithAttrs([]slog.Attr) slog.Handler { return handler }
func (handler *blockingSlogHandler) WithGroup(string) slog.Handler      { return handler }

// TestT159StreamPerformanceRegression measures client-visible SSE timing against
// paired direct provider requests. TTFB and post-first-byte close overhead are
// independent <10ms contracts; neither can hide behind the other or a compound
// total-lifetime allowance.
func TestT159StreamPerformanceRegression(t *testing.T) {
	const (
		baselineSamples = 20
		firstByteDelay  = 2 * time.Millisecond
		eofDelay        = 2 * time.Millisecond
	)
	upstream := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		time.Sleep(firstByteDelay)
		response.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := response.(http.Flusher)
		if _, err := io.WriteString(response, "data: performance\n\n"); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
		time.Sleep(eofDelay)
	})
	gateway := newHarness(t, upstream, 256, 256, nil)
	_, key := createKey(t, gateway, "performance")

	type streamMeasurement struct {
		ttfb  time.Duration
		close time.Duration
		total time.Duration
		err   error
	}
	measure := func(target string, headers http.Header) streamMeasurement {
		request, err := http.NewRequest(http.MethodGet, target, nil)
		if err != nil {
			return streamMeasurement{err: err}
		}
		request.Header = headers.Clone()
		started := time.Now()
		response, err := gateway.client.Do(request)
		if err != nil {
			return streamMeasurement{total: time.Since(started), err: err}
		}
		if response.StatusCode != http.StatusOK {
			closeErr := response.Body.Close()
			return streamMeasurement{total: time.Since(started), err: fmt.Errorf("status=%d, close=%w", response.StatusCode, closeErr)}
		}
		var firstByte [1]byte
		if _, err = io.ReadFull(response.Body, firstByte[:]); err != nil {
			closeErr := response.Body.Close()
			return streamMeasurement{total: time.Since(started), err: errors.Join(err, closeErr)}
		}
		first := time.Now()
		_, readErr := io.Copy(io.Discard, response.Body)
		closeErr := response.Body.Close()
		closed := time.Now()
		return streamMeasurement{ttfb: first.Sub(started), close: closed.Sub(first), total: closed.Sub(started), err: errors.Join(readErr, closeErr)}
	}
	measurePair := func() (streamMeasurement, streamMeasurement) {
		direct := measure(gateway.upstream.URL+"/v1/sse", nil)
		gatewayRequest := make(http.Header)
		gatewayRequest.Set("Authorization", "Bearer "+key)
		through := measure(gateway.baseURL+"/v1/sse", gatewayRequest)
		return direct, through
	}
	direct := make([]streamMeasurement, baselineSamples)
	through := make([]streamMeasurement, baselineSamples)
	for i := 0; i < baselineSamples; i++ {
		direct[i], through[i] = measurePair()
	}
	mean := func(values []streamMeasurement, selectValue func(streamMeasurement) time.Duration) time.Duration {
		var total time.Duration
		for _, value := range values {
			total += selectValue(value)
		}
		return total / time.Duration(len(values))
	}
	for i := range direct {
		if direct[i].err != nil || through[i].err != nil {
			t.Fatalf("paired stream %d failed: direct=%v gateway=%v", i, direct[i].err, through[i].err)
		}
	}
	baselineTTFB := mean(direct, func(value streamMeasurement) time.Duration { return value.ttfb })
	gatewayTTFB := mean(through, func(value streamMeasurement) time.Duration { return value.ttfb })
	if gatewayTTFB-baselineTTFB >= 10*time.Millisecond {
		t.Fatalf("TTFB overhead=%s (direct mean=%s gateway mean=%s)", gatewayTTFB-baselineTTFB, baselineTTFB, gatewayTTFB)
	}
	t.Logf("paired means: TTFB direct=%s gateway=%s overhead=%s", baselineTTFB, gatewayTTFB, gatewayTTFB-baselineTTFB)
	baselineClose := mean(direct, func(value streamMeasurement) time.Duration { return value.close })
	gatewayClose := mean(through, func(value streamMeasurement) time.Duration { return value.close })
	if gatewayClose-baselineClose >= 10*time.Millisecond {
		t.Fatalf("post-first-byte stream-close overhead=%s (direct mean=%s gateway mean=%s)", gatewayClose-baselineClose, baselineClose, gatewayClose)
	}
	t.Logf("paired means: post-first-byte close direct=%s gateway=%s overhead=%s", baselineClose, gatewayClose, gatewayClose-baselineClose)

	const concurrent = 100
	type streamResult struct {
		total time.Duration
		close time.Duration
		err   error
	}
	results := make(chan streamResult, concurrent)
	var wg sync.WaitGroup
	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			started := time.Now()
			request, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, gateway.baseURL+"/v1/sse", nil)
			request.Header.Set("Authorization", "Bearer "+key)
			response, err := gateway.client.Do(request)
			if err != nil {
				results <- streamResult{total: time.Since(started), err: err}
				return
			}
			if response.StatusCode != http.StatusOK {
				closeErr := response.Body.Close()
				results <- streamResult{total: time.Since(started), err: fmt.Errorf("status=%d, close=%w", response.StatusCode, closeErr)}
				return
			}
			var firstByte [1]byte
			_, readErr := io.ReadFull(response.Body, firstByte[:])
			first := time.Now()
			if readErr == nil {
				_, readErr = io.Copy(io.Discard, response.Body)
			}
			closeErr := response.Body.Close()
			results <- streamResult{total: time.Since(started), close: time.Since(first), err: errors.Join(readErr, closeErr)}
		}()
	}
	wg.Wait()
	close(results)
	var total time.Duration
	var closeTotal time.Duration
	count := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("parallel stream request/read: %v", result.err)
		}
		total += result.total
		closeTotal += result.close
		count++
		if result.total >= 10*time.Second {
			t.Fatalf("parallel stream total took %s", result.total)
		}
	}
	parallelClose := closeTotal / time.Duration(count)
	if count != concurrent || parallelClose-baselineClose >= 50*time.Millisecond {
		t.Fatalf("parallel stream mean close overhead=%s (gateway close=%s direct close=%s) total=%s requests=%d want overhead <50ms and %d requests", parallelClose-baselineClose, parallelClose, baselineClose, total/time.Duration(count), count, concurrent)
	}
	t.Logf("parallel means: post-first-byte close=%s overhead=%s total=%s requests=%d", parallelClose, parallelClose-baselineClose, total/time.Duration(count), count)
}
