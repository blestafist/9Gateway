package accounting

import (
	"errors"
	"math/big"
)

// Bifrost provenance review (commit 03ab391865710462302bbcf52dca2f32682b91b5,
// branch dev) inspected .references/bifrost/framework/modelcatalog/datasheet/cost.go
// (CalculateCost, CalculateCostForUsage, and the compute cost paths at lines
// 13-23, 167-220, and 1100-1200). Bifrost's implementation uses float64 and
// treats missing pricing/usage as zero, so it was not suitable for this exact,
// unknown-aware contract. The repository LICENSE and THIRD_PARTY_NOTICES.md
// were reviewed: Bifrost is Apache-2.0 and its listed dependency/license chain
// does not apply here. No Bifrost code was copied or adapted, and no dependency
// was added.

var (
	// ErrCostInputMultiplicationOverflow indicates that the input component is
	// too large for a Money result even before the output component is added.
	ErrCostInputMultiplicationOverflow = errors.New("accounting: input cost multiplication overflow")
	// ErrCostOutputMultiplicationOverflow indicates that the output component is
	// too large for a Money result even before the input component is added.
	ErrCostOutputMultiplicationOverflow = errors.New("accounting: output cost multiplication overflow")
	// ErrCostSumOverflow indicates that the two exact cost components exceed the
	// largest representable Money value when combined.
	ErrCostSumOverflow = errors.New("accounting: cost sum overflow")
	// ErrCostCeilingOverflow indicates that the final once-only ceiling cannot be
	// represented by Money.
	ErrCostCeilingOverflow = errors.New("accounting: cost ceiling overflow")
	// ErrCostOverflow is the common sentinel for every bounded cost arithmetic
	// failure. More specific errors above can be inspected with errors.Is.
	ErrCostOverflow = errors.New("accounting: cost overflow")
)

// CalculateActualCost calculates usage cost from the canonical input and
// output counts. Total, cached-input, and reasoning-output counts are not used:
// total alone cannot be priced when the two rates differ, while the latter two
// are metadata subsets and must not be charged a second time.
//
// An unresolved pricing result or absent input/output produces UnknownMoney
// with no error. Invalid values and bounded arithmetic failures return an
// error and never a fabricated or truncated Money value.
func CalculateActualCost(usage Usage, pricing PricingResolution) (Money, error) {
	if !pricing.Known() || !usage.Input().Known() || !usage.Output().Known() {
		return UnknownMoney(), nil
	}
	input, _ := usage.Input().Value()
	output, _ := usage.Output().Value()
	rule := pricing.Rule()
	return calculateTokenCost(input, output, rule.InputPerMillionMicros(), rule.OutputPerMillionMicros())
}

// CalculateCost is the concise public name for CalculateActualCost.
func CalculateCost(usage Usage, pricing PricingResolution) (Money, error) {
	return CalculateActualCost(usage, pricing)
}

// CalculateUsageCost is a descriptive alias for CalculateActualCost.
func CalculateUsageCost(usage Usage, pricing PricingResolution) (Money, error) {
	return CalculateActualCost(usage, pricing)
}

// CalculateReservationCost calculates the preflight cost from the explicit
// input and potential-output components in a T086 ReservationPlan. It does not
// derive either component from plan.Total and has no limiter or reservation
// state dependency.
//
// A manually assembled plan with an absent component is treated as unknown;
// valid plans produced by PlanReservation retain both known components.
func CalculateReservationCost(plan ReservationPlan, pricing PricingResolution) (Money, error) {
	if !pricing.Known() || !plan.Input.Known() || !plan.Output.Known() {
		return UnknownMoney(), nil
	}
	input, _ := plan.Input.Value()
	output, _ := plan.Output.Value()
	rule := pricing.Rule()
	return calculateTokenCost(input, output, rule.InputPerMillionMicros(), rule.OutputPerMillionMicros())
}

// ReservationCost is a descriptive alias for CalculateReservationCost.
func ReservationCost(plan ReservationPlan, pricing PricingResolution) (Money, error) {
	return CalculateReservationCost(plan, pricing)
}

// calculateTokenCost uses exact arbitrary-precision integers only for the
// intermediate numerator. This is necessary because a successful calculation
// can have a raw product larger than int64 (for example MaxInt64 tokens at an
// exact-million rate), while its post-division Money value remains bounded.
// Every intermediate is checked against the largest numerator that can round
// up to MaxMoneyMicros, so no unchecked or truncated arithmetic reaches Money.
func calculateTokenCost(input, output, inputRate, outputRate int64) (Money, error) {
	if input < 0 || output < 0 || inputRate < 0 || outputRate < 0 {
		return UnknownMoney(), ErrNegativeCount
	}

	maxNumerator := new(big.Int).Mul(
		big.NewInt(MaxMoneyMicros),
		big.NewInt(MicrosPerUSD),
	)
	inputProduct := new(big.Int).Mul(big.NewInt(input), big.NewInt(inputRate))
	if inputProduct.Cmp(maxNumerator) > 0 {
		return UnknownMoney(), errors.Join(ErrCostOverflow, ErrCostInputMultiplicationOverflow)
	}
	outputProduct := new(big.Int).Mul(big.NewInt(output), big.NewInt(outputRate))
	if outputProduct.Cmp(maxNumerator) > 0 {
		return UnknownMoney(), errors.Join(ErrCostOverflow, ErrCostOutputMultiplicationOverflow)
	}

	numerator := new(big.Int).Add(inputProduct, outputProduct)
	if numerator.Cmp(maxNumerator) > 0 {
		// The exact sum is outside the Money result's numerator range. This is
		// both a checked component sum failure and, when the excess is less than
		// one denominator, a once-only ceiling failure; expose both safe typed
		// causes to callers without returning a wrapped/truncated value.
		return UnknownMoney(), errors.Join(ErrCostOverflow, ErrCostSumOverflow, ErrCostCeilingOverflow)
	}

	denominator := big.NewInt(MicrosPerUSD)
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, denominator, remainder)
	if remainder.Sign() != 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	maxMoney := big.NewInt(MaxMoneyMicros)
	if quotient.Cmp(maxMoney) > 0 || !quotient.IsInt64() {
		return UnknownMoney(), errors.Join(ErrCostOverflow, ErrCostCeilingOverflow)
	}
	return NewMoneyMicros(quotient.Int64())
}
