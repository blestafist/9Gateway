package httpserver

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/observability"
	"github.com/pestit/9gateway/internal/transport"
)

func TestT133RequestBodyCaptureSeparatesInspectedClientAndUpstreamReads(t *testing.T) {
	const payload = `{"model":"capture","stream":false}`
	var upstreamBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var err error
		upstreamBody, err = io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read upstream body: %v", err)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{}`)
	}))
	t.Cleanup(upstream.Close)

	key, authenticator := t133Authenticator(t)
	var trace *RequestTraceState
	proxy := withGatewayAuthentication(authenticator, newProxyHandlerWithLimitersAndTokenConfig(
		transport.NewClient(), upstream.URL, "upstream", limiter.NewRequestLimiter(nil),
		limiter.NewConcurrencyLimiter(), TokenAdmissionConfig{MaxCapturedBodyBytes: 128},
	))
	handler := withRequestID(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		trace = TraceFromContext(request.Context())
		proxy.ServeHTTP(response, request)
	}))
	gateway := httptest.NewServer(handler)
	t.Cleanup(gateway.Close)

	request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", bytes.NewBufferString(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+key.RawKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !bytes.Equal(upstreamBody, []byte(payload)) {
		t.Fatalf("response/body = %d/%q, want 200/%q", response.StatusCode, upstreamBody, payload)
	}

	client, upstreamSnapshot, ok := trace.RequestBodySnapshots()
	if !ok {
		t.Fatal("request body snapshots were not stored")
	}
	if client.Kind != "client_request" || upstreamSnapshot.Kind != "upstream_request" ||
		!bytes.Equal(client.Bytes, []byte(payload)) || !bytes.Equal(upstreamSnapshot.Bytes, []byte(payload)) ||
		client.OriginalSize != int64(len(payload)) || upstreamSnapshot.OriginalSize != int64(len(payload)) ||
		!client.Captured || !upstreamSnapshot.Captured || client.Truncated || upstreamSnapshot.Truncated {
		t.Fatalf("snapshots = %s / %s", client, upstreamSnapshot)
	}
}

func TestT133UpstreamBodyCaptureSharesFinalizationBoundary(t *testing.T) {
	recorder, err := observability.NewBodyRecorder(observability.BodyKindUpstreamRequest, 1024)
	if err != nil {
		t.Fatal(err)
	}
	capture := &requestBodyCapture{recorder: recorder}
	body := capture.wrap(io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("x"), 1024))))
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		_, _ = io.Copy(io.Discard, body)
	}()
	go func() {
		defer wait.Done()
		capture.finalize()
	}()
	wait.Wait()
	if got := recorder.Finalize(); !got.Captured {
		t.Fatal("concurrent upstream capture was not finalized")
	}
}

func TestT133RedirectsAreForwardedWithoutFollowing(t *testing.T) {
	const payload = `{"model":"redirect-test","stream":false}`
	for _, test := range []struct {
		name       string
		status     int
		sameOrigin bool
	}{
		{name: "301 same origin", status: http.StatusMovedPermanently, sameOrigin: true},
		{name: "302 cross origin", status: http.StatusFound},
		{name: "307 same origin", status: http.StatusTemporaryRedirect, sameOrigin: true},
		{name: "308 cross origin", status: http.StatusPermanentRedirect},
	} {
		t.Run(test.name, func(t *testing.T) {
			var upstreamCalls, redirectTargetCalls atomic.Int32
			redirectTarget := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				redirectTargetCalls.Add(1)
			}))
			t.Cleanup(redirectTarget.Close)
			expectedLocation := redirectTarget.URL + "/cross-origin-target"
			if test.sameOrigin {
				expectedLocation = "/same-origin-target"
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/same-origin-target" {
					redirectTargetCalls.Add(1)
					return
				}
				upstreamCalls.Add(1)
				response.Header().Set("Location", expectedLocation)
				response.Header().Set("Content-Type", "text/event-stream")
				response.Header().Set("X-Upstream-Redirect", "preserve-me")
				response.WriteHeader(test.status)
				_, _ = io.WriteString(response, "redirect body")
			}))
			t.Cleanup(upstream.Close)

			key, authenticator := t133Authenticator(t)
			gateway := httptest.NewServer(withGatewayAuthentication(authenticator, newProxyHandlerWithLimitersAndTokenConfig(
				transport.NewClient(), upstream.URL, "upstream", limiter.NewRequestLimiter(nil),
				limiter.NewConcurrencyLimiter(), TokenAdmissionConfig{MaxCapturedBodyBytes: 128},
			)))
			t.Cleanup(gateway.Close)

			request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", bytes.NewBufferString(payload))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+key.RawKey)
			request.Header.Set("Content-Type", "application/json")
			response, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			}}).Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != test.status || response.Header.Get("Location") != expectedLocation || response.Header.Get("Content-Type") != "text/event-stream" || response.Header.Get("X-Upstream-Redirect") != "preserve-me" || string(body) != "redirect body" {
				t.Fatalf("redirect response = status %d, headers %v, body %q", response.StatusCode, response.Header, body)
			}
			if upstreamCalls.Load() != 1 || redirectTargetCalls.Load() != 0 {
				t.Fatalf("upstream/redirect target calls = %d/%d, want 1/0", upstreamCalls.Load(), redirectTargetCalls.Load())
			}
		})
	}
}

func t133Authenticator(t *testing.T) (auth.GeneratedGatewayKey, *auth.Authenticator) {
	t.Helper()
	pepper := []byte("t133-pepper")
	key, err := auth.GenerateGatewayKey(pepper)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := auth.NewAuthenticator(pepper, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := authenticator.Load([]auth.Record{{
		ID: "t133-key", DisplayPrefix: key.DisplayPrefix, Digest: key.Digest,
		Enabled: true, PolicyJSON: []byte(`{"log_request_body":true}`),
	}}); err != nil {
		t.Fatal(err)
	}
	return key, authenticator
}
