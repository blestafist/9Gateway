package main

import (
	"strings"
	"testing"
)

func TestCheckProfileDeduplicatesByLocationAndStatementCount(t *testing.T) {
	profile := `mode: count
pkg/a.go:1.1,1.2 2 0
pkg/a.go:1.1,1.2 2 7
pkg/a.go:1.1,1.2 3 0
pkg/b.go:2.1,2.2 1 0
`
	result, err := checkProfile(strings.NewReader(profile))
	if err != nil {
		t.Fatal(err)
	}
	if result.statements != 6 || result.covered != 2 || result.roundedTenths != 333 {
		t.Fatalf("result = %+v, want 2/6 and 33.3%%", result)
	}
}

func TestCheckProfileDuplicateCoveredByAnyPositiveCount(t *testing.T) {
	profile := `mode: set
pkg/a.go:1.1,1.2 2 0
pkg/a.go:1.1,1.2 2 1
pkg/a.go:1.1,1.2 2 0
`
	result, err := checkProfile(strings.NewReader(profile))
	if err != nil {
		t.Fatal(err)
	}
	if result.statements != 2 || result.covered != 2 || result.roundedTenths != 1000 {
		t.Fatalf("result = %+v, want 2/2 and 100.0%%", result)
	}
}

func TestRoundedCoverageThresholdMatchesGoOneDecimalRounding(t *testing.T) {
	tests := []struct {
		name       string
		covered    int64
		statements int64
		want       int64
	}{
		{name: "below half", covered: 7994, statements: 10000, want: 799},
		{name: "half rounds up", covered: 1599, statements: 2000, want: 800},
		{name: "exact threshold", covered: 4, statements: 5, want: 800},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := roundedCoverageTenths(test.covered, test.statements); got != test.want {
				t.Fatalf("roundedCoverageTenths(%d, %d) = %d, want %d", test.covered, test.statements, got, test.want)
			}
		})
	}
	below, _ := checkProfile(strings.NewReader("mode: set\nfile.go:1.1,1.2 7994 1\nfile.go:2.1,2.2 2006 0\n"))
	at, _ := checkProfile(strings.NewReader("mode: set\nfile.go:1.1,1.2 1599 1\nfile.go:2.1,2.2 401 0\n"))
	if below.roundedTenths >= 800 || at.roundedTenths < 800 {
		t.Fatalf("threshold rounding = %d and %d, want below 800 and at least 800", below.roundedTenths, at.roundedTenths)
	}
}

func TestParseThreshold(t *testing.T) {
	for value, want := range map[string]int64{"80": 800, "80.0": 800, "79.9": 799} {
		got, err := parseThreshold(value)
		if err != nil || got != want {
			t.Fatalf("parseThreshold(%q) = %d, %v; want %d", value, got, err, want)
		}
	}
	for _, value := range []string{"79.94", "NaN", "-1.0", "80.00", ""} {
		if _, err := parseThreshold(value); err == nil {
			t.Fatalf("parseThreshold(%q) unexpectedly succeeded", value)
		}
	}
}

func TestCheckProfileRejectsMalformedProfiles(t *testing.T) {
	for _, profile := range []string{
		"",
		"mode: nope\npkg/a.go:1.1,1.2 1 1\n",
		"mode: set\npkg/a.go:1.1,1.2 -1 1\n",
		"mode: set\nnot a profile line\n",
	} {
		if _, err := checkProfile(strings.NewReader(profile)); err == nil {
			t.Fatalf("checkProfile(%q) unexpectedly succeeded", profile)
		}
	}
}
