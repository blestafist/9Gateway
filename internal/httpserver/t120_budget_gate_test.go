package httpserver

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
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
	"github.com/pestit/9gateway/internal/config"
	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/storage"
	"github.com/pestit/9gateway/internal/transport"
	"gopkg.in/yaml.v3"
)

// TestT120BudgetMilestoneLifecycle is the budget gate's single real-HTTP
// scenario. It deliberately owns two fresh process graphs around one SQLite
// file: only committed token and budget buckets cross that boundary.
func TestT120BudgetMilestoneLifecycle(t *testing.T) {
	clock := &requestLimitTestClock{now: time.Date(2024, 12, 31, 23, 59, 30, 0, time.UTC)}
	pepper := []byte("t120-pepper-secret")
	adminCredential := "t120-admin-secret"
	upstreamCredential := "t120-upstream-secret"
	keyA := t100GenerateKey(t, pepper)
	keyB := t100GenerateKey(t, pepper)
	policyA := `{"allowed_models":["exact"],"request_windows":[{"amount":100,"duration":"1m"}],"token_windows":[{"amount":3,"duration":"1m"}],"token_mode":"usage_only","max_concurrent_requests":1,"budget_limits":[{"amount_micros":10,"period":"total"},{"amount_micros":10,"period":"day"},{"amount_micros":10,"period":"month"}]}`
	policyB := `{"allowed_models":["glob-*","zero","unknown"],"request_windows":[{"amount":100,"duration":"1m"}],"token_windows":[{"amount":100,"duration":"1m"}],"token_mode":"usage_only","max_concurrent_requests":1,"budget_limits":[{"amount_micros":50,"period":"total"},{"amount_micros":50,"period":"day"},{"amount_micros":50,"period":"month"}]}`
	databasePath := filepath.Join(t.TempDir(), "t120.db")
	createdAt := time.Unix(1, 0).UTC()
	database, err := storage.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	repository := storage.NewAPIKeyRepository(database)
	for _, value := range []struct {
		id, name, policy string
		key              auth.GeneratedGatewayKey
	}{{"key-a", "persistent-a", policyA, keyA}, {"key-b", "persistent-b", policyB, keyB}} {
		if err := repository.Insert(context.Background(), storage.APIKeyRecord{ID: value.id, Name: value.name, DisplayPrefix: value.key.DisplayPrefix, Digest: value.key.Digest, Enabled: true, CreatedAt: createdAt, UpdatedAt: createdAt, PolicyJSON: value.policy}); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	pricing := t120Pricing(t)
	upstream := newT120Upstream(t, upstreamCredential)
	var logs bytes.Buffer
	process := t120StartProcess(t, databasePath, clock, pricing, upstream.server.URL, upstreamCredential, adminCredential, string(pepper), &logs)
	client := transport.NewClient()
	request := func(method, target, key string, body string, headers map[string]string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, process.gateway.URL+target, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+key)
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	read := func(response *http.Response) []byte {
		t.Helper()
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	assertSafe := func(response *http.Response, body []byte) {
		t.Helper()
		text := string(body)
		for name, values := range response.Header {
			text += name + strings.Join(values, "|")
		}
		for _, secret := range []string{string(pepper), adminCredential, upstreamCredential, keyA.RawKey, keyB.RawKey, hex.EncodeToString(keyA.Digest), hex.EncodeToString(keyB.Digest), "sqlite", "database is locked", "prompt-fragment", "response-fragment", "input_per_million_micros", "reservation"} {
			if strings.Contains(strings.ToLower(text), strings.ToLower(secret)) {
				t.Fatalf("gateway response leaked %q: %q", secret, text)
			}
		}
	}
	assertStatus := func(response *http.Response, want int, contains string) {
		t.Helper()
		body := read(response)
		assertSafe(response, body)
		if response.StatusCode != want || (contains != "" && !bytes.Contains(body, []byte(contains))) {
			t.Fatalf("response = %d/%q, want %d containing %q", response.StatusCode, body, want, contains)
		}
	}

	// Exact pricing and an explicit output bound make the conservative two-micro
	// reservation larger than the actual one-micro completion.
	first := request(http.MethodPost, "/v1/chat/completions", keyA.RawKey, `{"model":"exact","max_tokens":2}`, map[string]string{"Content-Type": "application/json", "X-T120-Case": "a-json"})
	firstBody := read(first)
	assertSafe(first, firstBody)
	if first.StatusCode != http.StatusOK || !bytes.Contains(firstBody, []byte(`"total_tokens":1`)) {
		t.Fatalf("exact JSON lifecycle = %d/%q", first.StatusCode, firstBody)
	}
	t120WaitFor(t, func() bool { return process.worker.Stats().Succeeded >= 1 })

	// The committed token total survives the process-local reservation and
	// rejects a candidate that would oversubscribe the fixed token window.
	before := upstream.calls.Load()
	assertStatus(request(http.MethodPost, "/v1/chat/completions", keyA.RawKey, `{"model":"exact","max_tokens":2}`, map[string]string{"Content-Type": "application/json", "X-T120-Case": "a-token-rejected"}), http.StatusTooManyRequests, `"code":"token_limit_exceeded"`)
	if upstream.calls.Load() != before {
		t.Fatal("token rejection reached upstream")
	}

	// B covers the actual transport matrix while using an ordered glob price.
	holdReq := request(http.MethodPost, "/v1/chat/completions", keyB.RawKey, `{"model":"glob-hold","stream":true}`, map[string]string{"Content-Type": "application/json", "X-T120-Case": "a-hold"})
	upstream.waitFor("a-hold")
	before = upstream.calls.Load()
	assertStatus(request(http.MethodPost, "/v1/chat/completions", keyB.RawKey, `{"model":"glob-hold","stream":true}`, map[string]string{"Content-Type": "application/json", "X-T120-Case": "a-hold-rejected"}), http.StatusTooManyRequests, `"code":"concurrency_limit_exceeded"`)
	if upstream.calls.Load() != before {
		t.Fatal("concurrency rejection reached upstream")
	}
	upstream.release("a-hold")
	read(holdReq)

	for _, test := range []struct {
		name, body, content string
		status              int
		want                string
	}{
		{"b-json", `{"model":"glob-json"}`, "json", http.StatusOK, "json-body"},
		{"b-sse", `{"model":"glob-sse","stream":true}`, "sse", http.StatusOK, "data: sse"},
		{"b-convert", `{"model":"glob-convert","stream":false}`, "convert", http.StatusOK, `"total_tokens":2`},
		{"b-zero", `{"model":"zero"}`, "zero", http.StatusOK, "zero-body"},
		{"b-missing", `{"model":"glob-missing"}`, "missing", http.StatusOK, "missing-usage"},
		{"b-partial", `{"model":"glob-partial"}`, "partial", http.StatusOK, "partial-usage"},
		{"b-invalid", `{"model":"glob-invalid"}`, "invalid", http.StatusOK, "invalid-usage"},
		{"b-lower", `{"model":"glob-lower"}`, "lower", http.StatusOK, "lower-usage"},
		{"b-above", `{"model":"glob-above"}`, "above", http.StatusOK, "above-usage"},
		{"b-error", `{"model":"glob-error"}`, "error", http.StatusServiceUnavailable, "upstream-error"},
	} {
		response := request(http.MethodPost, "/v1/chat/completions", keyB.RawKey, test.body, map[string]string{"Content-Type": "application/json", "X-T120-Case": test.content})
		body := read(response)
		assertSafe(response, body)
		if response.StatusCode != test.status || !bytes.Contains(body, []byte(test.want)) {
			t.Fatalf("%s = %d/%q, want %d containing %q", test.name, response.StatusCode, body, test.status, test.want)
		}
	}
	// Unknown pricing, malformed input, and an oversized inspected body fail
	// closed before the upstream. The model policy rejection is also explicit.
	for _, test := range []struct {
		name, body, path, key string
	}{
		{"unknown price", `{"model":"not-priced"}`, "/v1/chat/completions", keyB.RawKey},
		{"malformed", `{"model":`, "/v1/chat/completions", keyA.RawKey},
		{"oversized", strings.Repeat("x", 5000), "/v1/chat/completions", keyA.RawKey},
		{"model denied", `{"model":"glob-denied"}`, "/v1/chat/completions", keyA.RawKey},
	} {
		before = upstream.calls.Load()
		response := request(http.MethodPost, test.path, test.key, test.body, map[string]string{"Content-Type": "application/json"})
		body := read(response)
		assertSafe(response, body)
		if response.StatusCode != http.StatusBadRequest && response.StatusCode != http.StatusForbidden {
			t.Fatalf("%s = %d/%q", test.name, response.StatusCode, body)
		}
		if upstream.calls.Load() != before {
			t.Fatalf("%s reached upstream", test.name)
		}
	}
	// A budget reservation held by B is rejected independently of A's state.
	// The large B policy lets every observation shape above finish, including
	// invalid/missing usage which remains conservatively charged.
	t120WaitFor(t, func() bool { return t120BudgetTotal(t, databasePath, "key-b") == 22 })
	if got := t120BudgetTotal(t, databasePath, "key-b"); got != 22 {
		t.Fatalf("B actual/conservative budget = %d, want 22", got)
	}
	if got := t120BudgetPeriod(t, databasePath, "key-b", limiter.BudgetPeriodDay, clock.Now()); got != 22 {
		t.Fatalf("B daily spend = %d, want 22", got)
	}
	if got := t120BudgetPeriod(t, databasePath, "key-b", limiter.BudgetPeriodMonth, clock.Now()); got != 22 {
		t.Fatalf("B monthly spend = %d, want 22", got)
	}

	t120StopProcess(t, process)
	if got := t120BudgetTotal(t, databasePath, "key-a"); got != 1 {
		t.Fatalf("persisted A spend before restart = %d, want 1", got)
	}
	if got := t120BudgetTotal(t, databasePath, "key-b"); got != 22 {
		t.Fatalf("persisted B spend before restart = %d, want 22", got)
	}

	// Restart restores committed totals but no active leases. B is then put into
	// deliberate over-budget debt by an atomic administrative policy replacement.
	process = t120StartProcess(t, databasePath, clock, pricing, upstream.server.URL, upstreamCredential, adminCredential, string(pepper), &logs)
	before = upstream.calls.Load()
	assertStatus(request(http.MethodPost, "/v1/chat/completions", keyB.RawKey, `{"model":"glob-restarted"}`, map[string]string{"Content-Type": "application/json"}), http.StatusOK, "json-body")
	if upstream.calls.Load() != before+1 {
		t.Fatal("restored B did not admit after restart")
	}
	update := newPolicyRequest(t, process.gateway.URL, "key-b", `{"enabled":true,"policy":{"allowed_models":["glob-*","zero","unknown"],"request_windows":[{"amount":100,"duration":"1m"}],"token_windows":[{"amount":100,"duration":"1m"}],"token_mode":"usage_only","max_concurrent_requests":1,"budget_limits":[{"amount_micros":3,"period":"total"},{"amount_micros":3,"period":"day"},{"amount_micros":3,"period":"month"}]}}`, adminCredential)
	update.Method = http.MethodPut
	updated, err := client.Do(update)
	if err != nil {
		t.Fatal(err)
	}
	updatedBody := read(updated)
	assertSafe(updated, updatedBody)
	if updated.StatusCode != http.StatusOK {
		t.Fatalf("budget policy replacement = %d/%q", updated.StatusCode, updatedBody)
	}
	// The replacement retains the 19-micro spend, so its debt rejects without a
	// provider call and has no fabricated Retry-After for a total limit.
	before = upstream.calls.Load()
	assertStatus(request(http.MethodPost, "/v1/chat/completions", keyB.RawKey, `{"model":"glob-debt"}`, map[string]string{"Content-Type": "application/json"}), http.StatusTooManyRequests, `"code":"budget_exceeded"`)
	if upstream.calls.Load() != before {
		t.Fatal("over-budget debt reached upstream")
	}

	// Same-process restart restoration of token usage is checked before changing
	// the fake clock; then UTC day and calendar-month reset are checked together.
	before = upstream.calls.Load()
	assertStatus(request(http.MethodPost, "/v1/chat/completions", keyA.RawKey, `{"model":"exact","max_tokens":2}`, map[string]string{"Content-Type": "application/json"}), http.StatusTooManyRequests, `"code":"token_limit_exceeded"`)
	if upstream.calls.Load() != before {
		t.Fatal("restored token rejection reached upstream")
	}
	clock.Set(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	assertStatus(request(http.MethodPost, "/v1/chat/completions", keyA.RawKey, `{"model":"exact","max_tokens":2}`, map[string]string{"Content-Type": "application/json", "X-T120-Case": "a-after-reset"}), http.StatusOK, `"total_tokens":1`)

	// Replacing the model selector persists and publishes atomically. The old
	// model is forbidden before upstream; the new exact selector is admitted
	// against the retained total spend and reset day/month buckets.
	update = newPolicyRequest(t, process.gateway.URL, "key-a", `{"enabled":true,"policy":{"allowed_models":["exact-new"],"request_windows":[{"amount":100,"duration":"1m"}],"token_windows":[{"amount":3,"duration":"1m"}],"token_mode":"usage_only","max_concurrent_requests":1,"budget_limits":[{"amount_micros":10,"period":"total"},{"amount_micros":10,"period":"day"},{"amount_micros":10,"period":"month"}]}}`, adminCredential)
	update.Method = http.MethodPut
	updated, err = client.Do(update)
	if err != nil {
		t.Fatal(err)
	}
	updatedBody = read(updated)
	assertSafe(updated, updatedBody)
	if updated.StatusCode != http.StatusOK {
		t.Fatalf("model policy replacement = %d/%q", updated.StatusCode, updatedBody)
	}
	before = upstream.calls.Load()
	assertStatus(request(http.MethodPost, "/v1/chat/completions", keyA.RawKey, `{"model":"exact"}`, map[string]string{"Content-Type": "application/json"}), http.StatusForbidden, `"code":"model_not_allowed"`)
	if upstream.calls.Load() != before {
		t.Fatal("old model after replacement reached upstream")
	}
	assertStatus(request(http.MethodPost, "/v1/chat/completions", keyA.RawKey, `{"model":"exact-new"}`, map[string]string{"Content-Type": "application/json", "X-T120-Case": "a-after-reset"}), http.StatusOK, `"total_tokens":1`)

	t120StopProcess(t, process)
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if line == "" {
			continue
		}
		if !json.Valid([]byte(line)) {
			t.Fatalf("completion log is not structured JSON: %q", line)
		}
		for _, secret := range []string{string(pepper), adminCredential, upstreamCredential, keyA.RawKey, keyB.RawKey, hex.EncodeToString(keyA.Digest), hex.EncodeToString(keyB.Digest), "prompt-fragment", "response-fragment", "input_per_million_micros", "reservation", "sqlite", "database is locked"} {
			if strings.Contains(strings.ToLower(line), strings.ToLower(secret)) {
				t.Fatalf("completion log leaked %q: %q", secret, line)
			}
		}
	}
}

// TestT120BlockedObservationAndSQLiteWritersAreOffTheTransportPath proves the
// hard timing boundary with real HTTP. Both persistence writers block while an
// observation parser is blocked; transport still flushes, closes, and releases
// concurrency so a second request can reuse the slot.
func TestT120BlockedObservationAndSQLiteWritersAreOffTheTransportPath(t *testing.T) {
	clock := &requestLimitTestClock{now: time.Unix(30, 0).UTC()}
	key, authenticator := t110Key(t, `{"allowed_models":["blocked"],"token_windows":[{"amount":100,"duration":"1m"}],"token_mode":"usage_only","max_concurrent_requests":1,"budget_limits":[{"amount_micros":100,"period":"total"}]}`)
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := storage.NewAPIKeyRepository(database).Insert(context.Background(), storage.APIKeyRecord{ID: "t110-key-a", Name: "blocked", DisplayPrefix: key.DisplayPrefix, Digest: key.Digest, Enabled: true, CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(1, 0).UTC(), PolicyJSON: `{"allowed_models":["blocked"],"token_windows":[{"amount":100,"duration":"1m"}],"token_mode":"usage_only","max_concurrent_requests":1,"budget_limits":[{"amount_micros":100,"period":"total"}]}`}); err != nil {
		t.Fatal(err)
	}
	tokenDB := &t120BlockingDB{DB: database, entered: make(chan struct{}), release: make(chan struct{})}
	budgetDB := &t120BlockingDB{DB: database, entered: make(chan struct{}), release: make(chan struct{})}
	tokenStore := storage.NewUsageBucketRepository(tokenDB)
	budgetStore := storage.NewBudgetBucketRepository(budgetDB)
	tokenAccumulator := storage.NewUsageAggregateAccumulator(tokenStore)
	budgetAccumulator := storage.NewBudgetAccumulator(budgetStore)
	t.Cleanup(func() {
		closeOnce(t, tokenDB.release)
		closeOnce(t, budgetDB.release)
		shutdownErr := tokenAccumulator.Shutdown(context.Background())
		if shutdownErr != nil {
			t.Errorf("token accumulator shutdown: %v", shutdownErr)
		}
		if shutdownErr = budgetAccumulator.Shutdown(context.Background()); shutdownErr != nil {
			t.Errorf("budget accumulator shutdown: %v", shutdownErr)
		}
	})
	tokens := limiter.NewTokenLimiter(clock.Now)
	budget := limiter.NewBudgetLimiter(clock.Now)
	tokens.SetCommittedDeltaSink(tokenAccumulator.Sink)
	budget.SetCommittedDeltaSink(budgetAccumulator.Sink)
	parserEntered := make(chan struct{})
	parserRelease := make(chan struct{})
	var parserOnce sync.Once
	worker := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: 2, ParseUsage: func([]byte, ContentCoding) (accounting.Usage, error) {
		parserOnce.Do(func() { close(parserEntered) })
		<-parserRelease
		return accounting.Usage{}, nil
	}})
	t.Cleanup(func() { closeOnce(t, parserRelease); shutdownObservationWorker(t, worker) })
	var calls atomic.Int32
	firstFragment := make(chan struct{})
	var firstOnce sync.Once
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		response.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(response, "data: first\n\n")
		response.(http.Flusher).Flush()
		if request.Header.Get("X-T120-Case") == "first" {
			firstOnce.Do(func() { close(firstFragment) })
			select {
			case <-request.Context().Done():
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
		_, _ = io.WriteString(response, "data: {\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n")
		response.(http.Flusher).Flush()
	}))
	t.Cleanup(upstream.Close)
	concurrency := limiter.NewConcurrencyLimiter()
	handler := NewHandlerWithAuthenticatorAndLimitersAndTokenLimiterAndUsageObservationWorker(transport.NewClient(), upstream.URL, "upstream", authenticator, limiter.NewRequestLimiter(clock.Now), concurrency, nil, tokens, TokenAdmissionConfig{FallbackUnknownInputTokens: 1, FallbackMaxOutputTokens: 1, PricingResolver: accounting.NewPricingResolver(t120Pricing(t)), BudgetLimiter: budget}, worker)
	gateway := httptest.NewServer(handler)
	t.Cleanup(gateway.Close)
	request := func(caseName string) *http.Response {
		req, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{"model":"blocked","stream":true}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+key.RawKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-T120-Case", caseName)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	first := request("first")
	select {
	case <-firstFragment:
	case <-time.After(time.Second):
		t.Fatal("first SSE fragment did not flush")
	}
	buffer := make([]byte, len("data: first\n\n"))
	if _, err := io.ReadFull(first.Body, buffer); err != nil || string(buffer) != "data: first\n\n" {
		t.Fatalf("first flush = %q/%v", buffer, err)
	}
	bodyDone := make(chan struct{})
	go func() { _, _ = io.ReadAll(first.Body); close(bodyDone) }()
	select {
	case <-bodyDone:
	case <-time.After(time.Second):
		t.Fatal("transparent stream close delayed by blocked observation/persistence")
	}
	first.Body.Close()
	select {
	case <-parserEntered:
	case <-time.After(time.Second):
		t.Fatal("observation parser did not block")
	}
	closeOnce(t, parserRelease)
	select {
	case <-tokenDB.entered:
	case <-time.After(time.Second):
		t.Fatal("token SQLite writer did not block")
	}
	closeOnce(t, tokenDB.release)
	select {
	case <-budgetDB.entered:
	case <-time.After(time.Second):
		t.Fatal("budget SQLite writer did not block")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && concurrency.Len() != 0 {
		time.Sleep(time.Millisecond)
	}
	if got := concurrency.Len(); got != 0 {
		t.Fatalf("concurrency retained while worker blocked: %d", got)
	}
	second := request("second")
	secondBody, err := io.ReadAll(second.Body)
	second.Body.Close()
	if err != nil || second.StatusCode != http.StatusOK || !bytes.Contains(secondBody, []byte("data: first")) || calls.Load() != 2 {
		t.Fatalf("concurrency reuse = %d/%q/%v", second.StatusCode, secondBody, err)
	}
	closeOnce(t, tokenDB.release)
	closeOnce(t, budgetDB.release)
}

func t120Pricing(t *testing.T) config.PricingConfig {
	t.Helper()
	var pricing config.PricingConfig
	const source = `rules:
  - model: exact
    input_per_million_micros: 500000
    output_per_million_micros: 500000
  - model: exact-new
    input_per_million_micros: 500000
    output_per_million_micros: 500000
  - model: blocked
    input_per_million_micros: 1000000
    output_per_million_micros: 1000000
  - model: 'glob-*'
    input_per_million_micros: 1000000
    output_per_million_micros: 1000000
  - model: zero
    input_per_million_micros: 0
    output_per_million_micros: 0
`
	if err := yaml.Unmarshal([]byte(source), &pricing); err != nil {
		t.Fatal(err)
	}
	return pricing
}

type t120Process struct {
	database          *storage.DB
	gateway           *httptest.Server
	worker            *UsageObservationWorker
	logger            *CompletionLogger
	tokenAccumulator  *storage.UsageAggregateAccumulator
	budgetAccumulator *storage.BudgetAccumulator
}

type t120Upstream struct {
	server     *httptest.Server
	credential string
	calls      atomic.Int32
	mu         sync.Mutex
	started    map[string]chan struct{}
	releaseCh  map[string]chan struct{}
}

func newT120Upstream(t *testing.T, credential string) *t120Upstream {
	t.Helper()
	upstream := &t120Upstream{credential: credential, started: map[string]chan struct{}{"a-hold": make(chan struct{}, 1)}, releaseCh: map[string]chan struct{}{"a-hold": make(chan struct{})}}
	upstream.server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+upstream.credential {
			t.Errorf("upstream authorization = %q", request.Header.Get("Authorization"))
		}
		upstream.calls.Add(1)
		_, _ = io.Copy(io.Discard, request.Body)
		switch request.Header.Get("X-T120-Case") {
		case "a-hold":
			response.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(response, "data: hold\n\n")
			response.(http.Flusher).Flush()
			upstream.started["a-hold"] <- struct{}{}
			select {
			case <-upstream.releaseCh["a-hold"]:
			case <-request.Context().Done():
			}
		case "a-json", "a-after-reset":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"usage":{"prompt_tokens":1,"completion_tokens":0,"total_tokens":1},"body":"json-body"}`)
		case "json":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2},"body":"json-body"}`)
		case "sse":
			response.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(response, "data: sse\n\ndata: {\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n")
		case "convert":
			response.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(response, "data: {\"choices\":[{\"delta\":{\"content\":\"converted\"}}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\ndata: [DONE]\n\n")
		case "zero":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"body":"zero-body","usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
		case "missing":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"id":"missing-usage"}`)
		case "partial":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"id":"partial-usage","usage":{"prompt_tokens":1}}`)
		case "invalid":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"id":"invalid-usage","usage":{"total_tokens":-1}}`)
		case "lower":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"id":"lower-usage","usage":{"prompt_tokens":1,"completion_tokens":0,"total_tokens":1}}`)
		case "above":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"id":"above-usage","usage":{"prompt_tokens":4,"completion_tokens":1,"total_tokens":5}}`)
		case "error":
			response.Header().Set("Content-Type", "application/json")
			response.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(response, `{"error":"upstream-error"}`)
		default:
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"body":"json-body","usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
		}
	}))
	t.Cleanup(upstream.server.Close)
	return upstream
}

func (upstream *t120Upstream) waitFor(name string) {
	<-upstream.started[name]
}

func (upstream *t120Upstream) release(name string) {
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	select {
	case <-upstream.releaseCh[name]:
	default:
		close(upstream.releaseCh[name])
	}
}

func t120WaitFor(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition did not become true")
}

func t120StartProcess(t *testing.T, path string, clock *requestLimitTestClock, pricing config.PricingConfig, upstreamURL, upstreamCredential, adminCredential, pepper string, logs io.Writer) *t120Process {
	t.Helper()
	database, err := storage.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	repository := storage.NewAPIKeyRepository(database)
	records, err := repository.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	policies := make(map[string]auth.EffectivePolicy, len(records))
	for _, record := range records {
		policy, parseErr := auth.ParsePolicyJSONWithTokenMode([]byte(record.PolicyJSON), auth.TokenModeUsageOnly)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		policies[record.ID] = policy
	}
	tokens := limiter.NewTokenLimiter(clock.Now)
	usageRepository := storage.NewUsageBucketRepository(database)
	persistedTokens, err := usageRepository.LoadUnexpired(context.Background(), clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	committed := make([]limiter.CommittedTokenBucket, 0, len(persistedTokens))
	for _, bucket := range persistedTokens {
		policy := policies[bucket.APIKeyID]
		var match limiter.TokenWindow
		for _, candidate := range policy.TokenWindows() {
			if int64(candidate.Duration/time.Second) == bucket.BucketSeconds && candidate.Amount == bucket.BucketAmount {
				match = limiter.TokenWindow{Amount: candidate.Amount, Duration: candidate.Duration}
				break
			}
		}
		if match.Duration == 0 {
			t.Fatalf("persisted token bucket does not match policy for %q", bucket.APIKeyID)
		}
		committed = append(committed, limiter.CommittedTokenBucket{KeyID: bucket.APIKeyID, BucketStart: bucket.BucketStart, Window: match, CommittedTokens: bucket.CommittedTokens})
	}
	if err := tokens.LoadCommitted(clock.Now(), committed); err != nil {
		t.Fatal(err)
	}
	budgetRepository := storage.NewBudgetBucketRepository(database)
	persistedTotal, err := budgetRepository.LoadTotal(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	persistedDay, err := budgetRepository.LoadDay(context.Background(), clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	persistedMonth, err := budgetRepository.LoadMonth(context.Background(), clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	spent := make([]limiter.BudgetSpent, 0, len(persistedTotal)+len(persistedDay)+len(persistedMonth))
	for _, bucket := range append(append(persistedTotal, persistedDay...), persistedMonth...) {
		value, valueErr := accounting.NewMoneyMicros(bucket.SpentMicros)
		if valueErr != nil {
			t.Fatal(valueErr)
		}
		spent = append(spent, limiter.BudgetSpent{KeyID: bucket.APIKeyID, Spent: value, Period: bucket.Period, PeriodStart: bucket.PeriodStart})
	}
	budget := limiter.NewBudgetLimiter(clock.Now)
	if err := budget.LoadSpent(spent); err != nil {
		t.Fatal(err)
	}
	worker := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: 32})
	logger := NewCompletionLogger(slog.New(slog.NewJSONHandler(logs, nil)), 32)
	tokenAccumulator := storage.NewUsageAggregateAccumulator(usageRepository)
	tokens.SetCommittedDeltaSink(tokenAccumulator.Sink)
	budgetAccumulator := storage.NewBudgetAccumulator(budgetRepository)
	budget.SetCommittedDeltaSink(budgetAccumulator.Sink)
	handler, err := NewHandlerWithAdminAndLimitersAndTokenConfigAndTokenLimiterAndUsageObservationWorker(transport.NewClient(), upstreamURL, upstreamCredential, adminCredential, pepper, repository, limiter.NewRequestLimiter(clock.Now), limiter.NewConcurrencyLimiter(), logger, tokens, TokenAdmissionConfig{FallbackUnknownInputTokens: 1, FallbackMaxOutputTokens: 1, PricingResolver: accounting.NewPricingResolver(pricing), BudgetLimiter: budget}, worker, auth.TokenModeUsageOnly)
	if err != nil {
		t.Fatal(err)
	}
	return &t120Process{database: database, gateway: httptest.NewServer(handler), worker: worker, logger: logger, tokenAccumulator: tokenAccumulator, budgetAccumulator: budgetAccumulator}
}

func t120StopProcess(t *testing.T, process *t120Process) {
	t.Helper()
	process.gateway.Close()
	shutdownObservationWorker(t, process.worker)
	if err := process.logger.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := process.tokenAccumulator.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := process.budgetAccumulator.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := process.database.Close(); err != nil {
		t.Fatal(err)
	}
}

func t120BudgetTotal(t *testing.T, path, key string) int64 {
	t.Helper()
	database, err := storage.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	buckets, err := storage.NewBudgetBucketRepository(database).LoadTotal(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, bucket := range buckets {
		if bucket.APIKeyID == key {
			return bucket.SpentMicros
		}
	}
	return 0
}

func t120BudgetPeriod(t *testing.T, path, key string, period limiter.BudgetPeriod, now time.Time) int64 {
	t.Helper()
	database, err := storage.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	repository := storage.NewBudgetBucketRepository(database)
	var buckets []storage.BudgetBucket
	switch period {
	case limiter.BudgetPeriodDay:
		buckets, err = repository.LoadDay(context.Background(), now)
	case limiter.BudgetPeriodMonth:
		buckets, err = repository.LoadMonth(context.Background(), now)
	default:
		t.Fatalf("unsupported budget period %q", period)
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, bucket := range buckets {
		if bucket.APIKeyID == key {
			return bucket.SpentMicros
		}
	}
	return 0
}

// t120BlockingDB blocks only transaction acquisition. Reads remain available,
// which makes the blocked writer boundary observable without changing the
// repository implementation or leaking SQL into the HTTP layer.
type t120BlockingDB struct {
	*storage.DB
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (database *t120BlockingDB) BeginTx(ctx context.Context, options *sql.TxOptions) (*sql.Tx, error) {
	database.once.Do(func() { close(database.entered) })
	select {
	case <-database.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return database.DB.BeginTx(ctx, options)
}

func closeOnce(t *testing.T, channel chan struct{}) {
	t.Helper()
	select {
	case <-channel:
	default:
		close(channel)
	}
}
