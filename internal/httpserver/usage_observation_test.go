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

	"github.com/pestit/9gateway/internal/accounting"
	"github.com/pestit/9gateway/internal/config"
	"github.com/pestit/9gateway/internal/limiter"
	"gopkg.in/yaml.v3"
)

func TestT111UsageObservationReconcilesTokenAndBudgetIndependently(t *testing.T) {
	clock := &requestLimitTestClock{now: time.Unix(30, 0).UTC()}
	var pricingConfig config.PricingConfig
	if err := yaml.Unmarshal([]byte("rules:\n  - model: test\n    input_per_million_micros: 500000\n    output_per_million_micros: 500000\n"), &pricingConfig); err != nil {
		t.Fatal(err)
	}
	pricing := accounting.NewPricingResolver(pricingConfig).Resolve("test")
	budget := limiter.NewBudgetLimiter()
	var budgetDelta int64
	budget.SetCommittedDeltaSink(func(delta limiter.CommittedBudgetDelta) { budgetDelta += delta.Delta })
	tokens := limiter.NewTokenLimiter(clock.Now)
	coordinator := limiter.NewResourceLeaseCoordinator(nil, tokens, budget)
	reserved, err := accounting.NewMoneyMicros(10)
	if err != nil {
		t.Fatal(err)
	}
	lease, rejection := coordinator.Acquire(limiter.ResourceLeaseOptions{
		KeyID: "t111", TokenWindows: []limiter.TokenWindow{{Amount: 100, Duration: time.Minute}}, TokenAmount: 10,
		BudgetPolicy: limiter.LimitedBudgetPolicy(reserved), BudgetCandidate: reserved,
	})
	if rejection != nil {
		t.Fatalf("admission rejection = %v", rejection)
	}
	worker := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: 1})
	t.Cleanup(func() { shutdownObservationWorker(t, worker) })
	tickets, err := lease.TransportCompleteWithAdjustments()
	if err != nil || tickets.Token == nil || tickets.Budget == nil {
		t.Fatalf("deferred tickets = (%+v, %v)", tickets, err)
	}
	if !worker.Submit(NewUsageObservationJobWithPricing([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`), ContentCodingIdentity, tickets, pricing)) {
		t.Fatal("observation was dropped")
	}
	waitForObservationCount(t, worker, 1)
	if got := budgetDelta; got != 1 {
		t.Fatalf("budget net charge = %d, want 1 micro", got)
	}
	if got := bucketForObservationTest(t, tokens, "t111"); got != 2 {
		t.Fatalf("token adjustment = %d, want 2", got)
	}
}

func TestT111MissingCostComponentKeepsBudgetConservative(t *testing.T) {
	var pricingConfig config.PricingConfig
	if err := yaml.Unmarshal([]byte("rules:\n  - model: test\n    input_per_million_micros: 500000\n    output_per_million_micros: 500000\n"), &pricingConfig); err != nil {
		t.Fatal(err)
	}
	pricing := accounting.NewPricingResolver(pricingConfig).Resolve("test")
	budget := limiter.NewBudgetLimiter()
	var budgetDelta int64
	budget.SetCommittedDeltaSink(func(delta limiter.CommittedBudgetDelta) { budgetDelta += delta.Delta })
	reserved, _ := accounting.NewMoneyMicros(10)
	coordinator := limiter.NewResourceLeaseCoordinator(nil, nil, budget)
	lease, rejection := coordinator.Acquire(limiter.ResourceLeaseOptions{KeyID: "t111-missing", BudgetPolicy: limiter.LimitedBudgetPolicy(reserved), BudgetCandidate: reserved})
	if rejection != nil {
		t.Fatalf("admission rejection = %v", rejection)
	}
	worker := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: 1})
	t.Cleanup(func() { shutdownObservationWorker(t, worker) })
	tickets, _ := lease.TransportCompleteWithAdjustments()
	if !worker.Submit(NewUsageObservationJobWithPricing([]byte(`{"usage":{"total_tokens":2}}`), ContentCodingIdentity, tickets, pricing)) {
		t.Fatal("observation was dropped")
	}
	waitForObservationCount(t, worker, 1)
	if budgetDelta != 10 {
		t.Fatalf("unknown cost changed budget by %d, want conservative charge", budgetDelta)
	}
}

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
	defer cancel()
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

func TestUsageObservationShutdownTimeoutWinsAtPreAdjustGate(t *testing.T) {
	clock := &requestLimitTestClock{now: time.Unix(30, 0).UTC()}
	tokens := limiter.NewTokenLimiter(clock.Now)
	entered := make(chan struct{})
	continueAdjust := make(chan struct{})
	worker := NewUsageObservationWorker(UsageObservationWorkerOptions{
		Capacity: 1,
		Parse: func([]byte, ContentCoding) (int64, error) {
			return 10, nil
		},
		beforeAdjust: func() {
			close(entered)
			<-continueAdjust
		},
	})
	lease := mustObservationLease(t, limiter.NewResourceLeaseCoordinator(nil, tokens), []limiter.TokenWindow{{Amount: 100, Duration: time.Minute}}, 40)
	if !worker.CompleteAndSubmit(lease, []byte("x"), ContentCodingIdentity) {
		t.Fatal("observation was dropped")
	}
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	errCh := make(chan error, 1)
	go func() { errCh <- worker.Shutdown(ctx) }()
	if err := <-errCh; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v", err)
	}
	cancel()
	close(continueAdjust)
	shutdownObservationWorker(t, worker)
	if got := bucketForObservationTest(t, tokens, "key"); got != 40 {
		t.Fatalf("late parser adjusted after timeout: got %d, want conservative 40", got)
	}
}

func TestUsageObservationShutdownTimeoutDoesNotWaitForCommittedDeltaSink(t *testing.T) {
	clock := &requestLimitTestClock{now: time.Unix(30, 0).UTC()}
	tokens := limiter.NewTokenLimiter(clock.Now)
	coordinator := limiter.NewResourceLeaseCoordinator(nil, tokens)
	lease := mustObservationLease(t, coordinator, []limiter.TokenWindow{{Amount: 100, Duration: time.Minute}}, 40)
	ticket, err := lease.TransportComplete()
	if err != nil || ticket == nil {
		t.Fatalf("transport completion = (%v, %v)", ticket, err)
	}
	sinkEntered := make(chan struct{})
	sinkReturned := make(chan struct{})
	releaseSink := make(chan struct{})
	var persisted limiter.CommittedTokenDelta
	tokens.SetCommittedDeltaSink(func(delta limiter.CommittedTokenDelta) {
		if delta.CommittedDelta != -30 {
			return
		}
		persisted = delta
		close(sinkEntered)
		<-releaseSink
		close(sinkReturned)
	})
	worker := NewUsageObservationWorker(UsageObservationWorkerOptions{
		Capacity: 1,
		Parse:    func([]byte, ContentCoding) (int64, error) { return 10, nil },
	})
	if !worker.Submit(NewUsageObservationJob([]byte("x"), ContentCodingIdentity, ticket)) {
		t.Fatal("observation was dropped")
	}
	select {
	case <-sinkEntered:
	case <-time.After(time.Second):
		t.Fatal("adjustment did not reach committed-delta sink")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = worker.Shutdown(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second/2 {
		t.Fatalf("shutdown waited for blocked sink for %s", elapsed)
	}
	// The adjustment committed limiter state before entering the blocked sink;
	// Shutdown need not wait for notification delivery to observe that state.
	if got := bucketForObservationTest(t, tokens, "key"); got != 10 {
		t.Fatalf("committed adjustment after timed-out shutdown = %d, want 10", got)
	}
	// The terminal adjustment owns completion, so release the test sink before
	// waiting for the worker or inspecting limiter state.
	close(releaseSink)
	select {
	case <-sinkReturned:
	case <-time.After(time.Second):
		t.Fatal("committed-delta sink did not return")
	}
	shutdownObservationWorker(t, worker)
	// The adjustment won the terminal decision before the deadline. Its
	// in-memory state was committed before the worker completed, while the sink
	// remained the explicit owner of the already-issued persistence notification.
	if persisted.KeyID != "key" || persisted.CommittedDelta != -30 {
		t.Fatalf("persisted adjustment = %+v, want key delta -30", persisted)
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
