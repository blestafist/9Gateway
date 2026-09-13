package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/storage"
	"github.com/pestit/9gateway/internal/transport"
)

func TestT143ListRequestsHTTPIsAuthenticatedAndMetadataOnly(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	keys := storage.NewAPIKeyRepository(database)
	generated, err := auth.GenerateGatewayKey([]byte("pepper"))
	if err != nil {
		t.Fatal(err)
	}
	if err := keys.Insert(context.Background(), storage.APIKeyRecord{ID: "key-0123456789abcdef0123456789abcdef", Name: "alpha", DisplayPrefix: generated.DisplayPrefix, Digest: generated.Digest, Enabled: true, CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(1, 0).UTC(), PolicyJSON: `{}`}); err != nil {
		t.Fatal(err)
	}
	history := storage.NewRequestHistoryRepository(database)
	if err := history.Persist(context.Background(), storage.HistoryRecord{
		RequestID: "0123456789abcdef0123456789abcdef", APIKeyID: "key-0123456789abcdef0123456789abcdef", KeyName: "alpha",
		Method: "POST", Path: "/v1/chat/completions", Route: "chat_completions", Model: "model",
		RequestedMode: "json", UpstreamMode: "sse", DeliveredMode: "json", TerminalOutcome: "complete", UpstreamStarted: true,
		DownstreamStatus: storage.KnownInt64(200), InputTokens: storage.KnownInt64(0), CostMicros: storage.KnownInt64(0),
	}, nil); err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandlerWithAdmin(transport.NewClient(), "http://127.0.0.1:1", "upstream", "admin-secret", "pepper", keys)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	request, err := http.NewRequest(http.MethodGet, server.URL+"/admin/v1/requests?limit=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer admin-secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body struct {
		Requests []map[string]any `json:"requests"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || len(body.Requests) != 1 {
		t.Fatalf("response status/body = %d/%#v", response.StatusCode, body)
	}
	if body.Requests[0]["input_tokens"] != float64(0) || body.Requests[0]["cost_micros"] != float64(0) || body.Requests[0]["output_tokens"] != nil {
		t.Fatalf("null versus zero response = %#v", body.Requests[0])
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"body", "authorization", "digest", "pepper", "policy_json"} {
		if containsFold(string(encoded), forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, encoded)
		}
	}
	for _, credential := range []string{"", "Bearer wrong", "Bearer upstream"} {
		unauthenticated := request.Clone(context.Background())
		unauthenticated.Header.Set("Authorization", credential)
		if credential == "" {
			unauthenticated.Header.Del("Authorization")
		}
		got, err := http.DefaultClient.Do(unauthenticated)
		if err != nil {
			t.Fatal(err)
		}
		got.Body.Close()
		if got.StatusCode != http.StatusUnauthorized {
			t.Fatalf("credential %q status = %d", credential, got.StatusCode)
		}
	}
}

func containsFold(value, needle string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(needle))
}
