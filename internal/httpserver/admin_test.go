package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/accounting"
	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/storage"
	"github.com/pestit/9gateway/internal/transport"
)

func TestAdminCreateKeyHTTPPersistsAndNeverCallsUpstream(t *testing.T) {
	upstreamCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { upstreamCalls++ }))
	t.Cleanup(upstream.Close)
	databasePath := filepath.Join(t.TempDir(), "gateway.db")
	database, err := storage.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	repository := storage.NewAPIKeyRepository(database)
	handler, err := NewHandlerWithAdmin(transport.NewClient(), upstream.URL, "upstream-secret", "admin-secret", "pepper", repository)
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(handler)

	request := newAdminRequest(t, gateway.URL, `{"name":"bootstrap","expires_at":"2030-01-02T03:04:05Z"}`, "admin-secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Prefix    string          `json:"prefix"`
		Key       string          `json:"key"`
		Policy    json.RawMessage `json:"policy"`
		ExpiresAt *time.Time      `json:"expires_at"`
	}
	decodeResponse(t, response, &created)
	if response.StatusCode != http.StatusCreated || created.ID == "" || created.Name != "bootstrap" || created.Key == "" || created.ExpiresAt == nil || string(created.Policy) != `{}` {
		t.Fatalf("creation response = %#v, status %d", created, response.StatusCode)
	}
	if !strings.HasPrefix(created.Key, auth.GatewayKeyNamespace) || !strings.HasPrefix(created.Key, created.Prefix) {
		t.Fatalf("creation key/prefix = %q/%q", created.Key, created.Prefix)
	}
	record, err := repository.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Name != created.Name || record.DisplayPrefix != created.Prefix || record.PolicyJSON != `{}` || strings.Contains(record.PolicyJSON, created.Key) {
		t.Fatalf("stored record = %#v", record)
	}
	encodedRecord, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedRecord), created.Key) {
		t.Fatal("repository read contains the raw key")
	}
	if upstreamCalls != 0 {
		t.Fatalf("upstream calls = %d, want zero", upstreamCalls)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = storage.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	reopened, err := storage.NewAPIKeyRepository(database).GetByID(context.Background(), created.ID)
	if err != nil || reopened.DisplayPrefix != created.Prefix {
		t.Fatalf("reopened record = %#v, error %v", reopened, err)
	}
	gateway.Close()
}

func TestAdminCreateKeyHTTPRejectsMissingWrongAndGatewayCredentials(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	handler, err := NewHandlerWithAdmin(transport.NewClient(), "http://127.0.0.1:1", "upstream-secret", "admin-secret", "pepper", storage.NewAPIKeyRepository(database))
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(handler)
	t.Cleanup(gateway.Close)
	for _, credential := range []string{"", "wrong", "Bearer sk-gw-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		request := newAdminRequest(t, gateway.URL, `{"name":"nope"}`, credential)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		decodeResponse(t, response, &body)
		if response.StatusCode != http.StatusUnauthorized || response.Header.Get(requestIDHeader) == "" || body["error"] == nil {
			t.Fatalf("credential %q response status/body = %d/%v", credential, response.StatusCode, body)
		}
	}
	health, err := http.Get(gateway.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", health.StatusCode)
	}
}

func TestAdminCreateKeyHTTPRejectsInvalidBodies(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	handler, err := NewHandlerWithAdmin(transport.NewClient(), "http://127.0.0.1:1", "upstream-secret", "admin-secret", "pepper", storage.NewAPIKeyRepository(database))
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(handler)
	t.Cleanup(gateway.Close)
	for _, body := range []string{
		`{"name":"ok","unknown":true}`,
		`{"name":42}`,
		`{"name":"ok"} {"name":"extra"}`,
		`{"name":"ok","expires_at":42}`,
		`{"name":""}`,
	} {
		request := newAdminRequest(t, gateway.URL, body, "admin-secret")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		decodeResponse(t, response, &payload)
		if response.StatusCode != http.StatusBadRequest || response.Header.Get(requestIDHeader) == "" || payload["error"] == nil {
			t.Fatalf("body %s response status/body = %d/%v", body, response.StatusCode, payload)
		}
	}
	tooLarge := `{"name":"` + strings.Repeat("x", int(adminRequestBodyLimit)) + `"}`
	request := newAdminRequest(t, gateway.URL, tooLarge, "admin-secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize status = %d", response.StatusCode)
	}
}

func TestAdminUpdateKeyPolicyHTTPIsAtomicAndTakesEffectImmediately(t *testing.T) {
	upstreamCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		upstreamCalls++
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	repository := storage.NewAPIKeyRepository(database)
	handler, err := NewHandlerWithAdmin(transport.NewClient(), upstream.URL, "upstream-secret", "admin-secret", "pepper", repository)
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(handler)
	t.Cleanup(gateway.Close)
	createdResponse, err := http.DefaultClient.Do(newAdminRequest(t, gateway.URL, `{"name":"mutable"}`, "admin-secret"))
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	decodeResponse(t, createdResponse, &created)
	if createdResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", createdResponse.StatusCode)
	}

	update := newPolicyRequest(t, gateway.URL, created.ID, `{"enabled":true,"policy":{"allowed_models":["new-model"],"request_windows":[{"amount":2,"duration":"1m"}]}}`, "admin-secret")
	update.Method = http.MethodPut
	updatedResponse, err := http.DefaultClient.Do(update)
	if err != nil {
		t.Fatal(err)
	}
	var updated struct {
		ID      string          `json:"id"`
		Enabled bool            `json:"enabled"`
		Policy  json.RawMessage `json:"policy"`
	}
	decodeResponse(t, updatedResponse, &updated)
	if updatedResponse.StatusCode != http.StatusOK || updated.ID != created.ID || !updated.Enabled || string(updated.Policy) != `{"allowed_models":["new-model"],"request_windows":[{"amount":2,"duration":"1m"}]}` {
		t.Fatalf("update response = %#v, status %d", updated, updatedResponse.StatusCode)
	}

	request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{"model":"old-model"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+created.Key)
	request.Header.Set("Content-Type", "application/json")
	oldResponse, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	oldResponse.Body.Close()
	if oldResponse.StatusCode != http.StatusForbidden || upstreamCalls != 0 {
		t.Fatalf("old model status/calls = %d/%d", oldResponse.StatusCode, upstreamCalls)
	}

	invalid := newPolicyRequest(t, gateway.URL, created.ID, `{"enabled":false,"policy":{"unknown":true}}`, "admin-secret")
	invalid.Method = http.MethodPut
	invalidResponse, err := http.DefaultClient.Do(invalid)
	if err != nil {
		t.Fatal(err)
	}
	invalidResponse.Body.Close()
	if invalidResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid update status = %d", invalidResponse.StatusCode)
	}
	record, err := repository.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !record.Enabled || record.PolicyJSON != `{"allowed_models":["new-model"],"request_windows":[{"amount":2,"duration":"1m"}]}` {
		t.Fatalf("invalid update changed record = %#v", record)
	}
}

func TestAdminBudgetPolicyHTTPIsAtomicImmediateAndPersistent(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	databasePath := filepath.Join(t.TempDir(), "budget-policy.db")
	database, err := storage.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	repository := storage.NewAPIKeyRepository(database)
	handler, err := NewHandlerWithAdminAndLimitersAndTokenConfig(
		transport.NewClient(), upstream.URL, "upstream-secret", "admin-secret", "pepper", repository,
		limiter.NewRequestLimiter(nil), limiter.NewConcurrencyLimiter(), nil,
		TokenAdmissionConfig{FallbackUnknownInputTokens: 1, FallbackMaxOutputTokens: 1, PricingResolver: accounting.NewPricingResolver(t110Pricing(t, `rules:
  - model: budget-model
    input_per_million_micros: 1
    output_per_million_micros: 1
`)), BudgetLimiter: limiter.NewBudgetLimiter()},
	)
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(handler)
	createdResponse, err := http.DefaultClient.Do(newAdminRequest(t, gateway.URL, `{"name":"budget"}`, "admin-secret"))
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	decodeResponse(t, createdResponse, &created)
	if createdResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", createdResponse.StatusCode)
	}
	policyJSON := `{"allowed_models":["budget-model"],"request_windows":[{"amount":2,"duration":"1m"}],"budget_limits":[{"amount_micros":1,"period":"total"}],"max_concurrent_requests":1}`
	update := newPolicyRequest(t, gateway.URL, created.ID, `{"enabled":true,"policy":`+policyJSON+`}`, "admin-secret")
	update.Method = http.MethodPut
	updatedResponse, err := http.DefaultClient.Do(update)
	if err != nil {
		t.Fatal(err)
	}
	var updated struct {
		Policy json.RawMessage `json:"Policy"`
	}
	decodeResponse(t, updatedResponse, &updated)
	if updatedResponse.StatusCode != http.StatusOK || string(updated.Policy) != policyJSON {
		t.Fatalf("budget update response = %d/%s", updatedResponse.StatusCode, updated.Policy)
	}
	invalid := newPolicyRequest(t, gateway.URL, created.ID, `{"enabled":false,"policy":{"budget_limits":[{"amount_micros":0,"period":"total"}]}}`, "admin-secret")
	invalid.Method = http.MethodPut
	invalidResponse, err := http.DefaultClient.Do(invalid)
	if err != nil {
		t.Fatal(err)
	}
	invalidResponse.Body.Close()
	if invalidResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid budget replacement status = %d", invalidResponse.StatusCode)
	}
	unchanged, err := repository.GetByID(context.Background(), created.ID)
	if err != nil || unchanged.Enabled != true || unchanged.PolicyJSON != policyJSON {
		t.Fatalf("invalid budget replacement changed durable state = %#v/%v", unchanged, err)
	}
	call := func(model string) *http.Response {
		t.Helper()
		request, requestErr := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{"model":"`+model+`"}`))
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		request.Header.Set("Authorization", "Bearer "+created.Key)
		request.Header.Set("Content-Type", "application/json")
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		return response
	}
	immediate := call("budget-model")
	immediate.Body.Close()
	if immediate.StatusCode != http.StatusNoContent {
		t.Fatalf("immediate budget policy status = %d", immediate.StatusCode)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	gateway.Close()
	reopened, err := storage.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	reopenedRepository := storage.NewAPIKeyRepository(reopened)
	reopenedRecord, err := reopenedRepository.GetByID(context.Background(), created.ID)
	if err != nil || reopenedRecord.PolicyJSON != policyJSON {
		t.Fatalf("reopened budget policy = %#v, error %v", reopenedRecord, err)
	}
	reopenedService, err := newAdminKeyService(reopenedRepository, []byte("pepper"))
	if err != nil {
		t.Fatal(err)
	}
	principal, err := reopenedService.auth.Authenticate(created.Key)
	if err != nil {
		t.Fatal(err)
	}
	budget, present := principal.Policy.TotalBudget()
	if micros, known := budget.Micros(); !present || !known || micros != 1 {
		t.Fatalf("reopened compiled budget = %v/%t", budget, present)
	}
	if _, err := reopenedService.updatePolicy(context.Background(), created.ID, true, []byte(policyJSON)); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAdminUpdatePolicyHTTPTransitionsValidationAndPersistence(t *testing.T) {
	var upstreamCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		upstreamCalls++
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	databasePath := filepath.Join(t.TempDir(), "policy.db")
	database, err := storage.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	repository := storage.NewAPIKeyRepository(database)
	handler, err := NewHandlerWithAdmin(transport.NewClient(), upstream.URL, "upstream-secret", "admin-secret", "pepper", repository)
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(handler)
	createdResponse, err := http.DefaultClient.Do(newAdminRequest(t, gateway.URL, `{"name":"transitions"}`, "admin-secret"))
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	decodeResponse(t, createdResponse, &created)
	if createdResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", createdResponse.StatusCode)
	}

	update := func(id, body, credential string) (*http.Response, map[string]any) {
		t.Helper()
		request := newPolicyRequest(t, gateway.URL, id, body, credential)
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		var payload map[string]any
		decodeResponse(t, response, &payload)
		return response, payload
	}

	unauthorized, payload := update(created.ID, `{"enabled":false,"policy":{}}`, "wrong-admin")
	if unauthorized.StatusCode != http.StatusUnauthorized || payload["error"] == nil || unauthorized.Header.Get(requestIDHeader) == "" {
		t.Fatalf("unauthorized update = %d/%v", unauthorized.StatusCode, payload)
	}
	for _, body := range []string{
		`{"enabled":false,"policy":{"unknown":true}}`,
		`{"enabled":false,"policy":{"token_windows":[{"amount":10,"duration":"500ms"}]}}`,
		`{"enabled":false,"policy":{"token_windows":[{"amount":10,"duration":"1500ms"}]}}`,
		`{"enabled":false,"policy":{}} trailing`,
		`{"enabled":false,"enabled":true,"policy":{}}`,
		`{"enabled":false,"policy":{"allowed_models":["[broken"]}}`,
	} {
		response, bodyPayload := update(created.ID, body, "admin-secret")
		if response.StatusCode != http.StatusBadRequest || response.Header.Get(requestIDHeader) == "" || bodyPayload["error"] == nil {
			t.Fatalf("invalid body %s = %d/%v", body, response.StatusCode, bodyPayload)
		}
	}
	tooLarge := `{"enabled":false,"policy":{"allowed_models":["` + strings.Repeat("x", int(adminRequestBodyLimit)) + `"]}}`
	oversized, oversizedPayload := update(created.ID, tooLarge, "admin-secret")
	if oversized.StatusCode != http.StatusRequestEntityTooLarge || oversized.Header.Get(requestIDHeader) == "" || oversizedPayload["error"] == nil {
		t.Fatalf("oversized update = %d/%v", oversized.StatusCode, oversizedPayload)
	}

	updated, updatedPayload := update(created.ID, `{"enabled":false,"policy":{"allowed_models":["new-model"]}}`, "admin-secret")
	if updated.StatusCode != http.StatusOK || updatedPayload["Enabled"] != false {
		t.Fatalf("disable update = %d/%v", updated.StatusCode, updatedPayload)
	}
	firstUpdatedAt, ok := updatedPayload["UpdatedAt"].(string)
	if !ok || firstUpdatedAt == "" {
		// encoding/json field matching permits lowercase request decoding, but
		// response field names are intentionally deterministic from the struct.
		t.Fatalf("update timestamp = %#v", updatedPayload["UpdatedAt"])
	}
	call := func(model string) *http.Response {
		t.Helper()
		request, requestErr := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{"model":"`+model+`"}`))
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		request.Header.Set("Authorization", "Bearer "+created.Key)
		request.Header.Set("Content-Type", "application/json")
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		response.Body.Close()
		return response
	}
	if response := call("new-model"); response.StatusCode != http.StatusUnauthorized || upstreamCalls != 0 {
		t.Fatalf("disabled key response/calls = %d/%d", response.StatusCode, upstreamCalls)
	}

	enabled, enabledPayload := update(created.ID, `{"enabled":true,"policy":{"allowed_models":["new-model"]}}`, "admin-secret")
	if enabled.StatusCode != http.StatusOK || enabledPayload["Enabled"] != true {
		t.Fatalf("enable update = %d/%v", enabled.StatusCode, enabledPayload)
	}
	if response := call("old-model"); response.StatusCode != http.StatusForbidden || upstreamCalls != 0 {
		t.Fatalf("policy rejection response/calls = %d/%d", response.StatusCode, upstreamCalls)
	}
	if response := call("new-model"); response.StatusCode != http.StatusNoContent || upstreamCalls != 1 {
		t.Fatalf("policy admission response/calls = %d/%d", response.StatusCode, upstreamCalls)
	}

	repeated, repeatedPayload := update(created.ID, `{"enabled":true,"policy":{"allowed_models":["new-model"]}}`, "admin-secret")
	if repeated.StatusCode != http.StatusOK || repeatedPayload["UpdatedAt"] != enabledPayload["UpdatedAt"] {
		t.Fatalf("idempotent update timestamps = %#v/%#v", repeatedPayload["UpdatedAt"], enabledPayload["UpdatedAt"])
	}
	notFound, notFoundPayload := update("missing-key", `{"enabled":true,"policy":{}}`, "admin-secret")
	if notFound.StatusCode != http.StatusNotFound || notFoundPayload["error"] == nil || notFound.Header.Get(requestIDHeader) == "" {
		t.Fatalf("missing update = %d/%v", notFound.StatusCode, notFoundPayload)
	}

	gateway.Close()
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	reopenedRecord, err := storage.NewAPIKeyRepository(reopened).GetByID(context.Background(), created.ID)
	if err != nil || !reopenedRecord.Enabled || reopenedRecord.PolicyJSON != `{"allowed_models":["new-model"]}` {
		t.Fatalf("reopened policy record = %#v, error %v", reopenedRecord, err)
	}
	responseUpdatedAt, err := time.Parse(time.RFC3339, firstUpdatedAt)
	if err != nil || !reopenedRecord.UpdatedAt.Equal(responseUpdatedAt) {
		t.Fatalf("reopened timestamp = %v, response %q", reopenedRecord.UpdatedAt, firstUpdatedAt)
	}
}

func TestAdminTokenPolicyReplacementRejectsActiveIdentity(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	repository := storage.NewAPIKeyRepository(database)
	clock := &requestLimitTestClock{now: time.Unix(30, 0).UTC()}
	tokens := limiter.NewTokenLimiter(clock.Now)
	service, err := newAdminKeyService(repository, []byte("pepper"))
	if err != nil {
		t.Fatal(err)
	}
	service.allowTokenPolicyReplacement = tokens.AllowsPolicyReplacement
	key := auth.GeneratedGatewayKey{RawKey: "sk-gw-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", DisplayPrefix: "sk-gw-xxxx", Digest: make([]byte, storage.HMACDigestSize)}
	if err := repository.Insert(context.Background(), storage.APIKeyRecord{ID: "id", Name: "n", DisplayPrefix: key.DisplayPrefix, Digest: key.Digest, Enabled: true, CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(1, 0).UTC(), PolicyJSON: `{"token_windows":[{"amount":10,"duration":"1m"}]}`}); err != nil {
		t.Fatal(err)
	}
	reservation, allowed, _ := tokens.Reserve("id", []limiter.TokenWindow{{Amount: 10, Duration: time.Minute}}, 1)
	if !allowed {
		t.Fatal("reservation rejected")
	}
	defer reservation.AbortConservative()
	if _, err := service.updatePolicy(context.Background(), "id", true, []byte(`{"token_windows":[]}`)); !errors.Is(err, errPolicyConflict) {
		t.Fatalf("replacement error = %v", err)
	}
	record, err := repository.GetByID(context.Background(), "id")
	if err != nil || !strings.Contains(record.PolicyJSON, "token_windows") {
		t.Fatalf("policy changed: %#v/%v", record, err)
	}
}

func TestAdminPolicyReplacementBlocksStaleHTTPAdmission(t *testing.T) {
	pepper := []byte("replacement-race-pepper")
	key := generatedForAdmin(t, pepper)
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	base := storage.NewAPIKeyRepository(database)
	if err := base.Insert(context.Background(), storage.APIKeyRecord{ID: "race", Name: "race", DisplayPrefix: key.DisplayPrefix, Digest: key.Digest, Enabled: true, CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(1, 0).UTC(), PolicyJSON: `{"allowed_models":["old"],"token_windows":[{"amount":100,"duration":"1m"}]}`}); err != nil {
		t.Fatal(err)
	}
	repository := &blockingPolicyRecordRepository{APIKeyRepository: base, entered: make(chan struct{}), release: make(chan struct{})}
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"usage":{"total_tokens":1}}`)
	}))
	t.Cleanup(upstream.Close)
	tokens := limiter.NewTokenLimiter(nil)
	handler, err := NewHandlerWithAdminAndLimitersAndTokenConfigAndTokenLimiterAndUsageObservationWorker(transport.NewClient(), upstream.URL, "upstream", "admin", string(pepper), repository, nil, nil, nil, tokens, TokenAdmissionConfig{FallbackUnknownInputTokens: 1, FallbackMaxOutputTokens: 1}, nil, auth.TokenModeEstimate)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	adminRequest := newPolicyRequest(t, server.URL, "race", `{"enabled":true,"policy":{"allowed_models":["new"],"token_windows":[{"amount":200,"duration":"1m"}]}}`, "admin")
	adminRequest.Method = http.MethodPut
	adminDone := make(chan *http.Response, 1)
	go func() {
		response, requestErr := http.DefaultClient.Do(adminRequest)
		if requestErr != nil {
			t.Errorf("admin request: %v", requestErr)
			return
		}
		adminDone <- response
	}()
	<-repository.entered
	publicRequest, err := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"old"}`))
	if err != nil {
		t.Fatal(err)
	}
	publicRequest.Header.Set("Authorization", "Bearer "+key.RawKey)
	publicRequest.Header.Set("Content-Type", "application/json")
	publicDone := make(chan *http.Response, 1)
	go func() {
		response, requestErr := http.DefaultClient.Do(publicRequest)
		if requestErr != nil {
			t.Errorf("public request: %v", requestErr)
			return
		}
		publicDone <- response
	}()
	select {
	case response := <-publicDone:
		response.Body.Close()
		t.Fatal("stale request admitted before policy commit")
	case <-time.After(25 * time.Millisecond):
	}
	close(repository.release)
	adminResponse := <-adminDone
	adminBody, _ := io.ReadAll(adminResponse.Body)
	adminResponse.Body.Close()
	if adminResponse.StatusCode != http.StatusOK {
		t.Fatalf("policy update = %d/%s", adminResponse.StatusCode, adminBody)
	}
	publicResponse := <-publicDone
	publicBody, _ := io.ReadAll(publicResponse.Body)
	publicResponse.Body.Close()
	if publicResponse.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("stale admission response = %d/%s, want token rejection", publicResponse.StatusCode, publicBody)
	}
	if _, err := base.GetByID(context.Background(), "race"); err != nil {
		t.Fatal(err)
	}
}

func TestAdminBudgetReplacementBlocksStaleHTTPAdmissionAndKeepsDurablePolicy(t *testing.T) {
	pepper := []byte("budget-replacement-race-pepper")
	key := generatedForAdmin(t, pepper)
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	base := storage.NewAPIKeyRepository(database)
	oldJSON := `{"budget_limits":[{"amount_micros":10,"period":"total"},{"amount_micros":10,"period":"day"},{"amount_micros":10,"period":"month"}]}`
	newJSON := `{"budget_limits":[{"amount_micros":1,"period":"total"},{"amount_micros":1,"period":"day"},{"amount_micros":1,"period":"month"}]}`
	if err := base.Insert(context.Background(), storage.APIKeyRecord{ID: "budget-race", Name: "budget-race", DisplayPrefix: key.DisplayPrefix, Digest: key.Digest, Enabled: true, CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(1, 0).UTC(), PolicyJSON: oldJSON}); err != nil {
		t.Fatal(err)
	}
	repository := &blockingPolicyRecordRepository{APIKeyRepository: base, entered: make(chan struct{}), release: make(chan struct{})}
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		upstreamCalls.Add(1)
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	t.Cleanup(upstream.Close)
	budget := limiter.NewBudgetLimiter()
	pricing := accounting.NewPricingResolver(t110Pricing(t, "rules:\n  - model: budget-race\n    input_per_million_micros: 1000000\n    output_per_million_micros: 1000000\n"))
	handler, err := NewHandlerWithAdminAndLimitersAndTokenConfigAndTokenLimiterAndUsageObservationWorker(transport.NewClient(), upstream.URL, "upstream", "admin", string(pepper), repository, nil, nil, nil, nil, TokenAdmissionConfig{FallbackUnknownInputTokens: 1, FallbackMaxOutputTokens: 1, PricingResolver: pricing, BudgetLimiter: budget}, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	adminRequest := newPolicyRequest(t, server.URL, "budget-race", `{"enabled":true,"policy":`+newJSON+`}`, "admin")
	adminRequest.Method = http.MethodPut
	adminDone := make(chan *http.Response, 1)
	go func() {
		response, requestErr := http.DefaultClient.Do(adminRequest)
		if requestErr != nil {
			t.Errorf("admin request: %v", requestErr)
			return
		}
		adminDone <- response
	}()
	<-repository.entered
	publicRequest, err := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"budget-race"}`))
	if err != nil {
		t.Fatal(err)
	}
	publicRequest.Header.Set("Authorization", "Bearer "+key.RawKey)
	publicRequest.Header.Set("Content-Type", "application/json")
	publicDone := make(chan *http.Response, 1)
	go func() {
		response, requestErr := http.DefaultClient.Do(publicRequest)
		if requestErr != nil {
			t.Errorf("public request: %v", requestErr)
			return
		}
		publicDone <- response
	}()
	select {
	case response := <-publicDone:
		response.Body.Close()
		t.Fatal("stale budget request passed policy barrier before commit")
	case <-time.After(25 * time.Millisecond):
	}
	close(repository.release)
	adminResponse := <-adminDone
	adminBody, _ := io.ReadAll(adminResponse.Body)
	adminResponse.Body.Close()
	if adminResponse.StatusCode != http.StatusOK {
		t.Fatalf("budget policy update = %d/%s", adminResponse.StatusCode, adminBody)
	}
	publicResponse := <-publicDone
	publicBody, _ := io.ReadAll(publicResponse.Body)
	publicResponse.Body.Close()
	if publicResponse.StatusCode != http.StatusUnauthorized || upstreamCalls.Load() != 0 {
		t.Fatalf("stale budget admission = %d/%s, upstream calls %d", publicResponse.StatusCode, publicBody, upstreamCalls.Load())
	}
	record, err := base.GetByID(context.Background(), "budget-race")
	if err != nil || record.PolicyJSON != newJSON {
		t.Fatalf("durable budget policy = %#v/%v", record, err)
	}
}

func TestAdminBudgetReplacementRepositoryFailureLeavesRuntimeAndSnapshotUnchanged(t *testing.T) {
	pepper := []byte("budget-replacement-failure-pepper")
	key := generatedForAdmin(t, pepper)
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	base := storage.NewAPIKeyRepository(database)
	oldJSON := `{"budget_limits":[{"amount_micros":10,"period":"total"}]}`
	newJSON := `{"budget_limits":[{"amount_micros":1,"period":"total"}]}`
	if err := base.Insert(context.Background(), storage.APIKeyRecord{ID: "budget-failure", Name: "budget-failure", DisplayPrefix: key.DisplayPrefix, Digest: key.Digest, Enabled: true, CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(1, 0).UTC(), PolicyJSON: oldJSON}); err != nil {
		t.Fatal(err)
	}
	repository := &failingPolicyRecordRepository{APIKeyRepository: base, err: errors.New("injected repository failure")}
	budget := limiter.NewBudgetLimiter()
	oldPolicy, _ := auth.ParsePolicyJSON([]byte(oldJSON))
	oldTotal, _ := oldPolicy.TotalBudget()
	budget.RegisterPolicy("budget-failure", limiter.BudgetPolicy{Total: oldTotal, Limited: true})
	service, err := newAdminKeyService(repository, pepper)
	if err != nil {
		t.Fatal(err)
	}
	service.allowBudgetPolicyReplacement = func(id string, old, next limiter.BudgetPolicy) bool {
		return budget.AllowsPolicyReplacement(id, old, next)
	}
	service.replaceBudgetPolicy = func(id string, old, next limiter.BudgetPolicy, commit func() error) error {
		return budget.ReplacePolicy(id, old, next, commit)
	}
	if _, err := service.updatePolicy(context.Background(), "budget-failure", true, []byte(newJSON)); err == nil {
		t.Fatal("repository failure unexpectedly succeeded")
	}
	principal, err := service.auth.Authenticate(key.RawKey)
	if err != nil {
		t.Fatal(err)
	}
	if got, present := principal.Policy.TotalBudget(); !present || got != oldTotal {
		t.Fatalf("snapshot budget changed after repository failure = %v/%t", got, present)
	}
	reservation, err := budget.Reserve("budget-failure", limiter.LimitedBudgetPolicy(oldTotal), oldTotal)
	if err != nil {
		t.Fatalf("old runtime budget changed after repository failure = %v", err)
	}
	_ = reservation.ReleaseBeforeUpstream()
	record, err := base.GetByID(context.Background(), "budget-failure")
	if err != nil || record.PolicyJSON != oldJSON {
		t.Fatalf("durable policy changed after repository failure = %#v/%v", record, err)
	}
}

type blockingPolicyRecordRepository struct {
	*storage.APIKeyRepository
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

type failingPolicyRecordRepository struct {
	*storage.APIKeyRepository
	err error
}

func (repository *failingPolicyRecordRepository) UpdatePolicyRecord(context.Context, string, bool, string) (storage.APIKeyRecord, error) {
	return storage.APIKeyRecord{}, repository.err
}

func (repository *blockingPolicyRecordRepository) UpdatePolicyRecord(ctx context.Context, id string, enabled bool, policy string) (storage.APIKeyRecord, error) {
	repository.once.Do(func() { close(repository.entered) })
	<-repository.release
	return repository.APIKeyRepository.UpdatePolicyRecord(ctx, id, enabled, policy)
}

func TestAdminUpdatePolicyHTTPTokenWindowsModesAndReopen(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	databasePath := filepath.Join(t.TempDir(), "token-policy.db")
	database, err := storage.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	repository := storage.NewAPIKeyRepository(database)
	handler, err := NewHandlerWithAdmin(transport.NewClient(), upstream.URL, "upstream-secret", "admin-secret", "pepper", repository, auth.TokenModeUsageOnly)
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(handler)
	createdResponse, err := http.DefaultClient.Do(newAdminRequest(t, gateway.URL, `{"name":"tokens"}`, "admin-secret"))
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	decodeResponse(t, createdResponse, &created)
	if createdResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", createdResponse.StatusCode)
	}
	policyJSON := `{"allowed_models":["token-model"],"request_windows":[{"amount":2,"duration":"1m"}],"token_windows":[{"amount":100,"duration":"1h"},{"amount":1000,"duration":"24h"}],"token_mode":"estimate","max_concurrent_requests":2}`
	update := newPolicyRequest(t, gateway.URL, created.ID, `{"enabled":true,"policy":`+policyJSON+`}`, "admin-secret")
	update.Method = http.MethodPut
	updatedResponse, err := http.DefaultClient.Do(update)
	if err != nil {
		t.Fatal(err)
	}
	var updated struct {
		Policy json.RawMessage `json:"Policy"`
	}
	decodeResponse(t, updatedResponse, &updated)
	if updatedResponse.StatusCode != http.StatusOK || string(updated.Policy) != policyJSON {
		t.Fatalf("token policy update = %d/%s", updatedResponse.StatusCode, updated.Policy)
	}
	service, err := newAdminKeyService(repository, []byte("pepper"), auth.TokenModeUsageOnly)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := service.auth.Authenticate(created.Key)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(principal.Policy.TokenWindows(), []auth.TokenWindow{{Amount: 100, Duration: time.Hour}, {Amount: 1000, Duration: 24 * time.Hour}}) || principal.Policy.TokenMode() != auth.TokenModeEstimate {
		t.Fatalf("published token policy = %#v/%q", principal.Policy.TokenWindows(), principal.Policy.TokenMode())
	}
	if mode, ok := principal.Policy.TokenModeOverride(); !ok || mode != auth.TokenModeEstimate {
		t.Fatalf("published token mode override = %q/%t", mode, ok)
	}
	if strings.Contains(string(principal.PolicyJSON), created.Key) {
		t.Fatal("published policy contains raw key")
	}

	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	gateway.Close()
	reopened, err := storage.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	reopenedService, err := newAdminKeyService(storage.NewAPIKeyRepository(reopened), []byte("pepper"), auth.TokenModeUsageOnly)
	if err != nil {
		t.Fatal(err)
	}
	reopenedPrincipal, err := reopenedService.auth.Authenticate(created.Key)
	if err != nil || len(reopenedPrincipal.Policy.TokenWindows()) != 2 || reopenedPrincipal.Policy.TokenMode() != auth.TokenModeEstimate {
		t.Fatalf("reopened token policy = %#v/%q, error %v", reopenedPrincipal.Policy.TokenWindows(), reopenedPrincipal.Policy.TokenMode(), err)
	}
}

func TestAdminKeyServiceRetriesDuplicateGenerationAndReturnsSafeFailure(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	service, err := newAdminKeyService(storage.NewAPIKeyRepository(database), []byte("pepper"))
	if err != nil {
		t.Fatal(err)
	}
	generated := auth.GeneratedGatewayKey{RawKey: "sk-gw-eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHg", DisplayPrefix: "sk-gw-eHh4eHh", Digest: make([]byte, storage.HMACDigestSize)}
	service.generator = &fixedGenerator{key: generated}
	if _, err := service.create(context.Background(), "first", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := service.create(context.Background(), "second", nil); !errors.Is(err, errAdminKeyCreation) || strings.Contains(err.Error(), generated.RawKey) {
		t.Fatalf("duplicate failure = %v", err)
	}
}

func TestAdminKeyServiceStartupSnapshotFailureIsFallible(t *testing.T) {
	service, err := newAdminKeyService(&testAdminRepository{listErr: errors.New("database unavailable")}, []byte("pepper"))
	if err == nil || service != nil || !errors.Is(err, errAdminKeyCreation) {
		t.Fatalf("startup service = %v, %v; want safe construction error", service, err)
	}
}

func TestNewHandlerWithAdminPropagatesStartupSnapshotFailure(t *testing.T) {
	handler, err := NewHandlerWithAdmin(transport.NewClient(), "http://127.0.0.1:1", "upstream-secret", "admin-secret", "pepper", &testAdminRepository{listErr: errors.New("database unavailable")})
	if err == nil || handler != nil || !errors.Is(err, errAdminKeyCreation) {
		t.Fatalf("exported handler construction = %v, %v; want startup failure", handler, err)
	}
}

func TestAdminKeyServicePreparesBeforeInsert(t *testing.T) {
	pepper := []byte("pepper")
	old := generatedForAdmin(t, pepper)
	repository := &testAdminRepository{}
	service, err := newAdminKeyService(repository, []byte("pepper"))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.auth.Load([]auth.Record{{ID: "old", Name: "old", DisplayPrefix: old.DisplayPrefix, Digest: old.Digest, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	repository.records = []storage.APIKeyRecord{
		{ID: "old", Name: "old", DisplayPrefix: old.DisplayPrefix, Digest: old.Digest, Enabled: true},
		{ID: "bad", DisplayPrefix: "not-a-gateway-prefix", Digest: make([]byte, storage.HMACDigestSize), Enabled: true},
	}
	if _, err := service.create(context.Background(), "not-persisted", nil); !errors.Is(err, errAdminKeyCreation) {
		t.Fatalf("create error = %v, want safe creation error", err)
	}
	if repository.insertCalls != 0 || len(repository.records) != 2 {
		t.Fatalf("repository after failed preparation: insert calls %d, records %d", repository.insertCalls, len(repository.records))
	}
	if _, err := service.auth.Authenticate(old.RawKey); err != nil {
		t.Fatalf("published snapshot changed after failed preparation: %v", err)
	}
}

func TestAdminKeyServiceNonConflictInsertFailureDoesNotPublish(t *testing.T) {
	pepper := []byte("pepper")
	old := generatedForAdmin(t, pepper)
	repository := &testAdminRepository{insertErr: errors.New("disk full")}
	service, err := newAdminKeyService(repository, pepper)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.auth.Load([]auth.Record{{ID: "old", Name: "old", DisplayPrefix: old.DisplayPrefix, Digest: old.Digest, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	service.generator = &fixedGenerator{key: generatedForAdmin(t, pepper)}
	if _, err := service.create(context.Background(), "failed", nil); !errors.Is(err, errAdminKeyCreation) {
		t.Fatalf("insert failure = %v, want safe creation failure", err)
	}
	if repository.insertCalls != 1 {
		t.Fatalf("insert calls = %d, want 1", repository.insertCalls)
	}
	if _, err := service.auth.Authenticate(old.RawKey); err != nil {
		t.Fatalf("old key stopped authenticating after failed insert: %v", err)
	}
	if _, err := service.auth.Authenticate(service.generator.(*fixedGenerator).key.RawKey); !errors.Is(err, auth.ErrInvalidCredential) {
		t.Fatalf("failed insert key authenticated: %v", err)
	}
}

func TestAdminKeyServicePublishesWhenContextCanceledAfterDurableInsert(t *testing.T) {
	pepper := []byte("pepper")
	repository := &testAdminRepository{
		blockAfterInsert:  true,
		insertCommitted:   make(chan struct{}),
		allowInsertReturn: make(chan struct{}),
	}
	service, err := newAdminKeyService(repository, pepper)
	if err != nil {
		t.Fatal(err)
	}
	generated := generatedForAdmin(t, pepper)
	service.generator = &fixedGenerator{key: generated}
	result := make(chan struct {
		created createdAdminKey
		err     error
	}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		created, err := service.create(ctx, "canceled-after-insert", nil)
		result <- struct {
			created createdAdminKey
			err     error
		}{created, err}
	}()
	select {
	case <-repository.insertCommitted:
		cancel()
		close(repository.allowInsertReturn)
	case <-time.After(time.Second):
		t.Fatal("insert did not become durable")
	}
	select {
	case got := <-result:
		if got.err != nil || got.created.RawKey != generated.RawKey {
			t.Fatalf("creation after cancellation = %#v, %v", got.created, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("creation did not return")
	}
	if _, err := service.auth.Authenticate(generated.RawKey); err != nil {
		t.Fatalf("durably inserted key was not published after cancellation: %v", err)
	}
}

func TestAdminKeyServiceCanceledPreparationDoesNotInsert(t *testing.T) {
	repository := &testAdminRepository{}
	service, err := newAdminKeyService(repository, []byte("pepper"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.create(ctx, "canceled", nil); !errors.Is(err, errAdminKeyCreation) {
		t.Fatalf("create error = %v, want safe creation error", err)
	}
	if repository.insertCalls != 0 || len(repository.records) != 0 {
		t.Fatalf("canceled creation persisted: insert calls %d, records %d", repository.insertCalls, len(repository.records))
	}
}

func TestAdminPolicyUpdatePublishesAfterCanceledRequestContext(t *testing.T) {
	pepper := []byte("policy-cancel-pepper")
	generated := generatedForAdmin(t, pepper)
	repository := &cancelingPolicyRepository{records: []storage.APIKeyRecord{{
		ID: "key", Name: "key", DisplayPrefix: generated.DisplayPrefix, Digest: generated.Digest,
		Enabled: true, CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(1, 0).UTC(), PolicyJSON: `{}`,
	}}, updated: make(chan struct{}), allowReturn: make(chan struct{})}
	service, err := newAdminKeyService(repository, pepper)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, updateErr := service.updatePolicy(ctx, "key", true, []byte(`{"allowed_models":["new"]}`))
		result <- updateErr
	}()
	select {
	case <-repository.updated:
		cancel()
		close(repository.allowReturn)
	case <-time.After(time.Second):
		t.Fatal("policy update did not reach durable mutation")
	}
	if updateErr := <-result; updateErr != nil {
		t.Fatalf("canceled policy update = %v", updateErr)
	}
	if _, err := service.auth.Authenticate(generated.RawKey); err != nil {
		t.Fatalf("key disappeared after canceled policy update: %v", err)
	}
	if !repository.records[0].Enabled || repository.records[0].PolicyJSON != `{"allowed_models":["new"]}` {
		t.Fatalf("durable policy = %#v", repository.records[0])
	}
}

func TestAdminKeyServiceConcurrentCreationsPublishAllKeys(t *testing.T) {
	repository := &testAdminRepository{}
	service, err := newAdminKeyService(repository, []byte("pepper"))
	if err != nil {
		t.Fatal(err)
	}
	const creations = 8
	results := make(chan createdAdminKey, creations)
	errorsCh := make(chan error, creations)
	for index := 0; index < creations; index++ {
		go func() {
			created, err := service.create(context.Background(), "concurrent", nil)
			if err != nil {
				errorsCh <- err
				return
			}
			results <- created
		}()
	}
	keys := make([]createdAdminKey, 0, creations)
	for index := 0; index < creations; index++ {
		select {
		case err := <-errorsCh:
			t.Fatal(err)
		case created := <-results:
			keys = append(keys, created)
		}
	}
	if len(repository.records) != creations {
		t.Fatalf("persisted records = %d, want %d", len(repository.records), creations)
	}
	for _, created := range keys {
		if _, err := service.auth.Authenticate(created.RawKey); err != nil {
			t.Fatalf("published key %q did not authenticate: %v", created.Prefix, err)
		}
	}
}

func TestAdminCreateKeyHTTPDuplicateGenerationFailureIsSafe(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	service, err := newAdminKeyService(storage.NewAPIKeyRepository(database), []byte("pepper"))
	if err != nil {
		t.Fatal(err)
	}
	generated := auth.GeneratedGatewayKey{RawKey: "sk-gw-eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHg", DisplayPrefix: "sk-gw-eHh4eHh", Digest: make([]byte, storage.HMACDigestSize)}
	service.generator = &fixedGenerator{key: generated}
	admin := &adminHandler{credential: "admin-secret", service: service}
	handler := newHandlerWithCompletionLogger(nil, routeWithAdmin(newProxyHandler(transport.NewClient(), "http://127.0.0.1:1", "upstream-secret"), admin))
	gateway := httptest.NewServer(handler)
	t.Cleanup(gateway.Close)

	first, err := http.DefaultClient.Do(newAdminRequest(t, gateway.URL, `{"name":"first"}`, "admin-secret"))
	if err != nil {
		t.Fatal(err)
	}
	firstBody, err := io.ReadAll(first.Body)
	first.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if first.StatusCode != http.StatusCreated || !strings.Contains(string(firstBody), generated.RawKey) {
		t.Fatalf("first creation status/body = %d/%s", first.StatusCode, firstBody)
	}

	second, err := http.DefaultClient.Do(newAdminRequest(t, gateway.URL, `{"name":"second"}`, "admin-secret"))
	if err != nil {
		t.Fatal(err)
	}
	secondBody, err := io.ReadAll(second.Body)
	second.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if second.StatusCode != http.StatusInternalServerError || second.Header.Get(requestIDHeader) == "" || strings.Contains(string(secondBody), generated.RawKey) {
		t.Fatalf("duplicate creation status/body = %d/%s", second.StatusCode, secondBody)
	}
}

type fixedGenerator struct{ key auth.GeneratedGatewayKey }

func (generator *fixedGenerator) Generate([]byte) (auth.GeneratedGatewayKey, error) {
	return generator.key, nil
}

type testAdminRepository struct {
	records           []storage.APIKeyRecord
	listErr           error
	insertErr         error
	insertCalls       int
	blockAfterInsert  bool
	insertCommitted   chan struct{}
	allowInsertReturn chan struct{}
}

type cancelingPolicyRepository struct {
	records     []storage.APIKeyRecord
	updated     chan struct{}
	allowReturn chan struct{}
}

func (repository *cancelingPolicyRepository) List(ctx context.Context) ([]storage.APIKeyRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]storage.APIKeyRecord(nil), repository.records...), nil
}

func (repository *cancelingPolicyRepository) Insert(context.Context, storage.APIKeyRecord) error {
	return nil
}

func (repository *cancelingPolicyRepository) UpdatePolicy(context.Context, string, bool, string) error {
	return errors.New("legacy update should not be called")
}

func (repository *cancelingPolicyRepository) UpdatePolicyRecord(_ context.Context, id string, enabled bool, policy string) (storage.APIKeyRecord, error) {
	for index := range repository.records {
		if repository.records[index].ID == id {
			repository.records[index].Enabled = enabled
			repository.records[index].PolicyJSON = policy
			if repository.updated == nil {
				repository.updated = make(chan struct{})
				repository.allowReturn = make(chan struct{})
			}
			close(repository.updated)
			<-repository.allowReturn
			return repository.records[index], nil
		}
	}
	return storage.APIKeyRecord{}, storage.ErrNotFound
}

func generatedForAdmin(t *testing.T, pepper []byte) auth.GeneratedGatewayKey {
	t.Helper()
	generated, err := auth.GenerateGatewayKey(pepper)
	if err != nil {
		t.Fatal(err)
	}
	return generated
}

func (repository *testAdminRepository) List(ctx context.Context) ([]storage.APIKeyRecord, error) {
	if repository.listErr != nil {
		return nil, repository.listErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]storage.APIKeyRecord(nil), repository.records...), nil
}

func (repository *testAdminRepository) Insert(_ context.Context, record storage.APIKeyRecord) error {
	repository.insertCalls++
	repository.records = append(repository.records, record)
	if repository.blockAfterInsert {
		close(repository.insertCommitted)
		<-repository.allowInsertReturn
	}
	if repository.insertErr != nil {
		return repository.insertErr
	}
	return nil
}

func newAdminRequest(t *testing.T, baseURL, body, credential string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, baseURL+"/admin/v1/keys", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if credential != "" {
		if strings.HasPrefix(credential, "Bearer ") {
			request.Header.Set("Authorization", credential)
		} else {
			request.Header.Set("Authorization", "Bearer "+credential)
		}
	}
	return request
}

func newPolicyRequest(t *testing.T, baseURL, id, body, credential string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodPut, baseURL+"/admin/v1/keys/"+id+"/policy", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if credential != "" {
		request.Header.Set("Authorization", "Bearer "+credential)
	}
	return request
}

func decodeResponse(t *testing.T, response *http.Response, destination any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}
