package httpserver

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/transport"
)

func TestT096InvalidUpstreamRequestReleasesAdmissionBeforeUpstream(t *testing.T) {
	clock := &requestLimitTestClock{now: time.Unix(30, 0).UTC()}
	key, authenticator := requestLimitTestAuthenticator(t, []byte("t096-pre-start"), "t096-pre-start", `{"token_windows":[{"amount":4,"duration":"1m"}],"max_concurrent_requests":1}`, clock)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	proxy := NewHandlerWithAuthenticatorAndLimitersAndTokenLimiter(
		transport.NewClient(), upstream.URL, "upstream-secret", authenticator,
		limiter.NewRequestLimiter(clock.Now), limiter.NewConcurrencyLimiter(), nil,
		limiter.NewTokenLimiter(clock.Now), TokenAdmissionConfig{FallbackUnknownInputTokens: 2, FallbackMaxOutputTokens: 2},
	)

	// A malformed method can be supplied to a handler directly even though the
	// HTTP server parser would reject it. It exercises the post-admission,
	// pre-client.Do construction failure without touching upstream.
	target, err := url.Parse("/v1/opaque")
	if err != nil {
		t.Fatal(err)
	}
	handler := withGatewayAuthentication(authenticator, proxy)
	first := httptest.NewRecorder()
	request := &http.Request{Method: "bad\nmethod", URL: target, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(nil))}
	request.Header.Set("Authorization", "Bearer "+key.RawKey)
	handler.ServeHTTP(first, request)
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("pre-start failure status = %d, want 500", first.Code)
	}

	secondRequest, err := http.NewRequest(http.MethodGet, "http://gateway.invalid/v1/opaque", nil)
	if err != nil {
		t.Fatal(err)
	}
	secondRequest.Header.Set("Authorization", "Bearer "+key.RawKey)
	second := httptest.NewRecorder()
	secondRequest.Method = http.MethodGet
	handler.ServeHTTP(second, secondRequest)
	if second.Code != http.StatusNoContent {
		t.Fatalf("capacity reuse after pre-start failure = %d, want 204", second.Code)
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want one reusable admission", calls.Load())
	}
}

func TestT096UpstreamReadFailureRetainsConservativeCharge(t *testing.T) {
	clock := &requestLimitTestClock{now: time.Unix(30, 0).UTC()}
	key, authenticator := requestLimitTestAuthenticator(t, []byte("t096-read-failure"), "t096-read-failure", `{"token_windows":[{"amount":4,"duration":"1m"}]}`, clock)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("Content-Length", "64")
		_, _ = io.WriteString(response, `{"usage":{"total_tokens":1}}`)
	}))
	t.Cleanup(upstream.Close)
	gateway := httptest.NewServer(NewHandlerWithAuthenticatorAndLimitersAndTokenLimiter(
		transport.NewClient(), upstream.URL, "upstream-secret", authenticator,
		limiter.NewRequestLimiter(clock.Now), limiter.NewConcurrencyLimiter(), nil,
		limiter.NewTokenLimiter(clock.Now), TokenAdmissionConfig{FallbackUnknownInputTokens: 2, FallbackMaxOutputTokens: 2},
	))
	t.Cleanup(gateway.Close)

	request := func() *http.Response {
		req, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", bytes.NewBufferString(`{"model":`))
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
	_, _ = io.ReadAll(first.Body)
	first.Body.Close()
	second := request()
	secondBody, _ := io.ReadAll(second.Body)
	second.Body.Close()
	if second.StatusCode != http.StatusTooManyRequests || !bytes.Contains(secondBody, []byte(`"code":"token_limit_exceeded"`)) {
		t.Fatalf("ambiguous read failure admission = %d/%q, want conservative token rejection", second.StatusCode, secondBody)
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want one", calls.Load())
	}
}
