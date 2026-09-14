package httpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/storage"
)

type readinessResponse struct {
	Ready  bool                      `json:"ready"`
	Checks map[string]readinessCheck `json:"checks"`
}

func newReadinessTestServer(t *testing.T, database *storage.DB, upstream string, worker *UsageObservationWorker, state *ReadinessState) *httptest.Server {
	t.Helper()
	return httptest.NewServer(WithReadiness(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		http.Error(response, "not found", http.StatusNotFound)
	}), NewReadiness(ReadinessConfig{
		Database:               database,
		UpstreamBaseURL:        upstream,
		UsageObservationWorker: worker,
		State:                  state,
	})))
}

func getReadiness(t *testing.T, server *httptest.Server) (int, readinessResponse, string) {
	t.Helper()
	response, err := http.Get(server.URL + "/ready")
	if err != nil {
		t.Fatalf("GET /ready: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read /ready: %v", err)
	}
	var decoded readinessResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode /ready: %v; body=%q", err, body)
	}
	return response.StatusCode, decoded, string(body)
}

func TestReadinessHealthyWithoutAuthentication(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	worker := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: 1})
	defer worker.Shutdown(context.Background())
	server := newReadinessTestServer(t, database, "https://router.example.test", worker, &ReadinessState{})
	defer server.Close()

	status, result, _ := getReadiness(t, server)
	if status != http.StatusOK || !result.Ready {
		t.Fatalf("readiness = status %d, ready %v; want 200/true", status, result.Ready)
	}
	for _, name := range []string{"sqlite", "schema", "telemetry", "upstream", "lifecycle"} {
		check, ok := result.Checks[name]
		if !ok || check.Name != name || check.Status != "pass" {
			t.Fatalf("check %q = %#v, want named pass", name, check)
		}
	}
}

func TestReadinessFailsForCriticalSubsystems(t *testing.T) {
	tests := []struct {
		name        string
		upstream    string
		closeDB     bool
		wrongSchema bool
		stopWorker  bool
		shutting    bool
		wantCheck   string
	}{
		{name: "closed sqlite", upstream: "https://router.example.test", closeDB: true, wantCheck: "sqlite"},
		{name: "schema mismatch", upstream: "https://router.example.test", wrongSchema: true, wantCheck: "schema"},
		{name: "stopped telemetry", upstream: "https://router.example.test", stopWorker: true, wantCheck: "telemetry"},
		{name: "invalid upstream", upstream: "not a URL", wantCheck: "upstream"},
		{name: "shutdown", upstream: "https://router.example.test", shutting: true, wantCheck: "lifecycle"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database, err := storage.Open(context.Background(), ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			worker := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: 1})
			state := &ReadinessState{}
			if test.wrongSchema {
				if _, err := database.ExecContext(context.Background(), "PRAGMA user_version = 10"); err != nil {
					t.Fatal(err)
				}
			}
			if test.stopWorker {
				if err := worker.Shutdown(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			if test.shutting {
				state.MarkDraining()
			}
			server := newReadinessTestServer(t, database, test.upstream, worker, state)
			if test.closeDB {
				if err := database.Close(); err != nil {
					t.Fatal(err)
				}
			}
			status, result, body := getReadiness(t, server)
			server.Close()
			if status != http.StatusServiceUnavailable || result.Ready {
				t.Fatalf("readiness = status %d, ready %v; want 503/false", status, result.Ready)
			}
			check, ok := result.Checks[test.wantCheck]
			if !ok || check.Status != "fail" || check.Message == "" {
				t.Fatalf("check %q = %#v, want bounded failure", test.wantCheck, check)
			}
			if strings.Contains(body, "SELECT") || strings.Contains(body, "goroutine") {
				t.Fatalf("readiness error leaked implementation detail: %q", body)
			}
			if !test.closeDB {
				_ = database.Close()
			}
		})
	}
}

func TestReadinessTimeoutIsBoundedWithBusySQLitePool(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	connection, err := database.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// :memory: uses one pooled connection. Holding it forces readiness's
	// context-aware query to wait without introducing a blocking SQL function.
	server := newReadinessTestServer(t, database, "https://router.example.test", nil, &ReadinessState{})
	defer server.Close()
	started := time.Now()
	status, result, _ := getReadiness(t, server)
	elapsed := time.Since(started)
	connection.Close()
	if status != http.StatusServiceUnavailable || result.Ready {
		t.Fatalf("timeout readiness = status %d, ready %v; want 503/false", status, result.Ready)
	}
	if elapsed > 2500*time.Millisecond {
		t.Fatalf("readiness took %v; want roughly <= 2 seconds", elapsed)
	}
	if len(result.Checks) != 5 {
		t.Fatalf("checks = %d, want all five bounded results", len(result.Checks))
	}
}
