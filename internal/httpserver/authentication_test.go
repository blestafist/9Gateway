package httpserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/transport"
)

func TestPublicV1AuthenticationRejectsCredentialsBeforeReadingBodyOrCallingUpstream(t *testing.T) {
	pepper := []byte("test-pepper")
	valid, err := auth.GenerateGatewayKey(pepper)
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := auth.GenerateGatewayKey(pepper)
	if err != nil {
		t.Fatal(err)
	}
	expiresAt := time.Now().UTC().Add(-time.Hour)
	expired, err := auth.GenerateGatewayKey(pepper)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := auth.NewAuthenticator(pepper, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := authenticator.Load([]auth.Record{
		{ID: "valid", DisplayPrefix: valid.DisplayPrefix, Digest: valid.Digest, Enabled: true},
		{ID: "disabled", DisplayPrefix: disabled.DisplayPrefix, Digest: disabled.Digest, Enabled: false},
		{ID: "expired", DisplayPrefix: expired.DisplayPrefix, Digest: expired.Digest, Enabled: true, ExpiresAt: &expiresAt},
	}); err != nil {
		t.Fatal(err)
	}

	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		upstreamCalls.Add(1)
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	gateway := httptest.NewServer(NewHandlerWithAuthenticator(transport.NewClient(), upstream.URL, "upstream-secret", authenticator, nil))
	t.Cleanup(gateway.Close)

	tests := []struct {
		name       string
		authorize  func(*http.Request)
		wantCode   string
		wantStatus int
	}{
		{name: "missing", wantCode: gatewayErrorInvalidAPIKey, wantStatus: http.StatusUnauthorized},
		{name: "repeated", authorize: func(request *http.Request) {
			request.Header.Add("Authorization", "Bearer "+valid.RawKey)
			request.Header.Add("Authorization", "Bearer "+valid.RawKey)
		}, wantCode: gatewayErrorInvalidAPIKey, wantStatus: http.StatusUnauthorized},
		{name: "malformed", authorize: func(request *http.Request) {
			request.Header.Set("Authorization", "Basic client-secret")
		}, wantCode: gatewayErrorInvalidAPIKey, wantStatus: http.StatusUnauthorized},
		{name: "unknown", authorize: func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+strings.Repeat("x", len(valid.RawKey)))
		}, wantCode: gatewayErrorInvalidAPIKey, wantStatus: http.StatusUnauthorized},
		{name: "disabled", authorize: func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+disabled.RawKey)
		}, wantCode: gatewayErrorKeyDisabled, wantStatus: http.StatusUnauthorized},
		{name: "expired", authorize: func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+expired.RawKey)
		}, wantCode: gatewayErrorKeyExpired, wantStatus: http.StatusUnauthorized},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := &trackingBody{Reader: bytes.NewBufferString(`{"model":"secret"}`)}
			request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", body)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Content-Type", "application/json")
			if test.authorize != nil {
				test.authorize(request)
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			payload := decodeGatewayError(t, response)
			if response.StatusCode != test.wantStatus || payload.Error.Code != test.wantCode {
				t.Fatalf("response = status %d, code %q; want status %d, code %q", response.StatusCode, payload.Error.Code, test.wantStatus, test.wantCode)
			}
			if response.Header.Get(requestIDHeader) == "" {
				t.Fatal("rejection did not include request ID")
			}
		})
	}
	if got := upstreamCalls.Load(); got != 0 {
		t.Fatalf("upstream calls = %d, want zero", got)
	}

	body := &trackingBody{Reader: bytes.NewBufferString(`{"secret":"must-not-be-read"}`)}
	request := httptest.NewRequest(http.MethodPost, "http://gateway.test/v1/chat/completions", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	withGatewayAuthentication(authenticator, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("rejected request reached downstream handler")
	})).ServeHTTP(response, request)
	if body.reads.Load() != 0 {
		t.Fatalf("directly rejected request body reads = %d, want zero", body.reads.Load())
	}
}

func TestPublicV1AuthenticationAddsPrincipalAndReplacesCredential(t *testing.T) {
	pepper := []byte("test-pepper")
	generated, err := auth.GenerateGatewayKey(pepper)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := auth.NewAuthenticator(pepper, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := authenticator.Load([]auth.Record{{ID: "principal-id", Name: "safe name", DisplayPrefix: generated.DisplayPrefix, Digest: generated.Digest, Enabled: true, PolicyJSON: []byte(`{"future":"opaque"}`)}}); err != nil {
		t.Fatal(err)
	}

	requestDetails := make(chan struct {
		method string
		path   string
		query  string
		body   []byte
		auth   string
		id     string
	}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		principal, ok := PrincipalFromContext(request.Context())
		if ok {
			// The context belongs to the gateway request, not this upstream
			// request. This branch documents that upstream never receives it.
			t.Errorf("upstream request unexpectedly carried principal %#v", principal)
		}
		requestDetails <- struct {
			method string
			path   string
			query  string
			body   []byte
			auth   string
			id     string
		}{request.Method, request.URL.Path, request.URL.RawQuery, body, request.Header.Get("Authorization"), request.Header.Get(requestIDHeader)}
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"ok":true}`)
	}))
	t.Cleanup(upstream.Close)

	var observed auth.Principal
	var observedOK atomic.Bool
	proxy := newProxyHandler(transport.NewClient(), upstream.URL, "upstream-secret")
	public := withGatewayAuthentication(authenticator, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		principal, ok := PrincipalFromContext(request.Context())
		observed = principal
		observedOK.Store(ok)
		proxy.ServeHTTP(response, request)
	}))
	gateway := httptest.NewServer(newHandlerWithCompletionLogger(nil, routeWithAuthenticator(public, nil, nil)))
	t.Cleanup(gateway.Close)

	body := []byte(`{"messages":[{"role":"user","content":"keep"}]}`)
	request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/custom/path?x=1&x=2", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+generated.RawKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()

	if !observedOK.Load() || observed.ID != "principal-id" || observed.Name != "safe name" || observed.DisplayPrefix != generated.DisplayPrefix {
		t.Fatalf("principal = %#v, present %t", observed, observedOK.Load())
	}
	details := <-requestDetails
	if details.method != request.Method || details.path != request.URL.Path || details.query != request.URL.RawQuery || !bytes.Equal(details.body, body) || details.auth != "Bearer upstream-secret" {
		t.Fatalf("upstream request details = %#v", details)
	}
}

type trackingBody struct {
	io.Reader
	reads atomic.Int32
}

func (body *trackingBody) Read(destination []byte) (int, error) {
	body.reads.Add(1)
	return body.Reader.Read(destination)
}

func (body *trackingBody) Close() error { return nil }

type gatewayErrorPayload struct {
	Error gatewayErrorDetail `json:"error"`
}

func decodeGatewayError(t *testing.T, response *http.Response) gatewayErrorPayload {
	t.Helper()
	defer response.Body.Close()
	var payload gatewayErrorPayload
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	return payload
}
