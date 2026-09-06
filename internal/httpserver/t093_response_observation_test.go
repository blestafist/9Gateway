package httpserver

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/transport"
)

func TestT093TransparentJSONReconcilesKnownUsageAfterForwarding(t *testing.T) {
	clock := &requestLimitTestClock{now: time.Unix(30, 0).UTC()}
	key, authenticator := requestLimitTestAuthenticator(t, []byte("t093-pepper"), "t093", `{"token_windows":[{"amount":4,"duration":"1m"}]}`, clock)
	wire := []byte(`{"id":"kept","usage":{"prompt_tokens":0,"completion_tokens":1,"total_tokens":1}}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("X-T093", "preserve")
		response.WriteHeader(http.StatusBadRequest)
		_, _ = response.Write(wire)
	}))
	t.Cleanup(upstream.Close)
	worker := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: 1})
	t.Cleanup(func() { shutdownObservationWorker(t, worker) })
	gateway := httptest.NewServer(NewHandlerWithAuthenticatorAndLimitersAndTokenLimiterAndUsageObservationWorker(
		transport.NewClient(), upstream.URL, "upstream-secret", authenticator,
		limiter.NewRequestLimiter(clock.Now), limiter.NewConcurrencyLimiter(), nil,
		limiter.NewTokenLimiter(clock.Now), TokenAdmissionConfig{FallbackUnknownInputTokens: 2, FallbackMaxOutputTokens: 1}, worker,
	))
	t.Cleanup(gateway.Close)

	request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", bytes.NewReader([]byte(`{"model":"gpt-test","max_tokens":1}`)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+key.RawKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest || response.Header.Get("X-T093") != "preserve" || !bytes.Equal(body, wire) {
		t.Fatalf("transparent response = %d/%q/%q", response.StatusCode, response.Header.Get("X-T093"), body)
	}
	waitForObservationCount(t, worker, 1)

	// The conservative reservation is three tokens; known total usage is one.
	// A second three-token admission therefore succeeds only after reconciliation.
	request, err = http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", bytes.NewReader([]byte(`{"model":"gpt-test","max_tokens":1}`)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+key.RawKey)
	request.Header.Set("Content-Type", "application/json")
	second, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	second.Body.Close()
	if second.StatusCode != http.StatusBadRequest {
		t.Fatalf("reconciled second admission status = %d, want %d", second.StatusCode, http.StatusBadRequest)
	}
}

func TestT093TransparentGzipObservationDoesNotRewriteWireBytes(t *testing.T) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, _ = writer.Write([]byte(`{"usage":{"total_tokens":0}}`))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	coding := compressed.Bytes()
	if _, err := responseObservationCoding(http.Header{"Content-Encoding": []string{"gzip"}}); err != nil {
		t.Fatal(err)
	}
	observation := newResponseObservation(int64(len(coding)), ContentCodingGZIP)
	observation.record(coding)
	observation.finish(nil)
	if !observation.eligible || !bytes.Equal(observation.bytes, coding) {
		t.Fatalf("gzip observation changed captured wire bytes")
	}
}
