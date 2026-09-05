package openai

import (
	"errors"
	"testing"

	"github.com/pestit/9gateway/internal/accounting"
	"github.com/pestit/9gateway/internal/streaming"
)

func TestObserverAcceptsValidJSONChunk(t *testing.T) {
	observer := NewObserver()

	err := observer.Observe(streaming.SSEEvent{Data: `{"id":"chunk-1","object":"chat.completion.chunk","created":123,"model":"gpt-test","choices":[]}`})
	if err != nil {
		t.Fatalf("Observe error = %v, want nil", err)
	}
	if got := observer.State().EventsObserved; got != 1 {
		t.Fatalf("EventsObserved = %d, want 1", got)
	}
}

func TestObserverCapturesResponseMetadataTogetherAndAcrossChunks(t *testing.T) {
	observer := NewObserver()

	for _, data := range []string{
		`{"id":"response-1","model":"gpt-test","created":123}`,
		`{"choices":[],"id":"response-2","model":"other-model","created":456}`,
	} {
		if err := observer.Observe(streaming.SSEEvent{Data: data}); err != nil {
			t.Fatalf("Observe(%q) error = %v, want nil", data, err)
		}
	}

	metadata := observer.Metadata()
	if metadata.ID != "response-1" {
		t.Fatalf("metadata ID = %q, want %q", metadata.ID, "response-1")
	}
	if metadata.Model != "gpt-test" {
		t.Fatalf("metadata Model = %q, want %q", metadata.Model, "gpt-test")
	}
	if metadata.Created == nil || *metadata.Created != 123 {
		t.Fatalf("metadata Created = %v, want 123", metadata.Created)
	}
}

func TestObserverMetadataMissingFieldsAndFirstPresentValues(t *testing.T) {
	observer := NewObserver()

	for _, data := range []string{
		`{"choices":[]}`,
		`{"id":"","model":"","created":0}`,
		`{"id":"response-1"}`,
		`{"model":"gpt-test"}`,
	} {
		if err := observer.Observe(streaming.SSEEvent{Data: data}); err != nil {
			t.Fatalf("Observe(%q) error = %v, want nil", data, err)
		}
	}

	metadata := observer.Metadata()
	if metadata.ID != "response-1" {
		t.Fatalf("metadata ID = %q, want %q", metadata.ID, "response-1")
	}
	if metadata.Model != "gpt-test" {
		t.Fatalf("metadata Model = %q, want %q", metadata.Model, "gpt-test")
	}
	if metadata.Created == nil || *metadata.Created != 0 {
		t.Fatalf("metadata Created = %v, want present zero", metadata.Created)
	}
}

func TestObserverMetadataKeepsFirstConflictingValues(t *testing.T) {
	observer := NewObserver()

	for _, data := range []string{
		`{"id":"first-id","model":"first-model","created":1}`,
		`{"id":"later-id","model":"later-model","created":2}`,
	} {
		if err := observer.Observe(streaming.SSEEvent{Data: data}); err != nil {
			t.Fatalf("Observe(%q) error = %v, want nil", data, err)
		}
	}

	metadata := observer.Metadata()
	if metadata.ID != "first-id" || metadata.Model != "first-model" {
		t.Fatalf("metadata = %+v, want first ID and model", metadata)
	}
	if metadata.Created == nil || *metadata.Created != 1 {
		t.Fatalf("metadata Created = %v, want 1", metadata.Created)
	}
}

func TestObserverMetadataSnapshotDoesNotExposeObserverState(t *testing.T) {
	observer := NewObserver()
	if err := observer.Observe(streaming.SSEEvent{Data: `{"id":"response-1","model":"gpt-test","created":123}`}); err != nil {
		t.Fatalf("Observe error = %v, want nil", err)
	}

	metadata := observer.Metadata()
	metadata.Created = int64Pointer(999)
	*metadata.Created = 1000
	metadata.ID = "changed"
	metadata.Model = "changed"

	got := observer.Metadata()
	if got.ID != "response-1" || got.Model != "gpt-test" {
		t.Fatalf("observer metadata = %+v, changed through snapshot", got)
	}
	if got.Created == nil || *got.Created != 123 {
		t.Fatalf("observer Created = %v, changed through snapshot", got.Created)
	}
}

func TestObserverMalformedChunkDoesNotEraseResponseMetadata(t *testing.T) {
	observer := NewObserver()
	if err := observer.Observe(streaming.SSEEvent{Data: `{"id":"response-1","model":"gpt-test","created":123}`}); err != nil {
		t.Fatalf("initial Observe error = %v, want nil", err)
	}

	if err := observer.Observe(streaming.SSEEvent{Data: `{"id":"later-id","model":`}); !errors.Is(err, ErrMalformedStreamChunk) {
		t.Fatalf("malformed Observe error = %v, want errors.Is(..., ErrMalformedStreamChunk)", err)
	}

	metadata := observer.Metadata()
	if metadata.ID != "response-1" || metadata.Model != "gpt-test" {
		t.Fatalf("metadata = %+v, prior metadata was erased", metadata)
	}
	if metadata.Created == nil || *metadata.Created != 123 {
		t.Fatalf("metadata Created = %v, prior metadata was erased", metadata.Created)
	}
}

func int64Pointer(value int64) *int64 {
	return &value
}

func TestObserverMalformedJSONDoesNotBecomeTerminal(t *testing.T) {
	observer := NewObserver()

	err := observer.Observe(streaming.SSEEvent{Data: `{"id":"incomplete"`})
	if !errors.Is(err, ErrMalformedStreamChunk) {
		t.Fatalf("malformed Observe error = %v, want errors.Is(..., ErrMalformedStreamChunk)", err)
	}
	if got := observer.State().EventsObserved; got != 0 {
		t.Fatalf("EventsObserved after malformed chunk = %d, want 0", got)
	}

	if err := observer.Observe(streaming.SSEEvent{Data: `{"id":"chunk-2","unknown":{"kept":true}}`}); err != nil {
		t.Fatalf("valid Observe after malformed chunk = %v, want nil", err)
	}
	if got := observer.State().EventsObserved; got != 1 {
		t.Fatalf("EventsObserved after later valid chunk = %d, want 1", got)
	}
}

func TestObserverIgnoresEventNamesAndUnknownJSONFields(t *testing.T) {
	observer := NewObserver()

	for _, event := range []streaming.SSEEvent{
		{Event: "message", Data: `{"id":"chunk-1","future":{"nested":[1,true,"value"]}}`},
		{Event: "delta", Data: `{"id":"chunk-2","another_unknown":null}`},
	} {
		if err := observer.Observe(event); err != nil {
			t.Fatalf("Observe(%q) error = %v, want nil", event.Event, err)
		}
	}

	if got := observer.State().EventsObserved; got != 2 {
		t.Fatalf("EventsObserved = %d, want 2", got)
	}
}

func TestObserverRejectsNonObjectJSONWithoutChangingState(t *testing.T) {
	observer := NewObserver()

	for _, data := range []string{"", "null", "[]", "[1]", "not-json"} {
		if err := observer.Observe(streaming.SSEEvent{Data: data}); !errors.Is(err, ErrMalformedStreamChunk) {
			t.Fatalf("Observe(%q) error = %v, want errors.Is(..., ErrMalformedStreamChunk)", data, err)
		}
	}
	if got := observer.State().EventsObserved; got != 0 {
		t.Fatalf("EventsObserved = %d, want 0", got)
	}
}

func TestObserverRecognizesExactDoneSentinelAsMetadataOnly(t *testing.T) {
	observer := NewObserver()

	if err := observer.Observe(streaming.SSEEvent{Data: "[DONE]"}); err != nil {
		t.Fatalf("Observe exact DONE error = %v, want nil", err)
	}

	state := observer.State()
	if !state.DoneObserved {
		t.Fatalf("DoneObserved = false, want true")
	}
	if state.EventsObserved != 0 || len(state.Choices) != 0 {
		t.Fatalf("state after DONE = %+v, want metadata only", state)
	}
}

func TestObserverRepeatedDoneIsIdempotentAndPostDoneEventsAreObserved(t *testing.T) {
	observer := NewObserver()

	for _, event := range []streaming.SSEEvent{
		{Data: "[DONE]"},
		{Data: "[DONE]"},
		{Data: `{"choices":[{"index":0,"delta":{"content":"after"}}]}`},
	} {
		if err := observer.Observe(event); err != nil {
			t.Fatalf("Observe(%q) error = %v, want nil", event.Data, err)
		}
	}

	state := observer.State()
	if !state.DoneObserved {
		t.Fatalf("DoneObserved = false, want true")
	}
	if state.EventsObserved != 1 || len(state.Choices) != 1 {
		t.Fatalf("state after repeated/post-DONE events = %+v, want one JSON event", state)
	}
	if state.Choices[0].Delta.Content == nil || *state.Choices[0].Delta.Content != "after" {
		t.Fatalf("post-DONE choice = %+v, want content after", state.Choices[0])
	}
}

func TestObserverNonExactDoneVariantsUseNormalJSONObservation(t *testing.T) {
	observer := NewObserver()

	for _, data := range []string{" [DONE]", "[DONE] ", `"[DONE]"`} {
		if err := observer.Observe(streaming.SSEEvent{Data: data}); !errors.Is(err, ErrMalformedStreamChunk) {
			t.Fatalf("Observe(%q) error = %v, want errors.Is(..., ErrMalformedStreamChunk)", data, err)
		}
	}

	state := observer.State()
	if state.DoneObserved {
		t.Fatalf("DoneObserved = true after non-exact variants, want false")
	}
	if state.EventsObserved != 0 {
		t.Fatalf("EventsObserved after non-exact variants = %d, want 0", state.EventsObserved)
	}
}

func TestObserverRecordsChoiceAndDeltaObservationsInUpstreamOrder(t *testing.T) {
	observer := NewObserver()
	data := `{"choices":[{"index":4,"delta":{"content":"first","unknown_delta":{"ignored":true}}},{"index":1,"delta":{"role":"assistant"}},{"index":4,"delta":{"tool_calls":[{"index":2,"id":"call-2","type":"function","function":{"name":"lookup","arguments":"{\"city\":\""}},{"index":0,"function":{"arguments":"Paris\"}"}}]}}]}`

	if err := observer.Observe(streaming.SSEEvent{Data: data}); err != nil {
		t.Fatalf("Observe error = %v, want nil", err)
	}

	state := observer.State()
	if len(state.Choices) != 3 {
		t.Fatalf("Choices length = %d, want 3", len(state.Choices))
	}
	if got := state.Choices[0].Index; got != 4 {
		t.Fatalf("first choice index = %d, want 4", got)
	}
	if got := *state.Choices[0].Delta.Content; got != "first" {
		t.Fatalf("first choice content = %q, want %q", got, "first")
	}
	if got := state.Choices[1].Index; got != 1 {
		t.Fatalf("second choice index = %d, want 1", got)
	}
	if state.Choices[1].Delta.Role == nil || *state.Choices[1].Delta.Role != "assistant" {
		t.Fatalf("second choice role = %v, want assistant", state.Choices[1].Delta.Role)
	}
	if state.Choices[1].Delta.Content != nil {
		t.Fatalf("role-only choice content = %v, want absent", state.Choices[1].Delta.Content)
	}

	toolCalls := state.Choices[2].Delta.ToolCalls
	if len(toolCalls) != 2 {
		t.Fatalf("tool calls length = %d, want 2", len(toolCalls))
	}
	if toolCalls[0].Index != 2 || toolCalls[0].ID != "call-2" || toolCalls[0].Type != "function" {
		t.Fatalf("first tool call = %+v, want indexed call-2 function", toolCalls[0])
	}
	if toolCalls[0].Function.Name != "lookup" || toolCalls[0].Function.Arguments != `{"city":"` {
		t.Fatalf("first function = %+v, want name and raw fragment", toolCalls[0].Function)
	}
	if toolCalls[1].Index != 0 || toolCalls[1].Function.Arguments != `Paris"}` {
		t.Fatalf("second function = %+v, want index 0 and raw fragment", toolCalls[1].Function)
	}
}

func TestObserverDistinguishesContentAbsenceAndEmpty(t *testing.T) {
	observer := NewObserver()
	for _, data := range []string{
		`{"choices":[{"index":0,"delta":{}}]}`,
		`{"choices":[{"index":0,"delta":{"content":""}}]}`,
	} {
		if err := observer.Observe(streaming.SSEEvent{Data: data}); err != nil {
			t.Fatalf("Observe(%q) error = %v, want nil", data, err)
		}
	}

	choices := observer.State().Choices
	if choices[0].Delta.Content != nil {
		t.Fatalf("missing content = %v, want nil", choices[0].Delta.Content)
	}
	if choices[1].Delta.Content == nil || *choices[1].Delta.Content != "" {
		t.Fatalf("empty content = %v, want non-nil empty string", choices[1].Delta.Content)
	}
}

func TestObserverKeepsSplitToolArgumentFragmentsAsSeparateObservations(t *testing.T) {
	observer := NewObserver()
	for _, data := range []string{
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"city\":\""}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"Paris\"}"}}]}}]}`,
	} {
		if err := observer.Observe(streaming.SSEEvent{Data: data}); err != nil {
			t.Fatalf("Observe(%q) error = %v, want nil", data, err)
		}
	}

	choices := observer.State().Choices
	if len(choices) != 2 || len(choices[0].Delta.ToolCalls) != 1 || len(choices[1].Delta.ToolCalls) != 1 {
		t.Fatalf("split tool-call observations = %+v, want one per chunk", choices)
	}
	if got := choices[0].Delta.ToolCalls[0].Function.Arguments; got != `{"city":"` {
		t.Fatalf("first argument fragment = %q, want raw first fragment", got)
	}
	if got := choices[1].Delta.ToolCalls[0].Function.Arguments; got != `Paris"}` {
		t.Fatalf("second argument fragment = %q, want raw second fragment", got)
	}
}

func TestObserverRecordsFinishReasonSemanticsAndLatestNonNilReason(t *testing.T) {
	observer := NewObserver()
	for _, data := range []string{
		`{"choices":[{"index":3,"delta":{},"finish_reason":null},{"index":8,"delta":{}}]}`,
		`{"choices":[{"index":3,"delta":{},"finish_reason":""},{"index":8,"delta":{},"finish_reason":"length"}]}`,
		`{"choices":[{"index":3,"delta":{},"finish_reason":"stop"},{"index":8,"delta":{},"finish_reason":null}]}`,
	} {
		if err := observer.Observe(streaming.SSEEvent{Data: data}); err != nil {
			t.Fatalf("Observe(%q) error = %v, want nil", data, err)
		}
	}

	choices := observer.State().Choices
	if !choices[0].FinishReasonPresent || choices[0].FinishReason != nil {
		t.Fatalf("null finish reason = %+v, want present nil", choices[0])
	}
	if choices[1].FinishReasonPresent || choices[1].FinishReason != nil {
		t.Fatalf("missing finish reason = %+v, want absent nil", choices[1])
	}
	if !choices[2].FinishReasonPresent || choices[2].FinishReason == nil || *choices[2].FinishReason != "" {
		t.Fatalf("empty finish reason = %+v, want present empty", choices[2])
	}
	if reason, ok := observer.State().LatestFinishReasons[3]; !ok || reason != "stop" {
		t.Fatalf("latest reason for choice 3 = %q, %v; want stop, true", reason, ok)
	}
	if reason, ok := observer.State().LatestFinishReasons[8]; !ok || reason != "length" {
		t.Fatalf("latest reason for choice 8 = %q, %v; want length, true", reason, ok)
	}
}

func TestObserverFinishReasonDoesNotMarkObserverDone(t *testing.T) {
	observer := NewObserver()
	for _, data := range []string{
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[{"index":0,"delta":{"content":"after"}}]}`,
	} {
		if err := observer.Observe(streaming.SSEEvent{Data: data}); err != nil {
			t.Fatalf("Observe(%q) error = %v, want nil", data, err)
		}
	}

	state := observer.State()
	if state.EventsObserved != 2 || len(state.Choices) != 2 {
		t.Fatalf("state after finish reason = %+v, want both events and choices", state)
	}
	if state.Choices[1].Delta.Content == nil || *state.Choices[1].Delta.Content != "after" {
		t.Fatalf("post-finish observation = %+v, want content after", state.Choices[1])
	}
}

func TestObserverStateSnapshotDoesNotExposeChoiceState(t *testing.T) {
	observer := NewObserver()
	if err := observer.Observe(streaming.SSEEvent{Data: `{"choices":[{"index":2,"delta":{"content":"kept","tool_calls":[{"index":1,"function":{"arguments":"fragment"}}]},"finish_reason":"stop"}]}`}); err != nil {
		t.Fatalf("Observe error = %v, want nil", err)
	}

	snapshot := observer.State()
	snapshot.Choices[0].Index = 99
	*snapshot.Choices[0].Delta.Content = "changed"
	snapshot.Choices[0].Delta.ToolCalls[0].Function.Arguments = "changed"
	snapshot.LatestFinishReasons[2] = "changed"

	state := observer.State()
	if state.Choices[0].Index != 2 || *state.Choices[0].Delta.Content != "kept" {
		t.Fatalf("choice state changed through snapshot: %+v", state.Choices[0])
	}
	if state.Choices[0].Delta.ToolCalls[0].Function.Arguments != "fragment" {
		t.Fatalf("tool-call state changed through snapshot: %+v", state.Choices[0].Delta.ToolCalls[0])
	}
	if state.LatestFinishReasons[2] != "stop" {
		t.Fatalf("finish reason changed through snapshot: %q", state.LatestFinishReasons[2])
	}
}

func TestObserverNormalizesTokenUsage(t *testing.T) {
	tests := []struct {
		name string
		data []string
		want UsageObservation
	}{
		{
			name: "OpenAI names",
			data: []string{`{"usage":{"prompt_tokens":4,"completion_tokens":6,"total_tokens":10}}`},
			want: UsageObservation{InputTokens: intPointer(4), OutputTokens: intPointer(6), TotalTokens: intPointer(10)},
		},
		{
			name: "input output names",
			data: []string{`{"usage":{"input_tokens":7,"output_tokens":8}}`},
			want: UsageObservation{InputTokens: intPointer(7), OutputTokens: intPointer(8)},
		},
		{
			name: "partial usage and usage only terminal chunk",
			data: []string{
				`{"choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}]}`,
				`{"usage":{"prompt_tokens":11}}`,
			},
			want: UsageObservation{InputTokens: intPointer(11)},
		},
		{
			name: "later replacement preserves absent fields",
			data: []string{
				`{"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`,
				`{"usage":{"completion_tokens":9}}`,
			},
			want: UsageObservation{InputTokens: intPointer(1), OutputTokens: intPointer(9), TotalTokens: intPointer(3)},
		},
		{
			name: "total remains absent",
			data: []string{`{"usage":{"input_tokens":12,"output_tokens":13}}`},
			want: UsageObservation{InputTokens: intPointer(12), OutputTokens: intPointer(13)},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observer := NewObserver()
			for _, data := range test.data {
				if err := observer.Observe(streaming.SSEEvent{Data: data}); err != nil {
					t.Fatalf("Observe(%q) error = %v, want nil", data, err)
				}
			}
			assertUsageObservation(t, observer.State().Usage, test.want)
		})
	}
}

func TestObserverUsageAliasPrecedenceIsIndependentOfJSONMemberOrder(t *testing.T) {
	for _, data := range []string{
		`{"usage":{"prompt_tokens":20,"input_tokens":21,"completion_tokens":30,"output_tokens":31}}`,
		`{"usage":{"output_tokens":31,"completion_tokens":30,"input_tokens":21,"prompt_tokens":20}}`,
	} {
		observer := NewObserver()
		if err := observer.Observe(streaming.SSEEvent{Data: data}); err != nil {
			t.Fatalf("Observe(%q) error = %v, want nil", data, err)
		}
		assertUsageObservation(t, observer.State().Usage, UsageObservation{
			InputTokens: intPointer(20), OutputTokens: intPointer(30),
		})
	}
}

func TestObserverInvalidUsagePreservesStateAndCanBeReused(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "negative", data: `{"usage":{"prompt_tokens":-1}}`},
		{name: "fractional", data: `{"usage":{"completion_tokens":1.5}}`},
		{name: "non integer", data: `{"usage":{"total_tokens":"10"}}`},
		{name: "invalid alias despite canonical value", data: `{"usage":{"prompt_tokens":4,"input_tokens":-1}}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observer := NewObserver()
			if err := observer.Observe(streaming.SSEEvent{Data: `{"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`}); err != nil {
				t.Fatalf("initial Observe error = %v, want nil", err)
			}
			before := observer.State()
			if err := observer.Observe(streaming.SSEEvent{Data: test.data}); !errors.Is(err, ErrInvalidUsage) {
				t.Fatalf("Observe(%q) error = %v, want errors.Is(..., ErrInvalidUsage)", test.data, err)
			}
			after := observer.State()
			if after.EventsObserved != before.EventsObserved {
				t.Fatalf("EventsObserved after invalid usage = %d, want %d", after.EventsObserved, before.EventsObserved)
			}
			assertUsageObservation(t, after.Usage, before.Usage)

			if err := observer.Observe(streaming.SSEEvent{Data: `{"usage":{"output_tokens":8}}`}); err != nil {
				t.Fatalf("Observe after invalid usage error = %v, want nil", err)
			}
			if got := observer.State().Usage.OutputTokens; got == nil || *got != 8 {
				t.Fatalf("completion tokens after reuse = %v, want 8", got)
			}
			if got := observer.State().Usage.InputTokens; got == nil || *got != 2 {
				t.Fatalf("prompt tokens after reuse = %v, want preserved 2", got)
			}
		})
	}
}

func TestObserverCanonicalUsageMatchesJSONParser(t *testing.T) {
	data := `{"usage":{"prompt_tokens":4,"completion_tokens":6,"total_tokens":10,"prompt_tokens_details":{"cached_tokens":2},"completion_tokens_details":{"reasoning_tokens":3}}}`
	parsed, err := ParseJSONUsage([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	observer := NewObserver()
	if err := observer.Observe(streaming.SSEEvent{Data: data}); err != nil {
		t.Fatal(err)
	}
	if got := observer.State().Usage.Usage; got != parsed.Usage {
		t.Fatalf("canonical stream usage = %+v, JSON usage = %+v", got, parsed.Usage)
	}
}

func TestObserverSupportsResponsesCompletionUsageAndIgnoresUnrelatedNestedUsage(t *testing.T) {
	observer := NewObserver()
	if err := observer.Observe(streaming.SSEEvent{Data: `{"metadata":{"usage":{"prompt_tokens":99}}}`}); err != nil {
		t.Fatal(err)
	}
	if observer.State().Usage.Input().Known() {
		t.Fatal("unrelated nested usage was observed")
	}
	if err := observer.Observe(streaming.SSEEvent{Event: "response.completed", Data: `{"response":{"usage":{"input_tokens":7,"output_tokens":8,"input_tokens_details":{"cached_tokens":5},"output_tokens_details":{"reasoning_tokens":4}}}}`}); err != nil {
		t.Fatal(err)
	}
	usage := observer.State().Usage.Usage
	if value, known := usage.Input().Value(); !known || value != 7 {
		t.Fatalf("input = (%d, %t), want (7, true)", value, known)
	}
	if value, known := usage.Output().Value(); !known || value != 8 {
		t.Fatalf("output = (%d, %t), want (8, true)", value, known)
	}
	if value, known := usage.CachedInput().Value(); !known || value != 5 {
		t.Fatalf("cached = (%d, %t), want (5, true)", value, known)
	}
	if value, known := usage.ReasoningOutput().Value(); !known || value != 4 {
		t.Fatalf("reasoning = (%d, %t), want (4, true)", value, known)
	}
}

func TestObserverCanonicalUsagePartialUpdatesAndMutationIsolation(t *testing.T) {
	observer := NewObserver()
	for _, data := range []string{`{"usage":{"prompt_tokens":2}}`, `{"usage":{"completion_tokens":3}}`} {
		if err := observer.Observe(streaming.SSEEvent{Data: data}); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := observer.State()
	if snapshot.Usage.InputTokens == nil || snapshot.Usage.OutputTokens == nil {
		t.Fatalf("projection = %+v, want both fields", snapshot.Usage)
	}
	*snapshot.Usage.InputTokens = 99
	*snapshot.Usage.OutputTokens = 99
	if value := observer.State().Usage.Input().Int64(); value != 2 {
		t.Fatalf("canonical input changed through snapshot mutation: %d", value)
	}
	if value := observer.State().Usage.Output().Int64(); value != 3 {
		t.Fatalf("canonical output changed through snapshot mutation: %d", value)
	}
}

func TestObserverInvalidRichUsageLeavesStateAndCanBeReused(t *testing.T) {
	observer := NewObserver()
	if err := observer.Observe(streaming.SSEEvent{Data: `{"usage":{"prompt_tokens":2}}`}); err != nil {
		t.Fatal(err)
	}
	before := observer.State()
	for _, data := range []string{
		`{"usage":{"prompt_tokens_details":{"cached_tokens":-1}}}`,
		`{"response":{"usage":{"output_tokens":"secret"}}}`,
		`{"usage":{"prompt_tokens":9223372036854775807,"completion_tokens":1}}`,
	} {
		event := streaming.SSEEvent{Data: data}
		if data == `{"response":{"usage":{"output_tokens":"secret"}}}` {
			event.Event = "response.completed"
		}
		if err := observer.Observe(event); !errors.Is(err, ErrInvalidUsage) && !errors.Is(err, accounting.ErrCountOverflow) {
			t.Fatalf("Observe(%s) error = %v, want invalid usage or overflow", data, err)
		}
		after := observer.State()
		if after.EventsObserved != before.EventsObserved || after.Usage.Usage != before.Usage.Usage {
			t.Fatalf("state changed after invalid update: before=%+v after=%+v", before, after)
		}
	}
	if err := observer.Observe(streaming.SSEEvent{Data: `{"usage":{"completion_tokens":3}}`}); err != nil {
		t.Fatal(err)
	}
}

func TestObserverUsageCanonicalAliasAndDetailPrecedence(t *testing.T) {
	observer := NewObserver()
	data := `{"usage":{"prompt_tokens":20,"input_tokens":21,"completion_tokens":30,"output_tokens":31,"prompt_tokens_details":{"cached_tokens":2},"input_tokens_details":{"cached_tokens":3},"completion_tokens_details":{"reasoning_tokens":4},"output_tokens_details":{"reasoning_tokens":5}}}`
	if err := observer.Observe(streaming.SSEEvent{Data: data}); err != nil {
		t.Fatal(err)
	}
	usage := observer.State().Usage.Usage
	if usage.Input().Int64() != 20 || usage.Output().Int64() != 30 || usage.CachedInput().Int64() != 2 || usage.ReasoningOutput().Int64() != 4 {
		t.Fatalf("precedence usage = %+v", usage)
	}
}

func assertUsageObservation(t *testing.T, got, want UsageObservation) {
	t.Helper()
	assertOptionalInt(t, "input_tokens", got.InputTokens, want.InputTokens)
	assertOptionalInt(t, "output_tokens", got.OutputTokens, want.OutputTokens)
	assertOptionalInt(t, "total_tokens", got.TotalTokens, want.TotalTokens)
}

func assertOptionalInt(t *testing.T, name string, got, want *int) {
	t.Helper()
	if got == nil || want == nil {
		if got != nil || want != nil {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
		return
	}
	if *got != *want {
		t.Fatalf("%s = %d, want %d", name, *got, *want)
	}
}
