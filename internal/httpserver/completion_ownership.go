package httpserver

import (
	"context"
	"sync"

	"github.com/pestit/9gateway/internal/accounting"
)

// completionOwnership is the one-shot boundary between the request handler and
// deferred response observation. It deliberately has no accounting state: a
// dropped observation can only affect the enrichment, never lease settlement.
type completionOwnership struct {
	mu     sync.Mutex
	trace  *RequestTraceState
	logger *CompletionLogger

	completed   bool
	transferred bool
	enriched    bool
	finalized   bool
	usage       accounting.Usage
	cost        accounting.Money
}

func newCompletionOwnership(trace *RequestTraceState, logger *CompletionLogger) *completionOwnership {
	return &completionOwnership{trace: trace, logger: logger}
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
	if !ownership.completed {
		ownership.mu.Unlock()
		return
	}
	ownership.finalized = true
	trace, logger := ownership.trace, ownership.logger
	ownership.mu.Unlock()
	ownership.emit(trace, logger, usage, cost)
}

func (ownership *completionOwnership) emit(trace *RequestTraceState, logger *CompletionLogger, usage accounting.Usage, cost accounting.Money) {
	if trace == nil {
		return
	}
	if usage.Input().Known() || usage.Output().Known() || usage.Total().Known() || usage.CachedInput().Known() || usage.ReasoningOutput().Known() {
		trace.SetUsage(usage)
	}
	if cost.Known() {
		trace.SetCost(cost)
	}
	record, err := trace.Final()
	if err != nil || logger == nil {
		return
	}
	logger.Enqueue(record)
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
		ownership.emit(ownership.trace, ownership.logger, usage, cost)
	}
}

func completionOwnershipFromContext(ctx context.Context) *completionOwnership {
	if ctx == nil {
		return nil
	}
	ownership, _ := ctx.Value(completionOwnershipContextKey{}).(*completionOwnership)
	return ownership
}

type completionOwnershipContextKey struct{}
