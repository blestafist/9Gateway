package httpserver

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/transport"
)

func TestT091TokenAdmissionRejectsBeforeUpstreamAndUsesExactReset(t *testing.T) {
	clock := &requestLimitTestClock{now: time.Unix(30, 0).UTC()}
	pepper := []byte("t091-token-pepper")
	key, authenticator := requestLimitTestAuthenticator(t, pepper, "token-key", `{"token_windows":[{"amount":10,"duration":"1m"}]}`, clock)
	var calls atomic.Int32
	var gotBody atomic.Value
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(request.Body)
		gotBody.Store(body)
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	tokens := limiter.NewTokenLimiter(clock.Now)
	gateway := httptest.NewServer(NewHandlerWithAuthenticatorAndLimitersAndTokenLimiter(
		transport.NewClient(), upstream.URL, "upstream-secret", authenticator,
		limiter.NewRequestLimiter(clock.Now), limiter.NewConcurrencyLimiter(), nil,
		tokens, TokenAdmissionConfig{FallbackUnknownInputTokens: 2, FallbackMaxOutputTokens: 2},
	))
	t.Cleanup(gateway.Close)

	body := []byte(`{"model":"gpt-test","max_tokens":1,"messages":[{"role":"user","content":"a"}]}`)
	request := func() *http.Response {
		req, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+key.RawKey)
		req.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}

	first := request()
	first.Body.Close()
	if first.StatusCode != http.StatusNoContent || calls.Load() != 1 {
		t.Fatalf("first result = status %d, calls %d", first.StatusCode, calls.Load())
	}
	if got, ok := gotBody.Load().([]byte); !ok || !bytes.Equal(got, body) {
		t.Fatalf("forwarded body = %q, want %q", got, body)
	}

	second := request()
	secondBody, _ := io.ReadAll(second.Body)
	second.Body.Close()
	if second.StatusCode != http.StatusTooManyRequests || second.Header.Get("Retry-After") != "30" || !bytes.Contains(secondBody, []byte(`"code":"token_limit_exceeded"`)) {
		t.Fatalf("rejection = status %d retry %q body %q", second.StatusCode, second.Header.Get("Retry-After"), secondBody)
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls after token rejection = %d, want 1", calls.Load())
	}

	clock.Set(time.Unix(60, 0).UTC())
	third := request()
	third.Body.Close()
	if third.StatusCode != http.StatusNoContent || calls.Load() != 2 {
		t.Fatalf("reset result = status %d, calls %d", third.StatusCode, calls.Load())
	}
}

func TestT091TokenFallbackPreservesMalformedAndUnknownBodies(t *testing.T) {
	clock := &requestLimitTestClock{now: time.Unix(10, 0).UTC()}
	pepper := []byte("t091-fallback-pepper")
	key, authenticator := requestLimitTestAuthenticator(t, pepper, "fallback-key", `{"token_windows":[{"amount":10,"duration":"1m"}]}`, clock)
	received := make(chan []byte, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		received <- body
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	gateway := httptest.NewServer(NewHandlerWithAuthenticatorAndLimitersAndTokenLimiter(
		transport.NewClient(), upstream.URL, "upstream-secret", authenticator,
		limiter.NewRequestLimiter(clock.Now), limiter.NewConcurrencyLimiter(), nil,
		limiter.NewTokenLimiter(clock.Now), TokenAdmissionConfig{FallbackUnknownInputTokens: 2, FallbackMaxOutputTokens: 1},
	))
	t.Cleanup(gateway.Close)

	for _, test := range []struct {
		path string
		body []byte
	}{
		{path: "/v1/chat/completions", body: []byte(`{"model":`)},
		{path: "/v1/custom", body: []byte("opaque bytes, not JSON")},
	} {
		req, err := http.NewRequest(http.MethodPost, gateway.URL+test.path, bytes.NewReader(test.body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+key.RawKey)
		req.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("%s status = %d", test.path, response.StatusCode)
		}
		select {
		case got := <-received:
			if !bytes.Equal(got, test.body) {
				t.Fatalf("%s body = %q, want %q", test.path, got, test.body)
			}
		case <-time.After(time.Second):
			t.Fatalf("upstream did not receive %s", test.path)
		}
	}
}
