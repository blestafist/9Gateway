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
