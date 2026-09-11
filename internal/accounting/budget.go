package accounting

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// Bifrost provenance review: commit 03ab391865710462302bbcf52dca2f32682b91b5
// (branch dev), .references/bifrost/plugins/governance/store.go:360-424 and
// .references/bifrost/framework/modelcatalog/pricing.go were inspected for
// budget lifecycle and model-price lookup boundaries. The reference is
// Apache-2.0 under .references/bifrost/LICENSE; its dependency/license chain
// was checked in .references/bifrost/THIRD_PARTY_NOTICES.md. Bifrost uses
// mutable governance state and float-based pricing, so no code or data was
// copied or adapted and no dependency was added.

var (
	// ErrBudgetModelMissing identifies a budget-governed request without model
	// metadata. The error intentionally does not include the model or body.
	ErrBudgetModelMissing = errors.New("accounting: budget model metadata is missing")
	// ErrBudgetModelInvalid identifies empty, non-UTF-8, or otherwise malformed
	// model metadata, or an unavailable model-bound configuration.
	ErrBudgetModelInvalid = errors.New("accounting: budget model metadata is invalid")
	// ErrBudgetModelOversized identifies model metadata exceeding the bounded
	// request-inspection contract supplied by the caller.
	ErrBudgetModelOversized = errors.New("accounting: budget model metadata is oversized")
	// ErrBudgetPricingUnknown identifies a model for which no configured pricing
	// rule matched. It remains distinct from a known all-zero rule.
	ErrBudgetPricingUnknown = errors.New("accounting: budget pricing is unknown")
	// ErrBudgetReservationInvalid identifies an absent, inconsistent, or
	// otherwise unsafe token reservation plan.
	ErrBudgetReservationInvalid = errors.New("accounting: budget reservation plan is invalid")
	// ErrBudgetCostInvalid identifies cost arithmetic that cannot safely produce
	// a bounded Money value, or a nonzero-priced request whose safe reservation
	// would be zero.
	ErrBudgetCostInvalid = errors.New("accounting: budget reservation cost is invalid")
)

// BudgetReservationOptions is the pure input to PlanBudgetReservation.
// MaxModelBytes is a byte bound, not a rune bound, and must be the same bound
// used by the caller's configured request inspector. The planner does not read
// request bodies or derive a model from them.
//
// Required is deliberately explicit so an unrestricted key can skip model
// validation, resolver lookup, and cost arithmetic altogether.
type BudgetReservationOptions struct {
	Required      bool
	Model         string
	MaxModelBytes int64
	Reservation   ReservationPlan
	Resolver      PricingResolver
}

// BudgetPricingSource is the copy-safe identity of the selected deployment
// pricing rule. It contains no compiled matcher or mutable rule table.
type BudgetPricingSource struct {
	Selector            string
	Exact               bool
	DeclarationPosition int
}

// BudgetReservationPlan is the immutable result used by later budget
// admission. Reservation is known zero for a deliberately matched all-zero
// rule; an unrestricted skip has an unknown Reservation and Skipped is true.
type BudgetReservationPlan struct {
	Reservation Money
	// Pricing is the immutable selected rule retained for post-response
	// reconciliation. It is copied by value and contains no resolver state.
	SelectedPricing PricingResolution
	Pricing         BudgetPricingSource
	InputSource     ReservationInputSource
	OutputSource    ReservationOutputSource
	InputQuality    EstimateQuality
	Skipped         bool
}

// Known reports whether Reservation is a resolved monetary amount. A skipped
// unrestricted request is not a known zero-cost request.
func (plan BudgetReservationPlan) Known() bool { return plan.Reservation.Known() }

// Reserved returns the safe amount to reserve. Callers must check Known before
// treating the returned Money as an amount.
func (plan BudgetReservationPlan) Reserved() Money { return plan.Reservation }

// PlanBudgetReservation resolves model pricing and prices the explicit T086
// input/output reservation components. It performs no state mutation and is
// safe to call concurrently with other lookups.
func PlanBudgetReservation(options BudgetReservationOptions) (BudgetReservationPlan, error) {
	if !options.Required {
		// Do this before touching any other option. In particular, an
		// unrestricted key must not pay for model validation or resolver work.
		return BudgetReservationPlan{Skipped: true}, nil
	}

	if options.Model == "" {
		return BudgetReservationPlan{}, ErrBudgetModelMissing
	}
	if options.MaxModelBytes <= 0 {
		return BudgetReservationPlan{}, ErrBudgetModelInvalid
	}
	if int64(len(options.Model)) > options.MaxModelBytes {
		return BudgetReservationPlan{}, ErrBudgetModelOversized
	}
	if !utf8.ValidString(options.Model) || strings.TrimSpace(options.Model) == "" {
		return BudgetReservationPlan{}, ErrBudgetModelInvalid
	}
	if err := validateBudgetReservation(options.Reservation); err != nil {
		return BudgetReservationPlan{}, err
	}

	pricing := options.Resolver.Resolve(options.Model)
	if !pricing.Known() {
		return BudgetReservationPlan{}, ErrBudgetPricingUnknown
	}
	cost, err := CalculateReservationCost(options.Reservation, pricing)
	if err != nil {
		return BudgetReservationPlan{}, errors.Join(ErrBudgetCostInvalid, err)
	}
	if !cost.Known() {
		return BudgetReservationPlan{}, ErrBudgetCostInvalid
	}
	micros, _ := cost.Micros()
	rule := pricing.Rule()
	if (rule.InputPerMillionMicros() != 0 || rule.OutputPerMillionMicros() != 0) && micros <= 0 {
		// A positive rate on a zero-count component is not evidence that the
		// request is free. Fail closed rather than reserving an unsafe zero.
		return BudgetReservationPlan{}, ErrBudgetCostInvalid
	}
	return BudgetReservationPlan{
		Reservation:     cost,
		SelectedPricing: pricing,
		Pricing: BudgetPricingSource{
			Selector:            rule.Model(),
			Exact:               rule.IsExact(),
			DeclarationPosition: rule.DeclarationPosition(),
		},
		InputSource:  options.Reservation.InputSource,
		OutputSource: options.Reservation.OutputSource,
		InputQuality: options.Reservation.InputQuality,
	}, nil
}

// BuildBudgetReservationPlan is a descriptive alias for
// PlanBudgetReservation.
func BuildBudgetReservationPlan(options BudgetReservationOptions) (BudgetReservationPlan, error) {
	return PlanBudgetReservation(options)
}

func validateBudgetReservation(plan ReservationPlan) error {
	if !plan.Input.Known() || !plan.Output.Known() || !plan.Total.Known() {
		return ErrBudgetReservationInvalid
	}
	if plan.Input.Int64() < 0 || plan.Output.Int64() < 0 || plan.Total.Int64() <= 0 {
		return ErrBudgetReservationInvalid
	}
	if plan.InputSource > InputSourceUnknownFallback || plan.OutputSource > OutputSourceFallback || plan.InputQuality > EstimateQualityKnown {
		return ErrBudgetReservationInvalid
	}
	sum, err := plan.Input.Add(inputTokensFromOutput(plan.Output))
	if err != nil || !sum.Known() || sum.Int64() != plan.Total.Int64() {
		return ErrBudgetReservationInvalid
	}
	return nil
}
