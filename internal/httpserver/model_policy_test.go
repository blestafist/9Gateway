package httpserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/transport"
)

func TestModelPolicyRejectsBeforeUpstreamAndPreservesAdmittedBody(t *testing.T) {
	pepper := []byte("model-policy-pepper")
	allowedKey, err := auth.GenerateGatewayKey(pepper)
	if err != nil {
		t.Fatal(err)
	}
	denyKey, err := auth.GenerateGatewayKey(pepper)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := auth.NewAuthenticator(pepper, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := authenticator.Load([]auth.Record{
		{ID: "allow", DisplayPrefix: allowedKey.DisplayPrefix, Digest: allowedKey.Digest, Enabled: true, PolicyJSON: []byte(`{"allowed_models":["gpt-*","exact"],"denied_models":["gpt-denied","blocked-*"]}`)},
		{ID: "deny", DisplayPrefix: denyKey.DisplayPrefix, Digest: denyKey.Digest, Enabled: true, PolicyJSON: []byte(`{"denied_models":["exact"]}`)},
	}); err != nil {
		t.Fatal(err)
	}

	var upstreamCalls atomic.Int32
	received := make(chan []byte, 16)
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		upstreamCalls.Add(1)
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read upstream body: %v", err)
			return
		}
		received <- body
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	gateway := httptest.NewServer(NewHandlerWithAuthenticator(transport.NewClient(), upstream.URL, "upstream-secret", authenticator, nil))
	t.Cleanup(gateway.Close)

	tests := []struct {
		name       string
		key        string
		model      string
		wantStatus int
		wantBody   []byte
	}{
		{name: "exact allow", key: allowedKey.RawKey, model: "exact", wantStatus: http.StatusNoContent},
		{name: "glob allow", key: allowedKey.RawKey, model: "gpt-good", wantStatus: http.StatusNoContent},
		{name: "exact deny", key: allowedKey.RawKey, model: "gpt-denied", wantStatus: http.StatusForbidden},
		{name: "glob deny", key: allowedKey.RawKey, model: "blocked-model", wantStatus: http.StatusForbidden},
		{name: "allow-only miss", key: allowedKey.RawKey, model: "other", wantStatus: http.StatusForbidden},
		{name: "case-sensitive miss", key: allowedKey.RawKey, model: "GPT-GOOD", wantStatus: http.StatusForbidden},
		{name: "different key policy", key: denyKey.RawKey, model: "exact", wantStatus: http.StatusForbidden},
		{name: "different key admitted", key: denyKey.RawKey, model: "other", wantStatus: http.StatusNoContent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(` {"model":"` + test.model + `","messages":[{"role":"user","content":"preserve"}]} `)
			request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+test.key)
			request.Header.Set("Content-Type", "application/json")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			responseBody, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != test.wantStatus {
				t.Fatalf("status = %d, want %d; body = %q", response.StatusCode, test.wantStatus, responseBody)
			}
			if test.wantStatus == http.StatusForbidden {
				var payload gatewayErrorPayload
				if err := decodeJSON(responseBody, &payload); err != nil {
					t.Fatal(err)
				}
				if payload.Error.Code != gatewayErrorModelNotAllowed || payload.Error.Param != "model" {
					t.Fatalf("error = %#v", payload.Error)
				}
				return
			}
			select {
			case got := <-received:
				if !bytes.Equal(got, body) {
					t.Fatalf("admitted body changed: got %q, want %q", got, body)
				}
			default:
				t.Fatal("upstream body was not recorded")
			}
		})
	}
	if got, want := upstreamCalls.Load(), int32(3); got != want {
		t.Fatalf("upstream calls = %d, want %d", got, want)
	}
}

func TestModelPolicyLeavesUnknownRequestsInspectablePassthrough(t *testing.T) {
	pepper := []byte("model-policy-passthrough")
	key, err := auth.GenerateGatewayKey(pepper)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := auth.NewAuthenticator(pepper, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := authenticator.Load([]auth.Record{{ID: "restricted", DisplayPrefix: key.DisplayPrefix, Digest: key.Digest, Enabled: true, PolicyJSON: []byte(`{"allowed_models":["only-this"]}`)}}); err != nil {
		t.Fatal(err)
	}

	var upstreamCalls atomic.Int32
	received := make(chan []byte, 3)
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		upstreamCalls.Add(1)
		body, _ := io.ReadAll(request.Body)
		received <- body
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	gateway := httptest.NewServer(NewHandlerWithAuthenticator(transport.NewClient(), upstream.URL, "upstream-secret", authenticator, nil))
	t.Cleanup(gateway.Close)

	oversized := append([]byte(`{"model":"not-inspected","padding":"`), bytes.Repeat([]byte{'x'}, int(requestInspectionLimit))...)
	oversized = append(oversized, []byte(`"}`)...)
	for _, test := range []struct {
		name string
		path string
		body []byte
	}{
		{name: "malformed", path: "/v1/chat/completions", body: []byte(`{"model":"only-this"`)},
		{name: "oversized", path: "/v1/chat/completions", body: oversized},
		{name: "unknown endpoint", path: "/v1/unknown", body: []byte(`{"model":"blocked"}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPost, gateway.URL+test.path, bytes.NewReader(test.body))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+key.RawKey)
			request.Header.Set("Content-Type", "application/json")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusNoContent {
				t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusNoContent)
			}
			select {
			case got := <-received:
				if !bytes.Equal(got, test.body) {
					t.Fatalf("passthrough body changed: got %q, want %q", got, test.body)
				}
			case <-make(chan struct{}):
				t.Fatal("unreachable")
			}
		})
	}
	if got, want := upstreamCalls.Load(), int32(3); got != want {
		t.Fatalf("upstream calls = %d, want %d", got, want)
	}
}

func decodeJSON(data []byte, destination any) error {
	return json.Unmarshal(data, destination)
}
