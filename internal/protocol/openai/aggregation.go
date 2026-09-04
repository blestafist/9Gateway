package openai

import (
	"errors"
	"fmt"
	"io"

	"github.com/pestit/9gateway/internal/streaming"
)

// ErrEmptyStream indicates that an SSE input contained no complete events.
var ErrEmptyStream = errors.New("openai: empty SSE stream")

// ErrStreamIncomplete indicates that the bounded conversion input ended while
// an SSE event was still being framed. A clean EOF after complete meaningful
// response events is a successful completion and does not return this error.
var ErrStreamIncomplete = errors.New("openai: SSE stream contains an incomplete event")

// ErrInvalidAggregationLimit indicates that one of the conversion limits is
// not positive.
var ErrInvalidAggregationLimit = errors.New("openai: invalid aggregation limit")

// AggregateSSEToJSON consumes complete SSE events until an exact [DONE] data
// event or clean EOF and renders the observed response as one OpenAI
// chat-completion JSON document. It is a conversion-only driver: it never
// reads another event once [DONE] has been observed. Event framing and
// accumulated message/tool argument bytes are bounded independently by the
// supplied limits.
//
// Observer errors are returned immediately because this path is responsible
// for producing a response, unlike the best-effort observation driver used by
// passive telemetry. No partial JSON is returned on error.
func AggregateSSEToJSON(input io.Reader, maxEventSize int, maxPayloadBytes int64) ([]byte, error) {
	result, _, err := AggregateSSEToJSONWithTermination(input, maxEventSize, maxPayloadBytes)
	return result, err
}

// AggregateSSEToJSONWithTermination is the conversion driver used when the
// caller needs to distinguish exact [DONE] completion from clean EOF. The
// termination value is true only when a complete event whose data is exactly
// [DONE] ended the aggregation.
func AggregateSSEToJSONWithTermination(input io.Reader, maxEventSize int, maxPayloadBytes int64) ([]byte, bool, error) {
	if maxEventSize <= 0 || maxPayloadBytes <= 0 {
		return nil, false, fmt.Errorf("%w: event size=%d payload=%d", ErrInvalidAggregationLimit, maxEventSize, maxPayloadBytes)
	}

	reader, err := streaming.NewReader(input, maxEventSize)
	if err != nil {
		return nil, false, err
	}
	accumulator, err := NewChatAccumulator(maxPayloadBytes)
	if err != nil {
		return nil, false, err
	}
	observer := NewObserver()

	events := 0
	for {
		event, err := reader.Next()
		if err != nil {
			if err == io.EOF {
				if events == 0 {
					return nil, false, ErrEmptyStream
				}
				// Render performs the meaningful-response check. In particular,
				// usage may be supplied after a terminal choice event, and a
				// finish reason is not required for EOF completion.
				result, renderErr := accumulator.Render()
				return result, false, renderErr
			}
			if errors.Is(err, streaming.ErrEventIncomplete) {
				return nil, false, fmt.Errorf("%w: %w", ErrStreamIncomplete, err)
			}
			return nil, false, err
		}

		if event.Data == "[DONE]" {
			// Do not call Next again. In particular, bytes following DONE are
			// outside this conversion's input semantics.
			if events == 0 {
				return nil, false, fmt.Errorf("%w: DONE arrived without response data", ErrInvalidAccumulatorState)
			}
			result, renderErr := accumulator.Render()
			return result, renderErr == nil, renderErr
		}

		previousChoices := len(observer.state.Choices)
		if err := observer.Observe(event); err != nil {
			return nil, false, err
		}
		events++

		state := observer.State()
		result := ObservationResult{
			State:    state,
			Metadata: observer.Metadata(),
		}
		if previousChoices < len(state.Choices) {
			result.State.Choices = state.Choices[previousChoices:]
		} else {
			result.State.Choices = nil
		}
		if err := accumulator.Accumulate(result); err != nil {
			return nil, false, err
		}
	}
}
