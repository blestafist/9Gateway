package openai

import (
	"errors"
	"fmt"
	"io"

	"github.com/pestit/9gateway/internal/streaming"
)

// ErrEmptyStream indicates that an SSE input contained no complete events.
var ErrEmptyStream = errors.New("openai: empty SSE stream")

// ErrStreamIncomplete indicates that the bounded T058 conversion input ended
// before the exact [DONE] sentinel. Clean EOF completion is intentionally left
// to the subsequent EOF-completion task.
var ErrStreamIncomplete = errors.New("openai: SSE stream ended before [DONE]")

// ErrInvalidAggregationLimit indicates that one of the conversion limits is
// not positive.
var ErrInvalidAggregationLimit = errors.New("openai: invalid aggregation limit")

// AggregateSSEToJSON consumes complete SSE events until an exact [DONE] data
// event and renders the observed response as one OpenAI chat-completion JSON
// document. It is a conversion-only driver: it never reads another event once
// [DONE] has been observed. Event framing and accumulated message/tool
// argument bytes are bounded independently by the supplied limits.
//
// Observer errors are returned immediately because this path is responsible
// for producing a response, unlike the best-effort observation driver used by
// passive telemetry. No partial JSON is returned on error.
func AggregateSSEToJSON(input io.Reader, maxEventSize int, maxPayloadBytes int64) ([]byte, error) {
	if maxEventSize <= 0 || maxPayloadBytes <= 0 {
		return nil, fmt.Errorf("%w: event size=%d payload=%d", ErrInvalidAggregationLimit, maxEventSize, maxPayloadBytes)
	}

	reader, err := streaming.NewReader(input, maxEventSize)
	if err != nil {
		return nil, err
	}
	accumulator, err := NewChatAccumulator(maxPayloadBytes)
	if err != nil {
		return nil, err
	}
	observer := NewObserver()

	chunks := 0
	for {
		event, err := reader.Next()
		if err != nil {
			if err == io.EOF {
				if chunks == 0 {
					return nil, ErrEmptyStream
				}
				return nil, ErrStreamIncomplete
			}
			return nil, err
		}

		if event.Data == "[DONE]" {
			// Do not call Next again. In particular, bytes following DONE are
			// outside this conversion's input semantics.
			if chunks == 0 {
				return nil, fmt.Errorf("%w: DONE arrived without response data", ErrInvalidAccumulatorState)
			}
			return accumulator.Render()
		}

		previousChoices := len(observer.state.Choices)
		if err := observer.Observe(event); err != nil {
			return nil, err
		}
		chunks++

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
			return nil, err
		}
	}
}
