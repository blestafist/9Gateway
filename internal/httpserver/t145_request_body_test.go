package httpserver

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/pestit/9gateway/internal/storage"
	"github.com/pestit/9gateway/internal/transport"
)

func TestT145GetRequestBodyServesExactBytesAndHeaders(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	id := "0123456789abcdef0123456789abcdef"
	if _, err := database.Exec(`INSERT INTO requests (request_id, terminal_outcome, upstream_started) VALUES (?, 'complete', 1)`, id); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		kind     string
		body     []byte
		original int64
		trunc    bool
	}{
		{"client_request", []byte("{\"model\":\"x\"}"), 13, false},
		{"upstream_request", []byte("data: [DONE]\n\n"), int64(len([]byte("data: [DONE]\n\n"))), false},
		{"response", []byte{0x1f, 0x8b, 0x00, 0xff, 0x80}, 10, true},
	}
	for _, test := range cases {
		if _, err := database.Exec(`INSERT INTO request_bodies (request_id, body_kind, body, original_size, truncated) VALUES (?, ?, ?, ?, ?)`, id, test.kind, test.body, test.original, test.trunc); err != nil {
			t.Fatalf("insert %s: %v", test.kind, err)
		}
	}
	handler, err := NewHandlerWithAdmin(transport.NewClient(), "http://127.0.0.1:1", "upstream", "admin-secret", "pepper", storage.NewAPIKeyRepository(database))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	for _, test := range cases {
		request, err := http.NewRequest(http.MethodGet, server.URL+"/admin/v1/requests/"+id+"/bodies/"+test.kind, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer admin-secret")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/octet-stream" || response.Header.Get("X-Original-Size") != strconv.FormatInt(test.original, 10) || response.Header.Get("X-Truncated") != strconv.FormatBool(test.trunc) || !bytes.Equal(body, test.body) {
			t.Fatalf("%s response = status %d headers %#v body %v", test.kind, response.StatusCode, response.Header, body)
		}
	}
}

func TestT145GetRequestBodyRejectsBadPathsAndAuth(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	id := "0123456789abcdef0123456789abcdef"
	if _, err := database.Exec(`INSERT INTO requests (request_id, upstream_started) VALUES (?, 0)`, id); err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandlerWithAdmin(transport.NewClient(), "http://127.0.0.1:1", "upstream", "admin-secret", "pepper", storage.NewAPIKeyRepository(database))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	for _, test := range []struct {
		path   string
		auth   string
		status int
	}{
		{"/admin/v1/requests/" + id + "/bodies/nope", "Bearer admin-secret", http.StatusBadRequest},
		{"/admin/v1/requests/not-an-id/bodies/response", "Bearer admin-secret", http.StatusBadRequest},
		{"/admin/v1/requests/ffffffffffffffffffffffffffffffff/bodies/response", "Bearer admin-secret", http.StatusNotFound},
		{"/admin/v1/requests/" + id + "/bodies/response", "", http.StatusUnauthorized},
		{"/admin/v1/requests/" + id + "/bodies/response", "Bearer wrong", http.StatusUnauthorized},
	} {
		request, err := http.NewRequest(http.MethodGet, server.URL+test.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if test.auth != "" {
			request.Header.Set("Authorization", test.auth)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != test.status {
			t.Fatalf("path %q status = %d, want %d", test.path, response.StatusCode, test.status)
		}
	}
}
