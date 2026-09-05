package httpserver

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/storage"
	"github.com/pestit/9gateway/internal/transport"
)

// TestT080PersistentPolicyLifecycle exercises the policy boundary and the
// transport boundary together. The database is closed and reopened midway so
// that authentication after a restart proves that the raw keys were never
// needed for persistence.
func TestT080PersistentPolicyLifecycle(t *testing.T) {
	pepper := []byte("t080-pepper-secret")
	adminCredential := "t080-admin-secret"
	upstreamCredential := "t080-upstream-secret"
	now := time.Unix(120, 0).UTC()
	clock := &requestLimitTestClock{now: now}

	keyA := t080GenerateKey(t, pepper)
	keyB := t080GenerateKey(t, pepper)
	disabled := t080GenerateKey(t, pepper)
	expired := t080GenerateKey(t, pepper)
	createdAt := time.Unix(1, 0).UTC()
	expiredAt := time.Unix(2, 0).UTC()
	policyA := `{"allowed_models":["model-a"],"request_windows":[{"amount":6,"duration":"1m"}],"max_concurrent_requests":1}`
	policyB := `{"allowed_models":["model-b"],"request_windows":[{"amount":4,"duration":"1m"}],"max_concurrent_requests":2}`

	databasePath := t.TempDir() + "/t080.db"
	database, err := storage.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	repository := storage.NewAPIKeyRepository(database)
	for _, key := range []struct {
		id      string
		name    string
		value   auth.GeneratedGatewayKey
		enabled bool
		expires *time.Time
		policy  string
	}{
		{id: "key-a", name: "persistent-a", value: keyA, enabled: true, policy: policyA},
		{id: "key-b", name: "persistent-b", value: keyB, enabled: true, policy: policyB},
		{id: "disabled", name: "disabled", value: disabled, enabled: false},
		{id: "expired", name: "expired", value: expired, enabled: true, expires: &expiredAt},
	} {
		record := storage.APIKeyRecord{
			ID: key.id, Name: key.name, DisplayPrefix: key.value.DisplayPrefix,
			Digest: key.value.Digest, Enabled: key.enabled, ExpiresAt: key.expires,
			CreatedAt: createdAt, UpdatedAt: createdAt, PolicyJSON: key.policy,
		}
		if err := repository.Insert(context.Background(), record); err != nil {
			t.Fatalf("insert %s: %v", key.id, err)
		}
	}

	var logs t080LogBuffer
	logHandler := slog.NewJSONHandler(&logs, nil)
	upstream := newT080Upstream(t, upstreamCredential)
	client := transport.NewClient()
	// The production handler intentionally does not expose its logger. Keep the
	// two logger lifetimes explicit instead of relying on test cleanup ordering.
	requestLimiter := limiter.NewRequestLimiter(clock.Now)
	concurrencyLimiter := limiter.NewConcurrencyLimiter()
	completionLogger := NewCompletionLogger(slog.New(logHandler), 64)
	handler, err := NewHandlerWithAdminAndLimiters(client, upstream.URL, upstreamCredential, adminCredential, string(pepper), repository, requestLimiter, concurrencyLimiter, completionLogger)
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(handler)

	assertNoSecrets := func(result t080HTTPResult) {
		t.Helper()
		combined := string(result.Body)
		for name, values := range result.Headers {
			combined += name + strings.Join(values, "|")
		}
		for _, secret := range t080Secrets(pepper, adminCredential, upstreamCredential, keyA, keyB, disabled, expired) {
			if strings.Contains(combined, secret) {
				t.Fatalf("response contains %s", secret)
			}
		}
	}
	do := func(method, target, rawKey string, body []byte, headers map[string]string) t080HTTPResult {
		t.Helper()
		request, requestErr := http.NewRequest(method, gateway.URL+target, bytes.NewReader(body))
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		if rawKey != "" {
			request.Header.Set("Authorization", "Bearer "+rawKey)
		}
		for name, value := range headers {
			request.Header.Set(name, value)
		}
		response, requestErr := client.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		result := t080ReadResponse(t, response)
		assertNoSecrets(result)
		return result
	}
	noUpstream := func(before int) {
		t.Helper()
		if got := upstream.callCount(); got != before {
			t.Fatalf("rejected request reached upstream: calls %d, want %d", got, before)
		}
	}

	// Admin credentials are a separate boundary and wrong credentials are
	// rejected before the body is interpreted or the upstream is contacted.
	before := upstream.callCount()
	adminRejected := do(http.MethodPost, "/admin/v1/keys", "", []byte(`{"name":"not-created"}`), map[string]string{
		"Authorization": "Bearer wrong-admin",
		"Content-Type":  "application/json",
	})
	if adminRejected.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong admin status = %d, want 401", adminRejected.StatusCode)
	}
	noUpstream(before)

	for name, credential := range map[string]string{
		"missing":   "",
		"malformed": "not-a-gateway-key",
		"unknown":   strings.Repeat("x", len(keyA.RawKey)),
		"disabled":  disabled.RawKey,
		"expired":   expired.RawKey,
	} {
		before = upstream.callCount()
		result := do(http.MethodPost, "/v1/chat/completions", credential, []byte(`{"model":"model-a"}`), map[string]string{"Content-Type": "application/json"})
		wantCode := gatewayErrorInvalidAPIKey
		if name == "disabled" {
			wantCode = gatewayErrorKeyDisabled
		} else if name == "expired" {
			wantCode = gatewayErrorKeyExpired
		}
		if result.StatusCode != http.StatusUnauthorized || !bytes.Contains(result.Body, []byte(`"code":"`+wantCode+`"`)) {
			t.Fatalf("%s auth result = %d/%q", name, result.StatusCode, result.Body)
		}
		noUpstream(before)
	}

	// A known policy request is denied before the request window and upstream.
	before = upstream.callCount()
	denied := do(http.MethodPost, "/v1/chat/completions", keyA.RawKey, []byte(`{"model":"model-b","stream":true}`), map[string]string{"Content-Type": "application/json"})
	if denied.StatusCode != http.StatusForbidden || !bytes.Contains(denied.Body, []byte(`"code":"model_not_allowed"`)) {
		t.Fatalf("model denial = %d/%q", denied.StatusCode, denied.Body)
	}
	noUpstream(before)

	// An admitted generic request must retain method, escaped path/query,
	// required headers, body bytes, upstream status, response headers and bytes.
	preservedBody := []byte("  opaque bytes, not JSON  \x00")
	preserved := do(http.MethodPatch, "/v1/custom/path%2Fpart?x=one&x=two", keyA.RawKey, preservedBody, map[string]string{
		"Content-Type":      "application/octet-stream",
		"X-Test-Key":        "A",
		"X-Required-Header": "required-value",
	})
	if preserved.StatusCode != http.StatusCreated || !bytes.Equal(preserved.Body, []byte(`{"accepted":true}`)) || preserved.Headers.Get("X-Upstream-Trace") != "t080-trace" {
		t.Fatalf("transparent admission = %d/%q/%v", preserved.StatusCode, preserved.Body, preserved.Headers)
	}
	call := upstream.lastCall()
	if call.Method != http.MethodPatch || call.Path != "/v1/custom/path/part" || call.RawQuery != "x=one&x=two" || call.RequiredHeader != "required-value" || !bytes.Equal(call.Body, preservedBody) || call.Authorization != "Bearer "+upstreamCredential {
		t.Fatalf("forwarded request = %#v", call)
	}
	if strings.Contains(call.HeaderDump, keyA.RawKey) || strings.Contains(call.HeaderDump, adminCredential) || strings.Contains(call.HeaderDump, string(pepper)) {
		t.Fatal("client or gateway secret reached upstream headers")
	}

	// Hold A's one admitted lease through response-body completion. A second A
	// request is rejected while the first is still held.
	firstHold := t080StartRequest(t, client, gateway.URL+"/v1/hold?kind=hold", keyA.RawKey, "A")
	upstream.waitForHold(t, "A", 1)
	secondHold := do(http.MethodGet, "/v1/hold?kind=hold", keyA.RawKey, nil, map[string]string{"X-Test-Key": "A"})
	if secondHold.StatusCode != http.StatusTooManyRequests || !bytes.Contains(secondHold.Body, []byte(`"code":"concurrency_limit_exceeded"`)) {
		t.Fatalf("A concurrency saturation = %d/%q", secondHold.StatusCode, secondHold.Body)
	}
	upstream.releaseHold("A")
	firstHoldResult := t080FinishRequest(t, firstHold)
	if firstHoldResult.StatusCode != http.StatusCreated || firstHoldResult.Headers.Get("Content-Type") != "text/event-stream" || !bytes.Equal(firstHoldResult.Body, []byte("data: start\n\ndata: done\n\n")) || concurrencyLimiter.Len() != 0 {
		t.Fatalf("A hold completion = %d, active states %d", firstHoldResult.StatusCode, concurrencyLimiter.Len())
	}

	upstreamError := do(http.MethodGet, "/v1/error?kind=error", keyA.RawKey, nil, map[string]string{"X-Test-Key": "A"})
	if upstreamError.StatusCode != http.StatusServiceUnavailable || !bytes.Equal(upstreamError.Body, []byte(`{"error":"upstream failure"}`)) {
		t.Fatalf("upstream error = %d/%q", upstreamError.StatusCode, upstreamError.Body)
	}

	// Cancellation after a flushed fragment must release A's lease and cancel
	// the upstream request. The request's context is deliberately canceled by
	// the client, not by the test server.
	cancelContext, cancel := context.WithCancel(context.Background())
	cancelRequest, err := http.NewRequestWithContext(cancelContext, http.MethodGet, gateway.URL+"/v1/cancel?kind=cancel", nil)
	if err != nil {
		t.Fatal(err)
	}
	cancelRequest.Header.Set("Authorization", "Bearer "+keyA.RawKey)
	cancelRequest.Header.Set("X-Test-Key", "A")
	cancelResponse, err := client.Do(cancelRequest)
	if err != nil {
		t.Fatal(err)
	}
	fragment := []byte("data: held\n\n")
	if got, readErr := io.ReadAll(io.LimitReader(cancelResponse.Body, int64(len(fragment)))); readErr != nil || !bytes.Equal(got, fragment) {
		t.Fatalf("cancellation fragment = %q, error %v", got, readErr)
	}
	cancel()
	_ = cancelResponse.Body.Close()
	upstream.waitForCancellation(t)
	t080WaitFor(t, func() bool { return concurrencyLimiter.Len() == 0 })

	finalA := do(http.MethodGet, "/v1/final", keyA.RawKey, nil, map[string]string{"X-Test-Key": "A"})
	if finalA.StatusCode != http.StatusCreated {
		t.Fatalf("final A status = %d", finalA.StatusCode)
	}
	// Five admitted A requests before this point include the preserved request,
	// hold, error, cancellation, and final request. The next request is rejected
	// without an upstream call,
	// proving that the fixed window is enforced after failures too.
	before = upstream.callCount()
	rateRejected := do(http.MethodGet, "/v1/rate-limited", keyA.RawKey, nil, map[string]string{"X-Test-Key": "A"})
	if rateRejected.StatusCode != http.StatusTooManyRequests || !bytes.Contains(rateRejected.Body, []byte(`"code":"request_limit_exceeded"`)) {
		t.Fatalf("A rate rejection = %d/%q", rateRejected.StatusCode, rateRejected.Body)
	}
	noUpstream(before)

	// B has a distinct window and allows two concurrent leases. A third B
	// request is rejected while both upstream response bodies remain open.
	firstB := t080StartRequest(t, client, gateway.URL+"/v1/hold?kind=hold", keyB.RawKey, "B")
	secondB := t080StartRequest(t, client, gateway.URL+"/v1/hold?kind=hold", keyB.RawKey, "B")
	upstream.waitForHold(t, "B", 2)
	before = upstream.callCount()
	thirdB := do(http.MethodGet, "/v1/hold?kind=hold", keyB.RawKey, nil, map[string]string{"X-Test-Key": "B"})
	if thirdB.StatusCode != http.StatusTooManyRequests || !bytes.Contains(thirdB.Body, []byte(`"code":"concurrency_limit_exceeded"`)) {
		t.Fatalf("B concurrency saturation = %d/%q", thirdB.StatusCode, thirdB.Body)
	}
	if got := upstream.callCount(); got != before {
		t.Fatalf("B saturated request reached upstream: calls %d/%d", got, before)
	}
	upstream.releaseHold("B")
	if t080FinishRequest(t, firstB).StatusCode != http.StatusCreated || t080FinishRequest(t, secondB).StatusCode != http.StatusCreated {
		t.Fatal("B admitted hold did not complete")
	}
	t080WaitFor(t, func() bool { return concurrencyLimiter.Len() == 0 })

	sse := do(http.MethodPost, "/v1/chat/completions?kind=sse", keyB.RawKey, []byte(`{"model":"model-b","stream":true}`), map[string]string{
		"Content-Type": "application/json",
		"X-Test-Key":   "B",
	})
	if sse.StatusCode != http.StatusAccepted || sse.Headers.Get("Content-Type") != "text/event-stream" || !bytes.Equal(sse.Body, []byte("data: eof\n\n")) {
		t.Fatalf("transparent SSE EOF = %d/%q/%q", sse.StatusCode, sse.Headers.Get("Content-Type"), sse.Body)
	}
	before = upstream.callCount()
	bRateRejected := do(http.MethodGet, "/v1/b-rate-limited", keyB.RawKey, nil, map[string]string{"X-Test-Key": "B"})
	if bRateRejected.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("B rate rejection = %d/%q", bRateRejected.StatusCode, bRateRejected.Body)
	}
	noUpstream(before)

	if got := upstream.maxActiveFor("A"); got > 1 {
		t.Fatalf("A oversubscribed: max active %d", got)
	}
	if got := upstream.maxActiveFor("B"); got > 2 {
		t.Fatalf("B oversubscribed: max active %d", got)
	}
	if got := concurrencyLimiter.Len(); got != 0 {
		t.Fatalf("concurrency states before restart = %d", got)
	}

	// Restart from the same SQLite file with fresh process-local limiters.
	gateway.Close()
	if err := completionLogger.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = storage.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	requestLimiter = limiter.NewRequestLimiter(clock.Now)
	concurrencyLimiter = limiter.NewConcurrencyLimiter()
	restartedLogger := NewCompletionLogger(slog.New(logHandler), 64)
	restartedHandler, err := NewHandlerWithAdminAndLimiters(client, upstream.URL, upstreamCredential, adminCredential, string(pepper), storage.NewAPIKeyRepository(database), requestLimiter, concurrencyLimiter, restartedLogger)
	if err != nil {
		t.Fatal(err)
	}
	restartedGateway := httptest.NewServer(restartedHandler)
	restarted := func(rawKey string) t080HTTPResult {
		t.Helper()
		request, requestErr := http.NewRequest(http.MethodGet, restartedGateway.URL+"/v1/restarted?after=process-restart", nil)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		request.Header.Set("Authorization", "Bearer "+rawKey)
		request.Header.Set("X-Test-Key", "restart")
		response, requestErr := client.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		result := t080ReadResponse(t, response)
		assertNoSecrets(result)
		return result
	}
	if result := restarted(keyA.RawKey); result.StatusCode != http.StatusCreated {
		t.Fatalf("persistent A after restart = %d", result.StatusCode)
	}
	if result := restarted(keyB.RawKey); result.StatusCode != http.StatusCreated {
		t.Fatalf("persistent B after restart = %d", result.StatusCode)
	}
	restartedGateway.Close()
	if err := restartedLogger.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if line != "" && !json.Valid([]byte(line)) {
			t.Fatalf("completion log is not structured JSON: %q", line)
		}
	}
	for _, secret := range t080Secrets(pepper, adminCredential, upstreamCredential, keyA, keyB, disabled, expired) {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("structured logs contain secret %q", secret)
		}
	}
}

type t080HTTPResult struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

type t080LogBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (buffer *t080LogBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.Buffer.Write(data)
}

func (buffer *t080LogBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.Buffer.String()
}

func t080ReadResponse(t *testing.T, response *http.Response) t080HTTPResult {
	t.Helper()
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return t080HTTPResult{StatusCode: response.StatusCode, Headers: response.Header.Clone(), Body: body}
}

func t080GenerateKey(t *testing.T, pepper []byte) auth.GeneratedGatewayKey {
	t.Helper()
	key, err := auth.GenerateGatewayKey(pepper)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func t080Secrets(pepper []byte, admin, upstream string, keys ...auth.GeneratedGatewayKey) []string {
	secrets := []string{string(pepper), admin, upstream}
	for _, key := range keys {
		secrets = append(secrets, key.RawKey, hex.EncodeToString(key.Digest))
	}
	return secrets
}

type t080Request struct {
	Method         string
	Path           string
	RawQuery       string
	Body           []byte
	Authorization  string
	RequiredHeader string
	HeaderDump     string
	Key            string
}

type t080Upstream struct {
	server      *httptest.Server
	URL         string
	credential  string
	mu          sync.Mutex
	calls       []t080Request
	active      map[string]int
	maxActive   map[string]int
	holdStarted map[string]chan struct{}
	holdRelease map[string]chan struct{}
	holdOnce    map[string]*sync.Once
	cancelled   chan struct{}
	cancelOnce  sync.Once
	callCounter atomic.Int32
}

func newT080Upstream(t *testing.T, credential string) *t080Upstream {
	t.Helper()
	upstream := &t080Upstream{
		credential: credential, active: make(map[string]int), maxActive: make(map[string]int),
		holdStarted: map[string]chan struct{}{"A": make(chan struct{}, 4), "B": make(chan struct{}, 4)},
		holdRelease: map[string]chan struct{}{"A": make(chan struct{}), "B": make(chan struct{})},
		holdOnce:    map[string]*sync.Once{"A": new(sync.Once), "B": new(sync.Once)},
		cancelled:   make(chan struct{}),
	}
	upstream.server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		key := request.Header.Get("X-Test-Key")
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return
		}
		upstream.callCounter.Add(1)
		upstream.mu.Lock()
		upstream.calls = append(upstream.calls, t080Request{
			Method: request.Method, Path: request.URL.Path, RawQuery: request.URL.RawQuery,
			Body: body, Authorization: request.Header.Get("Authorization"),
			RequiredHeader: request.Header.Get("X-Required-Header"), HeaderDump: fmt.Sprintf("%v", request.Header), Key: key,
		})
		upstream.mu.Unlock()
		if request.URL.Query().Get("kind") == "hold" {
			upstream.enterActive(key)
			defer upstream.leaveActive(key)
			response.Header().Set("Content-Type", "text/event-stream")
			response.Header().Set("X-Upstream-Trace", "t080-trace")
			response.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(response, "data: start\n\n")
			if flusher, ok := response.(http.Flusher); ok {
				flusher.Flush()
			}
			upstream.holdStarted[key] <- struct{}{}
			<-upstream.holdRelease[key]
			_, _ = io.WriteString(response, "data: done\n\n")
			return
		}
		if request.URL.Query().Get("kind") == "cancel" {
			upstream.enterActive(key)
			defer upstream.leaveActive(key)
			response.Header().Set("Content-Type", "text/event-stream")
			response.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(response, "data: held\n\n")
			if flusher, ok := response.(http.Flusher); ok {
				flusher.Flush()
			}
			<-request.Context().Done()
			upstream.cancelOnce.Do(func() { close(upstream.cancelled) })
			return
		}
		if request.URL.Query().Get("kind") == "sse" {
			response.Header().Set("Content-Type", "text/event-stream")
			response.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(response, "data: eof\n\n")
			return
		}
		if request.URL.Query().Get("kind") == "error" {
			response.Header().Set("Content-Type", "application/json")
			response.Header().Set("Authorization", "Bearer "+credential)
			response.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(response, `{"error":"upstream failure"}`)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("X-Upstream-Trace", "t080-trace")
		response.Header().Set("Authorization", "Bearer "+credential)
		response.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(response, `{"accepted":true}`)
	}))
	upstream.URL = upstream.server.URL
	t.Cleanup(func() { upstream.server.Close() })
	return upstream
}

func (upstream *t080Upstream) enterActive(key string) {
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	upstream.active[key]++
	if upstream.active[key] > upstream.maxActive[key] {
		upstream.maxActive[key] = upstream.active[key]
	}
}

func (upstream *t080Upstream) leaveActive(key string) {
	upstream.mu.Lock()
	upstream.active[key]--
	upstream.mu.Unlock()
}

func (upstream *t080Upstream) waitForHold(t *testing.T, key string, count int) {
	t.Helper()
	for range count {
		select {
		case <-upstream.holdStarted[key]:
		case <-time.After(time.Second):
			t.Fatalf("upstream did not receive %s hold", key)
		}
	}
}

func (upstream *t080Upstream) releaseHold(key string) {
	upstream.holdOnce[key].Do(func() { close(upstream.holdRelease[key]) })
}

func (upstream *t080Upstream) waitForCancellation(t *testing.T) {
	t.Helper()
	select {
	case <-upstream.cancelled:
	case <-time.After(time.Second):
		t.Fatal("upstream did not observe cancellation")
	}
}

func (upstream *t080Upstream) callCount() int {
	return int(upstream.callCounter.Load())
}

func (upstream *t080Upstream) lastCall() t080Request {
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	return upstream.calls[len(upstream.calls)-1]
}

func (upstream *t080Upstream) maxActiveFor(key string) int {
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	return upstream.maxActive[key]
}

type t080StartedRequest struct {
	response *http.Response
}

func t080StartRequest(t *testing.T, client *http.Client, target, rawKey, key string) t080StartedRequest {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+rawKey)
	request.Header.Set("X-Test-Key", key)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return t080StartedRequest{response: response}
}

func t080FinishRequest(t *testing.T, started t080StartedRequest) t080HTTPResult {
	t.Helper()
	return t080ReadResponse(t, started.response)
}

func t080WaitFor(t *testing.T, predicate func() bool) {
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
