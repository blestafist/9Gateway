package httpserver

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/transport"
)

func TestConcurrencyLimitIsHeldUntilResponseBodyCompletes(t *testing.T) {
	pepper := []byte("concurrency-http-pepper")
	clock := &requestLimitTestClock{now: time.Unix(1, 0).UTC()}
	key, authenticator := requestLimitTestAuthenticator(t, pepper, "key-a", `{"max_concurrent_requests":1}`, clock)
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	openRelease := func() { releaseOnce.Do(func() { close(release) }) }
	var startedOnce sync.Once
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		startedOnce.Do(func() { close(started) })
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusOK)
		<-release
		_, _ = io.WriteString(response, `{"ok":true}`)
	}))
	t.Cleanup(func() {
		openRelease()
		upstream.Close()
	})
	concurrencyLimiter := limiter.NewConcurrencyLimiter()
	gateway := httptest.NewServer(NewHandlerWithAuthenticatorAndLimiters(transport.NewClient(), upstream.URL, "upstream-secret", authenticator, limiter.NewRequestLimiter(nil), concurrencyLimiter, nil))
	t.Cleanup(gateway.Close)

	firstResult := make(chan *http.Response, 1)
	firstError := make(chan error, 1)
	go func() {
		request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/opaque", bytes.NewReader([]byte("first")))
		if err != nil {
			firstError <- err
			return
		}
		request.Header.Set("Authorization", "Bearer "+key.RawKey)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			firstError <- err
			return
		}
		firstResult <- response
	}()
	select {
	case <-started:
	case err := <-firstError:
		t.Fatalf("first request: %v", err)
	case <-time.After(time.Second):
		t.Fatal("upstream did not receive first request")
	}

	second := doAuthenticatedRequest(t, gateway.URL+"/v1/opaque", key.RawKey)
	secondBody, err := io.ReadAll(second.Body)
	second.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if second.StatusCode != http.StatusTooManyRequests || !bytes.Contains(secondBody, []byte(`"code":"concurrency_limit_exceeded"`)) {
		t.Fatalf("second response = %d/%q, want concurrency rejection", second.StatusCode, secondBody)
	}

	openRelease()
	var first *http.Response
	select {
	case first = <-firstResult:
	case err := <-firstError:
		t.Fatalf("first response: %v", err)
	case <-time.After(time.Second):
		t.Fatal("first request did not complete")
	}
	firstBody, err := io.ReadAll(first.Body)
	first.Body.Close()
	if err != nil || !bytes.Equal(firstBody, []byte(`{"ok":true}`)) {
		t.Fatalf("first body = %q, err = %v", firstBody, err)
	}

	third := doAuthenticatedRequest(t, gateway.URL+"/v1/opaque", key.RawKey)
	third.Body.Close()
	if third.StatusCode != http.StatusOK {
		t.Fatalf("third status = %d, want 200 after lease release", third.StatusCode)
	}
	if got := concurrencyLimiter.Len(); got != 0 {
		t.Fatalf("active concurrency states = %d, want 0", got)
	}
}

func TestConcurrencyLimitDifferentKeysRemainConcurrent(t *testing.T) {
	pepper := []byte("concurrency-keys-pepper")
	clock := &requestLimitTestClock{now: time.Unix(1, 0).UTC()}
	keyA, authenticator := requestLimitTestAuthenticator(t, pepper, "key-a", `{"max_concurrent_requests":1}`, clock)
	keyB, err := auth.GenerateGatewayKey(pepper)
	if err != nil {
		t.Fatal(err)
	}
	if err := authenticator.Load([]auth.Record{
		{ID: "key-a", DisplayPrefix: keyA.DisplayPrefix, Digest: keyA.Digest, Enabled: true, PolicyJSON: []byte(`{"max_concurrent_requests":1}`)},
		{ID: "key-b", DisplayPrefix: keyB.DisplayPrefix, Digest: keyB.Digest, Enabled: true, PolicyJSON: []byte(`{"max_concurrent_requests":1}`)},
	}); err != nil {
		t.Fatal(err)
	}
	started := make(chan string, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	openRelease := func() { releaseOnce.Do(func() { close(release) }) }
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		started <- request.Header.Get("X-Key")
		<-release
	}))
	t.Cleanup(func() {
		openRelease()
		upstream.Close()
	})
	gateway := httptest.NewServer(NewHandlerWithAuthenticatorAndLimiters(transport.NewClient(), upstream.URL, "upstream-secret", authenticator, nil, limiter.NewConcurrencyLimiter(), nil))
	t.Cleanup(gateway.Close)

	var wait sync.WaitGroup
	wait.Add(2)
	responses := make(chan *http.Response, 2)
	for _, key := range []auth.GeneratedGatewayKey{keyA, keyB} {
		go func(key auth.GeneratedGatewayKey) {
			defer wait.Done()
			responses <- doAuthenticatedRequest(t, gateway.URL+"/v1/opaque", key.RawKey)
		}(key)
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("different-key request was serialized")
		}
	}
	openRelease()
	wait.Wait()
	for range 2 {
		response := <-responses
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", response.StatusCode)
		}
	}
}

func doAuthenticatedRequest(t *testing.T, target, rawKey string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+rawKey)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
