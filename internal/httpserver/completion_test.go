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
			gateway := httptest.NewServer(newTransportHandlerWithCompletionLogger(transport.NewClient(), upstream.URL, "upstream-secret", completionLogger))
			t.Cleanup(gateway.Close)

			response, err := http.Get(gateway.URL + "/v1/response")
			if err != nil {
				t.Fatalf("GET response: %v", err)
			}
			readDone := make(chan struct{})
			var body []byte
			var readErr error
			go func() {
				body, readErr = io.ReadAll(response.Body)
				response.Body.Close()
				close(readDone)
			}()
			select {
			case <-readDone:
			case <-time.After(2 * time.Second):
				t.Fatal("response completion was delayed by blocked sink")
			}
			if readErr != nil {
				t.Fatalf("read response: %v", readErr)
			}
			if string(body) != test.body {
				t.Fatalf("body = %q, want %q", body, test.body)
			}
			select {
			case <-logHandler.entered:
			case <-time.After(2 * time.Second):
				t.Fatal("completion logger did not receive record")
			}
		})
	}
}

func TestNewHandlerDoesNotSynchronouslyLogCompletion(t *testing.T) {
	logHandler := &blockingCompletionHandler{entered: make(chan struct{}), release: make(chan struct{})}
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(logHandler))
	t.Cleanup(func() {
		slog.SetDefault(previousLogger)
		close(logHandler.release)
	})

	for _, test := range []struct {
		name        string
		contentType string
		body        string
	}{
		{name: "ordinary response", contentType: "application/json", body: `{"ok":true}`},
		{name: "SSE EOF", contentType: "text/event-stream", body: "data: final\n\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				response.Header().Set("Content-Type", test.contentType)
				_, _ = io.WriteString(response, test.body)
			}))
			t.Cleanup(upstream.Close)

			gateway := httptest.NewServer(newTransportHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
			t.Cleanup(gateway.Close)
			response, err := http.Get(gateway.URL + "/v1/response")
			if err != nil {
				t.Fatalf("GET response: %v", err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				t.Fatalf("read response: %v", err)
			}
			if string(body) != test.body {
				t.Fatalf("body = %q, want %q", body, test.body)
			}
			select {
			case <-logHandler.entered:
				t.Fatal("convenience handler synchronously logged completion")
			default:
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
	enqueueDone := make(chan bool, 1)
	go func() { enqueueDone <- completionLogger.Enqueue(record) }()
	select {
	case accepted := <-enqueueDone:
		if accepted {
			t.Fatal("saturated completion queue accepted a record")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("saturated enqueue blocked")
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

func TestCompletionLoggerShutdownIsBoundedWhenSinkBlocks(t *testing.T) {
	logHandler := &blockingCompletionHandler{entered: make(chan struct{}), release: make(chan struct{})}
	completionLogger := NewCompletionLogger(slog.New(logHandler), 1)
	if !completionLogger.Enqueue(CompletionRecord{RequestID: "request-1"}) {
		t.Fatal("completion record was dropped")
	}
	select {
	case <-logHandler.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not enter blocked sink")
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	err := completionLogger.Shutdown(shutdownContext)
	cancel()
	if err != context.DeadlineExceeded {
		t.Fatalf("Shutdown() error = %v, want deadline exceeded", err)
	}
	close(logHandler.release)
	shutdownCompletionLogger(t, completionLogger)
}

func TestCompletionLoggerConcurrentEnqueueAndShutdown(t *testing.T) {
	completionLogger := NewCompletionLogger(slog.New(slog.NewTextHandler(io.Discard, nil)), 8)
	const enqueueCount = 128
	start := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(enqueueCount)
	for index := 0; index < enqueueCount; index++ {
		go func(index int) {
			defer workers.Done()
			<-start
			completionLogger.Enqueue(CompletionRecord{RequestID: string(rune(index + 1))})
		}(index)
	}
	close(start)
	shutdownDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		shutdownDone <- completionLogger.Shutdown(ctx)
	}()
	workers.Wait()
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("Shutdown() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent enqueue/shutdown deadlocked")
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
