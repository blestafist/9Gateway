package limiter

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pestit/9gateway/internal/accounting"
)

// Bifrost provenance notice: composition and rollback ordering were inspected
// at reference commit 03ab391865710462302bbcf52dca2f32682b91b5 (branch dev),
// specifically .references/bifrost/plugins/governance/store.go,
// .references/bifrost/plugins/governance/tracker.go, and
// .references/bifrost/plugins/governance/storeconcurrency_test.go. The
// reference LICENSE and THIRD_PARTY_NOTICES.md were verified (Apache-2.0).
// Nothing was copied or adapted and no dependency or import architecture was
// added; this coordinator retains the gateway's admission-time ownership and
// conservative/pre-start semantics.

// AdmissionResource identifies the resource which rejected admission. It is
// deliberately a domain value rather than an HTTP concern; callers can choose
// their own response policy later.
type AdmissionResource string

const (
	AdmissionConcurrency AdmissionResource = "concurrency"
	AdmissionTokens      AdmissionResource = "tokens"
	AdmissionBudget      AdmissionResource = "budget"
)

var (
	ErrConcurrencyUnavailable = errors.New("concurrency admission rejected")
	ErrTokenUnavailable       = errors.New("token admission rejected")
	ErrBudgetUnavailable      = errors.New("budget admission rejected")
	ErrInvalidAdmission       = errors.New("invalid admission input")
)

// AdmissionError describes a failed resource admission. ResetAt is useful for
// token-window retry reporting and is zero when the resource has no useful
// reset (for example, a saturated concurrency slot or an oversized estimate).
type AdmissionError struct {
	Resource AdmissionResource
	ResetAt  time.Time
	Invalid  bool
}

func (err *AdmissionError) Error() string {
	if err == nil {
		return ""
	}
	if err.ResetAt.IsZero() {
		return fmt.Sprintf("%s admission rejected", err.Resource)
	}
	return fmt.Sprintf("%s admission rejected until %s", err.Resource, err.ResetAt.UTC().Format(time.RFC3339Nano))
}

func (err *AdmissionError) Unwrap() error {
	if err == nil {
		return nil
	}
	if err.Invalid {
		return ErrInvalidAdmission
	}
	if err.Resource == AdmissionTokens {
		return ErrTokenUnavailable
	}
	if err.Resource == AdmissionBudget {
		return ErrBudgetUnavailable
	}
	return ErrConcurrencyUnavailable
}

// ResourceLeaseOptions is the complete resource portion of one request's
// admission decision. Request-count windows are intentionally absent: request
// capacity is consumed outside this lifecycle and is never refunded.
type ResourceLeaseOptions struct {
	KeyID          string
	MaxConcurrency int
	TokenWindows   []TokenWindow
	TokenAmount    int64
	// BudgetLimiter is selected on the coordinator. BudgetPolicy and
	// BudgetCandidate are the narrow plan output consumed by admission; the
	// coordinator deliberately does not resolve prices or inspect requests.
	BudgetPolicy    BudgetPolicy
	BudgetCandidate accounting.Money
}

// RequestLeaseOptions is the request-oriented spelling of the admission
// options. Both names describe the same value and avoid coupling callers to a
// transport package.
type RequestLeaseOptions = ResourceLeaseOptions

// ResourceLeaseCoordinator admits the resources used by one request. It
// always acquires concurrency before tokens. This order both gives the
// concurrency rejection precedence and ensures a token rejection can roll
// back exactly the slot acquired by this request.
type ResourceLeaseCoordinator struct {
	concurrency *ConcurrencyLimiter
	tokens      *TokenLimiter
	budgets     *BudgetLimiter
}

// LeaseCoordinator and ResourceCoordinator are descriptive aliases for code
// which refers to this coordinator by its shorter architectural name.
type LeaseCoordinator = ResourceLeaseCoordinator
type ResourceCoordinator = ResourceLeaseCoordinator
type RequestLeaseCoordinator = ResourceLeaseCoordinator

// NewResourceLeaseCoordinator creates a coordinator. A nil limiter means that
// resource is unlimited. No limiter state is retained for unlimited resources.
func NewResourceLeaseCoordinator(concurrency *ConcurrencyLimiter, tokens *TokenLimiter, budgets ...*BudgetLimiter) *ResourceLeaseCoordinator {
	var budget *BudgetLimiter
	if len(budgets) != 0 {
		budget = budgets[0]
	}
	return &ResourceLeaseCoordinator{concurrency: concurrency, tokens: tokens, budgets: budget}
}

// NewLeaseCoordinator is the concise constructor spelling.
func NewLeaseCoordinator(concurrency *ConcurrencyLimiter, tokens *TokenLimiter, budgets ...*BudgetLimiter) *ResourceLeaseCoordinator {
	return NewResourceLeaseCoordinator(concurrency, tokens, budgets...)
}

// NewResourceCoordinator is an alias matching the package's other limiter
// constructors.
func NewResourceCoordinator(concurrency *ConcurrencyLimiter, tokens *TokenLimiter, budgets ...*BudgetLimiter) *ResourceLeaseCoordinator {
	return NewResourceLeaseCoordinator(concurrency, tokens, budgets...)
}

// NewRequestLeaseCoordinator is the request-lifecycle spelling of the
// coordinator constructor.
func NewRequestLeaseCoordinator(concurrency *ConcurrencyLimiter, tokens *TokenLimiter, budgets ...*BudgetLimiter) *ResourceLeaseCoordinator {
	return NewResourceLeaseCoordinator(concurrency, tokens, budgets...)
}

// Acquire admits one ordinary request lease. Concurrency is acquired first;
// if token admission rejects, the just-acquired slot is released before this
// method returns. Token reservation is optional: it is attempted only when a
// token limiter exists and TokenAmount is positive. A negative amount is a
// malformed token admission and is rejected when token limiting is enabled.
func (coordinator *ResourceLeaseCoordinator) Acquire(options ResourceLeaseOptions) (*ResourceLease, *AdmissionError) {
	if coordinator == nil {
		return nil, &AdmissionError{Resource: AdmissionConcurrency, Invalid: true}
	}
	concurrencyLease, rejection := coordinator.acquireConcurrency(options.KeyID, options.MaxConcurrency)
	if rejection != nil {
		return nil, rejection
	}
	return coordinator.finishAdmission(concurrencyLease, options)
}

// AcquireWithOptions is a descriptive alias for Acquire.
func (coordinator *ResourceLeaseCoordinator) AcquireWithOptions(options ResourceLeaseOptions) (*ResourceLease, *AdmissionError) {
	return coordinator.Acquire(options)
}

// AcquireRequest provides the compact argument form for request orchestration
// while Acquire keeps the options form convenient for policy callers.
func (coordinator *ResourceLeaseCoordinator) AcquireRequest(keyID string, maxConcurrency int, tokenWindows []TokenWindow, tokenAmount int64) (*ResourceLease, *AdmissionError) {
	return coordinator.Acquire(ResourceLeaseOptions{
		KeyID:          keyID,
		MaxConcurrency: maxConcurrency,
		TokenWindows:   tokenWindows,
		TokenAmount:    tokenAmount,
	})
}

// AcquireWithProvisional promotes an already-held provisional concurrency
// lease, such as the slot used while bounded request inspection reads a slow
// upload. Promotion does not acquire a second slot and does not charge token
// capacity until this method is called. On token rejection, ownership of the
// provisional slot is still settled before returning.
func (coordinator *ResourceLeaseCoordinator) AcquireWithProvisional(provisional *Lease, options ResourceLeaseOptions) (*ResourceLease, *AdmissionError) {
	if coordinator == nil || provisional == nil {
		return nil, &AdmissionError{Resource: AdmissionConcurrency, Invalid: true}
	}
	if options.MaxConcurrency < 0 {
		return nil, &AdmissionError{Resource: AdmissionConcurrency, Invalid: true}
	}
	if !provisional.adoptFor(coordinator.concurrency, options.KeyID, options.MaxConcurrency > 0) {
		return nil, &AdmissionError{Resource: AdmissionConcurrency, Invalid: true}
	}
	return coordinator.finishAdmission(provisional, options)
}

// AdoptProvisional is an alias that emphasizes ownership transfer rather than
// the inspection phase which commonly precedes it.
func (coordinator *ResourceLeaseCoordinator) AdoptProvisional(provisional *Lease, options ResourceLeaseOptions) (*ResourceLease, *AdmissionError) {
	return coordinator.AcquireWithProvisional(provisional, options)
}

// PromoteRequest is the compact argument form of AdoptProvisional.
func (coordinator *ResourceLeaseCoordinator) PromoteRequest(provisional *Lease, keyID string, maxConcurrency int, tokenWindows []TokenWindow, tokenAmount int64) (*ResourceLease, *AdmissionError) {
	return coordinator.AcquireWithProvisional(provisional, ResourceLeaseOptions{
		KeyID:          keyID,
		MaxConcurrency: maxConcurrency,
		TokenWindows:   tokenWindows,
		TokenAmount:    tokenAmount,
	})
}

// Promote is an alias for AcquireWithProvisional.
func (coordinator *ResourceLeaseCoordinator) Promote(provisional *Lease, options ResourceLeaseOptions) (*ResourceLease, *AdmissionError) {
	return coordinator.AcquireWithProvisional(provisional, options)
}

func (coordinator *ResourceLeaseCoordinator) acquireConcurrency(keyID string, maximum int) (*Lease, *AdmissionError) {
	if maximum < 0 {
		return nil, &AdmissionError{Resource: AdmissionConcurrency, Invalid: true}
	}
	if coordinator.concurrency == nil {
		return &Lease{keyID: keyID, admitted: true}, nil
	}
	lease, ok := coordinator.concurrency.Acquire(keyID, maximum)
	if !ok {
		return nil, &AdmissionError{Resource: AdmissionConcurrency}
	}
	return lease, nil
}

func (coordinator *ResourceLeaseCoordinator) finishAdmission(concurrencyLease *Lease, options ResourceLeaseOptions) (*ResourceLease, *AdmissionError) {
	var tokens *TokenReservation
	if coordinator.tokens != nil {
		if options.TokenAmount < 0 {
			concurrencyLease.releaseOwned()
			return nil, &AdmissionError{Resource: AdmissionTokens, Invalid: true}
		}
		if options.TokenAmount > 0 {
			var ok bool
			var resetAt time.Time
			tokens, ok, resetAt = coordinator.tokens.Reserve(options.KeyID, options.TokenWindows, options.TokenAmount)
			if !ok {
				concurrencyLease.releaseOwned()
				return nil, &AdmissionError{Resource: AdmissionTokens, ResetAt: resetAt}
			}
		}
	}

	// Budget is intentionally last. A failed budget reservation owns no budget
	// resource, and rolls back only the token reservation created above and then
	// the concurrency slot, in reverse acquisition order.
	var budget *BudgetReservation
	if coordinator.budgets != nil && budgetRequested(options) {
		if !options.BudgetCandidate.Known() {
			if tokens != nil {
				_ = tokens.ReleaseBeforeUpstream()
			}
			concurrencyLease.releaseOwned()
			return nil, &AdmissionError{Resource: AdmissionBudget, Invalid: true}
		}
		var err error
		budget, err = coordinator.budgets.Reserve(options.KeyID, options.BudgetPolicy, options.BudgetCandidate)
		if err != nil {
			if tokens != nil {
				_ = tokens.ReleaseBeforeUpstream()
			}
			concurrencyLease.releaseOwned()
			admission := &AdmissionError{Resource: AdmissionBudget}
			if !errors.Is(err, ErrBudgetCapacity) {
				admission.Invalid = true
			}
			return nil, admission
		}
	}
	return &ResourceLease{concurrency: concurrencyLease, tokens: tokens, budget: budget}, nil
}

func budgetRequested(options ResourceLeaseOptions) bool {
	// A limited policy is an explicit configured budget. A known candidate is
	// also a plan signal and permits an unlimited/no-op budget reservation.
	return options.BudgetPolicy.Limited || options.BudgetCandidate.Known()
}

// ResourceLease owns one concurrency slot and, when configured, one T089 token
// and one T108 budget reservation. It must be used by pointer: its mutex and
// terminal ownership state must not be copied.
type ResourceLease struct {
	concurrency *Lease
	tokens      *TokenReservation
	budget      *BudgetReservation

	mu              sync.Mutex
	finalized       bool
	finalizing      bool
	done            chan struct{}
	finalErr        error
	finalTokenErr   error
	finalBudgetErr  error
	deferredTickets LeaseAdjustmentTickets
}

// LeaseAdjustmentTickets is the deferred ownership returned after transport
// has ended. The two tickets are intentionally independent: a failed or
// discarded token observation must not consume the budget ticket, and vice
// versa. Neither ticket retains the composite lease.
type LeaseAdjustmentTickets struct {
	Token  *TokenAdjustmentTicket
	Budget *BudgetAdjustmentTicket
}

// DeferredLeaseAdjustments is a descriptive alias for LeaseAdjustmentTickets.
type DeferredLeaseAdjustments = LeaseAdjustmentTickets

func (tickets LeaseAdjustmentTickets) InvalidateToken() {
	if tickets.Token != nil {
		tickets.Token.Invalidate()
	}
}

func (tickets LeaseAdjustmentTickets) InvalidateBudget() {
	if tickets.Budget != nil {
		tickets.Budget.Invalidate()
	}
}

func (tickets LeaseAdjustmentTickets) Invalidate() {
	tickets.InvalidateToken()
	tickets.InvalidateBudget()
}

// RequestLease is the request-lifecycle name for ResourceLease.
type RequestLease = ResourceLease

type leaseOutcome uint8

const (
	outcomeKnown leaseOutcome = iota
	outcomeConservative
	outcomeRelease
	outcomeDeferred
	outcomeDeferredBudgetConservative
)

func (lease *ResourceLease) finish(outcome leaseOutcome, actual int64, actualKnown bool, cost accounting.Money) (error, LeaseAdjustmentTickets) {
	if lease == nil {
		return nil, LeaseAdjustmentTickets{}
	}
	lease.mu.Lock()
	if lease.finalized {
		err, tickets := lease.finalErr, lease.deferredTickets
		lease.mu.Unlock()
		return err, tickets
	}
	if lease.finalizing {
		done := lease.done
		lease.mu.Unlock()
		<-done
		lease.mu.Lock()
		err, tickets := lease.finalErr, lease.deferredTickets
		lease.mu.Unlock()
		return err, tickets
	}
	lease.finalizing = true
	lease.done = make(chan struct{})
	concurrency, tokens, budget := lease.concurrency, lease.tokens, lease.budget
	lease.mu.Unlock()

	var tickets LeaseAdjustmentTickets
	var tokenErr, budgetErr error
	switch outcome {
	case outcomeKnown:
		if tokens != nil {
			if actualKnown {
				tokenErr = tokens.Commit(actual)
			} else {
				tokenErr = tokens.AbortConservative()
			}
		}
		if budget != nil {
			budgetErr = budget.Commit(cost)
		}
	case outcomeConservative:
		if tokens != nil {
			tokenErr = tokens.AbortConservative()
		}
		if budget != nil {
			budgetErr = budget.CompleteConservative()
		}
	case outcomeRelease:
		if tokens != nil {
			tokenErr = tokens.ReleaseBeforeUpstream()
		}
		if budget != nil {
			budgetErr = budget.ReleaseBeforeUpstream()
		}
	case outcomeDeferred:
		if tokens != nil {
			tickets.Token, tokenErr = tokens.CommitDeferred()
		}
		if budget != nil {
			tickets.Budget, budgetErr = budget.CommitDeferred()
		}
	case outcomeDeferredBudgetConservative:
		if tokens != nil {
			tickets.Token, tokenErr = tokens.CommitDeferred()
		}
		if budget != nil {
			budgetErr = budget.CompleteConservative()
		}
	}
	// A token finalization error must not strand concurrency. TokenLimiter's
	// terminal methods are themselves idempotent and settle active capacity.
	concurrency.releaseOwned()
	err := errors.Join(tokenErr, budgetErr)
	lease.mu.Lock()
	lease.concurrency = nil
	lease.tokens = nil
	lease.budget = nil
	lease.finalErr = err
	lease.finalTokenErr = tokenErr
	lease.finalBudgetErr = budgetErr
	lease.deferredTickets = tickets
	lease.finalized = true
	lease.finalizing = false
	close(lease.done)
	lease.mu.Unlock()
	return err, tickets
}

// CommitKnown reconciles the token reservation with actual total usage and
// releases concurrency exactly once.
func (lease *ResourceLease) CommitKnown(actual int64) error {
	err, _ := lease.finish(outcomeKnown, actual, true, accounting.UnknownMoney())
	return err
}

// CommitKnownWithCost reconciles token usage and budget cost independently.
// An unknown cost conservatively settles budget while token usage can still be
// reconciled; errors from one child never prevent settling the other.
func (lease *ResourceLease) CommitKnownWithCost(actual int64, cost accounting.Money) error {
	err, _ := lease.finish(outcomeKnown, actual, true, cost)
	return err
}

// CompleteKnown reconciles a canonical usage total and independently supplied
// cost. It is the accounting-facing spelling of CommitKnownWithCost; pricing
// remains the caller's responsibility.
func (lease *ResourceLease) CompleteKnown(usage accounting.Usage, cost accounting.Money) error {
	return lease.CommitKnownUsage(usage, cost)
}

// CommitCanonical is an alias for CompleteKnown.
func (lease *ResourceLease) CommitCanonical(usage accounting.Usage, cost accounting.Money) error {
	return lease.CompleteKnown(usage, cost)
}

// CommitKnownUsage uses the canonical total and independently supplied cost.
// A missing total is passed through to the token limiter's safe conservative
// path rather than being invented here.
func (lease *ResourceLease) CommitKnownUsage(usage accounting.Usage, cost accounting.Money) error {
	if !usage.Total().Known() {
		err, _ := lease.finish(outcomeKnown, 0, false, cost)
		return err
	}
	return lease.CommitKnownWithCost(usage.Total().Int64(), cost)
}

// Commit is the short spelling of CommitKnown.
func (lease *ResourceLease) Commit(actual int64) error {
	return lease.CommitKnown(actual)
}

// CompleteConservative settles ambiguous work with its reserved estimate and
// releases concurrency exactly once.
func (lease *ResourceLease) CompleteConservative() error {
	err, _ := lease.finish(outcomeConservative, 0, false, accounting.UnknownMoney())
	return err
}

// ConservativeComplete is an alternate terminal spelling.
func (lease *ResourceLease) ConservativeComplete() error {
	return lease.CompleteConservative()
}

// AbortConservative is the lifecycle spelling used for cancellation and
// incomplete observation.
func (lease *ResourceLease) AbortConservative() error {
	return lease.CompleteConservative()
}

// Abort is a concise alias for AbortConservative.
func (lease *ResourceLease) Abort() error {
	return lease.CompleteConservative()
}

// ReleaseBeforeUpstream releases all resources for work proven never to have
// started upstream.
func (lease *ResourceLease) ReleaseBeforeUpstream() error {
	err, _ := lease.finish(outcomeRelease, 0, false, accounting.UnknownMoney())
	return err
}

// Release is retained as a convenient pre-upstream cleanup operation.
func (lease *ResourceLease) Release() {
	_ = lease.ReleaseBeforeUpstream()
}

// TransportComplete conservatively commits token usage, releases concurrency
// immediately, and returns only the T089 adjustment ticket. The composite
// lease retains no resource that the caller must clean up afterward.
func (lease *ResourceLease) TransportComplete() (*TokenAdjustmentTicket, error) {
	err, tickets := lease.finish(outcomeDeferred, 0, false, accounting.UnknownMoney())
	return tickets.Token, err
}

// TransportCompleteWithAdjustments returns independent one-shot token and
// budget ownership for deferred observation workers.
func (lease *ResourceLease) TransportCompleteWithAdjustments() (LeaseAdjustmentTickets, error) {
	err, tickets := lease.finish(outcomeDeferred, 0, false, accounting.UnknownMoney())
	return tickets, err
}

// TransportCompleteTokenDeferredBudgetConservative releases concurrency and
// keeps token observation eligible while settling the budget reservation at
// its conservative estimate. Budget reconciliation is intentionally deferred
// to the later cost-reconciliation tasks.
func (lease *ResourceLease) TransportCompleteTokenDeferredBudgetConservative() (*TokenAdjustmentTicket, error) {
	err, tickets := lease.finish(outcomeDeferredBudgetConservative, 0, false, accounting.UnknownMoney())
	return tickets.Token, err
}

// CompleteDeferredWithAdjustments is the explicit deferred-lifecycle spelling.
func (lease *ResourceLease) CompleteDeferredWithAdjustments() (LeaseAdjustmentTickets, error) {
	return lease.TransportCompleteWithAdjustments()
}

// DeferWithAdjustments is a concise alias for deferred composite ownership.
func (lease *ResourceLease) DeferWithAdjustments() (LeaseAdjustmentTickets, error) {
	return lease.TransportCompleteWithAdjustments()
}

// CompleteTransport is an alternate spelling used by transport orchestration.
func (lease *ResourceLease) CompleteTransport() (*TokenAdjustmentTicket, error) {
	return lease.TransportComplete()
}

// CompleteDeferred is an alias for TransportComplete.
func (lease *ResourceLease) CompleteDeferred() (*TokenAdjustmentTicket, error) {
	return lease.TransportComplete()
}

// Defer is a concise alias for TransportComplete.
func (lease *ResourceLease) Defer() (*TokenAdjustmentTicket, error) {
	return lease.TransportComplete()
}
