package httpserver

import (
	"context"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// CompletionRecord is the immutable, bounded data handed from HTTP handling to
// the structured logger. It intentionally contains no request headers or
// credentials.
type CompletionRecord struct {
	RequestID string
	Method    string
	Path      string
	Status    int
	Duration  time.Duration
}

const defaultCompletionQueueCapacity = 128

// CompletionLogger writes completion records on one background worker. The
// queue is deliberately bounded: a full queue drops a record instead of
// making response completion wait for a slow sink.
type CompletionLogger struct {
	logger *slog.Logger
	queue  chan CompletionRecord
	stop   chan struct{}
	done   chan struct{}

	accepting atomic.Bool
	dropped   atomic.Uint64
	stopOnce  sync.Once
	inFlight  atomic.Int64
}

// NewCompletionLogger starts a single worker for a bounded completion queue.
// A non-positive capacity uses the production default.
func NewCompletionLogger(logger *slog.Logger, capacity int) *CompletionLogger {
	if logger == nil {
		logger = slog.Default()
	}
	if capacity <= 0 {
		capacity = defaultCompletionQueueCapacity
	}

	completionLogger := &CompletionLogger{
		logger: logger,
		queue:  make(chan CompletionRecord, capacity),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	completionLogger.accepting.Store(true)
	go completionLogger.run()
	return completionLogger
}

func (completionLogger *CompletionLogger) run() {
	defer close(completionLogger.done)
	for {
		select {
		case record := <-completionLogger.queue:
			completionLogger.write(record)
		case <-completionLogger.stop:
			completionLogger.drain()
			return
		}
	}
}

func (completionLogger *CompletionLogger) drain() {
	for {
		select {
		case record := <-completionLogger.queue:
			completionLogger.write(record)
		default:
			return
		}
	}
}

func (completionLogger *CompletionLogger) write(record CompletionRecord) {
	completionLogger.logger.Info("request completed",
		"request_id", record.RequestID,
		"method", record.Method,
		"path", record.Path,
		"status", record.Status,
		"duration", record.Duration,
	)
}

// Enqueue hands off one completion record without waiting. It returns false
// when the logger is shutting down or its bounded queue is full.
func (completionLogger *CompletionLogger) Enqueue(record CompletionRecord) bool {
	// Keep the handoff non-blocking without a mutex. Shutdown changes
	// accepting to false and waits for operations admitted before that change.
	// Increment before checking accepting: an enqueue racing Shutdown is either
	// counted by Shutdown, or observes the closed admission gate and drops.
	completionLogger.inFlight.Add(1)
	defer completionLogger.inFlight.Add(-1)
	if !completionLogger.accepting.Load() {
		completionLogger.dropped.Add(1)
		return false
	}
	select {
	case completionLogger.queue <- record:
		return true
	default:
		completionLogger.dropped.Add(1)
		return false
	}
}

// Dropped reports completion records discarded because the queue was full or
// the logger had begun shutting down.
func (completionLogger *CompletionLogger) Dropped() uint64 {
	return completionLogger.dropped.Load()
}

// Shutdown stops accepting records and waits for the worker to drain the
// bounded queue, subject to the supplied deadline. A blocked sink can make
// the worker outlive this bounded wait; it cannot delay any HTTP response.
func (completionLogger *CompletionLogger) Shutdown(ctx context.Context) error {
	completionLogger.accepting.Store(false)
	for completionLogger.inFlight.Load() != 0 {
		if err := ctx.Err(); err != nil {
			completionLogger.stopOnce.Do(func() { close(completionLogger.stop) })
			return err
		}
		runtime.Gosched()
	}
	completionLogger.stopOnce.Do(func() { close(completionLogger.stop) })

	select {
	case <-completionLogger.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
