package httpserver

import (
	"context"
	"net/http"
	"sync"
)

// RequestLifecycle tracks handlers through the same accept/drain boundary as
// the production HTTP server. The sentinel keeps Add safe until StopAccepting
// is called after http.Server.Shutdown has closed the listener.
type RequestLifecycle struct {
	active sync.WaitGroup
	once   sync.Once
}

func NewRequestLifecycle() *RequestLifecycle {
	lifecycle := &RequestLifecycle{}
	lifecycle.active.Add(1)
	return lifecycle
}

// Handler wraps one gateway handler and records its complete lifetime,
// including deferred completion/history handoff.
func (lifecycle *RequestLifecycle) Handler(next http.Handler) http.Handler {
	if lifecycle == nil {
		return next
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		lifecycle.active.Add(1)
		defer lifecycle.active.Done()
		next.ServeHTTP(response, request)
	})
}

// StopAccepting releases the sentinel after the HTTP server has drained its
// listener. It is idempotent so cleanup paths can safely converge.
func (lifecycle *RequestLifecycle) StopAccepting() {
	if lifecycle != nil {
		lifecycle.once.Do(func() { lifecycle.active.Done() })
	}
}

// Wait joins all handlers that crossed the accept boundary.
func (lifecycle *RequestLifecycle) Wait(ctx context.Context) error {
	if lifecycle == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	done := make(chan struct{})
	go func() {
		lifecycle.active.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
