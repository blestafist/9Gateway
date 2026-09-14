package storage

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/pestit/9gateway/internal/limiter"
)

var ErrBudgetAccumulatorOverflow = errors.New("budget accumulator overflow")

// BudgetAccumulator coalesces exact lifetime bucket deltas. Add is bounded by
// one mutex and never waits for SQLite; admission and response transport only
// publish their already-committed in-memory delta here.
type BudgetAccumulator struct {
	repository *BudgetBucketRepository
	ctx        context.Context
	cancel     context.CancelFunc
	wake       chan struct{}
	stop       chan struct{}
	done       chan struct{}

	mu        sync.Mutex
	pending   map[BudgetBucketDelta]BudgetBucketDelta
	accepting bool
	stopOnce  sync.Once
	lastErr   error
}

func NewBudgetAccumulator(repository *BudgetBucketRepository) *BudgetAccumulator {
	return NewBudgetAccumulatorWithContext(context.Background(), repository)
}

func NewBudgetAccumulatorWithContext(parent context.Context, repository *BudgetBucketRepository) *BudgetAccumulator {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	accumulator := &BudgetAccumulator{
		repository: repository,
		ctx:        ctx, cancel: cancel,
		wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}),
		pending: make(map[BudgetBucketDelta]BudgetBucketDelta), accepting: true,
	}
	go accumulator.run()
	return accumulator
}

// Sink is suitable for BudgetLimiter.SetCommittedDeltaSink.
func (accumulator *BudgetAccumulator) Sink(delta limiter.CommittedBudgetDelta) {
	if accumulator == nil || delta.Delta == 0 {
		return
	}
	accumulator.mu.Lock()
	defer accumulator.mu.Unlock()
	if !accumulator.accepting {
		return
	}
	period := delta.Period
	if period == "" {
		period = limiter.BudgetPeriodTotal
	}
	identity := BudgetBucketDelta{APIKeyID: delta.KeyID, Period: period, PeriodStart: delta.PeriodStart.UTC()}
	current := accumulator.pending[identity]
	next, ok := checkedSignedAdd(current.SpentDelta, delta.Delta)
	if !ok {
		// The limiter already published the state. Keeping the accumulator
		// unchanged is safer than emitting a malformed persistence write.
		accumulator.lastErr = ErrBudgetAccumulatorOverflow
		return
	}
	if next == 0 {
		delete(accumulator.pending, identity)
	} else {
		value := identity
		value.SpentDelta = next
		accumulator.pending[identity] = value
	}
	select {
	case accumulator.wake <- struct{}{}:
	default:
	}
}

func (accumulator *BudgetAccumulator) run() {
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

func (accumulator *BudgetAccumulator) flushRetry() {
	backoff := 10 * time.Millisecond
	for {
		accumulator.flush(accumulator.ctx)
		accumulator.mu.Lock()
		pending := len(accumulator.pending) != 0
		err := accumulator.lastErr
		accumulator.mu.Unlock()
		if !pending || err == nil {
			return
		}
		timer := time.NewTimer(backoff)
		select {
		case <-timer.C:
		case <-accumulator.ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-accumulator.stop:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		}
		if backoff < time.Second {
			backoff *= 2
		}
	}
}

func (accumulator *BudgetAccumulator) take() []BudgetBucketDelta {
	accumulator.mu.Lock()
	defer accumulator.mu.Unlock()
	if len(accumulator.pending) == 0 {
		return nil
	}
	batch := make([]BudgetBucketDelta, 0, len(accumulator.pending))
	for identity, delta := range accumulator.pending {
		batch = append(batch, delta)
		delete(accumulator.pending, identity)
	}
	return batch
}

func (accumulator *BudgetAccumulator) restore(batch []BudgetBucketDelta) {
	accumulator.mu.Lock()
	defer accumulator.mu.Unlock()
	for _, delta := range batch {
		identity := delta
		identity.SpentDelta = 0
		current := accumulator.pending[identity]
		next, ok := checkedSignedAdd(current.SpentDelta, delta.SpentDelta)
		if !ok {
			accumulator.lastErr = ErrBudgetAccumulatorOverflow
			continue
		}
		if next == 0 {
			delete(accumulator.pending, identity)
		} else {
			value := identity
			value.SpentDelta = next
			accumulator.pending[identity] = value
		}
	}
}

func (accumulator *BudgetAccumulator) flush(ctx context.Context) {
	batch := accumulator.take()
	if len(batch) == 0 {
		return
	}
	if err := accumulator.repository.ApplyDeltas(ctx, batch); err != nil {
		accumulator.restore(batch)
		accumulator.mu.Lock()
		accumulator.lastErr = err
		accumulator.mu.Unlock()
		return
	}
	accumulator.mu.Lock()
	accumulator.lastErr = nil
	accumulator.mu.Unlock()
}

// Shutdown closes the notification boundary, drains the worker, and performs
// a final bounded write before the owner closes SQLite. No delta can enter the
// sink after accepting becomes false. A canceled worker context interrupts
// retry waits, while an in-flight SQL operation is joined before returning.
func (accumulator *BudgetAccumulator) Shutdown(ctx context.Context) error {
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
		accumulator.cancel()
		// The caller's shutdown deadline is authoritative. SQLite remains open
		// when the worker has not yet observed cancellation.
		return ctx.Err()
	}
	for {
		batch := accumulator.take()
		if len(batch) == 0 {
			return nil
		}
		if err := accumulator.repository.ApplyDeltas(ctx, batch); err != nil {
			accumulator.restore(batch)
			return err
		}
	}
}

// BudgetSpendAccumulator is a descriptive compatibility alias.
type BudgetSpendAccumulator = BudgetAccumulator

func NewBudgetSpendAccumulator(repository *BudgetBucketRepository) *BudgetAccumulator {
	return NewBudgetAccumulator(repository)
}
