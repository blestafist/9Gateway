package accounting

import (
	"errors"
	"math"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

func costPricing(t *testing.T, model string, input, output int64) PricingResolution {
	t.Helper()
	var config PricingConfig
	err := yaml.Unmarshal([]byte("rules:\n  - model: "+model+"\n    input_per_million_micros: "+itoa(input)+"\n    output_per_million_micros: "+itoa(output)+"\n"), &config)
	if err != nil {
		t.Fatal(err)
	}
	return NewPricingResolver(config).Resolve(model)
}

func itoa(value int64) string {
	if value < 0 {
		return "-" + itoa(-value)
	}
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}

func knownUsage(t *testing.T, input, output int64, extra ...*int64) Usage {
	t.Helper()
	fields := UsageInput{Input: &input, Output: &output}
	if len(extra) > 0 {
		fields.Total = extra[0]
	}
	usage, err := NewUsage(fields)
	if err != nil {
		t.Fatal(err)
	}
	return usage
}

func assertMoney(t *testing.T, got Money, want int64, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("cost error = %v", err)
	}
	value, known := got.Micros()
	if !known || value != want {
		t.Fatalf("cost = (%d, %t), want (%d, true)", value, known, want)
	}
}

func TestCalculateActualCostExactIntegerCeiling(t *testing.T) {
	tests := []struct {
		name                  string
		input, output         int64
		inputRate, outputRate int64
		want                  int64
	}{
		{name: "exact million", input: 1_000_000, inputRate: 7, want: 7},
		{name: "fractional ceiling", input: 1, inputRate: 1, want: 1},
		// Rounding the two components separately would produce 2, but the
		// combined numerator is exactly one micro-dollar.
		{name: "round once after sum", input: 1, output: 1, inputRate: 500_000, outputRate: 500_000, want: 1},
		{name: "zero tokens", inputRate: 123, outputRate: 456, want: 0},
		{name: "zero rates", input: 99, output: 100, want: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			usage := knownUsage(t, test.input, test.output)
			pricing := costPricing(t, "model", test.inputRate, test.outputRate)
			got, err := CalculateCost(usage, pricing)
			assertMoney(t, got, test.want, err)
		})
	}
}

func TestCalculateActualCostRequiresBothComponentsAndKnownPricing(t *testing.T) {
	input, output, total := int64(4), int64(5), int64(9)
	cases := []Usage{
		func() Usage { usage, _ := NewUsage(UsageInput{Output: &output}); return usage }(),
		func() Usage { usage, _ := NewUsage(UsageInput{Input: &input}); return usage }(),
		func() Usage { usage, _ := NewUsage(UsageInput{Total: &total}); return usage }(),
		func() Usage { usage, _ := NewUsage(UsageInput{}); return usage }(),
	}
	pricing := costPricing(t, "model", 10, 20)
	for index, usage := range cases {
		got, err := CalculateActualCost(usage, pricing)
		if err != nil || got.Known() {
			t.Errorf("case %d = %v, %v, want unknown without error", index, got, err)
		}
	}
	unknown, err := CalculateActualCost(knownUsage(t, 1, 1), UnknownPricingResolution())
	if err != nil || unknown.Known() {
		t.Fatalf("unknown pricing = %v, %v", unknown, err)
	}
}

func TestCalculateActualCostDoesNotDoubleCountMetadataSubsets(t *testing.T) {
	input, output, cached, reasoning, total := int64(10), int64(20), int64(7), int64(8), int64(30)
	usage, err := NewUsage(UsageInput{Input: &input, Output: &output, Total: &total, CachedInput: &cached, ReasoningOutput: &reasoning})
	if err != nil {
		t.Fatal(err)
	}
	got, err := CalculateActualCost(usage, costPricing(t, "model", 1_000_000, 1_000_000))
	assertMoney(t, got, 30, err)
}

func TestCalculateReservationCostUsesComponentsNotTotal(t *testing.T) {
	plan, err := PlanReservation(ReservationOptions{
		Mode: ReservationModeEstimate, Input: mustInput(t, 3), Quality: EstimateQualityKnown,
		UnknownInputFallback: 100, FallbackMaxOutputTokens: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	// The plan's component values are 3 and 4. A deliberately incorrect total
	// cannot affect the calculation because pricing consumes those components.
	plan.Total = mustTotal(t, 999)
	got, err := CalculateReservationCost(plan, costPricing(t, "model", 1_000_000, 2_000_000))
	assertMoney(t, got, 11, err)

	zero := int64(0)
	zeroPlan, err := PlanReservation(ReservationOptions{
		Mode: ReservationModeEstimate, Input: mustInput(t, 3), Quality: EstimateQualityKnown,
		UnknownInputFallback: 100, FallbackMaxOutputTokens: 4,
		Metadata: ReservationMetadata{MaxCompletionTokens: &zero},
	})
	if err != nil || zeroPlan.Output.Int64() != 4 {
		t.Fatalf("zero output fallback plan = %#v, %v", zeroPlan, err)
	}
	unknownPlan := ReservationPlan{Input: mustInput(t, 3), Output: UnknownOutputTokens()}
	unknownCost, err := CalculateReservationCost(unknownPlan, costPricing(t, "model", 1, 1))
	if err != nil || unknownCost.Known() {
		t.Fatalf("absent potential output cost = %v, %v, want unknown", unknownCost, err)
	}
}

func TestCalculateCostMaximumBoundaryAndOverflow(t *testing.T) {
	// The raw product is larger than int64, but the resulting Money is exactly
	// the maximum supported value and must remain representable.
	maxUsage := knownUsage(t, MaxMoneyMicros, 0)
	got, err := CalculateActualCost(maxUsage, costPricing(t, "model", MicrosPerUSD, 0))
	assertMoney(t, got, MaxMoneyMicros, err)
	// A numerator one micro below the next Money value still rounds to the
	// maximum Money value and is part of the safe successful boundary.
	boundaryTotal := int64(0)
	boundaryInput := int64(MaxMoneyMicros - 1)
	boundaryOutput := int64(1)
	boundaryUsage, err := NewUsage(UsageInput{Input: &boundaryInput, Output: &boundaryOutput, Total: &boundaryTotal})
	if err != nil {
		t.Fatal(err)
	}
	got, err = CalculateActualCost(boundaryUsage, costPricing(t, "model", MicrosPerUSD, 999_999))
	assertMoney(t, got, MaxMoneyMicros, err)

	tooMuchInputValue, tooMuchInputOutput, tooMuchInputTotal := int64(MaxMoneyMicros), int64(1), int64(0)
	tooMuchInput, err := NewUsage(UsageInput{Input: &tooMuchInputValue, Output: &tooMuchInputOutput, Total: &tooMuchInputTotal})
	if err != nil {
		t.Fatal(err)
	}
	_, err = CalculateActualCost(tooMuchInput, costPricing(t, "model", MicrosPerUSD+1, 1))
	if !errors.Is(err, ErrCostOverflow) || !errors.Is(err, ErrCostInputMultiplicationOverflow) {
		t.Fatalf("input overflow = %v", err)
	}
	tooMuchOutputValue, tooMuchOutputInput, tooMuchOutputTotal := int64(MaxMoneyMicros), int64(1), int64(0)
	tooMuchOutput, err := NewUsage(UsageInput{Input: &tooMuchOutputInput, Output: &tooMuchOutputValue, Total: &tooMuchOutputTotal})
	if err != nil {
		t.Fatal(err)
	}
	_, err = CalculateActualCost(tooMuchOutput, costPricing(t, "model", 1, MicrosPerUSD+1))
	if !errors.Is(err, ErrCostOverflow) || !errors.Is(err, ErrCostOutputMultiplicationOverflow) {
		t.Fatalf("output overflow = %v", err)
	}
	// Each component is individually bounded, but their sum cannot fit Money.
	sumTotal := int64(0)
	sumInput, sumOutput := int64(MaxMoneyMicros), int64(1)
	sumUsage, err := NewUsage(UsageInput{Input: &sumInput, Output: &sumOutput, Total: &sumTotal})
	if err != nil {
		t.Fatal(err)
	}
	_, err = CalculateActualCost(sumUsage, costPricing(t, "model", MicrosPerUSD, MicrosPerUSD))
	if !errors.Is(err, ErrCostOverflow) || !errors.Is(err, ErrCostSumOverflow) || !errors.Is(err, ErrCostCeilingOverflow) {
		t.Fatalf("sum overflow = %v", err)
	}
	if math.MaxInt64 != MaxMoneyMicros {
		t.Fatal("Money boundary unexpectedly changed")
	}
}

func TestCalculateCostPureConcurrentAndEquivalentUsage(t *testing.T) {
	jsonUsage := knownUsage(t, 12, 34)
	sseUsage := knownUsage(t, 12, 34)
	pricing := costPricing(t, "model", 7, 11)
	want, err := CalculateActualCost(jsonUsage, pricing)
	if err != nil {
		t.Fatal(err)
	}
	other, err := CalculateActualCost(sseUsage, pricing)
	if err != nil {
		t.Fatal(err)
	}
	equal, err := want.Equal(other)
	if err != nil || !equal {
		t.Fatalf("equivalent usage costs differ: %v, %v", want, other)
	}
	const workers = 16
	const iterations = 100
	var group sync.WaitGroup
	group.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer group.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				got, callErr := CalculateActualCost(jsonUsage, pricing)
				if callErr != nil || got.String() != want.String() {
					t.Errorf("concurrent cost = %v, %v", got, callErr)
					return
				}
			}
		}()
	}
	group.Wait()
}

func mustTotal(t *testing.T, value int64) TotalTokens {
	t.Helper()
	total, err := NewTotalTokens(value)
	if err != nil {
		t.Fatal(err)
	}
	return total
}
