package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/httpserver"
	"github.com/pestit/9gateway/internal/observability"
	"github.com/pestit/9gateway/internal/storage"
)

type blockedHistoryRepository struct {
	entered chan struct{}
	release chan struct{}
}

func (repository *blockedHistoryRepository) Persist(context.Context, storage.HistoryRecord, []observability.BodySnapshot) error {
	close(repository.entered)
	<-repository.release
	return nil
}

func (repository *blockedHistoryRepository) DeleteBodiesBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func (repository *blockedHistoryRepository) DeleteMetadataBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func TestShutdownGatewayLeavesDatabaseOpenWhenHistoryOwnerMissesDeadline(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	repository := &blockedHistoryRepository{entered: make(chan struct{}), release: make(chan struct{})}
	history := httpserver.NewHistoryPersistenceWorker(httpserver.HistoryPersistenceWorkerOptions{Repository: repository, Capacity: 1, RetentionEveryJobs: 100})
	if !history.Submit(httpserver.HistoryPersistenceJob{Record: httpserver.CompletionRecord{RequestID: "0123456789abcdef0123456789abcdef"}}) {
		t.Fatal("history job was dropped")
	}
	select {
	case <-repository.entered:
	case <-time.After(time.Second):
		t.Fatal("blocked repository was not entered")
	}
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	result := shutdownGateway(1, server.Config, database, nil, nil, nil, nil, nil, nil, history)
	close(repository.release)
	select {
	case <-history.Done():
	case <-time.After(time.Second):
		t.Fatal("history worker did not stop after repository release")
	}
	if err := database.PingContext(context.Background()); err != nil {
		t.Fatalf("database was closed before history owner stopped: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(result, errShutdownDeadline) {
		t.Fatalf("shutdown error = %v, want shutdown deadline", result)
	}
}
