package modelmatch

import "testing"

func TestCompileAndMatchesSlashAwarePatterns(t *testing.T) {
	tests := []struct {
		pattern string
		value   string
		match   bool
	}{
		{pattern: "gpt-*", value: "gpt-4o", match: true},
		{pattern: "gpt-*", value: "gpt/team-4o", match: false},
		{pattern: "gpt-?", value: "gpt-4", match: true},
		{pattern: "gpt-?", value: "gpt-/", match: false},
		{pattern: "gpt-[4-5]", value: "gpt-4", match: true},
		{pattern: "gpt-[4-5]", value: "gpt-6", match: false},
		{pattern: `literal\*`, value: "literal*", match: true},
		{pattern: `literal\*`, value: "literalX", match: false},
		{pattern: "[!/]", value: "x", match: true},
		{pattern: "[!/]", value: "/", match: false},
	}
	for _, test := range tests {
		t.Run(test.pattern+"/"+test.value, func(t *testing.T) {
			pattern, err := Compile(test.pattern)
			if err != nil {
				t.Fatal(err)
			}
			if got := pattern.Matches(test.value); got != test.match {
				t.Fatalf("Matches(%q) = %t, want %t", test.value, got, test.match)
			}
		})
	}
}

func TestCompileRejectsMalformedPatterns(t *testing.T) {
	for _, pattern := range []string{"", "gpt-\\", "gpt-[", "gpt-[]", "gpt-[a-]", "gpt-[z-a]"} {
		t.Run(pattern, func(t *testing.T) {
			if _, err := Compile(pattern); err == nil {
				t.Fatalf("Compile(%q) succeeded", pattern)
			}
		})
	}
}

func TestCompileCanonicalizesEquivalentGlobSelectors(t *testing.T) {
	first, err := Compile("gpt-[ab]")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Compile("gpt-[a-b]")
	if err != nil {
		t.Fatal(err)
	}
	if first.Key() != second.Key() {
		t.Fatalf("equivalent glob keys differ: %q != %q", first.Key(), second.Key())
	}
	if first.IsExact() || second.IsExact() {
		t.Fatal("glob classified as exact")
	}
}

func TestExactValueUnescapesLiteralMetacharacters(t *testing.T) {
	pattern, err := Compile(`provider/literal\*`)
	if err != nil {
		t.Fatal(err)
	}
	value, ok := pattern.ExactValue()
	if !ok || value != "provider/literal*" {
		t.Fatalf("ExactValue() = %q, %t", value, ok)
	}
}
