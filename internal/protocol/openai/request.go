package openai

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
