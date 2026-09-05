package httpserver

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
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
