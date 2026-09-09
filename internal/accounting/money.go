package accounting

import (
	"errors"
	"math"
	"strconv"
	"strings"
)

// Bifrost provenance review (commit 03ab391865710462302bbcf52dca2f32682b91b5,
// branch dev) inspected .references/bifrost/core/schemas/chatcompletions.go
// (BifrostCost, lines 1956-1972 and its cost addition paths). That reference
// uses float64 and therefore has no equivalent exact-money implementation to
// adapt. Its repository LICENSE is Apache-2.0; THIRD_PARTY_NOTICES.md was also
// reviewed. This implementation is independent, copies no Bifrost source, and
// adds no dependency.

const (
	// MicrosPerUSD is the number of integer micros in one US dollar.
	MicrosPerUSD int64 = 1_000_000
	// MaxMoneyMicros is the largest value representable by Money. It is limited
	// to SQLite's signed INTEGER range used by later accounting persistence.
	MaxMoneyMicros int64 = math.MaxInt64
)

var (
	// ErrNegativeMoney indicates that a known money value was negative.
	ErrNegativeMoney = errors.New("accounting: money is negative")
	// ErrMoneyOverflow indicates that an operation exceeds MaxMoneyMicros.
	ErrMoneyOverflow = errors.New("accounting: money overflow")
	// ErrMoneyUnderflow indicates that subtraction would produce a negative
	// known value.
	ErrMoneyUnderflow = errors.New("accounting: money underflow")
	// ErrInvalidMoney indicates malformed or non-canonical money input.
	ErrInvalidMoney = errors.New("accounting: invalid money")
	// ErrUnknownMoney indicates that a comparison requires a known operand.
	ErrUnknownMoney = errors.New("accounting: money is unknown")
)

// Comparison is the result of comparing two known Money values.
type Comparison int8

const (
	Less    Comparison = -1
	Equal   Comparison = 0
	Greater Comparison = 1
)

// Money is an immutable optional monetary value in integer USD micros. A zero
// value is unknown; use NewMoneyMicros(0) for a known zero. Unknown values are
// deliberately not treated as zero by arithmetic or comparisons.
type Money struct {
	micros int64
	known  bool
}

// NewMoneyMicros creates a known, non-negative Money value. The signed input
// is intentional: it lets callers receive a checked error for negative values
// instead of silently converting them through an unsigned type.
func NewMoneyMicros(micros int64) (Money, error) {
	if micros < 0 {
		return Money{}, ErrNegativeMoney
	}
	return Money{micros: micros, known: true}, nil
}

// UnknownMoney returns the distinct unknown cost state.
func UnknownMoney() Money { return Money{} }

// MaxMoney returns the largest known Money value.
func MaxMoney() Money { return Money{micros: MaxMoneyMicros, known: true} }

// Known reports whether the value is known. In particular, known zero and
// unknown are distinct states.
func (money Money) Known() bool { return money.known }

// IsKnown is a descriptive alias for Known.
func (money Money) IsKnown() bool { return money.Known() }

// Micros returns the integer micros and whether the value is known. The
// presence bit is authoritative; the numeric result is zero when unknown.
func (money Money) Micros() (int64, bool) { return money.micros, money.known }

// Add returns the checked sum. Unknown plus anything remains unknown because
// an absent cost cannot safely be assumed to be zero.
func (money Money) Add(other Money) (Money, error) {
	if !money.known || !other.known {
		return UnknownMoney(), nil
	}
	if money.micros > MaxMoneyMicros-other.micros {
		return Money{}, ErrMoneyOverflow
	}
	return Money{micros: money.micros + other.micros, known: true}, nil
}

// Subtract returns the checked difference. Unknown plus or minus anything
// remains unknown; known subtraction never produces a negative value.
func (money Money) Subtract(other Money) (Money, error) {
	if !money.known || !other.known {
		return UnknownMoney(), nil
	}
	if money.micros < other.micros {
		return Money{}, ErrMoneyUnderflow
	}
	return Money{micros: money.micros - other.micros, known: true}, nil
}

// Compare compares two known values. Unknown operands return ErrUnknownMoney
// rather than being ordered before or after a known value.
func (money Money) Compare(other Money) (Comparison, error) {
	if !money.known || !other.known {
		return 0, ErrUnknownMoney
	}
	if money.micros < other.micros {
		return Less, nil
	}
	if money.micros > other.micros {
		return Greater, nil
	}
	return Equal, nil
}

// Equal compares two known values and reports an error for an unknown operand.
func (money Money) Equal(other Money) (bool, error) {
	comparison, err := money.Compare(other)
	return comparison == Equal, err
}

// Less compares two known values and reports an error for an unknown operand.
func (money Money) Less(other Money) (bool, error) {
	comparison, err := money.Compare(other)
	return comparison == Less, err
}

// LessOrEqual compares two known values and reports an error for an unknown
// operand.
func (money Money) LessOrEqual(other Money) (bool, error) {
	comparison, err := money.Compare(other)
	if err != nil {
		return false, err
	}
	return comparison != Greater, nil
}

// Greater compares two known values and reports an error for an unknown
// operand.
func (money Money) Greater(other Money) (bool, error) {
	comparison, err := money.Compare(other)
	return comparison == Greater, err
}

// GreaterOrEqual compares two known values and reports an error for an
// unknown operand.
func (money Money) GreaterOrEqual(other Money) (bool, error) {
	comparison, err := money.Compare(other)
	if err != nil {
		return false, err
	}
	return comparison != Less, nil
}

// String returns canonical base-10 USD decimal text. Fractional trailing zeroes
// are omitted, except that a fractional part is never rounded. Unknown is
// rendered as "unknown" because it cannot safely be represented as zero.
func (money Money) String() string {
	if !money.known {
		return "unknown"
	}
	whole := money.micros / MicrosPerUSD
	fraction := money.micros % MicrosPerUSD
	if fraction == 0 {
		return strconv.FormatInt(whole, 10)
	}
	text := strconv.FormatInt(whole, 10) + "." + leftPadMicros(strconv.FormatInt(fraction, 10))
	return strings.TrimRight(text, "0")
}

// Decimal is an explicit alias for the canonical known/unknown display used
// by configuration and diagnostics.
func (money Money) Decimal() string { return money.String() }

func leftPadMicros(value string) string {
	if len(value) >= 6 {
		return value
	}
	return strings.Repeat("0", 6-len(value)) + value
}

// ParseMoney parses a non-negative decimal USD value with at most six digits
// after the decimal point. It accepts only ASCII digits and one non-empty
// fractional part when a decimal point is present. Signs, exponent notation,
// separators, whitespace, malformed forms, and values outside the signed
// persistence-safe micros range are rejected. No successful parse rounds.
func ParseMoney(value string) (Money, error) {
	if value == "" {
		return Money{}, ErrInvalidMoney
	}
	if value[0] == '-' {
		return Money{}, ErrNegativeMoney
	}

	dot := strings.IndexByte(value, '.')
	wholeText, fractionText := value, ""
	if dot >= 0 {
		if strings.IndexByte(value[dot+1:], '.') >= 0 || dot == 0 || dot == len(value)-1 {
			return Money{}, ErrInvalidMoney
		}
		wholeText, fractionText = value[:dot], value[dot+1:]
		if len(fractionText) > 6 {
			return Money{}, ErrInvalidMoney
		}
	}
	if !allASCIIDigits(wholeText) || (fractionText != "" && !allASCIIDigits(fractionText)) {
		return Money{}, ErrInvalidMoney
	}

	whole, err := strconv.ParseInt(wholeText, 10, 64)
	if err != nil || whole < 0 {
		return Money{}, ErrMoneyOverflow
	}
	fraction, err := strconv.ParseInt(fractionText+strings.Repeat("0", 6-len(fractionText)), 10, 64)
	if err != nil {
		return Money{}, ErrInvalidMoney
	}
	if whole > MaxMoneyMicros/MicrosPerUSD || (whole == MaxMoneyMicros/MicrosPerUSD && fraction > MaxMoneyMicros%MicrosPerUSD) {
		return Money{}, ErrMoneyOverflow
	}
	return Money{micros: whole*MicrosPerUSD + fraction, known: true}, nil
}

func allASCIIDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
