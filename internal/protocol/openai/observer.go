package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/pestit/9gateway/internal/streaming"
)

// ErrMalformedStreamChunk indicates that an SSE event did not contain a JSON
// object representing an OpenAI streaming chunk.
var ErrMalformedStreamChunk = errors.New("openai: malformed streaming chunk")

// ObserverState is the state accumulated by an Observer. It records only the
// successfully parsed chunks; it has no transport or completion semantics.
type ObserverState struct {
	EventsObserved      int
	Choices             []ChoiceObservation
	LatestFinishReasons map[int]string
}

// ChoiceObservation is one choice entry from one observed streaming chunk.
// Choices is retained as a slice in ObserverState so the upstream choices
// order is not lost. FinishReasonPresent distinguishes an omitted reason from
// an explicit null reason; a non-nil FinishReason is an explicit string,
// including an empty string.
type ChoiceObservation struct {
	Index               int
	Delta               DeltaObservation
	FinishReason        *string
	FinishReasonPresent bool
}

// DeltaObservation contains only the delta fields needed by the observer.
// Content is a pointer because an omitted content field is different from an
// explicitly empty content string.
type DeltaObservation struct {
	Role      *string
	Content   *string
	ToolCalls []ToolCallObservation
}

// ToolCallObservation is one indexed tool-call delta. Function arguments are
// retained as the string fragment supplied by upstream; they are not decoded
// as JSON.
type ToolCallObservation struct {
	Index    int
	ID       string
	Type     string
	Function ToolCallFunctionObservation
}

// ToolCallFunctionObservation contains the observed function delta fields.
type ToolCallFunctionObservation struct {
	Name      string
	Arguments string
}

// ResponseMetadata is the small set of response envelope fields observed by
// an Observer. Created is a pointer so an explicit zero remains distinct from
// an absent field.
type ResponseMetadata struct {
	ID      string `json:"id,omitempty"`
	Model   string `json:"model,omitempty"`
	Created *int64 `json:"created,omitempty"`
}

// Observer parses OpenAI streaming chunks from complete, protocol-neutral SSE
// events. It deliberately does not read from or write to a transport.
type Observer struct {
	state    ObserverState
	metadata ResponseMetadata
}

// NewObserver creates an empty OpenAI streaming observer.
func NewObserver() *Observer {
	return &Observer{}
}

// State returns a copy of the observer's current state.
func (observer *Observer) State() ObserverState {
	state := observer.state
	state.Choices = cloneChoiceObservations(observer.state.Choices)
	state.LatestFinishReasons = cloneFinishReasons(observer.state.LatestFinishReasons)
	return state
}

// Metadata returns a snapshot of the response envelope metadata observed so
// far. The Created pointer is copied so callers cannot mutate observer state.
func (observer *Observer) Metadata() ResponseMetadata {
	metadata := observer.metadata
	if metadata.Created != nil {
		created := *metadata.Created
		metadata.Created = &created
	}
	return metadata
}

// Observe parses one complete SSE event. Event names are transport metadata
// and do not affect OpenAI chunk parsing. A malformed event returns an error,
// but does not make the observer terminal; subsequent events can be observed.
func (observer *Observer) Observe(event streaming.SSEEvent) error {
	data := bytes.TrimSpace([]byte(event.Data))
	if len(data) == 0 || data[0] != '{' {
		return fmt.Errorf("%w: expected a JSON object", ErrMalformedStreamChunk)
	}

	var chunk streamChunk
	if err := json.Unmarshal(data, &chunk); err != nil {
		return fmt.Errorf("%w: %v", ErrMalformedStreamChunk, err)
	}

	observer.state.EventsObserved++
	observer.observeMetadata(chunk)
	observer.observeChoices(chunk.Choices)
	return nil
}

type streamChoice struct {
	Index        json.RawMessage `json:"index"`
	Delta        json.RawMessage `json:"delta"`
	FinishReason json.RawMessage `json:"finish_reason"`
}

type streamDelta struct {
	Role      json.RawMessage `json:"role"`
	Content   json.RawMessage `json:"content"`
	ToolCalls json.RawMessage `json:"tool_calls"`
}

type streamToolCall struct {
	Index    json.RawMessage `json:"index"`
	ID       json.RawMessage `json:"id"`
	Type     json.RawMessage `json:"type"`
	Function json.RawMessage `json:"function"`
}

type streamToolCallFunction struct {
	Name      json.RawMessage `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (observer *Observer) observeChoices(raw json.RawMessage) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return
	}

	var choices []json.RawMessage
	if err := json.Unmarshal(raw, &choices); err != nil {
		return
	}
	for _, rawChoice := range choices {
		var choice streamChoice
		if err := json.Unmarshal(rawChoice, &choice); err != nil {
			continue
		}

		observation := ChoiceObservation{Index: decodeInt(choice.Index)}
		observation.Delta = decodeDelta(choice.Delta)
		if len(choice.FinishReason) > 0 {
			observation.FinishReasonPresent = true
			observation.FinishReason = decodeStringPointer(choice.FinishReason)
		}
		observer.state.Choices = append(observer.state.Choices, observation)
		if observation.FinishReason != nil {
			if observer.state.LatestFinishReasons == nil {
				observer.state.LatestFinishReasons = make(map[int]string)
			}
			observer.state.LatestFinishReasons[observation.Index] = *observation.FinishReason
		}
	}
}

func decodeDelta(raw json.RawMessage) DeltaObservation {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return DeltaObservation{}
	}

	var delta streamDelta
	if err := json.Unmarshal(raw, &delta); err != nil {
		return DeltaObservation{}
	}
	return DeltaObservation{
		Role:      decodeStringPointer(delta.Role),
		Content:   decodeStringPointer(delta.Content),
		ToolCalls: decodeToolCalls(delta.ToolCalls),
	}
}

func decodeToolCalls(raw json.RawMessage) []ToolCallObservation {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}

	var calls []json.RawMessage
	if err := json.Unmarshal(raw, &calls); err != nil {
		return nil
	}
	observations := make([]ToolCallObservation, 0, len(calls))
	for _, rawCall := range calls {
		var call streamToolCall
		if err := json.Unmarshal(rawCall, &call); err != nil {
			continue
		}
		observation := ToolCallObservation{
			Index: decodeInt(call.Index),
			ID:    decodeString(call.ID),
			Type:  decodeString(call.Type),
		}
		if len(call.Function) > 0 && !bytes.Equal(bytes.TrimSpace(call.Function), []byte("null")) {
			var function streamToolCallFunction
			if json.Unmarshal(call.Function, &function) == nil {
				observation.Function = ToolCallFunctionObservation{
					Name:      decodeString(function.Name),
					Arguments: decodeString(function.Arguments),
				}
			}
		}
		observations = append(observations, observation)
	}
	return observations
}

func decodeInt(raw json.RawMessage) int {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0
	}
	var value int
	if json.Unmarshal(raw, &value) != nil {
		return 0
	}
	return value
}

func decodeString(raw json.RawMessage) string {
	value := decodeStringPointer(raw)
	if value == nil {
		return ""
	}
	return *value
}

func decodeStringPointer(raw json.RawMessage) *string {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return &value
}

func cloneChoiceObservations(observations []ChoiceObservation) []ChoiceObservation {
	if observations == nil {
		return nil
	}
	clone := make([]ChoiceObservation, len(observations))
	for index, observation := range observations {
		clone[index] = observation
		clone[index].FinishReason = cloneString(observation.FinishReason)
		clone[index].Delta.Role = cloneString(observation.Delta.Role)
		clone[index].Delta.Content = cloneString(observation.Delta.Content)
		if observation.Delta.ToolCalls != nil {
			clone[index].Delta.ToolCalls = append([]ToolCallObservation(nil), observation.Delta.ToolCalls...)
		}
	}
	return clone
}

func cloneFinishReasons(reasons map[int]string) map[int]string {
	if reasons == nil {
		return nil
	}
	clone := make(map[int]string, len(reasons))
	for index, reason := range reasons {
		clone[index] = reason
	}
	return clone
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func (observer *Observer) observeMetadata(chunk streamChunk) {
	if observer.metadata.ID == "" {
		var id string
		if err := json.Unmarshal(chunk.ID, &id); err == nil && id != "" {
			observer.metadata.ID = id
		}
	}
	if observer.metadata.Model == "" {
		var model string
		if err := json.Unmarshal(chunk.Model, &model); err == nil && model != "" {
			observer.metadata.Model = model
		}
	}
	if observer.metadata.Created == nil && len(chunk.Created) > 0 {
		var created int64
		value := bytes.TrimSpace(chunk.Created)
		if !bytes.Equal(value, []byte("null")) && json.Unmarshal(value, &created) == nil {
			observer.metadata.Created = &created
		}
	}
}

// streamChunk keeps response envelope values raw so metadata and choice
// observations can apply their own presence and type semantics.
type streamChunk struct {
	ID      json.RawMessage `json:"id"`
	Object  json.RawMessage `json:"object"`
	Created json.RawMessage `json:"created"`
	Model   json.RawMessage `json:"model"`
	Choices json.RawMessage `json:"choices"`
	Usage   json.RawMessage `json:"usage"`
}
