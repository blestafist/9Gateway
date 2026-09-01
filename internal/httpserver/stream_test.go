package httpserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type streamingUpstreamScript struct {
	contentType         string
	status              int
	fragments           []string
	flushEach           bool
	waitAfterFirst      bool
	waitForCancellation bool
}

type streamingUpstream struct {
	server            *httptest.Server
	URL               string
	arrivals          chan struct{}
	firstFlushed      chan struct{}
	releaseAfterFirst chan struct{}
	cancelled         chan struct{}
	returned          chan time.Time
	releaseOnce       sync.Once
}

func newStreamingUpstream(t *testing.T, script streamingUpstreamScript) *streamingUpstream {
	t.Helper()

	upstream := &streamingUpstream{
		arrivals:          make(chan struct{}, 32),
		firstFlushed:      make(chan struct{}, 32),
		releaseAfterFirst: make(chan struct{}),
		cancelled:         make(chan struct{}, 32),
		returned:          make(chan time.Time, 32),
	}
	upstream.server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		defer func() { trySendTime(upstream.returned, time.Now()) }()
		if script.waitForCancellation {
			defer func() {
				<-request.Context().Done()
				trySend(upstream.cancelled)
			}()
		}

		trySend(upstream.arrivals)
		if script.contentType != "" {
			response.Header().Set("Content-Type", script.contentType)
		}
		if script.status != 0 {
			response.WriteHeader(script.status)
		}

		for index, fragment := range script.fragments {
			if _, err := io.WriteString(response, fragment); err != nil {
				return
			}
			if script.flushEach {
				flusher, ok := response.(http.Flusher)
				if !ok {
					return
				}
				flusher.Flush()
				trySend(upstream.firstFlushed)
			}
			if index == 0 {
				if script.waitAfterFirst {
					<-upstream.releaseAfterFirst
				}
			}
		}

		if script.waitForCancellation {
			<-request.Context().Done()
		}
	}))
	upstream.URL = upstream.server.URL
	t.Cleanup(func() {
		upstream.release()
		upstream.server.CloseClientConnections()
		upstream.server.Close()
	})
	return upstream
}

func (upstream *streamingUpstream) release() {
	upstream.releaseOnce.Do(func() { close(upstream.releaseAfterFirst) })
}

func trySend(channel chan struct{}) {
	select {
	case channel <- struct{}{}:
	default:
	}
}

func trySendTime(channel chan time.Time, value time.Time) {
	select {
	case channel <- value:
	default:
	}
}
