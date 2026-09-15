package httpserver

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/observability"
	"github.com/pestit/9gateway/internal/transport"
)

func TestT135ResponseBodyCaptureRetainsFlushedSSEAndConvertedJSON(t *testing.T) {
	const sse = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: {\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\ndata: [DONE]\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		if request.URL.Path == "/v1/chat/completions" {
			_, _ = io.WriteString(response, sse)
			return
		}
		_, _ = io.WriteString(response, sse)
	}))
	t.Cleanup(upstream.Close)

	key, authenticator := t134Authenticator(t, `{"log_response_body":true}`)
	var traces []*RequestTraceState
	proxy := withGatewayAuthentication(authenticator, newProxyHandlerWithLimitersAndTokenConfig(
		transport.NewClient(), upstream.URL, "upstream", limiter.NewRequestLimiter(nil),
		limiter.NewConcurrencyLimiter(), TokenAdmissionConfig{MaxCapturedBodyBytes: 4096},
	))
	handler := withRequestID(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		traces = append(traces, TraceFromContext(request.Context()))
		proxy.ServeHTTP(response, request)
	}))
	gateway := httptest.NewServer(handler)
	t.Cleanup(gateway.Close)

	transparent, err := http.NewRequest(http.MethodGet, gateway.URL+"/v1/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	transparent.Header.Set("Authorization", "Bearer "+key.RawKey)
	response, err := http.DefaultClient.Do(transparent)
	if err != nil {
		t.Fatal(err)
	}
	transparentBody, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(transparentBody, []byte(sse)) {
		t.Fatalf("transparent body = %q, want %q", transparentBody, sse)
	}
	transparentSnapshot, ok := traces[len(traces)-1].ResponseBodySnapshot()
	if !ok || transparentSnapshot.Kind != observability.BodyKindResponse || !transparentSnapshot.Captured || transparentSnapshot.Truncated || !bytes.Equal(transparentSnapshot.Bytes, transparentBody) || transparentSnapshot.OriginalSize != int64(len(transparentBody)) {
		t.Fatalf("transparent snapshot = %s/%v, want flushed wire body", transparentSnapshot, ok)
	}

	converted, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", bytes.NewBufferString(`{"model":"capture","stream":false}`))
	if err != nil {
		t.Fatal(err)
	}
	converted.Header.Set("Authorization", "Bearer "+key.RawKey)
	converted.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(converted)
	if err != nil {
		t.Fatal(err)
	}
	convertedBody, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	convertedSnapshot, ok := traces[len(traces)-1].ResponseBodySnapshot()
	if !ok || convertedSnapshot.Kind != observability.BodyKindResponse || !convertedSnapshot.Captured || convertedSnapshot.Truncated || !bytes.Equal(convertedSnapshot.Bytes, convertedBody) || convertedSnapshot.OriginalSize != int64(len(convertedBody)) {
		t.Fatalf("converted snapshot = %s/%v, want generated JSON body", convertedSnapshot, ok)
	}
	if !bytes.Contains(convertedBody, []byte(`"content":"hello"`)) {
		t.Fatalf("converted body = %s, want generated completion JSON", convertedBody)
	}
}

func TestT135TransparentCaptureRequiresSuccessfulFlush(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "json", body: `{"id":"json"}`},
		{name: "opaque", body: "\x00\xffopaque"},
	} {
		t.Run(test.name, func(t *testing.T) {
			flushErr := errors.New("flush failed")
			underlying := &t135FlushErrorWriter{header: make(http.Header), err: flushErr}
			recorder, err := observability.NewBodyRecorder(observability.BodyKindResponse, 16)
			if err != nil {
				t.Fatal(err)
			}
			trace, _ := traceTestState(t)
			writer := &completionResponseWriter{ResponseWriter: underlying, trace: trace, responseBodyRecorder: recorder, responseBodyAfterFlush: true}
			observation := newResponseObservation(16, ContentCodingIdentity)
			if err := streamResponseBody(writer, bytes.NewBufferString(test.body), observation); !errors.Is(err, flushErr) {
				t.Fatalf("stream error = %v, want %v", err, flushErr)
			} else {
				observation.finish(err)
			}
			snapshot := recorder.Finalize()
			if !snapshot.Captured || snapshot.OriginalSize != 0 || len(snapshot.Bytes) != 0 {
				t.Fatalf("capture after failed flush = %s, want known empty snapshot", snapshot)
			}
			if len(observation.bytes) != 0 {
				t.Fatalf("observation after failed flush = %q, want empty", observation.bytes)
			}
			record, err := trace.Final()
			if err != nil {
				t.Fatal(err)
			}
			if delivered, known := record.DeliveredBytes.Value(); known && delivered != 0 {
				t.Fatalf("delivered bytes after failed flush = %d/%v, want no delivered bytes", delivered, known)
			}
		})
	}
}

func TestT135TransparentShortWriteCapturesAcceptedFlushedPrefix(t *testing.T) {
	underlying := &t135ShortWriteWriter{header: make(http.Header), accepted: 3}
	recorder, err := observability.NewBodyRecorder(observability.BodyKindResponse, 16)
	if err != nil {
		t.Fatal(err)
	}
	writer := &completionResponseWriter{ResponseWriter: underlying, responseBodyRecorder: recorder, responseBodyAfterFlush: true}
	if err := streamResponseBody(writer, bytes.NewBufferString("fragment")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("stream error = %v, want %v", err, io.ErrShortWrite)
	}
	snapshot := recorder.Finalize()
	if !bytes.Equal(snapshot.Bytes, []byte("fra")) || snapshot.OriginalSize != 3 || !snapshot.Captured || underlying.flushes != 1 {
		t.Fatalf("short-write capture = %s, flushes = %d", snapshot, underlying.flushes)
	}
}

type t135FlushErrorWriter struct {
	header http.Header
	err    error
}

func (writer *t135FlushErrorWriter) Header() http.Header { return writer.header }
func (writer *t135FlushErrorWriter) Write(body []byte) (int, error) {
	return len(body), nil
}
func (writer *t135FlushErrorWriter) WriteHeader(int)   {}
func (writer *t135FlushErrorWriter) FlushError() error { return writer.err }

type t135ShortWriteWriter struct {
	header   http.Header
	accepted int
	flushes  int
}

func (writer *t135ShortWriteWriter) Header() http.Header { return writer.header }
func (writer *t135ShortWriteWriter) Write(body []byte) (int, error) {
	if writer.accepted > len(body) {
		return len(body), nil
	}
	return writer.accepted, nil
}
func (writer *t135ShortWriteWriter) WriteHeader(int) {}
func (writer *t135ShortWriteWriter) FlushError() error {
	writer.flushes++
	return nil
}
