package httpserver

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/accounting"
	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/config"
	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/protocol/openai"
	"github.com/pestit/9gateway/internal/transport"
	"gopkg.in/yaml.v3"
)

func TestT130BodyCaptureBoundIsIndependentOfOtherBounds(t *testing.T) {
	const pricingModel = "review-priced-model"
	var baseline struct {
		tokenizer TokenAdmissionConfig
		worker    struct {
			capacity int
			maxBytes int64
		}
		pricing   accounting.PricingResolution
		transport time.Duration
	}
	for _, bodyLimit := range []int64{0, 1, config.MaxMaxCapturedBodyBytes} {
		t.Run("max_captured_body_bytes="+formatReviewInt(bodyLimit), func(t *testing.T) {
			loaded := loadReviewConfig(t, bodyLimit)
			resolver := accounting.NewPricingResolver(loaded.Pricing)
			wired := TokenAdmissionConfig{
				MaxInspectedRequestBytes:   loaded.Tokenizer.MaxInspectedRequestBytes,
				FallbackUnknownInputTokens: loaded.Tokenizer.FallbackUnknownInputTokens,
				FallbackMaxOutputTokens:    loaded.Tokenizer.FallbackMaxOutputTokens,
				MaxObservedResponseBytes:   DefaultUsageObservationMaxBytes,
				PricingResolver:            resolver,
			}
			client := transport.NewClient()
			proxy := newProxyHandlerWithLimitersAndTokenConfig(client, "http://router.example.test", "upstream", nil, nil, wired)
			worker := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: loaded.Observability.TelemetryQueueCapacity})
			t.Cleanup(func() { shutdownObservationWorker(t, worker) })
			if bodyLimit == config.DefaultMaxCapturedBodyBytes {
				baseline.tokenizer = proxy.tokenConfig
				baseline.worker.capacity = cap(worker.queue)
				baseline.worker.maxBytes = worker.maxBytes
				baseline.pricing = resolver.Resolve(pricingModel)
				baseline.transport = client.Transport.(*http.Transport).ResponseHeaderTimeout
				return
			}
			if got := proxy.tokenConfig; got.MaxInspectedRequestBytes != baseline.tokenizer.MaxInspectedRequestBytes ||
				got.FallbackUnknownInputTokens != baseline.tokenizer.FallbackUnknownInputTokens ||
				got.FallbackMaxOutputTokens != baseline.tokenizer.FallbackMaxOutputTokens ||
				got.MaxObservedResponseBytes != baseline.tokenizer.MaxObservedResponseBytes {
				t.Fatalf("body limit %d changed wired bounds: got %+v, baseline %+v", bodyLimit, got, baseline.tokenizer)
			}
			if cap(worker.queue) != baseline.worker.capacity || worker.maxBytes != baseline.worker.maxBytes {
				t.Fatalf("body limit %d changed observation bounds: got capacity=%d/max=%d, baseline capacity=%d/max=%d", bodyLimit, cap(worker.queue), worker.maxBytes, baseline.worker.capacity, baseline.worker.maxBytes)
			}
			gotPricing := resolver.Resolve(pricingModel)
			if gotPricing.Known() != baseline.pricing.Known() || gotPricing.Rule().InputPerMillionMicros() != baseline.pricing.Rule().InputPerMillionMicros() || gotPricing.Rule().OutputPerMillionMicros() != baseline.pricing.Rule().OutputPerMillionMicros() {
				t.Fatalf("body limit %d changed pricing resolution: got %+v, baseline %+v", bodyLimit, gotPricing, baseline.pricing)
			}
			if got := client.Transport.(*http.Transport).ResponseHeaderTimeout; got != baseline.transport {
				t.Fatalf("body limit %d changed transport timeout: got %s, baseline %s", bodyLimit, got, baseline.transport)
			}
		})
	}
}

func TestTelemetryRequestBodyCapturesCompletedUpload(t *testing.T) {
	data := []byte(`{"model":"priced"}`)
	body := newTelemetryRequestBody(io.NopCloser(bytes.NewReader(data)), int64(len(data)), 1024)
	got, err := io.ReadAll(body)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("read = %q, %v", got, err)
	}
	snapshot, ok := body.snapshot()
	if !ok || !bytes.Equal(snapshot, data) {
		t.Fatalf("snapshot = %q, %v; want complete body", snapshot, ok)
	}
}

func TestPricingOnlyCaptureStartsUpstreamBeforeChunkedUploadEOF(t *testing.T) {
	key, authenticator := reviewAuthenticator(t)
	var pricing config.PricingConfig
	if err := yaml.Unmarshal([]byte("rules:\n  - model: priced\n    input_per_million_micros: 1000000\n    output_per_million_micros: 1000000\n"), &pricing); err != nil {
		t.Fatal(err)
	}
	const firstChunk = `{"model":"priced",`
	const lastChunk = `"stream":false}`
	started := make(chan struct{})
	release := make(chan struct{})
	uploaded := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		close(started)
		body, err := io.ReadAll(request.Body)
		if err != nil {
			http.Error(response, err.Error(), http.StatusBadRequest)
			return
		}
		uploaded <- string(body)
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
	handler := NewHandlerWithAuthenticatorAndLimitersAndTokenConfigAndUsageObservationWorker(
		transport.NewClient(), upstream.URL, "upstream", authenticator, limiter.NewRequestLimiter(nil), limiter.NewConcurrencyLimiter(), logger,
		TokenAdmissionConfig{PricingResolver: accounting.NewPricingResolver(pricing)}, worker)
	gateway := httptest.NewServer(handler)
	t.Cleanup(gateway.Close)
	body := &reviewChunkedBody{chunks: [][]byte{[]byte(firstChunk), []byte(lastChunk)}, release: release}
	request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+key.RawKey)
	request.Header.Set("Content-Type", "application/json")
	responseCh := make(chan *http.Response, 1)
	errorCh := make(chan error, 1)
	go func() {
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			errorCh <- requestErr
			return
		}
		responseCh <- response
	}()
	select {
	case <-started:
		// The gateway must hand headers and the first body chunk upstream without
		// waiting for the client upload's EOF.
	case err := <-errorCh:
		t.Fatalf("request before upstream start: %v", err)
	case <-time.After(time.Second):
		t.Fatal("upstream did not start before upload EOF")
	}
	close(release)
	var response *http.Response
	select {
	case response = <-responseCh:
	case err := <-errorCh:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("gateway did not finish after upload release")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	select {
	case got := <-uploaded:
		if got != firstChunk+lastChunk {
			t.Fatalf("upstream body = %q, want %q", got, firstChunk+lastChunk)
		}
	case <-time.After(time.Second):
		t.Fatal("upstream upload was not observed")
	}
	waitForObservationCount(t, worker, 1)
	attrs := receiveReviewRecord(t, records)
	if attrs["cost_micros"] != 5 {
		t.Fatalf("cost = %d, want 5; attrs=%v", attrs["cost_micros"], attrs)
	}
}

func TestTelemetryRequestBodyDoesNotPublishMalformedOversizedOrPartialUpload(t *testing.T) {
	tests := []struct {
		name     string
		body     []byte
		expected int64
		limit    int64
	}{
		{name: "malformed", body: []byte(`{"model":`), expected: 9, limit: 1024},
		{name: "oversized", body: []byte(`{"model":"too-large"}`), expected: 22, limit: 4},
		{name: "partial", body: []byte(`{"model":"partial"}`), expected: 100, limit: 1024},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := newTelemetryRequestBody(io.NopCloser(bytes.NewReader(test.body)), test.expected, test.limit)
			if _, err := io.Copy(io.Discard, body); err != nil {
				t.Fatal(err)
			}
			snapshot, ok := body.snapshot()
			if test.name == "malformed" {
				if !ok || !bytes.Equal(snapshot, test.body) {
					t.Fatalf("malformed snapshot = %q, %v; want captured bytes", snapshot, ok)
				}
				if _, err := openai.ParseRequestMetadata(snapshot); err == nil {
					t.Fatal("malformed request unexpectedly parsed")
				}
				return
			}
			if ok || snapshot != nil {
				t.Fatalf("snapshot = %q, %v; want unavailable", snapshot, ok)
			}
		})
	}
}

type reviewChunkedBody struct {
	chunks  [][]byte
	release <-chan struct{}
	index   int
}

func (body *reviewChunkedBody) Read(destination []byte) (int, error) {
	if body.index >= len(body.chunks) {
		return 0, io.EOF
	}
	if body.index == 1 {
		<-body.release
	}
	chunk := body.chunks[body.index]
	body.index++
	return copy(destination, chunk), nil
}

func (body *reviewChunkedBody) Close() error { return nil }

func loadReviewConfig(t *testing.T, bodyLimit int64) config.Config {
	t.Helper()
	t.Setenv("T130_AUTH_PEPPER", "review-pepper")
	t.Setenv("T130_ADMIN_CREDENTIAL", "review-admin")
	contents := "listen_addr: :8080\n" +
		"upstream_base_url: http://router.example.test\n" +
		"upstream_api_key: upstream\n" +
		"sqlite_path: ':memory:'\n" +
		"auth_pepper: ${T130_AUTH_PEPPER}\n" +
		"admin_credential: ${T130_ADMIN_CREDENTIAL}\n" +
		"tokenizer:\n" +
		"  mode: usage_only\n" +
		"  max_inspected_request_bytes: 1234\n" +
		"  fallback_unknown_input_tokens: 17\n" +
		"  fallback_max_output_tokens: 19\n" +
		"observability:\n" +
		"  max_captured_body_bytes: " + formatReviewInt(bodyLimit) + "\n" +
		"pricing:\n" +
		"  rules:\n" +
		"    - model: review-priced-model\n" +
		"      input_per_million_micros: 7\n" +
		"      output_per_million_micros: 11\n"
	path := filepath.Join(t.TempDir(), "gateway.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Observability.MaxCapturedBodyBytes != bodyLimit {
		t.Fatalf("loaded body limit = %d, want %d", loaded.Observability.MaxCapturedBodyBytes, bodyLimit)
	}
	return loaded
}

func formatReviewInt(value int64) string {
	return strconv.FormatInt(value, 10)
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
