package limiter

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// AdmissionResource identifies the resource which rejected admission. It is
// deliberately a domain value rather than an HTTP concern; callers can choose
// their own response policy later.
type AdmissionResource string

const (
	AdmissionConcurrency AdmissionResource = "concurrency"
	AdmissionTokens      AdmissionResource = "tokens"
)

var (
	ErrConcurrencyUnavailable = errors.New("concurrency admission rejected")
	ErrTokenUnavailable       = errors.New("token admission rejected")
)

// AdmissionError describes a failed resource admission. ResetAt is useful for
// token-window retry reporting and is zero when the resource has no useful
// reset (for example, a saturated concurrency slot or an oversized estimate).
type AdmissionError struct {
	Resource AdmissionResource
	ResetAt  time.Time
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
	if err.Resource == AdmissionTokens {
		return ErrTokenUnavailable
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
}

// LeaseCoordinator and ResourceCoordinator are descriptive aliases for code
// which refers to this coordinator by its shorter architectural name.
type LeaseCoordinator = ResourceLeaseCoordinator
type ResourceCoordinator = ResourceLeaseCoordinator
type RequestLeaseCoordinator = ResourceLeaseCoordinator

// NewResourceLeaseCoordinator creates a coordinator. A nil limiter means that
// resource is unlimited. No limiter state is retained for unlimited resources.
func NewResourceLeaseCoordinator(concurrency *ConcurrencyLimiter, tokens *TokenLimiter) *ResourceLeaseCoordinator {
	return &ResourceLeaseCoordinator{concurrency: concurrency, tokens: tokens}
}

// NewLeaseCoordinator is the concise constructor spelling.
func NewLeaseCoordinator(concurrency *ConcurrencyLimiter, tokens *TokenLimiter) *ResourceLeaseCoordinator {
	return NewResourceLeaseCoordinator(concurrency, tokens)
}

// NewResourceCoordinator is an alias matching the package's other limiter
// constructors.
func NewResourceCoordinator(concurrency *ConcurrencyLimiter, tokens *TokenLimiter) *ResourceLeaseCoordinator {
	return NewResourceLeaseCoordinator(concurrency, tokens)
}

// NewRequestLeaseCoordinator is the request-lifecycle spelling of the
// coordinator constructor.
func NewRequestLeaseCoordinator(concurrency *ConcurrencyLimiter, tokens *TokenLimiter) *ResourceLeaseCoordinator {
	return NewResourceLeaseCoordinator(concurrency, tokens)
}

// Acquire admits one ordinary request lease. Concurrency is acquired first;
// if token admission rejects, the just-acquired slot is released before this
// method returns. Token reservation is optional: it is attempted only when a
// token limiter exists and TokenAmount is positive. A negative amount is a
// malformed token admission and is rejected when token limiting is enabled.
func (coordinator *ResourceLeaseCoordinator) Acquire(options ResourceLeaseOptions) (*ResourceLease, *AdmissionError) {
	if coordinator == nil {
		return nil, &AdmissionError{Resource: AdmissionConcurrency}
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
		return nil, &AdmissionError{Resource: AdmissionConcurrency}
	}
	if coordinator.concurrency != nil && provisional.limiter != nil && provisional.limiter != coordinator.concurrency {
		return nil, &AdmissionError{Resource: AdmissionConcurrency}
	}
	if provisional.limiter != nil && provisional.keyID != options.KeyID {
		return nil, &AdmissionError{Resource: AdmissionConcurrency}
	}
	if !provisional.adopt() {
		return nil, &AdmissionError{Resource: AdmissionConcurrency}
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
		return nil, &AdmissionError{Resource: AdmissionConcurrency}
	}
	if coordinator.concurrency == nil {
		return &Lease{}, nil
	}
	lease, ok := coordinator.concurrency.Acquire(keyID, maximum)
	if !ok {
		return nil, &AdmissionError{Resource: AdmissionConcurrency}
	}
	return lease, nil
}

func (coordinator *ResourceLeaseCoordinator) finishAdmission(concurrencyLease *Lease, options ResourceLeaseOptions) (*ResourceLease, *AdmissionError) {
	if coordinator.tokens == nil {
		return &ResourceLease{concurrency: concurrencyLease}, nil
	}
	if options.TokenAmount < 0 {
		concurrencyLease.releaseOwned()
		return nil, &AdmissionError{Resource: AdmissionTokens}
	}
	if options.TokenAmount == 0 {
		return &ResourceLease{concurrency: concurrencyLease}, nil
	}
	tokens, ok, resetAt := coordinator.tokens.Reserve(options.KeyID, options.TokenWindows, options.TokenAmount)
	if !ok {
		// TokenLimiter.Reserve is atomic, so only the earlier concurrency
		// acquisition needs rollback here.
		concurrencyLease.releaseOwned()
		return nil, &AdmissionError{Resource: AdmissionTokens, ResetAt: resetAt}
	}
	return &ResourceLease{concurrency: concurrencyLease, tokens: tokens}, nil
}

// ResourceLease owns one concurrency slot and, when configured, one T089 token
// reservation. It must be used by pointer: its mutex and terminal ownership
// state must not be copied.
type ResourceLease struct {
	concurrency *Lease
	tokens      *TokenReservation

	mu             sync.Mutex
	finalized      bool
	finalErr       error
	deferredTicket *TokenAdjustmentTicket
}

// RequestLease is the request-lifecycle name for ResourceLease.
type RequestLease = ResourceLease

type leaseOutcome uint8

const (
	outcomeKnown leaseOutcome = iota
	outcomeConservative
	outcomeRelease
	outcomeDeferred
)

func (lease *ResourceLease) finish(outcome leaseOutcome, actual int64) (error, *TokenAdjustmentTicket) {
	if lease == nil {
		return nil, nil
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.finalized {
		return lease.finalErr, lease.deferredTicket
	}

	var ticket *TokenAdjustmentTicket
	var err error
	concurrency := lease.concurrency
	tokens := lease.tokens
	switch outcome {
	case outcomeKnown:
		if tokens != nil {
			err = tokens.Commit(actual)
		}
	case outcomeConservative:
		if tokens != nil {
			err = tokens.AbortConservative()
		}
	case outcomeRelease:
		if tokens != nil {
			err = tokens.ReleaseBeforeUpstream()
		}
	case outcomeDeferred:
		if tokens != nil {
			ticket, err = tokens.CommitDeferred()
		}
	}
	// A token finalization error must not strand concurrency. TokenLimiter's
	// terminal methods are themselves idempotent and settle active capacity.
	concurrency.releaseOwned()
	// Terminal ownership is consumed here. Deferred completion returns only the
	// independent adjustment ticket and keeps no concurrency or reservation
	// reference alive through the transport path.
	lease.concurrency = nil
	lease.tokens = nil
	lease.finalErr = err
	lease.deferredTicket = ticket
	lease.finalized = true
	return err, ticket
}

// CommitKnown reconciles the token reservation with actual total usage and
// releases concurrency exactly once.
func (lease *ResourceLease) CommitKnown(actual int64) error {
	err, _ := lease.finish(outcomeKnown, actual)
	return err
}

// Commit is the short spelling of CommitKnown.
func (lease *ResourceLease) Commit(actual int64) error {
	return lease.CommitKnown(actual)
}

// CompleteConservative settles ambiguous work with its reserved estimate and
// releases concurrency exactly once.
func (lease *ResourceLease) CompleteConservative() error {
	err, _ := lease.finish(outcomeConservative, 0)
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
	err, _ := lease.finish(outcomeRelease, 0)
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
	err, ticket := lease.finish(outcomeDeferred, 0)
	return ticket, err
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
