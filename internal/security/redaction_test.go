package security_test

// This is intentionally an integration-shaped audit rather than a unit test
// of one formatter. Credentials can cross package boundaries through request
// setup, storage projections, HTTP wrappers, telemetry, and the CLI.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/gwctl"
	"github.com/pestit/9gateway/internal/httpserver"
	"github.com/pestit/9gateway/internal/observability"
	"github.com/pestit/9gateway/internal/storage"
	"github.com/pestit/9gateway/internal/transport"
)

type auditCanaries struct {
	gatewayRaw string
	admin      string
	pepper     string
	upstream   string
	sqlite     string
	body       string
	query      string
}

// A raw issued key's display prefix is an explicitly safe admin projection.
// The scanner therefore checks the whole credential and the non-prefix
// material, while not flagging the stable prefix returned by key list/detail.
func newAuditCanaries(raw string) auditCanaries {
	return auditCanaries{
		gatewayRaw: raw,
		admin:      "ADMIN-CANARY-9gateway-audit-7f4d",
		pepper:     "PEPPER-CANARY-9gateway-audit-2b91",
		upstream:   "UPSTREAM-CANARY-9gateway-audit-c3e8",
		sqlite:     "SQLITE-PASSWORD-CANARY-9gateway-audit-5a62",
		body:       "BODY-MOCK-SECRET-CANARY-9gateway-audit-81de",
		query:      "QUERY-USER-DATA-CANARY-9gateway-audit-44af",
	}
}

type auditSurface struct {
	name    string
	body    []byte
	headers http.Header
}

type canaryScanner struct {
	values []canaryValue
}

type canaryValue struct {
	label string
	value string
}

func newCanaryScanner(canaries auditCanaries) canaryScanner {
	values := []canaryValue{{"gateway key", canaries.gatewayRaw}, {"admin credential", canaries.admin}, {"auth pepper", canaries.pepper}, {"upstream key", canaries.upstream}, {"sqlite secret", canaries.sqlite}, {"body secret", canaries.body}, {"query data", canaries.query}}
	// Meaningful fragments catch truncation, concatenation, and URL/header
	// formatting bugs without treating the safe gateway display prefix as raw
	// key material.
	if len(canaries.gatewayRaw) > len(auth.GatewayKeyNamespace)+auth.GatewayKeyDisplayPrefixLength+12 {
		start := len(auth.GatewayKeyNamespace) + auth.GatewayKeyDisplayPrefixLength
		values = append(values, canaryValue{"gateway key fragment", canaries.gatewayRaw[start : start+12]}, canaryValue{"gateway key suffix", canaries.gatewayRaw[len(canaries.gatewayRaw)-12:]})
	}
	for _, item := range []canaryValue{{"admin fragment", canaries.admin}, {"pepper fragment", canaries.pepper}, {"upstream fragment", canaries.upstream}, {"sqlite fragment", canaries.sqlite}, {"body fragment", canaries.body}, {"query fragment", canaries.query}} {
		values = append(values, canaryValue{item.label, item.value[:12]}, canaryValue{item.label + " suffix", item.value[len(item.value)-12:]})
	}
	return canaryScanner{values: values}
}

func (scanner canaryScanner) check(t *testing.T, surface auditSurface) {
	t.Helper()
	var rendered strings.Builder
	rendered.Write(surface.body)
	for name, values := range surface.headers {
		rendered.WriteString(name)
		for _, value := range values {
			rendered.WriteString(value)
		}
	}
	text := rendered.String()
	for _, item := range scanner.values {
		if item.value != "" && strings.Contains(text, item.value) {
			if surface.name == "structured slog output" {
				for index, line := range strings.Split(text, "\n") {
					if strings.Contains(line, item.value) {
						var record map[string]any
						_ = json.Unmarshal([]byte(line), &record)
						for key, value := range record {
							if strings.Contains(valueString(value), item.value) {
								t.Fatalf("%s contains %s canary in field %s on log line %d", surface.name, item.label, key, index)
							}
						}
						t.Fatalf("%s contains %s canary on log line %d", surface.name, item.label, index)
					}
				}
			}
			t.Fatalf("%s contains %s canary", surface.name, item.label)
		}
	}
}

func valueString(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func auditRequest(t *testing.T, client *http.Client, method, target string, body io.Reader, headers map[string]string) auditSurface {
	t.Helper()
	request, err := http.NewRequest(method, target, body)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return auditSurface{name: method + " " + target, body: data, headers: response.Header.Clone()}
}

func auditAdminRequest(t *testing.T, gatewayURL, credential, method, path string, body io.Reader) auditSurface {
	return auditRequest(t, http.DefaultClient, method, gatewayURL+path, body, map[string]string{"Authorization": "Bearer " + credential, "Content-Type": "application/json"})
}

func TestSecretRedaction(t *testing.T) {
	var logs bytes.Buffer
	upstreamCalls := atomic.Int64{}
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		upstreamCalls.Add(1)
		if request.Header.Get("X-Audit-Case") == "timeout" {
			<-request.Context().Done()
			return
		}
		if request.Header.Get("X-Audit-Case") == "error" {
			response.Header().Set("Content-Type", "application/json")
			response.Header().Set("Authorization", "Bearer "+auditTestCanaries.upstream)
			response.Header().Set("X-Api-Key", auditTestCanaries.upstream)
			response.Header().Set("X-Upstream-Api-Key", auditTestCanaries.upstream)
			response.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(response, `{"error":"provider echoed `+auditTestCanaries.body+`"}`)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("X-Upstream-Trace", "safe-trace")
		_, _ = io.WriteString(response, `{"ok":true,"response_secret":"`+auditTestCanaries.body+`"}`)
	}))
	defer upstream.Close()

	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	keys := storage.NewAPIKeyRepository(database)
	history := storage.NewRequestHistoryRepository(database)
	logger := httpserver.NewCompletionLogger(slog.New(slog.NewJSONHandler(&logs, nil)), 128)
	defer logger.Shutdown(context.Background())

	// Create the first key through the only endpoint allowed to reveal its raw
	// credential. That creation response is a deliberate trusted exception.
	admin := "ADMIN-CANARY-9gateway-audit-7f4d"
	pepper := "PEPPER-CANARY-9gateway-audit-2b91"
	upstreamSecret := "UPSTREAM-CANARY-9gateway-audit-c3e8"
	handler, err := httpserver.NewHandlerWithAdminAndCompletionLogger(transport.NewClient(), upstream.URL, upstreamSecret, admin, pepper, keys, logger)
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(handler)
	defer gateway.Close()

	createdSurface := auditAdminRequest(t, gateway.URL, admin, http.MethodPost, "/admin/v1/keys", strings.NewReader(`{"name":"audit-primary"}`))
	var created struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	if err := json.Unmarshal(createdSurface.body, &created); err != nil || created.Key == "" || created.ID == "" {
		t.Fatalf("key creation response was not a valid creation envelope")
	}
	auditTestCanaries = newAuditCanaries(created.Key)
	canaries := auditTestCanaries
	scanner := newCanaryScanner(canaries)
	// Keep a separate closed repository for a storage error checkpoint without
	// invalidating the primary database used by the remaining scenarios.
	closedDatabase, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	closedKeys := storage.NewAPIKeyRepository(closedDatabase)
	closedHandler, err := httpserver.NewHandlerWithAdmin(transport.NewClient(), upstream.URL, upstreamSecret, admin, pepper, closedKeys)
	if err != nil {
		t.Fatal(err)
	}
	if err := closedDatabase.Close(); err != nil {
		t.Fatal(err)
	}
	closedGateway := httptest.NewServer(closedHandler)
	defer closedGateway.Close()

	// A second key gives both admin cursor implementations a real next page.
	// These are credential-shaped user data, not the actual canary values: names
	// and models are legitimate authenticated admin-domain output and also enter
	// ordinary completion metadata when the request is served.
	edgeName, edgeModel := "name-with-admin-credential-pattern", "model-with-upstream-api-key-pattern"
	edgeBody := `{"name":"` + edgeName + `"}`
	edgeSurface := auditAdminRequest(t, gateway.URL, admin, http.MethodPost, "/admin/v1/keys", strings.NewReader(edgeBody))
	var edge struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(edgeSurface.body, &edge); err != nil || edge.ID == "" {
		t.Fatalf("edge key creation response was not a valid creation envelope")
	}
	policy := `{"enabled":true,"policy":{"allowed_models":["` + edgeModel + `"]}}`
	_ = auditAdminRequest(t, gateway.URL, admin, http.MethodPut, "/admin/v1/keys/"+edge.ID+"/policy", strings.NewReader(policy))

	// Seed metadata and an opt-in body capture. Body contents are a trusted,
	// authenticated admin download surface and are checked for exactness below,
	// not incorrectly treated as an accidental leak.
	requestID1 := "0123456789abcdef0123456789abcdef"
	requestID2 := "abcdef0123456789abcdef0123456789"
	for index, requestID := range []string{requestID1, requestID2} {
		if err := history.Persist(context.Background(), storage.HistoryRecord{
			RequestID: requestID, APIKeyID: created.ID, KeyName: "audit-primary", Method: "POST", Path: "/v1/chat/completions", Route: "chat_completions", Model: "audit-model", RequestedMode: "json", UpstreamMode: "json", DeliveredMode: "json", TerminalOutcome: "complete", UpstreamStarted: true,
			DownstreamStatus: storage.KnownInt64(200), UpstreamStatus: storage.KnownInt64(200), TotalTokens: storage.KnownInt64(int64(index + 1)), CostMicros: storage.KnownInt64(int64(index + 7)), FinishedAt: storage.KnownInt64(int64(index + 20)), StartedAt: storage.KnownInt64(int64(index + 19)),
		}, []observability.BodySnapshot{{Kind: observability.BodyKindClientRequest, Bytes: []byte(canaries.body), OriginalSize: int64(len(canaries.body)), Captured: true}}); err != nil {
			t.Fatal(err)
		}
	}

	// Categories: public authentication and malformed ingress. These must use
	// constant gateway envelopes, never parser or credential details.
	scenarios := []struct {
		name string
		run  func() auditSurface
	}{
		{"public missing authorization", func() auditSurface {
			return auditRequest(t, http.DefaultClient, http.MethodGet, gateway.URL+"/v1/models", nil, nil)
		}},
		{"public wrong admin authorization", func() auditSurface {
			return auditRequest(t, http.DefaultClient, http.MethodGet, gateway.URL+"/v1/models", nil, map[string]string{"Authorization": "Bearer " + canaries.admin})
		}},
		{"public malformed gateway key", func() auditSurface {
			return auditRequest(t, http.DefaultClient, http.MethodGet, gateway.URL+"/v1/models", nil, map[string]string{"Authorization": "Bearer malformed-key"})
		}},
		{"public duplicated authorization", func() auditSurface {
			request, _ := http.NewRequest(http.MethodGet, gateway.URL+"/v1/models", nil)
			request.Header.Add("Authorization", "Bearer "+canaries.gatewayRaw)
			request.Header.Add("Authorization", "Bearer "+canaries.admin)
			response, _ := http.DefaultClient.Do(request)
			data, _ := io.ReadAll(response.Body)
			response.Body.Close()
			return auditSurface{name: "duplicated authorization", body: data, headers: response.Header.Clone()}
		}},
		{"malformed admin JSON", func() auditSurface {
			return auditAdminRequest(t, gateway.URL, canaries.admin, http.MethodPost, "/admin/v1/keys", strings.NewReader(`{"name":"`+canaries.body))
		}},
		{"admin unknown JSON field", func() auditSurface {
			return auditAdminRequest(t, gateway.URL, canaries.admin, http.MethodPost, "/admin/v1/keys", strings.NewReader(`{"name":"safe","credential":"`+canaries.upstream+`"}`))
		}},
		{"admin wrong media type", func() auditSurface {
			return auditRequest(t, http.DefaultClient, http.MethodPost, gateway.URL+"/admin/v1/keys", strings.NewReader(canaries.body), map[string]string{"Authorization": "Bearer " + canaries.admin, "Content-Type": "text/plain"})
		}},
		{"admin duplicate policy envelope", func() auditSurface {
			return auditAdminRequest(t, gateway.URL, canaries.admin, http.MethodPut, "/admin/v1/keys/"+created.ID+"/policy", strings.NewReader(`{"enabled":true,"enabled":false,"policy":{"x":"`+canaries.upstream+`"}}`))
		}},
		{"admin malformed policy with embedded key", func() auditSurface {
			return auditAdminRequest(t, gateway.URL, canaries.admin, http.MethodPut, "/admin/v1/keys/"+created.ID+"/policy", strings.NewReader(`{"enabled":true,"policy":{"allowed_models":["`+canaries.gatewayRaw+`",]}}`))
		}},
		{"admin invalid key route", func() auditSurface {
			return auditAdminRequest(t, gateway.URL, canaries.admin, http.MethodGet, "/admin/v1/keys/not valid/safe", nil)
		}},
		{"admin invalid request ID route", func() auditSurface {
			return auditAdminRequest(t, gateway.URL, canaries.admin, http.MethodGet, "/admin/v1/requests/not-valid/bodies/response", nil)
		}},
		{"admin invalid body kind", func() auditSurface {
			return auditAdminRequest(t, gateway.URL, canaries.admin, http.MethodGet, "/admin/v1/requests/"+requestID1+"/bodies/not-a-kind", nil)
		}},
		{"unknown gateway route", func() auditSurface {
			return auditRequest(t, http.DefaultClient, http.MethodGet, gateway.URL+"/not-found?secret=ordinary-user-input", nil, nil)
		}},
		{"oversized query ingress", func() auditSurface {
			return auditRequest(t, http.DefaultClient, http.MethodGet, gateway.URL+"/v1/models?"+strings.Repeat("x", 4097), nil, nil)
		}},
		{"unsupported admin method", func() auditSurface {
			return auditAdminRequest(t, gateway.URL, canaries.admin, http.MethodDelete, "/admin/v1/keys", nil)
		}},

		// Categories: storage failures and upstream failures. Storage errors are
		// intentionally mapped to stable admin messages; upstream bodies remain
		// transparent, so this case scans control headers and completion logs only.
		{"storage closed key list", func() auditSurface {
			return auditAdminRequest(t, closedGateway.URL, canaries.admin, http.MethodGet, "/admin/v1/keys", nil)
		}},
		{"storage closed request list", func() auditSurface {
			return auditAdminRequest(t, closedGateway.URL, canaries.admin, http.MethodGet, "/admin/v1/requests", nil)
		}},
		{"upstream application error", func() auditSurface {
			return auditRequest(t, http.DefaultClient, http.MethodGet, gateway.URL+"/v1/error", nil, map[string]string{"Authorization": "Bearer " + canaries.gatewayRaw, "X-Audit-Case": "error"})
		}},
		{"upstream timeout cancellation", func() auditSurface {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			request, _ := http.NewRequestWithContext(ctx, http.MethodGet, gateway.URL+"/v1/timeout", nil)
			request.Header.Set("Authorization", "Bearer "+canaries.gatewayRaw)
			request.Header.Set("X-Audit-Case", "timeout")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				return auditSurface{name: "upstream timeout client cancellation"}
			}
			data, _ := io.ReadAll(response.Body)
			response.Body.Close()
			return auditSurface{name: "upstream timeout", body: data, headers: response.Header.Clone()}
		}},
		{"client canceled request", func() auditSurface {
			ctx, cancel := context.WithCancel(context.Background())
			request, _ := http.NewRequestWithContext(ctx, http.MethodGet, gateway.URL+"/v1/timeout", nil)
			request.Header.Set("Authorization", "Bearer "+canaries.gatewayRaw)
			request.Header.Set("X-Audit-Case", "timeout")
			cancel()
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				return auditSurface{name: "client canceled request"}
			}
			data, _ := io.ReadAll(response.Body)
			response.Body.Close()
			return auditSurface{name: "client canceled request", body: data, headers: response.Header.Clone()}
		}},
		{"upstream connection failure", func() auditSurface {
			failure := httpserver.NewHandler(transport.NewClient(), "http://127.0.0.1:1", canaries.upstream)
			server := httptest.NewServer(failure)
			defer server.Close()
			return auditRequest(t, http.DefaultClient, http.MethodGet, server.URL+"/v1/connection", nil, nil)
		}},
		{"upstream successful secret body transparency", func() auditSurface {
			return auditRequest(t, http.DefaultClient, http.MethodGet, gateway.URL+"/v1/success", nil, map[string]string{"Authorization": "Bearer " + canaries.gatewayRaw, "X-Audit-Case": "success"})
		}},
		{"upstream credential header filtering", func() auditSurface {
			return auditRequest(t, http.DefaultClient, http.MethodGet, gateway.URL+"/v1/error", nil, map[string]string{"Authorization": "Bearer " + canaries.gatewayRaw, "X-Audit-Case": "error"})
		}},

		// Categories: authenticated admin projections. The edge name/model cases
		// are trusted product data and are asserted explicitly rather than passed
		// through the forbidden-secret scanner.
		{"admin key list metadata", func() auditSurface {
			return auditAdminRequest(t, gateway.URL, canaries.admin, http.MethodGet, "/admin/v1/keys?limit=1", nil)
		}},
		{"admin key detail safe projection", func() auditSurface {
			return auditAdminRequest(t, gateway.URL, canaries.admin, http.MethodGet, "/admin/v1/keys/"+created.ID, nil)
		}},
		{"admin request list metadata", func() auditSurface {
			return auditAdminRequest(t, gateway.URL, canaries.admin, http.MethodGet, "/admin/v1/requests?limit=1", nil)
		}},
		{"admin request detail metadata", func() auditSurface {
			return auditAdminRequest(t, gateway.URL, canaries.admin, http.MethodGet, "/admin/v1/requests/"+requestID1, nil)
		}},
		{"admin body exact trusted download", func() auditSurface {
			surface := auditAdminRequest(t, gateway.URL, canaries.admin, http.MethodGet, "/admin/v1/requests/"+requestID1+"/bodies/client_request", nil)
			if !bytes.Equal(surface.body, []byte(canaries.body)) {
				t.Fatalf("trusted body changed: got %d bytes, want %d bytes", len(surface.body), len(canaries.body))
			}
			return auditSurface{name: "admin body headers", headers: surface.headers}
		}},
		{"admin edge name is legitimate", func() auditSurface {
			surface := auditAdminRequest(t, gateway.URL, canaries.admin, http.MethodGet, "/admin/v1/keys/"+edge.ID, nil)
			if !bytes.Contains(surface.body, []byte(edgeName)) {
				t.Fatalf("edge name missing from trusted admin detail")
			}
			return auditSurface{name: "trusted edge name", headers: surface.headers}
		}},
		{"admin edge model is legitimate", func() auditSurface {
			surface := auditAdminRequest(t, gateway.URL, canaries.admin, http.MethodGet, "/admin/v1/keys/"+edge.ID, nil)
			if !bytes.Contains(surface.body, []byte(edgeModel)) {
				t.Fatalf("edge model missing from trusted admin detail")
			}
			return auditSurface{name: "trusted edge model", headers: surface.headers}
		}},
		{"admin unauthorized detail", func() auditSurface {
			return auditRequest(t, http.DefaultClient, http.MethodGet, gateway.URL+"/admin/v1/keys/"+created.ID, nil, map[string]string{"Authorization": "Bearer " + canaries.upstream})
		}},
		{"admin cursor tamper", func() auditSurface {
			surface := auditAdminRequest(t, gateway.URL, canaries.admin, http.MethodGet, "/admin/v1/keys?limit=1", nil)
			var page struct {
				Cursor string `json:"next_cursor"`
			}
			_ = json.Unmarshal(surface.body, &page)
			if page.Cursor == "" {
				t.Fatal("key cursor missing")
			}
			raw, _ := base64.RawURLEncoding.DecodeString(page.Cursor)
			if bytes.Contains(raw, []byte(created.ID)) {
				t.Fatal("key cursor contains textual database data")
			}
			tampered := page.Cursor[:len(page.Cursor)-1] + map[bool]string{true: "A", false: "B"}[page.Cursor[len(page.Cursor)-1] == 'A']
			return auditAdminRequest(t, gateway.URL, canaries.admin, http.MethodGet, "/admin/v1/keys?limit=1&cursor="+tampered, nil)
		}},
		{"admin request cursor opacity and tamper", func() auditSurface {
			surface := auditAdminRequest(t, gateway.URL, canaries.admin, http.MethodGet, "/admin/v1/requests?limit=1", nil)
			var page struct {
				Cursor string `json:"next_cursor"`
			}
			_ = json.Unmarshal(surface.body, &page)
			if page.Cursor == "" {
				t.Fatal("request cursor missing")
			}
			raw, _ := base64.RawURLEncoding.DecodeString(page.Cursor)
			if bytes.Contains(raw, []byte(requestID1)) || bytes.Contains(raw, []byte("1700000000000000")) {
				t.Fatal("request cursor contains textual database data")
			}
			tampered := page.Cursor[:len(page.Cursor)-1] + "A"
			return auditAdminRequest(t, gateway.URL, canaries.admin, http.MethodGet, "/admin/v1/requests?limit=1&cursor="+tampered, nil)
		}},

		// Categories: metrics. Every family is exercised after requests carrying
		// user data; labels must remain bounded enums and scalar observations.
		{"metrics all families", func() auditSurface {
			return auditRequest(t, http.DefaultClient, http.MethodGet, gateway.URL+"/metrics", nil, nil)
		}},
		{"metrics query data excluded", func() auditSurface {
			return auditRequest(t, http.DefaultClient, http.MethodGet, gateway.URL+"/metrics?query="+canaries.query, nil, nil)
		}},
		{"metrics request ID excluded", func() auditSurface {
			return auditRequest(t, http.DefaultClient, http.MethodGet, gateway.URL+"/metrics", nil, map[string]string{"X-Request-ID": canaries.query})
		}},
		{"metrics model data excluded", func() auditSurface {
			return auditRequest(t, http.DefaultClient, http.MethodGet, gateway.URL+"/metrics", strings.NewReader(canaries.body), map[string]string{"Content-Type": "application/json"})
		}},
		{"metrics repeated scrape stable", func() auditSurface {
			return auditRequest(t, http.DefaultClient, http.MethodGet, gateway.URL+"/metrics", nil, nil)
		}},
		{"unknown route query data excluded", func() auditSurface {
			// Query data is a realistic untrusted input at a route that reaches
			// request tracing; do not put the canary in an arbitrary path or
			// option name, where an echoed usage error would be ambiguous.
			return auditRequest(t, http.DefaultClient, http.MethodGet, gateway.URL+"/not-found?query="+canaries.query, nil, nil)
		}},

		// Categories: CLI diagnostics. The CLI may receive paths, transport
		// errors, and malformed server data; none may reflect credential values.
		{"gwctl network stderr", func() auditSurface {
			var stdout, stderr bytes.Buffer
			_ = gwctl.Run(context.Background(), []string{"--gateway-url", "http://127.0.0.1:1", "--admin-credential", canaries.admin, "ping"}, &stdout, &stderr)
			return auditSurface{name: "gwctl network stderr", body: stderr.Bytes()}
		}},
		{"gwctl auth stderr", func() auditSurface {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(response, canaries.upstream)
			}))
			defer server.Close()
			var stdout, stderr bytes.Buffer
			_ = gwctl.Run(context.Background(), []string{"--gateway-url", server.URL, "--admin-credential", canaries.admin, "ping"}, &stdout, &stderr)
			return auditSurface{name: "gwctl auth stderr", body: stderr.Bytes()}
		}},
		{"gwctl malformed response stderr", func() auditSurface {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(response, canaries.body) }))
			defer server.Close()
			var stdout, stderr bytes.Buffer
			_ = gwctl.Run(context.Background(), []string{"--gateway-url", server.URL, "--admin-credential", canaries.admin, "ping"}, &stdout, &stderr)
			return auditSurface{name: "gwctl malformed response stderr", body: stderr.Bytes()}
		}},
		{"gwctl output path diagnostic", func() auditSurface {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("X-Original-Size", "1")
				response.Header().Set("X-Truncated", "false")
				_, _ = io.WriteString(response, "x")
			}))
			defer server.Close()
			var stdout, stderr bytes.Buffer
			_ = gwctl.Run(context.Background(), []string{"--gateway-url", server.URL, "--admin-credential", canaries.admin, "requests", "get", requestID1, "--body", "response", "--output", filepath.Join("/proc", canaries.body, "out")}, &stdout, &stderr)
			return auditSurface{name: "gwctl output path diagnostic", body: stderr.Bytes()}
		}},
		{"gwctl invalid URL diagnostic", func() auditSurface {
			var stdout, stderr bytes.Buffer
			_ = gwctl.Run(context.Background(), []string{"--gateway-url", "http://localhost:8080/?ordinary-user-input", "--admin-credential", canaries.admin, "ping"}, &stdout, &stderr)
			return auditSurface{name: "gwctl invalid URL diagnostic", body: stderr.Bytes()}
		}},
		{"gwctl usage credential value", func() auditSurface {
			var stdout, stderr bytes.Buffer
			_ = gwctl.Run(context.Background(), []string{"unknown", canaries.admin}, &stdout, &stderr)
			return auditSurface{name: "gwctl usage credential value", body: stderr.Bytes()}
		}},
		{"gwctl invalid key argument", func() auditSurface {
			var stdout, stderr bytes.Buffer
			_ = gwctl.Run(context.Background(), []string{"--gateway-url", "http://127.0.0.1:1", "--admin-credential", canaries.admin, "keys", "get", "not/" + canaries.body}, &stdout, &stderr)
			return auditSurface{name: "gwctl invalid key argument", body: stderr.Bytes()}
		}},
		{"gwctl unknown option argument", func() auditSurface {
			var stdout, stderr bytes.Buffer
			_ = gwctl.Run(context.Background(), []string{"--gateway-url", "http://127.0.0.1:1", "--admin-credential", canaries.admin, "keys", "list", "--unknown-option"}, &stdout, &stderr)
			return auditSurface{name: "gwctl unknown option argument", body: stderr.Bytes()}
		}},
		{"gwctl canceled context", func() auditSurface {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			var stdout, stderr bytes.Buffer
			_ = gwctl.Run(ctx, []string{"--gateway-url", "http://127.0.0.1:1", "--admin-credential", canaries.admin, "ping"}, &stdout, &stderr)
			return auditSurface{name: "gwctl canceled context", body: stderr.Bytes()}
		}},
		{"gwctl empty stdout on failure", func() auditSurface {
			var stdout, stderr bytes.Buffer
			_ = gwctl.Run(context.Background(), []string{"--gateway-url", "http://127.0.0.1:1", "--admin-credential", canaries.admin, "requests", "get", requestID1}, &stdout, &stderr)
			if stdout.Len() != 0 {
				t.Fatalf("failure wrote %d bytes to stdout", stdout.Len())
			}
			return auditSurface{name: "gwctl empty stdout on failure", body: stderr.Bytes()}
		}},
		{"gwctl safe version output", func() auditSurface {
			var stdout, stderr bytes.Buffer
			_ = gwctl.Run(context.Background(), []string{"--admin-credential", canaries.admin, "version"}, &stdout, &stderr)
			return auditSurface{name: "gwctl safe version output", body: append(stdout.Bytes(), stderr.Bytes()...)}
		}},

		// Category: asynchronous panic recovery. A telemetry sink panic must be
		// converted into a bounded failure counter and never printed with values.
		{"history worker panic recovery", func() auditSurface {
			repo := &panicAuditRepository{}
			worker := httpserver.NewHistoryPersistenceWorker(httpserver.HistoryPersistenceWorkerOptions{Repository: repo, Capacity: 1})
			defer worker.Shutdown(context.Background())
			record := httpserver.CompletionRecord{RequestID: requestID1, KeyID: created.ID, Method: "GET", Route: httpserver.RouteClassGeneric, Terminal: httpserver.TerminalMetadata{Outcome: httpserver.TerminalOutcomeComplete, UpstreamStarted: true}}
			worker.SubmitRecord(record)
			deadline := time.Now().Add(time.Second)
			for worker.Stats().PersistFailed == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if worker.Stats().PersistFailed != 1 {
				t.Fatalf("panic recovery stats = %#v", worker.Stats())
			}
			return auditSurface{name: "history panic recovery", body: nil}
		}},
	}

	if len(scenarios) < 50 {
		t.Fatalf("audit scenario count = %d, want at least 50", len(scenarios))
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			surface := scenario.run()
			// Upstream application response bodies are intentionally omitted from
			// control-plane scanning because transparent proxying is contractual.
			if scenario.name != "upstream application error" && scenario.name != "upstream successful secret body transparency" && scenario.name != "upstream credential header filtering" {
				scanner.check(t, surface)
			} else {
				scanner.check(t, auditSurface{name: surface.name + " headers", headers: surface.headers})
			}
		})
	}

	// All ordinary completion records, including failures and cancellations,
	// have now been handed to the structured sink.
	if err := logger.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	scanner.check(t, auditSurface{name: "structured slog output", body: logs.Bytes()})
}

var auditTestCanaries auditCanaries

type panicAuditRepository struct{}

func (repo *panicAuditRepository) Persist(context.Context, storage.HistoryRecord, []observability.BodySnapshot) error {
	panic("audit sink panic with secret")
}
func (repo *panicAuditRepository) DeleteBodiesBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}
func (repo *panicAuditRepository) DeleteMetadataBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}
