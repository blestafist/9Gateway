package config

import (
	"strings"
	"testing"

	"github.com/pestit/9gateway/internal/accounting"
)

func pricingConfigYAML(rules string) string {
	return "listen_addr: :8080\nupstream_base_url: http://router.example.test\nupstream_api_key: secret\nsqlite_path: ':memory:'\nauth_pepper: ${TEST_AUTH_PEPPER}\nadmin_credential: ${TEST_ADMIN_CREDENTIAL}\n" + rules
}

func TestLoadPricingRulesCompilesInDeclarationOrder(t *testing.T) {
	t.Setenv("TEST_AUTH_PEPPER", "pepper")
	t.Setenv("TEST_ADMIN_CREDENTIAL", "admin")
	config, err := Load(writeConfig(t, pricingConfigYAML("pricing:\n  rules:\n    - model: exact\n      input_per_million_micros: 0\n      output_per_million_micros: 12\n    - model: 'team/*'\n      input_per_million_micros: 1000000\n      output_per_million_micros: 2000000\n")))
	if err != nil {
		t.Fatal(err)
	}
	rules := config.Pricing.Rules()
	if len(rules) != 2 || rules[0].Model() != "exact" || !rules[0].IsExact() || rules[1].IsExact() {
		t.Fatalf("compiled rules = %#v", rules)
	}
	input := rules[0].InputPerMillionMicros()
	output := rules[0].OutputPerMillionMicros()
	if input != 0 || output != 12 {
		t.Fatalf("first rates = %d and %d", input, output)
	}
	if !rules[1].Matches("team/model") || rules[1].Matches("team/a/model") {
		t.Fatal("pricing glob does not use slash-aware matching")
	}
}

func TestLoadPricingEmptyAndNullContainers(t *testing.T) {
	t.Setenv("TEST_AUTH_PEPPER", "pepper")
	t.Setenv("TEST_ADMIN_CREDENTIAL", "admin")
	base := pricingConfigYAML("")
	for _, suffix := range []string{"", "pricing: {}\n", "pricing: null\n", "pricing:\n  rules: []\n", "pricing:\n  rules: null\n"} {
		got, err := Load(writeConfig(t, base+suffix))
		if err != nil {
			t.Fatalf("Load(%q) error = %v", suffix, err)
		}
		if len(got.Pricing.Rules()) != 0 {
			t.Fatalf("Load(%q) has non-empty pricing", suffix)
		}
	}
}

func TestPricingDoesNotExpandEnvironmentReferences(t *testing.T) {
	t.Setenv("TEST_AUTH_PEPPER", "pepper")
	t.Setenv("TEST_ADMIN_CREDENTIAL", "admin")
	t.Setenv("PRICING_MODEL", "expanded-model")
	config, err := Load(writeConfig(t, pricingConfigYAML("pricing:\n  rules:\n    - model: '${PRICING_MODEL}'\n      input_per_million_micros: 1\n      output_per_million_micros: 2\n")))
	if err != nil {
		t.Fatal(err)
	}
	if got := config.Pricing.Rules()[0].Model(); got != "${PRICING_MODEL}" {
		t.Fatalf("pricing model = %q, want literal environment syntax", got)
	}
}

func TestLoadPricingRejectsInvalidRules(t *testing.T) {
	t.Setenv("TEST_AUTH_PEPPER", "pepper")
	t.Setenv("TEST_ADMIN_CREDENTIAL", "admin")
	tests := []string{
		"- model: model\n  input_per_million_micros: 1\n",
		"- model: model\n  input_per_million_micros: null\n  output_per_million_micros: 1\n",
		"- model: model\n  input_per_million_micros: -1\n  output_per_million_micros: 1\n",
		"- model: model\n  input_per_million_micros: 1.5\n  output_per_million_micros: 1\n",
		"- model: model\n  input_per_million_micros: '1'\n  output_per_million_micros: 1\n",
		"- model: model\n  input_per_million_micros: true\n  output_per_million_micros: 1\n",
		"- model: model\n  input_per_million_micros: 9223372036854775808\n  output_per_million_micros: 1\n",
		"- model: ''\n  input_per_million_micros: 1\n  output_per_million_micros: 1\n",
		"- model: 'model-[a-'\n  input_per_million_micros: 1\n  output_per_million_micros: 1\n",
		"- model: model\n  input_per_million_micros: 1\n  output_per_million_micros: 1\n  unknown: true\n",
	}
	for index, rule := range tests {
		t.Run(string(rune('a'+index)), func(t *testing.T) {
			_, err := Load(writeConfig(t, pricingConfigYAML("pricing:\n  rules:\n"+indentPricingRule(rule))))
			if err == nil || !strings.Contains(err.Error(), "pricing.rules[0]") {
				t.Fatalf("Load() error = %v, want positioned pricing rule error", err)
			}
		})
	}
}

func TestLoadPricingRejectsDuplicateCompiledSelectors(t *testing.T) {
	t.Setenv("TEST_AUTH_PEPPER", "pepper")
	t.Setenv("TEST_ADMIN_CREDENTIAL", "admin")
	for _, rules := range []string{
		"- model: 'literal\\*'\n  input_per_million_micros: 1\n  output_per_million_micros: 1\n- model: 'literal\\*'\n  input_per_million_micros: 2\n  output_per_million_micros: 2\n",
		"- model: 'model-[ab]'\n  input_per_million_micros: 1\n  output_per_million_micros: 1\n- model: 'model-[a-b]'\n  input_per_million_micros: 2\n  output_per_million_micros: 2\n",
	} {
		_, err := Load(writeConfig(t, pricingConfigYAML("pricing:\n  rules:\n"+indentPricingRule(rules))))
		if err == nil || !strings.Contains(err.Error(), "duplicate") {
			t.Fatalf("Load() error = %v, want duplicate selector error", err)
		}
	}
}

func TestLoadPricingRejectsUnknownPricingFields(t *testing.T) {
	t.Setenv("TEST_AUTH_PEPPER", "pepper")
	t.Setenv("TEST_ADMIN_CREDENTIAL", "admin")
	for _, contents := range []string{
		pricingConfigYAML("pricing:\n  unexpected: true\n"),
		pricingConfigYAML("pricing:\n  rules:\n    - model: model\n      input_per_million_micros: 1\n      output_per_million_micros: 1\n      unexpected: true\n"),
	} {
		if _, err := Load(writeConfig(t, contents)); err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("Load() error = %v, want unknown-field error", err)
		}
	}
}

func TestPricingRatesStayWithinMoneyDomain(t *testing.T) {
	if _, err := accounting.NewMoneyMicros(accounting.MaxMoneyMicros); err != nil {
		t.Fatal(err)
	}
}

func indentPricingRule(value string) string {
	lines := strings.Split(strings.TrimSuffix(value, "\n"), "\n")
	for index, line := range lines {
		lines[index] = "    " + line
	}
	return strings.Join(lines, "\n") + "\n"
}
