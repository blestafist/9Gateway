package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pestit/9gateway/internal/storage"
	"github.com/pestit/9gateway/internal/transport"
)

func TestT144GetRequestHTTPAuthValidationAndBodyKinds(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	id := "0123456789abcdef0123456789abcdef"
	if _, err := database.Exec(`INSERT INTO requests (request_id, terminal_outcome, upstream_started) VALUES (?, 'pre_upstream', 0)`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO request_bodies (request_id, body_kind, body, original_size, truncated) VALUES (?, 'client_request', ?, 6, 0)`, id, []byte("secret")); err != nil {
		t.Fatal(err)
	}
	keys := storage.NewAPIKeyRepository(database)
	handler, err := NewHandlerWithAdmin(transport.NewClient(), "http://127.0.0.1:1", "upstream", "admin-secret", "pepper", keys)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	request, err := http.NewRequest(http.MethodGet, server.URL+"/admin/v1/requests/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer admin-secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || body["request_id"] != id {
		t.Fatalf("response = %d/%#v", response.StatusCode, body)
	}
	if got, ok := body["has_bodies"].([]any); !ok || len(got) != 1 || got[0] != "client_request" {
		t.Fatalf("has_bodies = %#v", body["has_bodies"])
	}
	encoded, _ := json.Marshal(body)
	if containsFold(string(encoded), "secret") || containsFold(string(encoded), "body") && containsFold(string(encoded), "body_bytes") {
		t.Fatalf("response leaked body data: %s", encoded)
	}
	for _, path := range []string{"/admin/v1/requests/not-an-id", "/admin/v1/requests/ffffffffffffffffffffffffffffffff"} {
		bad := request.Clone(context.Background())
		bad.URL.Path = path
		got, err := http.DefaultClient.Do(bad)
		if err != nil {
			t.Fatal(err)
		}
		got.Body.Close()
		want := http.StatusBadRequest
		if path != "/admin/v1/requests/not-an-id" {
			want = http.StatusNotFound
		}
		if got.StatusCode != want {
			t.Fatalf("path %q status = %d", path, got.StatusCode)
		}
	}
	unauthenticated := request.Clone(context.Background())
	unauthenticated.Header.Del("Authorization")
	got, err := http.DefaultClient.Do(unauthenticated)
	if err != nil {
		t.Fatal(err)
	}
	got.Body.Close()
	if got.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", got.StatusCode)
	}
}
