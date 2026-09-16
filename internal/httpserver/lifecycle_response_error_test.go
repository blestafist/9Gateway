package httpserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTransparentDispatchFailuresSetResponseErrorTerminal(t *testing.T) {
	writeFailure := errors.New("downstream write failed")
	flushFailure := errors.New("downstream flush failed")
	tests := []struct {
		name          string
		contentType   string
		upstreamBody  string
		writer        *lifecycleFailureResponseWriter
		wantBody      string
		wantErrorCode SafeErrorCode
	}{
		{
			name:          "json write",
			contentType:   "application/json",
			upstreamBody:  `{"ok":true}`,
			writer:        &lifecycleFailureResponseWriter{writeErr: writeFailure},
			wantErrorCode: ErrorCodeResponseTransport,
		},
		{
			name:          "opaque write",
			contentType:   "application/octet-stream",
			upstreamBody:  "opaque",
			writer:        &lifecycleFailureResponseWriter{writeErr: writeFailure},
			wantErrorCode: ErrorCodeResponseTransport,
		},
		{
			name:          "SSE flush",
			contentType:   "text/event-stream",
			upstreamBody:  "data: fragment\n\n",
			writer:        &lifecycleFailureResponseWriter{flushErr: flushFailure},
			wantBody:      "data: fragment\n\n",
			wantErrorCode: ErrorCodeResponseTransport,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := lifecycleResponse(t, test.contentType, test.upstreamBody)
			proxy := newProxyHandler(&http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return upstream, nil
			})}, "http://router.example.test", "upstream-secret")
			var trace *RequestTraceState
			var terminal *terminalMetadataState
			handler := withRequestID(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				trace = TraceFromContext(request.Context())
				terminal = terminalMetadataFromContext(request.Context())
				route(proxy).ServeHTTP(response, request)
			}))
			request := httptest.NewRequest(http.MethodGet, "http://gateway.example.test/v1/dispatch", nil)
			handler.ServeHTTP(test.writer, request)

			record, err := trace.FreezeBase()
			if err != nil {
				t.Fatal(err)
			}
			if record.Terminal.Outcome != TerminalOutcomeResponseError {
				t.Fatalf("terminal outcome = %q, metadata=%#v, code=%q, status=%d body=%q, want %q", record.Terminal.Outcome, terminal.get(), record.ErrorCode, test.writer.status, test.writer.body.String(), TerminalOutcomeResponseError)
			}
			if record.ErrorCode != test.wantErrorCode || record.SafeErrorCode != test.wantErrorCode {
				t.Fatalf("error code = %q/%q, want %q", record.ErrorCode, record.SafeErrorCode, test.wantErrorCode)
			}
			if got := string(test.writer.body.Bytes()); got != test.wantBody {
				t.Fatalf("downstream body = %q, want %q", got, test.wantBody)
			}
		})
	}
}

func TestSSEConversionFailureKeepsConversionCodeAndResponseErrorTerminal(t *testing.T) {
	upstream := lifecycleResponse(t, "text/event-stream", "data: not-json\n\n")
	proxy := newProxyHandler(&http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return upstream, nil
	})}, "http://router.example.test", "upstream-secret")
	var trace *RequestTraceState
	handler := withRequestID(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		trace = TraceFromContext(request.Context())
		route(proxy).ServeHTTP(response, request)
	}))
	request := httptest.NewRequest(http.MethodPost, "http://gateway.example.test/v1/chat/completions", strings.NewReader(`{"model":"test","stream":false}`))
	request.Header.Set("Content-Type", "application/json")
	writer := &lifecycleFailureResponseWriter{}
	handler.ServeHTTP(writer, request)

	record, err := trace.FreezeBase()
	if err != nil {
		t.Fatal(err)
	}
	if record.Terminal.Outcome != TerminalOutcomeResponseError {
		t.Fatalf("terminal outcome = %q, want %q", record.Terminal.Outcome, TerminalOutcomeResponseError)
	}
	if record.ErrorCode != ErrorCodeConversion || record.SafeErrorCode != ErrorCodeConversion {
		t.Fatalf("error code = %q/%q, want %q", record.ErrorCode, record.SafeErrorCode, ErrorCodeConversion)
	}
	if writer.status != http.StatusBadGateway {
		t.Fatalf("conversion status = %d, want %d", writer.status, http.StatusBadGateway)
	}
	if bytes.Contains(writer.body.Bytes(), []byte("not-json")) {
		t.Fatal("conversion response leaked upstream body")
	}
}

func TestActiveSSEGatewayDeadlineIsUpstreamTimeout(t *testing.T) {
	proxy := newProxyHandler(&http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       &deadlineResponseBody{ctx: request.Context()},
		}, nil
	})}, "http://router.example.test", "upstream-secret")
	proxy.upstreamRequestTimeout = 20 * time.Millisecond
	var trace *RequestTraceState
	handler := withRequestID(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		trace = TraceFromContext(request.Context())
		route(proxy).ServeHTTP(response, request)
	}))
	handler.ServeHTTP(&lifecycleFailureResponseWriter{}, httptest.NewRequest(http.MethodGet, "http://gateway.example.test/v1/stream", nil))

	record, err := trace.FreezeBase()
	if err != nil {
		t.Fatal(err)
	}
	if record.Terminal.Outcome != TerminalOutcomeUpstreamError || record.ErrorCode != ErrorCodeUpstreamTimeout {
		t.Fatalf("terminal/code = %q/%q, want %q/%q", record.Terminal.Outcome, record.ErrorCode, TerminalOutcomeUpstreamError, ErrorCodeUpstreamTimeout)
	}
}

type deadlineResponseBody struct {
	ctx context.Context
}

func (body *deadlineResponseBody) Read([]byte) (int, error) {
	<-body.ctx.Done()
	return 0, body.ctx.Err()
}

func (*deadlineResponseBody) Close() error { return nil }

func lifecycleResponse(t *testing.T, contentType, body string) *http.Response {
	t.Helper()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

type lifecycleFailureResponseWriter struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	writeErr error
	flushErr error
}

func (writer *lifecycleFailureResponseWriter) Header() http.Header {
	if writer.header == nil {
		writer.header = make(http.Header)
	}
	return writer.header
}

func (writer *lifecycleFailureResponseWriter) WriteHeader(status int) {
	if writer.status == 0 {
		writer.status = status
	}
}

func (writer *lifecycleFailureResponseWriter) Write(body []byte) (int, error) {
	if writer.writeErr != nil {
		return 0, writer.writeErr
	}
	return writer.body.Write(body)
}

func (writer *lifecycleFailureResponseWriter) Flush() {
	_ = writer.FlushError()
}

func (writer *lifecycleFailureResponseWriter) FlushError() error {
	if writer.flushErr != nil {
		return writer.flushErr
	}
	return nil
}
