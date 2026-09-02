package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrMalformedJSON indicates that request metadata could not be decoded as a
// JSON object.
var ErrMalformedJSON = errors.New("openai: malformed JSON")

// ErrInvalidMetadataField indicates that an inspected metadata field has a
// type other than the one accepted by RequestMetadata.
var ErrInvalidMetadataField = errors.New("openai: invalid metadata field")

// RequestMetadata is the partial request metadata observed by the gateway.
//
// It is intentionally not a complete OpenAI request model. A nil pointer means
// that the corresponding optional JSON field was absent; a non-nil pointer
// preserves its explicit value, including false or zero.
type RequestMetadata struct {
	Model               string `json:"model,omitempty"`
	Stream              *bool  `json:"stream,omitempty"`
	MaxTokens           *int   `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int   `json:"max_completion_tokens,omitempty"`
}

// ParseRequestMetadata extracts the small set of request fields observed by
// the gateway from bounded JSON bytes. Unknown fields, including nested
// objects and arrays, are ignored. The input is only read; it is never
// modified or serialized again.
func ParseRequestMetadata(data []byte) (RequestMetadata, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return RequestMetadata{}, fmt.Errorf("%w: expected a JSON object", ErrMalformedJSON)
	}

	var fields requestMetadataJSON
	if err := json.Unmarshal(data, &fields); err != nil {
		return RequestMetadata{}, fmt.Errorf("%w: %v", ErrMalformedJSON, err)
	}

	metadata := RequestMetadata{}
	if err := decodeMetadataField(fields.Model, "model", &metadata.Model); err != nil {
		return RequestMetadata{}, err
	}
	if err := decodeMetadataField(fields.Stream, "stream", &metadata.Stream); err != nil {
		return RequestMetadata{}, err
	}
	if err := decodeMetadataField(fields.MaxTokens, "max_tokens", &metadata.MaxTokens); err != nil {
		return RequestMetadata{}, err
	}
	if err := decodeMetadataField(fields.MaxCompletionTokens, "max_completion_tokens", &metadata.MaxCompletionTokens); err != nil {
		return RequestMetadata{}, err
	}

	return metadata, nil
}

// requestMetadataJSON deliberately contains no OpenAI request body fields
// beyond the metadata inspected by this package. RawMessage lets the parser
// validate only those fields while encoding/json skips arbitrary unknown data.
type requestMetadataJSON struct {
	Model               json.RawMessage `json:"model"`
	Stream              json.RawMessage `json:"stream"`
	MaxTokens           json.RawMessage `json:"max_tokens"`
	MaxCompletionTokens json.RawMessage `json:"max_completion_tokens"`
}

func decodeMetadataField(raw json.RawMessage, name string, destination any) error {
	if len(raw) == 0 {
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("%w: %s", ErrInvalidMetadataField, name)
	}
	if err := json.Unmarshal(raw, destination); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidMetadataField, name)
	}
	return nil
}
