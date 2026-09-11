package httpserver

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/accounting"
	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/protocol/openai"
	"github.com/pestit/9gateway/internal/transport"
)

func TestT114BudgetPreStartFailureReusesBudgetImmediately(t *testing.T) {
	key, authenticator := t114BudgetKey(t, `{"budget_limits":[{"amount_micros":1,"period":"total"}]}`)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	budget := limiter.NewBudgetLimiter()
	proxy := newProxyHandlerWithLimitersAndTokenConfig(
		nil, upstream.URL, "upstream-secret",
		limiter.NewRequestLimiter(nil), limiter.NewConcurrencyLimiter(),
		TokenAdmissionConfig{FallbackUnknownInputTokens: 1, FallbackMaxOutputTokens: 1, PricingResolver: t114Pricing(t), BudgetLimiter: budget},
	)
	gateway := httptest.NewServer(withGatewayAuthentication(authenticator, proxy))
	t.Cleanup(gateway.Close)

	// A nil client exercises the admitted, pre-client.Do construction branch;
	// the real handler checks this dependency before invoking client.Do.
	first := t114HTTPBudgetRequest(t, gateway.URL, key.RawKey)
	firstBody, _ := io.ReadAll(first.Body)
	first.Body.Close()
	if first.StatusCode != http.StatusInternalServerError {
		t.Fatalf("pre-start status = %d body=%s, want 500", first.StatusCode, firstBody)
	}
	if budget.Len() != 0 {
		t.Fatalf("pre-start failure retained budget states = %d", budget.Len())
	}

	proxy.client = transport.NewClient()
	second := t114HTTPBudgetRequest(t, gateway.URL, key.RawKey)
	if second.StatusCode != http.StatusNoContent {
		t.Fatalf("budget reuse status = %d, want 204", second.StatusCode)
	}
	second.Body.Close()
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want one", calls.Load())
	}
}

func TestT114BudgetPostStartErrorsRemainConservative(t *testing.T) {
	tests := []struct {
		name         string
		makeUpstream func(*testing.T) (string, func())
		wantStatus   int
	}{
		{
			name: "connection error",
			makeUpstream: func(t *testing.T) (string, func()) {
				server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
				url := server.URL
				server.Close()
				return url, func() {}
			},
			wantStatus: http.StatusBadGateway,
		},
		{
			name: "response read error",
			makeUpstream: func(t *testing.T) (string, func()) {
				server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
					response.Header().Set("Content-Type", "application/json")
					response.Header().Set("Content-Length", "64")
					_, _ = io.WriteString(response, `{"partial":true}`)
				}))
				return server.URL, server.Close
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "unsupported response",
			makeUpstream: func(t *testing.T) (string, func()) {
				server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
					response.Header().Set("Content-Type", "application/octet-stream")
					_, _ = io.WriteString(response, "opaque")
				}))
				return server.URL, server.Close
			},
			wantStatus: http.StatusOK,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key, authenticator := t114BudgetKey(t, `{"budget_limits":[{"amount_micros":1,"period":"total"}]}`)
			upstreamURL, closeUpstream := test.makeUpstream(t)
			t.Cleanup(closeUpstream)
			budget := limiter.NewBudgetLimiter()
			handler := NewHandlerWithAuthenticatorAndLimitersAndTokenConfig(
				transport.NewClient(), upstreamURL, "upstream-secret", authenticator,
				limiter.NewRequestLimiter(nil), limiter.NewConcurrencyLimiter(), nil,
				TokenAdmissionConfig{FallbackUnknownInputTokens: 1, FallbackMaxOutputTokens: 1, PricingResolver: t114Pricing(t), BudgetLimiter: budget},
			)
			gateway := httptest.NewServer(withGatewayAuthentication(authenticator, handler))
			t.Cleanup(gateway.Close)
			first := t114HTTPBudgetRequest(t, gateway.URL, key.RawKey)
			_, _ = io.ReadAll(first.Body)
			first.Body.Close()
			if first.StatusCode != test.wantStatus {
				t.Fatalf("post-start status = %d, want %d", first.StatusCode, test.wantStatus)
			}
			if budget.Len() != 1 {
				t.Fatalf("post-start budget states = %d, want conservative spend", budget.Len())
			}
			second := t114HTTPBudgetRequest(t, gateway.URL, key.RawKey)
			secondBody, _ := io.ReadAll(second.Body)
			second.Body.Close()
			if second.StatusCode != http.StatusTooManyRequests || !bytes.Contains(secondBody, []byte(`"code":"budget_exceeded"`)) {
				t.Fatalf("conservative reuse status = %d/%q", second.StatusCode, secondBody)
			}
		})
	}
}

func TestT114CustomDispatchConservativelySettlesBudget(t *testing.T) {
	key, authenticator := t114BudgetKey(t, `{"budget_limits":[{"amount_micros":1,"period":"total"}]}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(upstream.Close)
	budget := limiter.NewBudgetLimiter()
	proxy := newProxyHandlerWithLimitersAndTokenConfig(
		transport.NewClient(), upstream.URL, "upstream-secret",
		limiter.NewRequestLimiter(nil), limiter.NewConcurrencyLimiter(),
		TokenAdmissionConfig{FallbackUnknownInputTokens: 1, FallbackMaxOutputTokens: 1, PricingResolver: t114Pricing(t), BudgetLimiter: budget},
	)
	proxy.responseDispatch = func(response http.ResponseWriter, upstreamResponse *http.Response, _ *openai.RequestMetadata) {
		response.WriteHeader(upstreamResponse.StatusCode)
	}
	gateway := httptest.NewServer(withGatewayAuthentication(authenticator, proxy))
	t.Cleanup(gateway.Close)
	first := t114HTTPBudgetRequest(t, gateway.URL, key.RawKey)
	first.Body.Close()
	if first.StatusCode != http.StatusAccepted || budget.Len() != 1 {
		t.Fatalf("custom dispatch = %d, budget states = %d; want accepted and conservative spend", first.StatusCode, budget.Len())
	}
	second := t114HTTPBudgetRequest(t, gateway.URL, key.RawKey)
	secondBody, _ := io.ReadAll(second.Body)
	second.Body.Close()
	if second.StatusCode != http.StatusTooManyRequests || !bytes.Contains(secondBody, []byte(`"code":"budget_exceeded"`)) {
		t.Fatalf("custom dispatch conservative admission = %d/%q", second.StatusCode, secondBody)
	}
}

func TestT114BudgetCleanupConcurrentAndKeyIsolated(t *testing.T) {
	budget := limiter.NewBudgetLimiter()
	coordinator := limiter.NewResourceLeaseCoordinator(nil, nil, budget)
	policy := limiter.LimitedBudgetPolicy(t114Money(t, 64))
	leases := make([]*limiter.ResourceLease, 0, 64)
	for index := 0; index < 64; index++ {
		key := "t114-a"
		if index%2 != 0 {
			key = "t114-b"
		}
		lease, rejection := coordinator.Acquire(limiter.ResourceLeaseOptions{KeyID: key, BudgetPolicy: policy, BudgetCandidate: t114Money(t, 1)})
		if rejection != nil || lease == nil {
			t.Fatalf("acquire %d = %v", index, rejection)
		}
		leases = append(leases, lease)
	}
	var wait sync.WaitGroup
	for index, lease := range leases {
		wait.Add(1)
		go func(index int, lease *limiter.ResourceLease) {
			defer wait.Done()
			for attempt := 0; attempt < 8; attempt++ {
				if index%2 == 0 {
					_ = lease.CompleteConservative()
				} else {
					_ = lease.ReleaseBeforeUpstream()
				}
			}
		}(index, lease)
	}
	wait.Wait()
	if budget.Len() != 1 {
		t.Fatalf("terminal budget states = %d, want one committed key", budget.Len())
	}
	other, err := budget.Reserve("t114-b", policy, t114Money(t, 1))
	if err != nil {
		t.Fatalf("isolated key was charged by another key: %v", err)
	}
	other.ReleaseBeforeUpstream()
}

func TestT114CancellationCancelsUpstreamBeforeLeaseCleanup(t *testing.T) {
	key, authenticator := t114BudgetKey(t, `{"budget_limits":[{"amount_micros":1,"period":"total"}]}`)
	cancelSeen := make(chan struct{})
	var cleanupOrder atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(response, "data: fragment\n\n")
		if flusher, ok := response.(http.Flusher); ok {
			flusher.Flush()
		}
		<-request.Context().Done()
		cleanupOrder.Store(1)
		close(cancelSeen)
	}))
	t.Cleanup(upstream.Close)
	budget := limiter.NewBudgetLimiter()
	// The test-only sink waits for upstream cancellation before acknowledging
	// budget cleanup. This makes the required ordering observable rather than
	// merely inferred from the defer statement.
	budget.SetCommittedDeltaSink(func(delta limiter.CommittedBudgetDelta) {
		if delta.Delta == 0 {
			return
		}
		select {
		case <-cancelSeen:
			cleanupOrder.Store(2)
		case <-time.After(time.Second):
			cleanupOrder.Store(3)
		}
	})
	concurrency := limiter.NewConcurrencyLimiter()
	gateway := httptest.NewServer(NewHandlerWithAuthenticatorAndLimitersAndTokenConfig(
		transport.NewClient(), upstream.URL, "upstream-secret", authenticator,
		limiter.NewRequestLimiter(nil), concurrency, nil,
		TokenAdmissionConfig{FallbackUnknownInputTokens: 1, FallbackMaxOutputTokens: 1, PricingResolver: t114Pricing(t), BudgetLimiter: budget},
	))
	t.Cleanup(gateway.Close)
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{"model":"test","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+key.RawKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	fragment := make([]byte, len("data: fragment\n\n"))
	if _, err := io.ReadFull(response.Body, fragment); err != nil {
		t.Fatal(err)
	}
	cancel()
	response.Body.Close()
	select {
	case <-cancelSeen:
	case <-time.After(time.Second):
		t.Fatal("upstream did not observe cancellation")
	}
	deadline := time.Now().Add(time.Second)
	for concurrency.Len() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if concurrency.Len() != 0 {
		t.Fatal("cancellation retained concurrency")
	}
	if budget.Len() != 1 {
		t.Fatal("cancellation failed to retain conservative budget")
	}
	deadline = time.Now().Add(time.Second)
	for cleanupOrder.Load() == 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := cleanupOrder.Load(); got != 2 {
		t.Fatalf("cleanup order = %d, want upstream cancellation before budget settlement", got)
	}
}

func TestT114CompletionLogCarriesOnlyTypedTerminalMetadata(t *testing.T) {
	var logs bytes.Buffer
	logger := NewCompletionLogger(slog.New(slog.NewJSONHandler(&logs, nil)), 4)
	request := httptest.NewRequest(http.MethodGet, "http://gateway.test/v1/models", nil)
	recorder := httptest.NewRecorder()
	withCompletionLogger(logger, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		state := terminalMetadataFromContext(request.Context())
		state.set(TerminalMetadata{Outcome: TerminalOutcomePreUpstream})
		response.WriteHeader(http.StatusBadRequest)
	})).ServeHTTP(recorder, request)
	shutdownCompletionLogger(t, logger)
	text := logs.String()
	if !strings.Contains(text, `"terminal_outcome":"pre_upstream"`) || !strings.Contains(text, `"upstream_started":false`) {
		t.Fatalf("terminal metadata missing: %s", text)
	}
	for _, forbidden := range []string{"reservation", "price", "usage", "Authorization"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("completion log leaked %q: %s", forbidden, text)
		}
	}
}

func t114HTTPBudgetRequest(t *testing.T, gatewayURL, rawKey string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, gatewayURL+"/v1/chat/completions", strings.NewReader(`{"model":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+rawKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func t114BudgetKey(t *testing.T, policy string) (auth.GeneratedGatewayKey, *auth.Authenticator) {
	t.Helper()
	return t110Key(t, policy)
}

func t114Pricing(t *testing.T) accounting.PricingResolver {
	t.Helper()
	return accounting.NewPricingResolver(t112Pricing(t))
}

func t114Money(t *testing.T, micros int64) accounting.Money {
	t.Helper()
	money, err := accounting.NewMoneyMicros(micros)
	if err != nil {
		t.Fatal(err)
	}
	return money
}
