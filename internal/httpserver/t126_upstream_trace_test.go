package httpserver

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pestit/9gateway/internal/transport"
)

func TestT126TraceUsesUpstreamHeadersAndDispatchMode(t *testing.T) {
	const convertedSSE = "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/json":
			response.Header().Set("Content-Type", "application/json")
			response.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(response, `{"ok":true}`)
		case "/v1/opaque":
			response.Header().Set("Content-Type", "application/octet-stream")
			response.WriteHeader(http.StatusTeapot)
			_, _ = io.WriteString(response, "opaque")
		case "/v1/sse":
			response.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(response, "data: ok\n\n")
		case "/v1/malformed":
			response.Header().Set("Content-Type", "application/json; charset")
			_, _ = io.WriteString(response, `{"ok":true}`)
		case "/v1/repeated":
			response.Header().Add("Content-Type", "text/event-stream")
			response.Header().Add("Content-Type", "application/json")
			_, _ = io.WriteString(response, "data: ok\n\n")
		case "/v1/chat/completions":
			response.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(response, convertedSSE)
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(upstream.Close)

	var trace *RequestTraceState
	proxy := route(newProxyHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	handler := withRequestID(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		trace = TraceFromContext(request.Context())
		proxy.ServeHTTP(response, request)
	}))
	gateway := httptest.NewServer(handler)
	t.Cleanup(gateway.Close)

	for _, test := range []struct {
		name          string
		path          string
		body          string
		wantStatus    int
		wantUpstream  ResponseMode
		wantDelivered ResponseMode
		wantRequest   RequestMode
	}{
		{name: "json", path: "/v1/json", wantStatus: http.StatusCreated, wantUpstream: ResponseModeJSON, wantDelivered: ResponseModeJSON},
		{name: "opaque", path: "/v1/opaque", wantStatus: http.StatusTeapot, wantUpstream: ResponseModeOpaque, wantDelivered: ResponseModeOpaque},
		{name: "sse", path: "/v1/sse", wantStatus: http.StatusOK, wantUpstream: ResponseModeSSE, wantDelivered: ResponseModeSSE},
		{name: "malformed content type", path: "/v1/malformed", wantStatus: http.StatusOK, wantDelivered: ResponseModeOpaque},
		{name: "repeated content type", path: "/v1/repeated", wantStatus: http.StatusOK, wantDelivered: ResponseModeOpaque},
		{name: "converted SSE", path: "/v1/chat/completions", body: `{"model":"test","stream":false}`, wantStatus: http.StatusOK, wantUpstream: ResponseModeSSE, wantDelivered: ResponseModeJSON, wantRequest: RequestModeJSON},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requestBody io.Reader
			if test.body != "" {
				requestBody = bytes.NewBufferString(test.body)
			}
			request, err := http.NewRequest(http.MethodPost, gateway.URL+test.path, requestBody)
			if err != nil {
				t.Fatal(err)
			}
			if test.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.wantStatus)
			}
			record, err := trace.FreezeBase()
			if err != nil {
				t.Fatal(err)
			}
			if record.UpstreamMode != test.wantUpstream || record.DeliveredMode != test.wantDelivered || record.RequestedMode != test.wantRequest {
				t.Fatalf("modes = requested %q, upstream %q, delivered %q", record.RequestedMode, record.UpstreamMode, record.DeliveredMode)
			}
			if !record.UpstreamStatus.Known() || !record.DownstreamStatus.Known() {
				t.Fatal("upstream/downstream status was not recorded independently")
			}
		})
	}
}

func TestT126ConnectionFailureLeavesActualModeAndHeaderLatencyUnknown(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	baseURL := upstream.URL
	upstream.Close()

	var trace *RequestTraceState
	handler := withRequestID(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		trace = TraceFromContext(request.Context())
		route(newProxyHandler(transport.NewClient(), baseURL, "upstream-secret")).ServeHTTP(response, request)
	}))
	gateway := httptest.NewServer(handler)
	t.Cleanup(gateway.Close)
	response, err := http.Get(gateway.URL + "/v1/failure")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	record, err := trace.FreezeBase()
	if err != nil {
		t.Fatal(err)
	}
	if record.UpstreamMode != ResponseModeUnknown || record.DeliveredMode != ResponseModeUnknown {
		t.Fatalf("failed request modes = upstream %q, delivered %q", record.UpstreamMode, record.DeliveredMode)
	}
	if record.UpstreamStatus.Known() || record.Timing.UpstreamHeadersAt.Known() || record.Timing.TimeToUpstreamHeaders.Known() {
		t.Fatal("failed request incorrectly recorded upstream headers")
	}
}
