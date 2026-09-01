package httpserver

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/transport"
)

func TestNewHandlerAcceptsHTTPRequests(t *testing.T) {
	server := httptest.NewServer(NewHandler(transport.NewClient(), "http://router.example.test", "upstream-secret"))
	t.Cleanup(server.Close)

	response, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("GET %s: %v", server.URL, err)
	}
	t.Cleanup(func() { response.Body.Close() })

	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusNotFound)
	}
}

func TestHealth(t *testing.T) {
	server := httptest.NewServer(NewHandler(transport.NewClient(), "http://router.example.test", "upstream-secret"))
	t.Cleanup(server.Close)

	response, err := http.Get(server.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	t.Cleanup(func() { response.Body.Close() })

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if response.Header.Get(requestIDHeader) == "" {
		t.Fatalf("%s header is missing", requestIDHeader)
	}
}

func TestRequestIDsAreDistinct(t *testing.T) {
	server := httptest.NewServer(NewHandler(transport.NewClient(), "http://router.example.test", "upstream-secret"))
	t.Cleanup(server.Close)

	requestIDs := make([]string, 2)
	for index := range requestIDs {
		response, err := http.Get(server.URL + "/health")
		if err != nil {
			t.Fatalf("GET /health: %v", err)
		}
		requestIDs[index] = response.Header.Get(requestIDHeader)
		response.Body.Close()
	}

	if requestIDs[0] == requestIDs[1] {
		t.Fatalf("request IDs are not distinct: %q", requestIDs[0])
	}
}

func TestProxyForwardsMethodPathAndQuery(t *testing.T) {
	requests := make(chan *http.Request, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests <- request
	}))
	t.Cleanup(upstream.Close)

	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)

	request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/models?alpha=one&beta=two", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST /v1/models: %v", err)
	}
	response.Body.Close()

	select {
	case upstreamRequest := <-requests:
		if upstreamRequest.Method != request.Method {
			t.Fatalf("method = %s, want %s", upstreamRequest.Method, request.Method)
		}
		if upstreamRequest.URL.Path != request.URL.Path {
			t.Fatalf("path = %s, want %s", upstreamRequest.URL.Path, request.URL.Path)
		}
		if upstreamRequest.URL.RawQuery != request.URL.RawQuery {
			t.Fatalf("query = %s, want %s", upstreamRequest.URL.RawQuery, request.URL.RawQuery)
		}
	case <-time.After(time.Second):
		t.Fatal("upstream did not receive request")
	}
}

func TestJoinURLPath(t *testing.T) {
	for _, test := range []struct {
		name        string
		baseURL     string
		requestURL  string
		wantPath    string
		wantRawPath string
	}{
		{
			name:       "no prefix",
			baseURL:    "https://router.example",
			requestURL: "/v1/models",
			wantPath:   "/v1/models",
		},
		{
			name:       "prefix",
			baseURL:    "https://router.example/gateway",
			requestURL: "/v1/models",
			wantPath:   "/gateway/v1/models",
		},
		{
			name:       "trailing slash",
			baseURL:    "https://router.example/gateway/",
			requestURL: "/v1/models",
			wantPath:   "/gateway/v1/models",
		},
		{
			name:        "escaped slash",
			baseURL:     "https://router.example/gateway%2Ftenant",
			requestURL:  "/v1/models%2Fspecial",
			wantPath:    "/gateway/tenant/v1/models/special",
			wantRawPath: "/gateway%2Ftenant/v1/models%2Fspecial",
		},
		{
			name:        "escaped trailing slash",
			baseURL:     "https://router.example/gateway%2F",
			requestURL:  "/v1/models",
			wantPath:    "/gateway//v1/models",
			wantRawPath: "/gateway%2F/v1/models",
		},
		{
			name:       "duplicate request slash",
			baseURL:    "https://router.example/gateway",
			requestURL: "//v1//models",
			wantPath:   "/gateway//v1//models",
		},
		{
			name:       "dot-like segments",
			baseURL:    "https://router.example/gateway",
			requestURL: "/v1/../models/./current",
			wantPath:   "/gateway/v1/../models/./current",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			baseURL, err := url.Parse(test.baseURL)
			if err != nil {
				t.Fatalf("parse base URL: %v", err)
			}
			requestURL, err := url.ParseRequestURI(test.requestURL)
			if err != nil {
				t.Fatalf("parse request URL: %v", err)
			}

			gotPath, gotRawPath := joinURLPath(baseURL, requestURL)
			if gotPath != test.wantPath {
				t.Fatalf("path = %q, want %q", gotPath, test.wantPath)
			}
			if gotRawPath != test.wantRawPath {
				t.Fatalf("raw path = %q, want %q", gotRawPath, test.wantRawPath)
			}
		})
	}
}

func TestProxyJoinsConfiguredBasePathWithoutChangingAuthority(t *testing.T) {
	type requestDetails struct {
		host       string
		path       string
		requestURI string
	}
	details := make(chan requestDetails, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		details <- requestDetails{
			host:       request.Host,
			path:       request.URL.Path,
			requestURI: request.URL.RequestURI(),
		}
	}))
	t.Cleanup(upstream.Close)

	configuredURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream URL: %v", err)
	}
	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL+"/gateway", "upstream-secret"))
	t.Cleanup(gateway.Close)

	request, err := http.NewRequest(http.MethodGet, gateway.URL+"/v1//models%2Fspecial?target=//attacker.example", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("GET prefixed path: %v", err)
	}
	response.Body.Close()

	select {
	case got := <-details:
		if got.host != configuredURL.Host {
			t.Fatalf("upstream host = %q, want configured host %q", got.host, configuredURL.Host)
		}
		if got.path != "/gateway/v1//models/special" {
			t.Fatalf("upstream path = %q, want %q", got.path, "/gateway/v1//models/special")
		}
		if got.requestURI != "/gateway/v1//models%2Fspecial?target=//attacker.example" {
			t.Fatalf("upstream request URI = %q, want %q", got.requestURI, "/gateway/v1//models%2Fspecial?target=//attacker.example")
		}
	case <-time.After(time.Second):
		t.Fatal("upstream did not receive request")
	}
}

func TestProxyRewritesAuthorization(t *testing.T) {
	authorizations := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		authorizations <- request.Header.Get("Authorization")
	}))
	t.Cleanup(upstream.Close)

	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)

	request, err := http.NewRequest(http.MethodGet, gateway.URL+"/v1/models", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer client-secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	response.Body.Close()

	select {
	case authorization := <-authorizations:
		if authorization != "Bearer upstream-secret" {
			t.Fatalf("upstream Authorization = %q, want %q", authorization, "Bearer upstream-secret")
		}
	case <-time.After(time.Second):
		t.Fatal("upstream did not receive request")
	}
}

func TestProxyCopiesEndToEndHeadersAndRemovesHopByHopHeaders(t *testing.T) {
	headers := make(chan http.Header, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		headers <- request.Header.Clone()
	}))
	t.Cleanup(upstream.Close)

	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)

	request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/models", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Request-Trace", "trace-value")
	request.Header.Set("Connection", "X-Remove-Me")
	request.Header.Set("X-Remove-Me", "hop-by-hop-value")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST /v1/models: %v", err)
	}
	response.Body.Close()

	select {
	case upstreamHeaders := <-headers:
		if upstreamHeaders.Get("Content-Type") != "application/json" {
			t.Fatalf("upstream Content-Type = %q, want %q", upstreamHeaders.Get("Content-Type"), "application/json")
		}
		if upstreamHeaders.Get("X-Request-Trace") != "trace-value" {
			t.Fatalf("upstream X-Request-Trace = %q, want %q", upstreamHeaders.Get("X-Request-Trace"), "trace-value")
		}
		if upstreamHeaders.Get("Connection") != "" || upstreamHeaders.Get("X-Remove-Me") != "" {
			t.Fatal("upstream received hop-by-hop headers")
		}
	case <-time.After(time.Second):
		t.Fatal("upstream did not receive request")
	}
}

func TestProxyPreservesUpstreamResponseStatus(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(upstream.Close)

	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)

	response, err := http.Get(gateway.URL + "/v1/status")
	if err != nil {
		t.Fatalf("GET /v1/status: %v", err)
	}
	response.Body.Close()

	if response.StatusCode != http.StatusTeapot {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusTeapot)
	}
}

func TestProxyCopiesEndToEndResponseHeadersAndRemovesHopByHopHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("X-Upstream-Trace", "trace-value")
		response.Header().Set("Connection", "X-Remove-Me")
		response.Header().Set("X-Remove-Me", "hop-by-hop-value")
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)

	response, err := http.Get(gateway.URL + "/v1/status")
	if err != nil {
		t.Fatalf("GET /v1/status: %v", err)
	}
	response.Body.Close()

	if response.Header.Get("X-Upstream-Trace") != "trace-value" {
		t.Fatalf("X-Upstream-Trace = %q, want %q", response.Header.Get("X-Upstream-Trace"), "trace-value")
	}
	if response.Header.Get("Connection") != "" || response.Header.Get("X-Remove-Me") != "" {
		t.Fatal("downstream received hop-by-hop response headers")
	}
}

func TestProxyDoesNotExposeUpstreamAuthorization(t *testing.T) {
	const upstreamSecret = "upstream-secret"
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Authorization", "Bearer "+upstreamSecret)
		response.Header().Set("Proxy-Authorization", "Bearer proxy-secret")
		response.Header().Set("X-Upstream-Trace", "trace-value")
		response.Header().Set("Connection", "X-Remove-Me")
		response.Header().Set("X-Remove-Me", "hop-by-hop-value")
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	gateway := httptest.NewServer(newHandler(logger, newProxyHandler(transport.NewClient(), upstream.URL, upstreamSecret)))
	t.Cleanup(gateway.Close)

	response, err := http.Get(gateway.URL + "/v1/status")
	if err != nil {
		t.Fatalf("GET /v1/status: %v", err)
	}
	response.Body.Close()

	if response.Header.Get("Authorization") != "" || response.Header.Get("Proxy-Authorization") != "" {
		t.Fatal("downstream received upstream credential headers")
	}
	if response.Header.Get("X-Upstream-Trace") != "trace-value" {
		t.Fatalf("X-Upstream-Trace = %q, want %q", response.Header.Get("X-Upstream-Trace"), "trace-value")
	}
	if response.Header.Get("Connection") != "" || response.Header.Get("X-Remove-Me") != "" {
		t.Fatal("downstream received hop-by-hop response headers")
	}
	if strings.Contains(logs.String(), upstreamSecret) || strings.Contains(logs.String(), "Authorization") {
		t.Fatal("completion log contains upstream credential data")
	}
}

func TestProxyPassesOrdinaryResponseBodyWithoutChangingBytes(t *testing.T) {
	wantBody := []byte("{\"id\":\"response-1\",\"choices\":[{\"text\":\"keep spacing\"}]}\n")
	upstream := newStreamingUpstream(t, streamingUpstreamScript{
		contentType: "application/json",
		status:      http.StatusCreated,
		fragments:   []string{string(wantBody)},
	})

	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)

	response, err := http.Get(gateway.URL + "/v1/responses")
	if err != nil {
		t.Fatalf("GET /v1/responses: %v", err)
	}
	gotBody, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil {
		t.Fatalf("read response body: %v", readErr)
	}

	if response.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusCreated)
	}
	if !bytes.Equal(gotBody, wantBody) {
		t.Fatalf("response body = %q, want %q", gotBody, wantBody)
	}
}

func TestProxyDispatchesResponseBodyByUpstreamContentType(t *testing.T) {
	for _, test := range []struct {
		name        string
		contentType string
		status      int
		body        string
	}{
		{
			name:        "json",
			contentType: "application/json; charset=utf-8",
			status:      http.StatusCreated,
			body:        `{"id":"json-response"}`,
		},
		{
			name:        "opaque",
			contentType: "application/octet-stream",
			status:      http.StatusPartialContent,
			body:        "\x00opaque\xff",
		},
		{
			name:        "SSE",
			contentType: "text/event-stream; charset=utf-8",
			status:      http.StatusTooManyRequests,
			body:        "event: message\ndata: raw bytes\n\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				response.Header().Set("Content-Type", test.contentType)
				response.Header().Set("X-Upstream-Mode", test.name)
				response.WriteHeader(test.status)
				_, _ = response.Write([]byte(test.body))
			}))
			t.Cleanup(upstream.Close)

			gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
			t.Cleanup(gateway.Close)

			response, err := http.Get(gateway.URL + "/v1/dispatch")
			if err != nil {
				t.Fatalf("GET /v1/dispatch: %v", err)
			}
			gotBody, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if readErr != nil {
				t.Fatalf("read response body: %v", readErr)
			}

			if response.StatusCode != test.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.status)
			}
			if response.Header.Get("Content-Type") != test.contentType {
				t.Fatalf("Content-Type = %q, want %q", response.Header.Get("Content-Type"), test.contentType)
			}
			if response.Header.Get("X-Upstream-Mode") != test.name {
				t.Fatalf("X-Upstream-Mode = %q, want %q", response.Header.Get("X-Upstream-Mode"), test.name)
			}
			if string(gotBody) != test.body {
				t.Fatalf("response body = %q, want %q", gotBody, test.body)
			}
		})
	}
}

func TestProxyDispatchesOnlySSEThroughStreamingCopyPath(t *testing.T) {
	for _, test := range []struct {
		name        string
		contentType string
		wantFlushes int
	}{
		{name: "json", contentType: "application/json", wantFlushes: 0},
		{name: "opaque", contentType: "application/octet-stream", wantFlushes: 0},
		{name: "SSE", contentType: "text/event-stream", wantFlushes: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				response.Header().Set("Content-Type", test.contentType)
				_, _ = io.WriteString(response, "body")
			}))
			t.Cleanup(upstream.Close)

			handler := newProxyHandler(transport.NewClient(), upstream.URL, "upstream-secret")
			request := httptest.NewRequest(http.MethodGet, "http://gateway.example.test/v1/dispatch", nil)
			response := &flushCountingResponseWriter{ResponseRecorder: httptest.NewRecorder()}
			handler.ServeHTTP(response, request)

			if response.flushes != test.wantFlushes {
				t.Fatalf("flushes = %d, want %d for %s copy path", response.flushes, test.wantFlushes, test.name)
			}
		})
	}
}

func TestProxyFlushesEachSSEFragmentBeforeUpstreamContinues(t *testing.T) {
	const firstFragment = "data: first\n\n"
	const secondFragment = "data: second\n\n"
	upstream := newStreamingUpstream(t, streamingUpstreamScript{
		contentType:    "text/event-stream",
		fragments:      []string{firstFragment, secondFragment},
		flushEach:      true,
		waitAfterFirst: true,
	})

	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)

	response, err := http.Get(gateway.URL + "/v1/stream")
	if err != nil {
		t.Fatalf("GET /v1/stream: %v", err)
	}
	t.Cleanup(func() { response.Body.Close() })

	select {
	case <-upstream.firstFlushed:
	case <-time.After(time.Second):
		t.Fatal("upstream did not flush first fragment")
	}

	first := make([]byte, len(firstFragment))
	readDone := make(chan error, 1)
	go func() {
		_, readErr := io.ReadFull(response.Body, first)
		readDone <- readErr
	}()
	select {
	case readErr := <-readDone:
		if readErr != nil {
			t.Fatalf("read first fragment: %v", readErr)
		}
	case <-time.After(time.Second):
		t.Fatal("first SSE fragment was buffered before flush")
	}
	if string(first) != firstFragment {
		t.Fatalf("first fragment = %q, want %q", first, firstFragment)
	}

	upstream.release()
	rest, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read remaining SSE body: %v", err)
	}
	if string(rest) != secondFragment {
		t.Fatalf("remaining body = %q, want %q", rest, secondFragment)
	}
}

func TestProxyPreservesSSECommentsDoneAndBytesAfterDone(t *testing.T) {
	const firstFragment = ": heartbeat\r\n\r\ndata: before-done\n\n"
	const secondFragment = "data: [DONE]\n\n: after-done\r\n\r\ndata: still-open\n\n"
	upstream := newStreamingUpstream(t, streamingUpstreamScript{
		contentType:    "text/event-stream",
		fragments:      []string{firstFragment, secondFragment},
		flushEach:      true,
		waitAfterFirst: true,
	})

	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)

	response, err := http.Get(gateway.URL + "/v1/stream")
	if err != nil {
		t.Fatalf("GET /v1/stream: %v", err)
	}
	t.Cleanup(func() { response.Body.Close() })

	select {
	case <-upstream.firstFlushed:
	case <-time.After(time.Second):
		t.Fatal("upstream did not flush first SSE fragment")
	}

	first := make([]byte, len(firstFragment))
	readDone := make(chan error, 1)
	go func() {
		_, readErr := io.ReadFull(response.Body, first)
		readDone <- readErr
	}()
	select {
	case readErr := <-readDone:
		if readErr != nil {
			t.Fatalf("read first SSE fragment: %v", readErr)
		}
	case <-time.After(time.Second):
		t.Fatal("first comment/event fragment was buffered before flush")
	}
	if string(first) != firstFragment {
		t.Fatalf("first fragment = %q, want %q", first, firstFragment)
	}

	upstream.release()
	rest, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read remaining SSE body: %v", err)
	}
	if got := string(first) + string(rest); got != firstFragment+secondFragment {
		t.Fatalf("response body = %q, want %q", got, firstFragment+secondFragment)
	}
}

func TestProxyPreservesSplitSSEWrites(t *testing.T) {
	const wantBody = "da" + "ta: split across writes\r\n" + "\r\n"
	upstream := newStreamingUpstream(t, streamingUpstreamScript{
		contentType: "text/event-stream",
		fragments:   []string{"da", "ta: split ", "across writes", "\r\n", "\r\n"},
		flushEach:   true,
	})

	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)

	response, err := http.Get(gateway.URL + "/v1/stream")
	if err != nil {
		t.Fatalf("GET /v1/stream: %v", err)
	}
	gotBody, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil {
		t.Fatalf("read SSE body: %v", readErr)
	}
	if string(gotBody) != wantBody {
		t.Fatalf("response body = %q, want %q", gotBody, wantBody)
	}
}

func TestProxyPreservesCoalescedSSEEvents(t *testing.T) {
	const wantBody = ": first\n\ndata: one\n\nevent: named\ndata: two\n\n"
	upstream := newStreamingUpstream(t, streamingUpstreamScript{
		contentType: "text/event-stream",
		fragments:   []string{wantBody},
		flushEach:   true,
	})

	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)

	response, err := http.Get(gateway.URL + "/v1/stream")
	if err != nil {
		t.Fatalf("GET /v1/stream: %v", err)
	}
	gotBody, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil {
		t.Fatalf("read SSE body: %v", readErr)
	}
	if string(gotBody) != wantBody {
		t.Fatalf("response body = %q, want %q", gotBody, wantBody)
	}
}

func TestProxyPropagatesCancellationDuringActiveSSE(t *testing.T) {
	const firstFragment = "data: first\n\n"
	upstream := newStreamingUpstream(t, streamingUpstreamScript{
		contentType:         "text/event-stream",
		fragments:           []string{firstFragment},
		flushEach:           true,
		waitForCancellation: true,
	})

	gatewayCompleted := make(chan struct{})
	proxy := newProxyHandler(transport.NewClient(), upstream.URL, "upstream-secret")
	gatewayHandler := newHandler(slog.Default(), http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		proxy.ServeHTTP(response, request)
		close(gatewayCompleted)
	}))
	gateway := httptest.NewServer(gatewayHandler)
	t.Cleanup(gateway.Close)

	requestContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, gateway.URL+"/v1/stream", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("start active stream: %v", err)
	}
	t.Cleanup(func() { response.Body.Close() })

	select {
	case <-upstream.firstFlushed:
	case <-time.After(time.Second):
		t.Fatal("upstream did not receive active stream")
	}
	first := make([]byte, len(firstFragment))
	readDone := make(chan error, 1)
	go func() {
		_, readErr := io.ReadFull(response.Body, first)
		readDone <- readErr
	}()
	select {
	case readErr := <-readDone:
		if readErr != nil {
			t.Fatalf("read first SSE fragment: %v", readErr)
		}
	case <-time.After(time.Second):
		t.Fatal("first SSE fragment was not delivered")
	}
	if string(first) != firstFragment {
		t.Fatalf("first fragment = %q, want %q", first, firstFragment)
	}
	cancel()
	response.Body.Close()

	select {
	case <-upstream.cancelled:
	case <-time.After(time.Second):
		t.Fatal("upstream did not observe active stream cancellation")
	}
	select {
	case <-gatewayCompleted:
	case <-time.After(time.Second):
		t.Fatal("gateway handler did not finish after active stream cancellation")
	}
}

func TestProxyStreamsRunInParallel(t *testing.T) {
	const firstFragment = "data: first\n\n"
	upstream := newStreamingUpstream(t, streamingUpstreamScript{
		contentType:    "text/event-stream",
		fragments:      []string{firstFragment, "data: done\n\n"},
		flushEach:      true,
		waitAfterFirst: true,
	})

	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)

	type streamResult struct {
		body []byte
		err  error
	}
	results := make(chan streamResult, 2)
	firstReceived := make(chan struct{}, 2)
	for range 2 {
		go func() {
			response, err := http.Get(gateway.URL + "/v1/stream")
			if err != nil {
				results <- streamResult{err: err}
				return
			}
			first := make([]byte, len(firstFragment))
			if _, err := io.ReadFull(response.Body, first); err != nil {
				response.Body.Close()
				results <- streamResult{err: err}
				return
			}
			if string(first) != firstFragment {
				response.Body.Close()
				results <- streamResult{err: errors.New("first SSE fragment changed")}
				return
			}
			firstReceived <- struct{}{}
			body, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			results <- streamResult{body: append(first, body...), err: readErr}
		}()
	}

	for range 2 {
		select {
		case <-upstream.arrivals:
		case <-time.After(time.Second):
			upstream.release()
			t.Fatal("not both streams reached upstream before completion")
		}
	}
	for range 2 {
		select {
		case <-firstReceived:
		case <-time.After(time.Second):
			upstream.release()
			t.Fatal("not both clients received their first fragment before release")
		}
	}
	upstream.release()

	for range 2 {
		select {
		case result := <-results:
			if result.err != nil {
				t.Fatalf("parallel stream request: %v", result.err)
			}
			if got := string(result.body); got != firstFragment+"data: done\n\n" {
				t.Fatalf("parallel stream body = %q, want %q", got, firstFragment+"data: done\n\n")
			}
		case <-time.After(time.Second):
			t.Fatal("parallel stream did not complete after release")
		}
	}
}

func TestProxyClosesSSEOnUpstreamEOFWithoutDone(t *testing.T) {
	const body = "data: terminal\n\n"
	upstream := newStreamingUpstream(t, streamingUpstreamScript{
		contentType: "text/event-stream",
		fragments:   []string{body},
		flushEach:   true,
	})

	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)

	startedAt := time.Now()
	response, err := http.Get(gateway.URL + "/v1/stream")
	if err != nil {
		t.Fatalf("GET /v1/stream: %v", err)
	}
	gotBody, readErr := io.ReadAll(response.Body)
	finishedAt := time.Now()
	response.Body.Close()
	if readErr != nil {
		t.Fatalf("read SSE body: %v", readErr)
	}
	if string(gotBody) != body {
		t.Fatalf("response body = %q, want %q", gotBody, body)
	}

	select {
	case closedAt := <-upstream.returned:
		if closedAt.Before(startedAt) || finishedAt.Sub(closedAt) > 250*time.Millisecond {
			t.Fatalf("downstream completion was %v after upstream handler close", finishedAt.Sub(closedAt))
		}
	case <-time.After(time.Second):
		t.Fatal("upstream close was not observed")
	}
}

func TestProxySSECloseDelayRegression(t *testing.T) {
	const body = "data: {\"finish_reason\":\"stop\"}\n\n"
	upstream := newStreamingUpstream(t, streamingUpstreamScript{
		contentType: "text/event-stream",
		fragments:   []string{body},
		flushEach:   true,
	})

	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)

	response, err := http.Get(gateway.URL + "/v1/stream")
	if err != nil {
		t.Fatalf("GET /v1/stream: %v", err)
	}
	startedReading := time.Now()
	gotBody, readErr := io.ReadAll(response.Body)
	finishedReading := time.Now()
	response.Body.Close()
	if readErr != nil {
		t.Fatalf("read SSE body: %v", readErr)
	}
	if string(gotBody) != body {
		t.Fatalf("response body = %q, want %q", gotBody, body)
	}

	select {
	case upstreamEOF := <-upstream.returned:
		if delay := finishedReading.Sub(upstreamEOF); delay > 250*time.Millisecond {
			t.Fatalf("downstream completed %v after upstream EOF", delay)
		}
	case <-time.After(time.Second):
		t.Fatalf("upstream did not return within %v", time.Since(startedReading))
	}
}

func TestProxyPreservesCompressedResponseRepresentation(t *testing.T) {
	var compressed bytes.Buffer
	compressor := gzip.NewWriter(&compressed)
	if _, err := compressor.Write([]byte("compressed upstream response")); err != nil {
		t.Fatalf("compress response: %v", err)
	}
	if err := compressor.Close(); err != nil {
		t.Fatalf("close compressor: %v", err)
	}
	wantBody := compressed.Bytes()

	for _, test := range []struct {
		name           string
		acceptEncoding string
	}{
		{name: "absent"},
		{name: "client provided", acceptEncoding: "gzip"},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstreamEncoding := make(chan string, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				upstreamEncoding <- request.Header.Get("Accept-Encoding")
				response.Header().Set("Content-Encoding", "gzip")
				response.WriteHeader(http.StatusOK)
				_, _ = response.Write(wantBody)
			}))
			t.Cleanup(upstream.Close)

			gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
			t.Cleanup(gateway.Close)

			request, err := http.NewRequest(http.MethodGet, gateway.URL+"/v1/responses", nil)
			if err != nil {
				t.Fatalf("create request: %v", err)
			}
			if test.acceptEncoding != "" {
				request.Header.Set("Accept-Encoding", test.acceptEncoding)
			}
			client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
			response, err := client.Do(request)
			if err != nil {
				t.Fatalf("GET /v1/responses: %v", err)
			}
			gotBody, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if readErr != nil {
				t.Fatalf("read response body: %v", readErr)
			}

			if got := <-upstreamEncoding; got != test.acceptEncoding {
				t.Fatalf("upstream Accept-Encoding = %q, want %q", got, test.acceptEncoding)
			}
			if got := response.Header.Get("Content-Encoding"); got != "gzip" {
				t.Fatalf("Content-Encoding = %q, want gzip", got)
			}
			if !bytes.Equal(gotBody, wantBody) {
				t.Fatalf("response body changed: got %x, want %x", gotBody, wantBody)
			}
		})
	}
}

func TestProxySupportsUnknownV1Endpoint(t *testing.T) {
	requestDetails := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestDetails <- request.Method + " " + request.URL.RequestURI()
		response.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(upstream.Close)

	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)

	request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/something-unknown?mode=raw", strings.NewReader("unknown-payload"))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST /v1/something-unknown: %v", err)
	}
	response.Body.Close()

	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusAccepted)
	}
	select {
	case details := <-requestDetails:
		if details != "POST /v1/something-unknown?mode=raw" {
			t.Fatalf("upstream request = %q, want %q", details, "POST /v1/something-unknown?mode=raw")
		}
	case <-time.After(time.Second):
		t.Fatal("unknown endpoint did not reach upstream")
	}
}

func TestProxyBindsUpstreamRequestToClientContext(t *testing.T) {
	type contextKey struct{}
	const contextValue = "request-context"

	requestContext, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, contextValue))
	t.Cleanup(cancel)
	receivedContext := make(chan context.Context, 1)
	roundTripper := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		receivedContext <- request.Context()
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	handler := newProxyHandler(&http.Client{Transport: roundTripper}, "http://router.example.test", "upstream-secret")
	request := httptest.NewRequest(http.MethodGet, "http://gateway.example.test/v1/models", nil).WithContext(requestContext)
	response := httptest.NewRecorder()
	finished := make(chan struct{})

	go func() {
		handler.ServeHTTP(response, request)
		close(finished)
	}()

	var upstreamContext context.Context
	select {
	case upstreamContext = <-receivedContext:
	case <-time.After(time.Second):
		t.Fatal("upstream transport did not receive request")
	}
	if upstreamContext.Value(contextKey{}) != contextValue {
		t.Fatalf("upstream context value = %v, want %q", upstreamContext.Value(contextKey{}), contextValue)
	}

	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("upstream request did not observe context cancellation")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (roundTripper roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTripper(request)
}

func TestProxyPropagatesHTTPClientCancellationToUpstream(t *testing.T) {
	upstream := newStreamingUpstream(t, streamingUpstreamScript{
		waitForCancellation: true,
	})

	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)

	requestContext, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, gateway.URL+"/v1/slow", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	clientDone := make(chan struct{})
	go func() {
		response, _ := http.DefaultClient.Do(request)
		if response != nil {
			response.Body.Close()
		}
		close(clientDone)
	}()

	select {
	case <-upstream.arrivals:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("upstream did not receive request")
	}

	cancel()
	select {
	case <-upstream.cancelled:
	case <-time.After(time.Second):
		t.Fatal("upstream did not observe client cancellation")
	}
	select {
	case <-clientDone:
	case <-time.After(time.Second):
		t.Fatal("client request did not finish after cancellation")
	}
}

func TestProxyStreamsRequestBodyWithoutChangingBytes(t *testing.T) {
	wantBody := []byte(`{"model":"unknown","input":[1,2,3]}`)
	body := make(chan []byte, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		gotBody, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read upstream request body: %v", err)
			return
		}
		body <- gotBody
	}))
	t.Cleanup(upstream.Close)

	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)

	request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/unknown", bytes.NewReader(wantBody))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST /v1/unknown: %v", err)
	}
	response.Body.Close()

	select {
	case gotBody := <-body:
		if !bytes.Equal(gotBody, wantBody) {
			t.Fatalf("upstream body = %q, want %q", gotBody, wantBody)
		}
	case <-time.After(time.Second):
		t.Fatal("upstream did not receive request body")
	}
}

func TestProxyPreservesRequestContentLengthSemantics(t *testing.T) {
	type requestDetails struct {
		body             []byte
		contentLength    int64
		transferEncoding []string
	}

	for _, test := range []struct {
		name                 string
		body                 io.Reader
		wantBody             string
		wantContentLength    int64
		wantTransferEncoding string
	}{
		{
			name:              "known length",
			body:              strings.NewReader("known request body"),
			wantBody:          "known request body",
			wantContentLength: int64(len("known request body")),
		},
		{
			name:              "empty body",
			wantContentLength: 0,
		},
		{
			name:                 "unknown length",
			body:                 io.NopCloser(strings.NewReader("streamed request body")),
			wantBody:             "streamed request body",
			wantContentLength:    -1,
			wantTransferEncoding: "chunked",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			details := make(chan requestDetails, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Errorf("read upstream body: %v", err)
					return
				}
				details <- requestDetails{
					body:             body,
					contentLength:    request.ContentLength,
					transferEncoding: request.TransferEncoding,
				}
			}))
			t.Cleanup(upstream.Close)

			gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
			t.Cleanup(gateway.Close)

			request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/responses", test.body)
			if err != nil {
				t.Fatalf("create request: %v", err)
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatalf("POST /v1/responses: %v", err)
			}
			response.Body.Close()

			select {
			case got := <-details:
				if string(got.body) != test.wantBody {
					t.Fatalf("upstream body = %q, want %q", got.body, test.wantBody)
				}
				if got.contentLength != test.wantContentLength {
					t.Fatalf("upstream ContentLength = %d, want %d", got.contentLength, test.wantContentLength)
				}
				if strings.Join(got.transferEncoding, ",") != test.wantTransferEncoding {
					t.Fatalf("upstream TransferEncoding = %q, want %q", got.transferEncoding, test.wantTransferEncoding)
				}
			case <-time.After(time.Second):
				t.Fatal("upstream did not receive request")
			}
		})
	}
}

func TestCompletionLogContainsRequestMetadataWithoutAuthorization(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	server := httptest.NewServer(newHandler(logger, healthHandler()))
	t.Cleanup(server.Close)

	request, err := http.NewRequest(http.MethodGet, server.URL+"/health", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer client-secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	response.Body.Close()

	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("completion log lines = %d, want 1", len(lines))
	}

	var record map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("decode completion log: %v", err)
	}
	if record["request_id"] != response.Header.Get(requestIDHeader) {
		t.Fatalf("request_id = %v, want %q", record["request_id"], response.Header.Get(requestIDHeader))
	}
	if record["method"] != http.MethodGet {
		t.Fatalf("method = %v, want %q", record["method"], http.MethodGet)
	}
	if record["path"] != "/health" {
		t.Fatalf("path = %v, want /health", record["path"])
	}
	if record["status"] != float64(http.StatusOK) {
		t.Fatalf("status = %v, want %d", record["status"], http.StatusOK)
	}
	if _, ok := record["duration"]; !ok {
		t.Fatal("duration is missing")
	}
	if strings.Contains(logs.String(), "Authorization") || strings.Contains(logs.String(), "client-secret") {
		t.Fatal("completion log contains Authorization data")
	}
}

func TestCompletionResponseWriterPreservesFlush(t *testing.T) {
	underlying := &flushRecordingWriter{basicResponseWriter: &basicResponseWriter{header: make(http.Header)}}
	writer := &completionResponseWriter{ResponseWriter: underlying}

	if err := http.NewResponseController(writer).Flush(); err != nil {
		t.Fatalf("flush wrapped writer: %v", err)
	}
	if !underlying.flushed {
		t.Fatal("flush did not reach underlying writer")
	}
}

func TestCompletionResponseWriterRecordsImplicitStatusFromFlush(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://gateway.example.test/stream", nil)
	withCompletionLog(logger, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := http.NewResponseController(response).Flush(); err != nil {
			t.Errorf("flush response: %v", err)
			return
		}
		response.WriteHeader(http.StatusInternalServerError)
	})).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &record); err != nil {
		t.Fatalf("decode completion log: %v", err)
	}
	if record["status"] != float64(http.StatusOK) {
		t.Fatalf("logged status = %v, want %d", record["status"], http.StatusOK)
	}
}

func TestCompletionResponseWriterReportsUnsupportedFlush(t *testing.T) {
	underlying := &basicResponseWriter{header: make(http.Header)}
	writer := &completionResponseWriter{ResponseWriter: underlying}

	err := http.NewResponseController(writer).Flush()
	if !errors.Is(err, http.ErrNotSupported) {
		t.Fatalf("flush error = %v, want %v", err, http.ErrNotSupported)
	}
}

func TestCompletionResponseWriterCapturesStatusThroughLogging(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://gateway.example.test/status", nil)
	withCompletionLog(logger, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusAccepted)
	})).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusAccepted)
	}
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("completion log lines = %d, want 1", len(lines))
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("decode completion log: %v", err)
	}
	if record["status"] != float64(http.StatusAccepted) {
		t.Fatalf("logged status = %v, want %d", record["status"], http.StatusAccepted)
	}
}

type basicResponseWriter struct {
	header http.Header
}

type flushCountingResponseWriter struct {
	*httptest.ResponseRecorder
	flushes int
}

func (writer *flushCountingResponseWriter) Flush() {
	writer.flushes++
	writer.ResponseRecorder.Flush()
}

func (writer *basicResponseWriter) Header() http.Header {
	return writer.header
}

func (writer *basicResponseWriter) Write(body []byte) (int, error) {
	return len(body), nil
}

func (writer *basicResponseWriter) WriteHeader(status int) {}

type flushRecordingWriter struct {
	*basicResponseWriter
	flushed bool
}

func (writer *flushRecordingWriter) Flush() {
	writer.flushed = true
}

func healthHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health)
	return mux
}
