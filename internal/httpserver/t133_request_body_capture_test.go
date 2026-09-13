package httpserver

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
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
