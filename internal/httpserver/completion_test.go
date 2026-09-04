package httpserver

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/transport"
)

type blockingCompletionHandler struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (handler *blockingCompletionHandler) Enabled(context.Context, slog.Level) bool { return true }

func (handler *blockingCompletionHandler) Handle(context.Context, slog.Record) error {
	handler.once.Do(func() { close(handler.entered) })
	<-handler.release
	return nil
}

func (handler *blockingCompletionHandler) WithAttrs([]slog.Attr) slog.Handler { return handler }

func (handler *blockingCompletionHandler) WithGroup(string) slog.Handler { return handler }

func TestCompletionHandoffDoesNotDelayResponseCompletion(t *testing.T) {
	for _, test := range []struct {
		name        string
		contentType string
		body        string
	}{
		{name: "ordinary response", contentType: "application/json", body: `{"ok":true}`},
		{name: "SSE EOF", contentType: "text/event-stream", body: "data: final\n\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			logHandler := &blockingCompletionHandler{entered: make(chan struct{}), release: make(chan struct{})}
			completionLogger := NewCompletionLogger(slog.New(logHandler), 1)
			t.Cleanup(func() {
				close(logHandler.release)
				shutdownCompletionLogger(t, completionLogger)
			})

			upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				response.Header().Set("Content-Type", test.contentType)
				_, _ = io.WriteString(response, test.body)
			}))
			t.Cleanup(upstream.Close)
			gateway := httptest.NewServer(NewHandlerWithCompletionLogger(transport.NewClient(), upstream.URL, "upstream-secret", completionLogger))
			t.Cleanup(gateway.Close)

			response, err := http.Get(gateway.URL + "/v1/response")
			if err != nil {
				t.Fatalf("GET response: %v", err)
			}
			started := time.Now()
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				t.Fatalf("read response: %v", err)
			}
			if string(body) != test.body {
				t.Fatalf("body = %q, want %q", body, test.body)
			}
			if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
				t.Fatalf("response completion took %v while sink was blocked", elapsed)
			}
			select {
			case <-logHandler.entered:
			case <-time.After(time.Second):
				t.Fatal("completion logger did not receive record")
			}
		})
	}
}

func TestCompletionQueueDropsWithoutBlockingWhenSaturated(t *testing.T) {
	logHandler := &blockingCompletionHandler{entered: make(chan struct{}), release: make(chan struct{})}
	completionLogger := NewCompletionLogger(slog.New(logHandler), 1)
	defer func() {
		close(logHandler.release)
		shutdownCompletionLogger(t, completionLogger)
	}()

	record := CompletionRecord{RequestID: "request-1", Method: http.MethodGet, Path: "/health", Status: http.StatusOK}
	if !completionLogger.Enqueue(record) {
		t.Fatal("first completion record was dropped")
	}
	select {
	case <-logHandler.entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not enter blocked sink")
	}
	if !completionLogger.Enqueue(record) {
		t.Fatal("second completion record was dropped instead of filling queue")
	}
	started := time.Now()
	if completionLogger.Enqueue(record) {
		t.Fatal("saturated completion queue accepted a record")
	}
	if elapsed := time.Since(started); elapsed > 50*time.Millisecond {
		t.Fatalf("saturated enqueue blocked for %v", elapsed)
	}
	if got := completionLogger.Dropped(); got != 1 {
		t.Fatalf("dropped records = %d, want 1", got)
	}
}

type completionRecordHandler struct {
	records chan slog.Record
}

func (handler *completionRecordHandler) Enabled(context.Context, slog.Level) bool { return true }

func (handler *completionRecordHandler) Handle(_ context.Context, record slog.Record) error {
	handler.records <- record
	return nil
}

func (handler *completionRecordHandler) WithAttrs([]slog.Attr) slog.Handler { return handler }

func (handler *completionRecordHandler) WithGroup(string) slog.Handler { return handler }

func TestCompletionRecordPreservesSafeFields(t *testing.T) {
	logHandler := &completionRecordHandler{records: make(chan slog.Record, 1)}
	completionLogger := NewCompletionLogger(slog.New(logHandler), 1)
	request := httptest.NewRequest(http.MethodPost, "http://gateway.test/v1/models?secret=query", nil)
	request = request.WithContext(context.WithValue(request.Context(), requestIDContextKey{}, "request-id"))
	recorder := httptest.NewRecorder()
	withCompletionLogger(completionLogger, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusAccepted)
	})).ServeHTTP(recorder, request)
	shutdownCompletionLogger(t, completionLogger)

	select {
	case record := <-logHandler.records:
		values := make(map[string]any)
		record.Attrs(func(attribute slog.Attr) bool {
			values[attribute.Key] = attribute.Value.Any()
			return true
		})
		if values["request_id"] != "request-id" || values["method"] != http.MethodPost || values["path"] != "/v1/models" || values["status"] != int64(http.StatusAccepted) {
			t.Fatalf("completion fields = %#v", values)
		}
		if _, ok := values["duration"]; !ok {
			t.Fatal("duration is missing")
		}
		if _, ok := values["Authorization"]; ok {
			t.Fatal("completion record contains Authorization")
		}
	case <-time.After(time.Second):
		t.Fatal("completion record was not written")
	}
}

func TestCompletionLoggerShutdownStopsWorker(t *testing.T) {
	completionLogger := NewCompletionLogger(slog.New(slog.NewTextHandler(io.Discard, nil)), 1)
	shutdownCompletionLogger(t, completionLogger)
	select {
	case <-completionLogger.done:
	default:
		t.Fatal("completion logger worker is still running after shutdown")
	}
}

func shutdownCompletionLogger(t *testing.T, completionLogger *CompletionLogger) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := completionLogger.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown completion logger: %v", err)
	}
}
