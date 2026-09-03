package openai

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrInvalidAccumulatorState indicates that an accumulator snapshot contains
// no message, tool call, or finish metadata to render.
var ErrInvalidAccumulatorState = errors.New("openai: invalid accumulator state")

// RenderChatCompletion renders an accumulator snapshot as one non-streaming
// OpenAI chat-completion response. It deliberately uses encoding/json rather
// than assembling JSON text so content and tool-call argument fragments are
// escaped correctly, including arbitrary Unicode.
func RenderChatCompletion(state AccumulatorState) ([]byte, error) {
	if !hasMeaningfulChoice(state.Choices) {
		return nil, fmt.Errorf("%w: no meaningful response data", ErrInvalidAccumulatorState)
	}

	response := chatCompletionResponse{
		ID:      state.ID,
		Object:  "chat.completion",
		Model:   state.Model,
		Choices: make([]chatCompletionChoice, 0, len(state.Choices)),
	}
	if state.Created != nil {
		response.Created = *state.Created
	}
	for _, choice := range state.Choices {
		response.Choices = append(response.Choices, chatCompletionChoice{
			Index: choice.Index,
			Message: chatCompletionMessage{
				Role:      choice.Message.Role,
				Content:   choice.Message.Content,
				ToolCalls: renderToolCalls(choice.ToolCalls),
			},
			FinishReason: choice.FinishReason,
		})
	}
	if usage := renderUsage(state.Usage); usage != nil {
		response.Usage = usage
	}

	return json.Marshal(response)
}

// Render renders the current accumulator snapshot as a non-streaming chat
// completion response.
func (accumulator *ChatAccumulator) Render() ([]byte, error) {
	if accumulator == nil {
		return nil, fmt.Errorf("%w: nil accumulator", ErrInvalidAccumulatorState)
	}
	return RenderChatCompletion(accumulator.State())
}

type chatCompletionResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []chatCompletionChoice `json:"choices"`
	Usage   *chatCompletionUsage   `json:"usage,omitempty"`
}

type chatCompletionChoice struct {
	Index        int                   `json:"index"`
	Message      chatCompletionMessage `json:"message"`
	FinishReason *string               `json:"finish_reason"`
}

type chatCompletionMessage struct {
	Role      string                `json:"role"`
	Content   string                `json:"content"`
	ToolCalls []AccumulatedToolCall `json:"tool_calls,omitempty"`
}

type chatCompletionUsage struct {
	PromptTokens     *int `json:"prompt_tokens,omitempty"`
	CompletionTokens *int `json:"completion_tokens,omitempty"`
	TotalTokens      *int `json:"total_tokens,omitempty"`
}

func hasMeaningfulChoice(choices []AccumulatedChoice) bool {
	for _, choice := range choices {
		if choice.Message.Role != "" || choice.Message.Content != "" ||
			len(choice.ToolCalls) != 0 || choice.FinishReason != nil || choice.FinishReasonPresent {
			return true
		}
	}
	return false
}

func renderToolCalls(toolCalls []AccumulatedToolCall) []AccumulatedToolCall {
	if len(toolCalls) == 0 {
		return nil
	}
	return append([]AccumulatedToolCall(nil), toolCalls...)
}

func renderUsage(usage UsageObservation) *chatCompletionUsage {
	if usage.InputTokens == nil && usage.OutputTokens == nil && usage.TotalTokens == nil {
		return nil
	}
	return &chatCompletionUsage{
		PromptTokens:     cloneInt(usage.InputTokens),
		CompletionTokens: cloneInt(usage.OutputTokens),
		TotalTokens:      cloneInt(usage.TotalTokens),
	}
}
