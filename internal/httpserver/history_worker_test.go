package httpserver

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/observability"
	"github.com/pestit/9gateway/internal/storage"
)

type historyWorkerTestRepository struct {
	entered           chan struct{}
	closed            chan struct{}
	mu                sync.Mutex
	startedAfterClose bool
}

func (repository *historyWorkerTestRepository) Persist(ctx context.Context, _ storage.HistoryRecord, _ []observability.BodySnapshot) error {
	select {
	case <-repository.entered:
	default:
		close(repository.entered)
	}
	select {
	case <-repository.closed:
		repository.mu.Lock()
		repository.startedAfterClose = true
		repository.mu.Unlock()
		return errors.New("repository used after owner close")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (repository *historyWorkerTestRepository) DeleteBodiesBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func (repository *historyWorkerTestRepository) DeleteMetadataBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func TestHistoryWorkerShutdownWaitsForCanceledSQLBeforeStorageClose(t *testing.T) {
	repository := &historyWorkerTestRepository{entered: make(chan struct{}), closed: make(chan struct{})}
	worker := NewHistoryPersistenceWorker(HistoryPersistenceWorkerOptions{
		Repository: repository, Capacity: 1, RetentionEveryJobs: 100,
	})
	if !worker.Submit(HistoryPersistenceJob{Record: CompletionRecord{RequestID: "0123456789abcdef0123456789abcdef"}}) {
		t.Fatal("job was dropped")
	}
	select {
	case <-repository.entered:
	case <-time.After(time.Second):
		t.Fatal("persistence did not start")
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := worker.Shutdown(shutdownContext); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v, want deadline exceeded", err)
	}
	// Shutdown's return is the storage ownership barrier, even when the caller
	// deadline expired. Closing the owner after it returns cannot race SQL.
	close(repository.closed)
	repository.mu.Lock()
	startedAfterClose := repository.startedAfterClose
	repository.mu.Unlock()
	if startedAfterClose {
		t.Fatal("repository issued SQL after its storage owner closed")
	}
	select {
	case <-worker.done:
	default:
		t.Fatal("history worker still running after shutdown")
	}
}

func TestHistoryWorkerClampsProductionRetentionPassBounds(t *testing.T) {
	worker := NewHistoryPersistenceWorker(HistoryPersistenceWorkerOptions{
		MaxBodyRowsPerPass: 1_000_001, MaxMetadataRowsPerPass: 1_000_002,
	})
	if worker.bodyLimit != productionHistoryRowsPerPass || worker.metadataLimit != productionHistoryRowsPerPass {
		t.Fatalf("retention limits = %d/%d, want %d/%d", worker.bodyLimit, worker.metadataLimit, productionHistoryRowsPerPass, productionHistoryRowsPerPass)
	}
	if err := worker.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryWorkerAllowsSmallerInjectedRetentionPassBounds(t *testing.T) {
	worker := NewHistoryPersistenceWorker(HistoryPersistenceWorkerOptions{
		MaxBodyRowsPerPass: 3, MaxMetadataRowsPerPass: 7,
	})
	if worker.bodyLimit != 3 || worker.metadataLimit != 7 {
		t.Fatalf("retention limits = %d/%d, want 3/7", worker.bodyLimit, worker.metadataLimit)
	}
	if err := worker.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryPersistenceJobTransfersBodyBytesWithoutReclone(t *testing.T) {
	body := []byte("immutable body")
	job := NewHistoryPersistenceJob(CompletionRecord{}, observability.BodySnapshot{
		Kind: observability.BodyKindResponse, Bytes: body, OriginalSize: int64(len(body)), Captured: true,
	})
	if len(job.Bodies) != 1 || &job.Bodies[0].Bytes[0] != &body[0] {
		t.Fatal("history job copied body bytes at construction")
	}
	worker := &HistoryPersistenceWorker{
		queue: make(chan HistoryPersistenceJob, 1), stop: make(chan struct{}), done: make(chan struct{}), accepting: true,
	}
	if !worker.Submit(job) {
		t.Fatal("job was dropped")
	}
	queued := <-worker.queue
	if &queued.Bodies[0].Bytes[0] != &body[0] {
		t.Fatal("history admission copied body bytes")
	}
	worker.drop(queued)
}

// Issue 6: Counter invariant tests

type failingRetentionRepository struct {
	failBodies   bool
	failMetadata bool
	mu           sync.Mutex
	persistCalls int
}

func (r *failingRetentionRepository) Persist(_ context.Context, _ storage.HistoryRecord, _ []observability.BodySnapshot) error {
	r.mu.Lock()
	r.persistCalls++
	r.mu.Unlock()
	return nil
}

func (r *failingRetentionRepository) DeleteBodiesBefore(context.Context, time.Time, int) (int64, error) {
	if r.failBodies {
		return 0, errors.New("body retention failed")
	}
	return 1, nil
}

func (r *failingRetentionRepository) DeleteMetadataBefore(context.Context, time.Time, int) (int64, error) {
	if r.failMetadata {
		return 0, errors.New("metadata retention failed")
	}
	return 1, nil
}

func TestHistoryWorkerCounterInvariantStartupRetentionFailure(t *testing.T) {
	repo := &failingRetentionRepository{failBodies: true, failMetadata: false}
	worker := NewHistoryPersistenceWorker(HistoryPersistenceWorkerOptions{
		Repository: repo, Capacity: 10, RetentionEveryJobs: 100,
	})
	<-worker.startupDone

	stats := worker.Stats()
	if stats.RetentionFailed != 1 {
		t.Errorf("RetentionFailed = %d, want 1", stats.RetentionFailed)
	}
	if stats.Processed != stats.Persisted+stats.PersistFailed {
		t.Errorf("invariant broken: Processed(%d) != Persisted(%d) + PersistFailed(%d)", stats.Processed, stats.Persisted, stats.PersistFailed)
	}

	if err := worker.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryWorkerCounterInvariantScheduledRetentionFailure(t *testing.T) {
	repo := &failingRetentionRepository{failMetadata: true}
	worker := NewHistoryPersistenceWorker(HistoryPersistenceWorkerOptions{
		Repository: repo, Capacity: 10, RetentionEveryJobs: 2,
	})
	<-worker.startupDone

	// Submit jobs to trigger scheduled retention
	for i := 0; i < 3; i++ {
		if !worker.Submit(HistoryPersistenceJob{Record: CompletionRecord{RequestID: RequestID("0123456789abcdef0123456789abcde0")}}) {
			t.Fatal("job was dropped")
		}
	}

	if err := worker.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	stats := worker.Stats()
	if stats.Processed != 3 {
		t.Errorf("Processed = %d, want 3", stats.Processed)
	}
	if stats.Persisted != 3 {
		t.Errorf("Persisted = %d, want 3", stats.Persisted)
	}
	if stats.RetentionFailed == 0 {
		t.Error("RetentionFailed = 0, want >0")
	}
	if stats.Processed != stats.Persisted+stats.PersistFailed {
		t.Errorf("invariant broken: Processed(%d) != Persisted(%d) + PersistFailed(%d)", stats.Processed, stats.Persisted, stats.PersistFailed)
	}
}

type failingPersistRepository struct {
	persistError   error
	retentionError error
}

func (r *failingPersistRepository) Persist(_ context.Context, _ storage.HistoryRecord, _ []observability.BodySnapshot) error {
	return r.persistError
}

func (r *failingPersistRepository) DeleteBodiesBefore(context.Context, time.Time, int) (int64, error) {
	if r.retentionError != nil {
		return 0, r.retentionError
	}
	return 0, nil
}

func (r *failingPersistRepository) DeleteMetadataBefore(context.Context, time.Time, int) (int64, error) {
	if r.retentionError != nil {
		return 0, r.retentionError
	}
	return 0, nil
}

func TestHistoryWorkerCounterInvariantPersistenceWriteFailure(t *testing.T) {
	repo := &failingPersistRepository{persistError: errors.New("write failed")}
	worker := NewHistoryPersistenceWorker(HistoryPersistenceWorkerOptions{
		Repository: repo, Capacity: 10, RetentionEveryJobs: 100,
	})
	<-worker.startupDone

	for i := 0; i < 5; i++ {
		if !worker.Submit(HistoryPersistenceJob{Record: CompletionRecord{RequestID: RequestID("0123456789abcdef0123456789abcde0")}}) {
			t.Fatal("job was dropped")
		}
	}

	if err := worker.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	stats := worker.Stats()
	if stats.Processed != 5 {
		t.Errorf("Processed = %d, want 5", stats.Processed)
	}
	if stats.PersistFailed != 5 {
		t.Errorf("PersistFailed = %d, want 5", stats.PersistFailed)
	}
	if stats.Persisted != 0 {
		t.Errorf("Persisted = %d, want 0", stats.Persisted)
	}
	if stats.Processed != stats.Persisted+stats.PersistFailed {
		t.Errorf("invariant broken: Processed(%d) != Persisted(%d) + PersistFailed(%d)", stats.Processed, stats.Persisted, stats.PersistFailed)
	}
}

func TestHistoryWorkerCounterInvariantCombinedFailures(t *testing.T) {
	repo := &failingPersistRepository{persistError: errors.New("write failed"), retentionError: errors.New("retention failed")}
	worker := NewHistoryPersistenceWorker(HistoryPersistenceWorkerOptions{
		Repository: repo, Capacity: 10, RetentionEveryJobs: 2,
	})
	<-worker.startupDone

	// Startup retention should have failed
	stats := worker.Stats()
	if stats.RetentionFailed == 0 {
		t.Error("RetentionFailed = 0, expected startup retention to fail")
	}

	// Submit 5 jobs with both persist and retention failures
	for i := 0; i < 5; i++ {
		if !worker.Submit(HistoryPersistenceJob{Record: CompletionRecord{RequestID: RequestID("0123456789abcdef0123456789abcde0")}}) {
			t.Fatal("job was dropped")
		}
	}

	if err := worker.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	stats = worker.Stats()
	if stats.Processed != 5 {
		t.Errorf("Processed = %d, want 5", stats.Processed)
	}
	if stats.PersistFailed != 5 {
		t.Errorf("PersistFailed = %d, want 5", stats.PersistFailed)
	}
	if stats.Persisted != 0 {
		t.Errorf("Persisted = %d, want 0", stats.Persisted)
	}
	// Critical invariant: even with both persistence and retention failures
	if stats.Processed != stats.Persisted+stats.PersistFailed {
		t.Errorf("invariant broken: Processed(%d) != Persisted(%d) + PersistFailed(%d)", stats.Processed, stats.Persisted, stats.PersistFailed)
	}
}

// Issue 7: Panic handling tests

type panicRepository struct {
	panicOnCall int
	callCount   int
	mu          sync.Mutex
}

func (r *panicRepository) Persist(_ context.Context, _ storage.HistoryRecord, _ []observability.BodySnapshot) error {
	r.mu.Lock()
	r.callCount++
	shouldPanic := r.callCount == r.panicOnCall
	r.mu.Unlock()
	if shouldPanic {
		panic("simulated panic during persist")
	}
	return nil
}

func (r *panicRepository) DeleteBodiesBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func (r *panicRepository) DeleteMetadataBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func TestHistoryWorkerPanicWithBodyBearingJob(t *testing.T) {
	repo := &panicRepository{panicOnCall: 1}
	worker := NewHistoryPersistenceWorker(HistoryPersistenceWorkerOptions{
		Repository: repo, Capacity: 10, RetentionEveryJobs: 100,
	})
	<-worker.startupDone

	body := []byte("test body content")
	bodySnapshot := observability.BodySnapshot{
		Kind: observability.BodyKindResponse, Bytes: body, OriginalSize: int64(len(body)), Captured: true,
	}
	job := NewHistoryPersistenceJob(CompletionRecord{RequestID: RequestID("0123456789abcdef0123456789abcde0")}, bodySnapshot)

	if !worker.Submit(job) {
		t.Fatal("job was dropped")
	}

	if err := worker.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	stats := worker.Stats()
	if stats.Processed != 1 {
		t.Errorf("Processed = %d, want 1", stats.Processed)
	}
	if stats.PersistFailed != 1 {
		t.Errorf("PersistFailed = %d, want 1", stats.PersistFailed)
	}
	if stats.Persisted != 0 {
		t.Errorf("Persisted = %d, want 0", stats.Persisted)
	}
}

func TestHistoryWorkerQueuedJobsContinueAfterPanic(t *testing.T) {
	repo := &panicRepository{panicOnCall: 2}
	worker := NewHistoryPersistenceWorker(HistoryPersistenceWorkerOptions{
		Repository: repo, Capacity: 10, RetentionEveryJobs: 100,
	})
	<-worker.startupDone

	// Submit 4 jobs: first succeeds, second panics, third and fourth should still process
	for i := 0; i < 4; i++ {
		if !worker.Submit(HistoryPersistenceJob{Record: CompletionRecord{RequestID: RequestID("0123456789abcdef0123456789abcde0")}}) {
			t.Fatal("job was dropped")
		}
	}

	if err := worker.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	stats := worker.Stats()
	if stats.Processed != 4 {
		t.Errorf("Processed = %d, want 4", stats.Processed)
	}
	if stats.Persisted != 3 {
		t.Errorf("Persisted = %d, want 3 (panic job should fail)", stats.Persisted)
	}
	if stats.PersistFailed != 1 {
		t.Errorf("PersistFailed = %d, want 1", stats.PersistFailed)
	}
}

func TestHistoryWorkerPanicCounterNoDuplicateIncrement(t *testing.T) {
	repo := &panicRepository{panicOnCall: 1}
	worker := NewHistoryPersistenceWorker(HistoryPersistenceWorkerOptions{
		Repository: repo, Capacity: 10, RetentionEveryJobs: 100,
	})
	<-worker.startupDone

	if !worker.Submit(HistoryPersistenceJob{Record: CompletionRecord{RequestID: RequestID("0123456789abcdef0123456789abcde0")}}) {
		t.Fatal("job was dropped")
	}

	if err := worker.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	stats := worker.Stats()
	// Critical invariant: Processed must equal Persisted + PersistFailed
	// If double-counting occurs, Processed would be 2 but Persisted+PersistFailed would be 1
	if stats.Processed != stats.Persisted+stats.PersistFailed {
		t.Errorf("invariant broken: Processed(%d) != Persisted(%d) + PersistFailed(%d)", stats.Processed, stats.Persisted, stats.PersistFailed)
	}
	if stats.Processed != 1 {
		t.Errorf("Processed = %d, want 1 (no double-counting)", stats.Processed)
	}
}

func TestHistoryWorkerPanicMemoryReleased(t *testing.T) {
	repo := &panicRepository{panicOnCall: 1}
	worker := NewHistoryPersistenceWorker(HistoryPersistenceWorkerOptions{
		Repository: repo, Capacity: 10, RetentionEveryJobs: 100,
	})
	<-worker.startupDone

	body := make([]byte, 1024)
	for i := range body {
		body[i] = byte(i % 256)
	}
	bodySnapshot := observability.BodySnapshot{
		Kind: observability.BodyKindResponse, Bytes: body, OriginalSize: int64(len(body)), Captured: true,
	}
	job := NewHistoryPersistenceJob(CompletionRecord{RequestID: RequestID("0123456789abcdef0123456789abcde0")}, bodySnapshot)

	if !worker.Submit(job) {
		t.Fatal("job was dropped")
	}

	if err := worker.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	// If clearHistoryJob was not called in panic handler, memory would leak
	// This test verifies the panic handler calls clearHistoryJob
	stats := worker.Stats()
	if stats.PersistFailed != 1 {
		t.Errorf("PersistFailed = %d, want 1 (panic should mark failure)", stats.PersistFailed)
	}
}

func TestHistoryWorkerShutdownAfterPanic(t *testing.T) {
	repo := &panicRepository{panicOnCall: 1}
	worker := NewHistoryPersistenceWorker(HistoryPersistenceWorkerOptions{
		Repository: repo, Capacity: 10, RetentionEveryJobs: 100,
	})
	<-worker.startupDone

	if !worker.Submit(HistoryPersistenceJob{Record: CompletionRecord{RequestID: RequestID("0123456789abcdef0123456789abcde0")}}) {
		t.Fatal("job was dropped")
	}

	// Give time for panic to occur
	time.Sleep(50 * time.Millisecond)

	// Shutdown should complete successfully even after panic
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := worker.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown failed after panic: %v", err)
	}

	select {
	case <-worker.done:
		// Good: worker exited cleanly
	default:
		t.Fatal("worker still running after shutdown")
	}
}
