package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	// ErrInvalidPolicy means that stored policy data cannot be used safely.
	// Policy errors intentionally do not include the stored document, which may
	// contain information that should not be returned to a client.
	ErrInvalidPolicy = errors.New("invalid gateway policy")
)

// RequestWindow is one fixed request-count window. Duration is normalized to
// time.Duration while the policy is loaded, so request handling never parses
// duration strings.
type RequestWindow struct {
	Amount   int
	Duration time.Duration
}

type policyDocument struct {
	AllowedModels  []string                `json:"allowed_models"`
	DeniedModels   []string                `json:"denied_models"`
	RequestWindows []requestWindowDocument `json:"request_windows"`
	MaxConcurrency *int                    `json:"max_concurrent_requests"`
}

type requestWindowDocument struct {
	Amount   int    `json:"amount"`
	Duration string `json:"duration"`
}

// EffectivePolicy is the storage-independent, compiled policy used by the
// gateway. Its state is private; accessors return copies where a slice is
// involved, so callers cannot mutate a published snapshot.
type EffectivePolicy struct {
	allowedModels  []compiledModelPattern
	deniedModels   []compiledModelPattern
	requestWindows []RequestWindow
	maxConcurrency int
}

// ParsePolicy strictly validates and compiles one stored policy document.
// A nil document represents the empty, unrestricted policy. Empty JSON
// objects are unrestricted as well.
func ParsePolicy(data []byte) (EffectivePolicy, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return EffectivePolicy{}, nil
	}
	if data[0] != '{' {
		return EffectivePolicy{}, ErrInvalidPolicy
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return EffectivePolicy{}, ErrInvalidPolicy
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return EffectivePolicy{}, ErrInvalidPolicy
	}
	for _, field := range []string{"allowed_models", "denied_models", "request_windows", "max_concurrent_requests"} {
		if value, present := fields[field]; present && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return EffectivePolicy{}, ErrInvalidPolicy
		}
	}
	var document policyDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return EffectivePolicy{}, ErrInvalidPolicy
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return EffectivePolicy{}, ErrInvalidPolicy
	}
	policy := EffectivePolicy{maxConcurrency: 0}
	seenAllowed := make(map[string]struct{}, len(document.AllowedModels))
	for _, pattern := range document.AllowedModels {
		if strings.TrimSpace(pattern) == "" || !utf8.ValidString(pattern) {
			return EffectivePolicy{}, ErrInvalidPolicy
		}
		if _, exists := seenAllowed[pattern]; exists {
			return EffectivePolicy{}, ErrInvalidPolicy
		}
		seenAllowed[pattern] = struct{}{}
		compiled, err := compileModelPattern(pattern)
		if err != nil {
			return EffectivePolicy{}, ErrInvalidPolicy
		}
		policy.allowedModels = append(policy.allowedModels, compiled)
	}
	seenDenied := make(map[string]struct{}, len(document.DeniedModels))
	for _, pattern := range document.DeniedModels {
		if strings.TrimSpace(pattern) == "" || !utf8.ValidString(pattern) {
			return EffectivePolicy{}, ErrInvalidPolicy
		}
		if _, exists := seenDenied[pattern]; exists {
			return EffectivePolicy{}, ErrInvalidPolicy
		}
		seenDenied[pattern] = struct{}{}
		compiled, err := compileModelPattern(pattern)
		if err != nil {
			return EffectivePolicy{}, ErrInvalidPolicy
		}
		policy.deniedModels = append(policy.deniedModels, compiled)
	}

	seenWindows := make(map[RequestWindow]struct{}, len(document.RequestWindows))
	for _, window := range document.RequestWindows {
		if window.Amount <= 0 || window.Duration == "" {
			return EffectivePolicy{}, ErrInvalidPolicy
		}
		duration, err := time.ParseDuration(window.Duration)
		if err != nil || duration <= 0 {
			return EffectivePolicy{}, ErrInvalidPolicy
		}
		normalized := RequestWindow{Amount: window.Amount, Duration: duration}
		if _, exists := seenWindows[normalized]; exists {
			return EffectivePolicy{}, ErrInvalidPolicy
		}
		seenWindows[normalized] = struct{}{}
		policy.requestWindows = append(policy.requestWindows, normalized)
	}
	if document.MaxConcurrency != nil {
		// Zero is the documented unlimited value. Negative values are invalid.
		if *document.MaxConcurrency < 0 {
			return EffectivePolicy{}, ErrInvalidPolicy
		}
		policy.maxConcurrency = *document.MaxConcurrency
	}
	return policy, nil
}

// ParsePolicyJSON is the explicit spelling used by storage-loading callers.
func ParsePolicyJSON(data []byte) (EffectivePolicy, error) {
	return ParsePolicy(data)
}

// CompilePolicy is a descriptive alias for ParsePolicy.
func CompilePolicy(data []byte) (EffectivePolicy, error) {
	return ParsePolicy(data)
}

// AllowedModels returns the original allow patterns in document order.
func (policy EffectivePolicy) AllowedModels() []string {
	return modelPatternStrings(policy.allowedModels)
}

// DeniedModels returns the original deny patterns in document order.
func (policy EffectivePolicy) DeniedModels() []string {
	return modelPatternStrings(policy.deniedModels)
}

// RequestWindows returns a copy of all normalized request windows.
func (policy EffectivePolicy) RequestWindows() []RequestWindow {
	return append([]RequestWindow(nil), policy.requestWindows...)
}

// RequestLimits is a compatibility spelling for RequestWindows.
func (policy EffectivePolicy) RequestLimits() []RequestWindow {
	return policy.RequestWindows()
}

// MaxConcurrency returns zero when concurrency is unrestricted.
func (policy EffectivePolicy) MaxConcurrency() int {
	return policy.maxConcurrency
}

// AllowsModel applies deny precedence and then the optional allow list.
func (policy EffectivePolicy) AllowsModel(model string) bool {
	for _, pattern := range policy.deniedModels {
		if pattern.matches(model) {
			return false
		}
	}
	if len(policy.allowedModels) == 0 {
		return true
	}
	for _, pattern := range policy.allowedModels {
		if pattern.matches(model) {
			return true
		}
	}
	return false
}

func modelPatternStrings(patterns []compiledModelPattern) []string {
	result := make([]string, len(patterns))
	for index, pattern := range patterns {
		result[index] = pattern.source
	}
	return result
}

type globTokenKind uint8

const (
	globLiteral globTokenKind = iota
	globStar
	globQuestion
	globClass
)

type globClassRange struct {
	lo rune
	hi rune
}

type globToken struct {
	kind    globTokenKind
	literal rune
	negated bool
	ranges  []globClassRange
}

type compiledModelPattern struct {
	source string
	tokens []globToken
}

func compileModelPattern(source string) (compiledModelPattern, error) {
	runes := []rune(source)
	tokens := make([]globToken, 0, len(runes))
	for index := 0; index < len(runes); index++ {
		switch runes[index] {
		case '\\':
			if index+1 >= len(runes) {
				return compiledModelPattern{}, ErrInvalidPolicy
			}
			index++
			tokens = append(tokens, globToken{kind: globLiteral, literal: runes[index]})
		case '*':
			if len(tokens) == 0 || tokens[len(tokens)-1].kind != globStar {
				tokens = append(tokens, globToken{kind: globStar})
			}
		case '?':
			tokens = append(tokens, globToken{kind: globQuestion})
		case '[':
			class, next, err := parseGlobClass(runes, index)
			if err != nil {
				return compiledModelPattern{}, ErrInvalidPolicy
			}
			tokens = append(tokens, class)
			index = next
		default:
			tokens = append(tokens, globToken{kind: globLiteral, literal: runes[index]})
		}
	}
	return compiledModelPattern{source: source, tokens: tokens}, nil
}

func parseGlobClass(runes []rune, start int) (globToken, int, error) {
	index := start + 1
	token := globToken{kind: globClass}
	if index < len(runes) && (runes[index] == '!' || runes[index] == '^') {
		token.negated = true
		index++
	}
	if index >= len(runes) || runes[index] == ']' {
		return globToken{}, 0, ErrInvalidPolicy
	}
	for index < len(runes) && runes[index] != ']' {
		lo, next, err := globClassRune(runes, index)
		if err != nil {
			return globToken{}, 0, err
		}
		index = next
		hi := lo
		if index < len(runes) && runes[index] == '-' {
			if index+1 >= len(runes) || runes[index+1] == ']' {
				return globToken{}, 0, ErrInvalidPolicy
			}
			hi, index, err = globClassRune(runes, index+1)
			if err != nil || hi < lo {
				return globToken{}, 0, ErrInvalidPolicy
			}
		}
		token.ranges = append(token.ranges, globClassRange{lo: lo, hi: hi})
	}
	if index >= len(runes) || len(token.ranges) == 0 {
		return globToken{}, 0, ErrInvalidPolicy
	}
	return token, index, nil
}

func globClassRune(runes []rune, index int) (rune, int, error) {
	if runes[index] == '\\' {
		if index+1 >= len(runes) {
			return 0, 0, ErrInvalidPolicy
		}
		return runes[index+1], index + 2, nil
	}
	if runes[index] == '[' {
		return 0, 0, ErrInvalidPolicy
	}
	return runes[index], index + 1, nil
}

func (pattern compiledModelPattern) matches(value string) bool {
	valueRunes := []rune(value)
	previous := make([]bool, len(valueRunes)+1)
	previous[0] = true
	for _, token := range pattern.tokens {
		current := make([]bool, len(valueRunes)+1)
		for valueIndex := 0; valueIndex <= len(valueRunes); valueIndex++ {
			if token.kind == globStar {
				current[valueIndex] = previous[valueIndex] || valueIndex > 0 && valueRunes[valueIndex-1] != '/' && current[valueIndex-1]
			} else if valueIndex > 0 && previous[valueIndex-1] && tokenMatches(token, valueRunes[valueIndex-1]) {
				current[valueIndex] = true
			}
		}
		previous = current
	}
	return previous[len(valueRunes)]
}

func tokenMatches(token globToken, value rune) bool {
	switch token.kind {
	case globLiteral:
		return token.literal == value
	case globQuestion:
		return value != '/'
	case globClass:
		if value == '/' {
			return false
		}
		matched := false
		for _, classRange := range token.ranges {
			if value >= classRange.lo && value <= classRange.hi {
				matched = true
				break
			}
		}
		if token.negated {
			return !matched
		}
		return matched
	default:
		return false
	}
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrInvalidPolicy
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch delimiter := token.(type) {
	case json.Delim:
		switch delimiter {
		case '{':
			seen := make(map[string]struct{})
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				keyString, ok := key.(string)
				if !ok {
					return ErrInvalidPolicy
				}
				if _, exists := seen[keyString]; exists {
					return ErrInvalidPolicy
				}
				seen[keyString] = struct{}{}
				if err := scanJSONValue(decoder); err != nil {
					return err
				}
			}
			close, err := decoder.Token()
			if err != nil || close != json.Delim('}') {
				return ErrInvalidPolicy
			}
		case '[':
			for decoder.More() {
				if err := scanJSONValue(decoder); err != nil {
					return err
				}
			}
			close, err := decoder.Token()
			if err != nil || close != json.Delim(']') {
				return ErrInvalidPolicy
			}
		default:
			return ErrInvalidPolicy
		}
	}
	return nil
}
