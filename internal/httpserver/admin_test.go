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
	gateway := httptest.NewServer(NewHandlerWithAdmin(transport.NewClient(), upstream.URL, "upstream-secret", "admin-secret", "pepper", repository))

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
	gateway := httptest.NewServer(NewHandlerWithAdmin(transport.NewClient(), "http://127.0.0.1:1", "upstream-secret", "admin-secret", "pepper", storage.NewAPIKeyRepository(database)))
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
	gateway := httptest.NewServer(NewHandlerWithAdmin(transport.NewClient(), "http://127.0.0.1:1", "upstream-secret", "admin-secret", "pepper", storage.NewAPIKeyRepository(database)))
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

func TestAdminKeyServiceRetriesDuplicateGenerationAndReturnsSafeFailure(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	service := newAdminKeyService(storage.NewAPIKeyRepository(database), []byte("pepper"))
	generated := auth.GeneratedGatewayKey{RawKey: "sk-gw-eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHg", DisplayPrefix: "sk-gw-eHh4eHh", Digest: make([]byte, storage.HMACDigestSize)}
	service.generator = &fixedGenerator{key: generated}
	if _, err := service.create(context.Background(), "first", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := service.create(context.Background(), "second", nil); !errors.Is(err, errAdminKeyCreation) || strings.Contains(err.Error(), generated.RawKey) {
		t.Fatalf("duplicate failure = %v", err)
	}
}

func TestAdminCreateKeyHTTPDuplicateGenerationFailureIsSafe(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	service := newAdminKeyService(storage.NewAPIKeyRepository(database), []byte("pepper"))
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

func decodeResponse(t *testing.T, response *http.Response, destination any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}
