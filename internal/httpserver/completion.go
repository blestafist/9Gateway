package httpserver

import (
	"context"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
)

// TerminalOutcome is a bounded, non-sensitive description of the request's
// final transport state. It is deliberately an enum-like string: completion
// logs carry only this safe lifecycle value, never reservation amounts, headers,
// or error text.
type TerminalOutcome string

const (
	TerminalOutcomeUnknown        TerminalOutcome = "unknown"
	TerminalOutcomePreUpstream    TerminalOutcome = "pre_upstream"
	TerminalOutcomeUpstreamError  TerminalOutcome = "upstream_error"
	TerminalOutcomeResponseError  TerminalOutcome = "response_error"
	TerminalOutcomeComplete       TerminalOutcome = "complete"
	TerminalOutcomeCustomDispatch TerminalOutcome = "custom_dispatch"
	TerminalOutcomeCancelled      TerminalOutcome = "cancelled"
)

// TerminalMetadata carries safe typed lifecycle scalars and contains no
// accounting or payload data. Accounting is projected separately from the
// canonical final record when it is known.
type TerminalMetadata struct {
	Outcome         TerminalOutcome
	UpstreamStarted bool
}

type terminalMetadataContextKey struct{}

type terminalMetadataState struct {
	mu       sync.Mutex
	metadata TerminalMetadata
}

func newTerminalMetadataState() *terminalMetadataState {
	return &terminalMetadataState{metadata: TerminalMetadata{Outcome: TerminalOutcomeUnknown}}
}

func terminalMetadataFromContext(ctx context.Context) *terminalMetadataState {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(terminalMetadataContextKey{}).(*terminalMetadataState)
	return state
}

func (state *terminalMetadataState) set(metadata TerminalMetadata) {
	if state != nil {
		state.mu.Lock()
		if state.metadata.Outcome != TerminalOutcomeUnknown && !(metadata.Outcome == TerminalOutcomeCancelled && state.metadata.UpstreamStarted) {
			state.mu.Unlock()
			return
		}
		state.metadata = metadata
		state.mu.Unlock()
	}
}

func (state *terminalMetadataState) get() TerminalMetadata {
	if state == nil {
		return TerminalMetadata{Outcome: TerminalOutcomeUnknown}
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.metadata
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
	completionLogger.logger.LogAttrs(context.Background(), slog.LevelInfo, "request completed", completionLogAttrs(record)...)
}

// completionLogAttrs is deliberately a projection, rather than a formatter of
// the request lifecycle. The worker receives a frozen canonical record and
// emits only bounded, scalar facts from that record. In particular, optional
// values are absent when unknown; a known zero still produces an attribute.
func completionLogAttrs(record CompletionRecord) []slog.Attr {
	attrs := make([]slog.Attr, 0, 24)
	if validRequestID(record.RequestID) {
		attrs = append(attrs, slog.String("request_id", record.RequestID))
	}
	if validBoundedText(record.Method, 32) && record.Method != "" {
		attrs = append(attrs, slog.String("method", record.Method))
	}
	if validBoundedText(record.Path, 2048) && record.Path != "" {
		attrs = append(attrs, slog.String("path", record.Path))
	}
	if record.Route > RouteClassUnknown && record.Route <= RouteClassAdmin {
		attrs = append(attrs, slog.String("route", record.Route.String()))
	}
	if validBoundedText(record.Model, 512) && record.Model != "" {
		attrs = append(attrs, slog.String("model", record.Model))
	}
	if validBoundedText(record.KeyName, 256) && record.KeyName != "" {
		attrs = append(attrs, slog.String("key_name", record.KeyName))
	}
	if record.RequestedMode == RequestModeJSON || record.RequestedMode == RequestModeSSE {
		attrs = append(attrs, slog.String("requested_mode", record.RequestedMode.String()))
	}
	if validResponseMode(record.UpstreamMode) && record.UpstreamMode != ResponseModeUnknown {
		attrs = append(attrs, slog.String("upstream_mode", string(record.UpstreamMode)))
	}
	if validResponseMode(record.DeliveredMode) && record.DeliveredMode != ResponseModeUnknown {
		attrs = append(attrs, slog.String("delivered_mode", string(record.DeliveredMode)))
	}
	if value, known := record.DownstreamStatus.Value(); known {
		attrs = append(attrs, slog.Int64("status", int64(value)), slog.Int64("downstream_status", int64(value)))
	}
	if value, known := record.UpstreamStatus.Value(); known {
		attrs = append(attrs, slog.Int("upstream_status", value))
	}
	if record.Terminal.Outcome != TerminalOutcomeUnknown && validTerminalOutcome(record.Terminal.Outcome) {
		attrs = append(attrs, slog.String("terminal_outcome", string(record.Terminal.Outcome)))
	}
	attrs = append(attrs,
		slog.Bool("upstream_started", record.Terminal.UpstreamStarted),
	)
	if record.ErrorCode != ErrorCodeUnknown && validErrorCode(record.ErrorCode) {
		attrs = append(attrs, slog.String("error_code", record.ErrorCode.String()))
	}
	appendTraceCount := func(name string, count interface{ Value() (int64, bool) }) {
		if value, known := count.Value(); known {
			attrs = append(attrs, slog.Int64(name, value))
		}
	}
	appendTraceCount("client_bytes", record.ClientBytes)
	appendTraceCount("upstream_bytes", record.UpstreamBytes)
	appendTraceCount("delivered_bytes", record.DeliveredBytes)
	appendTraceCount("usage_input", record.Usage.Input())
	appendTraceCount("usage_output", record.Usage.Output())
	appendTraceCount("usage_total", record.Usage.Total())
	appendTraceCount("usage_cached_input", record.Usage.CachedInput())
	appendTraceCount("usage_reasoning_output", record.Usage.ReasoningOutput())
	if value, known := record.Cost.Micros(); known {
		attrs = append(attrs, slog.Int64("cost_micros", value))
	}
	appendTraceCount("total_micros", record.Timing.Total)
	appendTraceCount("time_to_upstream_headers_micros", record.Timing.TimeToUpstreamHeaders)
	appendTraceCount("time_to_first_byte_micros", record.Timing.TimeToFirstByte)
	appendTraceCount("stream_close_delay_micros", record.Timing.StreamCloseDelay)
	return attrs
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
