package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/transport"
)

func TestRequestLimitHTTPRejectsBeforeUpstreamAndHonorsReset(t *testing.T) {
	clock := &requestLimitTestClock{now: time.Unix(30, 0).UTC()}
	pepper := []byte("request-limit-pepper")
	key, authenticator := requestLimitTestAuthenticator(t, pepper, "key-a", `{"request_windows":[{"amount":1,"duration":"1m"}]}`, clock)
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		upstreamCalls.Add(1)
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	requestLimiter := limiter.NewRequestLimiter(clock.Now)
	gateway := httptest.NewServer(NewHandlerWithAuthenticatorAndRequestLimiter(transport.NewClient(), upstream.URL, "upstream-secret", authenticator, requestLimiter, nil))
	t.Cleanup(gateway.Close)

	request := func() *http.Response {
		req, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/opaque", bytes.NewReader([]byte("preserve")))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+key.RawKey)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}

	first := request()
	first.Body.Close()
	if first.StatusCode != http.StatusNoContent {
		t.Fatalf("first status = %d, want 204", first.StatusCode)
	}
	second := request()
	body, err := io.ReadAll(second.Body)
	second.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if second.StatusCode != http.StatusTooManyRequests || second.Header.Get("Retry-After") != "30" {
		t.Fatalf("rejected response = status %d, retry-after %q, body %q", second.StatusCode, second.Header.Get("Retry-After"), body)
	}
	if !bytes.Contains(body, []byte(`"code":"request_limit_exceeded"`)) {
		t.Fatalf("rejected body = %q", body)
	}
	if got := upstreamCalls.Load(); got != 1 {
		t.Fatalf("upstream calls after rejection = %d, want 1", got)
	}

	clock.Set(time.Unix(60, 0).UTC())
	third := request()
	third.Body.Close()
	if third.StatusCode != http.StatusNoContent {
		t.Fatalf("boundary status = %d, want 204", third.StatusCode)
	}
	if got := upstreamCalls.Load(); got != 2 {
		t.Fatalf("upstream calls after boundary = %d, want 2", got)
	}
}

func TestRequestLimitHTTPDoesNotConsumeNoWindowOrRefundFailures(t *testing.T) {
	clock := &requestLimitTestClock{now: time.Unix(10, 0).UTC()}
	pepper := []byte("request-limit-no-window")
	limited, authenticator := requestLimitTestAuthenticator(t, pepper, "limited", `{"request_windows":[{"amount":1,"duration":"1m"}]}`, clock)
	unlimited, err := auth.GenerateGatewayKey(pepper)
	if err != nil {
		t.Fatal(err)
	}
	if err := authenticator.Load([]auth.Record{
		{ID: "limited", DisplayPrefix: limited.DisplayPrefix, Digest: limited.Digest, Enabled: true, PolicyJSON: []byte(`{"request_windows":[{"amount":1,"duration":"1m"}]}`)},
		{ID: "unlimited", DisplayPrefix: unlimited.DisplayPrefix, Digest: unlimited.Digest, Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		call := upstreamCalls.Add(1)
		if call == 1 {
			response.WriteHeader(http.StatusBadGateway)
			return
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	gateway := httptest.NewServer(NewHandlerWithAuthenticatorAndRequestLimiter(transport.NewClient(), upstream.URL, "upstream-secret", authenticator, limiter.NewRequestLimiter(clock.Now), nil))
	t.Cleanup(gateway.Close)

	get := func(rawKey string) int {
		req, err := http.NewRequest(http.MethodGet, gateway.URL+"/v1/models", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+rawKey)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response.StatusCode
	}
	if got := get(limited.RawKey); got != http.StatusBadGateway {
		t.Fatalf("upstream failure status = %d", got)
	}
	if got := get(limited.RawKey); got != http.StatusTooManyRequests {
		t.Fatalf("post-failure status = %d, want 429", got)
	}
	if got := get(unlimited.RawKey); got != http.StatusNoContent {
		t.Fatalf("unlimited first status = %d", got)
	}
	if got := get(unlimited.RawKey); got != http.StatusNoContent {
		t.Fatalf("unlimited second status = %d", got)
	}
	if got := upstreamCalls.Load(); got != 3 {
		t.Fatalf("upstream calls = %d, want 3", got)
	}
}

func TestRequestLimitHTTPUsesPositiveRoundedRetryAfter(t *testing.T) {
	now := time.Unix(1, 0).UTC()
	reset := time.Unix(1, 500_000_000).UTC()
	if got := limiter.RetryAfterSecondsAt(now, reset); got != 1 {
		t.Fatalf("fractional retry-after = %d, want 1", got)
	}
	if got := limiter.RetryAfterSecondsAt(reset, reset); got != 1 {
		t.Fatalf("exact retry-after = %d, want 1", got)
	}
}

func TestLimiterHTTPSSEToJSONHoldsAndReleasesConcurrencyLease(t *testing.T) {
	clock := &requestLimitTestClock{now: time.Unix(30, 0).UTC()}
	pepper := []byte("limited-sse-compatibility")
	key, authenticator := requestLimitTestAuthenticator(t, pepper, "sse", `{"allowed_models":["gpt-*"],"request_windows":[{"amount":4,"duration":"1m"}],"max_concurrent_requests":1}`, clock)
	firstStarted := make(chan struct{})
	firstRelease := make(chan struct{})
	cancelStarted := make(chan struct{})
	cancelObserved := make(chan struct{})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		call := calls.Add(1)
		response.Header().Set("Content-Type", "text/event-stream")
		switch call {
		case 1:
			_, _ = io.WriteString(response, `data: {"id":"limited-sse","choices":[{"index":0,"delta":{"content":"hello"}}]}`+"\n\n")
			if flusher, ok := response.(http.Flusher); ok {
				flusher.Flush()
			}
			close(firstStarted)
			<-firstRelease
			_, _ = io.WriteString(response, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		case 2:
			_, _ = io.WriteString(response, "data: {not-json}\n\n")
		case 3:
			_, _ = io.WriteString(response, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"cancel\"}}]}\n\n")
			if flusher, ok := response.(http.Flusher); ok {
				flusher.Flush()
			}
			close(cancelStarted)
			<-request.Context().Done()
			close(cancelObserved)
		default:
			_, _ = io.WriteString(response, "data: {\"id\":\"final\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}
	}))
	t.Cleanup(upstream.Close)
	requestLimiter := limiter.NewRequestLimiter(clock.Now)
	concurrencyLimiter := limiter.NewConcurrencyLimiter()
	gateway := httptest.NewServer(NewHandlerWithAuthenticatorAndLimiters(transport.NewClient(), upstream.URL, "upstream-secret", authenticator, requestLimiter, concurrencyLimiter, nil))
	t.Cleanup(gateway.Close)
	newRequest := func(ctx context.Context) *http.Request {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","stream":false}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+key.RawKey)
		request.Header.Set("Content-Type", "application/json")
		return request
	}
	do := func(ctx context.Context) (*http.Response, error) {
		return http.DefaultClient.Do(newRequest(ctx))
	}
	readBody := func(response *http.Response) []byte {
		t.Helper()
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return body
	}

	firstResult := make(chan *http.Response, 1)
	firstError := make(chan error, 1)
	go func() {
		response, err := do(context.Background())
		if err != nil {
			firstError <- err
		} else {
			firstResult <- response
		}
	}()
	select {
	case <-firstStarted:
	case err := <-firstError:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("first aggregation did not block after receiving SSE")
	}
	second, err := do(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	secondBody := readBody(second)
	if second.StatusCode != http.StatusTooManyRequests || !bytes.Contains(secondBody, []byte(`"code":"concurrency_limit_exceeded"`)) {
		t.Fatalf("aggregation saturation = %d/%q", second.StatusCode, secondBody)
	}
	close(firstRelease)
	select {
	case response := <-firstResult:
		body := readBody(response)
		if response.StatusCode != http.StatusOK || !json.Valid(body) || !bytes.Contains(body, []byte(`"hello"`)) {
			t.Fatalf("converted response = %d/%q", response.StatusCode, body)
		}
	case err := <-firstError:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("first aggregation did not release after EOF")
	}

	failed, err := do(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	failedBody := readBody(failed)
	if failed.StatusCode != http.StatusBadGateway || bytes.Contains(failedBody, []byte("not-json")) {
		t.Fatalf("conversion failure = %d/%q", failed.StatusCode, failedBody)
	}
	// A failed conversion must release the lease for the next request.
	cancelContext, cancel := context.WithCancel(context.Background())
	cancelResult := make(chan error, 1)
	go func() {
		response, requestErr := do(cancelContext)
		if response != nil {
			response.Body.Close()
		}
		cancelResult <- requestErr
	}()
	select {
	case <-cancelStarted:
	case <-time.After(time.Second):
		t.Fatal("cancellation aggregation did not start")
	}
	cancel()
	select {
	case <-cancelObserved:
	case <-time.After(time.Second):
		t.Fatal("upstream did not observe aggregation cancellation")
	}
	select {
	case <-cancelResult:
	case <-time.After(time.Second):
		t.Fatal("canceled aggregation did not finish")
	}

	final, err := do(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if final.StatusCode != http.StatusOK {
		t.Fatalf("post-cancel status = %d, want 200", final.StatusCode)
	}
	readBody(final)
	// Only the four admitted requests consume the request window; the
	// concurrency rejection does not, and no lifecycle path refunds capacity.
	limited, err := do(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	limitedBody := readBody(limited)
	if limited.StatusCode != http.StatusTooManyRequests || !bytes.Contains(limitedBody, []byte(`"code":"request_limit_exceeded"`)) {
		t.Fatalf("one-time request-window consumption = %d/%q", limited.StatusCode, limitedBody)
	}
	if got := calls.Load(); got != 4 {
		t.Fatalf("upstream calls = %d, want 4 admitted requests", got)
	}
}

type requestLimitTestClock struct {
	mu  sync.RWMutex
	now time.Time
}

func (clock *requestLimitTestClock) Now() time.Time {
	clock.mu.RLock()
	defer clock.mu.RUnlock()
	return clock.now
}

func (clock *requestLimitTestClock) Set(now time.Time) {
	clock.mu.Lock()
	clock.now = now
	clock.mu.Unlock()
}

func requestLimitTestAuthenticator(t *testing.T, pepper []byte, id, policy string, clock *requestLimitTestClock) (auth.GeneratedGatewayKey, *auth.Authenticator) {
	t.Helper()
	key, err := auth.GenerateGatewayKey(pepper)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := auth.NewAuthenticator(pepper, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := authenticator.Load([]auth.Record{{ID: id, DisplayPrefix: key.DisplayPrefix, Digest: key.Digest, Enabled: true, PolicyJSON: []byte(policy)}}); err != nil {
		t.Fatal(err)
	}
	return key, authenticator
}
