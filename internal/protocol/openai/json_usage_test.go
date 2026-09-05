package openai

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/pestit/9gateway/internal/accounting"
)

func TestParseJSONUsageSupportsOpenAIAndResponsesNames(t *testing.T) {
	tests := []struct {
		name                                    string
		data                                    string
		input, output, total, cached, reasoning int64
	}{
		{name: "chat completion", data: `{"usage":{"prompt_tokens":4,"completion_tokens":6,"total_tokens":10,"prompt_tokens_details":{"cached_tokens":2},"completion_tokens_details":{"reasoning_tokens":3}}}`, input: 4, output: 6, total: 10, cached: 2, reasoning: 3},
		{name: "responses", data: `{"usage":{"input_tokens":7,"output_tokens":8,"input_tokens_details":{"cached_tokens":5},"output_tokens_details":{"reasoning_tokens":4}}}`, input: 7, output: 8, total: 15, cached: 5, reasoning: 4},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := ParseJSONUsage([]byte(test.data))
			if err != nil {
				t.Fatalf("ParseJSONUsage() error = %v", err)
			}
			if !result.Observed {
				t.Fatal("Observed = false, want true")
			}
			assertUsageCounts(t, result.Usage, test.input, test.output, test.total, test.cached, test.reasoning)
		})
	}
}

func TestParseJSONUsageCanonicalNamesWinAndAliasesAreValidated(t *testing.T) {
	data := []byte(`{"usage":{"prompt_tokens":20,"input_tokens":21,"completion_tokens":30,"output_tokens":31,"prompt_tokens_details":{"cached_tokens":2},"input_tokens_details":{"cached_tokens":3},"completion_tokens_details":{"reasoning_tokens":4},"output_tokens_details":{"reasoning_tokens":5}}}`)
	result, err := ParseJSONUsage(data)
	if err != nil {
		t.Fatalf("ParseJSONUsage() error = %v", err)
	}
	assertUsageCounts(t, result.Usage, 20, 30, 50, 2, 4)

	for _, invalidAlias := range []string{
		`{"usage":{"prompt_tokens":4,"input_tokens":-1}}`,
		`{"usage":{"completion_tokens":4,"output_tokens":"bad"}}`,
		`{"usage":{"prompt_tokens":4,"input_tokens_details":{"cached_tokens":-1},"prompt_tokens_details":{"cached_tokens":2}}}`,
		`{"usage":{"completion_tokens":4,"output_tokens_details":{"reasoning_tokens":-1},"completion_tokens_details":{"reasoning_tokens":2}}}`,
	} {
		result, err := ParseJSONUsage([]byte(invalidAlias))
		if !errors.Is(err, ErrInvalidJSONUsage) {
			t.Fatalf("ParseJSONUsage(%s) error = %v, want ErrInvalidJSONUsage", invalidAlias, err)
		}
		if result.Observed {
			t.Fatalf("ParseJSONUsage(%s) returned observed result on error", invalidAlias)
		}
	}
}

func TestParseJSONUsagePreservesPartialAndExplicitZeroUsage(t *testing.T) {
	partial, err := ParseJSONUsage([]byte(`{"usage":{"prompt_tokens":0}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !partial.Observed || !partial.Usage.Input().Known() || partial.Usage.Input().Int64() != 0 {
		t.Fatalf("partial usage = %+v, want known input zero", partial)
	}
	if partial.Usage.Output().Known() || partial.Usage.Total().Known() {
		t.Fatalf("partial usage unexpectedly filled missing fields: %+v", partial.Usage)
	}

	zero, err := ParseJSONUsage([]byte(`{"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0,"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}}`))
	if err != nil {
		t.Fatal(err)
	}
	assertUsageCounts(t, zero.Usage, 0, 0, 0, 0, 0)
}

func TestParseJSONUsageAbsentNullAndUnknownUsageAreNotObserved(t *testing.T) {
	for _, data := range []string{`{}`, `{"usage":null}`, `{"usage":{}}`, `{"usage":{"future_tokens":12}}`, `{"metadata":{"usage":{"prompt_tokens":99}}}`} {
		result, err := ParseJSONUsage([]byte(data))
		if err != nil {
			t.Fatalf("ParseJSONUsage(%s) error = %v", data, err)
		}
		if result.Observed || result.Usage.Input().Known() || result.Usage.Output().Known() {
			t.Fatalf("ParseJSONUsage(%s) = %+v, want unknown", data, result)
		}
	}
}

func TestParseJSONUsageRejectsMalformedKnownDataWithoutBodyFragments(t *testing.T) {
	for _, data := range []string{
		`{"usage":{"prompt_tokens":-1}}`,
		`{"usage":{"completion_tokens":1.5}}`,
		`{"usage":{"total_tokens":"secret-token"}}`,
		`{"usage":{"prompt_tokens_details":[]}}`,
		`{"usage":{"output_tokens_details":{"reasoning_tokens":null}}}`,
		`{"usage":{"prompt_tokens":1}`, // malformed response
	} {
		_, err := ParseJSONUsage([]byte(data))
		if err == nil {
			t.Fatalf("ParseJSONUsage(%s) error = nil", data)
		}
		if strings.Contains(err.Error(), "secret-token") || strings.Contains(err.Error(), data) {
			t.Fatalf("ParseJSONUsage(%s) leaked response body in error %q", data, err)
		}
	}
}

func TestParseJSONUsageRejectsDerivedTotalOverflowAndDoesNotModifyInput(t *testing.T) {
	data := []byte(`{"usage":{"input_tokens":9223372036854775807,"output_tokens":1}}`)
	wantData := append([]byte(nil), data...)
	result, err := ParseJSONUsage(data)
	if !errors.Is(err, accounting.ErrCountOverflow) {
		t.Fatalf("ParseJSONUsage() error = %v, want accounting.ErrCountOverflow", err)
	}
	if result.Observed || !bytes.Equal(data, wantData) {
		t.Fatalf("result/input after parse = %+v/%q, want unobserved and unchanged", result, data)
	}

	for _, field := range []string{"prompt_tokens", "completion_tokens", "total_tokens"} {
		data := []byte(`{"usage":{"` + field + `":9223372036854775808}}`)
		if _, err := ParseJSONUsage(data); !errors.Is(err, ErrInvalidJSONUsage) {
			t.Fatalf("overflow in %s error = %v, want ErrInvalidJSONUsage", field, err)
		}
	}
}

func assertUsageCounts(t *testing.T, usage accounting.Usage, input, output, total, cached, reasoning int64) {
	t.Helper()
	if got := usage.Input().Int64(); got != input || !usage.Input().Known() {
		t.Errorf("input = (%d, %t), want (%d, true)", got, usage.Input().Known(), input)
	}
	if got := usage.Output().Int64(); got != output || !usage.Output().Known() {
		t.Errorf("output = (%d, %t), want (%d, true)", got, usage.Output().Known(), output)
	}
	if got := usage.Total().Int64(); got != total || !usage.Total().Known() {
		t.Errorf("total = (%d, %t), want (%d, true)", got, usage.Total().Known(), total)
	}
	if got := usage.CachedInput().Int64(); got != cached || !usage.CachedInput().Known() {
		t.Errorf("cached = (%d, %t), want (%d, true)", got, usage.CachedInput().Known(), cached)
	}
	if got := usage.ReasoningOutput().Int64(); got != reasoning || !usage.ReasoningOutput().Known() {
		t.Errorf("reasoning = (%d, %t), want (%d, true)", got, usage.ReasoningOutput().Known(), reasoning)
	}
}
