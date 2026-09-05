package limiter

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/auth"
)

func TestResourceLeaseUnlimitedCombinations(t *testing.T) {
	clock := &testClock{now: time.Unix(1, 0).UTC()}
	concurrency := NewConcurrencyLimiter()
	tokens := NewTokenLimiter(clock.Now)
	coordinator := NewResourceLeaseCoordinator(concurrency, tokens)

	for name, options := range map[string]ResourceLeaseOptions{
		"both unlimited":   {KeyID: "both", MaxConcurrency: 0, TokenAmount: 10},
		"concurrency only": {KeyID: "concurrency", MaxConcurrency: 1},
		"tokens only":      {KeyID: "tokens", TokenWindows: tokenWindowForTest(), TokenAmount: 10},
		"both disabled":    {KeyID: "disabled"},
	} {
		t.Run(name, func(t *testing.T) {
			lease, rejection := coordinator.Acquire(options)
			if rejection != nil || lease == nil {
				t.Fatalf("acquire = (%v, %v)", lease, rejection)
			}
			if err := lease.ReleaseBeforeUpstream(); err != nil {
				t.Fatal(err)
			}
		})
	}
	if got := concurrency.Len(); got != 0 {
		t.Fatalf("concurrency states = %d, want 0", got)
	}
}

func TestResourceLeaseAcquiresConcurrencyFirstAndRollsBackOnTokenReject(t *testing.T) {
	clock := &testClock{now: time.Unix(1, 0).UTC()}
	concurrency := NewConcurrencyLimiter()
	tokens := NewTokenLimiter(clock.Now)
	coordinator := NewResourceLeaseCoordinator(concurrency, tokens)
	options := ResourceLeaseOptions{
		KeyID:          "key",
		MaxConcurrency: 1,
		TokenWindows:   []auth.TokenWindow{{Amount: 1, Duration: time.Minute}},
		TokenAmount:    1,
	}
	first, rejection := coordinator.Acquire(options)
	if rejection != nil || first == nil {
		t.Fatalf("first acquire = (%v, %v)", first, rejection)
	}
	second, rejection := coordinator.Acquire(options)
	if second != nil || rejection == nil || rejection.Resource != AdmissionConcurrency {
		t.Fatalf("saturated acquire = (%v, %v), want concurrency rejection", second, rejection)
	}
	if err := first.ReleaseBeforeUpstream(); err != nil {
		t.Fatal(err)
	}

	first, rejection = coordinator.Acquire(options)
	if rejection != nil || first == nil {
		t.Fatalf("second first acquire = (%v, %v)", first, rejection)
	}
	// The token is reserved, so this call reaches token admission only after
	// the concurrency slot has been acquired.
	second, rejection = coordinator.Acquire(options)
	if second != nil || rejection == nil || rejection.Resource != AdmissionConcurrency {
		t.Fatalf("token test acquire = (%v, %v), want concurrency rejection", second, rejection)
	}
	if err := first.ReleaseBeforeUpstream(); err != nil {
		t.Fatal(err)
	}

	// With a free slot, a token rejection must return that slot immediately.
	reservation, allowed, _ := tokens.Reserve("key", options.TokenWindows, 1)
	if !allowed {
		t.Fatal("setup reservation rejected")
	}
	failed, rejection := coordinator.Acquire(options)
	if failed != nil || rejection == nil || rejection.Resource != AdmissionTokens {
		t.Fatalf("token rejection = (%v, %v), want token rejection", failed, rejection)
	}
	if got := concurrency.Len(); got != 0 {
		t.Fatalf("rollback retained concurrency state = %d", got)
	}
	reservation.ReleaseBeforeUpstream()
	if reusable, rejection := coordinator.Acquire(options); rejection != nil || reusable == nil {
		t.Fatalf("capacity was not reusable = (%v, %v)", reusable, rejection)
	} else {
		reusable.ReleaseBeforeUpstream()
	}
}

func TestResourceLeaseTerminalOutcomesReleaseExactlyOnce(t *testing.T) {
	outcomes := []struct {
		name string
		end  func(*ResourceLease) error
	}{
		{"known", func(lease *ResourceLease) error { return lease.CommitKnown(3) }},
		{"conservative", (*ResourceLease).CompleteConservative},
		{"release", (*ResourceLease).ReleaseBeforeUpstream},
	}
	for _, test := range outcomes {
		t.Run(test.name, func(t *testing.T) {
			clock := &testClock{now: time.Unix(1, 0).UTC()}
			concurrency := NewConcurrencyLimiter()
			tokens := NewTokenLimiter(clock.Now)
			lease, rejection := NewResourceLeaseCoordinator(concurrency, tokens).Acquire(ResourceLeaseOptions{
				KeyID: "key", MaxConcurrency: 1, TokenWindows: tokenWindowForTest(), TokenAmount: 10,
			})
			if rejection != nil || lease == nil {
				t.Fatalf("acquire = (%v, %v)", lease, rejection)
			}
			if err := test.end(lease); err != nil {
				t.Fatal(err)
			}
			if err := test.end(lease); err != nil {
				t.Fatal(err)
			}
			if got := concurrency.Len(); got != 0 {
				t.Fatalf("concurrency states = %d, want 0", got)
			}
			if next, rejection := NewResourceLeaseCoordinator(concurrency, tokens).Acquire(ResourceLeaseOptions{KeyID: "key", MaxConcurrency: 1, TokenWindows: tokenWindowForTest(), TokenAmount: 1}); rejection != nil || next == nil {
				t.Fatalf("immediate reuse = (%v, %v)", next, rejection)
			} else {
				next.ReleaseBeforeUpstream()
			}
		})
	}
}

func TestResourceLeaseDeferredReleasesConcurrencyAndOnlyReturnsAdjustmentTicket(t *testing.T) {
	clock := &testClock{now: time.Unix(1, 0).UTC()}
	concurrency := NewConcurrencyLimiter()
	tokens := NewTokenLimiter(clock.Now)
	lease, rejection := NewResourceLeaseCoordinator(concurrency, tokens).Acquire(ResourceLeaseOptions{
		KeyID: "key", MaxConcurrency: 1, TokenWindows: tokenWindowForTest(), TokenAmount: 40,
	})
	if rejection != nil || lease == nil {
		t.Fatalf("acquire = (%v, %v)", lease, rejection)
	}
	ticket, err := lease.TransportComplete()
	if err != nil || ticket == nil {
		t.Fatalf("deferred completion = (%v, %v)", ticket, err)
	}
	if got := concurrency.Len(); got != 0 {
		t.Fatalf("deferred completion retained concurrency = %d", got)
	}
	if err := ticket.Adjust(10); err != nil {
		t.Fatal(err)
	}
	if next, rejection := NewResourceLeaseCoordinator(concurrency, tokens).Acquire(ResourceLeaseOptions{KeyID: "key", MaxConcurrency: 1, TokenWindows: tokenWindowForTest(), TokenAmount: 91}); rejection == nil {
		t.Fatal("conservative deferred charge did not remain accounted")
	} else if next != nil {
		t.Fatal("rejected deferred admission returned a lease")
	}
	if repeated, repeatedErr := lease.TransportComplete(); repeated != ticket || repeatedErr != nil {
		t.Fatalf("repeated deferred completion = (%v, %v)", repeated, repeatedErr)
	}
}

func TestResourceLeasePromotesProvisionalWithoutSecondConcurrencySlot(t *testing.T) {
	clock := &testClock{now: time.Unix(1, 0).UTC()}
	concurrency := NewConcurrencyLimiter()
	tokens := NewTokenLimiter(clock.Now)
	coordinator := NewResourceLeaseCoordinator(concurrency, tokens)
	provisional, admitted := concurrency.Acquire("key", 1)
	if !admitted {
		t.Fatal("provisional acquisition rejected")
	}
	lease, rejection := coordinator.AcquireWithProvisional(provisional, ResourceLeaseOptions{
		KeyID: "key", MaxConcurrency: 1, TokenWindows: tokenWindowForTest(), TokenAmount: 10,
	})
	if rejection != nil || lease == nil {
		t.Fatalf("promotion = (%v, %v)", lease, rejection)
	}
	provisional.Release()
	if next, rejection := coordinator.Acquire(ResourceLeaseOptions{KeyID: "key", MaxConcurrency: 1, TokenWindows: tokenWindowForTest(), TokenAmount: 1}); rejection == nil {
		t.Fatal("promoted lease did not retain the one slot")
	} else if next != nil {
		t.Fatal("saturated acquisition returned a lease")
	}
	lease.ReleaseBeforeUpstream()
	if next, rejection := coordinator.Acquire(ResourceLeaseOptions{KeyID: "key", MaxConcurrency: 1, TokenWindows: tokenWindowForTest(), TokenAmount: 1}); rejection != nil || next == nil {
		t.Fatalf("promoted slot not reusable = (%v, %v)", next, rejection)
	} else {
		next.ReleaseBeforeUpstream()
	}
}

func TestResourceLeaseConcurrentRepeatedCleanup(t *testing.T) {
	clock := &testClock{now: time.Unix(1, 0).UTC()}
	concurrency := NewConcurrencyLimiter()
	tokens := NewTokenLimiter(clock.Now)
	lease, rejection := NewResourceLeaseCoordinator(concurrency, tokens).Acquire(ResourceLeaseOptions{
		KeyID: "key", MaxConcurrency: 1, TokenWindows: tokenWindowForTest(), TokenAmount: 10,
	})
	if rejection != nil || lease == nil {
		t.Fatalf("acquire = (%v, %v)", lease, rejection)
	}
	var wait sync.WaitGroup
	for index := 0; index < 128; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			if index%2 == 0 {
				_ = lease.CompleteConservative()
			} else {
				_ = lease.ReleaseBeforeUpstream()
			}
		}(index)
	}
	wait.Wait()
	if got := concurrency.Len(); got != 0 {
		t.Fatalf("concurrent cleanup retained concurrency = %d", got)
	}
	if next, rejection := NewResourceLeaseCoordinator(concurrency, tokens).Acquire(ResourceLeaseOptions{KeyID: "key", MaxConcurrency: 1, TokenWindows: tokenWindowForTest(), TokenAmount: 1}); rejection != nil || next == nil {
		t.Fatal("capacity not immediately reusable")
	} else {
		next.ReleaseBeforeUpstream()
	}
}

func TestAdmissionErrorUnwrapsDomain(t *testing.T) {
	error := &AdmissionError{Resource: AdmissionTokens}
	if !errors.Is(error, ErrTokenUnavailable) {
		t.Fatal("token admission did not unwrap token domain error")
	}
}
