package httpserver

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/accounting"
	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/config"
	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/transport"
	"gopkg.in/yaml.v3"
)

func TestT130BodyCaptureBoundIsIndependentOfOtherBounds(t *testing.T) {
	base := TokenAdmissionConfig{MaxInspectedRequestBytes: 1234, MaxObservedResponseBytes: 5678}
	for _, bodyLimit := range []int64{0, 1, config.MaxMaxCapturedBodyBytes} {
		if base.MaxInspectedRequestBytes != 1234 || base.MaxObservedResponseBytes != 5678 {
			t.Fatalf("body capture limit %d changed request/observation bounds", bodyLimit)
		}
		if bodyLimit > config.MaxMaxCapturedBodyBytes {
			t.Fatal("invalid test body bound")
		}
	}
	if DefaultUsageObservationMaxBytes == config.MaxMaxCapturedBodyBytes {
		t.Fatal("test requires distinct body capture and usage observation bounds")
	}
}

func TestT127UnrestrictedKnownGenerationJSONResolvesTelemetryPricing(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		rule      string
		wantCost  int64
		costKnown bool
	}{
		{name: "exact", model: "priced-exact", rule: "priced-exact", wantCost: 5, costKnown: true},
		{name: "glob", model: "priced-glob", rule: "priced-*", wantCost: 5, costKnown: true},
		{name: "zero", model: "free", rule: "free", wantCost: 0, costKnown: true},
		{name: "unknown", model: "unpriced", rule: "other", costKnown: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var pricing config.PricingConfig
			if err := yaml.Unmarshal([]byte("rules:\n  - model: "+test.rule+"\n    input_per_million_micros: "+rateForReview(test.name)+"\n    output_per_million_micros: "+rateForReview(test.name)+"\n"), &pricing); err != nil {
				t.Fatal(err)
			}
			resolver := accounting.NewPricingResolver(pricing)
			if got := resolver.Resolve(test.model).Known(); got != (test.name != "unknown") {
				t.Fatalf("pricing resolution known = %v, want %v", got, test.name != "unknown")
			}
			key, authenticator := reviewAuthenticator(t)
			upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(response, `{"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`)
			}))
			t.Cleanup(upstream.Close)
			records := make(chan slog.Record, 1)
			logger := NewCompletionLogger(slog.New(&completionRecordHandler{records: records}), 1)
			worker := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: 1})
			t.Cleanup(func() {
				shutdownObservationWorker(t, worker)
				shutdownCompletionLogger(t, logger)
			})
			handler := NewHandlerWithAuthenticatorAndLimitersAndTokenLimiterAndUsageObservationWorker(
				transport.NewClient(), upstream.URL, "upstream", authenticator, limiter.NewRequestLimiter(nil), limiter.NewConcurrencyLimiter(), logger, nil,
				TokenAdmissionConfig{PricingResolver: resolver}, worker)
			gateway := httptest.NewServer(handler)
			t.Cleanup(gateway.Close)
			request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", bytes.NewBufferString(`{"model":"`+test.model+`","stream":false}`))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+key.RawKey)
			request.Header.Set("Content-Type", "application/json")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", response.StatusCode)
			}
			waitForObservationCount(t, worker, 1)
			attrs := receiveReviewRecord(t, records)
			value, known := attrs["cost_micros"]
			if known != test.costKnown {
				t.Fatalf("cost presence = %v, want %v; attrs=%v", known, test.costKnown, attrs)
			}
			if known && value != test.wantCost {
				t.Fatalf("cost = %d, want %d", value, test.wantCost)
			}
		})
	}
}

func TestT127UnrestrictedKnownGenerationSSEResolvesTelemetryPricing(t *testing.T) {
	var pricing config.PricingConfig
	if err := yaml.Unmarshal([]byte("rules:\n  - model: stream-*\n    input_per_million_micros: 1000000\n    output_per_million_micros: 1000000\n"), &pricing); err != nil {
		t.Fatal(err)
	}
	key, authenticator := reviewAuthenticator(t)
	const body = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\ndata: {\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3,\"total_tokens\":5}}\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(response, body)
	}))
	t.Cleanup(upstream.Close)
	records := make(chan slog.Record, 1)
	logger := NewCompletionLogger(slog.New(&completionRecordHandler{records: records}), 1)
	worker := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: 1})
	t.Cleanup(func() {
		shutdownObservationWorker(t, worker)
		shutdownCompletionLogger(t, logger)
	})
	handler := NewHandlerWithAuthenticatorAndLimitersAndTokenLimiterAndUsageObservationWorker(
		transport.NewClient(), upstream.URL, "upstream", authenticator, limiter.NewRequestLimiter(nil), limiter.NewConcurrencyLimiter(), logger, nil,
		TokenAdmissionConfig{PricingResolver: accounting.NewPricingResolver(pricing)}, worker)
	gateway := httptest.NewServer(handler)
	t.Cleanup(gateway.Close)
	request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", bytes.NewBufferString(`{"model":"stream-model","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+key.RawKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || string(got) != body {
		t.Fatalf("SSE response = %d/%q, want 200/original body", response.StatusCode, got)
	}
	waitForObservationCount(t, worker, 1)
	attrs := receiveReviewRecord(t, records)
	if attrs["cost_micros"] != 5 {
		t.Fatalf("SSE cost = %d, want 5; attrs=%v", attrs["cost_micros"], attrs)
	}
}

func reviewAuthenticator(t *testing.T) (auth.GeneratedGatewayKey, *auth.Authenticator) {
	t.Helper()
	pepper := []byte("review-pepper")
	key, err := auth.GenerateGatewayKey(pepper)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := auth.NewAuthenticator(pepper, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := authenticator.Load([]auth.Record{{ID: "review-key", DisplayPrefix: key.DisplayPrefix, Digest: key.Digest, Enabled: true, PolicyJSON: []byte(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	return key, authenticator
}

func receiveReviewRecord(t *testing.T, records <-chan slog.Record) map[string]int64 {
	t.Helper()
	select {
	case record := <-records:
		attrs := make(map[string]int64)
		record.Attrs(func(attribute slog.Attr) bool {
			if value, ok := attribute.Value.Any().(int64); ok {
				attrs[attribute.Key] = value
			}
			return true
		})
		return attrs
	case <-time.After(time.Second):
		t.Fatal("completion record was not emitted")
		return nil
	}
}

func rateForReview(name string) string {
	if name == "zero" {
		return "0"
	}
	return "1000000"
}
