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
	"strconv"
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

func TestProxyPreservesUpstreamErrorResponse(t *testing.T) {
	wantBody := []byte(`{"error":{"message":"upstream failure","code":"provider_error"}}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("X-Upstream-Error", "preserve-me")
		response.WriteHeader(http.StatusServiceUnavailable)
		_, _ = response.Write(wantBody)
	}))
	t.Cleanup(upstream.Close)

	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)
	response, err := http.Get(gateway.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	gotBody, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusServiceUnavailable || !bytes.Equal(gotBody, wantBody) {
		t.Fatalf("upstream error = status %d body %q", response.StatusCode, gotBody)
	}
	if response.Header.Get("Content-Type") != "application/json" || response.Header.Get("X-Upstream-Error") != "preserve-me" {
		t.Fatalf("upstream error headers were not preserved: %v", response.Header)
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

func TestProxyPreservesRepresentationHeadersWithoutTransformation(t *testing.T) {
	for _, contentType := range []string{"application/json", "application/octet-stream", "text/event-stream"} {
		t.Run(contentType, func(t *testing.T) {
			body := []byte("transparent body")
			upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				response.Header().Set("Content-Type", contentType)
				response.Header().Set("Content-Encoding", "identity")
				response.Header().Set("Content-Length", strconv.Itoa(len(body)))
				response.Header().Set("Content-Range", "bytes 0-15/16")
				response.Header().Set("Accept-Ranges", "bytes")
				response.Header().Set("ETag", `"transparent-validator"`)
				response.Header().Set("Content-MD5", "transparent-md5")
				response.Header().Set("Digest", "sha-256=transparent-digest")
				response.Header().Set("Content-Digest", "sha-256=:transparent-content-digest:")
				response.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
				response.Header().Set("X-Gateway-Trace", "trace-value")
				_, _ = response.Write(body)
			}))
			t.Cleanup(upstream.Close)
			gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
			t.Cleanup(gateway.Close)

			client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
			response, err := client.Get(gateway.URL + "/v1/transparent")
			if err != nil {
				t.Fatalf("GET transparent response: %v", err)
			}
			gotBody, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if readErr != nil {
				t.Fatalf("read transparent response: %v", readErr)
			}
			if !bytes.Equal(gotBody, body) {
				t.Fatalf("body = %q, want %q", gotBody, body)
			}
			wantHeaders := map[string]string{
				"Content-Type":     contentType,
				"Content-Encoding": "identity",
				"Content-Length":   strconv.Itoa(len(body)),
				"Content-Range":    "bytes 0-15/16",
				"Accept-Ranges":    "bytes",
				"ETag":             `"transparent-validator"`,
				"Content-MD5":      "transparent-md5",
				"Digest":           "sha-256=transparent-digest",
				"Content-Digest":   "sha-256=:transparent-content-digest:",
				"Last-Modified":    "Wed, 21 Oct 2015 07:28:00 GMT",
				"X-Gateway-Trace":  "trace-value",
			}
			for name, want := range wantHeaders {
				if got := response.Header.Get(name); got != want {
					t.Fatalf("%s = %q, want %q", name, got, want)
				}
			}
		})
	}
}

func TestProxyConvertsExplicitNonStreamChatSSEToJSON(t *testing.T) {
	const stream = `data: {"id":"chatcmpl-bifrost","model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"}}]}

data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}

data: [DONE]

`
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		response.Header().Set("X-Upstream-Trace", "trace-value")
		response.Header().Set("X-RateLimit-Remaining", "17")
		response.Header().Set("Content-Length", strconv.Itoa(len(stream)))
		response.Header().Set("Content-Encoding", "identity")
		response.Header().Set("Content-Range", "bytes 0-10/11")
		response.Header().Set("Accept-Ranges", "bytes")
		response.Header().Set("ETag", `"upstream-validator"`)
		response.Header().Set("Content-MD5", "upstream-md5")
		response.Header().Set("Digest", "sha-256=upstream-digest")
		response.Header().Set("Content-Digest", "sha-256=:upstream-content-digest:")
		response.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
		response.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(response, stream)
	}))
	t.Cleanup(upstream.Close)

	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)

	request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","stream":false,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST chat completions: %v", err)
	}
	gotBody, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil {
		t.Fatalf("read response body: %v", readErr)
	}
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusBadGateway)
	}
	if response.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", response.Header.Get("Content-Type"))
	}
	if response.Header.Get("Content-Length") != strconv.Itoa(len(gotBody)) {
		t.Fatalf("Content-Length = %q, want %d", response.Header.Get("Content-Length"), len(gotBody))
	}
	if !json.Valid(gotBody) || bytes.Contains(gotBody, []byte("failed to unmarshal")) || bytes.Contains(gotBody, []byte("text/event-stream")) {
		t.Fatalf("Bifrost mismatch response is not valid converted JSON: %q", gotBody)
	}
	var decoded struct {
		ID      string `json:"id"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage map[string]int `json:"usage"`
	}
	if err := json.Unmarshal(gotBody, &decoded); err != nil {
		t.Fatalf("unmarshal converted response: %v", err)
	}
	if decoded.ID != "chatcmpl-bifrost" || len(decoded.Choices) != 1 || decoded.Choices[0].Message.Content != "hello" || decoded.Usage["total_tokens"] != 3 {
		t.Fatalf("converted response = %#v", decoded)
	}
	if response.Header.Get("X-Upstream-Trace") != "trace-value" {
		t.Fatalf("safe upstream header was not preserved")
	}
	if response.Header.Get("X-RateLimit-Remaining") != "17" {
		t.Fatalf("rate-limit header was not preserved")
	}
	for _, name := range []string{"Content-Encoding", "Content-Range", "Accept-Ranges", "ETag", "Content-MD5", "Digest", "Content-Digest", "Last-Modified"} {
		if got := response.Header.Get(name); got != "" {
			t.Fatalf("transformed %s = %q, want absent", name, got)
		}
	}
}

func TestProxyReturnsIdentityChatSSEAfterDONEWithoutWaitingForUpstreamEOF(t *testing.T) {
	const stream = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\ndata: [DONE]\n\n: bytes after done\n\n"
	started := make(chan struct{})
	cancelled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		response.Header().Set("Content-Encoding", "identity")
		if _, err := io.WriteString(response, stream); err != nil {
			return
		}
		if flusher, ok := response.(http.Flusher); ok {
			flusher.Flush()
		}
		close(started)
		<-request.Context().Done()
		close(cancelled)
	}))
	t.Cleanup(upstream.Close)

	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)
	request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{"stream":false}`))
	if err != nil {
		t.Fatal(err)
	}
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
	case <-time.After(time.Second):
		t.Fatal("upstream did not flush DONE")
	}

	var response *http.Response
	select {
	case err := <-errorCh:
		t.Fatalf("POST chat completions: %v", err)
	case response = <-responseCh:
	case <-time.After(time.Second):
		t.Fatal("gateway waited for upstream EOF after identity DONE")
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil {
		t.Fatalf("read converted response: %v", readErr)
	}
	if response.StatusCode != http.StatusOK || !json.Valid(body) {
		t.Fatalf("status/body = %d/%q, want successful JSON", response.StatusCode, body)
	}
	if bytes.Contains(body, []byte("bytes after done")) {
		t.Fatalf("converted response included bytes after DONE: %q", body)
	}

	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("upstream did not observe cancellation after converted response completed")
	}
}

func TestProxyConvertsGzipChatSSEToUncompressedJSON(t *testing.T) {
	const stream = "data: {\"id\":\"gzip-chat\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	var compressed bytes.Buffer
	compressor := gzip.NewWriter(&compressed)
	if _, err := compressor.Write([]byte(stream)); err != nil {
		t.Fatalf("compress SSE: %v", err)
	}
	if err := compressor.Close(); err != nil {
		t.Fatalf("close compressor: %v", err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		response.Header().Set("Content-Encoding", "gzip")
		response.Header().Set("Content-Length", strconv.Itoa(compressed.Len()))
		response.Header().Set("X-Upstream-Trace", "trace-value")
		response.WriteHeader(http.StatusCreated)
		_, _ = response.Write(compressed.Bytes())
	}))
	t.Cleanup(upstream.Close)

	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)
	request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{"stream":false}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST chat completions: %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil {
		t.Fatalf("read converted response: %v", readErr)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusCreated)
	}
	if !json.Valid(body) {
		t.Fatalf("converted body is not JSON: %q", body)
	}
	if response.Header.Get("Content-Encoding") != "" {
		t.Fatalf("Content-Encoding = %q, want absent", response.Header.Get("Content-Encoding"))
	}
	if response.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", response.Header.Get("Content-Type"))
	}
	if response.Header.Get("Content-Length") != strconv.Itoa(len(body)) {
		t.Fatalf("Content-Length = %q, want %d", response.Header.Get("Content-Length"), len(body))
	}
	if response.Header.Get("X-Upstream-Trace") != "trace-value" {
		t.Fatal("safe upstream header was not preserved")
	}
}

func TestDecodedRepresentationReaderAcceptsExactLimitAndRejectsOneByteOver(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want error
	}{
		{name: "exact limit", body: strings.Repeat("x", int(aggregationMaxDecodedBytes))},
		{name: "one byte over", body: strings.Repeat("x", int(aggregationMaxDecodedBytes)+1), want: errDecodedRepresentationTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := &decodedRepresentationReader{
				reader:    strings.NewReader(test.body),
				remaining: aggregationMaxDecodedBytes,
			}
			got, err := io.ReadAll(reader)
			if test.want != nil {
				if !errors.Is(err, test.want) {
					t.Fatalf("read error = %v, want errors.Is(..., %v)", err, test.want)
				}
				if len(got) != int(aggregationMaxDecodedBytes) {
					t.Fatalf("decoded bytes = %d, want %d before overflow", len(got), aggregationMaxDecodedBytes)
				}
				return
			}
			if err != nil {
				t.Fatalf("read exact-limit representation: %v", err)
			}
			if len(got) != int(aggregationMaxDecodedBytes) {
				t.Fatalf("decoded bytes = %d, want %d", len(got), aggregationMaxDecodedBytes)
			}
		})
	}
}

func TestProxyConvertsChatSSEWithExactAccumulatedPayloadLimit(t *testing.T) {
	const chunkSize = 60 * 1024
	content := strings.Repeat("x", aggregationMaxPayloadSize)
	var stream strings.Builder
	stream.Grow(len(content) + len(content)/chunkSize*128)
	stream.WriteString(`data: {"choices":[{"index":0,"delta":{"role":"assistant"}}]}` + "\n\n")
	for offset := 0; offset < len(content); {
		end := offset + chunkSize
		if end > len(content) {
			end = len(content)
		}
		stream.WriteString(`data: {"choices":[{"index":0,"delta":{"content":"`)
		stream.WriteString(content[offset:end])
		stream.WriteString(`"}}]}` + "\n\n")
		offset = end
	}
	stream.WriteString(`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n")
	stream.WriteString("data: [DONE]\n\n")

	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(response, stream.String())
	}))
	t.Cleanup(upstream.Close)
	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)

	request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{"stream":false}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST chat completions: %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil {
		t.Fatalf("read converted response: %v", readErr)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", response.StatusCode, body[:min(len(body), 200)])
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("unmarshal converted response: %v", err)
	}
	if len(decoded.Choices) != 1 || len(decoded.Choices[0].Message.Content) != aggregationMaxPayloadSize {
		t.Fatalf("converted content length = %d, want %d", len(decoded.Choices[0].Message.Content), aggregationMaxPayloadSize)
	}
}

func TestProxyValidatesGzipTrailerAfterSSEDONE(t *testing.T) {
	const stream = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\ndata: [DONE]\n\n"
	var compressed bytes.Buffer
	compressor := gzip.NewWriter(&compressed)
	if _, err := compressor.Write([]byte(stream)); err != nil {
		t.Fatal(err)
	}
	if err := compressor.Close(); err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte(nil), compressed.Bytes()...)
	corrupt[len(corrupt)-1] ^= 0xff

	for _, test := range []struct {
		name string
		body []byte
	}{
		{name: "corrupt trailer", body: corrupt},
		{name: "truncated trailer", body: compressed.Bytes()[:len(compressed.Bytes())-2]},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				response.Header().Set("Content-Type", "text/event-stream")
				response.Header().Set("Content-Encoding", "gzip")
				_, _ = response.Write(test.body)
			}))
			t.Cleanup(upstream.Close)
			gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
			t.Cleanup(gateway.Close)

			request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{"stream":false}`))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Content-Type", "application/json")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if readErr != nil {
				t.Fatal(readErr)
			}
			if response.StatusCode != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502 (body %q)", response.StatusCode, body)
			}
		})
	}
}

func TestProxyCancellationUnblocksGzipDrainAfterSSEDONE(t *testing.T) {
	const stream = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\ndata: [DONE]\n\n"
	started := make(chan struct{})
	cancelled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		response.Header().Set("Content-Encoding", "gzip")
		compressor := gzip.NewWriter(response)
		if _, err := compressor.Write([]byte(stream)); err != nil {
			return
		}
		if err := compressor.Flush(); err != nil {
			return
		}
		if flusher, ok := response.(http.Flusher); ok {
			flusher.Flush()
		}
		close(started)
		<-request.Context().Done()
		close(cancelled)
	}))
	t.Cleanup(upstream.Close)
	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)

	requestContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{"stream":false}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	finished := make(chan struct{})
	go func() {
		response, _ := http.DefaultClient.Do(request)
		if response != nil {
			response.Body.Close()
		}
		close(finished)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("upstream did not flush semantic DONE")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("upstream did not observe cancellation while gzip trailer was withheld")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("gateway request did not finish after gzip drain cancellation")
	}
}

func TestProxyConvertsCleanEOFGzipChatSSE(t *testing.T) {
	const stream = "data: {\"id\":\"gzip-eof\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"
	var compressed bytes.Buffer
	compressor := gzip.NewWriter(&compressed)
	if _, err := compressor.Write([]byte(stream)); err != nil {
		t.Fatal(err)
	}
	if err := compressor.Close(); err != nil {
		t.Fatal(err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		response.Header().Set("Content-Encoding", "gzip")
		_, _ = response.Write(compressed.Bytes())
	}))
	t.Cleanup(upstream.Close)
	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)
	request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{"stream":false}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusOK || !json.Valid(body) {
		t.Fatalf("status/body = %d/%q, want successful JSON", response.StatusCode, body)
	}
}

func TestProxyRejectsDecodedSSERepresentationOverflow(t *testing.T) {
	const firstEvent = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"x\"}}]}\n\n"
	const emptyEvent = "data: {}\n\n"
	stream := firstEvent + strings.Repeat(emptyEvent, int(aggregationMaxDecodedBytes/int64(len(emptyEvent)))+1) + "data: [DONE]\n\n"
	var compressed bytes.Buffer
	compressor := gzip.NewWriter(&compressed)
	if _, err := compressor.Write([]byte(stream)); err != nil {
		t.Fatal(err)
	}
	if err := compressor.Close(); err != nil {
		t.Fatal(err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		response.Header().Set("Content-Encoding", "gzip")
		_, _ = response.Write(compressed.Bytes())
	}))
	t.Cleanup(upstream.Close)
	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)
	request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{"stream":false}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body %q)", response.StatusCode, body)
	}
}

func TestProxyPreservesCompressedTransparentChatSSE(t *testing.T) {
	const plain = "data: transparent-gzip\n\n"
	var compressed bytes.Buffer
	compressor := gzip.NewWriter(&compressed)
	if _, err := compressor.Write([]byte(plain)); err != nil {
		t.Fatalf("compress SSE: %v", err)
	}
	if err := compressor.Close(); err != nil {
		t.Fatalf("close compressor: %v", err)
	}
	wantBody := compressed.Bytes()

	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		response.Header().Set("Content-Encoding", "gzip")
		response.Header().Set("Content-Length", strconv.Itoa(len(wantBody)))
		response.Header().Set("Content-Range", "bytes 0-10/11")
		response.Header().Set("Accept-Ranges", "bytes")
		response.Header().Set("ETag", `"upstream-validator"`)
		response.Header().Set("Content-MD5", "upstream-md5")
		response.Header().Set("Digest", "sha-256=upstream-digest")
		response.Header().Set("Content-Digest", "sha-256=:upstream-content-digest:")
		response.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
		response.Header().Set("X-RateLimit-Remaining", "17")
		_, _ = response.Write(wantBody)
	}))
	t.Cleanup(upstream.Close)
	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)

	request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("POST chat completions: %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil {
		t.Fatalf("read transparent SSE: %v", readErr)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status/content type = %d/%q, want 200/text/event-stream", response.StatusCode, response.Header.Get("Content-Type"))
	}
	if response.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", response.Header.Get("Content-Encoding"))
	}
	if response.Header.Get("Content-Length") != strconv.Itoa(len(wantBody)) {
		t.Fatalf("Content-Length = %q, want %d", response.Header.Get("Content-Length"), len(wantBody))
	}
	for _, name := range []string{"Content-Range", "Accept-Ranges", "ETag", "Content-MD5", "Digest", "Content-Digest", "Last-Modified", "X-RateLimit-Remaining"} {
		if got := response.Header.Get(name); got == "" {
			t.Fatalf("transparent %s was not preserved", name)
		}
	}
	if !bytes.Equal(body, wantBody) {
		t.Fatalf("transparent body changed: got %x, want %x", body, wantBody)
	}
}

func TestProxyRejectsUnsupportedOrMalformedEncodingDuringChatSSEConversion(t *testing.T) {
	for _, test := range []struct {
		name     string
		encoding string
		body     []byte
	}{
		{name: "unsupported", encoding: "br", body: []byte("not decoded")},
		{name: "malformed gzip", encoding: "gzip", body: []byte("not gzip")},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				response.Header().Set("Content-Type", "text/event-stream")
				response.Header().Set("Content-Encoding", test.encoding)
				response.Header().Set("X-Upstream-Secret", "do-not-copy")
				_, _ = response.Write(test.body)
			}))
			t.Cleanup(upstream.Close)

			gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
			t.Cleanup(gateway.Close)
			request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{"stream":false}`))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Content-Type", "application/json")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502", response.StatusCode)
			}
			if strings.Contains(string(body), "do-not-copy") || strings.Contains(string(body), "not decoded") || strings.Contains(string(body), "gzip") {
				t.Fatalf("encoding details leaked: %q", body)
			}
		})
	}
}

func TestProxyAggregatesChatSSEAtGatewayOnCleanEOFWithSplitToolCallsAndChoices(t *testing.T) {
	fragments := []string{
		`data: {"id":"chat-eof","model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant"}},{"index":1,"delta":{"role":"assistant"}}]}` + "\n\n",
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"lookup","arguments":"{\"city\":\""}}]}}]}` + "\n\n",
		`data: {"choices":[{"index":1,"delta":{"content":"second"}}]}` + "\n\n",
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"Paris\"}"}}]}}]}` + "\n\n",
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"},{"index":1,"delta":{},"finish_reason":"stop"}]}` + "\n\n",
	}
	for _, fragment := range fragments {
		data := strings.TrimSuffix(strings.TrimPrefix(fragment, "data: "), "\n\n")
		if !json.Valid([]byte(data)) {
			t.Fatalf("invalid test event: %q", data)
		}
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		response.WriteHeader(http.StatusCreated)
		for _, fragment := range fragments {
			for _, part := range []string{fragment[:len(fragment)/2], fragment[len(fragment)/2:]} {
				if _, err := io.WriteString(response, part); err != nil {
					return
				}
				if flusher, ok := response.(http.Flusher); ok {
					flusher.Flush()
				}
			}
		}
	}))
	t.Cleanup(upstream.Close)
	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)

	request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{"stream":false}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST chat completions: %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil {
		t.Fatalf("read converted response: %v", readErr)
	}
	if response.StatusCode != http.StatusCreated || response.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("status/content type = %d/%q body=%q, want 201/application/json", response.StatusCode, response.Header.Get("Content-Type"), body)
	}
	var decoded struct {
		ID      string `json:"id"`
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					Function struct {
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("converted body is not JSON: %v", err)
	}
	if decoded.ID != "chat-eof" || len(decoded.Choices) != 2 || decoded.Choices[0].Message.ToolCalls[0].Function.Arguments != `{"city":"Paris"}` || decoded.Choices[1].Message.Content != "second" {
		t.Fatalf("converted response = %#v", decoded)
	}
}

func TestProxyKeepsChatSSETransparentUnlessRequestExplicitlyDisablesStreaming(t *testing.T) {
	const body = "data: raw-chat-event\n\n"
	upstream := newStreamingUpstream(t, streamingUpstreamScript{
		contentType: "text/event-stream",
		fragments:   []string{body},
	})
	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)

	requestBodies := []struct {
		name string
		body string
	}{
		{name: "true", body: `{"stream":true}`},
		{name: "absent", body: `{}`},
		{name: "unknown type", body: `{"stream":"false"}`},
		{name: "malformed", body: `{"stream":false`},
		{name: "over limit", body: string(append([]byte(`{"stream":false,"padding":"`), bytes.Repeat([]byte{'x'}, int(requestInspectionLimit))...))},
	}
	for _, test := range requestBodies {
		t.Run(test.name, func(t *testing.T) {
			requestBody := test.body
			if test.name == "over limit" {
				requestBody += `"}`
			}
			request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(requestBody))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Content-Type", "application/json")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatalf("POST chat completions: %v", err)
			}
			gotBody, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if readErr != nil {
				t.Fatalf("read transparent body: %v", readErr)
			}
			if response.Header.Get("Content-Type") != "text/event-stream" || string(gotBody) != body {
				t.Fatalf("transparent response = %q, Content-Type %q", gotBody, response.Header.Get("Content-Type"))
			}
		})
	}
}

func TestProxyCancelsChatSSEAggregationPromptly(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(response, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"held\"}}]}\n\n")
		if flusher, ok := response.(http.Flusher); ok {
			flusher.Flush()
		}
		close(started)
		<-request.Context().Done()
		close(cancelled)
	}))
	t.Cleanup(upstream.Close)
	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)

	requestContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{"stream":false}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	done := make(chan struct{})
	go func() {
		response, _ := http.DefaultClient.Do(request)
		if response != nil {
			response.Body.Close()
		}
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("upstream did not start aggregation stream")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("upstream did not observe aggregation cancellation")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("gateway client did not finish after aggregation cancellation")
	}
}

func TestProxyAggregationFailureIsControlledAndDoesNotCommitUpstreamResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		response.Header().Set("Authorization", "Bearer upstream-secret")
		_, _ = io.WriteString(response, "data: {not-json}\n\n")
	}))
	t.Cleanup(upstream.Close)

	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)
	request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{"stream":false}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", response.StatusCode)
	}
	if strings.Contains(string(body), "not-json") || strings.Contains(string(body), "upstream-secret") || strings.Contains(string(body), "malformed") {
		t.Fatalf("aggregation details leaked: %q", body)
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
		path                 string
		contentType          string
		body                 io.Reader
		wantBody             string
		wantContentLength    int64
		wantTransferEncoding string
	}{
		{
			name:              "known length",
			path:              "/v1/responses",
			contentType:       "application/octet-stream",
			body:              strings.NewReader("known request body"),
			wantBody:          "known request body",
			wantContentLength: int64(len("known request body")),
		},
		{
			name:              "empty body",
			path:              "/v1/chat/completions",
			contentType:       "application/json",
			body:              http.NoBody,
			wantContentLength: 0,
		},
		{
			name:                 "unknown length",
			path:                 "/v1/responses",
			contentType:          "application/octet-stream",
			body:                 io.NopCloser(strings.NewReader("streamed request body")),
			wantBody:             "streamed request body",
			wantContentLength:    -1,
			wantTransferEncoding: "chunked",
		},
		{
			name:              "inspected known length",
			path:              "/v1/chat/completions",
			contentType:       "application/json",
			body:              strings.NewReader(`{"model":"gpt-test"}`),
			wantBody:          `{"model":"gpt-test"}`,
			wantContentLength: int64(len(`{"model":"gpt-test"}`)),
		},
		{
			name:                 "inspected unknown length",
			path:                 "/v1/chat/completions",
			contentType:          "application/json",
			body:                 io.NopCloser(strings.NewReader(`{"model":"gpt-test"}`)),
			wantBody:             `{"model":"gpt-test"}`,
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

			request, err := http.NewRequest(http.MethodPost, gateway.URL+test.path, test.body)
			if err != nil {
				t.Fatalf("create request: %v", err)
			}
			request.Header.Set("Content-Type", test.contentType)
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatalf("POST %s: %v", test.path, err)
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
