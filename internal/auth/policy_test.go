package auth

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/accounting"
)

func TestParsePolicy(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr bool
		check   func(t *testing.T, policy EffectivePolicy)
	}{
		{
			name: "empty",
			json: `{}`,
			check: func(t *testing.T, policy EffectivePolicy) {
				if !policy.AllowsModel("anything") || policy.MaxConcurrency() != 0 || len(policy.RequestWindows()) != 0 {
					t.Fatal("empty policy is not unrestricted")
				}
			},
		},
		{
			name: "valid combined",
			json: `{"allowed_models":["gpt-*","exact"],"denied_models":["gpt-bad"],"request_windows":[{"amount":10,"duration":"1m"},{"amount":100,"duration":"1h"}],"token_windows":[{"amount":1000,"duration":"1h"},{"amount":10000,"duration":"24h"}],"budget_limits":[{"amount_micros":1,"period":"total"},{"amount_micros":2,"period":"day"},{"amount_micros":3,"period":"month"}],"token_mode":"usage_only","max_concurrent_requests":3}`,
			check: func(t *testing.T, policy EffectivePolicy) {
				if !policy.AllowsModel("gpt-good") || !policy.AllowsModel("exact") || policy.AllowsModel("gpt-bad") || policy.AllowsModel("other") {
					t.Fatal("combined model policy evaluated incorrectly")
				}
				want := []RequestWindow{{Amount: 10, Duration: time.Minute}, {Amount: 100, Duration: time.Hour}}
				wantTokens := []TokenWindow{{Amount: 1000, Duration: time.Hour}, {Amount: 10000, Duration: 24 * time.Hour}}
				budget, present := policy.TotalBudget()
				day, dayPresent := policy.DailyBudget()
				month, monthPresent := policy.MonthlyBudget()
				dayMicros, dayKnown := day.Micros()
				monthMicros, monthKnown := month.Micros()
				if micros, known := budget.Micros(); !present || !known || micros != 1 || !dayPresent || !dayKnown || dayMicros != 2 || !monthPresent || !monthKnown || monthMicros != 3 || !reflect.DeepEqual(policy.RequestWindows(), want) || !reflect.DeepEqual(policy.TokenWindows(), wantTokens) || policy.TokenMode() != TokenModeUsageOnly || policy.MaxConcurrency() != 3 {
					t.Fatalf("compiled policy = %#v", policy)
				}
				if mode, ok := policy.TokenModeOverride(); !ok || mode != TokenModeUsageOnly {
					t.Fatalf("token mode override = %q/%t", mode, ok)
				}
			},
		},
		{
			name: "inherited token mode",
			json: `{"token_windows":[{"amount":5,"duration":"1m"}]}`,
			check: func(t *testing.T, policy EffectivePolicy) {
				if policy.TokenMode() != TokenModeEstimate {
					t.Fatalf("inherited mode = %q", policy.TokenMode())
				}
				if mode, ok := policy.TokenModeOverride(); ok || mode != TokenModeEstimate {
					t.Fatalf("inherited override = %q/%t", mode, ok)
				}
			},
		},
		{name: "unknown field", json: `{"future":true}`, wantErr: true},
		{name: "malformed pattern", json: `{"allowed_models":["gpt-["]}`, wantErr: true},
		{name: "invalid window", json: `{"request_windows":[{"amount":0,"duration":"1m"}]}`, wantErr: true},
		{name: "invalid duration", json: `{"request_windows":[{"amount":1,"duration":"nope"}]}`, wantErr: true},
		{name: "null token windows", json: `{"token_windows":null}`, wantErr: true},
		{name: "null budget limits", json: `{"budget_limits":null}`, wantErr: true},
		{name: "budget day", json: `{"budget_limits":[{"amount_micros":1,"period":"day"}]}`},
		{name: "budget month", json: `{"budget_limits":[{"amount_micros":1,"period":"month"}]}`},
		{name: "budget unknown period", json: `{"budget_limits":[{"amount_micros":1,"period":"future"}]}`, wantErr: true},
		{name: "budget duplicate period", json: `{"budget_limits":[{"amount_micros":1,"period":"total"},{"amount_micros":2,"period":"total"}]}`, wantErr: true},
		{name: "budget duplicate month", json: `{"budget_limits":[{"amount_micros":1,"period":"month"},{"amount_micros":2,"period":"month"}]}`, wantErr: true},
		{name: "budget duplicate nested field", json: `{"budget_limits":[{"amount_micros":1,"period":"month","period":"day"}]}`, wantErr: true},
		{name: "budget zero", json: `{"budget_limits":[{"amount_micros":0,"period":"total"}]}`, wantErr: true},
		{name: "budget negative", json: `{"budget_limits":[{"amount_micros":-1,"period":"total"}]}`, wantErr: true},
		{name: "budget decimal", json: `{"budget_limits":[{"amount_micros":1.0,"period":"total"}]}`, wantErr: true},
		{name: "budget string", json: `{"budget_limits":[{"amount_micros":"1","period":"total"}]}`, wantErr: true},
		{name: "budget boolean", json: `{"budget_limits":[{"amount_micros":true,"period":"total"}]}`, wantErr: true},
		{name: "budget overflow", json: `{"budget_limits":[{"amount_micros":9223372036854775808,"period":"total"}]}`, wantErr: true},
		{name: "budget maximum", json: `{"budget_limits":[{"amount_micros":9223372036854775807,"period":"total"}]}`},
		{name: "budget exponent", json: `{"budget_limits":[{"amount_micros":1e1,"period":"total"}]}`, wantErr: true},
		{name: "budget missing amount", json: `{"budget_limits":[{"period":"total"}]}`, wantErr: true},
		{name: "budget null amount", json: `{"budget_limits":[{"amount_micros":null,"period":"total"}]}`, wantErr: true},
		{name: "budget missing period", json: `{"budget_limits":[{"amount_micros":1}]}`, wantErr: true},
		{name: "budget null period", json: `{"budget_limits":[{"amount_micros":1,"period":null}]}`, wantErr: true},
		{name: "budget unknown object field", json: `{"budget_limits":[{"amount_micros":1,"period":"total","future":true}]}`, wantErr: true},
		{name: "budget scalar shape", json: `{"budget_limits":1}`, wantErr: true},
		{name: "budget object shape", json: `{"budget_limits":{}}`, wantErr: true},
		{name: "budget list null entry", json: `{"budget_limits":[null]}`, wantErr: true},
		{name: "null token mode", json: `{"token_mode":null}`, wantErr: true},
		{name: "invalid token mode", json: `{"token_mode":"other"}`, wantErr: true},
		{name: "invalid token window amount", json: `{"token_windows":[{"amount":0,"duration":"1m"}]}`, wantErr: true},
		{name: "negative token window amount", json: `{"token_windows":[{"amount":-1,"duration":"1m"}]}`, wantErr: true},
		{name: "invalid token duration", json: `{"token_windows":[{"amount":1,"duration":"nope"}]}`, wantErr: true},
		{name: "fractional token duration", json: `{"token_windows":[{"amount":1,"duration":"500ms"}]}`, wantErr: true},
		{name: "fractional token duration two", json: `{"token_windows":[{"amount":1,"duration":"1500ms"}]}`, wantErr: true},
		{name: "whole second token duration", json: `{"token_windows":[{"amount":1,"duration":"2s"}]}`, wantErr: false},
		{name: "duplicate normalized token window", json: `{"token_windows":[{"amount":1,"duration":"60s"},{"amount":1,"duration":"1m"}]}`, wantErr: true},
		{name: "overflow token amount", json: `{"token_windows":[{"amount":9223372036854775808,"duration":"1m"}]}`, wantErr: true},
		{name: "unknown token window field", json: `{"token_windows":[{"amount":1,"duration":"1m","future":true}]}`, wantErr: true},
		{name: "invalid concurrency", json: `{"max_concurrent_requests":-1}`, wantErr: true},
		{name: "duplicate model rule", json: `{"denied_models":["x","x"]}`, wantErr: true},
		{name: "duplicate JSON field", json: `{"allowed_models":["x"],"allowed_models":["y"]}`, wantErr: true},
		{name: "duplicate normalized window", json: `{"request_windows":[{"amount":1,"duration":"60s"},{"amount":1,"duration":"1m"}]}`, wantErr: true},
		{name: "malformed document", json: `{"allowed_models":[]`, wantErr: true},
		{name: "empty budget unrestricted", json: `{"budget_limits":[]}`, check: func(t *testing.T, policy EffectivePolicy) {
			if _, present := policy.TotalBudget(); present {
				t.Fatal("empty budget list is restricted")
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, err := ParsePolicyJSON([]byte(test.json))
			if test.wantErr {
				if !errors.Is(err, ErrInvalidPolicy) {
					t.Fatalf("ParsePolicyJSON() error = %v, want ErrInvalidPolicy", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if test.check != nil {
				test.check(t, policy)
			}
		})
	}
}

func TestEffectivePolicyAccessorCopiesPreventMutation(t *testing.T) {
	policy, err := ParsePolicy([]byte(`{"allowed_models":["gpt-*"],"request_windows":[{"amount":2,"duration":"1m"}],"token_windows":[{"amount":20,"duration":"1m"}],"budget_limits":[{"amount_micros":42,"period":"total"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	models := policy.AllowedModels()
	models[0] = "*"
	windows := policy.RequestWindows()
	windows[0].Amount = 999
	tokenWindows := policy.TokenWindows()
	tokenWindows[0].Amount = 999
	if policy.AllowsModel("other") || policy.RequestWindows()[0].Amount != 2 || policy.TokenWindows()[0].Amount != 20 {
		t.Fatal("policy accessor exposed mutable state")
	}
	budget, present := policy.TotalBudget()
	if !present || budget == accounting.UnknownMoney() {
		t.Fatal("budget was not compiled as an immutable known value")
	}
}

func TestParsePolicyWithDeploymentTokenModeKeepsInheritanceUnserialized(t *testing.T) {
	policy, err := ParsePolicyWithTokenMode([]byte(`{"token_windows":[]}`), TokenModeUsageOnly)
	if err != nil {
		t.Fatal(err)
	}
	if policy.TokenMode() != TokenModeUsageOnly {
		t.Fatalf("deployment mode = %q", policy.TokenMode())
	}
	if mode, ok := policy.TokenModeOverride(); ok || mode != TokenModeUsageOnly {
		t.Fatalf("deployment mode reported as override = %q/%t", mode, ok)
	}
}

func TestAuthenticatorTokenModeDefaultIsAppliedOnlyToCompiledPolicy(t *testing.T) {
	pepper := []byte("token-mode-default-pepper")
	key := generatedWithMaterial(t, pepper, bytes.Repeat([]byte{9}, GatewayKeyRandomBytes))
	authenticator, err := NewAuthenticator(pepper, nil, TokenModeUsageOnly)
	if err != nil {
		t.Fatal(err)
	}
	if err := authenticator.Load([]Record{{
		ID: "key", DisplayPrefix: key.DisplayPrefix, Digest: key.Digest, Enabled: true,
		PolicyJSON: []byte(`{"token_windows":[{"amount":100,"duration":"1m"}]}`),
	}}); err != nil {
		t.Fatal(err)
	}
	principal, err := authenticator.Authenticate(key.RawKey)
	if err != nil {
		t.Fatal(err)
	}
	if principal.Policy.TokenMode() != TokenModeUsageOnly {
		t.Fatalf("effective token mode = %q", principal.Policy.TokenMode())
	}
	if mode, ok := principal.Policy.TokenModeOverride(); ok || mode != TokenModeUsageOnly {
		t.Fatalf("inherited token mode = %q/%t", mode, ok)
	}
	if string(principal.PolicyJSON) != `{"token_windows":[{"amount":100,"duration":"1m"}]}` {
		t.Fatalf("stored policy was rewritten: %s", principal.PolicyJSON)
	}
}

func TestInvalidPolicyDoesNotReplaceSnapshot(t *testing.T) {
	pepper := []byte("policy-atomic-pepper")
	key := generatedWithMaterial(t, pepper, bytes.Repeat([]byte{8}, GatewayKeyRandomBytes))
	authenticator, err := NewAuthenticator(pepper, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := authenticator.Load([]Record{{ID: "good", DisplayPrefix: key.DisplayPrefix, Digest: key.Digest, Enabled: true, PolicyJSON: []byte(`{"allowed_models":["good"]}`)}}); err != nil {
		t.Fatal(err)
	}
	if err := authenticator.Load([]Record{{ID: "bad", DisplayPrefix: key.DisplayPrefix, Digest: key.Digest, Enabled: true, PolicyJSON: []byte(`{"unknown":true}`)}}); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("invalid replacement error = %v", err)
	}
	principal, err := authenticator.Authenticate(key.RawKey)
	if err != nil || principal.ID != "good" || !principal.Policy.AllowsModel("good") {
		t.Fatalf("published policy changed after invalid replacement: %#v, %v", principal, err)
	}
}
