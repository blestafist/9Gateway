package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRunHealthcheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/ready" {
			t.Fatalf("request = %s %s, want GET /ready", request.Method, request.URL.Path)
		}
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if err := runHealthcheck([]string{"--address", server.URL}); err != nil {
		t.Fatalf("runHealthcheck() error = %v", err)
	}
}

func TestRunHealthcheckRejectsUnreadyServer(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	if err := runHealthcheck([]string{"--address", server.URL}); err == nil {
		t.Fatal("runHealthcheck() error = nil, want non-200 failure")
	}
}

func TestReadinessURL(t *testing.T) {
	tests := []struct {
		address string
		want    string
	}{
		{address: ":8080", want: "http://127.0.0.1:8080/ready"},
		{address: "127.0.0.1:9000", want: "http://127.0.0.1:9000/ready"},
		{address: "http://localhost:8080/health?ignored=true", want: "http://localhost:8080/ready"},
	}
	for _, test := range tests {
		t.Run(test.address, func(t *testing.T) {
			got, err := readinessURL(test.address)
			if err != nil {
				t.Fatalf("readinessURL() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("readinessURL() = %q, want %q", got, test.want)
			}
		})
	}
}
