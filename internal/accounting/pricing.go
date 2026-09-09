package accounting

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/pestit/9gateway/internal/modelmatch"
	"gopkg.in/yaml.v3"
)

// Bifrost provenance review: commit 03ab391865710462302bbcf52dca2f32682b91b5
// (branch dev), .references/bifrost/framework/configstore/tables/modelpricing.go
// and framework/modelcatalog/pricing.go were inspected for model/rate
// configuration and matching boundaries. Bifrost is Apache-2.0 under LICENSE;
// THIRD_PARTY_NOTICES.md was checked for the dependency/license chain. No code
// or data is copied or adapted, and Bifrost is not a dependency.

// PricingConfig is the validated, deployment-level pricing table. Its rules
// are compiled during YAML loading and accessors return copies.
type PricingConfig struct {
	table *pricingTable
}

type pricingTable struct {
	rules []PricingRule
}

// PricingRule is one ordered exact or glob model pricing rule. The selector's
// compiled form is private; callers can only obtain rules made by strict YAML
// validation.
type PricingRule struct {
	model      string
	pattern    modelmatch.Pattern
	inputRate  int64
	outputRate int64
	position   int
}

// Rules returns validated rules in declaration order.
func (pricing PricingConfig) Rules() []PricingRule {
	if pricing.table == nil {
		return nil
	}
	return append([]PricingRule(nil), pricing.table.rules...)
}

func (rule PricingRule) Model() string { return rule.model }

func (rule PricingRule) IsExact() bool { return rule.pattern.IsExact() }

// DeclarationPosition is the zero-based position in the validated YAML rule
// sequence. It is stable metadata for future trace records.
func (rule PricingRule) DeclarationPosition() int { return rule.position }

func (rule PricingRule) InputPerMillionMicros() int64 { return rule.inputRate }

func (rule PricingRule) OutputPerMillionMicros() int64 { return rule.outputRate }

// Matches applies the already-compiled selector. It exists for the local
// resolver; it never parses or compiles a selector on the request path.
func (rule PricingRule) Matches(model string) bool { return rule.pattern.Matches(model) }

// UnmarshalYAML performs shape, scalar, rate, glob, and duplicate validation
// before a Config can be returned by Load. A present pricing container and its
// rules field must have their declared mapping/sequence types; omission and an
// empty sequence are the supported empty-table forms.
func (pricing *PricingConfig) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!null" {
		pricing.table = nil
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return pricingError(node, "pricing", "must be a mapping")
	}
	var rulesNode *yaml.Node
	seenFields := make(map[string]struct{}, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		key, value := node.Content[index], node.Content[index+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return pricingError(key, "pricing", "field name must be a string")
		}
		if _, exists := seenFields[key.Value]; exists {
			return pricingError(key, "pricing", "duplicate field")
		}
		seenFields[key.Value] = struct{}{}
		if key.Value != "rules" {
			return pricingError(key, "pricing."+key.Value, "unknown field")
		}
		rulesNode = value
	}
	if rulesNode == nil {
		pricing.table = nil
		return nil
	}
	if rulesNode.Kind == yaml.ScalarNode && rulesNode.Tag == "!!null" {
		pricing.table = nil
		return nil
	}
	if rulesNode.Kind != yaml.SequenceNode {
		return pricingError(rulesNode, "pricing.rules", "must be a sequence")
	}

	rules := make([]PricingRule, 0, len(rulesNode.Content))
	seenExact := make(map[string]struct{}, len(rulesNode.Content))
	seenGlob := make(map[string]struct{}, len(rulesNode.Content))
	for index, ruleNode := range rulesNode.Content {
		rule, err := parsePricingRule(ruleNode, index)
		if err != nil {
			return err
		}
		if rule.pattern.IsExact() {
			if _, exists := seenExact[rule.pattern.Key()]; exists {
				return ruleError(index, "model", "duplicate exact selector")
			}
			seenExact[rule.pattern.Key()] = struct{}{}
		} else {
			if _, exists := seenGlob[rule.pattern.Key()]; exists {
				return ruleError(index, "model", "duplicate glob selector")
			}
			seenGlob[rule.pattern.Key()] = struct{}{}
		}
		rules = append(rules, rule)
	}
	pricing.table = &pricingTable{rules: rules}
	return nil
}

func parsePricingRule(node *yaml.Node, index int) (PricingRule, error) {
	position := fmt.Sprintf("pricing.rules[%d]", index)
	if node.Kind != yaml.MappingNode {
		return PricingRule{}, pricingError(node, position, "must be a mapping")
	}
	fields := make(map[string]*yaml.Node, len(node.Content)/2)
	for fieldIndex := 0; fieldIndex < len(node.Content); fieldIndex += 2 {
		key, value := node.Content[fieldIndex], node.Content[fieldIndex+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return PricingRule{}, pricingError(key, position, "field name must be a string")
		}
		if _, exists := fields[key.Value]; exists {
			return PricingRule{}, pricingError(key, position+"."+key.Value, "duplicate field")
		}
		switch key.Value {
		case "model", "input_per_million_micros", "output_per_million_micros":
			fields[key.Value] = value
		default:
			return PricingRule{}, pricingError(key, position+"."+key.Value, "unknown field")
		}
	}
	for _, field := range []string{"model", "input_per_million_micros", "output_per_million_micros"} {
		if fields[field] == nil {
			return PricingRule{}, ruleError(index, field, "field is required")
		}
	}
	modelNode := fields["model"]
	if modelNode.Kind != yaml.ScalarNode || modelNode.Tag != "!!str" || modelNode.Value == "" || strings.TrimSpace(modelNode.Value) == "" || !utf8.ValidString(modelNode.Value) {
		return PricingRule{}, ruleError(index, "model", "must be a non-empty UTF-8 string")
	}
	pattern, err := modelmatch.Compile(modelNode.Value)
	if err != nil {
		return PricingRule{}, ruleError(index, "model", "invalid model glob")
	}
	input, err := parsePricingRate(fields["input_per_million_micros"], index, "input_per_million_micros")
	if err != nil {
		return PricingRule{}, err
	}
	output, err := parsePricingRate(fields["output_per_million_micros"], index, "output_per_million_micros")
	if err != nil {
		return PricingRule{}, err
	}
	return PricingRule{model: modelNode.Value, pattern: pattern, inputRate: input, outputRate: output, position: index}, nil
}

func parsePricingRate(node *yaml.Node, index int, field string) (int64, error) {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!int" {
		return 0, ruleError(index, field, "must be a non-negative integer")
	}
	if node.Value == "" || strings.IndexFunc(node.Value, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		if strings.HasPrefix(node.Value, "-") {
			return 0, ruleError(index, field, "must be non-negative")
		}
		return 0, ruleError(index, field, "must be a non-negative integer")
	}
	value, err := strconv.ParseUint(node.Value, 10, 63)
	if err != nil || value > uint64(MaxMoneyMicros) {
		return 0, ruleError(index, field, "exceeds the supported money range")
	}
	money, err := NewMoneyMicros(int64(value))
	if err != nil {
		return 0, ruleError(index, field, "is invalid")
	}
	micros, _ := money.Micros()
	return micros, nil
}

func pricingError(node *yaml.Node, field, message string) error {
	return fmt.Errorf("%s at line %d, column %d: %s", field, node.Line, node.Column, message)
}

func ruleError(index int, field, message string) error {
	return fmt.Errorf("pricing.rules[%d].%s: %s", index, field, message)
}

// Validate checks the already-compiled pricing representation. YAML loading
// performs the same checks; this method protects Config values assembled by
// in-process startup code before they are handed to accounting consumers.
func (pricing PricingConfig) Validate() error {
	if pricing.table == nil {
		return nil
	}
	seenExact := make(map[string]struct{}, len(pricing.table.rules))
	seenGlob := make(map[string]struct{}, len(pricing.table.rules))
	for index, rule := range pricing.table.rules {
		if rule.model == "" || !utf8.ValidString(rule.model) || rule.inputRate < 0 || rule.outputRate < 0 {
			return ruleError(index, "rule", "is invalid")
		}
		if rule.pattern.IsExact() {
			if _, exists := seenExact[rule.pattern.Key()]; exists {
				return ruleError(index, "model", "duplicate exact selector")
			}
			seenExact[rule.pattern.Key()] = struct{}{}
		} else {
			if _, exists := seenGlob[rule.pattern.Key()]; exists {
				return ruleError(index, "model", "duplicate glob selector")
			}
			seenGlob[rule.pattern.Key()] = struct{}{}
		}
	}
	return nil
}

// PricingResolution is an immutable result. Known is independent of the
// selected rates, so a matched rule whose rates are both zero is not unknown.
type PricingResolution struct {
	rule  PricingRule
	known bool
}

func UnknownPricingResolution() PricingResolution { return PricingResolution{} }

func (resolution PricingResolution) Known() bool { return resolution.known }

func (resolution PricingResolution) IsKnown() bool { return resolution.Known() }

// Rule returns a copied immutable rule. The returned rule is zero-valued when
// the resolution is unknown; callers should check Known first.
func (resolution PricingResolution) Rule() PricingRule { return resolution.rule }

// PricingResolver holds precompiled exact and ordered glob rules. It has no
// mutable state after construction and can be copied or shared by lookups.
type PricingResolver struct {
	exact map[string]PricingRule
	globs []PricingRule
}

// NewPricingResolver constructs a resolver only from PricingConfig, the
// validated representation produced by the configuration loader. It copies
// the selected table, so later configuration values cannot affect lookups.
func NewPricingResolver(pricing PricingConfig) PricingResolver {
	resolver := PricingResolver{}
	if pricing.table == nil || len(pricing.table.rules) == 0 {
		return resolver
	}
	resolver.exact = make(map[string]PricingRule)
	resolver.globs = make([]PricingRule, 0, len(pricing.table.rules))
	for _, rule := range pricing.table.rules {
		if rule.IsExact() {
			value, _ := rule.pattern.ExactValue()
			resolver.exact[value] = rule
		} else {
			resolver.globs = append(resolver.globs, rule)
		}
	}
	return resolver
}

// Resolve selects an exact rule before scanning globs in declaration order.
// Empty and invalid UTF-8 model values are conservatively unknown.
func (resolver PricingResolver) Resolve(model string) PricingResolution {
	if model == "" || !utf8.ValidString(model) {
		return UnknownPricingResolution()
	}
	if rule, ok := resolver.exact[model]; ok {
		return PricingResolution{rule: rule, known: true}
	}
	for _, rule := range resolver.globs {
		if rule.Matches(model) {
			return PricingResolution{rule: rule, known: true}
		}
	}
	return UnknownPricingResolution()
}
