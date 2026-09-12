package openai

import (
	"bytes"
	"encoding/json"
	"io"
	"time"

	"github.com/pestit/9gateway/internal/streaming"
)

// ObservationResult is the snapshot produced by a best-effort stream
// observation. Errors contains only errors returned by Observer.Observe;
// errors from the generic reader are returned by ObserveStream instead.
type ObservationResult struct {
	State                ObserverState
	Metadata             ResponseMetadata
	Errors               []error
	LastMeaningfulOffset int64
	LastMeaningfulAt     time.Time
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
	return ObserveStreamWithTiming(input, maxEventSize, reportError, nil)
}

func ObserveStreamWithTiming(input io.Reader, maxEventSize int, reportError func(error), meaningful func(int64) time.Time) (ObservationResult, error) {
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

		// Once DONE is seen, continue reading only to validate physical SSE
		// framing through EOF. Post-DONE events cannot contribute usage (or any
		// other observed state), but an incomplete trailing frame must still
		// retain conservative accounting for transparent observations.
		if observer.state.DoneObserved {
			trailing := NewObserver()
			if err := trailing.Observe(event); err != nil {
				result.Errors = append(result.Errors, err)
				if reportError != nil {
					reportError(err)
				}
			}
			continue
		}
		previousChoices := len(observer.state.Choices)
		previousUsageKnown := meaningfulUsage(observer.state.Usage)
		observed := false
		if err := observer.Observe(event); err != nil {
			result.Errors = append(result.Errors, err)
			if reportError != nil {
				reportError(err)
			}
		} else if event.Data != "[DONE]" {
			state := observer.State()
			observed = meaningfulEvent(event, state, previousChoices, previousUsageKnown)
		}
		if observed {
			result.LastMeaningfulOffset = reader.LastEventEndOffset()
			if meaningful != nil {
				result.LastMeaningfulAt = meaningful(result.LastMeaningfulOffset)
			}
		}
	}
}

// meaningfulObservation mirrors the response aggregation semantics rather than
// treating every valid JSON object as a response event. Metadata-only objects,
// empty deltas, comments, and [DONE] do not advance stream timing.
func meaningfulObservation(state ObserverState, previousChoices int, previousUsageKnown bool) bool {
	if meaningfulUsage(state.Usage) && !previousUsageKnown {
		return true
	}
	if previousChoices < 0 || previousChoices > len(state.Choices) {
		previousChoices = len(state.Choices)
	}
	for _, choice := range state.Choices[previousChoices:] {
		if choice.FinishReasonPresent || choice.FinishReason != nil ||
			(choice.Delta.Content != nil && *choice.Delta.Content != "") || len(choice.Delta.ToolCalls) != 0 {
			return true
		}
	}
	return false
}

// meaningfulEvent preserves event-level semantics that are lost in the
// cumulative observer snapshot, notably a usage-only terminal event after an
// earlier usage event. The extra bounded decode is off the transport path.
func meaningfulEvent(event streaming.SSEEvent, state ObserverState, previousChoices int, previousUsageKnown bool) bool {
	if meaningfulObservation(state, previousChoices, previousUsageKnown) {
		return true
	}
	var chunk meaningfulChunk
	if json.Unmarshal([]byte(event.Data), &chunk) != nil {
		return false
	}
	if jsonUsageMeaningful(chunk.Usage) {
		return true
	}
	if chunk.Response != nil {
		var nested map[string]json.RawMessage
		if json.Unmarshal(chunk.Response, &nested) == nil {
			if usage, ok := nested["usage"]; ok {
				if jsonUsageMeaningful(usage) {
					return true
				}
			}
		}
	}
	for _, choice := range chunk.Choices {
		if choice.FinishReason != nil {
			return true
		}
		if choice.Delta.Content != nil && *choice.Delta.Content != "" {
			return true
		}
		if choice.Delta.Reasoning != nil && *choice.Delta.Reasoning != "" {
			return true
		}
		if choice.Delta.ReasoningContent != nil && *choice.Delta.ReasoningContent != "" {
			return true
		}
		if nonEmptyJSONArray(choice.Delta.ToolCalls) {
			return true
		}
	}
	return false
}

func nonEmptyJSONArray(raw json.RawMessage) bool {
	if len(bytes.TrimSpace(raw)) == 0 {
		return false
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return false
	}
	return len(values) != 0
}

type meaningfulChunk struct {
	Choices  []meaningfulChoice `json:"choices"`
	Usage    json.RawMessage    `json:"usage"`
	Response json.RawMessage    `json:"response"`
}

type meaningfulChoice struct {
	Delta        meaningfulDelta `json:"delta"`
	FinishReason json.RawMessage `json:"finish_reason"`
}

type meaningfulDelta struct {
	Content          *string         `json:"content"`
	Reasoning        *string         `json:"reasoning"`
	ReasoningContent *string         `json:"reasoning_content"`
	ToolCalls        json.RawMessage `json:"tool_calls"`
}

func jsonUsageMeaningful(raw json.RawMessage) bool {
	parsed, err := parseJSONUsageObject(raw)
	return err == nil && parsed.Observed
}

func meaningfulUsage(usage UsageObservation) bool {
	return usage.Input().Known() || usage.Output().Known() || usage.Total().Known() ||
		usage.CachedInput().Known() || usage.ReasoningOutput().Known()
}

func normalizeObservationEOF(err error) error {
	if err == io.EOF {
		return nil
	}
	return err
}
