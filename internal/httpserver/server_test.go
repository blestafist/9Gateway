package httpserver

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewHandlerAcceptsHTTPRequests(t *testing.T) {
	server := httptest.NewServer(NewHandler())
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
	server := httptest.NewServer(NewHandler())
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
	server := httptest.NewServer(NewHandler())
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
