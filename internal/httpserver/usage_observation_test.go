package httpserver

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/limiter"
)

func TestUsageObservationParsesBoundedJSONAndGzip(t *testing.T) {
	clock := &requestLimitTestClock{now: time.Unix(30, 0).UTC()}
	tokens := limiter.NewTokenLimiter(clock.Now)
	reservation, admitted, _ := tokens.Reserve("key", []limiter.TokenWindow{{Amount: 100, Duration: time.Minute}}, 40)
	if !admitted {
		t.Fatal("reservation rejected")
	}
	ticket, err := reservation.CommitDeferred()
	if err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, _ = io.WriteString(writer, `{"usage":{"prompt_tokens":4,"completion_tokens":6}}`)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	worker := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: 1})
	if !worker.Submit(NewUsageObservationJob(compressed.Bytes(), ContentCodingGZIP, ticket)) {
		t.Fatal("gzip observation was dropped")
	}
	waitForObservationCount(t, worker, 1)
	shutdownObservationWorker(t, worker)
	if got := bucketForObservationTest(t, tokens, "key"); got != 10 {
		t.Fatalf("observed committed usage = %d, want 10", got)
	}
	if stats := worker.Stats(); stats.Succeeded != 1 || stats.Failed != 0 {
		t.Fatalf("parser stats = %+v", stats)
	}
}

func TestUsageObservationParseFailureRetainsConservativeCharge(t *testing.T) {
	clock := &requestLimitTestClock{now: time.Unix(30, 0).UTC()}
	tokens := limiter.NewTokenLimiter(clock.Now)
	reservation, admitted, _ := tokens.Reserve("key", []limiter.TokenWindow{{Amount: 100, Duration: time.Minute}}, 40)
	if !admitted {
		t.Fatal("reservation rejected")
	}
	ticket, err := reservation.CommitDeferred()
	if err != nil {
		t.Fatal(err)
	}
	worker := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: 1})
	if !worker.Submit(NewUsageObservationJob([]byte(`{"usage":{"total_tokens":-1}}`), ContentCodingIdentity, ticket)) {
		t.Fatal("invalid observation was dropped before worker")
	}
	waitForObservationCount(t, worker, 1)
	shutdownObservationWorker(t, worker)
	if got := bucketForObservationTest(t, tokens, "key"); got != 40 {
		t.Fatalf("failed observation charge = %d, want conservative 40", got)
	}
	if got := worker.Failed(); got != 1 {
		t.Fatalf("failed observations = %d, want 1", got)
	}
}

func TestUsageObservationSaturationRetainsConservativeCharge(t *testing.T) {
	clock := &requestLimitTestClock{now: time.Unix(30, 0).UTC()}
	tokens := limiter.NewTokenLimiter(clock.Now)
	concurrency := limiter.NewConcurrencyLimiter()
	coordinator := limiter.NewResourceLeaseCoordinator(concurrency, tokens)
	windows := []limiter.TokenWindow{{Amount: 100, Duration: time.Minute}}
	blocked := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	worker := NewUsageObservationWorker(UsageObservationWorkerOptions{
		Capacity: 1,
		Parse: func([]byte, ContentCoding) (int64, error) {
			once.Do(func() { close(blocked) })
			<-release
			return 10, nil
		},
	})

	first := mustObservationLease(t, coordinator, windows, 40)
	if !worker.CompleteAndSubmit(first, []byte("first"), ContentCodingIdentity) {
		t.Fatal("first observation was dropped")
	}
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("worker did not block")
	}
	if got := concurrency.Len(); got != 0 {
		t.Fatalf("concurrency after handoff = %d, want 0", got)
	}
	second := mustObservationLease(t, coordinator, windows, 20)
	if !worker.CompleteAndSubmit(second, []byte("second"), ContentCodingIdentity) {
		t.Fatal("second observation was dropped instead of filling queue")
	}
	third := mustObservationLease(t, coordinator, windows, 1)
	if worker.CompleteAndSubmit(third, []byte("third"), ContentCodingIdentity) {
		t.Fatal("saturated observation was accepted")
	}
	if _, admitted, _ := tokens.Reserve("key", windows, 61); admitted {
		t.Fatal("dropped observation did not retain conservative charge")
	}
	if got := worker.Dropped(); got != 1 {
		t.Fatalf("dropped observations = %d, want one saturated drop", got)
	}

	close(release)
	shutdownObservationWorker(t, worker)
	if got := concurrency.Len(); got != 0 {
		t.Fatalf("concurrency after worker shutdown = %d", got)
	}
	if got := worker.Stats(); got.Succeeded != 1 || got.Dropped != 2 {
		t.Fatalf("worker stats = %+v, want one success and two drops", got)
	}
}

func TestUsageObservationShutdownDiscardsQueuedTicketsAndIsIdempotent(t *testing.T) {
	clock := &requestLimitTestClock{now: time.Unix(30, 0).UTC()}
	tokens := limiter.NewTokenLimiter(clock.Now)
	coordinator := limiter.NewResourceLeaseCoordinator(nil, tokens)
	entered := make(chan struct{})
	release := make(chan struct{})
	worker := NewUsageObservationWorker(UsageObservationWorkerOptions{
		Capacity: 1,
		Parse: func([]byte, ContentCoding) (int64, error) {
			close(entered)
			<-release
			return 10, nil
		},
	})
	first := mustObservationLease(t, coordinator, []limiter.TokenWindow{{Amount: 100, Duration: time.Minute}}, 40)
	if !worker.CompleteAndSubmit(first, []byte("first"), ContentCodingIdentity) {
		t.Fatal("first observation dropped")
	}
	<-entered
	second := mustObservationLease(t, coordinator, []limiter.TokenWindow{{Amount: 100, Duration: time.Minute}}, 20)
	if !worker.CompleteAndSubmit(second, []byte("second"), ContentCodingIdentity) {
		t.Fatal("queued observation dropped")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	if err := worker.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked shutdown error = %v, want deadline exceeded", err)
	}
	// Shutdown invalidates queued tickets at its boundary, even while the
	// already-claimed parser remains blocked. If the queued ticket were allowed
	// to adjust after release, 41 more tokens would fit in this 100-token window.
	if _, admitted, _ := tokens.Reserve("key", []limiter.TokenWindow{{Amount: 100, Duration: time.Minute}}, 41); admitted {
		t.Fatal("queued shutdown ticket remained adjustable after shutdown boundary")
	}
	cancel()
	close(release)
	shutdownObservationWorker(t, worker)
	if err := worker.Shutdown(context.Background()); err != nil {
		t.Fatalf("repeated shutdown = %v", err)
	}
	if got := worker.Stats(); got.Dropped != 1 {
		t.Fatalf("shutdown dropped = %d, want one queued drop", got.Dropped)
	}
	if _, admitted, _ := tokens.Reserve("key", []limiter.TokenWindow{{Amount: 100, Duration: time.Minute}}, 71); admitted {
		t.Fatal("queued shutdown ticket did not retain conservative charge")
	}
}

func TestUsageObservationAcceptedJobOwnsOneImmutableCopy(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	observed := make(chan byte, 1)
	worker := NewUsageObservationWorker(UsageObservationWorkerOptions{
		Capacity: 1,
		Parse: func(data []byte, _ ContentCoding) (int64, error) {
			close(started)
			<-release
			observed <- data[0]
			return 1, nil
		},
	})
	defer shutdownObservationWorker(t, worker)
	clock := &requestLimitTestClock{now: time.Unix(30, 0).UTC()}
	tokens := limiter.NewTokenLimiter(clock.Now)
	lease := mustObservationLease(t, limiter.NewResourceLeaseCoordinator(nil, tokens), []limiter.TokenWindow{{Amount: 10, Duration: time.Minute}}, 1)
	captured := []byte("original")
	if !worker.CompleteAndSubmit(lease, captured, ContentCodingIdentity) {
		t.Fatal("observation was dropped")
	}
	<-started
	captured[0] = 'm'
	close(release)
	if got := <-observed; got != 'o' {
		t.Fatalf("parser saw caller mutation %q, want immutable copy", got)
	}
}

func TestUsageObservationJobBoundsAndValidatesCoding(t *testing.T) {
	if _, err := ValidateContentCoding("br"); !errors.Is(err, ErrUsageObservationUnsupported) {
		t.Fatalf("unsupported coding error = %v", err)
	}
	job := NewUsageObservationJob(make([]byte, DefaultUsageObservationMaxBytes+1), ContentCodingIdentity, nil)
	if int64(len(job.Bytes)) != DefaultUsageObservationMaxBytes {
		t.Fatalf("job bytes = %d, want bounded %d", len(job.Bytes), DefaultUsageObservationMaxBytes)
	}
}

func mustObservationLease(t *testing.T, coordinator *limiter.ResourceLeaseCoordinator, windows []limiter.TokenWindow, amount int64) *limiter.ResourceLease {
	t.Helper()
	lease, rejection := coordinator.Acquire(limiter.ResourceLeaseOptions{KeyID: "key", TokenWindows: windows, TokenAmount: amount})
	if rejection != nil || lease == nil {
		t.Fatalf("acquire observation lease = (%v, %v)", lease, rejection)
	}
	return lease
}

func shutdownObservationWorker(t *testing.T, worker *UsageObservationWorker) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := worker.Shutdown(ctx); err != nil {
		t.Fatalf("worker shutdown = %v", err)
	}
}

func waitForObservationCount(t *testing.T, worker *UsageObservationWorker, count uint64) {
	t.Helper()
	deadline := time.After(time.Second)
	for worker.Stats().Processed < count {
		select {
		case <-deadline:
			t.Fatalf("worker processed %d jobs, want %d", worker.Stats().Processed, count)
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func bucketForObservationTest(t *testing.T, tokens *limiter.TokenLimiter, key string) int64 {
	t.Helper()
	// A fresh reservation reveals the currently committed amount without
	// exposing limiter internals to this package's tests.
	for amount := int64(100); amount > 0; amount-- {
		candidate, ok, _ := tokens.Reserve(key, []limiter.TokenWindow{{Amount: 100, Duration: time.Minute}}, amount)
		if ok {
			candidate.ReleaseBeforeUpstream()
			return 100 - amount
		}
	}
	return -1
}
