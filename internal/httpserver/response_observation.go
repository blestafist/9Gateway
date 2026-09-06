package httpserver

import (
	"io"
	"net/http"

	"github.com/pestit/9gateway/internal/limiter"
)

// responseObservation is a post-forwarding side channel for one transparent
// JSON or SSE representation. It owns only bytes that were accepted by the
// downstream writer and flushed; it never participates in response buffering.
type responseObservation struct {
	maxBytes int64
	coding   ContentCoding

	bytes    []byte
	overflow bool
	eligible bool
}

func newResponseObservation(maxBytes int64, coding ContentCoding) *responseObservation {
	if maxBytes <= 0 {
		maxBytes = DefaultUsageObservationMaxBytes
	}
	return &responseObservation{maxBytes: maxBytes, coding: coding}
}

func responseObservationCoding(header http.Header) (ContentCoding, error) {
	values := header.Values("Content-Encoding")
	if len(values) == 0 {
		return ContentCodingIdentity, nil
	}
	if len(values) != 1 {
		return 0, ErrUsageObservationUnsupported
	}
	return ValidateContentCoding(values[0])
}

func (observation *responseObservation) wrap(response http.ResponseWriter) http.ResponseWriter {
	return &observedResponseWriter{ResponseWriter: response, observation: observation}
}

func (observation *responseObservation) record(written []byte) {
	if observation == nil || len(written) == 0 || observation.overflow {
		return
	}
	remaining := observation.maxBytes - int64(len(observation.bytes))
	if remaining <= 0 {
		observation.overflow = true
		return
	}
	if int64(len(written)) > remaining {
		observation.bytes = append(observation.bytes, written[:remaining]...)
		observation.overflow = true
		return
	}
	observation.bytes = append(observation.bytes, written...)
}

func (observation *responseObservation) finish(err error) {
	if observation == nil {
		return
	}
	observation.eligible = err == nil && !observation.overflow
}

func (observation *responseObservation) settle(lease *limiter.ResourceLease, worker *UsageObservationWorker) {
	if observation == nil || lease == nil {
		return
	}
	if observation.eligible {
		worker.CompleteAndSubmit(lease, observation.bytes, observation.coding)
		return
	}
	lease.TransportComplete()
}

type observedResponseWriter struct {
	http.ResponseWriter
	observation *responseObservation
}

func (writer *observedResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func (writer *observedResponseWriter) Write(body []byte) (int, error) {
	written, err := writer.ResponseWriter.Write(body)
	if written > 0 {
		writer.observation.record(body[:min(written, len(body))])
	}
	if err == nil && written != len(body) {
		return written, io.ErrShortWrite
	}
	return written, err
}

func (writer *observedResponseWriter) ReadFrom(source io.Reader) (int64, error) {
	return io.Copy(struct{ io.Writer }{Writer: writer}, source)
}
