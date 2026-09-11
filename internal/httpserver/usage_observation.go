package httpserver

// This handoff was implemented independently after inspecting the optional
// Bifrost reference at commit 03ab391865710462302bbcf52dca2f32682b91b5
// (Apache-2.0; .references/bifrost/LICENSE). Inspected source paths were
// .references/bifrost/plugins/governance/tracker.go and
// .references/bifrost/framework/modelcatalog/datasheet/cost.go. Bifrost's
// governance worker was not copied: this path has a bounded immutable byte
// job, a pre-settled one-shot ticket, and conservative drop semantics specific
// to this gateway.

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/pestit/9gateway/internal/accounting"
	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/protocol/openai"
)

const defaultUsageObservationQueueCapacity = 128

// DefaultUsageObservationMaxBytes is the maximum captured representation a
// worker will retain or decode. It is deliberately independent of request and
// completion-log limits.
const DefaultUsageObservationMaxBytes int64 = 8 * 1024 * 1024

var (
	errUsageObservationUnsupported = errors.New("usage observation: unsupported representation")
	errUsageObservationMalformed   = errors.New("usage observation: malformed representation")
	errUsageObservationTooLarge    = errors.New("usage observation: representation exceeds limit")
	// Exported safe sentinels let lifecycle callers classify drops without
	// exposing captured bytes or parser details.
	ErrUsageObservationUnsupported = errUsageObservationUnsupported
	ErrUsageObservationMalformed   = errUsageObservationMalformed
	ErrUsageObservationTooLarge    = errUsageObservationTooLarge
)

// ContentCoding is a validated, bounded description of the response coding.
// It is a value rather than a retained header so observation jobs never retain
// request/response headers. Only codings the worker can safely decode are
// accepted.
type ContentCoding uint8

const (
	ContentCodingIdentity ContentCoding = iota
	ContentCodingGZIP
)

// ContentCodingDescriptor is the descriptive name for ContentCoding.
type ContentCodingDescriptor = ContentCoding

// ValidateContentCoding validates one Content-Encoding value. Empty encoding
// means identity. Lists and unknown codings are rejected rather than guessed.
func ValidateContentCoding(value string) (ContentCoding, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "identity") {
		return ContentCodingIdentity, nil
	}
	if strings.EqualFold(value, "gzip") {
		return ContentCodingGZIP, nil
	}
	return 0, errUsageObservationUnsupported
}

// ParseContentCoding is an alias for ValidateContentCoding.
func ParseContentCoding(value string) (ContentCoding, error) {
	return ValidateContentCoding(value)
}

// UsageObservationJob is the complete handoff to the usage worker. Submit takes
// ownership of Bytes and makes the one immutable queue copy only after a queue
// slot has been admitted. No request, headers, reservation, or concurrency
// lease is part of this value; the ticket is the already-settled one-shot
// adjustment handle only.
type UsageObservationJob struct {
	Bytes         []byte
	ContentCoding ContentCoding
	Ticket        *limiter.TokenAdjustmentTicket
	BudgetTicket  *limiter.BudgetAdjustmentTicket
	Pricing       accounting.PricingResolution
}

// NewUsageObservationJobWithCoding validates a wire Content-Encoding value
// and creates a bounded immutable job. The returned error is safe to expose to
// callers; it never contains the captured representation.
func NewUsageObservationJobWithCoding(captured []byte, contentEncoding string, ticket *limiter.TokenAdjustmentTicket) (UsageObservationJob, error) {
	coding, err := ValidateContentCoding(contentEncoding)
	if err != nil {
		if ticket != nil {
			ticket.Invalidate()
		}
		return UsageObservationJob{}, err
	}
	return NewUsageObservationJob(captured, coding, ticket), nil
}

// NewUsageObservationJobWithPricingCoding is the validated-coding variant for
// budget-aware callers.
func NewUsageObservationJobWithPricingCoding(captured []byte, contentEncoding string, tickets limiter.LeaseAdjustmentTickets, pricing accounting.PricingResolution) (UsageObservationJob, error) {
	coding, err := ValidateContentCoding(contentEncoding)
	if err != nil {
		invalidateObservationJob(UsageObservationJob{Ticket: tickets.Token, BudgetTicket: tickets.Budget})
		return UsageObservationJob{}, err
	}
	return NewUsageObservationJobWithPricing(captured, coding, tickets, pricing), nil
}

// NewUsageObservationJob bounds captured bytes and transfers their ownership
// to the returned job. Submit makes an immutable copy only when it accepts the
// job; callers must not mutate captured after submitting it.
func NewUsageObservationJob(captured []byte, coding ContentCoding, ticket *limiter.TokenAdjustmentTicket) UsageObservationJob {
	if int64(len(captured)) > DefaultUsageObservationMaxBytes {
		captured = captured[:DefaultUsageObservationMaxBytes]
	}
	return UsageObservationJob{Bytes: captured, ContentCoding: coding, Ticket: ticket}
}

// NewUsageObservationJobWithPricing carries both independently owned deferred
// settlements. Pricing is immutable and contains no request identity or body.
func NewUsageObservationJobWithPricing(captured []byte, coding ContentCoding, tickets limiter.LeaseAdjustmentTickets, pricing accounting.PricingResolution) UsageObservationJob {
	job := NewUsageObservationJob(captured, coding, tickets.Token)
	job.BudgetTicket = tickets.Budget
	job.Pricing = pricing
	return job
}

// UsageObservationStats contains safe scalar worker counters.
type UsageObservationStats struct {
	Submitted uint64
	Processed uint64
	Succeeded uint64
	Failed    uint64
	Dropped   uint64
}

// UsageObservationWorkerOptions configures the process-owned bounded worker.
// Parse is intended for deterministic tests and specialized embedders; nil
// selects the bounded OpenAI JSON/SSE parser.
type UsageObservationWorkerOptions struct {
	Capacity int
	MaxBytes int64
	Parse    func([]byte, ContentCoding) (int64, error)
	// ParseUsage is the canonical usage parser used for actual cost
	// reconciliation. Parse remains a compatibility hook for token-only tests.
	ParseUsage func([]byte, ContentCoding) (accounting.Usage, error)
	// beforeAdjust is test-only lifecycle instrumentation; production callers
	// leave it nil. It runs after parsing and before the terminal gate.
	beforeAdjust func()
}

// UsageObservationWorker performs best-effort usage reconciliation on one
// process-owned worker. Submission never waits and never starts a goroutine.
type UsageObservationWorker struct {
	queue chan UsageObservationJob
	slots chan struct{}
	wake  chan struct{}
	stop  chan struct{}
	done  chan struct{}

	maxBytes     int64
	parse        func([]byte, ContentCoding) (int64, error)
	parseUsage   func([]byte, ContentCoding) (accounting.Usage, error)
	beforeAdjust func()

	mu        sync.Mutex
	accepting bool
	stopOnce  sync.Once
	phase     atomic.Uint32

	submitted atomic.Uint64
	processed atomic.Uint64
	succeeded atomic.Uint64
	failed    atomic.Uint64
	dropped   atomic.Uint64
}

// NewUsageObservationWorker starts one bounded usage-observation worker.
func NewUsageObservationWorker(options UsageObservationWorkerOptions) *UsageObservationWorker {
	if options.Capacity <= 0 {
		options.Capacity = defaultUsageObservationQueueCapacity
	}
	if options.MaxBytes <= 0 {
		options.MaxBytes = DefaultUsageObservationMaxBytes
	}
	if options.MaxBytes > DefaultUsageObservationMaxBytes {
		options.MaxBytes = DefaultUsageObservationMaxBytes
	}
	worker := &UsageObservationWorker{
		queue:        make(chan UsageObservationJob, options.Capacity),
		slots:        make(chan struct{}, options.Capacity),
		wake:         make(chan struct{}, 1),
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
		maxBytes:     options.MaxBytes,
		parse:        options.Parse,
		parseUsage:   options.ParseUsage,
		beforeAdjust: options.beforeAdjust,
		accepting:    true,
	}
	for index := 0; index < options.Capacity; index++ {
		worker.slots <- struct{}{}
	}
	if worker.parse == nil {
		worker.parse = func(data []byte, coding ContentCoding) (int64, error) {
			return parseUsageObservation(data, coding, worker.maxBytes)
		}
	}
	if worker.parseUsage == nil {
		worker.parseUsage = func(data []byte, coding ContentCoding) (accounting.Usage, error) {
			return parseCanonicalUsageObservation(data, coding, worker.maxBytes)
		}
	}
	go worker.run()
	return worker
}

// NewUsageObserver is a concise constructor alias.
func NewUsageObserver(options UsageObservationWorkerOptions) *UsageObservationWorker {
	return NewUsageObservationWorker(options)
}

func (worker *UsageObservationWorker) run() {
	defer close(worker.done)
	for {
		select {
		case <-worker.stop:
			worker.mu.Lock()
			worker.discardQueuedLocked()
			worker.mu.Unlock()
			return
		case <-worker.wake:
		}
		// Queue claims are serialized with the shutdown boundary. A job claimed
		// here is in flight; jobs still queued when accepting becomes false are
		// synchronously invalidated by Shutdown and cannot adjust late.
		for {
			worker.mu.Lock()
			if !worker.accepting {
				worker.discardQueuedLocked()
				worker.mu.Unlock()
				return
			}
			var job UsageObservationJob
			select {
			case job = <-worker.queue:
				worker.releaseSlot()
				worker.mu.Unlock()
				worker.process(job)
				continue
			default:
				worker.mu.Unlock()
				break
			}
			break
		}
	}
}

func (worker *UsageObservationWorker) process(job UsageObservationJob) {
	worker.processed.Add(1)
	if job.Ticket == nil && job.BudgetTicket == nil {
		worker.failed.Add(1)
		return
	}

	var actual int64
	var usage accounting.Usage
	var err error
	canonical := job.BudgetTicket != nil || job.Pricing.Known()
	func() {
		defer func() {
			if recover() != nil {
				err = errUsageObservationMalformed
			}
		}()
		if canonical {
			usage, err = worker.parseUsage(job.Bytes, job.ContentCoding)
			if err == nil && usage.Total().Known() {
				actual = usage.Total().Int64()
			}
		} else {
			actual, err = worker.parse(job.Bytes, job.ContentCoding)
		}
	}()
	// Release the worker's bounded byte copy before accounting, and make sure
	// a parser failure never consumes the ticket or changes its conservative
	// charge.
	if err != nil {
		worker.failed.Add(1)
		return
	}
	if worker.beforeAdjust != nil {
		worker.beforeAdjust()
	}
	// Claim the one terminal adjustment slot atomically. Shutdown changes queue
	// acceptance without taking this lifecycle state, so an already in-flight
	// job may finish normally while Shutdown waits. If the wait times out, the
	// terminal state prevents any unclaimed ticket from adjusting later.
	// A claimed slot is explicit terminal ownership: it may finish after a
	// timed-out Shutdown, but no unclaimed ticket can adjust after that point.
	if !worker.beginAdjust() {
		invalidateObservationJob(job)
		worker.failed.Add(1)
		return
	}
	defer worker.finishAdjust()
	var settleErr error
	if job.Ticket != nil && (!canonical || usage.Total().Known()) {
		if err := job.Ticket.Adjust(actual); err != nil {
			settleErr = err
		}
	} else if job.Ticket != nil {
		job.Ticket.Invalidate()
	}
	if job.BudgetTicket != nil {
		cost, costErr := accounting.CalculateActualCost(usage, job.Pricing)
		if costErr != nil || !cost.Known() {
			job.BudgetTicket.Invalidate()
			settleErr = errors.Join(settleErr, errUsageObservationMalformed)
		} else if err := job.BudgetTicket.Adjust(cost); err != nil {
			settleErr = errors.Join(settleErr, err)
		}
	}
	if settleErr != nil {
		worker.failed.Add(1)
		return
	}
	if job.Ticket == nil && job.BudgetTicket == nil {
		worker.failed.Add(1)
		return
	}
	worker.succeeded.Add(1)
}

func (worker *UsageObservationWorker) discardQueued() {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	worker.discardQueuedLocked()
}

func (worker *UsageObservationWorker) discardQueuedLocked() {
	for {
		select {
		case job := <-worker.queue:
			worker.releaseSlot()
			worker.dropped.Add(1)
			invalidateObservationJob(job)
		default:
			return
		}
	}
}

// Submit hands off a job without waiting. Invalid, oversized, saturated, or
// post-shutdown jobs invalidate their ticket and retain the conservative
// accounting charge.
func (worker *UsageObservationWorker) Submit(job UsageObservationJob) bool {
	if worker == nil {
		invalidateObservationJob(job)
		return false
	}
	if (job.Ticket == nil && job.BudgetTicket == nil) || !validContentCoding(job.ContentCoding) || int64(len(job.Bytes)) > worker.maxBytes {
		worker.dropped.Add(1)
		invalidateObservationJob(job)
		return false
	}
	select {
	case <-worker.slots:
	default:
		worker.dropped.Add(1)
		invalidateObservationJob(job)
		return false
	}
	worker.mu.Lock()
	if !worker.accepting {
		worker.mu.Unlock()
		worker.releaseSlot()
		worker.dropped.Add(1)
		invalidateObservationJob(job)
		return false
	}
	worker.mu.Unlock()

	// Make the sole immutable ownership copy outside the contended lifecycle
	// gate. Shutdown may race this copy; the final gate below invalidates the
	// ticket instead of allowing a late queue adjustment.
	job.Bytes = append([]byte(nil), job.Bytes...)
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if !worker.accepting {
		worker.releaseSlot()
		worker.dropped.Add(1)
		invalidateObservationJob(job)
		return false
	}
	select {
	case worker.queue <- job:
		select {
		case worker.wake <- struct{}{}:
		default:
		}
		worker.submitted.Add(1)
		return true
	default:
		worker.releaseSlot()
		worker.dropped.Add(1)
		invalidateObservationJob(job)
		return false
	}
}

// Enqueue is an alias for Submit.
func (worker *UsageObservationWorker) Enqueue(job UsageObservationJob) bool {
	return worker.Submit(job)
}

// CompleteAndSubmit settles transport's deferred outcome before attempting the
// non-blocking handoff. This is the only worker API that accepts a lifecycle
// lease: the lease is consumed immediately and is never retained by a job.
// A nil worker still settles and invalidates the ticket, preserving the
// conservative charge without leaking request resources.
func (worker *UsageObservationWorker) CompleteAndSubmit(lease *limiter.ResourceLease, captured []byte, coding ContentCoding) bool {
	if lease == nil {
		return false
	}
	ticket, _ := lease.TransportComplete()
	if ticket == nil {
		return false
	}
	return worker.Submit(NewUsageObservationJob(captured, coding, ticket))
}

// CompleteAndSubmitWithPricing releases the composite lease before making a
// nonblocking handoff, retaining independent token and budget ownership in the
// bounded job. The lease itself is never retained by the worker.
func (worker *UsageObservationWorker) CompleteAndSubmitWithPricing(lease *limiter.ResourceLease, captured []byte, coding ContentCoding, pricing accounting.PricingResolution) bool {
	if lease == nil {
		return false
	}
	tickets, _ := lease.TransportCompleteWithAdjustments()
	if tickets.Token == nil && tickets.Budget == nil {
		return false
	}
	return worker.Submit(NewUsageObservationJobWithPricing(captured, coding, tickets, pricing))
}

// CompleteAndSubmitTokenDeferredBudgetConservative keeps token observation
// asynchronous while charging any budget reservation conservatively. The
// budget adjustment ticket is never handed to the observation worker.
func (worker *UsageObservationWorker) CompleteAndSubmitTokenDeferredBudgetConservative(lease *limiter.ResourceLease, captured []byte, coding ContentCoding) bool {
	if lease == nil {
		return false
	}
	ticket, _ := lease.TransportCompleteTokenDeferredBudgetConservative()
	if ticket == nil {
		return false
	}
	return worker.Submit(NewUsageObservationJob(captured, coding, ticket))
}

// Stats returns only bounded scalar counters.
func (worker *UsageObservationWorker) Stats() UsageObservationStats {
	if worker == nil {
		return UsageObservationStats{}
	}
	return UsageObservationStats{
		Submitted: worker.submitted.Load(),
		Processed: worker.processed.Load(),
		Succeeded: worker.succeeded.Load(),
		Failed:    worker.failed.Load(),
		Dropped:   worker.dropped.Load(),
	}
}

func (worker *UsageObservationWorker) Dropped() uint64 { return worker.Stats().Dropped }
func (worker *UsageObservationWorker) Failed() uint64  { return worker.Stats().Failed }

// Shutdown stops accepting work, discards queued jobs safely, and waits for
// the single worker subject to ctx. Repeated calls are safe. A nil context is
// treated as context.Background().
func (worker *UsageObservationWorker) Shutdown(ctx context.Context) error {
	if worker == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	worker.mu.Lock()
	worker.accepting = false
	// Drain synchronously at the shutdown boundary. The worker may be blocked
	// parsing an already-claimed job, but queued tickets must not remain valid
	// until that parse returns.
	worker.discardQueuedLocked()
	worker.mu.Unlock()
	worker.stopOnce.Do(func() { close(worker.stop) })
	select {
	case <-worker.done:
		return nil
	case <-ctx.Done():
		// An adjustment that already claimed the slot is explicit terminal
		// ownership and may finish without blocking this caller. Otherwise make
		// timeout the terminal decision atomically with the claim.
		worker.phase.CompareAndSwap(usageObservationIdle, usageObservationTerminal)
		worker.phase.CompareAndSwap(usageObservationAdjusting, usageObservationTerminal)
		return ctx.Err()
	}
}

const (
	usageObservationIdle uint32 = iota
	usageObservationAdjusting
	usageObservationTerminal
)

func (worker *UsageObservationWorker) beginAdjust() bool {
	return worker.phase.CompareAndSwap(usageObservationIdle, usageObservationAdjusting)
}

func (worker *UsageObservationWorker) finishAdjust() {
	worker.phase.CompareAndSwap(usageObservationAdjusting, usageObservationIdle)
}

func (worker *UsageObservationWorker) releaseSlot() {
	worker.slots <- struct{}{}
}

func validContentCoding(coding ContentCoding) bool {
	return coding == ContentCodingIdentity || coding == ContentCodingGZIP
}

func parseUsageObservation(data []byte, coding ContentCoding, maxBytes int64) (int64, error) {
	if !validContentCoding(coding) {
		return 0, errUsageObservationUnsupported
	}
	decoded, err := decodeObservedBytes(data, coding, maxBytes)
	if err != nil {
		return 0, err
	}
	trimmed := bytes.TrimSpace(decoded)
	if len(trimmed) == 0 {
		return 0, errUsageObservationMalformed
	}
	if trimmed[0] == '{' {
		result, err := openai.ParseJSONUsage(trimmed)
		if err != nil || !result.Observed || !result.Usage.Total().Known() {
			return 0, errUsageObservationMalformed
		}
		return result.Usage.Total().Int64(), nil
	}
	result, err := openai.ObserveStream(bytes.NewReader(decoded), 64*1024, nil)
	if err == nil && len(result.Errors) == 0 && result.State.Usage.Total().Known() {
		return result.State.Usage.Total().Int64(), nil
	}
	return 0, errUsageObservationMalformed
}

func parseCanonicalUsageObservation(data []byte, coding ContentCoding, maxBytes int64) (accounting.Usage, error) {
	if !validContentCoding(coding) {
		return accounting.Usage{}, errUsageObservationUnsupported
	}
	decoded, err := decodeObservedBytes(data, coding, maxBytes)
	if err != nil {
		return accounting.Usage{}, err
	}
	trimmed := bytes.TrimSpace(decoded)
	if len(trimmed) == 0 {
		return accounting.Usage{}, errUsageObservationMalformed
	}
	if trimmed[0] == '{' {
		result, err := openai.ParseJSONUsage(trimmed)
		if err != nil || !result.Observed {
			return accounting.Usage{}, errUsageObservationMalformed
		}
		return result.Usage, nil
	}
	result, err := openai.ObserveStream(bytes.NewReader(decoded), 64*1024, nil)
	if err != nil || len(result.Errors) != 0 || !result.State.Usage.Total().Known() {
		return accounting.Usage{}, errUsageObservationMalformed
	}
	return result.State.Usage.Usage, nil
}

func invalidateObservationJob(job UsageObservationJob) {
	if job.Ticket != nil {
		job.Ticket.Invalidate()
	}
	if job.BudgetTicket != nil {
		job.BudgetTicket.Invalidate()
	}
}

func decodeObservedBytes(data []byte, coding ContentCoding, maxBytes int64) ([]byte, error) {
	if int64(len(data)) > maxBytes {
		return nil, errUsageObservationTooLarge
	}
	if coding == ContentCodingIdentity {
		return data, nil
	}
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, errUsageObservationMalformed
	}
	defer reader.Close()
	decoded, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, errUsageObservationMalformed
	}
	if int64(len(decoded)) > maxBytes {
		return nil, errUsageObservationTooLarge
	}
	return decoded, nil
}
