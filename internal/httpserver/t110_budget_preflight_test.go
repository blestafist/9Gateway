package httpserver

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/pestit/9gateway/internal/accounting"
	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/config"
	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/transport"
	"gopkg.in/yaml.v3"
)

func TestT110BudgetPreflightUsesExactThenGlobAndConservativeCapacity(t *testing.T) {
	pricing := t110Pricing(t, `rules:
  - model: exact
    input_per_million_micros: 500000
    output_per_million_micros: 500000
  - model: '*'
    input_per_million_micros: 1000000
    output_per_million_micros: 1000000
`)
	key, authenticator := t110Key(t, `{"token_mode":"usage_only","budget_limits":[{"amount_micros":1,"period":"total"}]}`)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	budget := limiter.NewBudgetLimiter()
	gateway := httptest.NewServer(NewHandlerWithAuthenticatorAndLimitersAndTokenConfig(
		transport.NewClient(), upstream.URL, "upstream-secret", authenticator,
		limiter.NewRequestLimiter(nil), limiter.NewConcurrencyLimiter(), nil,
		TokenAdmissionConfig{FallbackUnknownInputTokens: 1, FallbackMaxOutputTokens: 1, PricingResolver: accounting.NewPricingResolver(pricing), BudgetLimiter: budget},
	))
	t.Cleanup(gateway.Close)

	call := func(model string) *http.Response {
		request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", bytes.NewBufferString(`{"model":"`+model+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+key.RawKey)
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	first := call("exact")
	first.Body.Close()
	if first.StatusCode != http.StatusNoContent {
		t.Fatalf("exact price status = %d", first.StatusCode)
	}
	second := call("globbed")
	secondBody, _ := io.ReadAll(second.Body)
	second.Body.Close()
	if second.StatusCode != http.StatusTooManyRequests || second.Header.Get("Retry-After") != "" || !bytes.Contains(secondBody, []byte(`"code":"budget_exceeded"`)) {
		t.Fatalf("glob capacity rejection = %d/retry %q/body %q", second.StatusCode, second.Header.Get("Retry-After"), secondBody)
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls.Load())
	}
}

func TestT110BudgetPreflightZeroRuleAndModelsAreSafe(t *testing.T) {
	pricing := t110Pricing(t, `rules:
  - model: free
    input_per_million_micros: 0
    output_per_million_micros: 0
`)
	key, authenticator := t110Key(t, `{"budget_limits":[{"amount_micros":1,"period":"total"}]}`)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(request.Body)
		if request.Method == http.MethodPost && !bytes.Equal(body, []byte(`{"model":"free"}`)) {
			t.Errorf("body changed: %q", body)
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	gateway := httptest.NewServer(NewHandlerWithAuthenticatorAndLimitersAndTokenConfig(
		transport.NewClient(), upstream.URL, "upstream-secret", authenticator,
		limiter.NewRequestLimiter(nil), limiter.NewConcurrencyLimiter(), nil,
		TokenAdmissionConfig{FallbackUnknownInputTokens: 1, FallbackMaxOutputTokens: 1, PricingResolver: accounting.NewPricingResolver(pricing), BudgetLimiter: limiter.NewBudgetLimiter()},
	))
	t.Cleanup(gateway.Close)

	free := t110Request(t, gateway.URL+"/v1/chat/completions", key.RawKey, http.MethodPost, []byte(`{"model":"free"}`), "application/json")
	free.Body.Close()
	models := t110Request(t, gateway.URL+"/v1/models", key.RawKey, http.MethodGet, nil, "")
	models.Body.Close()
	if free.StatusCode != http.StatusNoContent || models.StatusCode != http.StatusNoContent || calls.Load() != 2 {
		t.Fatalf("zero/models statuses = %d/%d, calls = %d", free.StatusCode, models.StatusCode, calls.Load())
	}
}

func TestT110BudgetPreflightRejectsUnknownAndUninspectableBeforeUpstream(t *testing.T) {
	pricing := t110Pricing(t, `rules:
  - model: known
    input_per_million_micros: 1
    output_per_million_micros: 1
`)
	key, authenticator := t110Key(t, `{"budget_limits":[{"amount_micros":100,"period":"total"}]}`)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	gateway := httptest.NewServer(NewHandlerWithAuthenticatorAndLimitersAndTokenConfig(
		transport.NewClient(), upstream.URL, "upstream-secret", authenticator,
		limiter.NewRequestLimiter(nil), limiter.NewConcurrencyLimiter(), nil,
		TokenAdmissionConfig{MaxInspectedRequestBytes: 32, FallbackUnknownInputTokens: 1, FallbackMaxOutputTokens: 1, PricingResolver: accounting.NewPricingResolver(pricing), BudgetLimiter: limiter.NewBudgetLimiter()},
	))
	t.Cleanup(gateway.Close)
	for _, test := range []struct {
		name        string
		method      string
		path        string
		body        []byte
		contentType string
	}{
		{name: "unknown model", method: http.MethodPost, path: "/v1/chat/completions", body: []byte(`{"model":"other"}`), contentType: "application/json"},
		{name: "missing model", method: http.MethodPost, path: "/v1/chat/completions", body: []byte(`{"messages":[]}`), contentType: "application/json"},
		{name: "malformed", method: http.MethodPost, path: "/v1/chat/completions", body: []byte(`{"model":`), contentType: "application/json"},
		{name: "oversized", method: http.MethodPost, path: "/v1/chat/completions", body: bytes.Repeat([]byte{'x'}, 64), contentType: "application/json"},
		{name: "unknown endpoint", method: http.MethodPost, path: "/v1/custom", body: []byte(`{"model":"known"}`), contentType: "application/json"},
		{name: "wrong method", method: http.MethodPut, path: "/v1/chat/completions", body: []byte(`{"model":"known"}`), contentType: "application/json"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := t110Request(t, gateway.URL+test.path, key.RawKey, test.method, test.body, test.contentType)
			response.Body.Close()
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want controlled 400", response.StatusCode)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("upstream calls after rejected requests = %d", calls.Load())
	}
}

func t110Pricing(t *testing.T, source string) config.PricingConfig {
	t.Helper()
	var pricing config.PricingConfig
	if err := yaml.Unmarshal([]byte(source), &pricing); err != nil {
		t.Fatal(err)
	}
	return pricing
}

func t110Key(t *testing.T, policy string) (auth.GeneratedGatewayKey, *auth.Authenticator) {
	t.Helper()
	pepper := []byte("t110-budget-pepper")
	key, err := auth.GenerateGatewayKey(pepper)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := auth.NewAuthenticator(pepper, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := authenticator.Load([]auth.Record{{ID: "t110-key", DisplayPrefix: key.DisplayPrefix, Digest: key.Digest, Enabled: true, PolicyJSON: []byte(policy)}}); err != nil {
		t.Fatal(err)
	}
	return key, authenticator
}

func t110Request(t *testing.T, url, key, method string, body []byte, contentType string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+key)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
