package accounting

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestApproximateEstimator(t *testing.T) {
	estimator := NewApproximateEstimator(99)
	tests := []struct {
		name    string
		body    string
		want    int64
		quality EstimateQuality
		err     error
	}{
		{name: "plain messages", body: `{"messages":[{"role":"system","content":"You are helpful."},{"role":"user","name":"Ada","content":"Hello"},{"role":"assistant","content":"Hi"}]}`, want: 57, quality: EstimateQualityKnown},
		{name: "responses string", body: `{"input":"Hello"}`, want: 5, quality: EstimateQualityKnown},
		{name: "responses items", body: `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Hello"}]},{"type":"input_text","text":"world"}]}`, want: 18, quality: EstimateQualityKnown},
		{name: "unicode uses UTF-8 bytes", body: `{"messages":[{"role":"user","content":"你好"}]}`, want: 14, quality: EstimateQualityKnown},
		{name: "tools and schema", body: `{"messages":[],"tools":[{"type":"function","function":{"name":"weather","description":"Get weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}]}`, want: 83, quality: EstimateQualityKnown},
		{name: "empty input", body: `{"messages":[]}`, want: 0, quality: EstimateQualityKnown},
		{name: "multimodal fallback", body: `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"secret-image"}}]}]}`, want: 99, quality: EstimateQualityUnknown},
		{name: "unknown response item fallback", body: `{"input":[{"type":"computer_use_preview","id":"secret-id"}]}`, want: 99, quality: EstimateQualityUnknown},
		{name: "malformed known field", body: `{"messages":[{"role":"user","content":null}]}`, want: 99, quality: EstimateQualityUnknown, err: ErrEstimateMalformed},
		{name: "unknown fields ignored", body: `{"messages":[],"future":{"secret":"not counted"}}`, want: 0, quality: EstimateQualityKnown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, quality, err := estimator.EstimateInputTokens("model", []byte(test.body))
			if got.Int64() != test.want || quality != test.quality {
				t.Fatalf("estimate = %d/%v, want %d/%v", got.Int64(), quality, test.want, test.quality)
			}
			if test.err != nil && !errors.Is(err, test.err) {
				t.Fatalf("error = %v, want %v", err, test.err)
			}
			if test.err == nil && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.Contains(errString(err), "secret") {
				t.Fatalf("error contains request text: %v", err)
			}
		})
	}
}

func TestApproximateEstimatorMalformedJSONAndImmutability(t *testing.T) {
	estimator := NewApproximateEstimator(7)
	body := []byte(`{"messages":[{"role":"user","content":null}]}`)
	want := append([]byte(nil), body...)
	got, quality, err := estimator.EstimateInputTokens("model", body)
	if !errors.Is(err, ErrEstimateMalformed) || quality != EstimateQualityUnknown || got.Int64() != 7 {
		t.Fatalf("malformed estimate = %d/%v/%v", got.Int64(), quality, err)
	}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("estimator modified request bytes")
	}

	for _, body := range [][]byte{nil, []byte(`not-json`), []byte(`{"messages":`)} {
		got, quality, err := estimator.EstimateInputTokens("model", body)
		if got.Int64() != 7 || quality != EstimateQualityUnknown || !errors.Is(err, ErrEstimateMalformed) {
			t.Errorf("body %q = %d/%v/%v, want fallback/unknown/malformed", body, got.Int64(), quality, err)
		}
	}
}

func TestUsageOnlyEstimatorDoesNotParse(t *testing.T) {
	got, quality, err := (UsageOnlyEstimator{}).EstimateInputTokens("model", []byte(`definitely not JSON with private text`))
	if got.Known() || quality != EstimateQualityUnknown || err != nil {
		t.Fatalf("usage-only estimate = %d/%v/%v, want unknown/nil", got.Int64(), quality, err)
	}
}

func TestEstimateArithmeticChecksOverflow(t *testing.T) {
	if _, err := addEstimate(math.MaxInt64, 1); !errors.Is(err, ErrEstimateOverflow) {
		t.Fatalf("addEstimate overflow = %v", err)
	}
	if _, err := addEstimate(-1, 1); !errors.Is(err, ErrEstimateOverflow) {
		t.Fatalf("addEstimate negative = %v", err)
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
