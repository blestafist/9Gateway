package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/storage"
	"github.com/pestit/9gateway/internal/transport"
)

func TestT141T143HTTPPaginationUsesInitialInsertionSnapshot(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	keys := storage.NewAPIKeyRepository(database)
	when := time.Unix(1_700_000_000, 0).UTC()
	for _, id := range []string{"key-a", "key-b", "key-c"} {
		generated, err := auth.GenerateGatewayKey([]byte("pagination-" + id))
		if err != nil {
			t.Fatal(err)
		}
		if err := keys.Insert(context.Background(), storage.APIKeyRecord{ID: id, Name: id, DisplayPrefix: generated.DisplayPrefix, Digest: generated.Digest, Enabled: true, CreatedAt: when, UpdatedAt: when, PolicyJSON: `{}`}); err != nil {
			t.Fatal(err)
		}
	}
	history := storage.NewRequestHistoryRepository(database)
	for index, id := range []string{"00000000000000000000000000000001", "00000000000000000000000000000002", "00000000000000000000000000000003"} {
		if err := history.Persist(context.Background(), storage.HistoryRecord{RequestID: id, APIKeyID: "key-a", KeyName: "key-a", Method: "GET", Path: "/v1/models", Route: "models", TerminalOutcome: "complete", UpstreamStarted: true, FinishedAt: storage.KnownInt64(20), StartedAt: storage.KnownInt64(19 - int64(index))}, nil); err != nil {
			t.Fatal(err)
		}
	}
	handler, err := NewHandlerWithAdmin(transport.NewClient(), "http://127.0.0.1:1", "upstream", "admin-secret", "pepper", keys)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	get := func(path string) map[string]any {
		request, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
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
		decodeResponse(t, response, &body)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d", path, response.StatusCode)
		}
		return body
	}
	keysPage := get("/admin/v1/keys?limit=2")
	keyCursor := keysPage["next_cursor"].(string)
	generated, err := auth.GenerateGatewayKey([]byte("pagination-new"))
	if err != nil {
		t.Fatal(err)
	}
	if err := keys.Insert(context.Background(), storage.APIKeyRecord{ID: "key-b5", Name: "new", DisplayPrefix: generated.DisplayPrefix, Digest: generated.Digest, Enabled: true, CreatedAt: when, UpdatedAt: when, PolicyJSON: `{}`}); err != nil {
		t.Fatal(err)
	}
	keysPage2 := get("/admin/v1/keys?limit=2&cursor=" + keyCursor)
	if got := keysPage2["keys"].([]any); len(got) != 1 || got[0].(map[string]any)["id"] != "key-a" {
		t.Fatalf("key page 2 = %#v", keysPage2)
	}
	requestsPage := get("/admin/v1/requests?limit=2")
	requestCursor := requestsPage["next_cursor"].(string)
	if err := history.Persist(context.Background(), storage.HistoryRecord{RequestID: "00000000000000000000000000000025", APIKeyID: "key-a", KeyName: "key-a", Method: "GET", Path: "/v1/models", Route: "models", TerminalOutcome: "complete", UpstreamStarted: true, FinishedAt: storage.KnownInt64(20), StartedAt: storage.KnownInt64(18)}, nil); err != nil {
		t.Fatal(err)
	}
	requestsPage2 := get("/admin/v1/requests?limit=2&cursor=" + requestCursor)
	if got := requestsPage2["requests"].([]any); len(got) != 1 || got[0].(map[string]any)["request_id"] != "00000000000000000000000000000001" {
		t.Fatalf("request page 2 = %#v", requestsPage2)
	}
}

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

func TestT143RequestHTTPCursorFilterBindingAndExpiry(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	keys := storage.NewAPIKeyRepository(database)
	for _, id := range []string{"key-a", "key-b"} {
		generated, err := auth.GenerateGatewayKey([]byte("cursor-" + id))
		if err != nil {
			t.Fatal(err)
		}
		if err := keys.Insert(context.Background(), storage.APIKeyRecord{ID: id, Name: id, DisplayPrefix: generated.DisplayPrefix, Digest: generated.Digest, Enabled: true, CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(1, 0).UTC(), PolicyJSON: `{}`}); err != nil {
			t.Fatal(err)
		}
	}
	history := storage.NewRequestHistoryRepository(database)
	ids := []string{"11111111111111111111111111111111", "22222222222222222222222222222222", "33333333333333333333333333333333"}
	for index, finished := range []int64{30, 20, 10} {
		if err := history.Persist(context.Background(), storage.HistoryRecord{RequestID: ids[index], APIKeyID: "key-a", KeyName: "key-a", Method: "GET", Path: "/v1/models", Route: "models", TerminalOutcome: "complete", UpstreamStarted: true, FinishedAt: storage.KnownInt64(finished), StartedAt: storage.KnownInt64(finished - 1)}, nil); err != nil {
			t.Fatal(err)
		}
	}
	handler, err := NewHandlerWithAdmin(transport.NewClient(), "http://127.0.0.1:1", "upstream", "admin-secret", "pepper", keys)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	do := func(path string) (int, map[string]any) {
		request, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
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
		decodeResponse(t, response, &body)
		return response.StatusCode, body
	}
	base := "/admin/v1/requests?limit=1&key_id=key-a&after=1970-01-01T00%3A00%3A00.000015Z&before=1970-01-01T00%3A00%3A00.000040Z"
	status, first := do(base)
	if status != http.StatusOK {
		t.Fatalf("first page status/body = %d/%#v", status, first)
	}
	cursor, ok := first["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatalf("first page cursor = %#v", first)
	}
	continued := "/admin/v1/requests?limit=1&key_id=key-a&after=" + url.QueryEscape("1970-01-01T02:00:00.000015+02:00") + "&before=" + url.QueryEscape("1970-01-01T02:00:00.000040+02:00") + "&cursor=" + url.QueryEscape(cursor)
	status, second := do(continued)
	if status != http.StatusOK || len(second["requests"].([]any)) != 1 {
		t.Fatalf("equivalent normalized continuation = %d/%#v", status, second)
	}
	for name, filter := range map[string]string{
		"key":    "key-b",
		"after":  "1970-01-01T00:00:00.000016Z",
		"before": "1970-01-01T00:00:00.000025Z",
	} {
		path := "/admin/v1/requests?limit=1&key_id=key-a&after=1970-01-01T00%3A00%3A00.000015Z&before=1970-01-01T00%3A00%3A00.000040Z&cursor=" + url.QueryEscape(cursor)
		if name == "key" {
			path = "/admin/v1/requests?limit=1&key_id=key-b&after=1970-01-01T00%3A00%3A00.000015Z&before=1970-01-01T00%3A00%3A00.000040Z&cursor=" + url.QueryEscape(cursor)
		} else if name == "after" {
			path = "/admin/v1/requests?limit=1&key_id=key-a&after=" + url.QueryEscape(filter) + "&before=1970-01-01T00%3A00%3A00.000040Z&cursor=" + url.QueryEscape(cursor)
		} else {
			path = "/admin/v1/requests?limit=1&key_id=key-a&after=1970-01-01T00%3A00%3A00.000015Z&before=" + url.QueryEscape(filter) + "&cursor=" + url.QueryEscape(cursor)
		}
		status, body := do(path)
		if status != http.StatusBadRequest || body["error"].(map[string]any)["code"] != "invalid_request" {
			t.Errorf("%s mismatch = %d/%#v", name, status, body)
		}
	}
	if _, err := database.Exec(`DELETE FROM requests WHERE request_id = ?`, ids[0]); err != nil {
		t.Fatal(err)
	}
	status, body := do("/admin/v1/requests?limit=1&key_id=key-a&after=1970-01-01T00%3A00%3A00.000015Z&before=1970-01-01T00%3A00%3A00.000040Z&cursor=" + url.QueryEscape(cursor))
	if status != http.StatusBadRequest || body["error"].(map[string]any)["code"] != "cursor_expired" {
		t.Fatalf("expired cursor = %d/%#v", status, body)
	}
}

func containsFold(value, needle string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(needle))
}
