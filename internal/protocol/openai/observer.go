package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/pestit/9gateway/internal/accounting"
	"github.com/pestit/9gateway/internal/streaming"
)

// ErrMalformedStreamChunk indicates that an SSE event did not contain a JSON
// object representing an OpenAI streaming chunk.
var ErrMalformedStreamChunk = errors.New("openai: malformed streaming chunk")

// ErrInvalidUsage indicates that a known usage field was present but was not a
// non-negative JSON integer.
var ErrInvalidUsage = errors.New("openai: invalid usage")

// ObserverState is the state accumulated by an Observer. It records only the
// successfully observed events; it has no transport semantics.
type ObserverState struct {
	EventsObserved      int
	DoneObserved        bool
	Choices             []ChoiceObservation
	LatestFinishReasons map[int]string
	// Usage retains the existing pointer projection and embeds the immutable
	// canonical accounting value. The projection is kept for chat aggregation;
	// protocol-neutral callers should use its promoted accounting accessors.
	Usage UsageObservation
}

// UsageObservation embeds the protocol-independent canonical usage while
// retaining the pointer-shaped projection used by existing chat accumulation
// and rendering callers. New callers should use the promoted accounting
// accessors (Input, Output, Total, CachedInput, and ReasoningOutput).
type UsageObservation struct {
	accounting.Usage
	InputTokens  *int `json:"input_tokens,omitempty"`
	OutputTokens *int `json:"output_tokens,omitempty"`
	TotalTokens  *int `json:"total_tokens,omitempty"`
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
	state.Usage = cloneUsageObservation(observer.state.Usage)
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
	if event.Data == "[DONE]" {
		observer.state.DoneObserved = true
		return nil
	}

	data := bytes.TrimSpace([]byte(event.Data))
	if len(data) == 0 || data[0] != '{' {
		return fmt.Errorf("%w: expected a JSON object", ErrMalformedStreamChunk)
	}

	var chunk streamChunk
	if err := json.Unmarshal(data, &chunk); err != nil {
		return fmt.Errorf("%w: %v", ErrMalformedStreamChunk, err)
	}
	usage, observed, err := observeEventUsage(event, data, chunk.Usage)
	if err != nil {
		return err
	}
	canonical := observer.state.Usage.Usage
	if observed {
		canonical, err = mergeCanonicalUsage(canonical, usage)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidUsage, err)
		}
	}

	observer.state.EventsObserved++
	observer.observeMetadata(chunk)
	observer.observeChoices(chunk.Choices)
	if observed {
		observer.state.Usage = usageObservationFromCanonical(canonical)
	}
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

func mergeCanonicalUsage(current, update accounting.Usage) (accounting.Usage, error) {
	input, inputKnown := current.Input().Value()
	if value, known := update.Input().Value(); known {
		input, inputKnown = value, true
	}
	output, outputKnown := current.Output().Value()
	if value, known := update.Output().Value(); known {
		output, outputKnown = value, true
	}
	total, totalKnown := current.Total().Value()
	totalObserved := current.TotalWasObserved()
	if value, known := update.Total().Value(); known && update.TotalWasObserved() {
		total, totalKnown = value, true
		totalObserved = true
	} else if !totalObserved {
		// A derived total must be recalculated when a later partial event updates
		// either component. It is not an explicitly observed field.
		total, totalKnown = 0, false
	}
	cached, cachedKnown := current.CachedInput().Value()
	if value, known := update.CachedInput().Value(); known {
		cached, cachedKnown = value, true
	}
	reasoning, reasoningKnown := current.ReasoningOutput().Value()
	if value, known := update.ReasoningOutput().Value(); known {
		reasoning, reasoningKnown = value, true
	}
	merged, err := accounting.NewUsage(accounting.UsageInput{
		Input:           optionalInt64(input, inputKnown),
		Output:          optionalInt64(output, outputKnown),
		Total:           optionalInt64(total, totalKnown && totalObserved),
		CachedInput:     optionalInt64(cached, cachedKnown),
		ReasoningOutput: optionalInt64(reasoning, reasoningKnown),
	})
	if err != nil {
		return accounting.Usage{}, err
	}
	return merged, nil
}

func cloneUsageObservation(usage UsageObservation) UsageObservation {
	usage.InputTokens = cloneInt(usage.InputTokens)
	usage.OutputTokens = cloneInt(usage.OutputTokens)
	usage.TotalTokens = cloneInt(usage.TotalTokens)
	return usage
}

func mergeUsageObservation(current, update UsageObservation) UsageObservation {
	if update.InputTokens != nil {
		current.InputTokens = cloneInt(update.InputTokens)
	}
	if update.OutputTokens != nil {
		current.OutputTokens = cloneInt(update.OutputTokens)
	}
	if update.TotalTokens != nil {
		current.TotalTokens = cloneInt(update.TotalTokens)
	}
	if update.Usage.Input().Known() || update.Usage.Output().Known() || update.Usage.Total().Known() ||
		update.Usage.CachedInput().Known() || update.Usage.ReasoningOutput().Known() {
		canonical, err := mergeCanonicalUsage(current.Usage, update.Usage)
		if err == nil {
			current.Usage = canonical
		}
	}
	return current
}

func usageObservationFromCanonical(usage accounting.Usage) UsageObservation {
	observation := UsageObservation{Usage: usage}
	observation.InputTokens = intPointerFromCanonical(usage.Input())
	observation.OutputTokens = intPointerFromCanonical(usage.Output())
	if usage.TotalWasObserved() {
		observation.TotalTokens = intPointerFromCanonical(usage.Total())
	}
	return observation
}

func intPointerFromCanonical[T interface{ Value() (int64, bool) }](count T) *int {
	value, known := count.Value()
	if !known {
		return nil
	}
	converted := int(value)
	return &converted
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func optionalInt64(value int64, known bool) *int64 {
	if !known {
		return nil
	}
	return &value
}

func observeEventUsage(event streaming.SSEEvent, data []byte, root json.RawMessage) (accounting.Usage, bool, error) {
	rootResult, err := parseJSONUsageObject(root)
	if err != nil {
		return accounting.Usage{}, false, fmt.Errorf("%w: %w", ErrInvalidUsage, err)
	}
	usage, observed := rootResult.Usage, rootResult.Observed
	if !isResponsesCompletionEvent(event, data) {
		return usage, observed, nil
	}

	var envelope responseCompletionEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return accounting.Usage{}, false, fmt.Errorf("%w: response envelope", ErrInvalidUsage)
	}
	if len(envelope.Response) == 0 || isJSONNull(envelope.Response) {
		return usage, observed, nil
	}
	var response responseUsageEnvelope
	if err := json.Unmarshal(envelope.Response, &response); err != nil {
		return accounting.Usage{}, false, fmt.Errorf("%w: response envelope", ErrInvalidUsage)
	}
	responseResult, err := parseJSONUsageObject(response.Usage)
	if err != nil {
		return accounting.Usage{}, false, fmt.Errorf("%w: %w", ErrInvalidUsage, err)
	}
	if responseResult.Observed {
		if observed {
			usage, err = mergeCanonicalUsage(usage, responseResult.Usage)
			if err != nil {
				return accounting.Usage{}, false, fmt.Errorf("%w: %w", ErrInvalidUsage, err)
			}
		} else {
			usage = responseResult.Usage
		}
		observed = true
	}
	return usage, observed, nil
}

func isResponsesCompletionEvent(event streaming.SSEEvent, data []byte) bool {
	if event.Event == "response.completed" {
		return true
	}
	var envelope struct {
		Type json.RawMessage `json:"type"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		return false
	}
	var eventType string
	return json.Unmarshal(envelope.Type, &eventType) == nil && eventType == "response.completed"
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

type responseCompletionEnvelope struct {
	Response json.RawMessage `json:"response"`
}

type responseUsageEnvelope struct {
	Usage json.RawMessage `json:"usage"`
}
