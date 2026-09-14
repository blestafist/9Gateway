package httpserver

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/common/expfmt"
)

func TestMetricsExposition(t *testing.T) {
	metrics := newGatewayMetrics()
	trace := NewRequestTraceState("0123456789abcdef0123456789abcdef")
	trace.SetRouteMetadata("POST", "/v1/chat/completions", RouteClassChatCompletions)
	if !trace.SetRequestMetadata("POST", RouteClassChatCompletions, "", RequestModeJSON) {
		t.Fatal("request metadata")
	}
	trace.SetUpstreamStart()
	trace.SetUpstreamHeaders(200)
	trace.SetDownstreamStatus(200)
	trace.SetFirstDownstreamByte()
	trace.SetTerminalOutcome(TerminalOutcomeComplete)
	trace.Complete()
	trace.setMetrics(metrics)
	if record, err := trace.Final(); err == nil {
		metrics.observe(record)
	} else {
		t.Fatalf("final metrics err: %v", err)
	}
	handler := withMetrics(metrics, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	server := httptest.NewServer(handler)
	defer server.Close()
	response, err := http.Get(server.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, family := range []string{"gateway_requests_total", "gateway_request_errors_total", "gateway_upstream_requests_total", "gateway_telemetry_jobs_total", "gateway_active_requests", "gateway_telemetry_queue_depth", "gateway_request_duration_seconds", "gateway_upstream_duration_seconds", "gateway_ttfb_seconds"} {
		if !strings.Contains(text, "# TYPE "+family) {
			t.Errorf("missing family %s", family)
		}
	}
	if !strings.Contains(text, `gateway_requests_total{route="chat_completions",method="POST",status="200",outcome="complete"} 1`) {
		t.Errorf("request series missing: %s", text)
	}
	if !strings.Contains(text, `gateway_request_duration_seconds_bucket{route="chat_completions",method="POST",status="200",outcome="complete",le="+Inf"} 1`) {
		t.Error("histogram +Inf missing")
	}
	if !strings.Contains(text, "gateway_request_duration_seconds_count") || !strings.Contains(text, "gateway_request_duration_seconds_sum") {
		t.Error("histogram count/sum missing")
	}
	if strings.Contains(text, "0123456789abcdef") {
		t.Error("request id leaked")
	}
}

func TestMetricsActiveGaugeCountsBlockedRequestOnce(t *testing.T) {
	metrics := newGatewayMetrics()
	entered := make(chan struct{})
	release := make(chan struct{})
	handler := withMetrics(metrics, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	server := httptest.NewServer(handler)
	defer server.Close()
	response := make(chan *http.Response, 1)
	go func() {
		result, err := http.Get(server.URL + "/blocked")
		if err == nil {
			response <- result
		}
	}()
	<-entered
	if got := metrics.active.Load(); got != 1 {
		t.Fatalf("active while blocked = %d, want 1", got)
	}
	close(release)
	result := <-response
	if result != nil {
		result.Body.Close()
	}
	deadline := time.Now().Add(time.Second)
	for metrics.active.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := metrics.active.Load(); got != 0 {
		t.Fatalf("active after request = %d, want 0", got)
	}
}

func TestMetricsParserSecretFreeAndConcurrentScrape(t *testing.T) {
	metrics := newGatewayMetrics()
	metrics.telemetryResult("persisted")
	metrics.telemetryResult("dropped")
	trace := NewRequestTraceState("0123456789abcdef0123456789abcdef")
	trace.SetRouteMetadata("POST", "/v1/chat/completions", RouteClassChatCompletions)
	trace.SetRequestMetadata("POST", RouteClassChatCompletions, "", RequestModeJSON)
	trace.SetUpstreamStart()
	trace.SetUpstreamHeaders(200)
	trace.SetDownstreamStatus(200)
	trace.SetFirstDownstreamByte()
	trace.SetTerminalOutcome(TerminalOutcomeComplete)
	trace.Complete()
	record, err := trace.Final()
	if err != nil {
		t.Fatal(err)
	}
	metrics.observe(record)
	handler := withMetrics(metrics, http.NotFoundHandler())
	server := httptest.NewServer(handler)
	defer server.Close()
	var wait sync.WaitGroup
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for j := 0; j < 20; j++ {
				metrics.telemetryResult("failed")
				response, getErr := http.Get(server.URL + "/metrics")
				if getErr == nil {
					_, _ = io.Copy(io.Discard, response.Body)
					response.Body.Close()
				}
			}
		}()
	}
	wait.Wait()
	response, err := http.Get(server.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	families, err := (&expfmt.TextParser{}).TextToMetricFamilies(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("parse metrics: %v\n%s", err, body)
	}
	for _, name := range []string{"gateway_requests_total", "gateway_telemetry_jobs_total", "gateway_request_duration_seconds"} {
		if _, ok := families[name]; !ok {
			t.Fatalf("missing family %s", name)
		}
	}
	text := string(body)
	for _, secret := range []string{"0123456789abcdef", "hidden", "secret", "model", "Bearer"} {
		if strings.Contains(text, secret) {
			t.Fatalf("secret leaked in metrics: %q", secret)
		}
	}
	if !strings.Contains(text, `gateway_telemetry_jobs_total{result="dropped"} 1`) {
		t.Fatalf("dropped metric missing: %s", text)
	}
}

func TestHistogramConcurrentScrapeBucketsShareCountSnapshot(t *testing.T) {
	hist := &metricHistogram{}
	const observers = 8
	const observations = 500
	var wait sync.WaitGroup
	wait.Add(observers)
	for i := 0; i < observers; i++ {
		go func() {
			defer wait.Done()
			for j := 0; j < observations; j++ {
				hist.observe(float64(j%20) / 100)
			}
		}()
	}

	// Scrapes deliberately overlap bucket publication. Every exposition must
	// remain a valid cumulative histogram even when observations are in flight.
	for i := 0; i < observers*observations; i++ {
		var builder strings.Builder
		writeHistogramSamples(&builder, "test_histogram", hist, "")
		var previous uint64
		var count, inf uint64
		buckets := make([]uint64, 0, len(metricBuckets))
		for _, line := range strings.Split(builder.String(), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 {
				continue
			}
			switch {
			case strings.HasSuffix(fields[0], `_bucket{le="+Inf"}`):
				value, err := strconv.ParseUint(fields[1], 10, 64)
				if err != nil {
					t.Fatalf("parse +Inf sample %q: %v", line, err)
				}
				inf = value
			case strings.HasSuffix(fields[0], `_count{}`):
				value, err := strconv.ParseUint(fields[1], 10, 64)
				if err != nil {
					t.Fatalf("parse count sample %q: %v", line, err)
				}
				count = value
			case strings.Contains(fields[0], `_bucket{le="`):
				value, err := strconv.ParseUint(fields[1], 10, 64)
				if err != nil {
					t.Fatalf("parse bucket sample %q: %v", line, err)
				}
				if value < previous {
					t.Fatalf("non-cumulative buckets: %s", builder.String())
				}
				buckets = append(buckets, value)
				previous = value
			}
		}
		for _, value := range buckets {
			if value > count {
				t.Fatalf("bucket exceeded count: %s", builder.String())
			}
		}
		if inf != count {
			t.Fatalf("+Inf %d differs from count %d: %s", inf, count, builder.String())
		}
	}
	wait.Wait()
	if got, want := hist.count.Load(), uint64(observers*observations); got != want {
		t.Fatalf("count = %d, want %d", got, want)
	}
}

func TestMetricsQueueRegistrationLifecycle(t *testing.T) {
	metrics := newGatewayMetrics()
	worker := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: 1})
	worker.setMetrics(metrics)
	if len(metrics.queueSources) != 1 {
		t.Fatalf("queue sources = %d", len(metrics.queueSources))
	}
	other := newGatewayMetrics()
	worker.setMetrics(other)
	if len(metrics.queueSources) != 0 || len(other.queueSources) != 1 {
		t.Fatalf("registration = %d/%d", len(metrics.queueSources), len(other.queueSources))
	}
	_ = worker.Shutdown(context.Background())
}

func TestTelemetryDropMetricsMatchLocalCounts(t *testing.T) {
	metrics := newGatewayMetrics()
	worker := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: 1})
	worker.setMetrics(metrics)
	worker.markDropped()
	worker.markDropped()
	if got := worker.Dropped(); got != 2 {
		t.Fatalf("worker dropped = %d", got)
	}
	logger := NewCompletionLogger(nil, 1)
	logger.setMetrics(metrics)
	_ = logger.Shutdown(context.Background())
	if logger.Enqueue(CompletionRecord{}) {
		t.Fatal("enqueue after shutdown succeeded")
	}
	if got := logger.Dropped(); got != 1 {
		t.Fatalf("logger dropped = %d", got)
	}
	text := metricsText(t, metrics)
	if !strings.Contains(text, `gateway_telemetry_jobs_total{result="dropped"} 3`) {
		t.Fatalf("dropped metric did not match local counts: %s", text)
	}
}

func TestMetricsWiredThroughHandlerConstruction(t *testing.T) {
	worker := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: 2})
	handler := NewHandlerWithAuthenticatorAndLimitersAndTokenLimiterAndUsageObservationWorkerAndHistory(
		http.DefaultClient, "http://127.0.0.1:1", "upstream-secret", nil,
		nil, nil, nil, nil, TokenAdmissionConfig{}, worker, nil,
	)
	server := httptest.NewServer(handler)
	defer server.Close()
	response, err := http.Get(server.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("rejection status = %d", response.StatusCode)
	}
	response, err = http.Get(server.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&expfmt.TextParser{}).TextToMetricFamilies(bytes.NewReader(body)); err != nil {
		t.Fatalf("parse metrics: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, `gateway_telemetry_queue_depth 0`) {
		t.Fatalf("worker queue was not registered: %s", text)
	}
	if strings.Contains(text, "upstream-secret") {
		t.Fatal("upstream secret leaked")
	}
	_ = worker.Shutdown(context.Background())
}

func metricsText(t *testing.T, metrics *gatewayMetrics) string {
	t.Helper()
	response := httptest.NewRecorder()
	serveMetrics(response, metrics)
	return response.Body.String()
}
