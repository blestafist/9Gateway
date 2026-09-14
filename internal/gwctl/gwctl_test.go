package gwctl

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/pestit/9gateway/internal/httpserver"
	"github.com/pestit/9gateway/internal/storage"
	"github.com/pestit/9gateway/internal/transport"
)

func TestRunVersionHelpAndUnknownCommand(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		status     int
		stdoutPart string
		stderrPart string
	}{
		{name: "version ignores gateway and credential", args: []string{"--gateway-url", ":not-a-url", "version"}, status: ExitSuccess, stdoutPart: "gwctl version "},
		{name: "help", args: []string{"--help"}, status: ExitSuccess, stdoutPart: "Usage: gwctl", stderrPart: ""},
		{name: "help after command", args: []string{"ping", "--help"}, status: ExitSuccess, stdoutPart: "Commands:"},
		{name: "unknown", args: []string{"wat"}, status: ExitUsage, stderrPart: "unknown command"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			status := Run(context.Background(), test.args, &stdout, &stderr)
			if status != test.status {
				t.Fatalf("status = %d, want %d (stdout %q, stderr %q)", status, test.status, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String(), test.stdoutPart) {
				t.Fatalf("stdout = %q, want %q", stdout.String(), test.stdoutPart)
			}
			if !strings.Contains(stderr.String(), test.stderrPart) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), test.stderrPart)
			}
		})
	}
}

func TestRequestsUsageErrorsDoNotReflectArguments(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "unknown subcommand", args: []string{"requests", "invalid-subcommand-canary"}},
		{name: "unknown get option", args: []string{"requests", "get", "0123456789abcdef0123456789abcdef", "--invalid-option-canary"}},
		{name: "unknown list option", args: []string{"requests", "list", "--invalid-list-option-canary"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if status := Run(context.Background(), test.args, &stdout, &stderr); status != ExitUsage {
				t.Fatalf("status = %d, want usage (stdout %q, stderr %q)", status, stdout.String(), stderr.String())
			}
			if strings.Contains(stderr.String(), "canary") || strings.Contains(stderr.String(), test.args[len(test.args)-1]) {
				t.Fatalf("usage error reflected raw argument: %q", stderr.String())
			}
		})
	}
}

func TestRunPingAgainstRealGateway(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	handler, err := newAdminTestGateway(database, "integration-admin")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	var stdout, stderr bytes.Buffer
	status := Run(context.Background(), []string{"--gateway-url", server.URL, "--admin-credential", "integration-admin", "ping"}, &stdout, &stderr)
	if status != ExitSuccess {
		t.Fatalf("status = %d, stdout %q, stderr %q", status, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "authentication succeeded") || stderr.Len() != 0 {
		t.Fatalf("stdout/stderr = %q/%q", stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	status = Run(context.Background(), []string{"--gateway-url", server.URL, "--admin-credential", "wrong-secret", "ping"}, &stdout, &stderr)
	if status != ExitAPI || !strings.Contains(stderr.String(), "authentication failed") || stdout.Len() != 0 {
		t.Fatalf("wrong credential result = %d/%q/%q", status, stdout.String(), stderr.String())
	}
}

func TestRunPingAuthenticationNetworkAndValidationErrors(t *testing.T) {
	t.Setenv("GWCTL_ADMIN_CREDENTIAL", "")
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/admin/v1/keys" || request.URL.RawQuery != "limit=1" {
			t.Errorf("request target = %s?%s", request.URL.Path, request.URL.RawQuery)
		}
		response.WriteHeader(http.StatusUnauthorized)
	}))
	serverURL := server.URL
	defer server.Close()

	for _, test := range []struct {
		name       string
		args       []string
		status     int
		stderrPart string
	}{
		{name: "wrong credential", args: []string{"--gateway-url", serverURL, "--admin-credential", "wrong-secret", "ping"}, status: ExitAPI, stderrPart: "authentication failed"},
		{name: "connection", args: []string{"--gateway-url", "http://127.0.0.1:1", "--admin-credential", "network-secret", "ping"}, status: ExitAPI, stderrPart: "connection failed"},
		{name: "missing credential", args: []string{"--gateway-url", "http://127.0.0.1:1", "ping"}, status: ExitUsage, stderrPart: "admin credential is required"},
		{name: "bad URL", args: []string{"--gateway-url", "file:///tmp/gateway", "--admin-credential", "secret", "ping"}, status: ExitUsage, stderrPart: "HTTP or HTTPS"},
		{name: "URL query", args: []string{"--gateway-url", "http://localhost:8080/base?secret=x", "--admin-credential", "secret", "ping"}, status: ExitUsage, stderrPart: "query"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			status := Run(context.Background(), test.args, &stdout, &stderr)
			if status != test.status {
				t.Fatalf("status = %d, want %d (stdout %q, stderr %q)", status, test.status, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), test.stderrPart) || stdout.Len() != 0 {
				t.Fatalf("stdout/stderr = %q/%q", stdout.String(), stderr.String())
			}
			if strings.Contains(stderr.String(), "wrong-secret") || strings.Contains(stderr.String(), "network-secret") {
				t.Fatalf("credential leaked in stderr: %q", stderr.String())
			}
		})
	}
}

func TestRunPingUsesEnvironmentCredentialAndExplicitPrecedence(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	handler, err := newAdminTestGateway(database, "explicit")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	t.Setenv("GWCTL_ADMIN_CREDENTIAL", "explicit")
	var stdout, stderr bytes.Buffer
	if status := Run(context.Background(), []string{"--gateway-url", server.URL, "ping"}, &stdout, &stderr); status != ExitSuccess {
		t.Fatalf("environment credential status = %d, stdout %q, stderr %q", status, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if status := Run(context.Background(), []string{"--gateway-url", server.URL, "--admin-credential", "wrong", "ping"}, &stdout, &stderr); status != ExitAPI || !strings.Contains(stderr.String(), "authentication failed") {
		t.Fatalf("explicit credential result = %d/%q/%q", status, stdout.String(), stderr.String())
	}
}

func TestValidateBaseURLAndJoinURLPreserveBasePath(t *testing.T) {
	for _, raw := range []string{"", "localhost:8080", "ftp://localhost", "http://localhost/path?x=1", "http://user:pass@localhost/path", "http://localhost/path#fragment"} {
		if _, err := validateBaseURL(raw); err == nil {
			t.Errorf("validateBaseURL(%q) succeeded", raw)
		}
	}
	base, err := validateBaseURL("http://localhost:8080/tenant/api/")
	if err != nil {
		t.Fatal(err)
	}
	joined := joinURL(base, adminKeysPath)
	if got, want := joined.String(), "http://localhost:8080/tenant/api/admin/v1/keys?limit=1"; got != want {
		t.Fatalf("joined URL = %q, want %q", got, want)
	}
}

func newAdminTestGateway(database *storage.DB, credential string) (http.Handler, error) {
	return httpserver.NewHandlerWithAdmin(transport.NewClient(), "http://127.0.0.1:1", "upstream", credential, "pepper", storage.NewAPIKeyRepository(database))
}

func TestRunKeysListAggregatesPagesAndPreservesJSON(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.URL.RawQuery)
		if request.Header.Get("Authorization") != "Bearer admin" {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		page := 0
		if request.URL.Query().Get("cursor") == "cursor-1" {
			page = 1
		}
		if request.URL.Query().Get("cursor") == "cursor-2" {
			page = 2
		}
		items := make([]map[string]any, 0, 50)
		for index := 0; index < 50; index++ {
			id := "key-" + strconv.Itoa(page*50+index)
			items = append(items, map[string]any{"id": id, "name": "name", "display_prefix": "gw", "enabled": true, "created_at": "2024-01-02T03:04:05Z", "custom": nil})
		}
		body := map[string]any{"keys": items}
		if page < 2 {
			body["next_cursor"] = "cursor-" + strconv.Itoa(page+1)
		}
		_ = json.NewEncoder(response).Encode(body)
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	status := Run(context.Background(), []string{"--gateway-url", server.URL, "--admin-credential", "admin", "keys", "list", "--json"}, &stdout, &stderr)
	if status != ExitSuccess {
		t.Fatalf("status = %d, stdout %q, stderr %q", status, stdout.String(), stderr.String())
	}
	var result struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("JSON output: %v (%q)", err, stdout.String())
	}
	if len(result.Keys) != 150 || len(requests) != 3 {
		t.Fatalf("keys/requests = %d/%d, want 150/3 (%v)", len(result.Keys), len(requests), requests)
	}
	if requests[0] != "limit=50" || requests[1] != "cursor=cursor-1&limit=50" || requests[2] != "cursor=cursor-2&limit=50" {
		t.Fatalf("queries = %v", requests)
	}
	if !strings.Contains(stderr.String(), "Fetching additional key pages") || !strings.Contains(stderr.String(), "Fetching key page 3") {
		t.Fatalf("progress = %q", stderr.String())
	}
	if result.Keys[0]["custom"] != nil {
		t.Fatalf("JSON field semantics were not retained: %#v", result.Keys[0])
	}
}

func TestRunKeysListLimitAndEmptyHumanOutput(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		queries = append(queries, request.URL.RawQuery)
		if request.URL.Query().Get("empty") == "1" {
			_, _ = response.Write([]byte(`{"keys":[]}`))
			return
		}
		_, _ = response.Write([]byte(`{"keys":[{"id":"short","name":"demo","display_prefix":"gw-","enabled":false,"created_at":"2024-01-02T03:04:05Z"}],"next_cursor":"ignored"}`))
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	status := Run(context.Background(), []string{"--gateway-url", server.URL, "--admin-credential", "admin", "keys", "list", "--limit", "1"}, &stdout, &stderr)
	if status != ExitSuccess || !strings.Contains(stdout.String(), "ID") || !strings.Contains(stdout.String(), "short") || stderr.Len() != 0 {
		t.Fatalf("limit output = %d/%q/%q", status, stdout.String(), stderr.String())
	}
	if len(queries) != 1 || queries[0] != "limit=1" {
		t.Fatalf("limit query = %v", queries)
	}
}

func TestRunKeysGetHumanJSONAndErrors(t *testing.T) {
	id := "key-safe_123"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/admin/v1/keys/" + id:
			_, _ = response.Write([]byte(`{"id":"key-safe_123","name":"demo","display_prefix":"gw","enabled":true,"created_at":"2024-01-02T03:04:05Z","updated_at":"2024-01-03T03:04:05Z","expires_at":null,"policy":{"allowed_models":["gpt-*"],"denied_models":null,"request_windows":[{"amount":2,"duration":60}],"token_windows":null,"token_mode":"usage_only","max_concurrent_requests":3,"budget_limits":[{"period":"day","amount_micros":42}],"log_request_body":true,"log_response_body":false}}`))
		case "/admin/v1/keys/missing":
			response.WriteHeader(http.StatusNotFound)
		case "/admin/v1/keys/auth":
			response.WriteHeader(http.StatusUnauthorized)
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	status := Run(context.Background(), []string{"--gateway-url", server.URL, "--admin-credential", "admin", "keys", "get", id}, &stdout, &stderr)
	if status != ExitSuccess || !strings.Contains(stdout.String(), "Request limits") || !strings.Contains(stdout.String(), "Allow models") || stderr.Len() != 0 {
		t.Fatalf("human get = %d/%q/%q", status, stdout.String(), stderr.String())
	}
	stdout.Reset()
	status = Run(context.Background(), []string{"--gateway-url", server.URL, "--admin-credential", "admin", "keys", "get", id, "--json"}, &stdout, &stderr)
	if status != ExitSuccess {
		t.Fatalf("JSON get status = %d (%q)", status, stderr.String())
	}
	var raw map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil || raw["expires_at"] != nil {
		t.Fatalf("JSON get = %v/%q", err, stdout.String())
	}
	for _, test := range []struct {
		id, want string
	}{
		{id: "missing", want: "Key not found"},
		{id: "auth", want: "Authentication failed"},
	} {
		stdout.Reset()
		stderr.Reset()
		status = Run(context.Background(), []string{"--gateway-url", server.URL, "--admin-credential", "admin", "keys", "get", test.id}, &stdout, &stderr)
		if status != ExitAPI || !strings.Contains(stderr.String(), test.want) || stdout.Len() != 0 {
			t.Fatalf("%s = %d/%q/%q", test.id, status, stdout.String(), stderr.String())
		}
	}
	stdout.Reset()
	stderr.Reset()
	if status = Run(context.Background(), []string{"--gateway-url", server.URL, "--admin-credential", "admin", "keys", "get", "bad/id"}, &stdout, &stderr); status != ExitUsage {
		t.Fatalf("unsafe ID status = %d/%q", status, stderr.String())
	}
}
