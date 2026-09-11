package httpserver

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/accounting"
	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/transport"
)

func TestT113TransparentSSEReconcilesBudgetAtPhysicalEOF(t *testing.T) {
	for _, test := range []struct {
		name      string
		terminal  string
		wantSpent int64
	}{
		{
			name:      "without DONE",
			terminal:  "data: {\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n",
			wantSpent: 1,
		},
		{
			name:      "usage-only terminal with DONE",
			terminal:  "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: {\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\ndata: [DONE]\n\n",
			wantSpent: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			key, authenticator := t110Key(t, `{"budget_limits":[{"amount_micros":3,"period":"total"}]}`)
			body := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\n" + test.terminal
			upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Type", "text/event-stream")
				for _, fragment := range []string{body[:len(body)/3], body[len(body)/3 : 2*len(body)/3], body[2*len(body)/3:]} {
					if _, err := io.WriteString(response, fragment); err != nil {
						return
					}
					response.(http.Flusher).Flush()
				}
			}))
			t.Cleanup(upstream.Close)
			budget := limiter.NewBudgetLimiter()
			var spent atomic.Int64
			budget.SetCommittedDeltaSink(func(delta limiter.CommittedBudgetDelta) { spent.Add(delta.Delta) })
			worker := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: 1})
			t.Cleanup(func() { shutdownObservationWorker(t, worker) })
			gateway := httptest.NewServer(NewHandlerWithAuthenticatorAndLimitersAndTokenLimiterAndUsageObservationWorker(
				transport.NewClient(), upstream.URL, "upstream-secret", authenticator,
				limiter.NewRequestLimiter(nil), limiter.NewConcurrencyLimiter(), nil, nil,
				TokenAdmissionConfig{
					FallbackUnknownInputTokens: 2,
					FallbackMaxOutputTokens:    2,
					PricingResolver:            accounting.NewPricingResolver(t112Pricing(t)),
					BudgetLimiter:              budget,
				}, worker,
			))
			t.Cleanup(gateway.Close)

			request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", bytes.NewBufferString(`{"model":"test","stream":true}`))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+key.RawKey)
			request.Header.Set("Content-Type", "application/json")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || response.StatusCode != http.StatusOK || string(got) != body {
				t.Fatalf("transparent stream = %d/%q (%v), want original body", response.StatusCode, got, err)
			}
			waitForObservationCount(t, worker, 1)
			waitForT113Budget(t, &spent, test.wantSpent)
		})
	}
}

func TestT113TransparentSSEBudgetKeepsConservativeOnMalformedAndGzip(t *testing.T) {
	for _, test := range []struct {
		name        string
		content     []byte
		encoding    string
		wantSpent   int64
		wantObserve bool
	}{
		{
			name:        "malformed",
			content:     []byte("data: {\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\ndata: {truncated"),
			wantSpent:   1,
			wantObserve: true,
		},
		{
			name:        "gzip",
			content:     []byte("data: {\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n"),
			encoding:    "gzip",
			wantSpent:   1,
			wantObserve: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			wire := test.content
			if test.encoding == "gzip" {
				var encoded bytes.Buffer
				writer := gzip.NewWriter(&encoded)
				if _, err := writer.Write(wire); err != nil {
					t.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				wire = encoded.Bytes()
			}
			key, authenticator := t110Key(t, `{"budget_limits":[{"amount_micros":3,"period":"total"}]}`)
			upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Type", "text/event-stream")
				if test.encoding != "" {
					response.Header().Set("Content-Encoding", test.encoding)
				}
				_, _ = response.Write(wire)
			}))
			t.Cleanup(upstream.Close)
			budget := limiter.NewBudgetLimiter()
			var spent atomic.Int64
			budget.SetCommittedDeltaSink(func(delta limiter.CommittedBudgetDelta) { spent.Add(delta.Delta) })
			worker := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: 1})
			t.Cleanup(func() { shutdownObservationWorker(t, worker) })
			gateway := httptest.NewServer(NewHandlerWithAuthenticatorAndLimitersAndTokenLimiterAndUsageObservationWorker(
				transport.NewClient(), upstream.URL, "upstream-secret", authenticator,
				limiter.NewRequestLimiter(nil), limiter.NewConcurrencyLimiter(), nil, nil,
				TokenAdmissionConfig{FallbackUnknownInputTokens: 2, FallbackMaxOutputTokens: 2, PricingResolver: accounting.NewPricingResolver(t112Pricing(t)), BudgetLimiter: budget}, worker,
			))
			t.Cleanup(gateway.Close)
			request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", bytes.NewBufferString(`{"model":"test","stream":true}`))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+key.RawKey)
			request.Header.Set("Content-Type", "application/json")
			response, err := (&http.Client{Transport: &http.Transport{DisableCompression: true}}).Do(request)
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || response.StatusCode != http.StatusOK || !bytes.Equal(got, wire) {
				t.Fatalf("wire response = %d/%x (%v), want %x", response.StatusCode, got, err, wire)
			}
			if test.wantObserve {
				waitForObservationCount(t, worker, 1)
			}
			waitForT113Budget(t, &spent, test.wantSpent)
		})
	}
}

func TestT113TransparentSSEBudgetActualHigherThanReservation(t *testing.T) {
	key, authenticator := t110Key(t, `{"budget_limits":[{"amount_micros":3,"period":"total"}]}`)
	body := "data: {\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":1,\"total_tokens\":5}}\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(response, body)
	}))
	t.Cleanup(upstream.Close)
	budget := limiter.NewBudgetLimiter()
	var spent atomic.Int64
	budget.SetCommittedDeltaSink(func(delta limiter.CommittedBudgetDelta) { spent.Add(delta.Delta) })
	worker := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: 1})
	t.Cleanup(func() { shutdownObservationWorker(t, worker) })
	gateway := httptest.NewServer(NewHandlerWithAuthenticatorAndLimitersAndTokenLimiterAndUsageObservationWorker(
		transport.NewClient(), upstream.URL, "upstream-secret", authenticator,
		limiter.NewRequestLimiter(nil), limiter.NewConcurrencyLimiter(), nil, nil,
		TokenAdmissionConfig{FallbackUnknownInputTokens: 2, FallbackMaxOutputTokens: 2, PricingResolver: accounting.NewPricingResolver(t112Pricing(t)), BudgetLimiter: budget}, worker,
	))
	t.Cleanup(gateway.Close)
	request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", bytes.NewBufferString(`{"model":"test","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+key.RawKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || string(got) != body {
		t.Fatalf("transparent response = %d/%q (%v)", response.StatusCode, got, err)
	}
	waitForObservationCount(t, worker, 1)
	waitForT113Budget(t, &spent, 3)
	if got := spent.Load(); got != 3 {
		// The sink receives the conservative commit and the subsequent positive
		// adjustment; their cumulative value is the actual cost.
		t.Fatalf("higher actual spend = %d, want 3; worker=%+v", got, worker.Stats())
	}
}

func waitForT113Budget(t *testing.T, spent *atomic.Int64, want int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for spent.Load() != want && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := spent.Load(); got != want {
		t.Fatalf("budget settlement = %d, want %d", got, want)
	}
}
