package httpserver

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/pestit/9gateway/internal/accounting"
	"github.com/pestit/9gateway/internal/config"
	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/transport"
	"gopkg.in/yaml.v3"
)

func TestT112ConvertedSSESettlesActualBudgetBeforeFollowUpAdmission(t *testing.T) {
	for _, test := range []struct {
		name       string
		usage      string
		wantSpent  int64
		wantSecond int
	}{
		{name: "lower", usage: `"prompt_tokens":1,"completion_tokens":1,"total_tokens":2`, wantSpent: 1, wantSecond: http.StatusOK},
		{name: "higher", usage: `"prompt_tokens":4,"completion_tokens":1,"total_tokens":5`, wantSpent: 3, wantSecond: http.StatusTooManyRequests},
	} {
		t.Run(test.name, func(t *testing.T) {
			pricing := t112Pricing(t)
			key, authenticator := t110Key(t, `{"budget_limits":[{"amount_micros":3,"period":"total"}]}`)
			stream := "data: {\"id\":\"t112\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\n" +
				"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: {\"choices\":[],\"usage\":{" + test.usage + "}}\n\n" +
				"data: [DONE]\n\n"
			upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(response, stream)
			}))
			t.Cleanup(upstream.Close)
			budget := limiter.NewBudgetLimiter()
			var spent atomic.Int64
			budget.SetCommittedDeltaSink(func(delta limiter.CommittedBudgetDelta) { spent.Add(delta.Delta) })
			gateway := httptest.NewServer(NewHandlerWithAuthenticatorAndLimitersAndTokenConfig(
				transport.NewClient(), upstream.URL, "upstream-secret", authenticator,
				limiter.NewRequestLimiter(nil), limiter.NewConcurrencyLimiter(), nil,
				TokenAdmissionConfig{
					FallbackUnknownInputTokens: 2,
					FallbackMaxOutputTokens:    2,
					PricingResolver:            accounting.NewPricingResolver(pricing),
					BudgetLimiter:              budget,
				},
			))
			t.Cleanup(gateway.Close)

			call := func() *http.Response {
				request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", bytes.NewBufferString(`{"model":"test","stream":false}`))
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

			first := call()
			body, err := io.ReadAll(first.Body)
			first.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if first.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"total_tokens":`)) {
				t.Fatalf("converted response = %d/%q", first.StatusCode, body)
			}
			if got := spent.Load(); got != test.wantSpent {
				t.Fatalf("settled budget = %d, want %d", got, test.wantSpent)
			}

			second := call()
			secondBody, _ := io.ReadAll(second.Body)
			second.Body.Close()
			if second.StatusCode != test.wantSecond {
				t.Fatalf("follow-up status = %d/%q, want %d", second.StatusCode, secondBody, test.wantSecond)
			}
		})
	}
}

func t112Pricing(t *testing.T) config.PricingConfig {
	t.Helper()
	var pricing config.PricingConfig
	if err := yaml.Unmarshal([]byte("rules:\n  - model: test\n    input_per_million_micros: 500000\n    output_per_million_micros: 500000\n"), &pricing); err != nil {
		t.Fatal(err)
	}
	return pricing
}
