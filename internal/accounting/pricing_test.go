package accounting

import (
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

func pricingForTest(t *testing.T, rules string) PricingConfig {
	t.Helper()
	var pricing PricingConfig
	if err := yaml.Unmarshal([]byte("rules:\n"+rules), &pricing); err != nil {
		t.Fatal(err)
	}
	return pricing
}

func TestPricingResolverExactWinsAndGlobsKeepDeclarationOrder(t *testing.T) {
	pricing := pricingForTest(t, ""+
		"  - model: 'provider/*'\n"+
		"    input_per_million_micros: 10\n"+
		"    output_per_million_micros: 11\n"+
		"  - model: 'provider/gpt-*'\n"+
		"    input_per_million_micros: 20\n"+
		"    output_per_million_micros: 21\n"+
		"  - model: 'provider/gpt-4o'\n"+
		"    input_per_million_micros: 30\n"+
		"    output_per_million_micros: 31\n")
	resolver := NewPricingResolver(pricing)

	glob := resolver.Resolve("provider/gpt-4o-mini")
	assertPricingRule(t, glob, "provider/*", 10, 11, 0)
	exact := resolver.Resolve("provider/gpt-4o")
	assertPricingRule(t, exact, "provider/gpt-4o", 30, 31, 2)
}

func TestPricingResolverMatchesCompleteSlashAwareValues(t *testing.T) {
	pricing := pricingForTest(t, ""+
		"  - model: 'team/*/gpt-[4-5]?'\n"+
		"    input_per_million_micros: 1\n"+
		"    output_per_million_micros: 2\n"+
		"  - model: 'team/**'\n"+
		"    input_per_million_micros: 3\n"+
		"    output_per_million_micros: 4\n"+
		"  - model: 'unicode/模型'\n"+
		"    input_per_million_micros: 5\n"+
		"    output_per_million_micros: 6\n"+
		"  - model: 'literal\\*'\n"+
		"    input_per_million_micros: 7\n"+
		"    output_per_million_micros: 8\n")
	resolver := NewPricingResolver(pricing)

	assertPricingRule(t, resolver.Resolve("team/one/gpt-4o"), "team/*/gpt-[4-5]?", 1, 2, 0)
	// A star never crosses a slash, while the existing matcher treats a pair
	// of stars as two ordinary slash-aware stars rather than recursive syntax.
	if got := resolver.Resolve("team/one/two"); got.Known() {
		t.Fatalf("multi-segment model unexpectedly matched %q", got.Rule().Model())
	}
	assertPricingRule(t, resolver.Resolve("unicode/模型"), "unicode/模型", 5, 6, 2)
	assertPricingRule(t, resolver.Resolve("literal*"), `literal\*`, 7, 8, 3)
	for _, model := range []string{"team/x/gpt-60", "team/x/y/gpt-40", "Team/one/gpt-4o", "provider/team/gpt-4o"} {
		if got := resolver.Resolve(model); got.Known() {
			t.Fatalf("Resolve(%q) unexpectedly matched %q", model, got.Rule().Model())
		}
	}
}

func TestPricingResolverUnknownAndMatchedZeroAreDistinct(t *testing.T) {
	pricing := pricingForTest(t, "  - model: zero\n    input_per_million_micros: 0\n    output_per_million_micros: 0\n")
	resolver := NewPricingResolver(pricing)
	zero := resolver.Resolve("zero")
	if !zero.Known() || zero.Rule().InputPerMillionMicros() != 0 || zero.Rule().OutputPerMillionMicros() != 0 {
		t.Fatalf("zero rule resolution = %#v", zero)
	}
	if unknown := resolver.Resolve("missing"); unknown.Known() || unknown.Rule().Model() != "" {
		t.Fatalf("unknown resolution = %#v", unknown)
	}
	if empty := NewPricingResolver(PricingConfig{}).Resolve("anything"); empty.Known() {
		t.Fatal("empty validated pricing table resolved a model")
	}
}

func TestPricingResolverPresenceDistinguishesEmptyConstructionFromZeroValue(t *testing.T) {
	if resolver := (PricingResolver{}); resolver.Present() {
		t.Fatal("zero resolver is unexpectedly present")
	}
	if resolver := NewPricingResolver(PricingConfig{}); !resolver.Present() {
		t.Fatal("resolver built from an empty validated table is absent")
	}
}

func TestPricingResolverUsesEscapedExactSelector(t *testing.T) {
	pricing := pricingForTest(t, ""+
		"  - model: '*'\n"+
		"    input_per_million_micros: 1\n"+
		"    output_per_million_micros: 1\n"+
		"  - model: 'literal\\*'\n"+
		"    input_per_million_micros: 2\n"+
		"    output_per_million_micros: 2\n")
	assertPricingRule(t, NewPricingResolver(pricing).Resolve("literal*"), `literal\*`, 2, 2, 1)
}

func TestPricingResolverCopiesSourceRulesAndResults(t *testing.T) {
	pricing := pricingForTest(t, "  - model: model\n    input_per_million_micros: 12\n    output_per_million_micros: 13\n")
	resolver := NewPricingResolver(pricing)
	rules := pricing.Rules()
	rules[0] = PricingRule{}
	first := resolver.Resolve("model")
	if !first.Known() || first.Rule().InputPerMillionMicros() != 12 {
		t.Fatalf("resolver changed after source mutation: %#v", first)
	}
	result := resolver.Resolve("model")
	_ = result.Rule()
	second := resolver.Resolve("model")
	if !second.Known() || second.Rule().InputPerMillionMicros() != 12 {
		t.Fatalf("resolver changed after result mutation: %#v", second)
	}
}

func TestPricingResolverRejectsMalformedLookupInput(t *testing.T) {
	pricing := pricingForTest(t, "  - model: '*'\n    input_per_million_micros: 1\n    output_per_million_micros: 1\n")
	resolver := NewPricingResolver(pricing)
	for _, model := range []string{"", string([]byte{0xff, 0xfe})} {
		if model != "" && utf8.ValidString(model) {
			t.Fatalf("test input unexpectedly valid UTF-8: %q", model)
		}
		if got := resolver.Resolve(model); got.Known() {
			t.Fatalf("Resolve(%q) matched malformed input", model)
		}
	}
}

func TestPricingResolverConcurrentLookups(t *testing.T) {
	pricing := pricingForTest(t, ""+
		"  - model: 'team/*'\n"+
		"    input_per_million_micros: 1\n"+
		"    output_per_million_micros: 2\n"+
		"  - model: exact\n"+
		"    input_per_million_micros: 3\n"+
		"    output_per_million_micros: 4\n")
	resolver := NewPricingResolver(pricing)
	const workers = 32
	const iterations = 1000
	var group sync.WaitGroup
	group.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func(worker int) {
			defer group.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				model := "team/model"
				wantInput := int64(1)
				if (worker+iteration)%7 == 0 {
					model, wantInput = "exact", 3
				}
				got := resolver.Resolve(model)
				if !got.Known() || got.Rule().InputPerMillionMicros() != wantInput {
					t.Errorf("Resolve(%q) = %#v, want input %d", model, got, wantInput)
					return
				}
			}
		}(worker)
	}
	group.Wait()
}

func assertPricingRule(t *testing.T, got PricingResolution, model string, input, output int64, position int) {
	t.Helper()
	if !got.Known() {
		t.Fatalf("resolution is unknown, want %q", model)
	}
	rule := got.Rule()
	if rule.Model() != model || rule.InputPerMillionMicros() != input || rule.OutputPerMillionMicros() != output || rule.DeclarationPosition() != position {
		t.Fatalf("rule = %q (%d, %d, position %d), want %q (%d, %d, position %d)", rule.Model(), rule.InputPerMillionMicros(), rule.OutputPerMillionMicros(), rule.DeclarationPosition(), model, input, output, position)
	}
	if strings.TrimSpace(rule.Model()) == "" {
		t.Fatal("matched rule has empty model")
	}
}
