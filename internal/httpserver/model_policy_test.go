package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/limiter"
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

func TestRestrictedModelInspectionUsesProvisionalConcurrencyAndDoesNotSpendRequestWindow(t *testing.T) {
	pepper := []byte("slow-model-policy")
	key, err := auth.GenerateGatewayKey(pepper)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := auth.NewAuthenticator(pepper, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := authenticator.Load([]auth.Record{{
		ID: "restricted", DisplayPrefix: key.DisplayPrefix, Digest: key.Digest, Enabled: true,
		PolicyJSON: []byte(`{"allowed_models":["allowed"],"request_windows":[{"amount":1,"duration":"1m"}],"max_concurrent_requests":1}`),
	}}); err != nil {
		t.Fatal(err)
	}
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		upstreamCalls.Add(1)
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	requestLimiter := limiter.NewRequestLimiter(nil)
	concurrencyLimiter := limiter.NewConcurrencyLimiter()
	gateway := httptest.NewServer(NewHandlerWithAuthenticatorAndLimiters(transport.NewClient(), upstream.URL, "upstream-secret", authenticator, requestLimiter, concurrencyLimiter, nil))
	t.Cleanup(gateway.Close)

	body := newBlockingModelBody(`{"model":"blocked"}`)
	firstContext, cancelFirst := context.WithCancel(context.Background())
	first, err := http.NewRequestWithContext(firstContext, http.MethodPost, gateway.URL+"/v1/chat/completions", body)
	if err != nil {
		t.Fatal(err)
	}
	first.Header.Set("Authorization", "Bearer "+key.RawKey)
	first.Header.Set("Content-Type", "application/json")
	firstResult := make(chan *http.Response, 1)
	firstErr := make(chan error, 1)
	go func() {
		response, requestErr := http.DefaultClient.Do(first)
		if requestErr != nil {
			firstErr <- requestErr
			return
		}
		firstResult <- response
	}()
	select {
	case <-body.started:
	case <-time.After(time.Second):
		t.Fatal("restricted request did not begin body inspection")
	}

	second := newModelPolicyRequest(t, gateway.URL, key.RawKey, `{"model":"allowed"}`)
	secondResponse, err := http.DefaultClient.Do(second)
	if err != nil {
		t.Fatal(err)
	}
	secondResponse.Body.Close()
	if secondResponse.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("slow-upload saturation status = %d, want 429", secondResponse.StatusCode)
	}
	if got := concurrencyLimiter.Len(); got != 1 {
		t.Fatalf("provisional inspection reservations = %d, want 1", got)
	}

	close(body.release)
	select {
	case response := <-firstResult:
		firstBody, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusForbidden || !bytes.Contains(firstBody, []byte(`"model_not_allowed"`)) {
			t.Fatalf("slow denied response = %d/%q", response.StatusCode, firstBody)
		}
	case requestErr := <-firstErr:
		t.Fatal(requestErr)
	case <-time.After(time.Second):
		t.Fatal("slow restricted request did not finish")
	}
	if got := concurrencyLimiter.Len(); got != 0 {
		t.Fatalf("reservations after model denial = %d, want 0", got)
	}
	cancelFirst()

	// The model denial consumed no request-window capacity, so the next
	// admitted request is still allowed after the lease is released.
	third := newModelPolicyRequest(t, gateway.URL, key.RawKey, `{"model":"allowed"}`)
	thirdResponse, err := http.DefaultClient.Do(third)
	if err != nil {
		t.Fatal(err)
	}
	thirdResponse.Body.Close()
	if thirdResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("post-denial request status = %d, want 204", thirdResponse.StatusCode)
	}
	if got := upstreamCalls.Load(); got != 1 {
		t.Fatalf("upstream calls = %d, want 1", got)
	}
}

type blockingModelBody struct {
	remaining []byte
	started   chan struct{}
	release   chan struct{}
	once      sync.Once
}

func newBlockingModelBody(value string) *blockingModelBody {
	return &blockingModelBody{remaining: []byte(value), started: make(chan struct{}), release: make(chan struct{})}
}

func (body *blockingModelBody) Read(destination []byte) (int, error) {
	body.once.Do(func() { close(body.started) })
	<-body.release
	if len(body.remaining) == 0 {
		return 0, io.EOF
	}
	count := copy(destination, body.remaining)
	body.remaining = body.remaining[count:]
	return count, nil
}

func (body *blockingModelBody) Close() error { return nil }

func newModelPolicyRequest(t *testing.T, baseURL, rawKey, body string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+rawKey)
	request.Header.Set("Content-Type", "application/json")
	return request
}

func decodeJSON(data []byte, destination any) error {
	return json.Unmarshal(data, destination)
}
