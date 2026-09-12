package accounting

import (
	"errors"
	"math"
	"strings"
	"testing"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

func budgetResolver(t *testing.T, rules string) PricingResolver {
	t.Helper()
	var config PricingConfig
	if err := yaml.Unmarshal([]byte("rules:\n"+rules), &config); err != nil {
		t.Fatal(err)
	}
	return NewPricingResolver(config)
}

func budgetPlan(t *testing.T, input, output int64) ReservationPlan {
	t.Helper()
	inputTokens, err := NewInputTokens(input)
	if err != nil {
		t.Fatal(err)
	}
	outputTokens, err := NewOutputTokens(output)
	if err != nil {
		t.Fatal(err)
	}
	totalTokens, err := NewTotalTokens(input + output)
	if err != nil {
		t.Fatal(err)
	}
	return ReservationPlan{Input: inputTokens, Output: outputTokens, Total: totalTokens, InputQuality: EstimateQualityKnown, InputSource: InputSourceEstimate, OutputSource: OutputSourceExplicit}
}

func TestPlanBudgetReservationResolutionAndZeroPricing(t *testing.T) {
	resolver := budgetResolver(t, "  - model: '*'\n    input_per_million_micros: 1\n    output_per_million_micros: 1\n  - model: exact\n    input_per_million_micros: 0\n    output_per_million_micros: 0\n")
	plan, err := PlanBudgetReservation(BudgetReservationOptions{Required: true, Model: "exact", MaxModelBytes: 5, Reservation: budgetPlan(t, 4, 8), Resolver: resolver})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Known() || plan.Pricing.Selector != "exact" || !plan.Pricing.Exact || plan.Reservation.String() != "0" {
		t.Fatalf("plan = %#v, want known zero exact rule", plan)
	}

	glob, err := PlanBudgetReservation(BudgetReservationOptions{Required: true, Model: "other", MaxModelBytes: 5, Reservation: budgetPlan(t, 1_000_000, 2_000_000), Resolver: resolver})
	if err != nil {
		t.Fatal(err)
	}
	if glob.Reservation.String() != "0.000003" || glob.Pricing.Selector != "*" {
		t.Fatalf("glob plan = %#v, want 3 micros", glob)
	}
}

func TestPlanBudgetReservationFallbackAndModelBounds(t *testing.T) {
	resolver := budgetResolver(t, "  - model: model\n    input_per_million_micros: 1000000\n    output_per_million_micros: 2000000\n")
	plan := ReservationPlan{}
	input, _ := NewInputTokens(3)
	output, _ := NewOutputTokens(4)
	total, _ := NewTotalTokens(7)
	plan.Input, plan.Output, plan.Total = input, output, total
	plan.InputSource, plan.OutputSource = InputSourceUnknownFallback, OutputSourceFallback
	got, err := PlanBudgetReservation(BudgetReservationOptions{Required: true, Model: "model", MaxModelBytes: int64(len("model")), Reservation: plan, Resolver: resolver})
	if err != nil || got.Reservation.String() != "0.000011" {
		t.Fatalf("fallback plan = %#v, err %v", got, err)
	}

	for _, test := range []struct {
		name  string
		model string
		bound int64
		want  error
	}{
		{name: "missing", want: ErrBudgetModelMissing},
		{name: "empty bound", model: "model", want: ErrBudgetModelInvalid},
		{name: "invalid utf8", model: string([]byte{0xff}), bound: 1, want: ErrBudgetModelInvalid},
		{name: "oversized", model: "model", bound: 4, want: ErrBudgetModelOversized},
		{name: "whitespace", model: " ", bound: 1, want: ErrBudgetModelInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := PlanBudgetReservation(BudgetReservationOptions{Required: true, Model: test.model, MaxModelBytes: test.bound, Reservation: plan, Resolver: resolver})
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if strings.Contains(err.Error(), "body-secret") || strings.Contains(err.Error(), "credential") {
				t.Fatalf("error contains sensitive request text: %v", err)
			}
		})
	}
	// The configured model bound counts Unicode code points rather than UTF-8
	// encoding bytes.
	unicodeResolver := budgetResolver(t, "  - model: '模型'\n    input_per_million_micros: 1\n    output_per_million_micros: 1\n")
	model := "模型"
	if utf8.RuneCountInString(model) != 2 {
		t.Fatalf("test model unexpectedly uses %d code points", utf8.RuneCountInString(model))
	}
	if _, err := PlanBudgetReservation(BudgetReservationOptions{Required: true, Model: model, MaxModelBytes: 2, Reservation: plan, Resolver: unicodeResolver}); err != nil {
		t.Fatalf("unicode exact boundary error = %v", err)
	}
	if _, err := PlanBudgetReservation(BudgetReservationOptions{Required: true, Model: model, MaxModelBytes: 1, Reservation: plan, Resolver: unicodeResolver}); !errors.Is(err, ErrBudgetModelOversized) {
		t.Fatalf("unicode boundary error = %v", err)
	}
}

func TestPlanBudgetReservationFailsClosedAndSkipsUnrestricted(t *testing.T) {
	resolver := budgetResolver(t, "  - model: model\n    input_per_million_micros: 1\n    output_per_million_micros: 1\n")
	base := BudgetReservationOptions{Required: true, Model: "unknown", MaxModelBytes: 100, Reservation: budgetPlan(t, 1, 1), Resolver: resolver}
	if _, err := PlanBudgetReservation(base); !errors.Is(err, ErrBudgetPricingUnknown) {
		t.Fatalf("unknown price error = %v", err)
	}
	if _, err := PlanBudgetReservation(BudgetReservationOptions{Required: true, Model: "model", MaxModelBytes: 100, Reservation: ReservationPlan{}, Resolver: resolver}); !errors.Is(err, ErrBudgetReservationInvalid) {
		t.Fatalf("invalid plan error = %v", err)
	}
	max := budgetPlan(t, math.MaxInt64-1, 1)
	if _, err := PlanBudgetReservation(BudgetReservationOptions{Required: true, Model: "model", MaxModelBytes: 100, Reservation: max, Resolver: budgetResolver(t, "  - model: model\n    input_per_million_micros: 9223372036854775807\n    output_per_million_micros: 1\n")}); !errors.Is(err, ErrBudgetCostInvalid) {
		t.Fatalf("overflow error = %v", err)
	}
	zeroInput := budgetPlan(t, 0, 1)
	if _, err := PlanBudgetReservation(BudgetReservationOptions{Required: true, Model: "model", MaxModelBytes: 100, Reservation: zeroInput, Resolver: budgetResolver(t, "  - model: model\n    input_per_million_micros: 1\n    output_per_million_micros: 0\n")}); !errors.Is(err, ErrBudgetCostInvalid) {
		t.Fatalf("zero nonzero-priced cost error = %v", err)
	}

	// Required is checked before model bounds, reservation validation, or
	// resolver lookup, proving the no-budget skip seam without HTTP wiring.
	skipped, err := PlanBudgetReservation(BudgetReservationOptions{Model: "body-secret", Resolver: resolver})
	if err != nil || !skipped.Skipped || skipped.Known() {
		t.Fatalf("skip = %#v, err %v", skipped, err)
	}
}

func TestPlanBudgetReservationDoesNotMutateInputsOrRetainRules(t *testing.T) {
	resolver := budgetResolver(t, "  - model: model\n    input_per_million_micros: 1000000\n    output_per_million_micros: 1000000\n")
	input := budgetPlan(t, 2, 3)
	before := input
	got, err := PlanBudgetReservation(BudgetReservationOptions{Required: true, Model: "model", MaxModelBytes: 100, Reservation: input, Resolver: resolver})
	if err != nil {
		t.Fatal(err)
	}
	if input != before || got.Pricing.Selector != "model" {
		t.Fatalf("input or output changed: input=%#v output=%#v", input, got)
	}
}
