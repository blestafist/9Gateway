package httpserver

import (
	"io"
	"net/http"
	"time"

	"github.com/pestit/9gateway/internal/accounting"
	"github.com/pestit/9gateway/internal/limiter"
)

// responseObservation is a post-forwarding side channel for one transparent
// JSON or SSE representation. It owns only bytes that were accepted by the
// downstream writer and flushed; it never participates in response buffering.
type responseObservation struct {
	maxBytes int64
	coding   ContentCoding

	bytes              []byte
	overflow           bool
	eligible           bool
	pricing            accounting.PricingResolution
	completion         *completionOwnership
	canonical          accounting.Usage
	canonicalKnown     bool
	canonicalCost      accounting.Money
	checkpoints        []streamCheckpoint
	checkpointBytes    int64
	checkpointOverflow bool
	wireOffset         int64
	checkpointAt       func() time.Time
	requestPricing     accounting.PricingResolver
	requestBody        *telemetryRequestBody
}

type streamCheckpoint struct {
	offset int64
	at     time.Time
}

const (
	maxStreamCheckpointCount       = 256
	maxStreamCheckpointBytes int64 = 8 * 1024 * 1024
)

func (observation *responseObservation) setCanonical(usage accounting.Usage, cost accounting.Money) {
	if observation != nil {
		observation.canonical, observation.canonicalKnown, observation.canonicalCost = usage, true, cost
	}
}

func newResponseObservation(maxBytes int64, coding ContentCoding, pricing ...accounting.PricingResolution) *responseObservation {
	if maxBytes <= 0 {
		maxBytes = DefaultUsageObservationMaxBytes
	}
	observation := &responseObservation{maxBytes: maxBytes, coding: coding}
	if len(pricing) != 0 {
		observation.pricing = pricing[0]
	}
	return observation
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

func (observation *responseObservation) checkpoint(written int) {
	if observation == nil || written <= 0 || observation.checkpointOverflow {
		return
	}
	if int64(written) > maxStreamCheckpointBytes-observation.checkpointBytes || len(observation.checkpoints) >= maxStreamCheckpointCount {
		observation.checkpointOverflow = true
		observation.checkpoints = nil
		return
	}
	observation.wireOffset += int64(written)
	observation.checkpointBytes += int64(written)
	if observation.checkpointAt != nil {
		observation.addCheckpoint(observation.checkpointAt())
	}
}

func (observation *responseObservation) addCheckpoint(at time.Time) {
	if observation == nil || observation.checkpointOverflow || len(observation.checkpoints) >= maxStreamCheckpointCount {
		return
	}
	observation.checkpoints = append(observation.checkpoints, streamCheckpoint{offset: observation.wireOffset, at: at})
}

func (observation *responseObservation) finish(err error) {
	if observation == nil {
		return
	}
	observation.eligible = err == nil && !observation.overflow
}

func (observation *responseObservation) settle(lease *limiter.ResourceLease, worker *UsageObservationWorker, budgetConservative ...bool) {
	if observation == nil {
		return
	}
	if observation.eligible {
		if observation.completion != nil && observation.canonicalKnown {
			observation.completion.finish(observation.canonical, observation.canonicalCost)
			// Conversion has already reconciled its lease synchronously. For a
			// transparent path this branch is reached only after the parser has
			// supplied the same canonical result.
			return
		}
		if lease == nil && observation.completion != nil {
			if worker == nil || !worker.submitForCompletionWithRequestOwned(observation.completion, observation.bytes, observation.coding, observation.checkpoints, observation.checkpointOverflow, observation.requestBody, observation.requestPricing) {
				observation.completion.finish(accounting.Usage{}, accounting.UnknownMoney())
			}
			return
		}
		if lease == nil {
			return
		}
		budgetSettled := len(budgetConservative) != 0 && budgetConservative[0]
		if worker == nil {
			// The convenience constructors do not own an observation worker. Both
			// resources must settle conservatively; creating an unowned ticket
			// here would leave deferred ownership stranded.
			_ = lease.CompleteConservative()
			return
		}
		if budgetSettled {
			if observation.pricing.Known() {
				worker.completeAndSubmitWithPricingTimingAndRequest(lease, observation.bytes, observation.coding, observation.pricing, observation.completion, observation.checkpoints, observation.checkpointOverflow, observation.requestBody, observation.requestPricing)
			} else {
				worker.completeAndSubmitWithPricingTimingAndRequest(lease, observation.bytes, observation.coding, accounting.UnknownPricingResolution(), observation.completion, observation.checkpoints, observation.checkpointOverflow, observation.requestBody, observation.requestPricing)
			}
		} else {
			worker.completeAndSubmitWithPricingTimingAndRequest(lease, observation.bytes, observation.coding, observation.pricing, observation.completion, observation.checkpoints, observation.checkpointOverflow, observation.requestBody, observation.requestPricing)
		}
		return
	}
	if lease != nil {
		_ = lease.CompleteConservative()
	}
	if observation.completion != nil {
		observation.completion.finish(accounting.Usage{}, accounting.UnknownMoney())
	}
}

type observedResponseWriter struct {
	http.ResponseWriter
	observation *responseObservation
}

func (writer *observedResponseWriter) completionWriter() *completionResponseWriter {
	return completionWriterFor(writer.ResponseWriter)
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
