package httpserver

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/storage"
	"github.com/pestit/9gateway/internal/transport"
)

// TestT100PersistentTokenAccountingLifecycle combines the persistent policy,
// token limiter and transparent transport boundaries. Each process owns fresh
// limiters and workers; only committed token aggregates and key policy cross
// the restart boundary.
func TestT100PersistentTokenAccountingLifecycle(t *testing.T) {
	clock := &requestLimitTestClock{now: time.Unix(30, 0).UTC()}
	pepper := []byte("t100-pepper-secret")
	adminCredential := "t100-admin-secret"
	upstreamCredential := "t100-upstream-secret"

	keyA := t100GenerateKey(t, pepper)
	keyB := t100GenerateKey(t, pepper)
	policyA := `{"allowed_models":["model-a"],"request_windows":[{"amount":10,"duration":"1m"}],"token_windows":[{"amount":30,"duration":"1m"}],"token_mode":"estimate","max_concurrent_requests":1}`
	policyB := `{"allowed_models":["model-b"],"request_windows":[{"amount":7,"duration":"1m"}],"token_windows":[{"amount":1000,"duration":"1m"}],"token_mode":"usage_only","max_concurrent_requests":2}`
	databasePath := filepath.Join(t.TempDir(), "t100.db")
	createdAt := time.Unix(1, 0).UTC()

	database, err := storage.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	repository := storage.NewAPIKeyRepository(database)
	for _, key := range []struct {
		id, name, policy string
		value            auth.GeneratedGatewayKey
	}{
		{id: "key-a", name: "persistent-a", policy: policyA, value: keyA},
		{id: "key-b", name: "persistent-b", policy: policyB, value: keyB},
	} {
		if err := repository.Insert(context.Background(), storage.APIKeyRecord{
			ID: key.id, Name: key.name, DisplayPrefix: key.value.DisplayPrefix,
			Digest: key.value.Digest, Enabled: true, CreatedAt: createdAt,
			UpdatedAt: createdAt, PolicyJSON: key.policy,
		}); err != nil {
			t.Fatalf("insert %s: %v", key.id, err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	upstream := newT100Upstream(t, upstreamCredential)
	process := t100StartProcess(t, databasePath, clock, upstream.server.URL, upstreamCredential, adminCredential, string(pepper), &logs)

	client := transport.NewClient()
	request := func(method, target, rawKey string, body []byte, headers map[string]string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, process.gateway.URL+target, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if rawKey != "" {
			req.Header.Set("Authorization", "Bearer "+rawKey)
		}
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	read := func(response *http.Response) []byte {
		t.Helper()
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	assertSafe := func(response *http.Response, body []byte) {
		t.Helper()
		combined := response.Header.Clone()
		text := string(body)
		for name, values := range combined {
			text += name + strings.Join(values, "|")
		}
		for _, secret := range []string{
			string(pepper), adminCredential, upstreamCredential,
			keyA.RawKey, keyB.RawKey, hex.EncodeToString(keyA.Digest), hex.EncodeToString(keyB.Digest),
			"sqlite", "database is locked", "prompt-body-fragment",
		} {
			if strings.Contains(strings.ToLower(text), strings.ToLower(secret)) {
				t.Fatalf("response contains sensitive value %q: %q", secret, text)
			}
		}
	}

	// Estimate mode reserves the bounded request estimate (ten tokens here),
	// while the upstream reports 25. Reconciliation therefore leaves the
	// durable bucket charged at the observed total rather than the estimate.
	jsonResponse := request(http.MethodPost, "/v1/chat/completions", keyA.RawKey,
		[]byte(`{"model":"model-a","max_tokens":1,"messages":[{"role":"user","content":"x"}]}`),
		map[string]string{"Content-Type": "application/json", "X-T100-Case": "a-json"})
	jsonBody := read(jsonResponse)
	assertSafe(jsonResponse, jsonBody)
	if jsonResponse.StatusCode != http.StatusOK || !bytes.Contains(jsonBody, []byte(`"total_tokens":25`)) {
		t.Fatalf("estimated JSON response = %d/%q", jsonResponse.StatusCode, jsonBody)
	}
	t100WaitFor(t, func() bool { return process.worker.Stats().Succeeded >= 1 })

	// A second A request is refused while the first hold owns the only
	// concurrency slot. It is rejected before the upstream sees it.
	holdRequest, err := http.NewRequest(http.MethodGet, process.gateway.URL+"/v1/hold", nil)
	if err != nil {
		t.Fatal(err)
	}
	holdRequest.Header.Set("Authorization", "Bearer "+keyA.RawKey)
	holdRequest.Header.Set("X-T100-Case", "a-hold")
	holdResponse, err := client.Do(holdRequest)
	if err != nil {
		t.Fatal(err)
	}
	upstream.waitFor("a-hold")
	before := upstream.calls.Load()
	concurrent := request(http.MethodGet, "/v1/hold", keyA.RawKey, nil, map[string]string{"X-T100-Case": "a-hold"})
	concurrentBody := read(concurrent)
	assertSafe(concurrent, concurrentBody)
	if concurrent.StatusCode != http.StatusTooManyRequests || !bytes.Contains(concurrentBody, []byte(`"code":"concurrency_limit_exceeded"`)) {
		t.Fatalf("concurrency saturation = %d/%q", concurrent.StatusCode, concurrentBody)
	}
	if upstream.calls.Load() != before {
		t.Fatalf("concurrency rejection reached upstream: %d -> %d", before, upstream.calls.Load())
	}
	upstream.release("a-hold")
	if body := read(holdResponse); !bytes.Contains(body, []byte("data: hold")) {
		t.Fatalf("hold body = %q", body)
	}

	// Exercise ordinary JSON, transparent SSE, explicit SSE-to-JSON, absent
	// usage, invalid usage, upstream errors, and client cancellation together.
	doB := func(caseName, body string) (*http.Response, []byte) {
		t.Helper()
		response := request(http.MethodPost, "/v1/chat/completions", keyB.RawKey, []byte(body), map[string]string{
			"Content-Type": "application/json", "X-T100-Case": caseName,
		})
		result := read(response)
		assertSafe(response, result)
		return response, result
	}
	if response, body := doB("b-json", `{"model":"model-b"}`); response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("json-body")) {
		t.Fatalf("ordinary JSON = %d/%q", response.StatusCode, body)
	}
	if response, body := doB("b-sse", `{"model":"model-b","stream":true}`); response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("data: sse")) {
		t.Fatalf("transparent SSE = %d/%q", response.StatusCode, body)
	}
	if response, body := doB("b-convert", `{"model":"model-b","stream":false}`); response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"total_tokens":5`)) {
		t.Fatalf("SSE-to-JSON = %d/%q", response.StatusCode, body)
	}
	if response, body := doB("b-missing", `{"model":"model-b"}`); response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("missing-usage")) {
		t.Fatalf("absent usage = %d/%q", response.StatusCode, body)
	}
	if response, body := doB("b-invalid", `{"model":"model-b"}`); response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("invalid-usage")) {
		t.Fatalf("invalid usage = %d/%q", response.StatusCode, body)
	}
	if response, body := doB("b-error", `{"model":"model-b"}`); response.StatusCode != http.StatusServiceUnavailable || !bytes.Contains(body, []byte("upstream-error")) {
		t.Fatalf("upstream error = %d/%q", response.StatusCode, body)
	}

	cancelContext, cancel := context.WithCancel(context.Background())
	cancelRequest, err := http.NewRequestWithContext(cancelContext, http.MethodPost, process.gateway.URL+"/v1/chat/completions", strings.NewReader(`{"model":"model-b","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	cancelRequest.Header.Set("Authorization", "Bearer "+keyB.RawKey)
	cancelRequest.Header.Set("Content-Type", "application/json")
	cancelRequest.Header.Set("X-T100-Case", "b-cancel")
	cancelResponse, err := client.Do(cancelRequest)
	if err != nil {
		t.Fatal(err)
	}
	fragment := []byte("data: cancel\n\n")
	if got, readErr := io.ReadFull(cancelResponse.Body, make([]byte, len(fragment))); readErr != nil || got != len(fragment) {
		t.Fatalf("cancel first flush = %d/%v", got, readErr)
	}
	cancel()
	cancelResponse.Body.Close()
	upstream.waitForCancel(t)
	t100WaitFor(t, func() bool { return process.concurrency.Len() == 0 })

	// Six admitted B requests consumed the request window. The seventh is
	// rejected without an upstream call; the request limiter is independent of
	// token reconciliation and transport errors.
	before = upstream.calls.Load()
	windowRejected := request(http.MethodGet, "/v1/window", keyB.RawKey, nil, map[string]string{"X-T100-Case": "b-json"})
	windowBody := read(windowRejected)
	assertSafe(windowRejected, windowBody)
	if windowRejected.StatusCode != http.StatusTooManyRequests || !bytes.Contains(windowBody, []byte(`"code":"request_limit_exceeded"`)) {
		t.Fatalf("request window rejection = %d/%q", windowRejected.StatusCode, windowBody)
	}
	if upstream.calls.Load() != before {
		t.Fatalf("request-window rejection reached upstream")
	}

	// Replacement is prepared, persisted, and published atomically. The old
	// model is rejected without contacting upstream, while the new model keeps
	// the same token-window identity so committed accounting remains restorable.
	replacement := newPolicyRequest(t, process.gateway.URL, "key-a", `{"enabled":true,"policy":{"allowed_models":["model-a-new"],"request_windows":[{"amount":20,"duration":"1m"}],"token_windows":[{"amount":30,"duration":"1m"}],"token_mode":"estimate","max_concurrent_requests":1}}`, adminCredential)
	replacement.Method = http.MethodPut
	replacementResponse, err := client.Do(replacement)
	if err != nil {
		t.Fatal(err)
	}
	replacementBody := read(replacementResponse)
	assertSafe(replacementResponse, replacementBody)
	if replacementResponse.StatusCode != http.StatusOK || !bytes.Contains(replacementBody, []byte("model-a-new")) {
		t.Fatalf("policy replacement = %d/%q", replacementResponse.StatusCode, replacementBody)
	}
	oldModel := request(http.MethodPost, "/v1/chat/completions", keyA.RawKey, []byte(`{"model":"model-a"}`), map[string]string{"Content-Type": "application/json"})
	oldModelBody := read(oldModel)
	assertSafe(oldModel, oldModelBody)
	if oldModel.StatusCode != http.StatusForbidden || upstream.calls.Load() != before {
		t.Fatalf("old model replacement result = %d/%q", oldModel.StatusCode, oldModelBody)
	}

	t100StopProcess(t, process)
	if got := t100Committed(t, databasePath, clock.Now()); got != 28 {
		t.Fatalf("committed pre-restart A usage = %d, want 28", got)
	}

	// A fresh process restores the committed 29-token bucket but no active
	// reservation or concurrency slot. The estimate cannot fit until the fixed
	// token window resets, after which the new policy admits normally.
	process = t100StartProcess(t, databasePath, clock, upstream.server.URL, upstreamCredential, adminCredential, string(pepper), &logs)
	restored := request(http.MethodPost, "/v1/chat/completions", keyA.RawKey, []byte(`{"model":"model-a-new","max_tokens":10,"messages":[{"role":"user","content":"x"}]}`), map[string]string{"Content-Type": "application/json", "X-T100-Case": "a-after-reset"})
	restoredBody := read(restored)
	assertSafe(restored, restoredBody)
	if restored.StatusCode != http.StatusTooManyRequests || !bytes.Contains(restoredBody, []byte(`"code":"token_limit_exceeded"`)) {
		t.Fatalf("restored usage admission = %d/%q", restored.StatusCode, restoredBody)
	}
	if upstream.calls.Load() != before {
		t.Fatalf("restored token rejection reached upstream")
	}
	clock.Set(time.Unix(60, 0).UTC())
	afterReset := request(http.MethodPost, "/v1/chat/completions", keyA.RawKey, []byte(`{"model":"model-a-new","max_tokens":1,"messages":[{"role":"user","content":"x"}]}`), map[string]string{"Content-Type": "application/json", "X-T100-Case": "a-after-reset"})
	afterResetBody := read(afterReset)
	assertSafe(afterReset, afterResetBody)
	if afterReset.StatusCode != http.StatusOK || !bytes.Contains(afterResetBody, []byte(`"total_tokens":1`)) {
		t.Fatalf("post-reset admission = %d/%q", afterReset.StatusCode, afterResetBody)
	}
	t100WaitFor(t, func() bool { return process.worker.Stats().Succeeded >= 1 })
	t100StopProcess(t, process)

	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if line == "" {
			continue
		}
		if !json.Valid([]byte(line)) {
			t.Fatalf("completion log is not structured JSON: %q", line)
		}
		lower := strings.ToLower(line)
		for _, secret := range []string{string(pepper), adminCredential, upstreamCredential, keyA.RawKey, keyB.RawKey, hex.EncodeToString(keyA.Digest), hex.EncodeToString(keyB.Digest), "prompt-body-fragment", "sqlite", "database is locked"} {
			if strings.Contains(lower, strings.ToLower(secret)) {
				t.Fatalf("structured log contains sensitive value %q", secret)
			}
		}
	}
}

type t100Process struct {
	database    *storage.DB
	gateway     *httptest.Server
	worker      *UsageObservationWorker
	accumulator *storage.UsageAggregateAccumulator
	logger      *CompletionLogger
	concurrency *limiter.ConcurrencyLimiter
}

func t100StartProcess(t *testing.T, databasePath string, clock *requestLimitTestClock, upstreamURL, upstreamCredential, adminCredential, pepper string, logs io.Writer) *t100Process {
	t.Helper()
	database, err := storage.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	repository := storage.NewAPIKeyRepository(database)
	tokenLimiter := limiter.NewTokenLimiter(clock.Now)
	usageRepository := storage.NewUsageBucketRepository(database)
	records, err := repository.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	policies := make(map[string]auth.EffectivePolicy, len(records))
	for _, record := range records {
		policy, policyErr := auth.ParsePolicyJSONWithTokenMode(recordPolicy(record), auth.TokenModeEstimate)
		if policyErr != nil {
			t.Fatal(policyErr)
		}
		policies[record.ID] = policy
	}
	buckets, err := usageRepository.LoadUnexpired(context.Background(), clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	committed := make([]limiter.CommittedTokenBucket, 0, len(buckets))
	for _, bucket := range buckets {
		policy, ok := policies[bucket.APIKeyID]
		if !ok {
			t.Fatalf("persisted bucket has unknown key %q", bucket.APIKeyID)
		}
		var matched limiter.TokenWindow
		for _, window := range policy.TokenWindows() {
			if int64(window.Duration/time.Second) == bucket.BucketSeconds && window.Amount == bucket.BucketAmount {
				matched = window
				break
			}
		}
		if matched.Duration == 0 {
			t.Fatalf("persisted bucket does not match policy for %q", bucket.APIKeyID)
		}
		committed = append(committed, limiter.CommittedTokenBucket{KeyID: bucket.APIKeyID, BucketStart: bucket.BucketStart, Window: matched, CommittedTokens: bucket.CommittedTokens})
	}
	if err := tokenLimiter.LoadCommitted(clock.Now(), committed); err != nil {
		t.Fatal(err)
	}
	accumulator := storage.NewUsageAggregateAccumulator(usageRepository)
	tokenLimiter.SetCommittedDeltaSink(accumulator.Sink)
	logger := NewCompletionLogger(slog.New(slog.NewJSONHandler(logs, nil)), 64)
	worker := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: 16})
	concurrency := limiter.NewConcurrencyLimiter()
	handler, err := NewHandlerWithAdminAndLimitersAndTokenConfigAndTokenLimiterAndUsageObservationWorker(
		transport.NewClient(), upstreamURL, upstreamCredential, adminCredential, pepper, repository,
		limiter.NewRequestLimiter(clock.Now), concurrency, logger, tokenLimiter,
		TokenAdmissionConfig{FallbackUnknownInputTokens: 2, FallbackMaxOutputTokens: 1, MaxInspectedRequestBytes: 4096}, worker,
		auth.TokenModeEstimate,
	)
	if err != nil {
		t.Fatal(err)
	}
	return &t100Process{database: database, gateway: httptest.NewServer(handler), worker: worker, accumulator: accumulator, logger: logger, concurrency: concurrency}
}

func t100StopProcess(t *testing.T, process *t100Process) {
	t.Helper()
	process.gateway.Close()
	if err := process.worker.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := process.logger.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := process.accumulator.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := process.database.Close(); err != nil {
		t.Fatal(err)
	}
}

func t100Committed(t *testing.T, path string, now time.Time) int64 {
	t.Helper()
	database, err := storage.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	buckets, err := storage.NewUsageBucketRepository(database).LoadUnexpired(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	for _, bucket := range buckets {
		if bucket.APIKeyID == "key-a" {
			return bucket.CommittedTokens
		}
	}
	return 0
}

func recordPolicy(record storage.APIKeyRecord) []byte { return []byte(record.PolicyJSON) }

func t100GenerateKey(t *testing.T, pepper []byte) auth.GeneratedGatewayKey {
	t.Helper()
	key, err := auth.GenerateGatewayKey(pepper)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

type t100Upstream struct {
	server     *httptest.Server
	credential string
	calls      atomic.Int32
	mu         sync.Mutex
	started    map[string]chan struct{}
	releaseCh  map[string]chan struct{}
	cancelled  chan struct{}
	cancelOnce sync.Once
}

func newT100Upstream(t *testing.T, credential string) *t100Upstream {
	t.Helper()
	upstream := &t100Upstream{credential: credential, started: make(map[string]chan struct{}), releaseCh: make(map[string]chan struct{}), cancelled: make(chan struct{})}
	for _, name := range []string{"a-hold"} {
		upstream.started[name] = make(chan struct{}, 2)
		upstream.releaseCh[name] = make(chan struct{})
	}
	upstream.server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+upstream.credential {
			t.Errorf("upstream authorization = %q", request.Header.Get("Authorization"))
		}
		caseName := request.Header.Get("X-T100-Case")
		upstream.calls.Add(1)
		_, _ = io.Copy(io.Discard, request.Body)
		switch caseName {
		case "a-json":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"usage":{"prompt_tokens":10,"completion_tokens":15,"total_tokens":25},"ok":true}`)
		case "a-hold":
			response.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(response, "data: hold\n\n")
			if flusher, ok := response.(http.Flusher); ok {
				flusher.Flush()
			}
			upstream.started[caseName] <- struct{}{}
			<-upstream.releaseCh[caseName]
		case "a-after-reset":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"usage":{"prompt_tokens":0,"completion_tokens":1,"total_tokens":1},"ok":true}`)
		case "b-json":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3},"body":"json-body"}`)
		case "b-sse":
			response.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(response, "data: sse\n\ndata: {\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":3,\"total_tokens\":4}}\n\n")
		case "b-convert":
			response.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(response, "data: {\"choices\":[{\"delta\":{\"content\":\"converted\"}}]}\n\ndata: {\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3,\"total_tokens\":5}}\n\ndata: [DONE]\n\n")
		case "b-missing":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"id":"missing-usage"}`)
		case "b-invalid":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"id":"invalid-usage","usage":{"prompt_tokens":-1}}`)
		case "b-error":
			response.Header().Set("Content-Type", "application/json")
			response.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(response, `{"error":"upstream-error"}`)
		case "b-cancel":
			response.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(response, "data: cancel\n\n")
			if flusher, ok := response.(http.Flusher); ok {
				flusher.Flush()
			}
			<-request.Context().Done()
			upstream.cancelOnce.Do(func() { close(upstream.cancelled) })
		default:
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"ok":true}`)
		}
	}))
	t.Cleanup(upstream.server.Close)
	return upstream
}

func (upstream *t100Upstream) waitFor(caseName string) {
	<-upstream.started[caseName]
}

func (upstream *t100Upstream) release(caseName string) { close(upstream.releaseCh[caseName]) }

func (upstream *t100Upstream) waitForCancel(t *testing.T) {
	t.Helper()
	select {
	case <-upstream.cancelled:
	case <-time.After(time.Second):
		t.Fatal("upstream did not observe client cancellation")
	}
}

func t100WaitFor(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition did not become true")
}
