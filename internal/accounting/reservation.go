package accounting

import (
	"errors"
	"fmt"
)

// ReservationMode selects the source of preflight input information. It is a
// string to keep this pure accounting API independent of deployment config.
type ReservationMode string

const (
	ReservationModeUsageOnly ReservationMode = "usage_only"
	ReservationModeEstimate  ReservationMode = "estimate"
	// Descriptive aliases matching the configuration vocabulary.
	UsageOnlyMode ReservationMode = ReservationModeUsageOnly
	EstimateMode  ReservationMode = ReservationModeEstimate
)

var (
	ErrInvalidReservationConfig = errors.New("accounting: invalid reservation configuration")
	ErrReservationUnavailable   = errors.New("accounting: reservation plan unavailable")
	ErrReservationOverflow      = errors.New("accounting: reservation total overflow")
)

// ReservationMetadata is the copied, presence-aware token-limit subset of an
// OpenAI request. A nil pointer means absent; a pointer to zero is explicit.
// Protocol parsers own JSON type validation and must not use this type to
// silently reinterpret malformed metadata.
type ReservationMetadata struct {
	MaxTokens           *int64
	MaxCompletionTokens *int64
	MaxOutputTokens     *int64
}

// Bifrost provenance review: commit 03ab391865710462302bbcf52dca2f32682b91b5
// (branch dev), core/schemas/chatcompletions.go:196-203 and
// core/schemas/responses.go:218-231 confirm the independent metadata names and
// pointer presence semantics. plugins/governance/store.go:360-424 was reviewed
// for reservation-adjacent budget lifecycle handling. The reference is Apache
// 2.0 under LICENSE, with attribution notes in THIRD_PARTY_NOTICES.md. No
// Bifrost code is copied or adapted and no dependency is added: this gateway
// needs a pure, bounded planner rather than stateful governance storage.

// ReservationOptions supplies validated deployment fallbacks and the endpoint
// flavor needed for output-limit precedence. The planner never reads request
// bytes or mutates any option or metadata value.
type ReservationOptions struct {
	Mode                    ReservationMode
	UnknownInputFallback    int64
	FallbackMaxOutputTokens int64
	ResponsesEndpoint       bool
	Metadata                ReservationMetadata
	Input                   InputTokens
	Quality                 EstimateQuality
}

// ReservationPlan is the immutable preflight reservation decision. Input and
// output retain their component values, while Total is the enforceable amount.
// InputQuality is useful for trace data and is not inferred from a fallback.
type ReservationPlan struct {
	Input        InputTokens
	Output       OutputTokens
	Total        TotalTokens
	InputQuality EstimateQuality
	InputSource  ReservationInputSource
	OutputSource ReservationOutputSource
}

type ReservationInputSource uint8

const (
	InputSourceEstimate ReservationInputSource = iota
	InputSourceUnknownFallback
)

type ReservationOutputSource uint8

const (
	OutputSourceExplicit ReservationOutputSource = iota
	OutputSourceFallback
)

// PlanReservation builds one safe reservation without inspecting payloads.
// In usage_only mode the supplied estimate is deliberately ignored, because
// that mode must reserve its configured unknown-input fallback.
func PlanReservation(options ReservationOptions) (ReservationPlan, error) {
	input, source, err := reservationInput(options)
	if err != nil {
		return ReservationPlan{}, err
	}
	output, outputSource, err := reservationOutput(options)
	if err != nil {
		return ReservationPlan{}, err
	}
	total, err := input.Add(inputTokensFromOutput(output))
	if err != nil {
		return ReservationPlan{}, fmt.Errorf("%w: %w", ErrReservationOverflow, err)
	}
	if !total.Known() || total.Int64() <= 0 {
		return ReservationPlan{}, ErrReservationUnavailable
	}
	return ReservationPlan{
		Input:        input,
		Output:       output,
		Total:        totalTokensFromInput(total),
		InputQuality: options.Quality,
		InputSource:  source,
		OutputSource: outputSource,
	}, nil
}

// BuildReservationPlan is a descriptive alias for PlanReservation.
func BuildReservationPlan(options ReservationOptions) (ReservationPlan, error) {
	return PlanReservation(options)
}

func reservationInput(options ReservationOptions) (InputTokens, ReservationInputSource, error) {
	if options.UnknownInputFallback <= 0 {
		return InputTokens{}, 0, fmt.Errorf("%w: unknown input fallback must be positive", ErrInvalidReservationConfig)
	}
	if options.Mode != ReservationModeEstimate && options.Mode != ReservationModeUsageOnly {
		return InputTokens{}, 0, fmt.Errorf("%w: unsupported mode %q", ErrInvalidReservationConfig, options.Mode)
	}
	if options.Mode == ReservationModeEstimate && options.Quality.Known() && options.Input.Known() {
		if options.Input.Int64() < 0 {
			return InputTokens{}, 0, ErrReservationUnavailable
		}
		return options.Input, InputSourceEstimate, nil
	}
	fallback, err := NewInputTokens(options.UnknownInputFallback)
	if err != nil {
		return InputTokens{}, 0, fmt.Errorf("%w: unknown input fallback: %v", ErrInvalidReservationConfig, err)
	}
	return fallback, InputSourceUnknownFallback, nil
}

func reservationOutput(options ReservationOptions) (OutputTokens, ReservationOutputSource, error) {
	if options.FallbackMaxOutputTokens <= 0 {
		return OutputTokens{}, 0, fmt.Errorf("%w: output fallback must be positive", ErrInvalidReservationConfig)
	}
	values := make([]*int64, 0, 3)
	applicable := []*int64{options.Metadata.MaxCompletionTokens, options.Metadata.MaxTokens}
	if options.ResponsesEndpoint {
		applicable = append(applicable, options.Metadata.MaxOutputTokens)
	}
	for _, value := range applicable {
		if value != nil && *value < 0 {
			return OutputTokens{}, 0, fmt.Errorf("%w: negative output limit", ErrReservationUnavailable)
		}
	}
	if options.ResponsesEndpoint {
		values = append(values, options.Metadata.MaxOutputTokens)
	}
	values = append(values, options.Metadata.MaxCompletionTokens, options.Metadata.MaxTokens)
	for _, value := range values {
		if value == nil {
			continue
		}
		// Explicit zero is present but unusable. Continue to a lower-precedence
		// limit, or use the configured fallback when none is positive.
		if *value == 0 {
			continue
		}
		output, err := NewOutputTokens(*value)
		if err != nil {
			return OutputTokens{}, 0, fmt.Errorf("%w: output limit: %w", ErrReservationUnavailable, err)
		}
		return output, OutputSourceExplicit, nil
	}
	fallback, err := NewOutputTokens(options.FallbackMaxOutputTokens)
	if err != nil {
		return OutputTokens{}, 0, fmt.Errorf("%w: output fallback: %w", ErrInvalidReservationConfig, err)
	}
	return fallback, OutputSourceFallback, nil
}

// These conversions keep the public component types distinct while reusing
// checked TokenCount arithmetic. Their values are only produced from known
// non-negative constructors above.
func inputTokensFromOutput(output OutputTokens) InputTokens {
	value, _ := NewInputTokens(output.Int64())
	return value
}

func totalTokensFromInput(input InputTokens) TotalTokens {
	value, _ := NewTotalTokens(input.Int64())
	return value
}
