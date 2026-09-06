package httpserver

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/transport"
)

func TestT095TransparentSSEReconcilesUsageAtPhysicalEOF(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{
			name: "without DONE",
			body: "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\n" +
				"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: {\"usage\":{\"prompt_tokens\":0,\"completion_tokens\":1,\"total_tokens\":1}}\n\n",
		},
		{
			name: "usage-only terminal event and DONE",
			body: "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\n" +
				"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: {\"usage\":{\"prompt_tokens\":0,\"completion_tokens\":1,\"total_tokens\":1}}\n\n" +
				"data: [DONE]\n\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			clock := &requestLimitTestClock{now: time.Unix(30, 0).UTC()}
			key, authenticator := requestLimitTestAuthenticator(t, []byte("t095-"+test.name), "t095", `{"token_windows":[{"amount":5,"duration":"1m"}]}`, clock)
			upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Type", "text/event-stream")
				for _, fragment := range []string{test.body[:len(test.body)/3], test.body[len(test.body)/3 : 2*len(test.body)/3], test.body[2*len(test.body)/3:]} {
					if _, err := io.WriteString(response, fragment); err != nil {
						return
					}
					if flusher, ok := response.(http.Flusher); ok {
						flusher.Flush()
					}
				}
			}))
			t.Cleanup(upstream.Close)
			tokens := limiter.NewTokenLimiter(clock.Now)
			worker := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: 1})
			t.Cleanup(func() { shutdownObservationWorker(t, worker) })
			gateway := httptest.NewServer(NewHandlerWithAuthenticatorAndLimitersAndTokenLimiterAndUsageObservationWorker(
				transport.NewClient(), upstream.URL, "upstream-secret", authenticator,
				limiter.NewRequestLimiter(clock.Now), limiter.NewConcurrencyLimiter(), nil,
				tokens, TokenAdmissionConfig{FallbackUnknownInputTokens: 2, FallbackMaxOutputTokens: 2}, worker,
			))
			t.Cleanup(gateway.Close)

			request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-test","stream":true}`))
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
			if response.StatusCode != http.StatusOK || string(body) != test.body {
				t.Fatalf("transparent response = %d/%q, want 200/%q", response.StatusCode, body, test.body)
			}
			waitForObservationCount(t, worker, 1)

			// The request reserved four tokens and used one. A fresh four-token
			// admission proves that the worker adjusted the deferred ticket.
			second, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-test","stream":true}`))
			if err != nil {
				t.Fatal(err)
			}
			second.Header.Set("Authorization", "Bearer "+key.RawKey)
			second.Header.Set("Content-Type", "application/json")
			secondResponse, err := http.DefaultClient.Do(second)
			if err != nil {
				t.Fatal(err)
			}
			secondResponse.Body.Close()
			if secondResponse.StatusCode != http.StatusOK {
				t.Fatalf("reconciled admission status = %d, want 200", secondResponse.StatusCode)
			}
		})
	}
}

func TestT095TransparentGzipSSEReconcilesWithoutChangingWireBytes(t *testing.T) {
	plain := []byte("data: {\"usage\":{\"prompt_tokens\":0,\"completion_tokens\":1,\"total_tokens\":1}}\n\ndata: [DONE]\n\n")
	var encoded bytes.Buffer
	compressor := gzip.NewWriter(&encoded)
	if _, err := compressor.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := compressor.Close(); err != nil {
		t.Fatal(err)
	}
	want := append([]byte(nil), encoded.Bytes()...)
	clock := &requestLimitTestClock{now: time.Unix(30, 0).UTC()}
	key, authenticator := requestLimitTestAuthenticator(t, []byte("t095-gzip"), "t095-gzip", `{"token_windows":[{"amount":5,"duration":"1m"}]}`, clock)
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		response.Header().Set("Content-Encoding", "gzip")
		_, _ = response.Write(want)
	}))
	t.Cleanup(upstream.Close)
	worker := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: 1})
	t.Cleanup(func() { shutdownObservationWorker(t, worker) })
	gateway := httptest.NewServer(NewHandlerWithAuthenticatorAndLimitersAndTokenLimiterAndUsageObservationWorker(
		transport.NewClient(), upstream.URL, "upstream-secret", authenticator,
		limiter.NewRequestLimiter(clock.Now), limiter.NewConcurrencyLimiter(), nil,
		limiter.NewTokenLimiter(clock.Now), TokenAdmissionConfig{FallbackUnknownInputTokens: 2, FallbackMaxOutputTokens: 2}, worker,
	))
	t.Cleanup(gateway.Close)

	request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-test","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+key.RawKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Transport: &http.Transport{DisableCompression: true}}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.Header.Get("Content-Encoding") != "gzip" || !bytes.Equal(got, want) {
		t.Fatalf("transparent gzip = encoding %q/body %x, want gzip/%x", response.Header.Get("Content-Encoding"), got, want)
	}
	waitForObservationCount(t, worker, 1)
}

func TestT095TransparentSSETrailingMalformedDataRetainsConservativeCharge(t *testing.T) {
	const body = "data: {\"usage\":{\"prompt_tokens\":0,\"completion_tokens\":1,\"total_tokens\":1}}\n\ndata: [DONE]\n\ndata: {truncated"
	clock := &requestLimitTestClock{now: time.Unix(30, 0).UTC()}
	key, authenticator := requestLimitTestAuthenticator(t, []byte("t095-trailing-malformed"), "t095-trailing", `{"token_mode":"usage_only","token_windows":[{"amount":5,"duration":"1m"}]}`, clock)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		response.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(response, body)
	}))
	t.Cleanup(upstream.Close)
	worker := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: 1})
	t.Cleanup(func() { shutdownObservationWorker(t, worker) })
	gateway := httptest.NewServer(NewHandlerWithAuthenticatorAndLimitersAndTokenLimiterAndUsageObservationWorker(
		transport.NewClient(), upstream.URL, "upstream-secret", authenticator,
		limiter.NewRequestLimiter(clock.Now), limiter.NewConcurrencyLimiter(), nil,
		limiter.NewTokenLimiter(clock.Now), TokenAdmissionConfig{FallbackUnknownInputTokens: 2, FallbackMaxOutputTokens: 2}, worker,
	))
	t.Cleanup(gateway.Close)

	request := func() *http.Response {
		req, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-test","stream":true}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+key.RawKey)
		req.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	first := request()
	got, err := io.ReadAll(first.Body)
	first.Body.Close()
	if err != nil || first.StatusCode != http.StatusOK || string(got) != body {
		t.Fatalf("transparent malformed trailing response = %d/%q (%v), want original 200 body", first.StatusCode, got, err)
	}
	waitForObservationCount(t, worker, 1)
	second := request()
	second.Body.Close()
	if second.StatusCode != http.StatusTooManyRequests || calls.Load() != 1 {
		t.Fatalf("post-malformed admission = status %d, calls %d; want 429 and one upstream call", second.StatusCode, calls.Load())
	}
}
