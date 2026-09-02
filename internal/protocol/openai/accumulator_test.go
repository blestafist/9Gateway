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

func TestChatAccumulatorReconstructsMessageDeltas(t *testing.T) {
	accumulator, err := NewChatAccumulator(64)
	if err != nil {
		t.Fatal(err)
	}

	role := "assistant"
	conflictingRole := "system"
	firstReason := "stop"
	replacementReason := "length"
	empty := ""
	unicodeBytes := []byte("界")
	unicodeFirst := string(unicodeBytes[:1])
	unicodeSecond := string(unicodeBytes[1:])

	observations := []ChoiceObservation{
		{Index: 7, Delta: DeltaObservation{Role: &role}},
		{Index: 7, Delta: DeltaObservation{Role: &conflictingRole, Content: stringPointer("TEST")}},
		{Index: 7, Delta: DeltaObservation{Content: &empty}},
		{Index: 7, Delta: DeltaObservation{Content: stringPointer("_OK")}, FinishReason: &firstReason},
		{Index: 7, Delta: DeltaObservation{Content: stringPointer(unicodeFirst)}},
		{Index: 7, Delta: DeltaObservation{Content: stringPointer(unicodeSecond)}, FinishReason: &replacementReason},
		{Index: 2, Delta: DeltaObservation{Content: stringPointer("other")}},
	}
	if err := accumulator.Accumulate(ObservationResult{State: ObserverState{Choices: observations}}); err != nil {
		t.Fatal(err)
	}

	state := accumulator.Snapshot()
	if len(state.Choices) != 2 || state.Choices[0].Index != 7 || state.Choices[1].Index != 2 {
		t.Fatalf("choice order = %+v, want indexes [7 2]", state.Choices)
	}
	choice := state.ChoicesByIndex[7]
	if choice.Message.Role != role {
		t.Fatalf("role = %q, want first non-empty role %q", choice.Message.Role, role)
	}
	if choice.Message.Content != "TEST_OK界" {
		t.Fatalf("content = %q, want TEST_OK界", choice.Message.Content)
	}
	if choice.FinishReason == nil || *choice.FinishReason != replacementReason {
		t.Fatalf("finish reason = %v, want latest non-nil reason %q", choice.FinishReason, replacementReason)
	}

	// A finish reason is metadata only: subsequent content remains accepted.
	if err := accumulator.Accumulate(ObservationResult{State: ObserverState{Choices: []ChoiceObservation{{
		Index: 7,
		Delta: DeltaObservation{Content: stringPointer(" after-finish")},
	}}}}); err != nil {
		t.Fatal(err)
	}
	if got := accumulator.Snapshot().ChoicesByIndex[7].Message.Content; got != "TEST_OK界 after-finish" {
		t.Fatalf("content after finish = %q, want TEST_OK界 after-finish", got)
	}
}

func TestChatAccumulatorReconstructsIndexedToolCallsInFirstObservedOrder(t *testing.T) {
	accumulator, err := NewChatAccumulator(128)
	if err != nil {
		t.Fatal(err)
	}

	toolCall := func(index int, id, callType, name, arguments string) ToolCallObservation {
		return ToolCallObservation{
			Index: index,
			ID:    id,
			Type:  callType,
			Function: ToolCallFunctionObservation{
				Name:      name,
				Arguments: arguments,
			},
		}
	}
	observations := []ChoiceObservation{
		{Index: 8, Delta: DeltaObservation{ToolCalls: []ToolCallObservation{
			toolCall(11, "call-11", "", "", `{"city":"`),
		}}},
		{Index: 8, Delta: DeltaObservation{ToolCalls: []ToolCallObservation{
			toolCall(4, "", "function", "lookup", `Paris"}`),
		}}},
		{Index: 8, Delta: DeltaObservation{ToolCalls: []ToolCallObservation{
			toolCall(11, "", "function", "lookup", `}`),
		}}},
		{Index: 8, Delta: DeltaObservation{ToolCalls: []ToolCallObservation{
			toolCall(11, "conflicting-id", "conflicting-type", "conflicting-name", ""),
		}}},
		{Index: 2, Delta: DeltaObservation{ToolCalls: []ToolCallObservation{
			toolCall(99, "call-99", "function", "other", `{"ok":true}`),
		}}},
	}

	if err := accumulator.Accumulate(ObservationResult{State: ObserverState{Choices: observations}}); err != nil {
		t.Fatal(err)
	}

	state := accumulator.Snapshot()
	if len(state.Choices) != 2 || state.Choices[0].Index != 8 || state.Choices[1].Index != 2 {
		t.Fatalf("choice order = %+v, want indexes [8 2]", state.Choices)
	}
	choice := state.ChoicesByIndex[8]
	if len(choice.ToolCalls) != 2 || choice.ToolCalls[0].Index != 11 || choice.ToolCalls[1].Index != 4 {
		t.Fatalf("tool-call order = %+v, want indexes [11 4]", choice.ToolCalls)
	}
	first := choice.ToolCalls[0]
	if first.ID != "call-11" || first.Type != "function" || first.Function.Name != "lookup" {
		t.Fatalf("first call metadata = %+v, want first non-empty metadata", first)
	}
	if first.Function.Arguments != `{"city":"}` {
		t.Fatalf("first call arguments = %q, want raw concatenation", first.Function.Arguments)
	}
	second := choice.ToolCalls[1]
	if second.ID != "" || second.Type != "function" || second.Function.Name != "lookup" || second.Function.Arguments != `Paris"}` {
		t.Fatalf("second call = %+v, want split metadata and raw arguments", second)
	}
	other := state.ChoicesByIndex[2]
	if len(other.ToolCalls) != 1 || other.ToolCalls[0].Index != 99 || other.ToolCalls[0].Function.Arguments != `{"ok":true}` {
		t.Fatalf("other choice tool calls = %+v", other.ToolCalls)
	}
}

func TestChatAccumulatorToolCallPayloadBoundIsSharedAndTransactional(t *testing.T) {
	accumulator, err := NewChatAccumulator(7)
	if err != nil {
		t.Fatal(err)
	}

	if err := accumulator.Accumulate(ObservationResult{State: ObserverState{Choices: []ChoiceObservation{{
		Index: 3,
		Delta: DeltaObservation{
			Content: stringPointer("ab"),
			ToolCalls: []ToolCallObservation{{
				Index:    12,
				Function: ToolCallFunctionObservation{Arguments: "12345"},
			}},
		},
	}}}}); err != nil {
		t.Fatalf("exact shared bound error = %v, want nil", err)
	}
	before := accumulator.Snapshot()
	if before.PayloadBytes != 7 || before.ChoicesByIndex[3].Message.Content != "ab" || before.ChoicesByIndex[3].ToolCalls[0].Function.Arguments != "12345" {
		t.Fatalf("exact-bound state = %+v", before)
	}

	overflowResult := ObservationResult{
		Metadata: ResponseMetadata{ID: "must-not-apply"},
		State: ObserverState{
			Choices: []ChoiceObservation{
				{
					Index: 3,
					Delta: DeltaObservation{
						ToolCalls: []ToolCallObservation{
							{
								Index: 12,
								Type:  "function",
								Function: ToolCallFunctionObservation{
									Name:      "must-not-apply",
									Arguments: "!",
								},
							},
						},
					},
				},
			},
		},
	}
	if err := accumulator.Accumulate(overflowResult); !errors.Is(err, ErrAccumulatorOverflow) {
		t.Fatalf("tool-call overflow error = %v, want ErrAccumulatorOverflow", err)
	}
	after := accumulator.Snapshot()
	if !after.Terminal || after.PayloadBytes != before.PayloadBytes || after.ID != before.ID || len(after.Choices) != 1 {
		t.Fatalf("state changed incorrectly on tool-call overflow: before=%+v after=%+v", before, after)
	}
	choice := after.ChoicesByIndex[3]
	if choice.Message.Content != "ab" || choice.ToolCalls[0].Function.Arguments != "12345" || choice.ToolCalls[0].Type != "" || choice.ToolCalls[0].Function.Name != "" {
		t.Fatalf("tool-call overflow leaked partial state: %+v", choice)
	}
}

func TestChatAccumulatorOverflowIsTransactionalAndTerminal(t *testing.T) {
	accumulator, err := NewChatAccumulator(4)
	if err != nil {
		t.Fatal(err)
	}
	role := "assistant"
	if err := accumulator.Accumulate(ObservationResult{State: ObserverState{Choices: []ChoiceObservation{{
		Index: 1,
		Delta: DeltaObservation{Role: &role, Content: stringPointer("ok")},
	}}}}); err != nil {
		t.Fatal(err)
	}
	before := accumulator.Snapshot()

	// The role and new choice must not leak when the content fragment overflows.
	if err := accumulator.Accumulate(ObservationResult{Metadata: ResponseMetadata{ID: "should-not-apply"}, State: ObserverState{Choices: []ChoiceObservation{{
		Index: 9,
		Delta: DeltaObservation{Role: stringPointer("user"), Content: stringPointer("!!!")},
	}}}}); !errors.Is(err, ErrAccumulatorOverflow) {
		t.Fatalf("overflow error = %v, want ErrAccumulatorOverflow", err)
	}
	after := accumulator.Snapshot()
	if !after.Terminal || after.PayloadBytes != before.PayloadBytes || len(after.Choices) != 1 || after.Choices[0].Message.Content != "ok" || after.ID != "" {
		t.Fatalf("state after overflow = %+v, want unchanged state marked terminal", after)
	}
	if err := accumulator.Accumulate(ObservationResult{State: ObserverState{Choices: []ChoiceObservation{{
		Index: 1,
		Delta: DeltaObservation{Content: stringPointer("later")},
	}}}}); !errors.Is(err, ErrAccumulatorOverflow) {
		t.Fatalf("post-overflow error = %v, want ErrAccumulatorOverflow", err)
	}
}

func stringPointer(value string) *string { return &value }
