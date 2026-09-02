package openai

import (
	"io"

	"github.com/pestit/9gateway/internal/streaming"
)

// ObservationResult is the snapshot produced by a best-effort stream
// observation. Errors contains only errors returned by Observer.Observe;
// errors from the generic reader are returned by ObserveStream instead.
type ObservationResult struct {
	State    ObserverState
	Metadata ResponseMetadata
	Errors   []error
}

// ObserveStream pulls complete, bounded SSE events from input and passes each
// one to a new OpenAI Observer. Observer errors are collected and, when
// reportError is non-nil, reported to it before observation continues. A
// reader error (including a framing or size error) ends observation and is
// returned without panicking; events successfully observed before that error
// remain in the result.
//
// This driver is deliberately not part of transparent transport. If a future
// live path uses it, that path must observe copied bytes only after writing and
// flushing them downstream. Observation may be dropped or disabled rather
// than blocking delivery.
func ObserveStream(input io.Reader, maxEventSize int, reportError func(error)) (ObservationResult, error) {
	observer := NewObserver()
	result := ObservationResult{}

	reader, err := streaming.NewReader(input, maxEventSize)
	if err != nil {
		return result, err
	}

	for {
		event, err := reader.Next()
		if err != nil {
			result.State = observer.State()
			result.Metadata = observer.Metadata()
			return result, normalizeObservationEOF(err)
		}

		if err := observer.Observe(event); err != nil {
			result.Errors = append(result.Errors, err)
			if reportError != nil {
				reportError(err)
			}
		}
	}
}

func normalizeObservationEOF(err error) error {
	if err == io.EOF {
		return nil
	}
	return err
}
