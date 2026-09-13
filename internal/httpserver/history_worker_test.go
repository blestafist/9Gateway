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
