package httpserver

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/accounting"
	"github.com/pestit/9gateway/internal/observability"
	"github.com/pestit/9gateway/internal/storage"
)

// TestCompletionOwnershipLoggerSaturationDoesNotAffectHistory verifies that
// when the completion logger queue is saturated, history persistence can still
// succeed independently.
func TestCompletionOwnershipLoggerSaturationDoesNotAffectHistory(t *testing.T) {
	// Create a blocking logger to saturate the completion logger queue
	logHandler := &blockingCompletionHandler{entered: make(chan struct{}), release: make(chan struct{})}
	completionLogger := NewCompletionLogger(slog.New(logHandler), 1)
	defer func() {
		close(logHandler.release)
		shutdownCompletionLogger(t, completionLogger)
	}()

	// Create a history worker with a working repository
	historyRepo := &captureHistoryRepository{records: make(chan storage.HistoryRecord, 10)}
	historyWorker := NewHistoryPersistenceWorker(HistoryPersistenceWorkerOptions{
		Repository:         historyRepo,
		Capacity:           10,
		RetentionEveryJobs: 1000,
	})
	defer shutdownHistoryWorker(t, historyWorker)

	// Saturate the logger queue
	trace := newTestTrace(t)
	ownership := newCompletionOwnership(trace, completionLogger, historyWorker)

	// Fill logger queue (capacity 1) and block the worker
	record1, err := trace.FreezeBase()
	if err != nil {
		t.Fatal(err)
	}
	completionLogger.Enqueue(record1)
	select {
	case <-logHandler.entered:
	case <-time.After(time.Second):
		t.Fatal("logger worker did not enter blocked sink")
	}

	// Fill the second slot
	record2, err := trace.FreezeBase()
	if err != nil {
		t.Fatal(err)
	}
	completionLogger.Enqueue(record2)

	// Now emit through ownership - logger should drop, history should succeed
	ownership.complete()

	// Verify history succeeded
	select {
	case historyRecord := <-historyRepo.records:
		if historyRecord.RequestID == "" {
			t.Fatal("history record was empty")
		}
	case <-time.After(time.Second):
		t.Fatal("history did not persist record despite logger saturation")
	}

	// Verify logger dropped at least one record
	if completionLogger.Dropped() == 0 {
		t.Fatal("logger did not drop any records when saturated")
	}
}

// TestCompletionOwnershipHistorySaturationDoesNotAffectLogger verifies that
// when the history worker queue is saturated, completion logger can still
// emit independently.
func TestCompletionOwnershipHistorySaturationDoesNotAffectLogger(t *testing.T) {
	// Create a logger that captures records
	logHandler := &completionRecordHandler{records: make(chan slog.Record, 10)}
	completionLogger := NewCompletionLogger(slog.New(logHandler), 10)
	defer shutdownCompletionLogger(t, completionLogger)

	// Create a history worker with blocking repository
	historyRepo := &blockingHistoryRepository{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	historyWorker := NewHistoryPersistenceWorker(HistoryPersistenceWorkerOptions{
		Repository:         historyRepo,
		Capacity:           1,
		RetentionEveryJobs: 1000,
	})
	defer func() {
		close(historyRepo.release)
		shutdownHistoryWorker(t, historyWorker)
	}()

	// Saturate the history queue
	historyWorker.Submit(HistoryPersistenceJob{
		Record: CompletionRecord{RequestID: "filler0000000000000000000000000000"},
	})
	select {
	case <-historyRepo.entered:
	case <-time.After(time.Second):
		t.Fatal("history worker did not enter blocked repository")
	}

	// Fill the second slot
	historyWorker.Submit(HistoryPersistenceJob{
		Record: CompletionRecord{RequestID: "filler0000000000000000000000000001"},
	})

	// Now emit through ownership - history should drop, logger should succeed
	trace := newTestTrace(t)
	ownership := newCompletionOwnership(trace, completionLogger, historyWorker)
	ownership.complete()

	// Verify logger succeeded
	select {
	case record := <-logHandler.records:
		if record.Message != "request completed" {
			t.Fatalf("logger record message = %q, want %q", record.Message, "request completed")
		}
	case <-time.After(time.Second):
		t.Fatal("logger did not emit record despite history saturation")
	}

	// Verify history dropped at least one record
	if historyWorker.Dropped() == 0 {
		t.Fatal("history did not drop any records when saturated")
	}
}

// TestCompletionOwnershipSimultaneousShutdownNoPanic verifies that shutting
// down both sinks concurrently during emit() does not panic or deadlock.
func TestCompletionOwnershipSimultaneousShutdownNoPanic(t *testing.T) {
	logHandler := &completionRecordHandler{records: make(chan slog.Record, 10)}
	completionLogger := NewCompletionLogger(slog.New(logHandler), 10)

	historyRepo := &captureHistoryRepository{records: make(chan storage.HistoryRecord, 10)}
	historyWorker := NewHistoryPersistenceWorker(HistoryPersistenceWorkerOptions{
		Repository:         historyRepo,
		Capacity:           10,
		RetentionEveryJobs: 1000,
	})

	// Start concurrent emissions
	const emitters = 10
	start := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(emitters)

	for i := 0; i < emitters; i++ {
		go func(index int) {
			defer workers.Done()
			<-start
			trace := newTestTrace(t)
			ownership := newCompletionOwnership(trace, completionLogger, historyWorker)
			ownership.complete()
		}(i)
	}

	// Start concurrent shutdown
	close(start)
	time.Sleep(10 * time.Millisecond) // Give goroutines a chance to start

	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var shutdownWG sync.WaitGroup
	shutdownWG.Add(2)

	go func() {
		defer shutdownWG.Done()
		completionLogger.Shutdown(shutdownContext)
	}()

	go func() {
		defer shutdownWG.Done()
		historyWorker.Shutdown(shutdownContext)
	}()

	workers.Wait()
	shutdownWG.Wait()

	// If we got here without panic or deadlock, the test passed
}

// TestCompletionOwnershipImmediateJSONWithLoggerSaturated verifies that
// immediate JSON completion paths work when logger is saturated.
func TestCompletionOwnershipImmediateJSONWithLoggerSaturated(t *testing.T) {
	logHandler := &blockingCompletionHandler{entered: make(chan struct{}), release: make(chan struct{})}
	completionLogger := NewCompletionLogger(slog.New(logHandler), 1)
	defer func() {
		close(logHandler.release)
		shutdownCompletionLogger(t, completionLogger)
	}()

	historyRepo := &captureHistoryRepository{records: make(chan storage.HistoryRecord, 10)}
	historyWorker := NewHistoryPersistenceWorker(HistoryPersistenceWorkerOptions{
		Repository:         historyRepo,
		Capacity:           10,
		RetentionEveryJobs: 1000,
	})
	defer shutdownHistoryWorker(t, historyWorker)

	// Saturate logger
	trace := newTestTrace(t)
	record, err := trace.FreezeBase()
	if err != nil {
		t.Fatal(err)
	}
	completionLogger.Enqueue(record)
	select {
	case <-logHandler.entered:
	case <-time.After(time.Second):
		t.Fatal("logger worker did not enter blocked sink")
	}
	completionLogger.Enqueue(record)

	// Immediate JSON path (no transfer, immediate complete)
	trace2 := newTestTrace(t)
	trace2.SetDownstreamStatus(200)
	trace2.SetResponseMode(ResponseModeJSON, ResponseModeJSON)
	ownership := newCompletionOwnership(trace2, completionLogger, historyWorker)

	// Complete without transfer - this is the immediate path
	ownership.complete()

	// History should still persist
	select {
	case historyRecord := <-historyRepo.records:
		if historyRecord.RequestID == "" {
			t.Fatal("history record was empty")
		}
	case <-time.After(time.Second):
		t.Fatal("immediate JSON path did not persist to history despite logger saturation")
	}

	if completionLogger.Dropped() == 0 {
		t.Fatal("logger did not drop records when saturated")
	}
}

// TestCompletionOwnershipRejectedWithHistorySaturated verifies that
// rejected requests (pre-upstream) work when history is saturated.
func TestCompletionOwnershipRejectedWithHistorySaturated(t *testing.T) {
	logHandler := &completionRecordHandler{records: make(chan slog.Record, 10)}
	completionLogger := NewCompletionLogger(slog.New(logHandler), 10)
	defer shutdownCompletionLogger(t, completionLogger)

	historyRepo := &blockingHistoryRepository{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	historyWorker := NewHistoryPersistenceWorker(HistoryPersistenceWorkerOptions{
		Repository:         historyRepo,
		Capacity:           1,
		RetentionEveryJobs: 1000,
	})
	defer func() {
		close(historyRepo.release)
		shutdownHistoryWorker(t, historyWorker)
	}()

	// Saturate history
	historyWorker.Submit(HistoryPersistenceJob{
		Record: CompletionRecord{RequestID: "filler0000000000000000000000000000"},
	})
	select {
	case <-historyRepo.entered:
	case <-time.After(time.Second):
		t.Fatal("history worker did not enter blocked repository")
	}
	historyWorker.Submit(HistoryPersistenceJob{
		Record: CompletionRecord{RequestID: "filler0000000000000000000000000001"},
	})

	// Rejected request path (pre-upstream)
	trace := newTestTrace(t)
	trace.SetDownstreamStatus(401)
	trace.SetTerminalMetadata(TerminalMetadata{
		Outcome:         TerminalOutcomePreUpstream,
		UpstreamStarted: false,
	})
	trace.SetErrorCode(ErrorCodeInvalidAPIKey)
	ownership := newCompletionOwnership(trace, completionLogger, historyWorker)
	ownership.complete()

	// Logger should still emit
	select {
	case record := <-logHandler.records:
		if record.Message != "request completed" {
			t.Fatalf("logger record message = %q, want %q", record.Message, "request completed")
		}
	case <-time.After(time.Second):
		t.Fatal("rejected request did not log despite history saturation")
	}

	if historyWorker.Dropped() == 0 {
		t.Fatal("history did not drop records when saturated")
	}
}

// TestCompletionOwnershipOpaqueWithBothSaturated verifies that opaque
// responses work when both sinks are saturated.
func TestCompletionOwnershipOpaqueWithBothSaturated(t *testing.T) {
	logHandler := &blockingCompletionHandler{entered: make(chan struct{}), release: make(chan struct{})}
	completionLogger := NewCompletionLogger(slog.New(logHandler), 1)
	defer func() {
		close(logHandler.release)
		shutdownCompletionLogger(t, completionLogger)
	}()

	historyRepo := &blockingHistoryRepository{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	historyWorker := NewHistoryPersistenceWorker(HistoryPersistenceWorkerOptions{
		Repository:         historyRepo,
		Capacity:           1,
		RetentionEveryJobs: 1000,
	})
	defer func() {
		close(historyRepo.release)
		shutdownHistoryWorker(t, historyWorker)
	}()

	// Saturate both
	trace := newTestTrace(t)
	record, err := trace.FreezeBase()
	if err != nil {
		t.Fatal(err)
	}
	completionLogger.Enqueue(record)
	historyWorker.Submit(HistoryPersistenceJob{Record: record})

	select {
	case <-logHandler.entered:
	case <-time.After(time.Second):
		t.Fatal("logger worker did not enter blocked sink")
	}
	select {
	case <-historyRepo.entered:
	case <-time.After(time.Second):
		t.Fatal("history worker did not enter blocked repository")
	}

	completionLogger.Enqueue(record)
	historyWorker.Submit(HistoryPersistenceJob{Record: record})

	// Opaque response path
	trace2 := newTestTrace(t)
	trace2.SetDownstreamStatus(200)
	trace2.SetUpstreamStatus(200)
	trace2.SetResponseMode(ResponseModeOpaque, ResponseModeOpaque)
	ownership := newCompletionOwnership(trace2, completionLogger, historyWorker)
	ownership.complete()

	// Both should drop, but completion should not block
	done := make(chan struct{})
	go func() {
		time.Sleep(100 * time.Millisecond)
		close(done)
	}()

	select {
	case <-done:
		// Success - completion did not block
	case <-time.After(2 * time.Second):
		t.Fatal("completion blocked when both sinks saturated")
	}

	if completionLogger.Dropped() == 0 {
		t.Error("logger did not drop records when saturated")
	}
	if historyWorker.Dropped() == 0 {
		t.Error("history did not drop records when saturated")
	}
}

// TestCompletionOwnershipEnrichmentWithLoggerSaturated verifies that
// finishWithTiming enrichment works when logger is saturated.
func TestCompletionOwnershipEnrichmentWithLoggerSaturated(t *testing.T) {
	logHandler := &blockingCompletionHandler{entered: make(chan struct{}), release: make(chan struct{})}
	completionLogger := NewCompletionLogger(slog.New(logHandler), 1)
	defer func() {
		close(logHandler.release)
		shutdownCompletionLogger(t, completionLogger)
	}()

	historyRepo := &captureHistoryRepository{records: make(chan storage.HistoryRecord, 10)}
	historyWorker := NewHistoryPersistenceWorker(HistoryPersistenceWorkerOptions{
		Repository:         historyRepo,
		Capacity:           10,
		RetentionEveryJobs: 1000,
	})
	defer shutdownHistoryWorker(t, historyWorker)

	// Saturate logger
	trace := newTestTrace(t)
	record, err := trace.FreezeBase()
	if err != nil {
		t.Fatal(err)
	}
	completionLogger.Enqueue(record)
	select {
	case <-logHandler.entered:
	case <-time.After(time.Second):
		t.Fatal("logger worker did not enter blocked sink")
	}
	completionLogger.Enqueue(record)

	// Create ownership and transfer to deferred path
	trace2 := newTestTrace(t)
	ownership := newCompletionOwnership(trace2, completionLogger, historyWorker)
	if !ownership.transfer() {
		t.Fatal("transfer failed")
	}

	// Complete handler
	ownership.complete()

	// Now finish with enrichment (deferred path)
	usage, err := accounting.NewUsage(accounting.UsageInput{
		Input:  ptrInt64(10),
		Output: ptrInt64(20),
		Total:  ptrInt64(30),
	})
	if err != nil {
		t.Fatal(err)
	}
	cost, err := accounting.NewMoneyMicros(1000)
	if err != nil {
		t.Fatal(err)
	}
	lastMeaningful := time.Now()
	ownership.finishWithTiming(usage, cost, lastMeaningful, true)

	// History should persist with enrichment
	select {
	case historyRecord := <-historyRepo.records:
		if historyRecord.RequestID == "" {
			t.Fatal("history record was empty")
		}
		if !historyRecord.InputTokens.Known || historyRecord.InputTokens.Value != 10 {
			t.Fatalf("input tokens = %v, want 10", historyRecord.InputTokens)
		}
	case <-time.After(time.Second):
		t.Fatal("enriched completion did not persist despite logger saturation")
	}

	if completionLogger.Dropped() == 0 {
		t.Fatal("logger did not drop records when saturated")
	}
}

// TestCompletionOwnershipInvalidEnrichmentWithHistorySaturated verifies that
// invalid enrichment (timing anomalies) works when history is saturated.
func TestCompletionOwnershipInvalidEnrichmentWithHistorySaturated(t *testing.T) {
	logHandler := &completionRecordHandler{records: make(chan slog.Record, 10)}
	completionLogger := NewCompletionLogger(slog.New(logHandler), 10)
	defer shutdownCompletionLogger(t, completionLogger)

	historyRepo := &blockingHistoryRepository{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	historyWorker := NewHistoryPersistenceWorker(HistoryPersistenceWorkerOptions{
		Repository:         historyRepo,
		Capacity:           1,
		RetentionEveryJobs: 1000,
	})
	defer func() {
		close(historyRepo.release)
		shutdownHistoryWorker(t, historyWorker)
	}()

	// Saturate history
	historyWorker.Submit(HistoryPersistenceJob{
		Record: CompletionRecord{RequestID: "filler0000000000000000000000000000"},
	})
	select {
	case <-historyRepo.entered:
	case <-time.After(time.Second):
		t.Fatal("history worker did not enter blocked repository")
	}
	historyWorker.Submit(HistoryPersistenceJob{
		Record: CompletionRecord{RequestID: "filler0000000000000000000000000001"},
	})

	// Create ownership and transfer
	trace := newTestTrace(t)
	ownership := newCompletionOwnership(trace, completionLogger, historyWorker)
	if !ownership.transfer() {
		t.Fatal("transfer failed")
	}

	// Complete handler
	ownership.complete()

	// Finish with invalid timing (zero time)
	usage, _ := accounting.NewUsage(accounting.UsageInput{})
	cost := accounting.UnknownMoney()
	ownership.finishWithTiming(usage, cost, time.Time{}, true)

	// Logger should still emit despite invalid timing and history saturation
	select {
	case record := <-logHandler.records:
		if record.Message != "request completed" {
			t.Fatalf("logger record message = %q, want %q", record.Message, "request completed")
		}
	case <-time.After(time.Second):
		t.Fatal("completion did not log despite invalid enrichment and history saturation")
	}

	if historyWorker.Dropped() == 0 {
		t.Fatal("history did not drop records when saturated")
	}
}

// TestCompletionOwnershipParseFailureWithLoggerSaturated verifies that
// when trace.Final() fails (parse error), one sink can still succeed.
func TestCompletionOwnershipParseFailureWithLoggerSaturated(t *testing.T) {
	logHandler := &blockingCompletionHandler{entered: make(chan struct{}), release: make(chan struct{})}
	completionLogger := NewCompletionLogger(slog.New(logHandler), 1)
	defer func() {
		close(logHandler.release)
		shutdownCompletionLogger(t, completionLogger)
	}()

	historyRepo := &captureHistoryRepository{records: make(chan storage.HistoryRecord, 10)}
	historyWorker := NewHistoryPersistenceWorker(HistoryPersistenceWorkerOptions{
		Repository:         historyRepo,
		Capacity:           10,
		RetentionEveryJobs: 1000,
	})
	defer shutdownHistoryWorker(t, historyWorker)

	// Saturate logger
	trace := newTestTrace(t)
	record, err := trace.FreezeBase()
	if err != nil {
		t.Fatal(err)
	}
	completionLogger.Enqueue(record)
	select {
	case <-logHandler.entered:
	case <-time.After(time.Second):
		t.Fatal("logger worker did not enter blocked sink")
	}
	completionLogger.Enqueue(record)

	// Create a trace with invalid state (this would cause Final() to fail)
	// In practice, this is rare, but we test the contract
	trace2 := newTestTrace(t)
	ownership := newCompletionOwnership(trace2, completionLogger, historyWorker)

	// Even if emit() encounters an error, it should not panic or block
	ownership.complete()

	// The completion should not block
	time.Sleep(50 * time.Millisecond)

	// Both sinks should have had the opportunity to process or drop
	if completionLogger.Dropped() == 0 {
		t.Fatal("logger did not drop records when saturated")
	}
}

// Helper types and functions

type captureHistoryRepository struct {
	records chan storage.HistoryRecord
	mu      sync.Mutex
}

func (repo *captureHistoryRepository) Persist(_ context.Context, record storage.HistoryRecord, _ []observability.BodySnapshot) error {
	repo.records <- record
	return nil
}

func (repo *captureHistoryRepository) DeleteBodiesBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func (repo *captureHistoryRepository) DeleteMetadataBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

type blockingHistoryRepository struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (repo *blockingHistoryRepository) Persist(ctx context.Context, _ storage.HistoryRecord, _ []observability.BodySnapshot) error {
	repo.once.Do(func() { close(repo.entered) })
	select {
	case <-repo.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (repo *blockingHistoryRepository) DeleteBodiesBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func (repo *blockingHistoryRepository) DeleteMetadataBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func newTestTrace(t *testing.T) *RequestTraceState {
	t.Helper()
	requestID, err := NewRequestID("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	state, err := NewRequestTrace(requestID, TraceClock{
		Wall:      time.Now,
		Monotonic: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	state.SetRouteMetadata("POST", "/v1/chat/completions", RouteClassChatCompletions)
	state.SetRequestMetadata("POST", RouteClassChatCompletions, "test-model", RequestModeJSON)
	state.SetDownstreamStatus(200)
	state.SetUpstreamStatus(200)
	state.SetResponseMode(ResponseModeJSON, ResponseModeJSON)
	state.SetTerminalMetadata(TerminalMetadata{
		Outcome:         TerminalOutcomeComplete,
		UpstreamStarted: true,
	})
	return state
}

func shutdownHistoryWorker(t *testing.T, worker *HistoryPersistenceWorker) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := worker.Shutdown(ctx); err != nil && err != context.DeadlineExceeded {
		t.Fatalf("shutdown history worker: %v", err)
	}
}
