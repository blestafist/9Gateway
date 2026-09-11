package httpserver

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/accounting"
	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/config"
	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/storage"
	"github.com/pestit/9gateway/internal/transport"
	"gopkg.in/yaml.v3"
)

// TestT120BudgetMilestoneLifecycle is the budget gate's single real-HTTP
// scenario. It deliberately owns two fresh process graphs around one SQLite
// file: only committed token and budget buckets cross that boundary.
func TestT120BudgetMilestoneLifecycle(t *testing.T) {
	const promptSentinel = "t120-unique-prompt-sentinel"
	const responseSentinel = "t120-unique-response-sentinel"
	clock := &requestLimitTestClock{now: time.Date(2024, 12, 31, 23, 59, 30, 0, time.UTC)}
	pepper := []byte("t120-pepper-secret")
	adminCredential := "t120-admin-secret"
	upstreamCredential := "t120-upstream-secret"
	keyA := t100GenerateKey(t, pepper)
	keyB := t100GenerateKey(t, pepper)
	policyA := `{"allowed_models":["exact"],"request_windows":[{"amount":100,"duration":"1m"}],"token_windows":[{"amount":3,"duration":"1m"}],"token_mode":"usage_only","max_concurrent_requests":1,"budget_limits":[{"amount_micros":10,"period":"total"},{"amount_micros":10,"period":"day"},{"amount_micros":10,"period":"month"}]}`
	policyB := `{"allowed_models":["glob-*","zero","unknown"],"request_windows":[{"amount":100,"duration":"1m"}],"token_windows":[{"amount":100,"duration":"1m"}],"token_mode":"usage_only","max_concurrent_requests":1,"budget_limits":[{"amount_micros":50,"period":"total"},{"amount_micros":50,"period":"day"},{"amount_micros":50,"period":"month"}]}`
	databasePath := filepath.Join(t.TempDir(), "t120.db")
	createdAt := time.Unix(1, 0).UTC()
	database, err := storage.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	repository := storage.NewAPIKeyRepository(database)
	for _, value := range []struct {
		id, name, policy string
		key              auth.GeneratedGatewayKey
	}{{"key-a", "persistent-a", policyA, keyA}, {"key-b", "persistent-b", policyB, keyB}} {
		if err := repository.Insert(context.Background(), storage.APIKeyRecord{ID: value.id, Name: value.name, DisplayPrefix: value.key.DisplayPrefix, Digest: value.key.Digest, Enabled: true, CreatedAt: createdAt, UpdatedAt: createdAt, PolicyJSON: value.policy}); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	pricing := t120Pricing(t)
	upstream := newT120Upstream(t, upstreamCredential)
	var logs bytes.Buffer
	process := t120StartProcess(t, databasePath, clock, pricing, upstream.server.URL, upstreamCredential, adminCredential, string(pepper), &logs)
	client := transport.NewClient()
	request := func(method, target, key string, body string, headers map[string]string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, process.gateway.URL+target, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+key)
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
		text := string(body)
		for name, values := range response.Header {
			text += name + strings.Join(values, "|")
		}
		for _, secret := range []string{string(pepper), adminCredential, upstreamCredential, keyA.RawKey, keyB.RawKey, hex.EncodeToString(keyA.Digest), hex.EncodeToString(keyB.Digest), "sqlite", "database is locked", "input_per_million_micros", "reservation"} {
			if strings.Contains(strings.ToLower(text), strings.ToLower(secret)) {
				t.Fatalf("gateway response leaked %q: %q", secret, text)
			}
		}
	}
	assertStatus := func(response *http.Response, want int, contains string) {
		t.Helper()
		body := read(response)
		assertSafe(response, body)
		if response.StatusCode != want || (contains != "" && !bytes.Contains(body, []byte(contains))) {
			t.Fatalf("response = %d/%q, want %d containing %q", response.StatusCode, body, want, contains)
		}
	}

	// Exact pricing and an explicit output bound make the conservative two-micro
	// reservation larger than the actual one-micro completion.
	first := request(http.MethodPost, "/v1/chat/completions", keyA.RawKey, `{"model":"exact","max_tokens":2,"messages":[{"role":"user","content":"t120-unique-prompt-sentinel"}]}`, map[string]string{"Content-Type": "application/json", "X-T120-Case": "a-json"})
	firstBody := read(first)
	assertSafe(first, firstBody)
	exactJSONBody := []byte(`{"usage":{"prompt_tokens":1,"completion_tokens":0,"total_tokens":1},"body":"json-body","sentinel":"t120-unique-response-sentinel"}`)
	if first.StatusCode != http.StatusOK || first.Header.Get("Content-Type") != "application/json" || first.Header.Get("X-T120-Upstream-Safe") != "exact-json" || !bytes.Equal(firstBody, exactJSONBody) {
		t.Fatalf("exact JSON lifecycle = %d/content-type %q/safe %q/body %q, want exact transparent response", first.StatusCode, first.Header.Get("Content-Type"), first.Header.Get("X-T120-Upstream-Safe"), firstBody)
	}
	if got := upstream.body("a-json"); !bytes.Equal(got, []byte(`{"model":"exact","max_tokens":2,"messages":[{"role":"user","content":"t120-unique-prompt-sentinel"}]}`)) {
		t.Fatalf("transparent request body = %q, want exact wire bytes", got)
	}
	t120WaitFor(t, func() bool { return process.worker.Stats().Succeeded >= 1 })

	// The committed token total survives the process-local reservation and
	// rejects a candidate that would oversubscribe the fixed token window.
	before := upstream.calls.Load()
	assertStatus(request(http.MethodPost, "/v1/chat/completions", keyA.RawKey, `{"model":"exact","max_tokens":2,"messages":[{"role":"user","content":"t120-unique-prompt-sentinel"}],"sentinel":"t120-unique-response-sentinel"}`, map[string]string{"Content-Type": "application/json", "X-T120-Case": "a-token-rejected"}), http.StatusTooManyRequests, `"code":"token_limit_exceeded"`)
	if upstream.calls.Load() != before {
		t.Fatal("token rejection reached upstream")
	}

	// B covers the actual transport matrix while using an ordered glob price.
	holdReq := request(http.MethodPost, "/v1/chat/completions", keyB.RawKey, `{"model":"glob-hold","stream":true}`, map[string]string{"Content-Type": "application/json", "X-T120-Case": "a-hold"})
	upstream.waitFor("a-hold")
	before = upstream.calls.Load()
	assertStatus(request(http.MethodPost, "/v1/chat/completions", keyB.RawKey, `{"model":"glob-hold","stream":true}`, map[string]string{"Content-Type": "application/json", "X-T120-Case": "a-hold-rejected"}), http.StatusTooManyRequests, `"code":"concurrency_limit_exceeded"`)
	if upstream.calls.Load() != before {
		t.Fatal("concurrency rejection reached upstream")
	}
	upstream.release("a-hold")
	read(holdReq)

	for _, test := range []struct {
		name, body, content string
		status              int
		want                string
	}{
		{"b-json", `{"model":"glob-json"}`, "json", http.StatusOK, "json-body"},
		{"b-sse", `{"model":"glob-sse","stream":true}`, "sse", http.StatusOK, "data: sse"},
		{"b-convert", `{"model":"glob-convert","stream":false}`, "convert", http.StatusOK, `"total_tokens":2`},
		{"b-zero", `{"model":"zero"}`, "zero", http.StatusOK, "zero-body"},
		{"b-missing", `{"model":"glob-missing"}`, "missing", http.StatusOK, "missing-usage"},
		{"b-partial", `{"model":"glob-partial"}`, "partial", http.StatusOK, "partial-usage"},
		{"b-invalid", `{"model":"glob-invalid"}`, "invalid", http.StatusOK, "invalid-usage"},
		{"b-lower", `{"model":"glob-lower"}`, "lower", http.StatusOK, "lower-usage"},
		{"b-above", `{"model":"glob-above"}`, "above", http.StatusOK, "above-usage"},
		{"b-error", `{"model":"glob-error","messages":[{"role":"user","content":"t120-unique-prompt-sentinel"}]}`, "error", http.StatusServiceUnavailable, "t120-unique-response-sentinel"},
	} {
		response := request(http.MethodPost, "/v1/chat/completions", keyB.RawKey, test.body, map[string]string{"Content-Type": "application/json", "X-T120-Case": test.content})
		body := read(response)
		assertSafe(response, body)
		if test.content == "error" {
			wantBody := []byte(`{"error":"t120-unique-response-sentinel"}`)
			if response.StatusCode != http.StatusServiceUnavailable || response.Header.Get("Content-Type") != "application/json" || response.Header.Get("X-T120-Upstream-Safe") != "upstream-error" || !bytes.Equal(body, wantBody) {
				t.Fatalf("%s transparent error = %d/content-type %q/safe %q/body %q, want exact status/headers/body", test.name, response.StatusCode, response.Header.Get("Content-Type"), response.Header.Get("X-T120-Upstream-Safe"), body)
			}
		}
		if response.StatusCode != test.status || !bytes.Contains(body, []byte(test.want)) {
			t.Fatalf("%s = %d/%q, want %d containing %q", test.name, response.StatusCode, body, test.status, test.want)
		}
	}
	// Unknown pricing, malformed input, and an oversized inspected body fail
	// closed before the upstream. The model policy rejection is also explicit.
	for _, test := range []struct {
		name, body, path, key string
	}{
		{"unknown price", `{"model":"not-priced"}`, "/v1/chat/completions", keyB.RawKey},
		{"malformed", `{"model":"t120-unique-prompt-sentinel","sentinel":"t120-unique-response-sentinel",`, "/v1/chat/completions", keyA.RawKey},
		{"oversized", "t120-unique-prompt-sentinel:t120-unique-response-sentinel" + strings.Repeat("x", 5000), "/v1/chat/completions", keyA.RawKey},
		{"model denied", `{"model":"glob-denied","messages":[{"role":"user","content":"t120-unique-prompt-sentinel"}],"sentinel":"t120-unique-response-sentinel"}`, "/v1/chat/completions", keyA.RawKey},
	} {
		before = upstream.calls.Load()
		response := request(http.MethodPost, test.path, test.key, test.body, map[string]string{"Content-Type": "application/json"})
		body := read(response)
		assertSafe(response, body)
		for name, values := range response.Header {
			if bytes.Contains([]byte(name+strings.Join(values, "|")), []byte(promptSentinel)) || bytes.Contains([]byte(name+strings.Join(values, "|")), []byte(responseSentinel)) {
				t.Fatalf("%s gateway-generated response header leaked a sentinel: %s=%q", test.name, name, values)
			}
		}
		if bytes.Contains(body, []byte(promptSentinel)) || bytes.Contains(body, []byte(responseSentinel)) {
			t.Fatalf("%s gateway-generated response body leaked a sentinel: %q", test.name, body)
		}
		if response.StatusCode != http.StatusBadRequest && response.StatusCode != http.StatusForbidden {
			t.Fatalf("%s = %d/%q", test.name, response.StatusCode, body)
		}
		if upstream.calls.Load() != before {
			t.Fatalf("%s reached upstream", test.name)
		}
	}
	// A budget reservation held by B is rejected independently of A's state.
	// The large B policy lets every observation shape above finish, including
	// invalid/missing usage which remains conservatively charged.
	t120WaitFor(t, func() bool { return t120BudgetTotal(t, databasePath, "key-b") == 22 })
	if got := t120BudgetTotal(t, databasePath, "key-b"); got != 22 {
		t.Fatalf("B actual/conservative budget = %d, want 22", got)
	}
	if got := t120BudgetPeriod(t, databasePath, "key-b", limiter.BudgetPeriodDay, clock.Now()); got != 22 {
		t.Fatalf("B daily spend = %d, want 22", got)
	}
	if got := t120BudgetPeriod(t, databasePath, "key-b", limiter.BudgetPeriodMonth, clock.Now()); got != 22 {
		t.Fatalf("B monthly spend = %d, want 22", got)
	}

	t120StopProcess(t, process)
	if got := t120BudgetTotal(t, databasePath, "key-a"); got != 1 {
		t.Fatalf("persisted A spend before restart = %d, want 1", got)
	}
	if got := t120BudgetTotal(t, databasePath, "key-b"); got != 22 {
		t.Fatalf("persisted B spend before restart = %d, want 22", got)
	}

	// Restart restores committed totals but no active leases. B is then put into
	// deliberate over-budget debt by an atomic administrative policy replacement.
	process = t120StartProcess(t, databasePath, clock, pricing, upstream.server.URL, upstreamCredential, adminCredential, string(pepper), &logs)
	before = upstream.calls.Load()
	assertStatus(request(http.MethodPost, "/v1/chat/completions", keyB.RawKey, `{"model":"glob-restarted"}`, map[string]string{"Content-Type": "application/json"}), http.StatusOK, "json-body")
	if upstream.calls.Load() != before+1 {
		t.Fatal("restored B did not admit after restart")
	}
	update := newPolicyRequest(t, process.gateway.URL, "key-b", `{"enabled":true,"policy":{"allowed_models":["glob-*","zero","unknown"],"request_windows":[{"amount":100,"duration":"1m"}],"token_windows":[{"amount":100,"duration":"1m"}],"token_mode":"usage_only","max_concurrent_requests":1,"budget_limits":[{"amount_micros":3,"period":"total"},{"amount_micros":3,"period":"day"},{"amount_micros":3,"period":"month"}]}}`, adminCredential)
	update.Method = http.MethodPut
	updated, err := client.Do(update)
	if err != nil {
		t.Fatal(err)
	}
	updatedBody := read(updated)
	assertSafe(updated, updatedBody)
	if updated.StatusCode != http.StatusOK {
		t.Fatalf("budget policy replacement = %d/%q", updated.StatusCode, updatedBody)
	}
	// The replacement retains the 19-micro spend, so its debt rejects without a
	// provider call and has no fabricated Retry-After for a total limit.
	before = upstream.calls.Load()
	assertStatus(request(http.MethodPost, "/v1/chat/completions", keyB.RawKey, `{"model":"glob-debt"}`, map[string]string{"Content-Type": "application/json"}), http.StatusTooManyRequests, `"code":"budget_exceeded"`)
	if upstream.calls.Load() != before {
		t.Fatal("over-budget debt reached upstream")
	}

	// Same-process restart restoration of token usage is checked before changing
	// the fake clock; then UTC day and calendar-month reset are checked together.
	before = upstream.calls.Load()
	assertStatus(request(http.MethodPost, "/v1/chat/completions", keyA.RawKey, `{"model":"exact","max_tokens":2}`, map[string]string{"Content-Type": "application/json"}), http.StatusTooManyRequests, `"code":"token_limit_exceeded"`)
	if upstream.calls.Load() != before {
		t.Fatal("restored token rejection reached upstream")
	}
	clock.Set(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	assertStatus(request(http.MethodPost, "/v1/chat/completions", keyA.RawKey, `{"model":"exact","max_tokens":2}`, map[string]string{"Content-Type": "application/json", "X-T120-Case": "a-after-reset"}), http.StatusOK, `"total_tokens":1`)

	// Replacing the model selector persists and publishes atomically. The old
	// model is forbidden before upstream; the new exact selector is admitted
	// against the retained total spend and reset day/month buckets.
	update = newPolicyRequest(t, process.gateway.URL, "key-a", `{"enabled":true,"policy":{"allowed_models":["exact-new"],"request_windows":[{"amount":100,"duration":"1m"}],"token_windows":[{"amount":3,"duration":"1m"}],"token_mode":"usage_only","max_concurrent_requests":1,"budget_limits":[{"amount_micros":10,"period":"total"},{"amount_micros":10,"period":"day"},{"amount_micros":10,"period":"month"}]}}`, adminCredential)
	update.Method = http.MethodPut
	updated, err = client.Do(update)
	if err != nil {
		t.Fatal(err)
	}
	updatedBody = read(updated)
	assertSafe(updated, updatedBody)
	if updated.StatusCode != http.StatusOK {
		t.Fatalf("model policy replacement = %d/%q", updated.StatusCode, updatedBody)
	}
	before = upstream.calls.Load()
	assertStatus(request(http.MethodPost, "/v1/chat/completions", keyA.RawKey, `{"model":"exact"}`, map[string]string{"Content-Type": "application/json"}), http.StatusForbidden, `"code":"model_not_allowed"`)
	if upstream.calls.Load() != before {
		t.Fatal("old model after replacement reached upstream")
	}
	assertStatus(request(http.MethodPost, "/v1/chat/completions", keyA.RawKey, `{"model":"exact-new"}`, map[string]string{"Content-Type": "application/json", "X-T120-Case": "a-after-reset"}), http.StatusOK, `"total_tokens":1`)

	t120StopProcess(t, process)
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if line == "" {
			continue
		}
		if !json.Valid([]byte(line)) {
			t.Fatalf("completion log is not structured JSON: %q", line)
		}
		for _, secret := range []string{string(pepper), adminCredential, upstreamCredential, keyA.RawKey, keyB.RawKey, hex.EncodeToString(keyA.Digest), hex.EncodeToString(keyB.Digest), "prompt-fragment", "response-fragment", "input_per_million_micros", "reservation", "sqlite", "database is locked"} {
			if strings.Contains(strings.ToLower(line), strings.ToLower(secret)) {
				t.Fatalf("completion log leaked %q: %q", secret, line)
			}
		}
	}
	for _, sentinel := range []string{promptSentinel, responseSentinel} {
		if strings.Contains(logs.String(), sentinel) {
			t.Fatalf("completion logs leaked sentinel %q", sentinel)
		}
	}
}

func TestT120PersistentRequestWindowRejectsWithoutUpstreamCall(t *testing.T) {
	clock := &requestLimitTestClock{now: time.Date(2025, 1, 15, 12, 0, 30, 0, time.UTC)}
	key, err := auth.GenerateGatewayKey([]byte("t120-request-window-pepper"))
	if err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(t.TempDir(), "request-window.db")
	database, err := storage.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	policy := `{"allowed_models":["exact"],"request_windows":[{"amount":1,"duration":"1m"}],"token_windows":[{"amount":100,"duration":"1m"}],"token_mode":"usage_only","max_concurrent_requests":1,"budget_limits":[{"amount_micros":100,"period":"total"},{"amount_micros":100,"period":"day"},{"amount_micros":100,"period":"month"}]}`
	created := time.Unix(1, 0).UTC()
	if err := storage.NewAPIKeyRepository(database).Insert(context.Background(), storage.APIKeyRecord{ID: "request-window", Name: "request-window", DisplayPrefix: key.DisplayPrefix, Digest: key.Digest, Enabled: true, CreatedAt: created, UpdatedAt: created, PolicyJSON: policy}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	upstream := newT120Upstream(t, "request-window-upstream")
	process := t120StartProcess(t, databasePath, clock, t120Pricing(t), upstream.server.URL, upstream.credential, "request-window-admin", "t120-request-window-pepper", io.Discard)
	client := transport.NewClient()
	request := func() *http.Response {
		t.Helper()
		req, requestErr := http.NewRequest(http.MethodPost, process.gateway.URL+"/v1/chat/completions", strings.NewReader(`{"model":"exact"}`))
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		req.Header.Set("Authorization", "Bearer "+key.RawKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-T120-Case", "a-json")
		response, requestErr := client.Do(req)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		return response
	}
	first := request()
	if body := readT120Body(t, first); first.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("json-body")) {
		t.Fatalf("first request = %d/%q", first.StatusCode, body)
	}
	before := upstream.calls.Load()
	second := request()
	body := readT120Body(t, second)
	if second.StatusCode != http.StatusTooManyRequests || second.Header.Get("Retry-After") != "30" || !bytes.Contains(body, []byte(`"code":"request_limit_exceeded"`)) {
		t.Fatalf("persistent request rejection = %d/retry %q/body %q", second.StatusCode, second.Header.Get("Retry-After"), body)
	}
	if got := upstream.calls.Load(); got != before {
		t.Fatalf("request rejection added upstream calls: before %d, after %d", before, got)
	}
	t120StopProcess(t, process)
}

func TestT120DailyAndMonthlyBudgetRejectionsAreIndependentHTTPGates(t *testing.T) {
	clock := &requestLimitTestClock{now: time.Date(2025, 1, 15, 12, 0, 30, 0, time.UTC)}
	pepper := []byte("t120-period-pepper")
	keyDay, err := auth.GenerateGatewayKey(pepper)
	if err != nil {
		t.Fatal(err)
	}
	keyMonth, err := auth.GenerateGatewayKey(pepper)
	if err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(t.TempDir(), "period-budget.db")
	database, err := storage.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	dayPolicy := `{"allowed_models":["exact"],"token_windows":[{"amount":100,"duration":"1m"}],"token_mode":"usage_only","max_concurrent_requests":1,"budget_limits":[{"amount_micros":100,"period":"total"},{"amount_micros":1,"period":"day"},{"amount_micros":100,"period":"month"}]}`
	monthPolicy := `{"allowed_models":["exact"],"token_windows":[{"amount":100,"duration":"1m"}],"token_mode":"usage_only","max_concurrent_requests":1,"budget_limits":[{"amount_micros":100,"period":"total"},{"amount_micros":100,"period":"day"},{"amount_micros":1,"period":"month"}]}`
	created := time.Unix(1, 0).UTC()
	repository := storage.NewAPIKeyRepository(database)
	for _, value := range []struct {
		id, name, policy string
		key              auth.GeneratedGatewayKey
	}{{"day-only", "day-only", dayPolicy, keyDay}, {"month-only", "month-only", monthPolicy, keyMonth}} {
		if err := repository.Insert(context.Background(), storage.APIKeyRecord{ID: value.id, Name: value.name, DisplayPrefix: value.key.DisplayPrefix, Digest: value.key.Digest, Enabled: true, CreatedAt: created, UpdatedAt: created, PolicyJSON: value.policy}); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	upstream := newT120Upstream(t, "period-upstream")
	process := t120StartProcess(t, databasePath, clock, t120Pricing(t), upstream.server.URL, upstream.credential, "period-admin", string(pepper), io.Discard)
	client := transport.NewClient()
	request := func(key auth.GeneratedGatewayKey) *http.Response {
		t.Helper()
		req, requestErr := http.NewRequest(http.MethodPost, process.gateway.URL+"/v1/chat/completions", strings.NewReader(`{"model":"exact"}`))
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		req.Header.Set("Authorization", "Bearer "+key.RawKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-T120-Case", "a-json")
		response, requestErr := client.Do(req)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		return response
	}
	if response := request(keyDay); response.StatusCode != http.StatusOK {
		t.Fatalf("daily setup = %d/%q", response.StatusCode, readT120Body(t, response))
	} else {
		readT120Body(t, response)
	}
	if response := request(keyMonth); response.StatusCode != http.StatusOK {
		t.Fatalf("monthly setup = %d/%q", response.StatusCode, readT120Body(t, response))
	} else {
		readT120Body(t, response)
	}
	dayBefore := upstream.calls.Load()
	dayRejected := request(keyDay)
	dayBody := readT120Body(t, dayRejected)
	dayRetry := int(clock.Now().AddDate(0, 0, 1).Truncate(24*time.Hour).Sub(clock.Now()) / time.Second)
	if dayRejected.StatusCode != http.StatusTooManyRequests || dayRejected.Header.Get("Retry-After") != strconv.Itoa(dayRetry) || !bytes.Contains(dayBody, []byte(`"code":"budget_exceeded"`)) {
		t.Fatalf("daily rejection = %d/retry %q/body %q, want %d", dayRejected.StatusCode, dayRejected.Header.Get("Retry-After"), dayBody, dayRetry)
	}
	if got := upstream.calls.Load(); got != dayBefore {
		t.Fatalf("daily rejection reached upstream: before %d, after %d", dayBefore, got)
	}
	monthBefore := upstream.calls.Load()
	monthRejected := request(keyMonth)
	monthBody := readT120Body(t, monthRejected)
	monthRetry := int(time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC).Sub(clock.Now()) / time.Second)
	if monthRejected.StatusCode != http.StatusTooManyRequests || monthRejected.Header.Get("Retry-After") != strconv.Itoa(monthRetry) || !bytes.Contains(monthBody, []byte(`"code":"budget_exceeded"`)) {
		t.Fatalf("monthly rejection = %d/retry %q/body %q, want %d", monthRejected.StatusCode, monthRejected.Header.Get("Retry-After"), monthBody, monthRetry)
	}
	if got := upstream.calls.Load(); got != monthBefore {
		t.Fatalf("monthly rejection reached upstream: before %d, after %d", monthBefore, got)
	}
	t120StopProcess(t, process)
}

func TestT120PersistentCancellationSettlesAndRestoresWithoutLease(t *testing.T) {
	clock := &requestLimitTestClock{now: time.Date(2025, 1, 15, 12, 0, 30, 0, time.UTC)}
	pepper := []byte("t120-cancel-pepper")
	key, err := auth.GenerateGatewayKey(pepper)
	if err != nil {
		t.Fatal(err)
	}
	policy := `{"allowed_models":["exact"],"token_windows":[{"amount":5,"duration":"1m"}],"token_mode":"usage_only","max_concurrent_requests":1,"budget_limits":[{"amount_micros":3,"period":"total"},{"amount_micros":3,"period":"day"},{"amount_micros":3,"period":"month"}]}`
	databasePath := filepath.Join(t.TempDir(), "cancel.db")
	database, err := storage.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	created := time.Unix(1, 0).UTC()
	if err := storage.NewAPIKeyRepository(database).Insert(context.Background(), storage.APIKeyRecord{ID: "cancel-key", Name: "cancel-key", DisplayPrefix: key.DisplayPrefix, Digest: key.Digest, Enabled: true, CreatedAt: created, UpdatedAt: created, PolicyJSON: policy}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	cancelSeen := make(chan struct{})
	var cancelOnce sync.Once
	var calls atomic.Int32
	settlementSeen := make(chan struct{})
	settlementError := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.Header.Get("Authorization") != "Bearer cancel-upstream" {
			t.Errorf("upstream authorization = %q", request.Header.Get("Authorization"))
		}
		switch request.Header.Get("X-T120-Case") {
		case "cancel":
			response.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(response, "data: cancellation-fragment\n\n")
			response.(http.Flusher).Flush()
			<-request.Context().Done()
			cancelOnce.Do(func() { close(cancelSeen) })
		case "a-json":
			response.Header().Set("Content-Type", "application/json")
			response.Header().Set("X-T120-Upstream-Safe", "exact-json")
			_, _ = io.WriteString(response, `{"usage":{"prompt_tokens":1,"completion_tokens":0,"total_tokens":1},"body":"reused"}`)
		}
	}))
	t.Cleanup(upstream.Close)
	process := t120StartProcessWithBudgetSink(t, databasePath, clock, t120Pricing(t), upstream.URL, "cancel-upstream", "cancel-admin", string(pepper), io.Discard, func(delta limiter.CommittedBudgetDelta) {
		if delta.Delta == 0 {
			return
		}
		select {
		case <-cancelSeen:
			select {
			case <-settlementSeen:
			default:
				close(settlementSeen)
			}
		default:
			select {
			case settlementError <- "budget settlement arrived before upstream cancellation was observed":
			default:
			}
		}
	})
	client := transport.NewClient()
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, process.gateway.URL+"/v1/chat/completions", strings.NewReader(`{"model":"exact","messages":[{"role":"user","content":"cancel prompt sentinel"}],"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+key.RawKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-T120-Case", "cancel")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	fragment := make([]byte, len("data: cancellation-fragment\n\n"))
	if _, err := io.ReadFull(response.Body, fragment); err != nil || string(fragment) != "data: cancellation-fragment\n\n" {
		t.Fatalf("cancellation first fragment = %q/%v", fragment, err)
	}
	cancel()
	response.Body.Close()
	select {
	case <-settlementSeen:
	case errText := <-settlementError:
		t.Fatal(errText)
	case <-time.After(time.Second):
		t.Fatal("upstream cancellation and subsequent budget settlement were not observed")
	}
	t120WaitFor(t, func() bool { return t120BudgetTotal(t, databasePath, "cancel-key") == 1 })
	t120WaitFor(t, func() bool { return t120TokenCommitted(t, databasePath, clock.Now(), "cancel-key") == 2 })
	if got := t120BudgetTotal(t, databasePath, "cancel-key"); got != 1 {
		t.Fatalf("cancellation persisted budget = %d, want exact conservative 1", got)
	}
	if got := t120TokenCommitted(t, databasePath, clock.Now(), "cancel-key"); got != 2 {
		t.Fatalf("cancellation persisted tokens = %d, want exact conservative 2", got)
	}

	// Upstream cancellation must release the in-memory lease before the
	// conservative accounting handoff completes.
	reused, err := http.NewRequest(http.MethodPost, process.gateway.URL+"/v1/chat/completions", strings.NewReader(`{"model":"exact"}`))
	if err != nil {
		t.Fatal(err)
	}
	reused.Header.Set("Authorization", "Bearer "+key.RawKey)
	reused.Header.Set("Content-Type", "application/json")
	reused.Header.Set("X-T120-Case", "a-json")
	reusedResponse, err := client.Do(reused)
	if err != nil {
		t.Fatal(err)
	}
	if body := readT120Body(t, reusedResponse); reusedResponse.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("reused")) {
		t.Fatalf("post-cancellation reuse = %d/%q", reusedResponse.StatusCode, body)
	}
	t120WaitFor(t, func() bool { return t120BudgetTotal(t, databasePath, "cancel-key") == 2 })
	t120WaitFor(t, func() bool { return t120TokenCommitted(t, databasePath, clock.Now(), "cancel-key") == 3 })
	t120StopProcess(t, process)
	if got := t120BudgetTotal(t, databasePath, "cancel-key"); got != 2 {
		t.Fatalf("cancellation plus immediate reuse persisted budget = %d, want exact 2", got)
	}
	if got := t120TokenCommitted(t, databasePath, clock.Now(), "cancel-key"); got != 3 {
		t.Fatalf("cancellation plus immediate reuse persisted tokens = %d, want exact 3", got)
	}

	// Restart restores committed spend, but no active lease. A fresh request
	// remains below the generous budget and proves the old lease was not loaded.
	process = t120StartProcess(t, databasePath, clock, t120Pricing(t), upstream.URL, "cancel-upstream", "cancel-admin", string(pepper), io.Discard)
	beforeRestart := calls.Load()
	restarted, err := http.NewRequest(http.MethodPost, process.gateway.URL+"/v1/chat/completions", strings.NewReader(`{"model":"exact"}`))
	if err != nil {
		t.Fatal(err)
	}
	restarted.Header.Set("Authorization", "Bearer "+key.RawKey)
	restarted.Header.Set("Content-Type", "application/json")
	restarted.Header.Set("X-T120-Case", "a-json")
	restartedResponse, err := client.Do(restarted)
	if err != nil {
		t.Fatal(err)
	}
	if body := readT120Body(t, restartedResponse); restartedResponse.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("reused")) {
		t.Fatalf("post-restart reuse = %d/%q", restartedResponse.StatusCode, body)
	}
	if got := calls.Load(); got != beforeRestart+1 {
		t.Fatalf("post-restart request calls = %d, want exactly one upstream call (before %d)", got, beforeRestart)
	}
	t120WaitFor(t, func() bool { return t120BudgetTotal(t, databasePath, "cancel-key") == 3 })
	t120WaitFor(t, func() bool { return t120TokenCommitted(t, databasePath, clock.Now(), "cancel-key") == 4 })
	if got := t120BudgetTotal(t, databasePath, "cancel-key"); got != 3 {
		t.Fatalf("restart settlement persisted budget = %d, want exact 3", got)
	}
	if got := t120TokenCommitted(t, databasePath, clock.Now(), "cancel-key"); got != 4 {
		t.Fatalf("restart settlement persisted tokens = %d, want exact 4", got)
	}
	t120StopProcess(t, process)
}

// TestT120BlockedObservationAndSQLiteWritersAreOffTheTransportPath proves the
// hard timing boundary with real HTTP. Both persistence writers block while an
// observation parser is blocked; transport still flushes, closes, and releases
// concurrency so a second request can reuse the slot.
func TestT120BlockedObservationAndSQLiteWritersAreOffTheTransportPath(t *testing.T) {
	clock := &requestLimitTestClock{now: time.Unix(30, 0).UTC()}
	key, authenticator := t110Key(t, `{"allowed_models":["blocked"],"token_windows":[{"amount":100,"duration":"1m"}],"token_mode":"usage_only","max_concurrent_requests":1,"budget_limits":[{"amount_micros":100,"period":"total"}]}`)
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := storage.NewAPIKeyRepository(database).Insert(context.Background(), storage.APIKeyRecord{ID: "t110-key-a", Name: "blocked", DisplayPrefix: key.DisplayPrefix, Digest: key.Digest, Enabled: true, CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(1, 0).UTC(), PolicyJSON: `{"allowed_models":["blocked"],"token_windows":[{"amount":100,"duration":"1m"}],"token_mode":"usage_only","max_concurrent_requests":1,"budget_limits":[{"amount_micros":100,"period":"total"}]}`}); err != nil {
		t.Fatal(err)
	}
	tokenDB := &t120BlockingDB{DB: database, entered: make(chan struct{}), release: make(chan struct{})}
	budgetDB := &t120BlockingDB{DB: database, entered: make(chan struct{}), release: make(chan struct{})}
	tokenStore := storage.NewUsageBucketRepository(tokenDB)
	budgetStore := storage.NewBudgetBucketRepository(budgetDB)
	tokenAccumulator := storage.NewUsageAggregateAccumulator(tokenStore)
	budgetAccumulator := storage.NewBudgetAccumulator(budgetStore)
	t.Cleanup(func() {
		closeOnce(t, tokenDB.release)
		closeOnce(t, budgetDB.release)
		shutdownErr := tokenAccumulator.Shutdown(context.Background())
		if shutdownErr != nil {
			t.Errorf("token accumulator shutdown: %v", shutdownErr)
		}
		if shutdownErr = budgetAccumulator.Shutdown(context.Background()); shutdownErr != nil {
			t.Errorf("budget accumulator shutdown: %v", shutdownErr)
		}
	})
	tokens := limiter.NewTokenLimiter(clock.Now)
	budget := limiter.NewBudgetLimiter(clock.Now)
	tokens.SetCommittedDeltaSink(tokenAccumulator.Sink)
	budget.SetCommittedDeltaSink(budgetAccumulator.Sink)
	parserEntered := make(chan struct{})
	parserRelease := make(chan struct{})
	var parserOnce sync.Once
	worker := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: 2, ParseUsage: func([]byte, ContentCoding) (accounting.Usage, error) {
		parserOnce.Do(func() { close(parserEntered) })
		<-parserRelease
		return accounting.Usage{}, nil
	}})
	t.Cleanup(func() { closeOnce(t, parserRelease); shutdownObservationWorker(t, worker) })
	var calls atomic.Int32
	firstFragment := make(chan struct{})
	firstReceived := make(chan struct{})
	secondFragment := make(chan struct{})
	var firstOnce sync.Once
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		response.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(response, "data: first\n\n")
		response.(http.Flusher).Flush()
		if request.Header.Get("X-T120-Case") == "first" {
			firstOnce.Do(func() { close(firstFragment) })
			select {
			case <-request.Context().Done():
				return
			case <-firstReceived:
			}
		}
		_, _ = io.WriteString(response, "data: {\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n")
		response.(http.Flusher).Flush()
		if request.Header.Get("X-T120-Case") == "first" {
			close(secondFragment)
		}
	}))
	t.Cleanup(upstream.Close)
	concurrency := limiter.NewConcurrencyLimiter()
	handler := NewHandlerWithAuthenticatorAndLimitersAndTokenLimiterAndUsageObservationWorker(transport.NewClient(), upstream.URL, "upstream", authenticator, limiter.NewRequestLimiter(clock.Now), concurrency, nil, tokens, TokenAdmissionConfig{FallbackUnknownInputTokens: 1, FallbackMaxOutputTokens: 1, PricingResolver: accounting.NewPricingResolver(t120Pricing(t)), BudgetLimiter: budget}, worker)
	gateway := httptest.NewServer(handler)
	t.Cleanup(gateway.Close)
	request := func(caseName string) *http.Response {
		req, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{"model":"blocked","stream":true}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+key.RawKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-T120-Case", caseName)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	first := request("first")
	select {
	case <-firstFragment:
	case <-time.After(time.Second):
		t.Fatal("first SSE fragment did not flush")
	}
	buffer := make([]byte, len("data: first\n\n"))
	if _, err := io.ReadFull(first.Body, buffer); err != nil || string(buffer) != "data: first\n\n" {
		t.Fatalf("first flush = %q/%v", buffer, err)
	}
	select {
	case <-secondFragment:
		t.Fatal("upstream wrote the second fragment before the client received the first")
	default:
	}
	close(firstReceived)
	secondBuffer := make([]byte, len("data: {\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n"))
	if _, err := io.ReadFull(first.Body, secondBuffer); err != nil {
		t.Fatalf("second SSE fragment = %q/%v", secondBuffer, err)
	}
	select {
	case <-secondFragment:
	case <-time.After(time.Second):
		t.Fatal("upstream did not observe the controlled second-fragment release")
	}
	bodyDone := make(chan struct{})
	go func() { _, _ = io.ReadAll(first.Body); close(bodyDone) }()
	select {
	case <-bodyDone:
	case <-time.After(time.Second):
		t.Fatal("transparent stream close delayed by blocked observation/persistence")
	}
	first.Body.Close()
	select {
	case <-parserEntered:
	case <-time.After(time.Second):
		t.Fatal("observation parser did not block")
	}
	closeOnce(t, parserRelease)
	select {
	case <-tokenDB.entered:
	case <-time.After(time.Second):
		t.Fatal("token SQLite writer did not block")
	}
	closeOnce(t, tokenDB.release)
	select {
	case <-budgetDB.entered:
	case <-time.After(time.Second):
		t.Fatal("budget SQLite writer did not block")
	}
	// Keep the budget writer blocked while a new request reuses the released
	// concurrency slot and receives its first fragment.
	second := request("second")
	secondFragmentBytes := make([]byte, len("data: first\n\n"))
	if _, err := io.ReadFull(second.Body, secondFragmentBytes); err != nil || second.StatusCode != http.StatusOK || string(secondFragmentBytes) != "data: first\n\n" || calls.Load() != 2 {
		t.Fatalf("concurrency reuse = %d/%q/%v", second.StatusCode, secondFragmentBytes, err)
	}
	closeOnce(t, budgetDB.release)
	secondBody, err := io.ReadAll(second.Body)
	second.Body.Close()
	if err != nil || !bytes.Contains(append(secondFragmentBytes, secondBody...), []byte("data: first")) {
		t.Fatalf("follow-up stream close = %q/%v", secondBody, err)
	}
	closeOnce(t, tokenDB.release)
}

func t120Pricing(t *testing.T) config.PricingConfig {
	t.Helper()
	var pricing config.PricingConfig
	const source = `rules:
  - model: exact
    input_per_million_micros: 500000
    output_per_million_micros: 500000
  - model: exact-new
    input_per_million_micros: 500000
    output_per_million_micros: 500000
  - model: blocked
    input_per_million_micros: 1000000
    output_per_million_micros: 1000000
  - model: 'glob-*'
    input_per_million_micros: 1000000
    output_per_million_micros: 1000000
  - model: zero
    input_per_million_micros: 0
    output_per_million_micros: 0
`
	if err := yaml.Unmarshal([]byte(source), &pricing); err != nil {
		t.Fatal(err)
	}
	return pricing
}

func readT120Body(t *testing.T, response *http.Response) []byte {
	t.Helper()
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return body
}

type t120Process struct {
	database          *storage.DB
	gateway           *httptest.Server
	worker            *UsageObservationWorker
	logger            *CompletionLogger
	tokenAccumulator  *storage.UsageAggregateAccumulator
	budgetAccumulator *storage.BudgetAccumulator
}

type t120Upstream struct {
	server     *httptest.Server
	credential string
	calls      atomic.Int32
	mu         sync.Mutex
	started    map[string]chan struct{}
	releaseCh  map[string]chan struct{}
	bodies     map[string][]byte
}

func newT120Upstream(t *testing.T, credential string) *t120Upstream {
	t.Helper()
	upstream := &t120Upstream{credential: credential, started: map[string]chan struct{}{"a-hold": make(chan struct{}, 1)}, releaseCh: map[string]chan struct{}{"a-hold": make(chan struct{})}, bodies: make(map[string][]byte)}
	upstream.server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+upstream.credential {
			t.Errorf("upstream authorization = %q", request.Header.Get("Authorization"))
		}
		upstream.calls.Add(1)
		wireBody, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read upstream request body: %v", err)
		}
		upstream.mu.Lock()
		upstream.bodies[request.Header.Get("X-T120-Case")] = append([]byte(nil), wireBody...)
		upstream.mu.Unlock()
		switch request.Header.Get("X-T120-Case") {
		case "a-hold":
			response.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(response, "data: hold\n\n")
			response.(http.Flusher).Flush()
			upstream.started["a-hold"] <- struct{}{}
			select {
			case <-upstream.releaseCh["a-hold"]:
			case <-request.Context().Done():
			}
		case "a-json", "a-after-reset":
			response.Header().Set("Content-Type", "application/json")
			response.Header().Set("X-T120-Upstream-Safe", "exact-json")
			_, _ = io.WriteString(response, `{"usage":{"prompt_tokens":1,"completion_tokens":0,"total_tokens":1},"body":"json-body","sentinel":"t120-unique-response-sentinel"}`)
		case "json":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2},"body":"json-body"}`)
		case "sse":
			response.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(response, "data: sse\n\ndata: {\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n")
		case "convert":
			response.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(response, "data: {\"choices\":[{\"delta\":{\"content\":\"converted\"}}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\ndata: [DONE]\n\n")
		case "zero":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"body":"zero-body","usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
		case "missing":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"id":"missing-usage"}`)
		case "partial":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"id":"partial-usage","usage":{"prompt_tokens":1}}`)
		case "invalid":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"id":"invalid-usage","usage":{"total_tokens":-1}}`)
		case "lower":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"id":"lower-usage","usage":{"prompt_tokens":1,"completion_tokens":0,"total_tokens":1}}`)
		case "above":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"id":"above-usage","usage":{"prompt_tokens":4,"completion_tokens":1,"total_tokens":5}}`)
		case "error":
			response.Header().Set("Content-Type", "application/json")
			response.Header().Set("X-T120-Upstream-Safe", "upstream-error")
			response.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(response, `{"error":"t120-unique-response-sentinel"}`)
		default:
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"body":"json-body","usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
		}
	}))
	t.Cleanup(upstream.server.Close)
	return upstream
}

func (upstream *t120Upstream) body(name string) []byte {
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	return append([]byte(nil), upstream.bodies[name]...)
}

func (upstream *t120Upstream) waitFor(name string) {
	<-upstream.started[name]
}

func (upstream *t120Upstream) release(name string) {
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	select {
	case <-upstream.releaseCh[name]:
	default:
		close(upstream.releaseCh[name])
	}
}

func t120WaitFor(t *testing.T, predicate func() bool) {
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

func t120StartProcess(t *testing.T, path string, clock *requestLimitTestClock, pricing config.PricingConfig, upstreamURL, upstreamCredential, adminCredential, pepper string, logs io.Writer, budgetSinks ...func(limiter.CommittedBudgetDelta)) *t120Process {
	t.Helper()
	database, err := storage.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	repository := storage.NewAPIKeyRepository(database)
	records, err := repository.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	policies := make(map[string]auth.EffectivePolicy, len(records))
	for _, record := range records {
		policy, parseErr := auth.ParsePolicyJSONWithTokenMode([]byte(record.PolicyJSON), auth.TokenModeUsageOnly)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		policies[record.ID] = policy
	}
	tokens := limiter.NewTokenLimiter(clock.Now)
	usageRepository := storage.NewUsageBucketRepository(database)
	persistedTokens, err := usageRepository.LoadUnexpired(context.Background(), clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	committed := make([]limiter.CommittedTokenBucket, 0, len(persistedTokens))
	for _, bucket := range persistedTokens {
		policy := policies[bucket.APIKeyID]
		var match limiter.TokenWindow
		for _, candidate := range policy.TokenWindows() {
			if int64(candidate.Duration/time.Second) == bucket.BucketSeconds && candidate.Amount == bucket.BucketAmount {
				match = limiter.TokenWindow{Amount: candidate.Amount, Duration: candidate.Duration}
				break
			}
		}
		if match.Duration == 0 {
			t.Fatalf("persisted token bucket does not match policy for %q", bucket.APIKeyID)
		}
		committed = append(committed, limiter.CommittedTokenBucket{KeyID: bucket.APIKeyID, BucketStart: bucket.BucketStart, Window: match, CommittedTokens: bucket.CommittedTokens})
	}
	if err := tokens.LoadCommitted(clock.Now(), committed); err != nil {
		t.Fatal(err)
	}
	budgetRepository := storage.NewBudgetBucketRepository(database)
	persistedTotal, err := budgetRepository.LoadTotal(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	persistedDay, err := budgetRepository.LoadDay(context.Background(), clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	persistedMonth, err := budgetRepository.LoadMonth(context.Background(), clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	spent := make([]limiter.BudgetSpent, 0, len(persistedTotal)+len(persistedDay)+len(persistedMonth))
	for _, bucket := range append(append(persistedTotal, persistedDay...), persistedMonth...) {
		value, valueErr := accounting.NewMoneyMicros(bucket.SpentMicros)
		if valueErr != nil {
			t.Fatal(valueErr)
		}
		spent = append(spent, limiter.BudgetSpent{KeyID: bucket.APIKeyID, Spent: value, Period: bucket.Period, PeriodStart: bucket.PeriodStart})
	}
	budget := limiter.NewBudgetLimiter(clock.Now)
	if err := budget.LoadSpent(spent); err != nil {
		t.Fatal(err)
	}
	worker := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: 32})
	logger := NewCompletionLogger(slog.New(slog.NewJSONHandler(logs, nil)), 32)
	tokenAccumulator := storage.NewUsageAggregateAccumulator(usageRepository)
	tokens.SetCommittedDeltaSink(tokenAccumulator.Sink)
	budgetAccumulator := storage.NewBudgetAccumulator(budgetRepository)
	budget.SetCommittedDeltaSink(func(delta limiter.CommittedBudgetDelta) {
		budgetAccumulator.Sink(delta)
		for _, sink := range budgetSinks {
			if sink != nil {
				sink(delta)
			}
		}
	})
	handler, err := NewHandlerWithAdminAndLimitersAndTokenConfigAndTokenLimiterAndUsageObservationWorker(transport.NewClient(), upstreamURL, upstreamCredential, adminCredential, pepper, repository, limiter.NewRequestLimiter(clock.Now), limiter.NewConcurrencyLimiter(), logger, tokens, TokenAdmissionConfig{FallbackUnknownInputTokens: 1, FallbackMaxOutputTokens: 1, PricingResolver: accounting.NewPricingResolver(pricing), BudgetLimiter: budget}, worker, auth.TokenModeUsageOnly)
	if err != nil {
		t.Fatal(err)
	}
	return &t120Process{database: database, gateway: httptest.NewServer(handler), worker: worker, logger: logger, tokenAccumulator: tokenAccumulator, budgetAccumulator: budgetAccumulator}
}

func t120StartProcessWithBudgetSink(t *testing.T, path string, clock *requestLimitTestClock, pricing config.PricingConfig, upstreamURL, upstreamCredential, adminCredential, pepper string, logs io.Writer, sink func(limiter.CommittedBudgetDelta)) *t120Process {
	return t120StartProcess(t, path, clock, pricing, upstreamURL, upstreamCredential, adminCredential, pepper, logs, sink)
}

func t120StopProcess(t *testing.T, process *t120Process) {
	t.Helper()
	process.gateway.Close()
	shutdownObservationWorker(t, process.worker)
	if err := process.logger.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := process.tokenAccumulator.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := process.budgetAccumulator.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := process.database.Close(); err != nil {
		t.Fatal(err)
	}
}

func t120BudgetTotal(t *testing.T, path, key string) int64 {
	t.Helper()
	database, err := storage.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	buckets, err := storage.NewBudgetBucketRepository(database).LoadTotal(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, bucket := range buckets {
		if bucket.APIKeyID == key {
			return bucket.SpentMicros
		}
	}
	return 0
}

func t120TokenCommitted(t *testing.T, path string, now time.Time, key string) int64 {
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
		if bucket.APIKeyID == key {
			return bucket.CommittedTokens
		}
	}
	return 0
}

func t120BudgetPeriod(t *testing.T, path, key string, period limiter.BudgetPeriod, now time.Time) int64 {
	t.Helper()
	database, err := storage.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	repository := storage.NewBudgetBucketRepository(database)
	var buckets []storage.BudgetBucket
	switch period {
	case limiter.BudgetPeriodDay:
		buckets, err = repository.LoadDay(context.Background(), now)
	case limiter.BudgetPeriodMonth:
		buckets, err = repository.LoadMonth(context.Background(), now)
	default:
		t.Fatalf("unsupported budget period %q", period)
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, bucket := range buckets {
		if bucket.APIKeyID == key {
			return bucket.SpentMicros
		}
	}
	return 0
}

// t120BlockingDB blocks only transaction acquisition. Reads remain available,
// which makes the blocked writer boundary observable without changing the
// repository implementation or leaking SQL into the HTTP layer.
type t120BlockingDB struct {
	*storage.DB
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (database *t120BlockingDB) BeginTx(ctx context.Context, options *sql.TxOptions) (*sql.Tx, error) {
	database.once.Do(func() { close(database.entered) })
	select {
	case <-database.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return database.DB.BeginTx(ctx, options)
}

func closeOnce(t *testing.T, channel chan struct{}) {
	t.Helper()
	select {
	case <-channel:
	default:
		close(channel)
	}
}
