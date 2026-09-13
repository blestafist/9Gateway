package httpserver

import (
	"context"
	"sync"
	"time"

	"github.com/pestit/9gateway/internal/accounting"
	"github.com/pestit/9gateway/internal/observability"
)

// completionOwnership is the one-shot boundary between the request handler and
// deferred response observation. It deliberately has no accounting state: a
// dropped observation can only affect the enrichment, never lease settlement.
type completionOwnership struct {
	mu      sync.Mutex
	trace   *RequestTraceState
	logger  *CompletionLogger
	history *HistoryPersistenceWorker

	completed          bool
	transferred        bool
	enriched           bool
	finalized          bool
	usage              accounting.Usage
	cost               accounting.Money
	lastMeaningfulMono time.Time
	meaningfulKnown    bool
	timingInvalid      bool
}

func newCompletionOwnership(trace *RequestTraceState, logger *CompletionLogger, history ...*HistoryPersistenceWorker) *completionOwnership {
	var persistence *HistoryPersistenceWorker
	if len(history) != 0 {
		persistence = history[0]
	}
	return &completionOwnership{trace: trace, logger: logger, history: persistence}
}

func (ownership *completionOwnership) transfer() bool {
	if ownership == nil {
		return false
	}
	ownership.mu.Lock()
	defer ownership.mu.Unlock()
	if ownership.completed || ownership.finalized {
		return false
	}
	ownership.transferred = true
	return true
}

// finish freezes the base and emits the immutable final record exactly once.
// Unknown enrichment is represented by leaving the trace enrichment empty.
func (ownership *completionOwnership) finish(usage accounting.Usage, cost accounting.Money) {
	ownership.finishWithTiming(usage, cost, time.Time{}, false)
}

func (ownership *completionOwnership) finishWithTiming(usage accounting.Usage, cost accounting.Money, lastMeaningful time.Time, timingKnown bool) {
	if ownership == nil {
		return
	}
	ownership.mu.Lock()
	if ownership.enriched || ownership.finalized {
		ownership.mu.Unlock()
		return
	}
	ownership.enriched = true
	ownership.usage, ownership.cost = usage, cost
	if timingKnown && !lastMeaningful.IsZero() {
		ownership.lastMeaningfulMono, ownership.meaningfulKnown = lastMeaningful, true
	} else {
		ownership.timingInvalid = true
	}
	if !ownership.completed {
		ownership.mu.Unlock()
		return
	}
	ownership.finalized = true
	trace, logger := ownership.trace, ownership.logger
	ownership.mu.Unlock()
	ownership.emit(trace, logger, usage, cost, ownership.streamCloseDelay(trace))
}

func (ownership *completionOwnership) streamCloseDelay(trace *RequestTraceState) DurationMicros {
	if ownership == nil || trace == nil {
		return UnknownDurationMicros()
	}
	ownership.mu.Lock()
	invalid, known, last := ownership.timingInvalid, ownership.meaningfulKnown, ownership.lastMeaningfulMono
	ownership.mu.Unlock()
	if invalid || !known {
		return UnknownDurationMicros()
	}
	return durationMicros(last, trace.finishedMonotonic())
}

func (ownership *completionOwnership) emit(trace *RequestTraceState, logger *CompletionLogger, usage accounting.Usage, cost accounting.Money, delay DurationMicros) {
	if trace == nil {
		return
	}
	if usage.Input().Known() || usage.Output().Known() || usage.Total().Known() || usage.CachedInput().Known() || usage.ReasoningOutput().Known() {
		trace.SetUsage(usage)
	}
	if cost.Known() {
		trace.SetCost(cost)
	}
	if delay.Known() {
		trace.SetTimingEnrichment(CompletionTiming{StreamCloseDelay: delay})
	}
	record, err := trace.Final()
	if err != nil {
		return
	}
	if logger != nil {
		logger.Enqueue(record)
	}
	if ownership.history != nil {
		bodies := make([]observability.BodySnapshot, 0, 3)
		if client, upstream, ok := trace.RequestBodySnapshots(); ok {
			if client.Captured {
				bodies = append(bodies, client)
			}
			if upstream.Captured {
				bodies = append(bodies, upstream)
			}
		}
		if response, ok := trace.ResponseBodySnapshot(); ok && response.Captured {
			bodies = append(bodies, response)
		}
		ownership.history.SubmitRecord(record, bodies...)
	}
}

func (ownership *completionOwnership) complete() {
	if ownership == nil {
		return
	}
	ownership.mu.Lock()
	if ownership.completed || ownership.finalized {
		ownership.mu.Unlock()
		return
	}
	ownership.completed = true
	transferred := ownership.transferred
	enriched := ownership.enriched
	usage, cost := ownership.usage, ownership.cost
	if !transferred || enriched {
		ownership.finalized = true
	}
	ownership.mu.Unlock()
	if !transferred || enriched {
		ownership.emit(ownership.trace, ownership.logger, usage, cost, ownership.streamCloseDelay(ownership.trace))
	}
}

func (ownership *completionOwnership) observeMeaningfulEvent(at time.Time) {
	if ownership == nil || at.IsZero() {
		return
	}
	ownership.mu.Lock()
	defer ownership.mu.Unlock()
	if ownership.meaningfulKnown && at.Before(ownership.lastMeaningfulMono) {
		ownership.timingInvalid = true
		return
	}
	ownership.lastMeaningfulMono, ownership.meaningfulKnown = at, true
}

func completionOwnershipFromContext(ctx context.Context) *completionOwnership {
	if ctx == nil {
		return nil
	}
	ownership, _ := ctx.Value(completionOwnershipContextKey{}).(*completionOwnership)
	return ownership
}

type completionOwnershipContextKey struct{}
