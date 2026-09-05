package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/auth"
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
		ID        string     `json:"id"`
		Name      string     `json:"name"`
		Prefix    string     `json:"prefix"`
		Key       string     `json:"key"`
		ExpiresAt *time.Time `json:"expires_at"`
	}
	decodeResponse(t, response, &created)
	if response.StatusCode != http.StatusCreated || created.ID == "" || created.Name != "bootstrap" || created.Key == "" || created.ExpiresAt == nil {
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
