package openai

import "testing"

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
