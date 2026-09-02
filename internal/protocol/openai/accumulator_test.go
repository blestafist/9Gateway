package openai

import (
	"errors"
	"testing"
)

func TestNewChatAccumulatorValidatesPayloadLimit(t *testing.T) {
	for _, limit := range []int64{0, -1} {
		if accumulator, err := NewChatAccumulator(limit); !errors.Is(err, ErrInvalidAccumulatorLimit) || accumulator != nil {
			t.Fatalf("NewChatAccumulator(%d) = (%v, %v), want nil and invalid-limit error", limit, accumulator, err)
		}
	}
	if accumulator, err := NewChatAccumulator(1); err != nil || accumulator == nil {
		t.Fatalf("NewChatAccumulator(1) = (%v, %v), want accumulator", accumulator, err)
	}
}

func TestChatAccumulatorStartsEmpty(t *testing.T) {
	accumulator, err := NewChatAccumulator(64)
	if err != nil {
		t.Fatal(err)
	}
	state := accumulator.State()
	if state.ID != "" || state.Model != "" || state.Created != nil || state.Choices != nil || state.ChoicesByIndex != nil || state.PayloadBytes != 0 || state.Terminal {
		t.Fatalf("empty accumulator state = %+v", state)
	}
}

func TestChatAccumulatorPreservesIdentityChoiceOrderAndIndexes(t *testing.T) {
	accumulator, err := NewChatAccumulator(64)
	if err != nil {
		t.Fatal(err)
	}
	created := int64(123)
	result := ObservationResult{
		Metadata: ResponseMetadata{ID: "response-1", Model: "model-1", Created: &created},
		State: ObserverState{Choices: []ChoiceObservation{
			{Index: 7, Delta: DeltaObservation{Content: stringPointer("seven")}},
			{Index: 2, Delta: DeltaObservation{Content: stringPointer("two")}},
		}},
	}
	if err := accumulator.Accumulate(result); err != nil {
		t.Fatal(err)
	}
	state := accumulator.Snapshot()
	if state.ID != "response-1" || state.Model != "model-1" || state.Created == nil || *state.Created != created {
		t.Fatalf("identity = %+v, want response-1/model-1/123", state.ResponseMetadata)
	}
	if len(state.Choices) != 2 || state.Choices[0].Index != 7 || state.Choices[1].Index != 2 {
		t.Fatalf("choice order = %+v, want indexes [7 2]", state.Choices)
	}
	if state.ChoicesByIndex[7].Message.Content != "seven" || state.ChoicesByIndex[2].Message.Content != "two" {
		t.Fatalf("keyed choices = %+v", state.ChoicesByIndex)
	}
}

func TestChatAccumulatorExactLimitAndTerminalOverflow(t *testing.T) {
	accumulator, err := NewChatAccumulator(5)
	if err != nil {
		t.Fatal(err)
	}
	if err := accumulator.Add(ObservationResult{State: ObserverState{Choices: []ChoiceObservation{{Index: 4, Delta: DeltaObservation{Content: stringPointer("hello")}}}}}); err != nil {
		t.Fatalf("exact-limit Add error = %v, want nil", err)
	}
	before := accumulator.Snapshot()
	if err := accumulator.Add(ObservationResult{State: ObserverState{Choices: []ChoiceObservation{{Index: 4, Delta: DeltaObservation{Content: stringPointer("!")}}}}}); !errors.Is(err, ErrAccumulatorOverflow) {
		t.Fatalf("overflow Add error = %v, want overflow", err)
	}
	after := accumulator.Snapshot()
	if !after.Terminal || after.PayloadBytes != before.PayloadBytes || after.Choices[0].Message.Content != before.Choices[0].Message.Content {
		t.Fatalf("state changed on overflow: before=%+v after=%+v", before, after)
	}
	if err := accumulator.Add(ObservationResult{Metadata: ResponseMetadata{ID: "late"}}); !errors.Is(err, ErrAccumulatorOverflow) {
		t.Fatalf("post-overflow Add error = %v, want overflow", err)
	}
	if got := accumulator.Snapshot().ID; got != "" {
		t.Fatalf("post-overflow metadata ID = %q, want empty", got)
	}
}

func TestChatAccumulatorSnapshotIsCallerSafe(t *testing.T) {
	accumulator, err := NewChatAccumulator(64)
	if err != nil {
		t.Fatal(err)
	}
	created := int64(1)
	if err := accumulator.Accumulate(ObservationResult{
		Metadata: ResponseMetadata{Created: &created},
		State:    ObserverState{Choices: []ChoiceObservation{{Index: 1, Delta: DeltaObservation{ToolCalls: []ToolCallObservation{{Index: 3, Function: ToolCallFunctionObservation{Arguments: "x"}}}}}}},
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := accumulator.State()
	snapshot.Created = &created
	*snapshot.Created = 9
	snapshot.Choices[0].ToolCalls[0].Function.Arguments = "changed"
	snapshot.ChoicesByIndex[1].ToolCalls[0].Function.Arguments = "changed-again"
	if state := accumulator.State(); *state.Created != 1 || state.Choices[0].ToolCalls[0].Function.Arguments != "x" || state.ChoicesByIndex[1].ToolCalls[0].Function.Arguments != "x" {
		t.Fatalf("snapshot mutation changed accumulator: %+v", state)
	}
}

func stringPointer(value string) *string { return &value }
