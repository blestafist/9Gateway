package httpserver

import (
	"net/http"
	"net/http/httptest"
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
