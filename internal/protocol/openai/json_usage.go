package openai

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/pestit/9gateway/internal/accounting"
)

// ErrInvalidJSONUsage indicates that a known usage field was present but did
// not contain a non-negative JSON integer, or that usage was not an object.
// It aliases ErrInvalidUsage so JSON and streaming observation have one
// recognizable invalid-usage sentinel.
var ErrInvalidJSONUsage = ErrInvalidUsage

// JSONUsageResult is the bounded observation returned by ParseJSONUsage.
// Observed is independent of Usage's individual presence bits: a response
// containing only a known detail count is observed, while absent, null, and
// empty/unknown usage objects are not observed.
type JSONUsageResult struct {
	Usage    accounting.Usage
	Observed bool
}

// UsageResult is the concise name used by protocol callers.
type UsageResult = JSONUsageResult

// ParseJSONUsage extracts the small, known usage surface from one complete
// OpenAI-compatible JSON response. It deliberately does not decode the
// response: only the root usage object and its known token-detail objects are
// inspected. The caller is responsible for bounding data before calling this
// function.
//
// For the two input/output pairs, the canonical chat-completion names take
// precedence over the Responses aliases, independent of JSON member order:
// prompt_tokens beats input_tokens and completion_tokens beats output_tokens.
// The same rule selects prompt_tokens_details over input_tokens_details and
// completion_tokens_details over output_tokens_details. Both members are
// still validated when present, so a malformed alias cannot be hidden by a
// valid canonical member. Within input details, cached_tokens is the known
// cached-input field; within output details, reasoning_tokens is the known
// reasoning-output field.
//
// A null usage member means that usage was not observed. Null known count
// members are invalid, because null is not an observed token count.
func ParseJSONUsage(data []byte) (JSONUsageResult, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return JSONUsageResult{}, fmt.Errorf("%w: expected a JSON object", ErrMalformedJSON)
	}

	var response jsonUsageResponse
	if err := json.Unmarshal(data, &response); err != nil {
		// Do not include encoding/json's error text: keeping this independent of
		// the input protects response secrets from observation errors.
		return JSONUsageResult{}, ErrMalformedJSON
	}
	if len(response.Usage) == 0 || isJSONNull(response.Usage) {
		return JSONUsageResult{}, nil
	}

	usageFields, err := decodeJSONUsageObject(response.Usage)
	if err != nil {
		return JSONUsageResult{}, err
	}

	prompt, err := decodeKnownCount(usageFields.PromptTokens, "prompt_tokens")
	if err != nil {
		return JSONUsageResult{}, err
	}
	input, err := decodeKnownCount(usageFields.InputTokens, "input_tokens")
	if err != nil {
		return JSONUsageResult{}, err
	}
	if prompt == nil {
		prompt = input
	}

	completion, err := decodeKnownCount(usageFields.CompletionTokens, "completion_tokens")
	if err != nil {
		return JSONUsageResult{}, err
	}
	output, err := decodeKnownCount(usageFields.OutputTokens, "output_tokens")
	if err != nil {
		return JSONUsageResult{}, err
	}
	if completion == nil {
		completion = output
	}

	total, err := decodeKnownCount(usageFields.TotalTokens, "total_tokens")
	if err != nil {
		return JSONUsageResult{}, err
	}

	promptCached, promptCachedObserved, err := decodeCachedDetails(usageFields.PromptTokensDetails, "prompt_tokens_details")
	if err != nil {
		return JSONUsageResult{}, err
	}
	inputCached, inputCachedObserved, err := decodeCachedDetails(usageFields.InputTokensDetails, "input_tokens_details")
	if err != nil {
		return JSONUsageResult{}, err
	}
	cached, cachedObserved := promptCached, promptCachedObserved
	if len(usageFields.PromptTokensDetails) == 0 {
		cached, cachedObserved = inputCached, inputCachedObserved
	}

	completionReasoning, completionReasoningObserved, err := decodeReasoningDetails(usageFields.CompletionTokensDetails, "completion_tokens_details")
	if err != nil {
		return JSONUsageResult{}, err
	}
	outputReasoning, outputReasoningObserved, err := decodeReasoningDetails(usageFields.OutputTokensDetails, "output_tokens_details")
	if err != nil {
		return JSONUsageResult{}, err
	}
	reasoning, reasoningObserved := completionReasoning, completionReasoningObserved
	if len(usageFields.CompletionTokensDetails) == 0 {
		reasoning, reasoningObserved = outputReasoning, outputReasoningObserved
	}

	observed := prompt != nil || completion != nil || total != nil || cachedObserved || reasoningObserved
	if !observed {
		return JSONUsageResult{}, nil
	}

	usage, err := accounting.NewUsage(accounting.UsageInput{
		Input:           prompt,
		Output:          completion,
		Total:           total,
		CachedInput:     cached,
		ReasoningOutput: reasoning,
	})
	if err != nil {
		return JSONUsageResult{}, fmt.Errorf("%w: %w", ErrInvalidJSONUsage, err)
	}
	return JSONUsageResult{Usage: usage, Observed: true}, nil
}

// ParseUsageJSON is an explicit alias for callers that put the format name at
// the end of the parser name.
func ParseUsageJSON(data []byte) (JSONUsageResult, error) { return ParseJSONUsage(data) }

type jsonUsageResponse struct {
	Usage json.RawMessage `json:"usage"`
}

type jsonUsageFields struct {
	PromptTokens            json.RawMessage `json:"prompt_tokens"`
	CompletionTokens        json.RawMessage `json:"completion_tokens"`
	InputTokens             json.RawMessage `json:"input_tokens"`
	OutputTokens            json.RawMessage `json:"output_tokens"`
	TotalTokens             json.RawMessage `json:"total_tokens"`
	PromptTokensDetails     json.RawMessage `json:"prompt_tokens_details"`
	CompletionTokensDetails json.RawMessage `json:"completion_tokens_details"`
	InputTokensDetails      json.RawMessage `json:"input_tokens_details"`
	OutputTokensDetails     json.RawMessage `json:"output_tokens_details"`
}

type jsonTokenDetails struct {
	CachedTokens    json.RawMessage `json:"cached_tokens"`
	ReasoningTokens json.RawMessage `json:"reasoning_tokens"`
}

func decodeJSONUsageObject(raw json.RawMessage) (jsonUsageFields, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return jsonUsageFields{}, fmt.Errorf("%w: usage", ErrInvalidJSONUsage)
	}
	var fields jsonUsageFields
	if err := json.Unmarshal(raw, &fields); err != nil {
		return jsonUsageFields{}, fmt.Errorf("%w: usage", ErrInvalidJSONUsage)
	}
	return fields, nil
}

func decodeKnownCount(raw json.RawMessage, name string) (*int64, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if isJSONNull(raw) {
		return nil, fmt.Errorf("%w: %s", ErrInvalidJSONUsage, name)
	}
	var value int64
	if err := json.Unmarshal(raw, &value); err != nil || value < 0 {
		return nil, fmt.Errorf("%w: %s", ErrInvalidJSONUsage, name)
	}
	return &value, nil
}

func decodeCachedDetails(raw json.RawMessage, name string) (*int64, bool, error) {
	if len(raw) == 0 || isJSONNull(raw) {
		return nil, false, nil
	}
	fields, err := decodeTokenDetailsObject(raw, name)
	if err != nil {
		return nil, false, err
	}
	value, err := decodeKnownCount(fields.CachedTokens, name+".cached_tokens")
	return value, value != nil, err
}

func decodeReasoningDetails(raw json.RawMessage, name string) (*int64, bool, error) {
	if len(raw) == 0 || isJSONNull(raw) {
		return nil, false, nil
	}
	fields, err := decodeTokenDetailsObject(raw, name)
	if err != nil {
		return nil, false, err
	}
	value, err := decodeKnownCount(fields.ReasoningTokens, name+".reasoning_tokens")
	return value, value != nil, err
}

func decodeTokenDetailsObject(raw json.RawMessage, name string) (jsonTokenDetails, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return jsonTokenDetails{}, fmt.Errorf("%w: %s", ErrInvalidJSONUsage, name)
	}
	var fields jsonTokenDetails
	if err := json.Unmarshal(raw, &fields); err != nil {
		return jsonTokenDetails{}, fmt.Errorf("%w: %s", ErrInvalidJSONUsage, name)
	}
	return fields, nil
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
