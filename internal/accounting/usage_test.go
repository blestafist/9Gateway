package accounting

import (
	"errors"
	"math"
	"testing"
)

func TestNewUsage(t *testing.T) {
	zero := int64(0)
	one := int64(1)
	two := int64(2)
	three := int64(3)
	tests := []struct {
		name               string
		input              UsageInput
		wantInput          int64
		wantInputKnown     bool
		wantOutput         int64
		wantOutputKnown    bool
		wantTotal          int64
		wantTotalKnown     bool
		wantSource         TotalSource
		wantCached         int64
		wantCachedKnown    bool
		wantReasoning      int64
		wantReasoningKnown bool
	}{
		{
			name:      "fully known and derived total",
			input:     UsageInput{Input: &one, Output: &two, CachedInput: &three, ReasoningOutput: &three},
			wantInput: 1, wantInputKnown: true, wantOutput: 2, wantOutputKnown: true,
			wantTotal: 3, wantTotalKnown: true, wantSource: TotalDerived,
			wantCached: 3, wantCachedKnown: true, wantReasoning: 3, wantReasoningKnown: true,
		},
		{
			name:      "partially known",
			input:     UsageInput{Input: &one},
			wantInput: 1, wantInputKnown: true, wantOutputKnown: false, wantTotalKnown: false,
			wantSource: TotalAbsent, wantCachedKnown: false, wantReasoningKnown: false,
		},
		{
			name:           "explicit zero",
			input:          UsageInput{Input: &zero, Output: &zero, Total: &zero},
			wantInputKnown: true, wantOutputKnown: true, wantTotalKnown: true,
			wantSource: TotalObserved,
		},
		{
			name:           "absent",
			input:          UsageInput{},
			wantInputKnown: false, wantOutputKnown: false, wantTotalKnown: false,
			wantSource: TotalAbsent,
		},
		{
			name:      "observed total wins over derivation",
			input:     UsageInput{Input: &one, Output: &two, Total: &three},
			wantInput: 1, wantInputKnown: true, wantOutput: 2, wantOutputKnown: true,
			wantTotal: 3, wantTotalKnown: true, wantSource: TotalObserved,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			usage, err := NewUsage(test.input)
			if err != nil {
				t.Fatalf("NewUsage() error = %v", err)
			}
			assertInput(t, "input", usage.Input(), test.wantInput, test.wantInputKnown)
			assertOutput(t, "output", usage.Output(), test.wantOutput, test.wantOutputKnown)
			assertTotal(t, "total", usage.Total(), test.wantTotal, test.wantTotalKnown)
			assertCached(t, "cached input", usage.CachedInput(), test.wantCached, test.wantCachedKnown)
			assertReasoning(t, "reasoning output", usage.ReasoningOutput(), test.wantReasoning, test.wantReasoningKnown)
			if usage.TotalSource() != test.wantSource {
				t.Errorf("TotalSource() = %v, want %v", usage.TotalSource(), test.wantSource)
			}
		})
	}
}

func TestNewUsageRejectsNegativeCounts(t *testing.T) {
	negative := int64(-1)
	fields := []struct {
		name  string
		input UsageInput
	}{
		{name: "input", input: UsageInput{Input: &negative}},
		{name: "output", input: UsageInput{Output: &negative}},
		{name: "total", input: UsageInput{Total: &negative}},
		{name: "cached input", input: UsageInput{CachedInput: &negative}},
		{name: "reasoning output", input: UsageInput{ReasoningOutput: &negative}},
	}
	for _, field := range fields {
		t.Run(field.name, func(t *testing.T) {
			_, err := NewUsage(field.input)
			if !errors.Is(err, ErrNegativeCount) {
				t.Fatalf("NewUsage() error = %v, want ErrNegativeCount", err)
			}
		})
	}
}

func TestNewUsageRejectsDerivedTotalOverflow(t *testing.T) {
	max := int64(math.MaxInt64)
	one := int64(1)
	_, err := NewUsage(UsageInput{Input: &max, Output: &one})
	if !errors.Is(err, ErrCountOverflow) {
		t.Fatalf("NewUsage() error = %v, want ErrCountOverflow", err)
	}
}

func TestTokenCountAddRejectsOverflowAndPreservesUnknown(t *testing.T) {
	max, err := NewTokenCount(math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	one, err := NewTokenCount(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := max.Add(one); !errors.Is(err, ErrCountOverflow) {
		t.Fatalf("Add() error = %v, want ErrCountOverflow", err)
	}
	unknown, err := UnknownTokenCount().Add(one)
	if err != nil {
		t.Fatal(err)
	}
	if unknown.Known() {
		t.Fatal("unknown plus known became known")
	}
}

func TestUsageCopiesInputValuesAndPublishesImmutableState(t *testing.T) {
	input, output, total := int64(1), int64(2), int64(3)
	usage, err := NewUsage(UsageInput{Input: &input, Output: &output, Total: &total})
	if err != nil {
		t.Fatal(err)
	}
	input, output, total = 10, 20, 30
	assertInput(t, "input", usage.Input(), 1, true)
	assertOutput(t, "output", usage.Output(), 2, true)
	assertTotal(t, "total", usage.Total(), 3, true)
}

func assertCount(t *testing.T, name string, got int64, known bool, want int64, wantKnown bool) {
	t.Helper()
	if got != want || known != wantKnown {
		t.Errorf("%s = (%d, %t), want (%d, %t)", name, got, known, want, wantKnown)
	}
}

func assertInput(t *testing.T, name string, got InputTokens, want int64, wantKnown bool) {
	t.Helper()
	value, known := got.Value()
	assertCount(t, name, value, known, want, wantKnown)
}

func assertOutput(t *testing.T, name string, got OutputTokens, want int64, wantKnown bool) {
	t.Helper()
	value, known := got.Value()
	assertCount(t, name, value, known, want, wantKnown)
}

func assertTotal(t *testing.T, name string, got TotalTokens, want int64, wantKnown bool) {
	t.Helper()
	value, known := got.Value()
	assertCount(t, name, value, known, want, wantKnown)
}

func assertCached(t *testing.T, name string, got CachedInputTokens, want int64, wantKnown bool) {
	t.Helper()
	value, known := got.Value()
	assertCount(t, name, value, known, want, wantKnown)
}

func assertReasoning(t *testing.T, name string, got ReasoningOutputTokens, want int64, wantKnown bool) {
	t.Helper()
	value, known := got.Value()
	assertCount(t, name, value, known, want, wantKnown)
}
