package httpserver

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/transport"
)

func TestT125RouteMetadataKeepsEscapedPathSeparateAndBounded(t *testing.T) {
	state, _ := traceTestState(t)
	path := "/v1/unknown/%E2%98%83"
	if !state.SetRouteMetadata(http.MethodPost, path, RouteGeneric) {
		t.Fatal("route metadata was not recorded")
	}
	if !state.SetRequestMetadataPath(http.MethodPost, path, RouteGeneric, "模型-1", RequestModeSSE) {
		t.Fatal("request metadata was not recorded")
	}
	record, err := state.Complete()
	if err != nil {
		t.Fatal(err)
	}
	if record.Route != RouteGeneric || record.Method != http.MethodPost || record.Path != path || record.Model != "模型-1" || record.RequestedMode != RequestModeSSE {
		t.Fatalf("trace metadata = %#v", record)
	}
	if state.SetRequestMetadataPath(http.MethodPost, path, RouteGeneric, string(make([]byte, 513)), RequestModeJSON) {
		t.Fatal("oversized model was accepted")
	}
}

func TestT125AuthenticatedCompletionCarriesOnlyStableIdentity(t *testing.T) {
	pepper := []byte("t125-pepper")
	keyA, err := auth.GenerateGatewayKey(pepper)
	if err != nil {
		t.Fatal(err)
	}
	keyB, err := auth.GenerateGatewayKey(pepper)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := auth.NewAuthenticator(pepper, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := authenticator.Load([]auth.Record{
		{ID: "key-a", Name: "alpha", DisplayPrefix: keyA.DisplayPrefix, Digest: keyA.Digest, Enabled: true},
		{ID: "key-b", Name: "beta", DisplayPrefix: keyB.DisplayPrefix, Digest: keyB.Digest, Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{}`)
	}))
	t.Cleanup(upstream.Close)
	records := make(chan slog.Record, 2)
	logger := NewCompletionLogger(slog.New(&completionRecordHandler{records: records}), 2)
	t.Cleanup(func() { shutdownCompletionLogger(t, logger) })
	gateway := httptest.NewServer(NewHandlerWithAuthenticator(transport.NewClient(), upstream.URL, "upstream-secret", authenticator, logger))
	t.Cleanup(gateway.Close)

	for _, key := range []auth.GeneratedGatewayKey{keyA, keyB} {
		request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, gateway.URL+"/v1/models?credential=query-secret", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+key.RawKey)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
	}
	shutdownCompletionLogger(t, logger)

	want := map[string]string{"key-a": "alpha", "key-b": "beta"}
	for range want {
		select {
		case record := <-records:
			values := make(map[string]any)
			record.Attrs(func(attribute slog.Attr) bool { values[attribute.Key] = attribute.Value.Any(); return true })
			id, _ := values["key_id"].(string)
			name, _ := values["key_name"].(string)
			if want[id] != name || values["route"] != "models" || values["path"] != "/v1/models" {
				t.Fatalf("completion identity/route = %#v", values)
			}
			if _, ok := want[id]; !ok {
				t.Fatalf("identity crossover or unknown key: %#v", values)
			}
		case <-time.After(time.Second):
			t.Fatal("completion record was not written")
		}
	}
}
