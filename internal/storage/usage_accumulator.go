package storage

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/pestit/9gateway/internal/limiter"
)

var ErrUsageAccumulatorOverflow = errors.New("usage accumulator overflow")

func checkedSignedAdd(left, right int64) (int64, bool) {
	if right > 0 && left > int64(^uint64(0)>>1)-right {
		return 0, false
	}
	if right < 0 && left < -int64(^uint64(0)>>1)-1-right {
		return 0, false
	}
	return left + right, true
}

// UsageAggregateAccumulator coalesces committed limiter changes by exact
// bucket identity. Add is intentionally lock-bounded and never waits for SQL.
type UsageAggregateAccumulator struct {
	repository *UsageBucketRepository
	ctx        context.Context
	cancel     context.CancelFunc
	wake       chan struct{}
	stop       chan struct{}
	done       chan struct{}

	mu        sync.Mutex
	pending   map[UsageBucketDelta]UsageBucketDelta
	accepting bool
	stopOnce  sync.Once
	lastErr   error
	failed    uint64
}

func NewUsageAggregateAccumulator(repository *UsageBucketRepository) *UsageAggregateAccumulator {
	return NewUsageAggregateAccumulatorWithContext(context.Background(), repository)
}

// NewUsageAggregateAccumulatorWithContext binds all worker writes to the
// process owner. Shutdown cancels this context before the database may close.
func NewUsageAggregateAccumulatorWithContext(parent context.Context, repository *UsageBucketRepository) *UsageAggregateAccumulator {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	accumulator := &UsageAggregateAccumulator{
		repository: repository,
		ctx:        ctx,
		cancel:     cancel,
		wake:       make(chan struct{}, 1),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
		pending:    make(map[UsageBucketDelta]UsageBucketDelta),
		accepting:  true,
	}
	go accumulator.run()
	return accumulator
}

// Sink is suitable for TokenLimiter.SetCommittedDeltaSink.
func (accumulator *UsageAggregateAccumulator) Sink(delta limiter.CommittedTokenDelta) {
	if accumulator == nil || delta.CommittedDelta == 0 {
		return
	}
	key := UsageBucketDelta{APIKeyID: delta.KeyID, BucketStart: delta.BucketStart.UTC(), BucketSeconds: int64(delta.Window.Duration / time.Second), BucketAmount: delta.Window.Amount, CommittedDelta: delta.CommittedDelta}
	accumulator.mu.Lock()
	if !accumulator.accepting {
		accumulator.mu.Unlock()
		return
	}
	identity := key
	identity.CommittedDelta = 0
	current := accumulator.pending[identity]
	current.APIKeyID, current.BucketStart, current.BucketSeconds, current.BucketAmount = identity.APIKeyID, identity.BucketStart, identity.BucketSeconds, identity.BucketAmount
	if _, ok := checkedSignedAdd(current.CommittedDelta, delta.CommittedDelta); !ok {
		accumulator.failed++
		accumulator.lastErr = ErrUsageAccumulatorOverflow
		accumulator.mu.Unlock()
		return
	}
	current.CommittedDelta += delta.CommittedDelta
	if current.CommittedDelta == 0 {
		delete(accumulator.pending, identity)
	} else {
		accumulator.pending[identity] = current
	}
	select {
	case accumulator.wake <- struct{}{}:
	default:
	}
	accumulator.mu.Unlock()
}

func (accumulator *UsageAggregateAccumulator) run() {
	defer close(accumulator.done)
	for {
		select {
		case <-accumulator.wake:
			accumulator.flushRetry()
		case <-accumulator.stop:
			return
		}
	}
}

func (accumulator *UsageAggregateAccumulator) flushRetry() {
	backoff := 10 * time.Millisecond
	for {
		accumulator.flush(accumulator.ctx)
		accumulator.mu.Lock()
		pending := len(accumulator.pending) != 0
		lastErr := accumulator.lastErr
		accumulator.mu.Unlock()
		if !pending || lastErr == nil {
			return
		}
		timer := time.NewTimer(backoff)
		select {
		case <-timer.C:
		case <-accumulator.ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		}
		if backoff < time.Second {
			backoff *= 2
		}
	}
}

func (accumulator *UsageAggregateAccumulator) take() []UsageBucketDelta {
	accumulator.mu.Lock()
	defer accumulator.mu.Unlock()
	if len(accumulator.pending) == 0 {
		return nil
	}
	batch := make([]UsageBucketDelta, 0, len(accumulator.pending))
	for key, delta := range accumulator.pending {
		batch = append(batch, delta)
		delete(accumulator.pending, key)
	}
	return batch
}

func (accumulator *UsageAggregateAccumulator) restore(batch []UsageBucketDelta) {
	accumulator.mu.Lock()
	defer accumulator.mu.Unlock()
	for _, delta := range batch {
		identity := delta
		identity.CommittedDelta = 0
		current := accumulator.pending[identity]
		current.APIKeyID, current.BucketStart, current.BucketSeconds, current.BucketAmount = identity.APIKeyID, identity.BucketStart, identity.BucketSeconds, identity.BucketAmount
		if _, ok := checkedSignedAdd(current.CommittedDelta, delta.CommittedDelta); !ok {
			accumulator.failed++
			accumulator.lastErr = ErrUsageAccumulatorOverflow
			continue
		}
		current.CommittedDelta += delta.CommittedDelta
		if current.CommittedDelta != 0 {
			accumulator.pending[identity] = current
		}
	}
}

func (accumulator *UsageAggregateAccumulator) flush(ctx context.Context) {
	batch := accumulator.take()
	if len(batch) == 0 {
		return
	}
	if err := accumulator.repository.UpsertCommittedDeltas(ctx, batch); err != nil {
		accumulator.restore(batch)
		accumulator.mu.Lock()
		accumulator.lastErr = err
		accumulator.mu.Unlock()
	}
}

// Shutdown stops new aggregate notifications and flushes pending deltas until
// ctx expires. A blocked SQLite writer therefore bounds shutdown only, never
// request response transport or limiter admission.
func (accumulator *UsageAggregateAccumulator) Shutdown(ctx context.Context) error {
	if accumulator == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	accumulator.mu.Lock()
	accumulator.accepting = false
	accumulator.mu.Unlock()
	accumulator.stopOnce.Do(func() { close(accumulator.stop) })
	select {
	case <-accumulator.done:
	case <-ctx.Done():
		// Cancel the worker-owned context and wait for any in-flight SQLite call;
		// callers must not close the database while this goroutine still exists.
		accumulator.cancel()
		<-accumulator.done
		return ctx.Err()
	}
	for {
		batch := accumulator.take()
		if len(batch) == 0 {
			return nil
		}
		if err := accumulator.repository.UpsertCommittedDeltas(ctx, batch); err != nil {
			accumulator.restore(batch)
			return err
		}
	}
}
