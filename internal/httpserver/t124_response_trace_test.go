package httpserver

import (
	"errors"
	"net/http"
	"testing"
)

type t124WriteResult struct {
	n   int
	err error
}

type t124ResponseWriter struct {
	header  http.Header
	status  int
	results []t124WriteResult
	flush   bool
}

func (writer *t124ResponseWriter) Header() http.Header { return writer.header }

func (writer *t124ResponseWriter) Write(body []byte) (int, error) {
	result := t124WriteResult{n: len(body)}
	if len(writer.results) != 0 {
		result, writer.results = writer.results[0], writer.results[1:]
	}
	return result.n, result.err
}

func (writer *t124ResponseWriter) WriteHeader(status int) {
	if writer.status == 0 {
		writer.status = status
	}
}

func (writer *t124ResponseWriter) Flush() { writer.flush = true }

func TestT124CompletionResponseWriterTracksStatusBytesAndTTFT(t *testing.T) {
	state, clock := traceTestState(t)
	underlying := &t124ResponseWriter{
		header:  make(http.Header),
		results: []t124WriteResult{{n: 2, err: ioShortWrite}, {n: 1}},
	}
	writer := &completionResponseWriter{ResponseWriter: underlying, trace: state}

	writer.WriteHeader(http.StatusCreated)
	writer.WriteHeader(http.StatusInternalServerError)
	if n, err := writer.Write([]byte("abcd")); n != 2 || !errors.Is(err, ioShortWrite) {
		t.Fatalf("short write = %d/%v", n, err)
	}
	clock.advance(2, 2)
	if n, err := writer.Write([]byte("x")); n != 1 || err != nil {
		t.Fatalf("successful write = %d/%v", n, err)
	}
	record, err := state.Complete()
	if err != nil {
		t.Fatal(err)
	}
	if status, ok := record.DownstreamStatus.Value(); !ok || status != http.StatusCreated {
		t.Fatalf("status = %d/%v, want 201/true", status, ok)
	}
	if delivered, ok := record.DeliveredBytes.Value(); !ok || delivered != 3 {
		t.Fatalf("delivered bytes = %d/%v, want 3/true", delivered, ok)
	}
	if !record.Timing.TimeToFirstByte.Known() {
		t.Fatal("accepted non-empty write did not set TTFT")
	}
}

func TestT124FlushAndUnsupportedControllerPreserveTraceSemantics(t *testing.T) {
	state, _ := traceTestState(t)
	underlying := &t124ResponseWriter{header: make(http.Header)}
	writer := &completionResponseWriter{ResponseWriter: underlying, trace: state}
	if err := http.NewResponseController(writer).Flush(); err != nil {
		t.Fatalf("flush = %v", err)
	}
	if !underlying.flush {
		t.Fatal("flush did not reach downstream")
	}
	writer.WriteHeader(http.StatusTeapot)
	record, err := state.Complete()
	if err != nil {
		t.Fatal(err)
	}
	if status, ok := record.DownstreamStatus.Value(); !ok || status != http.StatusOK {
		t.Fatalf("flush status = %d/%v, want 200/true", status, ok)
	}

	unsupportedState, _ := traceTestState(t)
	unsupported := &completionResponseWriter{ResponseWriter: &t124BasicResponseWriter{header: make(http.Header)}, trace: unsupportedState}
	if err := http.NewResponseController(unsupported).Flush(); !errors.Is(err, http.ErrNotSupported) {
		t.Fatalf("unsupported flush = %v, want %v", err, http.ErrNotSupported)
	}
	unsupportedRecord, err := unsupportedState.Complete()
	if err != nil {
		t.Fatal(err)
	}
	if unsupportedRecord.DownstreamStatus.Known() {
		t.Fatal("unsupported flush committed a status")
	}
}

type t124BasicResponseWriter struct{ header http.Header }

func (writer *t124BasicResponseWriter) Header() http.Header { return writer.header }
func (writer *t124BasicResponseWriter) Write(body []byte) (int, error) {
	return len(body), nil
}
func (writer *t124BasicResponseWriter) WriteHeader(int) {}

var ioShortWrite = errors.New("short write")
