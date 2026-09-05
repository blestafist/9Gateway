// Package accounting contains protocol-independent usage values and arithmetic.
//
// Usage is deliberately independent of transport, protocol, policy, and
// persistence packages. In particular, an absent count is not represented by
// zero: callers must check Known before using Value.
package accounting

import (
	"errors"
	"fmt"
	"math"
)

var (
	// ErrNegativeCount indicates that a supplied token count was negative.
	ErrNegativeCount = errors.New("accounting: token count is negative")
	// ErrCountOverflow indicates that a token count or sum cannot be represented
	// by the package's persistence-safe integer type.
	ErrCountOverflow = errors.New("accounting: token count overflow")
)

// TokenCount is an immutable, persistence-safe optional token count. The
// value is an int64 rather than an unsigned integer because future SQLite
// accounting columns use SQLite's signed INTEGER representation. Since token
// counts are validated on construction, every known value is non-negative.
type TokenCount struct {
	value int64
	known bool
}

// NewTokenCount creates a known token count. Zero is a known value; use
// UnknownTokenCount when the upstream did not supply a count.
func NewTokenCount(value int64) (TokenCount, error) {
	if value < 0 {
		return TokenCount{}, ErrNegativeCount
	}
	return TokenCount{value: value, known: true}, nil
}

// UnknownTokenCount returns an absent token count.
func UnknownTokenCount() TokenCount { return TokenCount{} }

// Known reports whether the count was explicitly supplied or safely derived.
func (count TokenCount) Known() bool { return count.known }

// IsKnown is a descriptive alias for Known.
func (count TokenCount) IsKnown() bool { return count.known }

// Value returns the count and whether it is known. The value is zero when the
// count is unknown, but the second result is the authoritative presence bit.
func (count TokenCount) Value() (int64, bool) { return count.value, count.known }

// Int64 returns the count. It returns zero for an unknown count; callers that
// need to distinguish unknown from an explicit zero must check Known.
func (count TokenCount) Int64() int64 { return count.value }

// Add combines two counts without allowing integer wraparound. Unknown plus
// anything remains unknown: missing usage must not be silently treated as zero.
func (count TokenCount) Add(other TokenCount) (TokenCount, error) {
	if !count.known || !other.known {
		return UnknownTokenCount(), nil
	}
	if count.value > math.MaxInt64-other.value {
		return TokenCount{}, ErrCountOverflow
	}
	return TokenCount{value: count.value + other.value, known: true}, nil
}

// InputTokens is the canonical input-token count.
type InputTokens struct{ count TokenCount }

// NewInputTokens creates a known input-token count.
func NewInputTokens(value int64) (InputTokens, error) {
	count, err := NewTokenCount(value)
	return InputTokens{count: count}, err
}

// UnknownInputTokens returns an absent input-token count.
func UnknownInputTokens() InputTokens { return InputTokens{} }

func (count InputTokens) Known() bool          { return count.count.Known() }
func (count InputTokens) IsKnown() bool        { return count.count.IsKnown() }
func (count InputTokens) Value() (int64, bool) { return count.count.Value() }
func (count InputTokens) Int64() int64         { return count.count.Int64() }
func (count InputTokens) Add(other InputTokens) (InputTokens, error) {
	value, err := count.count.Add(other.count)
	return InputTokens{count: value}, err
}

// OutputTokens is the canonical output-token count.
type OutputTokens struct{ count TokenCount }

// NewOutputTokens creates a known output-token count.
func NewOutputTokens(value int64) (OutputTokens, error) {
	count, err := NewTokenCount(value)
	return OutputTokens{count: count}, err
}

// UnknownOutputTokens returns an absent output-token count.
func UnknownOutputTokens() OutputTokens { return OutputTokens{} }

func (count OutputTokens) Known() bool          { return count.count.Known() }
func (count OutputTokens) IsKnown() bool        { return count.count.IsKnown() }
func (count OutputTokens) Value() (int64, bool) { return count.count.Value() }
func (count OutputTokens) Int64() int64         { return count.count.Int64() }
func (count OutputTokens) Add(other OutputTokens) (OutputTokens, error) {
	value, err := count.count.Add(other.count)
	return OutputTokens{count: value}, err
}

// TotalTokens is the total-token count. Its source is tracked by Usage rather
// than inferred from the count itself.
type TotalTokens struct{ count TokenCount }

// NewTotalTokens creates a known total-token count.
func NewTotalTokens(value int64) (TotalTokens, error) {
	count, err := NewTokenCount(value)
	return TotalTokens{count: count}, err
}

// UnknownTotalTokens returns an absent total-token count.
func UnknownTotalTokens() TotalTokens { return TotalTokens{} }

func (count TotalTokens) Known() bool          { return count.count.Known() }
func (count TotalTokens) IsKnown() bool        { return count.count.IsKnown() }
func (count TotalTokens) Value() (int64, bool) { return count.count.Value() }
func (count TotalTokens) Int64() int64         { return count.count.Int64() }

// CachedInputTokens is informational cached-input usage. It is not included a
// second time in the input/output total used by limits.
type CachedInputTokens struct{ count TokenCount }

// NewCachedInputTokens creates a known cached-input count.
func NewCachedInputTokens(value int64) (CachedInputTokens, error) {
	count, err := NewTokenCount(value)
	return CachedInputTokens{count: count}, err
}

// UnknownCachedInputTokens returns an absent cached-input count.
func UnknownCachedInputTokens() CachedInputTokens { return CachedInputTokens{} }

func (count CachedInputTokens) Known() bool          { return count.count.Known() }
func (count CachedInputTokens) IsKnown() bool        { return count.count.IsKnown() }
func (count CachedInputTokens) Value() (int64, bool) { return count.count.Value() }
func (count CachedInputTokens) Int64() int64         { return count.count.Int64() }

// ReasoningOutputTokens is informational reasoning-output usage. It is a
// subset of output usage and is not included a second time in totals used by
// limits.
type ReasoningOutputTokens struct{ count TokenCount }

// NewReasoningOutputTokens creates a known reasoning-output count.
func NewReasoningOutputTokens(value int64) (ReasoningOutputTokens, error) {
	count, err := NewTokenCount(value)
	return ReasoningOutputTokens{count: count}, err
}

// UnknownReasoningOutputTokens returns an absent reasoning-output count.
func UnknownReasoningOutputTokens() ReasoningOutputTokens { return ReasoningOutputTokens{} }

func (count ReasoningOutputTokens) Known() bool          { return count.count.Known() }
func (count ReasoningOutputTokens) IsKnown() bool        { return count.count.IsKnown() }
func (count ReasoningOutputTokens) Value() (int64, bool) { return count.count.Value() }
func (count ReasoningOutputTokens) Int64() int64         { return count.count.Int64() }

// TotalSource says how Usage obtained its total count.
type TotalSource uint8

const (
	// TotalAbsent means no total was supplied and input/output were not both
	// known, so deriving one would invent missing usage.
	TotalAbsent TotalSource = iota
	// TotalObserved means the upstream explicitly supplied total usage.
	TotalObserved
	// TotalDerived means total was checked and derived from known input/output.
	TotalDerived
)

// UsageInput is the protocol-neutral input to NewUsage. A nil pointer means
// that a field was absent. A non-nil pointer to zero is therefore distinct
// from an absent field. NewUsage copies all pointed-to values immediately.
type UsageInput struct {
	Input           *int64
	Output          *int64
	Total           *int64
	CachedInput     *int64
	ReasoningOutput *int64
}

// TokenUsageInput is a descriptive alias for UsageInput.
type TokenUsageInput = UsageInput

// Usage is an immutable snapshot of known and unknown token usage. Cached and
// reasoning counts are informational subsets; Input and Output are the only
// components of derived total usage.
type Usage struct {
	input           InputTokens
	output          OutputTokens
	total           TotalTokens
	cachedInput     CachedInputTokens
	reasoningOutput ReasoningOutputTokens
	totalSource     TotalSource
}

// TokenUsage is the canonical descriptive name for Usage.
type TokenUsage = Usage

// NewUsage validates supplied counts and derives total only when both input
// and output are known. An explicitly supplied total is always observed; it is
// not reconciled against input and output because upstream values are truth and
// protocol-specific inconsistencies must remain observable to later policy.
func NewUsage(input UsageInput) (Usage, error) {
	inputTokens, err := makeInputTokens(input.Input)
	if err != nil {
		return Usage{}, err
	}
	outputTokens, err := makeOutputTokens(input.Output)
	if err != nil {
		return Usage{}, err
	}
	cachedInput, err := makeCachedInputTokens(input.CachedInput)
	if err != nil {
		return Usage{}, err
	}
	reasoningOutput, err := makeReasoningOutputTokens(input.ReasoningOutput)
	if err != nil {
		return Usage{}, err
	}

	usage := Usage{
		input:           inputTokens,
		output:          outputTokens,
		cachedInput:     cachedInput,
		reasoningOutput: reasoningOutput,
	}
	if input.Total != nil {
		total, err := NewTotalTokens(*input.Total)
		if err != nil {
			return Usage{}, err
		}
		usage.total, usage.totalSource = total, TotalObserved
		return usage, nil
	}
	if inputTokens.Known() && outputTokens.Known() {
		combined, err := inputTokens.count.Add(outputTokens.count)
		if err != nil {
			return Usage{}, fmt.Errorf("derive total: %w", err)
		}
		usage.total = TotalTokens{count: combined}
		usage.totalSource = TotalDerived
	}
	return usage, nil
}

// NewTokenUsage is an alias for NewUsage.
func NewTokenUsage(input UsageInput) (Usage, error) { return NewUsage(input) }

func (usage Usage) Input() InputTokens                     { return usage.input }
func (usage Usage) Output() OutputTokens                   { return usage.output }
func (usage Usage) Total() TotalTokens                     { return usage.total }
func (usage Usage) CachedInput() CachedInputTokens         { return usage.cachedInput }
func (usage Usage) ReasoningOutput() ReasoningOutputTokens { return usage.reasoningOutput }

// InputTokens returns the canonical input-token count.
func (usage Usage) InputTokens() InputTokens { return usage.input }

// OutputTokens returns the canonical output-token count.
func (usage Usage) OutputTokens() OutputTokens { return usage.output }

// TotalTokens returns the observed or derived total-token count.
func (usage Usage) TotalTokens() TotalTokens { return usage.total }

// CachedInputTokens returns informational cached-input usage.
func (usage Usage) CachedInputTokens() CachedInputTokens { return usage.cachedInput }

// ReasoningOutputTokens returns informational reasoning-output usage.
func (usage Usage) ReasoningOutputTokens() ReasoningOutputTokens { return usage.reasoningOutput }

func (usage Usage) TotalSource() TotalSource { return usage.totalSource }
func (usage Usage) TotalWasObserved() bool   { return usage.totalSource == TotalObserved }
func (usage Usage) TotalWasDerived() bool    { return usage.totalSource == TotalDerived }

func makeInputTokens(value *int64) (InputTokens, error) {
	if value == nil {
		return UnknownInputTokens(), nil
	}
	return NewInputTokens(*value)
}

func makeOutputTokens(value *int64) (OutputTokens, error) {
	if value == nil {
		return UnknownOutputTokens(), nil
	}
	return NewOutputTokens(*value)
}

func makeCachedInputTokens(value *int64) (CachedInputTokens, error) {
	if value == nil {
		return UnknownCachedInputTokens(), nil
	}
	return NewCachedInputTokens(*value)
}

func makeReasoningOutputTokens(value *int64) (ReasoningOutputTokens, error) {
	if value == nil {
		return UnknownReasoningOutputTokens(), nil
	}
	return NewReasoningOutputTokens(*value)
}
