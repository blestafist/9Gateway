package auth

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
	"time"
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
			json: `{"allowed_models":["gpt-*","exact"],"denied_models":["gpt-bad"],"request_windows":[{"amount":10,"duration":"1m"},{"amount":100,"duration":"1h"}],"token_windows":[{"amount":1000,"duration":"1h"},{"amount":10000,"duration":"24h"}],"token_mode":"usage_only","max_concurrent_requests":3}`,
			check: func(t *testing.T, policy EffectivePolicy) {
				if !policy.AllowsModel("gpt-good") || !policy.AllowsModel("exact") || policy.AllowsModel("gpt-bad") || policy.AllowsModel("other") {
					t.Fatal("combined model policy evaluated incorrectly")
				}
				want := []RequestWindow{{Amount: 10, Duration: time.Minute}, {Amount: 100, Duration: time.Hour}}
				wantTokens := []TokenWindow{{Amount: 1000, Duration: time.Hour}, {Amount: 10000, Duration: 24 * time.Hour}}
				if !reflect.DeepEqual(policy.RequestWindows(), want) || !reflect.DeepEqual(policy.TokenWindows(), wantTokens) || policy.TokenMode() != TokenModeUsageOnly || policy.MaxConcurrency() != 3 {
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
		{name: "null token mode", json: `{"token_mode":null}`, wantErr: true},
		{name: "invalid token mode", json: `{"token_mode":"other"}`, wantErr: true},
		{name: "invalid token window amount", json: `{"token_windows":[{"amount":0,"duration":"1m"}]}`, wantErr: true},
		{name: "negative token window amount", json: `{"token_windows":[{"amount":-1,"duration":"1m"}]}`, wantErr: true},
		{name: "invalid token duration", json: `{"token_windows":[{"amount":1,"duration":"nope"}]}`, wantErr: true},
		{name: "duplicate normalized token window", json: `{"token_windows":[{"amount":1,"duration":"60s"},{"amount":1,"duration":"1m"}]}`, wantErr: true},
		{name: "overflow token amount", json: `{"token_windows":[{"amount":9223372036854775808,"duration":"1m"}]}`, wantErr: true},
		{name: "unknown token window field", json: `{"token_windows":[{"amount":1,"duration":"1m","future":true}]}`, wantErr: true},
		{name: "invalid concurrency", json: `{"max_concurrent_requests":-1}`, wantErr: true},
		{name: "duplicate model rule", json: `{"denied_models":["x","x"]}`, wantErr: true},
		{name: "duplicate JSON field", json: `{"allowed_models":["x"],"allowed_models":["y"]}`, wantErr: true},
		{name: "duplicate normalized window", json: `{"request_windows":[{"amount":1,"duration":"60s"},{"amount":1,"duration":"1m"}]}`, wantErr: true},
		{name: "malformed document", json: `{"allowed_models":[]`, wantErr: true},
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
	policy, err := ParsePolicy([]byte(`{"allowed_models":["gpt-*"],"request_windows":[{"amount":2,"duration":"1m"}],"token_windows":[{"amount":20,"duration":"1m"}]}`))
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
