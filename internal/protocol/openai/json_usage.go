package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

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

	root, err := scanKnownObjectMembers(data, map[string]struct{}{"usage": {}})
	if err != nil {
		if errors.Is(err, errDuplicateKnownMember) {
			return JSONUsageResult{}, fmt.Errorf("%w: root usage", ErrInvalidJSONUsage)
		}
		// Do not include encoding/json's error text: keeping this independent of
		// the input protects response secrets from observation errors.
		return JSONUsageResult{}, ErrMalformedJSON
	}
	return parseJSONUsageObject(root["usage"])
}

// parseJSONUsageObject normalizes one known usage object. Keeping this helper
// shared by complete JSON responses and SSE observation makes the two protocol
// surfaces use exactly the same field and alias semantics.
func parseJSONUsageObject(raw json.RawMessage) (JSONUsageResult, error) {
	if len(raw) == 0 || isJSONNull(raw) {
		return JSONUsageResult{}, nil
	}

	usageFields, err := decodeJSONUsageObject(raw)
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
	members, err := scanKnownObjectMembers(raw, knownUsageFields)
	if err != nil {
		return jsonUsageFields{}, fmt.Errorf("%w: usage", ErrInvalidJSONUsage)
	}
	return jsonUsageFields{
		PromptTokens:            members["prompt_tokens"],
		CompletionTokens:        members["completion_tokens"],
		InputTokens:             members["input_tokens"],
		OutputTokens:            members["output_tokens"],
		TotalTokens:             members["total_tokens"],
		PromptTokensDetails:     members["prompt_tokens_details"],
		CompletionTokensDetails: members["completion_tokens_details"],
		InputTokensDetails:      members["input_tokens_details"],
		OutputTokensDetails:     members["output_tokens_details"],
	}, nil
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
	members, err := scanKnownObjectMembers(raw, knownDetailFields)
	if err != nil {
		return jsonTokenDetails{}, fmt.Errorf("%w: %s", ErrInvalidJSONUsage, name)
	}
	return jsonTokenDetails{
		CachedTokens:    members["cached_tokens"],
		ReasoningTokens: members["reasoning_tokens"],
	}, nil
}

var (
	errDuplicateKnownMember = errors.New("duplicate known JSON member")
	knownUsageFields        = map[string]struct{}{
		"prompt_tokens": {}, "completion_tokens": {}, "input_tokens": {}, "output_tokens": {},
		"total_tokens": {}, "prompt_tokens_details": {}, "completion_tokens_details": {},
		"input_tokens_details": {}, "output_tokens_details": {},
	}
	knownDetailFields = map[string]struct{}{"cached_tokens": {}, "reasoning_tokens": {}}
)

// scanKnownObjectMembers is a small strictness boundary for the known usage
// surface. encoding/json intentionally accepts duplicate object members and
// keeps only one value when decoding into a struct or map. That behavior can
// hide an invalid earlier known value, so this scanner rejects duplicate known
// names while retaining the usual unknown-field tolerance. It returns no input
// data in its errors.
func scanKnownObjectMembers(raw []byte, known map[string]struct{}) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return nil, errors.New("malformed JSON object")
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != '{' {
		return nil, errors.New("expected JSON object")
	}

	members := make(map[string]json.RawMessage)
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, ok := keyToken.(string)
		if err != nil || !ok {
			return nil, errors.New("malformed JSON object")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, errors.New("malformed JSON object")
		}
		if _, isKnown := known[key]; isKnown {
			if _, duplicate := seen[key]; duplicate {
				return nil, errDuplicateKnownMember
			}
			seen[key] = struct{}{}
		}
		members[key] = value
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return nil, errors.New("malformed JSON object")
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("malformed JSON object")
	}
	return members, nil
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
