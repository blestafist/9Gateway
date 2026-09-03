package openai

import "errors"

// ErrInvalidAccumulatorLimit indicates that a chat accumulator was created
// without a positive payload limit.
var ErrInvalidAccumulatorLimit = errors.New("openai: invalid accumulator payload limit")

// ErrAccumulatorOverflow indicates that adding an observation would exceed the
// accumulator's payload limit. An accumulator that returns this error is
// terminal; subsequent calls return the same error without changing state.
var ErrAccumulatorOverflow = errors.New("openai: accumulator payload limit exceeded")

// ErrAccumulatorTerminal is an alias for ErrAccumulatorOverflow. It is kept as
// a recognizable name for callers that want to distinguish a terminal
// accumulator from other application errors.
var ErrAccumulatorTerminal = ErrAccumulatorOverflow

// ErrPayloadLimitExceeded is an alternate descriptive name for the terminal
// overflow error.
var ErrPayloadLimitExceeded = ErrAccumulatorOverflow

// AccumulatedMessage is the message reconstructed for one choice. Content is
// kept as upstream supplied bytes represented as a Go string; it is not
// decoded, normalized, or otherwise interpreted.
type AccumulatedMessage struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// AccumulatedFunction is the function portion of an accumulated tool call.
// Arguments are raw fragments concatenated in observation order. They are not
// decoded as JSON by this package.
type AccumulatedFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// AccumulatedToolCall is one tool call, identified by its upstream index
// within a choice. ToolCalls in an AccumulatedChoice retain first-observed
// order, including when indexes are non-contiguous.
type AccumulatedToolCall struct {
	Index    int                 `json:"index"`
	ID       string              `json:"id,omitempty"`
	Type     string              `json:"type,omitempty"`
	Function AccumulatedFunction `json:"function"`
}

// AccumulatedChoice is one choice keyed by its upstream Index. The ordered
// Choices slice in AccumulatorState is the deterministic rendering order.
type AccumulatedChoice struct {
	Index        int                   `json:"index"`
	Message      AccumulatedMessage    `json:"message"`
	ToolCalls    []AccumulatedToolCall `json:"tool_calls,omitempty"`
	FinishReason *string               `json:"finish_reason,omitempty"`
}

// AccumulatorState is a caller-owned snapshot of an accumulator. Choices are
// ordered by the first observation of each upstream choice index. ChoicesByIndex
// provides the same values for callers that need keyed lookup. All slices,
// maps, and pointers in a returned state are copies.
type AccumulatorState struct {
	ResponseMetadata
	Choices        []AccumulatedChoice
	ChoicesByIndex map[int]AccumulatedChoice
	Usage          UsageObservation
	PayloadBytes   int64
	Terminal       bool
}

// ChatAccumulator accumulates parsed OpenAI observer snapshots. It has no
// transport or JSON parsing responsibilities.
type ChatAccumulator struct {
	maxPayloadBytes int64
	payloadBytes    int64
	metadata        ResponseMetadata
	usage           UsageObservation
	choices         map[int]*AccumulatedChoice
	choiceOrder     []int
	terminal        bool
}

// Accumulator is the short name for ChatAccumulator.
type Accumulator = ChatAccumulator

// NewChatAccumulator creates an empty accumulator with an explicit positive
// maximum for content and function-argument bytes.
func NewChatAccumulator(maxPayloadBytes int64) (*ChatAccumulator, error) {
	if maxPayloadBytes <= 0 {
		return nil, ErrInvalidAccumulatorLimit
	}
	return &ChatAccumulator{
		maxPayloadBytes: maxPayloadBytes,
		choices:         make(map[int]*AccumulatedChoice),
	}, nil
}

// NewAccumulator is an alias for NewChatAccumulator.
func NewAccumulator(maxPayloadBytes int64) (*ChatAccumulator, error) {
	return NewChatAccumulator(maxPayloadBytes)
}

// Accumulate applies every choice observation in the supplied parsed result in
// upstream order. Results are treated as independent observations; callers that
// provide cumulative snapshots must pass only the newly observed entries.
// The update is transactional: if any content or argument fragment would cross
// the limit, no part of that result is applied and the accumulator becomes
// terminal.
func (accumulator *ChatAccumulator) Accumulate(result ObservationResult) error {
	if accumulator.terminal {
		return ErrAccumulatorOverflow
	}

	working := accumulator.clone()
	working.mergeMetadata(result.Metadata)
	working.mergeUsage(result.State.Usage)

	for _, observation := range result.State.Choices {
		if err := working.applyChoiceObservation(observation); err != nil {
			accumulator.terminal = true
			return ErrAccumulatorOverflow
		}
	}
	*accumulator = *working
	return nil
}

// Apply is a descriptive alias for Accumulate.
func (accumulator *ChatAccumulator) Apply(result ObservationResult) error {
	return accumulator.Accumulate(result)
}

// Add is an alias for Accumulate.
func (accumulator *ChatAccumulator) Add(result ObservationResult) error {
	return accumulator.Accumulate(result)
}

// State returns a caller-safe snapshot of accumulated data.
func (accumulator *ChatAccumulator) State() AccumulatorState {
	return accumulator.snapshot()
}

// Snapshot returns a caller-safe snapshot of accumulated data.
func (accumulator *ChatAccumulator) Snapshot() AccumulatorState {
	return accumulator.snapshot()
}

func (accumulator *ChatAccumulator) snapshot() AccumulatorState {
	state := AccumulatorState{
		ResponseMetadata: cloneResponseMetadata(accumulator.metadata),
		Usage:            cloneUsageObservation(accumulator.usage),
		PayloadBytes:     accumulator.payloadBytes,
		Terminal:         accumulator.terminal,
	}
	if len(accumulator.choiceOrder) != 0 {
		state.Choices = make([]AccumulatedChoice, 0, len(accumulator.choiceOrder))
		state.ChoicesByIndex = make(map[int]AccumulatedChoice, len(accumulator.choiceOrder))
		for _, index := range accumulator.choiceOrder {
			choice := cloneAccumulatedChoice(*accumulator.choices[index])
			state.Choices = append(state.Choices, choice)
			state.ChoicesByIndex[index] = cloneAccumulatedChoice(choice)
		}
	}
	return state
}

func (accumulator *ChatAccumulator) clone() *ChatAccumulator {
	clone := &ChatAccumulator{
		maxPayloadBytes: accumulator.maxPayloadBytes,
		payloadBytes:    accumulator.payloadBytes,
		metadata:        cloneResponseMetadata(accumulator.metadata),
		usage:           cloneUsageObservation(accumulator.usage),
		choiceOrder:     append([]int(nil), accumulator.choiceOrder...),
		terminal:        accumulator.terminal,
		choices:         make(map[int]*AccumulatedChoice, len(accumulator.choices)),
	}
	for index, choice := range accumulator.choices {
		copied := cloneAccumulatedChoice(*choice)
		clone.choices[index] = &copied
	}
	return clone
}

func (accumulator *ChatAccumulator) mergeMetadata(metadata ResponseMetadata) {
	if accumulator.metadata.ID == "" && metadata.ID != "" {
		accumulator.metadata.ID = metadata.ID
	}
	if accumulator.metadata.Model == "" && metadata.Model != "" {
		accumulator.metadata.Model = metadata.Model
	}
	if accumulator.metadata.Created == nil && metadata.Created != nil {
		created := *metadata.Created
		accumulator.metadata.Created = &created
	}
}

func (accumulator *ChatAccumulator) mergeUsage(usage UsageObservation) {
	// Merge from a copy so every value supplied by a caller is detached before
	// it becomes part of the accumulator state. Missing fields intentionally
	// leave the latest known values unchanged.
	accumulator.usage = mergeUsageObservation(accumulator.usage, cloneUsageObservation(usage))
}

func (accumulator *ChatAccumulator) applyChoiceObservation(observation ChoiceObservation) error {
	choice, exists := accumulator.choices[observation.Index]
	if !exists {
		choice = &AccumulatedChoice{Index: observation.Index}
		accumulator.choices[observation.Index] = choice
		accumulator.choiceOrder = append(accumulator.choiceOrder, observation.Index)
	}

	if observation.Delta.Role != nil && *observation.Delta.Role != "" && choice.Message.Role == "" {
		choice.Message.Role = *observation.Delta.Role
	}
	if observation.Delta.Content != nil {
		if err := accumulator.addPayload(*observation.Delta.Content); err != nil {
			return err
		}
		choice.Message.Content += *observation.Delta.Content
	}
	for _, toolObservation := range observation.Delta.ToolCalls {
		toolCall := accumulator.toolCall(choice, toolObservation.Index)
		if toolCall.ID == "" && toolObservation.ID != "" {
			toolCall.ID = toolObservation.ID
		}
		if toolCall.Type == "" && toolObservation.Type != "" {
			toolCall.Type = toolObservation.Type
		}
		if toolCall.Function.Name == "" && toolObservation.Function.Name != "" {
			toolCall.Function.Name = toolObservation.Function.Name
		}
		if toolObservation.Function.Arguments != "" {
			if err := accumulator.addPayload(toolObservation.Function.Arguments); err != nil {
				return err
			}
			toolCall.Function.Arguments += toolObservation.Function.Arguments
		}
	}
	if observation.FinishReason != nil {
		reason := *observation.FinishReason
		choice.FinishReason = &reason
	}
	return nil
}

func (accumulator *ChatAccumulator) addPayload(fragment string) error {
	if int64(len(fragment)) > accumulator.maxPayloadBytes-accumulator.payloadBytes {
		return ErrAccumulatorOverflow
	}
	accumulator.payloadBytes += int64(len(fragment))
	return nil
}

func (accumulator *ChatAccumulator) toolCall(choice *AccumulatedChoice, index int) *AccumulatedToolCall {
	for position := range choice.ToolCalls {
		if choice.ToolCalls[position].Index == index {
			return &choice.ToolCalls[position]
		}
	}
	choice.ToolCalls = append(choice.ToolCalls, AccumulatedToolCall{Index: index})
	return &choice.ToolCalls[len(choice.ToolCalls)-1]
}

func cloneResponseMetadata(metadata ResponseMetadata) ResponseMetadata {
	clone := metadata
	if metadata.Created != nil {
		created := *metadata.Created
		clone.Created = &created
	}
	return clone
}

func cloneAccumulatedChoice(choice AccumulatedChoice) AccumulatedChoice {
	clone := choice
	clone.FinishReason = cloneString(choice.FinishReason)
	if choice.ToolCalls != nil {
		clone.ToolCalls = append([]AccumulatedToolCall(nil), choice.ToolCalls...)
	}
	return clone
}
