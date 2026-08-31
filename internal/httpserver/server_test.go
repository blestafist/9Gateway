package httpserver

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/transport"
)

func TestNewHandlerAcceptsHTTPRequests(t *testing.T) {
	server := httptest.NewServer(NewHandler(transport.NewClient(), "http://router.example.test"))
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
	server := httptest.NewServer(NewHandler(transport.NewClient(), "http://router.example.test"))
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
	server := httptest.NewServer(NewHandler(transport.NewClient(), "http://router.example.test"))
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

	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL))
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

	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL))
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

func healthHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health)
	return mux
}
