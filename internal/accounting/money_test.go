package accounting

import (
	"errors"
	"math"
	"testing"
)

func TestMoneyConstructionAndDisplay(t *testing.T) {
	tests := []struct {
		name   string
		value  func() (Money, error)
		known  bool
		micros int64
		text   string
	}{
		{name: "unknown", value: func() (Money, error) { return UnknownMoney(), nil }, text: "unknown"},
		{name: "zero", value: func() (Money, error) { return NewMoneyMicros(0) }, known: true, text: "0"},
		{name: "one micro", value: func() (Money, error) { return NewMoneyMicros(1) }, known: true, micros: 1, text: "0.000001"},
		{name: "fractional dollar", value: func() (Money, error) { return NewMoneyMicros(1_234_567) }, known: true, micros: 1_234_567, text: "1.234567"},
		{name: "whole dollar", value: func() (Money, error) { return NewMoneyMicros(MicrosPerUSD) }, known: true, micros: MicrosPerUSD, text: "1"},
		{name: "maximum", value: func() (Money, error) { return NewMoneyMicros(math.MaxInt64) }, known: true, micros: math.MaxInt64, text: "9223372036854.775807"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			money, err := test.value()
			if err != nil {
				t.Fatal(err)
			}
			micros, known := money.Micros()
			if known != test.known || micros != test.micros || money.String() != test.text || money.Decimal() != test.text {
				t.Fatalf("money = (%d, %t), %q; want (%d, %t), %q", micros, known, money, test.micros, test.known, test.text)
			}
		})
	}
}

func TestMoneyDecimalParsingAndRoundTrip(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{input: "0", want: 0},
		{input: "0.000001", want: 1},
		{input: "1", want: MicrosPerUSD},
		{input: "1.2", want: 1_200_000},
		{input: "1.234567", want: 1_234_567},
		{input: "01.20", want: 1_200_000},
		{input: "9223372036854.775807", want: math.MaxInt64},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			money, err := ParseMoney(test.input)
			if err != nil {
				t.Fatal(err)
			}
			micros, known := money.Micros()
			if !known || micros != test.want {
				t.Fatalf("ParseMoney() = (%d, %t), want (%d, true)", micros, known, test.want)
			}
			formatted, err := ParseMoney(money.String())
			if err != nil {
				t.Fatalf("ParseMoney(String()) error = %v", err)
			}
			if got, _ := formatted.Micros(); got != test.want {
				t.Fatalf("round trip = %d, want %d", got, test.want)
			}
		})
	}

	for _, micros := range []int64{0, 1, 2, 9, 10, 999_999, 1_000_000, math.MaxInt64 - 1, math.MaxInt64} {
		money, err := NewMoneyMicros(micros)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := ParseMoney(money.String())
		if err != nil {
			t.Fatalf("round-trip micros %d: %v", micros, err)
		}
		got, _ := parsed.Micros()
		if got != micros {
			t.Fatalf("round-trip micros %d = %d", micros, got)
		}
	}
}

func TestMoneyRejectsInvalidInput(t *testing.T) {
	for _, input := range []string{"-1", "+1", "1e2", "1E2", "1,000", "1_000", "", ".1", "1.", "1.2345678", "1.2.3", " 1", "1 ", "NaN", "unknown"} {
		t.Run(input, func(t *testing.T) {
			_, err := ParseMoney(input)
			want := ErrInvalidMoney
			if input == "-1" {
				want = ErrNegativeMoney
			}
			if !errors.Is(err, want) {
				t.Fatalf("ParseMoney(%q) error = %v, want %v", input, err, want)
			}
		})
	}
	if _, err := ParseMoney("9223372036854.775808"); !errors.Is(err, ErrMoneyOverflow) {
		t.Fatalf("overflow parse error = %v", err)
	}
	if _, err := ParseMoney("9223372036855"); !errors.Is(err, ErrMoneyOverflow) {
		t.Fatalf("overflow whole parse error = %v", err)
	}
	if _, err := NewMoneyMicros(-1); !errors.Is(err, ErrNegativeMoney) {
		t.Fatalf("negative constructor error = %v", err)
	}
}

func TestMoneyArithmeticAndComparisons(t *testing.T) {
	zero, _ := NewMoneyMicros(0)
	one, _ := NewMoneyMicros(1)
	two, _ := NewMoneyMicros(2)
	max := MaxMoney()
	unknown := UnknownMoney()

	if got, err := one.Add(one); err != nil || got.String() != "0.000002" {
		t.Fatalf("add = %v, %v", got, err)
	}
	if got, err := two.Subtract(one); err != nil || got != one {
		t.Fatalf("subtract = %v, %v", got, err)
	}
	if _, err := zero.Subtract(one); !errors.Is(err, ErrMoneyUnderflow) {
		t.Fatalf("underflow error = %v", err)
	}
	if _, err := max.Add(one); !errors.Is(err, ErrMoneyOverflow) {
		t.Fatalf("overflow error = %v", err)
	}
	for _, known := range []Money{zero, one} {
		if got, err := unknown.Add(known); err != nil || got.Known() {
			t.Fatalf("unknown add = %v, %v", got, err)
		}
		if got, err := unknown.Subtract(known); err != nil || got.Known() {
			t.Fatalf("unknown subtract = %v, %v", got, err)
		}
		if _, err := unknown.Compare(known); !errors.Is(err, ErrUnknownMoney) {
			t.Fatalf("unknown compare error = %v", err)
		}
	}
	comparison, err := one.Compare(two)
	if err != nil || comparison != Less {
		t.Fatalf("compare = %v, %v", comparison, err)
	}
	if equal, err := zero.Equal(zero); err != nil || !equal {
		t.Fatalf("equal = %v, %v", equal, err)
	}
	if less, err := one.LessOrEqual(one); err != nil || !less {
		t.Fatalf("less or equal = %v, %v", less, err)
	}
}
