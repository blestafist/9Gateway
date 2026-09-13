package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/pestit/9gateway/internal/accounting"
	"github.com/pestit/9gateway/internal/modelmatch"
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

// TokenMode selects the source of token information used by later accounting
// stages. It is kept in auth rather than config so compiled policies do not
// depend on the deployment configuration package.
type TokenMode string

const (
	TokenModeUsageOnly TokenMode = "usage_only"
	TokenModeEstimate  TokenMode = "estimate"
)

// TokenWindow is one normalized fixed token-count window. Amount is int64 to
// match the checked token-count arithmetic used by accounting and the future
// token limiter.
type TokenWindow struct {
	Amount   int64
	Duration time.Duration
}

// BudgetPeriod identifies the accounting period of a budget limit.
type BudgetPeriod string

const BudgetPeriodTotal BudgetPeriod = "total"
const BudgetPeriodDay BudgetPeriod = "day"
const BudgetPeriodMonth BudgetPeriod = "month"

type BudgetLimit struct {
	Period BudgetPeriod
	Amount accounting.Money
}

// Bifrost provenance review: commit 03ab391865710462302bbcf52dca2f32682b91b5
// (branch dev), framework/configstore/tables/ratelimit.go,
// plugins/governance/ratelimitreset_test.go, and plugins/governance/store.go
// (CheckBudget) were inspected for policy/limit and budget representations.
// Bifrost is Apache-2.0 under LICENSE; THIRD_PARTY_NOTICES.md was checked for
// file-level and dependency obligations. No source is copied or adapted and no
// dependency is added: Bifrost's mutable provider-governance budget tables and
// float-based accounting are a different boundary from this strict immutable
// JSON policy compiler.

type policyDocument struct {
	AllowedModels   []string                `json:"allowed_models"`
	DeniedModels    []string                `json:"denied_models"`
	RequestWindows  []requestWindowDocument `json:"request_windows"`
	TokenWindows    []tokenWindowDocument   `json:"token_windows"`
	BudgetLimits    json.RawMessage         `json:"budget_limits"`
	TokenMode       *TokenMode              `json:"token_mode"`
	MaxConcurrency  *int                    `json:"max_concurrent_requests"`
	LogRequestBody  *bool                   `json:"log_request_body"`
	LogResponseBody *bool                   `json:"log_response_body"`
}

type requestWindowDocument struct {
	Amount   int    `json:"amount"`
	Duration string `json:"duration"`
}

type tokenWindowDocument struct {
	Amount   int64  `json:"amount"`
	Duration string `json:"duration"`
}

// EffectivePolicy is the storage-independent, compiled policy used by the
// gateway. Its state is private; accessors return copies where a slice is
// involved, so callers cannot mutate a published snapshot.
type EffectivePolicy struct {
	allowedModels   []modelmatch.Pattern
	deniedModels    []modelmatch.Pattern
	requestWindows  []RequestWindow
	tokenWindows    []TokenWindow
	tokenMode       TokenMode
	tokenModeSet    bool
	maxConcurrency  int
	totalBudget     accounting.Money
	totalBudgetSet  bool
	dayBudget       accounting.Money
	dayBudgetSet    bool
	monthBudget     accounting.Money
	monthBudgetSet  bool
	logRequestBody  bool
	logResponseBody bool
}

// ParsePolicy strictly validates and compiles one stored policy document.
// A nil document represents the empty, unrestricted policy. Empty JSON
// objects are unrestricted as well.
func ParsePolicy(data []byte) (EffectivePolicy, error) {
	return parsePolicy(data, TokenModeEstimate)
}

// ParsePolicyWithTokenMode compiles a policy using the validated deployment
// default for an omitted token_mode. The original JSON remains untouched, so
// inheritance is never serialized as an invented per-key override.
func ParsePolicyWithTokenMode(data []byte, defaultMode TokenMode) (EffectivePolicy, error) {
	return parsePolicy(data, defaultMode)
}

// ParsePolicyWithDefaultTokenMode is a descriptive alias for
// ParsePolicyWithTokenMode.
func ParsePolicyWithDefaultTokenMode(data []byte, defaultMode TokenMode) (EffectivePolicy, error) {
	return ParsePolicyWithTokenMode(data, defaultMode)
}

func parsePolicy(data []byte, defaultMode TokenMode) (EffectivePolicy, error) {
	if defaultMode != TokenModeUsageOnly && defaultMode != TokenModeEstimate {
		return EffectivePolicy{}, ErrInvalidPolicy
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return EffectivePolicy{tokenMode: defaultMode}, nil
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
	for _, field := range []string{"allowed_models", "denied_models", "request_windows", "token_windows", "budget_limits", "token_mode", "max_concurrent_requests", "log_request_body", "log_response_body"} {
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
	policy := EffectivePolicy{maxConcurrency: 0, tokenMode: defaultMode}
	seenAllowed := make(map[string]struct{}, len(document.AllowedModels))
	for _, pattern := range document.AllowedModels {
		if strings.TrimSpace(pattern) == "" {
			return EffectivePolicy{}, ErrInvalidPolicy
		}
		if _, exists := seenAllowed[pattern]; exists {
			return EffectivePolicy{}, ErrInvalidPolicy
		}
		seenAllowed[pattern] = struct{}{}
		compiled, err := modelmatch.Compile(pattern)
		if err != nil {
			return EffectivePolicy{}, ErrInvalidPolicy
		}
		policy.allowedModels = append(policy.allowedModels, compiled)
	}
	seenDenied := make(map[string]struct{}, len(document.DeniedModels))
	for _, pattern := range document.DeniedModels {
		if strings.TrimSpace(pattern) == "" {
			return EffectivePolicy{}, ErrInvalidPolicy
		}
		if _, exists := seenDenied[pattern]; exists {
			return EffectivePolicy{}, ErrInvalidPolicy
		}
		seenDenied[pattern] = struct{}{}
		compiled, err := modelmatch.Compile(pattern)
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
	seenTokenWindows := make(map[TokenWindow]struct{}, len(document.TokenWindows))
	for _, window := range document.TokenWindows {
		if window.Amount <= 0 || window.Duration == "" {
			return EffectivePolicy{}, ErrInvalidPolicy
		}
		duration, err := time.ParseDuration(window.Duration)
		if err != nil || duration <= 0 || duration%time.Second != 0 {
			return EffectivePolicy{}, ErrInvalidPolicy
		}
		normalized := TokenWindow{Amount: window.Amount, Duration: duration}
		if _, exists := seenTokenWindows[normalized]; exists {
			return EffectivePolicy{}, ErrInvalidPolicy
		}
		seenTokenWindows[normalized] = struct{}{}
		policy.tokenWindows = append(policy.tokenWindows, normalized)
	}
	if len(document.BudgetLimits) != 0 {
		total, totalPresent, day, dayPresent, month, monthPresent, err := parseBudgetLimits(document.BudgetLimits)
		if err != nil {
			return EffectivePolicy{}, ErrInvalidPolicy
		}
		if totalPresent {
			policy.totalBudget = total
			policy.totalBudgetSet = true
		}
		if dayPresent {
			policy.dayBudget = day
			policy.dayBudgetSet = true
		}
		if monthPresent {
			policy.monthBudget = month
			policy.monthBudgetSet = true
		}
	}
	if document.TokenMode != nil {
		if *document.TokenMode != TokenModeUsageOnly && *document.TokenMode != TokenModeEstimate {
			return EffectivePolicy{}, ErrInvalidPolicy
		}
		policy.tokenMode = *document.TokenMode
		policy.tokenModeSet = true
	}
	if document.MaxConcurrency != nil {
		// Zero is the documented unlimited value. Negative values are invalid.
		if *document.MaxConcurrency < 0 {
			return EffectivePolicy{}, ErrInvalidPolicy
		}
		policy.maxConcurrency = *document.MaxConcurrency
	}
	if document.LogRequestBody != nil {
		policy.logRequestBody = *document.LogRequestBody
	}
	if document.LogResponseBody != nil {
		policy.logResponseBody = *document.LogResponseBody
	}
	return policy, nil
}

// ParsePolicyJSON is the explicit spelling used by storage-loading callers.
func ParsePolicyJSON(data []byte) (EffectivePolicy, error) {
	return ParsePolicy(data)
}

// ParsePolicyJSONWithTokenMode is the storage-loading spelling of
// ParsePolicyWithTokenMode.
func ParsePolicyJSONWithTokenMode(data []byte, defaultMode TokenMode) (EffectivePolicy, error) {
	return ParsePolicyWithTokenMode(data, defaultMode)
}

// ParsePolicyJSONWithDefaultTokenMode is a descriptive alias for
// ParsePolicyJSONWithTokenMode.
func ParsePolicyJSONWithDefaultTokenMode(data []byte, defaultMode TokenMode) (EffectivePolicy, error) {
	return ParsePolicyJSONWithTokenMode(data, defaultMode)
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

// TokenWindows returns a copy of all normalized token windows.
func (policy EffectivePolicy) TokenWindows() []TokenWindow {
	return append([]TokenWindow(nil), policy.tokenWindows...)
}

// TokenMode returns the effective mode, including the deployment default when
// the stored policy omitted token_mode.
func (policy EffectivePolicy) TokenMode() TokenMode {
	return policy.tokenMode
}

// TokenModeOverride reports only a mode explicitly present in stored JSON.
// The second result is false for inherited deployment defaults.
func (policy EffectivePolicy) TokenModeOverride() (TokenMode, bool) {
	return policy.tokenMode, policy.tokenModeSet
}

// MaxConcurrency returns zero when concurrency is unrestricted.
func (policy EffectivePolicy) MaxConcurrency() int {
	return policy.maxConcurrency
}

// LogRequestBody reports whether this key opted in to bounded request-body
// capture. The deployment-wide capture bound remains authoritative; this
// policy value only enables the per-key opt-in.
func (policy EffectivePolicy) LogRequestBody() bool {
	return policy.logRequestBody
}

// LogResponseBody reports whether this key opted in to bounded response-body
// capture. Response capture is limited to bytes delivered downstream.
func (policy EffectivePolicy) LogResponseBody() bool {
	return policy.logResponseBody
}

// TotalBudget returns the configured lifetime budget and whether one was
// explicitly configured. The Money value is immutable, and absence remains
// distinct from a known numeric value.
func (policy EffectivePolicy) TotalBudget() (accounting.Money, bool) {
	return policy.totalBudget, policy.totalBudgetSet
}

// DailyBudget returns the configured UTC calendar-day budget, if present.
func (policy EffectivePolicy) DailyBudget() (accounting.Money, bool) {
	return policy.dayBudget, policy.dayBudgetSet
}

func (policy EffectivePolicy) DayBudget() (accounting.Money, bool) { return policy.DailyBudget() }

// MonthlyBudget returns the configured UTC calendar-month budget, if present.
func (policy EffectivePolicy) MonthlyBudget() (accounting.Money, bool) {
	return policy.monthBudget, policy.monthBudgetSet
}

func (policy EffectivePolicy) MonthBudget() (accounting.Money, bool) { return policy.MonthlyBudget() }

// BudgetLimits returns immutable budget entries in their documented order.
func (policy EffectivePolicy) BudgetLimits() []BudgetLimit {
	limits := make([]BudgetLimit, 0, 3)
	if policy.totalBudgetSet {
		limits = append(limits, BudgetLimit{Period: BudgetPeriodTotal, Amount: policy.totalBudget})
	}
	if policy.dayBudgetSet {
		limits = append(limits, BudgetLimit{Period: BudgetPeriodDay, Amount: policy.dayBudget})
	}
	if policy.monthBudgetSet {
		limits = append(limits, BudgetLimit{Period: BudgetPeriodMonth, Amount: policy.monthBudget})
	}
	return limits
}

// AllowsModel applies deny precedence and then the optional allow list.
func (policy EffectivePolicy) AllowsModel(model string) bool {
	for _, pattern := range policy.deniedModels {
		if pattern.Matches(model) {
			return false
		}
	}
	if len(policy.allowedModels) == 0 {
		return true
	}
	for _, pattern := range policy.allowedModels {
		if pattern.Matches(model) {
			return true
		}
	}
	return false
}

func modelPatternStrings(patterns []modelmatch.Pattern) []string {
	result := make([]string, len(patterns))
	for index, pattern := range patterns {
		result[index] = pattern.Source()
	}
	return result
}

func parseBudgetLimits(raw json.RawMessage) (accounting.Money, bool, accounting.Money, bool, accounting.Money, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || trimmed[0] != '[' {
		return accounting.Money{}, false, accounting.Money{}, false, accounting.Money{}, false, ErrInvalidPolicy
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(trimmed, &entries); err != nil || entries == nil {
		return accounting.Money{}, false, accounting.Money{}, false, accounting.Money{}, false, ErrInvalidPolicy
	}
	var total, day, month accounting.Money
	var totalPresent, dayPresent, monthPresent bool
	seenPeriods := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		object := bytes.TrimSpace(entry)
		if len(object) == 0 || object[0] != '{' {
			return accounting.Money{}, false, accounting.Money{}, false, accounting.Money{}, false, ErrInvalidPolicy
		}
		if err := rejectDuplicateJSONKeys(object); err != nil {
			return accounting.Money{}, false, accounting.Money{}, false, accounting.Money{}, false, ErrInvalidPolicy
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(object, &fields); err != nil {
			return accounting.Money{}, false, accounting.Money{}, false, accounting.Money{}, false, ErrInvalidPolicy
		}
		for field := range fields {
			if field != "amount_micros" && field != "period" {
				return accounting.Money{}, false, accounting.Money{}, false, accounting.Money{}, false, ErrInvalidPolicy
			}
		}
		amountRaw, amountOK := fields["amount_micros"]
		periodRaw, periodOK := fields["period"]
		if !amountOK || !periodOK || bytes.Equal(bytes.TrimSpace(amountRaw), []byte("null")) || bytes.Equal(bytes.TrimSpace(periodRaw), []byte("null")) {
			return accounting.Money{}, false, accounting.Money{}, false, accounting.Money{}, false, ErrInvalidPolicy
		}
		var period string
		if err := json.Unmarshal(periodRaw, &period); err != nil || period == "" || (period != string(BudgetPeriodTotal) && period != string(BudgetPeriodDay) && period != string(BudgetPeriodMonth)) {
			return accounting.Money{}, false, accounting.Money{}, false, accounting.Money{}, false, ErrInvalidPolicy
		}
		if _, exists := seenPeriods[period]; exists {
			return accounting.Money{}, false, accounting.Money{}, false, accounting.Money{}, false, ErrInvalidPolicy
		}
		seenPeriods[period] = struct{}{}
		micros, err := parseBudgetMicros(amountRaw)
		if err != nil {
			return accounting.Money{}, false, accounting.Money{}, false, accounting.Money{}, false, ErrInvalidPolicy
		}
		value, err := accounting.NewMoneyMicros(micros)
		if err != nil || micros == 0 {
			return accounting.Money{}, false, accounting.Money{}, false, accounting.Money{}, false, ErrInvalidPolicy
		}
		if period == string(BudgetPeriodTotal) {
			total, totalPresent = value, true
		} else {
			if period == string(BudgetPeriodDay) {
				day, dayPresent = value, true
			} else {
				month, monthPresent = value, true
			}
		}
	}
	return total, totalPresent, day, dayPresent, month, monthPresent, nil
}

// parseTotalBudget is retained for package-local compatibility with earlier
// tests and callers; T117's compiler uses parseBudgetLimits above.
func parseTotalBudget(raw json.RawMessage) (accounting.Money, bool, error) {
	total, present, _, _, _, _, err := parseBudgetLimits(raw)
	return total, present, err
}

func parseBudgetMicros(raw json.RawMessage) (int64, error) {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || (len(value) > 1 && value[0] == '0') {
		return 0, ErrInvalidPolicy
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, ErrInvalidPolicy
		}
	}
	parsed, err := strconv.ParseInt(string(value), 10, 64)
	if err != nil {
		return 0, ErrInvalidPolicy
	}
	return parsed, nil
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
