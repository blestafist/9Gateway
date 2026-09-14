package httpserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsExposition(t *testing.T) {
	metrics := newGatewayMetrics()
	trace := NewRequestTraceState("0123456789abcdef0123456789abcdef")
	trace.SetRouteMetadata("POST", "/v1/chat/completions", RouteClassChatCompletions)
	trace.SetUpstreamStart()
	trace.SetUpstreamHeaders(200)
	trace.SetDownstreamStatus(200)
	trace.SetFirstDownstreamByte()
	trace.SetTerminalOutcome(TerminalOutcomeComplete)
	trace.Complete()
	trace.setMetrics(metrics)
	trace.observeMetrics(metrics)
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
