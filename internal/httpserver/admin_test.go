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

func TestAdminKeyServicePreparesBeforeInsert(t *testing.T) {
	repository := &testAdminRepository{}
	service, err := newAdminKeyService(repository, []byte("pepper"))
	if err != nil {
		t.Fatal(err)
	}
	repository.records = []storage.APIKeyRecord{{ID: "bad", DisplayPrefix: "not-a-gateway-prefix", Digest: make([]byte, storage.HMACDigestSize), Enabled: true}}
	if _, err := service.create(context.Background(), "not-persisted", nil); !errors.Is(err, errAdminKeyCreation) {
		t.Fatalf("create error = %v, want safe creation error", err)
	}
	if repository.insertCalls != 0 || len(repository.records) != 1 {
		t.Fatalf("repository after failed preparation: insert calls %d, records %d", repository.insertCalls, len(repository.records))
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
	records     []storage.APIKeyRecord
	listErr     error
	insertCalls int
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

func decodeResponse(t *testing.T, response *http.Response, destination any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}
