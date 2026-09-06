package httpserver

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/transport"
)

func TestT094ConvertedSSECommitsCanonicalUsageAfterWritingJSON(t *testing.T) {
	clock := &requestLimitTestClock{now: time.Unix(30, 0).UTC()}
	key, authenticator := requestLimitTestAuthenticator(t, []byte("t094-pepper"), "t094", `{"token_windows":[{"amount":5,"duration":"1m"}]}`, clock)
	const stream = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":0,\"completion_tokens\":1,\"total_tokens\":1}}\n\ndata: [DONE]\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(response, stream)
	}))
	t.Cleanup(upstream.Close)
	tokens := limiter.NewTokenLimiter(clock.Now)
	gateway := httptest.NewServer(NewHandlerWithAuthenticatorAndLimitersAndTokenLimiter(
		transport.NewClient(), upstream.URL, "upstream-secret", authenticator,
		limiter.NewRequestLimiter(clock.Now), limiter.NewConcurrencyLimiter(), nil,
		tokens, TokenAdmissionConfig{FallbackUnknownInputTokens: 2, FallbackMaxOutputTokens: 2},
	))
	t.Cleanup(gateway.Close)

	request := func() *http.Response {
		req, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-test","stream":false}`))
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
	body, err := io.ReadAll(first.Body)
	first.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if first.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"total_tokens":1`)) {
		t.Fatalf("converted response = %d/%q", first.StatusCode, body)
	}
	// The admitted estimate is four tokens. Synchronous canonical reconciliation
	// leaves one committed token, so another four-token request is admissible.
	second := request()
	second.Body.Close()
	if second.StatusCode != http.StatusOK {
		t.Fatalf("reconciled admission status = %d, want %d", second.StatusCode, http.StatusOK)
	}
}

func TestT094ConvertedSSEFailureConservativelySettlesLease(t *testing.T) {
	clock := &requestLimitTestClock{now: time.Unix(30, 0).UTC()}
	key, authenticator := requestLimitTestAuthenticator(t, []byte("t094-failure-pepper"), "t094-failure", `{"token_windows":[{"amount":8,"duration":"1m"}]}`, clock)
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(response, "data: {bad}\n\n")
	}))
	t.Cleanup(upstream.Close)
	gateway := httptest.NewServer(NewHandlerWithAuthenticatorAndLimitersAndTokenLimiter(
		transport.NewClient(), upstream.URL, "upstream-secret", authenticator,
		limiter.NewRequestLimiter(clock.Now), limiter.NewConcurrencyLimiter(), nil,
		limiter.NewTokenLimiter(clock.Now), TokenAdmissionConfig{FallbackUnknownInputTokens: 2, FallbackMaxOutputTokens: 2},
	))
	t.Cleanup(gateway.Close)

	request := func() *http.Response {
		req, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-test","stream":false}`))
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
	if first.StatusCode != http.StatusBadGateway {
		t.Fatalf("failed conversion status = %d, want %d", first.StatusCode, http.StatusBadGateway)
	}
	second := request()
	secondBody, _ := io.ReadAll(second.Body)
	second.Body.Close()
	if second.StatusCode != http.StatusBadGateway || bytes.Contains(secondBody, []byte(`"code":"token_limit_exceeded"`)) {
		t.Fatalf("conservative follow-up = %d/%q", second.StatusCode, secondBody)
	}
}
