package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestRequestLifecycleDrainsAdmittedHandler(t *testing.T) {
	lifecycle := NewRequestLifecycle()
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	openRelease := func() { releaseOnce.Do(func() { close(release) }) }
	server := httptest.NewServer(lifecycle.Handler(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		_, _ = response.Write([]byte("complete"))
	})))
	t.Cleanup(func() {
		openRelease()
		lifecycle.StopAccepting()
		server.Close()
	})

	responseReady := make(chan *http.Response, 1)
	requestErr := make(chan error, 1)
	go func() {
		response, err := http.Get(server.URL)
		if err != nil {
			requestErr <- err
			return
		}
		responseReady <- response
	}()
	select {
	case <-entered:
	case err := <-requestErr:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("handler was not admitted")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := lifecycle.Wait(ctx); err == nil {
		t.Fatal("lifecycle reported drain before admitted handler completed")
	}

	openRelease()
	var response *http.Response
	select {
	case response = <-responseReady:
	case err := <-requestErr:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("handler did not complete")
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("response status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}

	lifecycle.StopAccepting()
	if err := lifecycle.Wait(context.Background()); err != nil {
		t.Fatal("lifecycle drain failed: ", err)
	}
	// Idempotence is part of the shutdown seam: cleanup may converge through
	// both the server owner and a test harness.
	lifecycle.StopAccepting()
}

func TestRequestLifecycleNilOwnerIsNoop(t *testing.T) {
	var lifecycle *RequestLifecycle
	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	lifecycle.Handler(next).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !called {
		t.Fatal("nil lifecycle did not invoke the wrapped handler")
	}
	lifecycle.StopAccepting()
	if err := lifecycle.Wait(nil); err != nil {
		t.Fatal("nil lifecycle wait failed: ", err)
	}
}

func TestRequestLifecycleStopsWithoutAdmittedHandlers(t *testing.T) {
	lifecycle := NewRequestLifecycle()
	lifecycle.StopAccepting()
	if err := lifecycle.Wait(context.Background()); err != nil {
		t.Fatal("empty lifecycle did not stop: ", err)
	}
}
