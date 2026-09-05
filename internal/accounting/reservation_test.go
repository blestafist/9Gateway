package accounting

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

func TestPlanReservationOutputPrecedence(t *testing.T) {
	completion, legacy, responses := int64(20), int64(30), int64(40)
	tests := []struct {
		name      string
		responses bool
		metadata  ReservationMetadata
		want      int64
		source    ReservationOutputSource
	}{
		{name: "chat completion wins", metadata: ReservationMetadata{MaxCompletionTokens: &completion, MaxTokens: &legacy, MaxOutputTokens: &responses}, want: 20, source: OutputSourceExplicit},
		{name: "chat legacy fallback", metadata: ReservationMetadata{MaxTokens: &legacy}, want: 30, source: OutputSourceExplicit},
		{name: "responses output wins", responses: true, metadata: ReservationMetadata{MaxCompletionTokens: &completion, MaxTokens: &legacy, MaxOutputTokens: &responses}, want: 40, source: OutputSourceExplicit},
		{name: "responses completion fallback", responses: true, metadata: ReservationMetadata{MaxCompletionTokens: &completion, MaxTokens: &legacy}, want: 20, source: OutputSourceExplicit},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, err := PlanReservation(ReservationOptions{
				Mode: ReservationModeEstimate, UnknownInputFallback: 7, FallbackMaxOutputTokens: 99,
				ResponsesEndpoint: test.responses, Metadata: test.metadata,
				Input: mustInput(t, 5), Quality: EstimateQualityKnown,
			})
			if err != nil || plan.Output.Int64() != test.want || plan.OutputSource != test.source {
				t.Fatalf("plan = %#v, error = %v", plan, err)
			}
		})
	}
}

func TestPlanReservationZeroAndFallback(t *testing.T) {
	zero := int64(0)
	plan, err := PlanReservation(ReservationOptions{
		Mode: ReservationModeEstimate, UnknownInputFallback: 7, FallbackMaxOutputTokens: 11,
		Metadata: ReservationMetadata{MaxCompletionTokens: &zero}, Input: mustInput(t, 5), Quality: EstimateQualityKnown,
	})
	if err != nil || plan.Output.Int64() != 11 || plan.OutputSource != OutputSourceFallback {
		t.Fatalf("zero plan = %#v, error = %v", plan, err)
	}
	if plan.Total.Int64() != 16 || plan.Total.Int64() <= 0 {
		t.Fatalf("total = %d, want 16", plan.Total.Int64())
	}
}

func TestPlanReservationModesAndQuality(t *testing.T) {
	input := mustInput(t, 23)
	for _, test := range []struct {
		name       string
		mode       ReservationMode
		quality    EstimateQuality
		wantInput  int64
		wantSource ReservationInputSource
	}{
		{name: "known estimate", mode: ReservationModeEstimate, quality: EstimateQualityKnown, wantInput: 23, wantSource: InputSourceEstimate},
		{name: "unknown estimate fallback", mode: ReservationModeEstimate, quality: EstimateQualityUnknown, wantInput: 7, wantSource: InputSourceUnknownFallback},
		{name: "usage only ignores estimate", mode: ReservationModeUsageOnly, quality: EstimateQualityKnown, wantInput: 7, wantSource: InputSourceUnknownFallback},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, err := PlanReservation(ReservationOptions{Mode: test.mode, Quality: test.quality, Input: input, UnknownInputFallback: 7, FallbackMaxOutputTokens: 9})
			if err != nil || plan.Input.Int64() != test.wantInput || plan.InputSource != test.wantSource {
				t.Fatalf("plan = %#v, error = %v", plan, err)
			}
		})
	}
}

func TestPlanReservationRejectsNegativeAndOverflow(t *testing.T) {
	negative := int64(-1)
	_, err := PlanReservation(ReservationOptions{Mode: ReservationModeEstimate, Quality: EstimateQualityKnown, Input: mustInput(t, 1), UnknownInputFallback: 1, FallbackMaxOutputTokens: 1, Metadata: ReservationMetadata{MaxTokens: &negative}})
	if !errors.Is(err, ErrReservationUnavailable) {
		t.Fatalf("negative error = %v", err)
	}
	max := int64(math.MaxInt64)
	_, err = PlanReservation(ReservationOptions{Mode: ReservationModeEstimate, Quality: EstimateQualityKnown, Input: mustInput(t, 1), UnknownInputFallback: 1, FallbackMaxOutputTokens: 1, Metadata: ReservationMetadata{MaxTokens: &max}})
	if !errors.Is(err, ErrReservationOverflow) {
		t.Fatalf("overflowing output error = %v", err)
	}
	maxInput := mustInput(t, math.MaxInt64)
	_, err = PlanReservation(ReservationOptions{Mode: ReservationModeEstimate, Quality: EstimateQualityKnown, Input: maxInput, UnknownInputFallback: 1, FallbackMaxOutputTokens: 1})
	if !errors.Is(err, ErrReservationOverflow) {
		t.Fatalf("overflowing total error = %v", err)
	}
}

func TestPlanReservationDoesNotMutateInputs(t *testing.T) {
	value := int64(12)
	metadata := ReservationMetadata{MaxCompletionTokens: &value}
	before := reflect.ValueOf(metadata).Interface()
	input := mustInput(t, 4)
	plan, err := PlanReservation(ReservationOptions{Mode: ReservationModeEstimate, Quality: EstimateQualityKnown, Input: input, UnknownInputFallback: 8, FallbackMaxOutputTokens: 9, Metadata: metadata})
	if err != nil || plan.Total.Int64() != 16 {
		t.Fatalf("plan = %#v, error = %v", plan, err)
	}
	if !reflect.DeepEqual(before, metadata) || value != 12 || input.Int64() != 4 {
		t.Fatal("planner mutated input metadata")
	}
}

func mustInput(t *testing.T, value int64) InputTokens {
	t.Helper()
	input, err := NewInputTokens(value)
	if err != nil {
		t.Fatal(err)
	}
	return input
}
