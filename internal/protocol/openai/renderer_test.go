package openai

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestRenderChatCompletionRendersChoicesToolCallsUsageAndEscaping(t *testing.T) {
	finish := "stop"
	created := int64(1712345678)
	state := AccumulatorState{
		ResponseMetadata: ResponseMetadata{ID: "chatcmpl-\\\"界", Model: "model-\n1", Created: &created},
		Choices: []AccumulatedChoice{
			{
				Index:   4,
				Message: AccumulatedMessage{Role: "assistant", Content: "Hello, 世界\n\"quoted\""},
				ToolCalls: []AccumulatedToolCall{{
					Index: 2, ID: "call-2", Type: "function",
					Function: AccumulatedFunction{Name: "lookup", Arguments: "{\"city\":\"München\"}"},
				}},
				FinishReason: &finish,
			},
			{
				Index:        9,
				Message:      AccumulatedMessage{Role: "assistant", Content: "second"},
				FinishReason: nil,
			},
		},
		Usage: UsageObservation{InputTokens: int64Pointer(11), OutputTokens: int64Pointer(7), TotalTokens: int64Pointer(18)},
	}

	data, err := RenderChatCompletion(state)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) {
		t.Fatalf("rendered response is invalid JSON: %s", data)
	}
	if strings.Contains(string(data), "delta") {
		t.Fatalf("rendered response contains streaming-only delta: %s", data)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"id":      "chatcmpl-\\\"界",
		"object":  "chat.completion",
		"created": float64(created),
		"model":   "model-\n1",
		"choices": []any{
			map[string]any{
				"index": float64(4),
				"message": map[string]any{
					"role":    "assistant",
					"content": "Hello, 世界\n\"quoted\"",
					"tool_calls": []any{map[string]any{
						"index": float64(2), "id": "call-2", "type": "function",
						"function": map[string]any{"name": "lookup", "arguments": "{\"city\":\"München\"}"},
					}},
				},
				"finish_reason": "stop",
			},
			map[string]any{
				"index":         float64(9),
				"message":       map[string]any{"role": "assistant", "content": "second"},
				"finish_reason": nil,
			},
		},
		"usage": map[string]any{"prompt_tokens": float64(11), "completion_tokens": float64(7), "total_tokens": float64(18)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded response = %#v, want %#v", got, want)
	}
}

func TestRenderChatCompletionOmitsAbsentUsage(t *testing.T) {
	data, err := RenderChatCompletion(AccumulatorState{
		Choices: []AccumulatedChoice{{Message: AccumulatedMessage{Content: "content"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["usage"]; ok {
		t.Fatalf("usage = %s, want absent", got["usage"])
	}
}

func TestRenderChatCompletionRejectsEmptyState(t *testing.T) {
	for _, state := range []AccumulatorState{
		{},
		{Usage: UsageObservation{TotalTokens: int64Pointer(1)}},
		{Choices: []AccumulatedChoice{{Message: AccumulatedMessage{}}}},
	} {
		if data, err := RenderChatCompletion(state); !errors.Is(err, ErrInvalidAccumulatorState) || data != nil {
			t.Fatalf("RenderChatCompletion(%+v) = (%s, %v), want nil and invalid-state error", state, data, err)
		}
	}
}

func TestRenderChatCompletionAcceptsExplicitNullFinishReason(t *testing.T) {
	data, err := RenderChatCompletion(AccumulatorState{
		Choices: []AccumulatedChoice{{Index: 3, FinishReasonPresent: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	var choices []map[string]json.RawMessage
	if err := json.Unmarshal(got["choices"], &choices); err != nil {
		t.Fatal(err)
	}
	if string(choices[0]["finish_reason"]) != "null" {
		t.Fatalf("finish_reason = %s, want null", choices[0]["finish_reason"])
	}
}

func TestChatAccumulatorRender(t *testing.T) {
	var accumulator *ChatAccumulator
	if data, err := accumulator.Render(); !errors.Is(err, ErrInvalidAccumulatorState) || data != nil {
		t.Fatalf("nil accumulator Render() = (%s, %v), want nil and invalid-state error", data, err)
	}

	accumulator, err := NewChatAccumulator(32)
	if err != nil {
		t.Fatal(err)
	}
	if err := accumulator.Accumulate(ObservationResult{State: ObserverState{Choices: []ChoiceObservation{{
		Index: 0, Delta: DeltaObservation{Content: stringPointer("ok")},
	}}}}); err != nil {
		t.Fatal(err)
	}
	if data, err := accumulator.Render(); err != nil || !json.Valid(data) {
		t.Fatalf("accumulator Render() = (%s, %v), want valid JSON", data, err)
	}
}
