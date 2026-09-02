package openai

import (
	"bytes"
	"errors"
	"testing"
)

func TestRequestMetadataStreamStates(t *testing.T) {
	tests := []struct {
		name  string
		value *bool
		want  *bool
	}{
		{name: "absent", value: nil, want: nil},
		{name: "false", value: boolPointer(false), want: boolPointer(false)},
		{name: "true", value: boolPointer(true), want: boolPointer(true)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metadata := RequestMetadata{Stream: test.value}

			if (metadata.Stream == nil) != (test.want == nil) {
				t.Fatalf("Stream nil = %t, want %t", metadata.Stream == nil, test.want == nil)
			}
			if metadata.Stream != nil && *metadata.Stream != *test.want {
				t.Fatalf("Stream = %t, want %t", *metadata.Stream, *test.want)
			}
		})
	}
}

func TestRequestMetadataTokenStates(t *testing.T) {
	tests := []struct {
		name  string
		value *int
		want  *int
	}{
		{name: "absent", value: nil, want: nil},
		{name: "zero", value: intPointer(0), want: intPointer(0)},
		{name: "positive", value: intPointer(128), want: intPointer(128)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metadata := RequestMetadata{
				MaxTokens:           test.value,
				MaxCompletionTokens: test.value,
			}

			assertIntPointer(t, "MaxTokens", metadata.MaxTokens, test.want)
			assertIntPointer(t, "MaxCompletionTokens", metadata.MaxCompletionTokens, test.want)
		})
	}
}

func TestRequestMetadataModel(t *testing.T) {
	metadata := RequestMetadata{Model: "gpt-test"}

	if metadata.Model != "gpt-test" {
		t.Fatalf("Model = %q, want %q", metadata.Model, "gpt-test")
	}
}

func TestParseRequestMetadata(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		want       RequestMetadata
		wantErr    error
		wantErrFor string
	}{
		{
			name:  "all inspected fields",
			input: `{"model":"gpt-test","stream":true,"max_tokens":128,"max_completion_tokens":256}`,
			want: RequestMetadata{
				Model:               "gpt-test",
				Stream:              boolPointer(true),
				MaxTokens:           intPointer(128),
				MaxCompletionTokens: intPointer(256),
			},
		},
		{
			name:  "stream false",
			input: `{"stream":false}`,
			want:  RequestMetadata{Stream: boolPointer(false)},
		},
		{
			name:  "stream absent",
			input: `{}`,
			want:  RequestMetadata{},
		},
		{
			name:  "token fields absent",
			input: `{"model":"gpt-test"}`,
			want:  RequestMetadata{Model: "gpt-test"},
		},
		{
			name:  "max tokens zero",
			input: `{"max_tokens":0}`,
			want:  RequestMetadata{MaxTokens: intPointer(0)},
		},
		{
			name:  "max completion tokens zero",
			input: `{"max_completion_tokens":0}`,
			want:  RequestMetadata{MaxCompletionTokens: intPointer(0)},
		},
		{
			name:  "both token fields zero",
			input: `{"max_tokens":0,"max_completion_tokens":0}`,
			want: RequestMetadata{
				MaxTokens:           intPointer(0),
				MaxCompletionTokens: intPointer(0),
			},
		},
		{
			name:  "unknown nested fields",
			input: `{"model":"gpt-test","messages":[{"role":"user","content":[{"type":"text","text":"kept untouched"}],"tool_calls":[{"function":{"arguments":"{}"}}]}],"tools":[{"type":"function","function":{"parameters":{"type":"object","properties":{"nested":{"type":"string"}}}}}],"array":[{"nested":true}]}`,
			want:  RequestMetadata{Model: "gpt-test"},
		},
		{
			name:  "surrounding whitespace",
			input: " \n\t{ \"model\": \"gpt-test\", \"stream\": true } \r\n",
			want:  RequestMetadata{Model: "gpt-test", Stream: boolPointer(true)},
		},
		{
			name:       "malformed JSON",
			input:      `{"model":"gpt-test"`,
			wantErr:    ErrMalformedJSON,
			wantErrFor: "malformed JSON",
		},
		{
			name:       "model wrong type",
			input:      `{"model":123}`,
			wantErr:    ErrInvalidMetadataField,
			wantErrFor: "model",
		},
		{
			name:       "stream wrong type",
			input:      `{"stream":"true"}`,
			wantErr:    ErrInvalidMetadataField,
			wantErrFor: "stream",
		},
		{
			name:       "max tokens wrong type",
			input:      `{"max_tokens":false}`,
			wantErr:    ErrInvalidMetadataField,
			wantErrFor: "max_tokens",
		},
		{
			name:       "max completion tokens wrong type",
			input:      `{"max_completion_tokens":[]}`,
			wantErr:    ErrInvalidMetadataField,
			wantErrFor: "max_completion_tokens",
		},
		{
			name:       "known null is not absent",
			input:      `{"stream":null}`,
			wantErr:    ErrInvalidMetadataField,
			wantErrFor: "stream",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := []byte(test.input)
			before := append([]byte(nil), input...)

			got, err := ParseRequestMetadata(input)

			if !bytes.Equal(input, before) {
				t.Fatalf("input changed from %q to %q", before, input)
			}
			if test.wantErr == nil {
				if err != nil {
					t.Fatalf("ParseRequestMetadata error = %v, want nil", err)
				}
				assertRequestMetadata(t, got, test.want)
				return
			}
			if err == nil {
				t.Fatalf("ParseRequestMetadata error = nil, want %s", test.wantErrFor)
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ParseRequestMetadata error = %v, want errors.Is(..., %v)", err, test.wantErr)
			}
			if test.wantErrFor != "" && !bytes.Contains([]byte(err.Error()), []byte(test.wantErrFor)) {
				t.Fatalf("ParseRequestMetadata error = %v, want context %q", err, test.wantErrFor)
			}
		})
	}
}

func assertRequestMetadata(t *testing.T, got, want RequestMetadata) {
	t.Helper()
	if got.Model != want.Model {
		t.Fatalf("Model = %q, want %q", got.Model, want.Model)
	}
	assertBoolPointer(t, "Stream", got.Stream, want.Stream)
	assertIntPointer(t, "MaxTokens", got.MaxTokens, want.MaxTokens)
	assertIntPointer(t, "MaxCompletionTokens", got.MaxCompletionTokens, want.MaxCompletionTokens)
}

func assertBoolPointer(t *testing.T, name string, got, want *bool) {
	t.Helper()
	if (got == nil) != (want == nil) {
		t.Fatalf("%s nil = %t, want %t", name, got == nil, want == nil)
	}
	if got != nil && *got != *want {
		t.Fatalf("%s = %t, want %t", name, *got, *want)
	}
}

func assertIntPointer(t *testing.T, name string, got, want *int) {
	t.Helper()

	if (got == nil) != (want == nil) {
		t.Fatalf("%s nil = %t, want %t", name, got == nil, want == nil)
	}
	if got != nil && *got != *want {
		t.Fatalf("%s = %d, want %d", name, *got, *want)
	}
}

func boolPointer(value bool) *bool {
	return &value
}

func intPointer(value int) *int {
	return &value
}
