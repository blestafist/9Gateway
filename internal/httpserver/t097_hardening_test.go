package httpserver

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/transport"
)

func TestT097HTTPTokenLifecycleAcrossPoliciesAndRedaction(t *testing.T) {
	clock := &requestLimitTestClock{now: time.Unix(30, 0).UTC()}
	pepper := []byte("t097-pepper-secret")
	upstreamCredential := "t097-upstream-secret"
	keyA, _ := t097Key(t, pepper, "key-a", `{"allowed_models":["model-a"],"request_windows":[{"amount":8,"duration":"1m"}],"token_windows":[{"amount":30,"duration":"1m"}],"max_concurrent_requests":1}`)
	keyB, _ := t097Key(t, pepper, "key-b", `{"allowed_models":["model-b"],"request_windows":[{"amount":10,"duration":"1m"}],"token_windows":[{"amount":40,"duration":"1m"}],"max_concurrent_requests":2}`)
	authenticator, err := auth.NewAuthenticator(pepper, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := authenticator.Load([]auth.Record{
		{ID: "key-a", DisplayPrefix: keyA.DisplayPrefix, Digest: keyA.Digest, Enabled: true, PolicyJSON: []byte(`{"allowed_models":["model-a"],"request_windows":[{"amount":8,"duration":"1m"}],"token_windows":[{"amount":30,"duration":"1m"}],"max_concurrent_requests":1}`)},
		{ID: "key-b", DisplayPrefix: keyB.DisplayPrefix, Digest: keyB.Digest, Enabled: true, PolicyJSON: []byte(`{"allowed_models":["model-b"],"request_windows":[{"amount":10,"duration":"1m"}],"token_windows":[{"amount":40,"duration":"1m"}],"max_concurrent_requests":2}`)},
	}); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	var lastBody atomic.Value
	cancelSeen := make(chan struct{})
	var cancelOnce sync.Once
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(request.Body)
		lastBody.Store(append([]byte(nil), body...))
		if request.Header.Get("Authorization") != "Bearer "+upstreamCredential {
			t.Errorf("upstream authorization = %q", request.Header.Get("Authorization"))
		}
		switch request.Header.Get("X-T097-Case") {
		case "sse", "convert":
			response.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(response, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"response-fragment\"}}]}\n\n")
			_, _ = io.WriteString(response, "data: {\"usage\":{\"prompt_tokens\":0,\"completion_tokens\":1,\"total_tokens\":1}}\n\ndata: [DONE]\n\n")
		case "missing":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"id":"missing-usage","choices":[]}`)
		case "error":
			response.Header().Set("Content-Type", "application/json")
			response.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(response, `{"error":"upstream-response-fragment"}`)
		case "cancel":
			response.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(response, "data: {\"choices\":[{\"delta\":{\"content\":\"response-fragment\"}}]}\n\n")
			if flusher, ok := response.(http.Flusher); ok {
				flusher.Flush()
			}
			<-request.Context().Done()
			cancelOnce.Do(func() { close(cancelSeen) })
		default:
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"id":"json","usage":{"prompt_tokens":0,"completion_tokens":1,"total_tokens":1},"response":"response-fragment"}`)
		}
	}))
	t.Cleanup(upstream.Close)
	var logs bytes.Buffer
	completionLogger := NewCompletionLogger(slog.New(slog.NewJSONHandler(&logs, nil)), 32)
	t.Cleanup(func() { _ = completionLogger.Shutdown(context.Background()) })
	worker := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: 8})
	t.Cleanup(func() { shutdownObservationWorker(t, worker) })
	tokens := limiter.NewTokenLimiter(clock.Now)
	concurrency := limiter.NewConcurrencyLimiter()
	gateway := httptest.NewServer(NewHandlerWithAuthenticatorAndLimitersAndTokenLimiterAndUsageObservationWorker(
		transport.NewClient(), upstream.URL, upstreamCredential, authenticator,
		limiter.NewRequestLimiter(clock.Now), concurrency, completionLogger, tokens,
		TokenAdmissionConfig{FallbackUnknownInputTokens: 2, FallbackMaxOutputTokens: 2, MaxInspectedRequestBytes: 32}, worker,
	))
	t.Cleanup(gateway.Close)

	do := func(rawKey, caseName, body string, stream bool) (int, []byte) {
		t.Helper()
		if stream {
			body = strings.TrimSuffix(body, "}") + `,"stream":true}`
		}
		request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+rawKey)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-T097-Case", caseName)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		result, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		return response.StatusCode, result
	}

	jsonStatus, jsonBody := do(keyA.RawKey, "json", `{"model":"model-a","max_tokens":4,"messages":[{"role":"user","content":"prompt-fragment"}]}`, false)
	if jsonStatus != http.StatusOK || !bytes.Contains(jsonBody, []byte("response-fragment")) {
		t.Fatalf("JSON lifecycle = %d/%q", jsonStatus, jsonBody)
	}
	waitForObservationCount(t, worker, 1)
	if status, body := do(keyB.RawKey, "sse", `{"model":"model-b"}`, true); status != http.StatusOK || !bytes.Contains(body, []byte("response-fragment")) {
		t.Fatalf("transparent SSE lifecycle = %d/%q", status, body)
	}
	waitForObservationCount(t, worker, 2)
	if status, body := do(keyB.RawKey, "convert", `{"model":"model-b","stream":false}`, false); status != http.StatusOK || !bytes.Contains(body, []byte(`"total_tokens":1`)) {
		t.Fatalf("SSE conversion lifecycle = %d/%q", status, body)
	}
	if status, _ := do(keyA.RawKey, "missing", `{"model":"model-a"}`, false); status != http.StatusOK {
		t.Fatal("missing usage was not transported")
	}
	if status, body := do(keyA.RawKey, "error", `{"model":"model-a"}`, false); status != http.StatusServiceUnavailable || !bytes.Contains(body, []byte("upstream-response-fragment")) {
		t.Fatalf("upstream error lifecycle = %d/%q", status, body)
	}
	if status, _ := do(keyA.RawKey, "malformed", `{"model":`, false); status != http.StatusOK {
		t.Fatal("malformed bounded input was not forwarded")
	}
	if status, _ := do(keyB.RawKey, "oversized", strings.Repeat("x", 128), false); status != http.StatusOK {
		t.Fatal("oversized bounded input was not forwarded")
	}
	if status, _ := do(keyA.RawKey, "denied", `{"model":"model-b"}`, false); status != http.StatusForbidden {
		t.Fatal("model policy did not reject before upstream")
	}
	if got := calls.Load(); got != 7 {
		t.Fatalf("upstream calls = %d, want 7 admitted requests", got)
	}

	cancelContext, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(cancelContext, http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{"model":"model-b"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+keyB.RawKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-T097-Case", "cancel")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	fragment := make([]byte, len("data: {\"choices\":[{\"delta\":{\"content\":\"response-fragment\"}}]}\n\n"))
	if _, err := io.ReadFull(response.Body, fragment); err != nil {
		t.Fatal(err)
	}
	cancel()
	response.Body.Close()
	select {
	case <-cancelSeen:
	case <-time.After(time.Second):
		t.Fatal("upstream did not observe cancellation")
	}
	deadline := time.Now().Add(time.Second)
	for concurrency.Len() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if concurrency.Len() != 0 {
		t.Fatal("cancellation leaked concurrency lease")
	}

	if got := lastBody.Load().([]byte); !bytes.Contains(got, []byte("model")) {
		t.Fatalf("upstream body was not captured")
	}
	if err := completionLogger.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{string(pepper), upstreamCredential, keyA.RawKey, keyB.RawKey, "prompt-fragment", "response-fragment"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("structured logs contain %q", secret)
		}
	}
}

func TestT097HTTPTokenReservationsPreventSharedWindowOversubscription(t *testing.T) {
	clock := &requestLimitTestClock{now: time.Unix(30, 0).UTC()}
	pepper := []byte("t097-contention-pepper")
	keyA, _ := t097Key(t, pepper, "contention-a", `{"token_mode":"usage_only","token_windows":[{"amount":8,"duration":"1m"}],"max_concurrent_requests":3}`)
	keyB, _ := t097Key(t, pepper, "contention-b", `{"token_mode":"usage_only","token_windows":[{"amount":8,"duration":"1m"}],"max_concurrent_requests":3}`)
	authenticator, err := auth.NewAuthenticator(pepper, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := authenticator.Load([]auth.Record{
		{ID: "contention-a", DisplayPrefix: keyA.DisplayPrefix, Digest: keyA.Digest, Enabled: true, PolicyJSON: []byte(`{"token_mode":"usage_only","token_windows":[{"amount":8,"duration":"1m"}],"max_concurrent_requests":3}`)},
		{ID: "contention-b", DisplayPrefix: keyB.DisplayPrefix, Digest: keyB.Digest, Enabled: true, PolicyJSON: []byte(`{"token_mode":"usage_only","token_windows":[{"amount":8,"duration":"1m"}],"max_concurrent_requests":3}`)},
	}); err != nil {
		t.Fatal(err)
	}
	arrivals := make(chan struct{}, 2)
	release := make(chan struct{})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		response.Header().Set("Content-Type", "application/json")
		arrivals <- struct{}{}
		<-release
		_, _ = io.WriteString(response, `{"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0},"ok":true}`)
	}))
	t.Cleanup(upstream.Close)
	tokens := limiter.NewTokenLimiter(clock.Now)
	concurrency := limiter.NewConcurrencyLimiter()
	gateway := httptest.NewServer(NewHandlerWithAuthenticatorAndLimitersAndTokenLimiter(
		transport.NewClient(), upstream.URL, "shared-upstream-secret", authenticator,
		limiter.NewRequestLimiter(clock.Now), concurrency, nil, tokens,
		TokenAdmissionConfig{FallbackUnknownInputTokens: 2, FallbackMaxOutputTokens: 2},
	))
	t.Cleanup(gateway.Close)

	do := func(ctx context.Context, rawKey string) (*http.Response, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{"model":"shared-model"}`))
		if err != nil {
			return nil, err
		}
		request.Header.Set("Authorization", "Bearer "+rawKey)
		request.Header.Set("Content-Type", "application/json")
		return http.DefaultClient.Do(request)
	}
	results := make(chan *http.Response, 2)
	for _, rawKey := range []string{keyA.RawKey, keyA.RawKey} {
		go func(rawKey string) {
			response, requestErr := do(context.Background(), rawKey)
			if requestErr != nil {
				t.Errorf("admitted contention request: %v", requestErr)
				return
			}
			results <- response
		}(rawKey)
	}
	for range 2 {
		select {
		case <-arrivals:
		case <-time.After(time.Second):
			t.Fatal("active reservations did not reach shared upstream")
		}
	}
	third, err := do(context.Background(), keyA.RawKey)
	if err != nil {
		t.Fatal(err)
	}
	thirdBody, _ := io.ReadAll(third.Body)
	third.Body.Close()
	if third.StatusCode != http.StatusTooManyRequests || !bytes.Contains(thirdBody, []byte(`"code":"token_limit_exceeded"`)) {
		t.Fatalf("oversubscribed token admission = %d/%q", third.StatusCode, thirdBody)
	}
	if calls.Load() != 2 {
		t.Fatalf("token-rejected request reached upstream: calls %d", calls.Load())
	}
	close(release)
	for range 2 {
		response := <-results
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("admitted contention status = %d", response.StatusCode)
		}
	}
	if got := concurrency.Len(); got != 0 {
		t.Fatalf("contention retained concurrency state = %d", got)
	}
}

func t097Key(t *testing.T, pepper []byte, id, policy string) (auth.GeneratedGatewayKey, *auth.Authenticator) {
	t.Helper()
	key, err := auth.GenerateGatewayKey(pepper)
	if err != nil {
		t.Fatal(err)
	}
	return key, nil
}
