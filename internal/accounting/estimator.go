package accounting

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

// EstimateQuality describes whether an estimator produced a usable input
// token count. Unknown is distinct from a known count of zero.
type EstimateQuality uint8

const (
	EstimateQualityUnknown EstimateQuality = iota
	EstimateQualityKnown
)

const (
	EstimateUnknown = EstimateQualityUnknown
	EstimateKnown   = EstimateQualityKnown
)

// Known reports whether an estimator supplied a usable count.
func (quality EstimateQuality) Known() bool { return quality == EstimateQualityKnown }

// TokenEstimator is the narrow accounting contract for preflight estimation.
// The caller must provide model and requestBytes already bounded according to
// deployment configuration. Implementations must not read HTTP bodies, access
// storage or depend on policy and pricing packages.
type TokenEstimator interface {
	EstimateInputTokens(model string, requestBytes []byte) (InputTokens, EstimateQuality, error)
}

// InputEstimator is a descriptive alias for the preflight input estimator
// contract.
type InputEstimator = TokenEstimator

// UsageOnlyEstimator implements the T084 usage_only strategy. It deliberately
// does not inspect requestBytes, so callers can use it without doing any
// parsing or approximation when only upstream usage is desired.
type UsageOnlyEstimator struct{}

func (UsageOnlyEstimator) EstimateInputTokens(_ string, _ []byte) (InputTokens, EstimateQuality, error) {
	return UnknownInputTokens(), EstimateQualityUnknown, nil
}

// NewUsageOnlyEstimator returns the no-op estimator for usage_only mode.
func NewUsageOnlyEstimator() UsageOnlyEstimator { return UsageOnlyEstimator{} }

// ErrEstimateMalformed indicates that a known input field was not valid JSON
// of the shape required by the small set of fields understood by the
// approximate estimator. The error intentionally contains no request data.
var ErrEstimateMalformed = errors.New("accounting: malformed estimable input")

// ErrEstimateOverflow indicates that the estimate could not be represented by
// the package's signed token-count type.
var ErrEstimateOverflow = errors.New("accounting: estimate overflow")

const (
	// ApproximateEstimatorDefaultFallback is deliberately the same safe value
	// as the deployment default, without making accounting depend on config.
	ApproximateEstimatorDefaultFallback int64 = 4 * 1024
	approximateBytesPerToken                  = int64(4)
	messageFrameTokens                        = int64(4)
	toolFrameTokens                           = int64(8)
)

// ApproximateEstimator implements the T084 estimate strategy. It accepts only
// an already bounded byte slice: body inspection and replay belong to the
// caller. Model is accepted to satisfy TokenEstimator, but is intentionally
// unused because this is one model-independent approximation.
//
// The approximation is deliberately conservative and deterministic:
// each non-empty textual value costs ceil(UTF-8 bytes / 4) + 1 token, each
// message costs four framing tokens, and each tool costs eight framing tokens.
// Tool schema JSON is treated as a raw fragment and costs the same byte rule;
// it is not recursively decoded. Unsupported or malformed input uses the
// configured fallback and is marked unknown.
//
// Bifrost provenance review (03ab391865710462302bbcf52dca2f32682b91b5,
// branch dev): inspected core/providers/bedrock/count_tokens.go and
// core/providers/bedrock/count_tokens_test.go, whose independent heuristic is
// serialized-body bytes rounded up at four bytes per token. Also inspected
// core/go.mod, LICENSE, and THIRD_PARTY_NOTICES.md. Bifrost is Apache-2.0;
// its core has a large transitive dependency tree and no tokenizer dependency
// relevant to this strategy. This file copies no Bifrost code or schemas and
// adds no dependency: an independent approximation is safer here because the
// gateway must inspect only selected OpenAI-compatible fields and preserve its
// narrow package boundaries. Consequently there is no adapted third-party
// source or additional license chain to carry.
type ApproximateEstimator struct {
	fallback int64
}

// NewApproximateEstimator creates an approximate estimator. With no argument,
// it uses ApproximateEstimatorDefaultFallback. A non-positive argument is
// treated as the default so a zero-value-friendly constructor cannot return an
// invalid InputTokens value.
func NewApproximateEstimator(fallback ...int64) *ApproximateEstimator {
	value := ApproximateEstimatorDefaultFallback
	if len(fallback) > 0 && fallback[0] > 0 {
		value = fallback[0]
	}
	return &ApproximateEstimator{fallback: value}
}

// NewApproximateTokenEstimator is a descriptive constructor alias.
func NewApproximateTokenEstimator(fallback ...int64) *ApproximateEstimator {
	return NewApproximateEstimator(fallback...)
}

// EstimateInputTokens estimates textual input in an already-bounded JSON
// request. Unsupported input and malformed known fields return a fallback and
// unknown quality. A malformed JSON document additionally returns
// ErrEstimateMalformed; callers that do not enforce estimation may use the
// returned fallback while recording the error.
func (estimator ApproximateEstimator) EstimateInputTokens(_ string, requestBytes []byte) (InputTokens, EstimateQuality, error) {
	fallback := estimator.fallback
	if fallback <= 0 {
		fallback = ApproximateEstimatorDefaultFallback
	}
	fallbackTokens, err := NewInputTokens(fallback)
	if err != nil {
		return UnknownInputTokens(), EstimateQualityUnknown, fmt.Errorf("create estimate fallback: %w", err)
	}

	root, err := decodeObject(requestBytes)
	if err != nil {
		return fallbackTokens, EstimateQualityUnknown, err
	}

	total := int64(0)
	unknown := false
	if raw, ok := root["messages"]; ok {
		value, state, fieldErr := estimateMessages(raw)
		if fieldErr != nil {
			return fallbackTokens, EstimateQualityUnknown, fieldErr
		} else if state == estimateUnsupported {
			unknown = true
		} else if total, err = addEstimate(total, value); err != nil {
			return fallbackTokens, EstimateQualityUnknown, err
		}
	}
	if raw, ok := root["input"]; ok {
		value, state, fieldErr := estimateResponsesInput(raw)
		if fieldErr != nil {
			return fallbackTokens, EstimateQualityUnknown, fieldErr
		} else if state == estimateUnsupported {
			unknown = true
		} else if total, err = addEstimate(total, value); err != nil {
			return fallbackTokens, EstimateQualityUnknown, err
		}
	}
	if raw, ok := root["instructions"]; ok {
		value, fieldErr := estimateStringField(raw)
		if fieldErr != nil {
			return fallbackTokens, EstimateQualityUnknown, fmt.Errorf("%w: instructions", fieldErr)
		} else if total, err = addEstimate(total, value); err != nil {
			return fallbackTokens, EstimateQualityUnknown, err
		}
	}
	if raw, ok := root["tools"]; ok {
		value, state, fieldErr := estimateTools(raw)
		if fieldErr != nil {
			return fallbackTokens, EstimateQualityUnknown, fieldErr
		} else if state == estimateUnsupported {
			unknown = true
		} else if total, err = addEstimate(total, value); err != nil {
			return fallbackTokens, EstimateQualityUnknown, err
		}
	}

	if unknown {
		return fallbackTokens, EstimateQualityUnknown, nil
	}
	result, err := NewInputTokens(total)
	if err != nil {
		return UnknownInputTokens(), EstimateQualityUnknown, fmt.Errorf("create input estimate: %w", err)
	}
	return result, EstimateQualityKnown, nil
}

type estimateState uint8

const (
	estimateKnown estimateState = iota
	estimateUnsupported
)

func decodeObject(raw []byte) (map[string]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("%w: expected object", ErrEstimateMalformed)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil || object == nil {
		return nil, fmt.Errorf("%w: invalid JSON object", ErrEstimateMalformed)
	}
	return object, nil
}

func estimateMessages(raw []byte) (int64, estimateState, error) {
	var messages []json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil || messages == nil {
		return 0, estimateKnown, fmt.Errorf("%w: messages", ErrEstimateMalformed)
	}
	total := int64(0)
	for _, rawMessage := range messages {
		message, err := decodeObject(rawMessage)
		if err != nil {
			return 0, estimateKnown, err
		}
		value, state, err := estimateMessage(message)
		if err != nil {
			return 0, estimateKnown, err
		}
		if state == estimateUnsupported {
			return 0, state, nil
		}
		total, err = addEstimate(total, value)
		if err != nil {
			return 0, estimateKnown, err
		}
	}
	return total, estimateKnown, nil
}

func estimateMessage(message map[string]json.RawMessage) (int64, estimateState, error) {
	total := messageFrameTokens
	for _, field := range []string{"role", "name"} {
		if raw, ok := message[field]; ok {
			value, err := estimateStringField(raw)
			if err != nil {
				return 0, estimateKnown, fmt.Errorf("%w: message %s", ErrEstimateMalformed, field)
			}
			total, err = addEstimate(total, value)
			if err != nil {
				return 0, estimateKnown, err
			}
		}
	}
	raw, ok := message["content"]
	if !ok {
		return total, estimateKnown, nil
	}
	value, state, err := estimateContent(raw)
	if err != nil {
		return 0, estimateKnown, err
	}
	if state == estimateUnsupported {
		return 0, state, nil
	}
	total, err = addEstimate(total, value)
	return total, estimateKnown, err
}

func estimateContent(raw []byte) (int64, estimateState, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, estimateKnown, fmt.Errorf("%w: content", ErrEstimateMalformed)
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return estimateText(text)
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil || parts == nil {
		return 0, estimateKnown, fmt.Errorf("%w: content", ErrEstimateMalformed)
	}
	total := int64(0)
	for _, rawPart := range parts {
		part, err := decodeObject(rawPart)
		if err != nil {
			return 0, estimateKnown, err
		}
		typeName, ok := part["type"]
		if !ok {
			return 0, estimateUnsupported, nil
		}
		kind, err := decodeString(typeName)
		if err != nil {
			return 0, estimateKnown, fmt.Errorf("%w: content part type", ErrEstimateMalformed)
		}
		if kind != "text" && kind != "input_text" && kind != "output_text" {
			return 0, estimateUnsupported, nil
		}
		value, ok := part["text"]
		if !ok {
			return 0, estimateKnown, fmt.Errorf("%w: text content", ErrEstimateMalformed)
		}
		partTotal, err := estimateStringField(value)
		if err != nil {
			return 0, estimateKnown, fmt.Errorf("%w: text content", ErrEstimateMalformed)
		}
		total, err = addEstimate(total, partTotal)
		if err != nil {
			return 0, estimateKnown, err
		}
	}
	return total, estimateKnown, nil
}

func estimateResponsesInput(raw []byte) (int64, estimateState, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, estimateKnown, fmt.Errorf("%w: input", ErrEstimateMalformed)
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return estimateText(text)
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil || items == nil {
		return 0, estimateKnown, fmt.Errorf("%w: input", ErrEstimateMalformed)
	}
	total := int64(0)
	for _, rawItem := range items {
		item, err := decodeObject(rawItem)
		if err != nil {
			return 0, estimateKnown, err
		}
		kind := "message"
		if rawType, ok := item["type"]; ok {
			var typeErr error
			kind, typeErr = decodeString(rawType)
			if typeErr != nil {
				return 0, estimateKnown, fmt.Errorf("%w: input item type", ErrEstimateMalformed)
			}
		}
		switch kind {
		case "message":
			value, state, err := estimateMessage(item)
			if err != nil {
				return 0, estimateKnown, err
			}
			if state == estimateUnsupported {
				return 0, state, nil
			}
			total, err = addEstimate(total, value)
			if err != nil {
				return 0, estimateKnown, err
			}
		case "input_text", "text":
			rawText, ok := item["text"]
			if !ok {
				return 0, estimateKnown, fmt.Errorf("%w: input text", ErrEstimateMalformed)
			}
			value, err := estimateStringField(rawText)
			if err != nil {
				return 0, estimateKnown, fmt.Errorf("%w: input text", ErrEstimateMalformed)
			}
			total, err = addEstimate(total, value)
			if err != nil {
				return 0, estimateKnown, err
			}
		default:
			return 0, estimateUnsupported, nil
		}
	}
	return total, estimateKnown, nil
}

func estimateTools(raw []byte) (int64, estimateState, error) {
	var tools []json.RawMessage
	if json.Unmarshal(raw, &tools) != nil || tools == nil {
		return 0, estimateKnown, fmt.Errorf("%w: tools", ErrEstimateMalformed)
	}
	total := int64(0)
	for _, rawTool := range tools {
		tool, err := decodeObject(rawTool)
		if err != nil {
			return 0, estimateKnown, err
		}
		if rawType, ok := tool["type"]; ok {
			typeName, typeErr := decodeString(rawType)
			if typeErr != nil {
				return 0, estimateKnown, fmt.Errorf("%w: tool type", ErrEstimateMalformed)
			}
			if typeName != "function" {
				return 0, estimateUnsupported, nil
			}
		}
		function := tool
		if functionRaw, nested := tool["function"]; nested {
			function, err = decodeObject(functionRaw)
			if err != nil {
				return 0, estimateKnown, fmt.Errorf("%w: tool function", ErrEstimateMalformed)
			}
		}
		if _, hasName := function["name"]; !hasName {
			return 0, estimateKnown, fmt.Errorf("%w: tool name", ErrEstimateMalformed)
		}
		value := toolFrameTokens
		for _, field := range []string{"name", "description"} {
			if fieldRaw, ok := function[field]; ok {
				fieldValue, err := estimateStringField(fieldRaw)
				if err != nil {
					return 0, estimateKnown, fmt.Errorf("%w: tool %s", ErrEstimateMalformed, field)
				}
				value, err = addEstimate(value, fieldValue)
				if err != nil {
					return 0, estimateKnown, err
				}
			}
		}
		if schema, ok := function["parameters"]; ok {
			if bytes.Equal(bytes.TrimSpace(schema), []byte("null")) {
				return 0, estimateKnown, fmt.Errorf("%w: tool schema", ErrEstimateMalformed)
			}
			schemaValue, err := estimateJSONFragment(schema)
			if err != nil {
				return 0, estimateKnown, err
			}
			value, err = addEstimate(value, schemaValue)
			if err != nil {
				return 0, estimateKnown, err
			}
		}
		total, err = addEstimate(total, value)
		if err != nil {
			return 0, estimateKnown, err
		}
	}
	return total, estimateKnown, nil
}

func estimateStringField(raw []byte) (int64, error) {
	text, err := decodeString(raw)
	if err != nil {
		return 0, ErrEstimateMalformed
	}
	value, _, err := estimateText(text)
	return value, err
}

func decodeString(raw []byte) (string, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", ErrEstimateMalformed
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return "", ErrEstimateMalformed
	}
	return text, nil
}

func estimateText(text string) (int64, estimateState, error) {
	if len(text) == 0 {
		return 0, estimateKnown, nil
	}
	bytesCount := int64(len([]byte(text)))
	if bytesCount > math.MaxInt64-(approximateBytesPerToken-1) {
		return 0, estimateKnown, ErrEstimateOverflow
	}
	value := (bytesCount + approximateBytesPerToken - 1) / approximateBytesPerToken
	value, err := addEstimate(value, 1)
	return value, estimateKnown, err
}

func estimateJSONFragment(raw []byte) (int64, error) {
	if len(raw) == 0 {
		return 0, ErrEstimateMalformed
	}
	bytesCount := int64(len(raw))
	if bytesCount > math.MaxInt64-(approximateBytesPerToken-1) {
		return 0, ErrEstimateOverflow
	}
	return (bytesCount + approximateBytesPerToken - 1) / approximateBytesPerToken, nil
}

func addEstimate(left, right int64) (int64, error) {
	if left < 0 || right < 0 || left > math.MaxInt64-right {
		return 0, ErrEstimateOverflow
	}
	return left + right, nil
}
