package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestGeneratedGatewayKeyFormatAndFingerprint(t *testing.T) {
	pepper := []byte("test-only-pepper")
	first, err := GenerateGatewayKey(pepper)
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateGatewayKey(pepper)
	if err != nil {
		t.Fatal(err)
	}
	if first.RawKey == second.RawKey || first.RawKey == "" || first.RawKey[:len(GatewayKeyNamespace)] != GatewayKeyNamespace {
		t.Fatalf("keys are not distinct issued keys: %q, %q", first.RawKey, second.RawKey)
	}
	if len(first.RawKey) != keyLength || len(first.Digest) != sha256.Size {
		t.Fatalf("generated key lengths = %d/%d", len(first.RawKey), len(first.Digest))
	}
	parsed, err := ParseGatewayKey(first.RawKey)
	if err != nil {
		t.Fatalf("ParseGatewayKey() error = %v", err)
	}
	if parsed.DisplayPrefix != first.DisplayPrefix || !strings.HasPrefix(first.RawKey, first.DisplayPrefix) {
		t.Fatalf("prefix = %q, generated prefix = %q", parsed.DisplayPrefix, first.DisplayPrefix)
	}
	if !hmac.Equal(first.Digest, mustFingerprint(t, pepper, first.RawKey)) {
		t.Fatal("generated digest does not match HMAC fingerprint")
	}
}

func TestFingerprintStableAndSensitiveInputsMatter(t *testing.T) {
	key := "sk-gw-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	pepper := []byte("pepper-a")
	one := mustFingerprint(t, pepper, key)
	if got := mustFingerprint(t, pepper, key); !hmac.Equal(one, got) {
		t.Fatal("same inputs produced different digest")
	}
	differentKey := key[:len(GatewayKeyNamespace)+1] + "B" + key[len(GatewayKeyNamespace)+2:]
	if hmac.Equal(one, mustFingerprint(t, pepper, differentKey)) {
		t.Fatal("different keys produced the same digest")
	}
	if hmac.Equal(one, mustFingerprint(t, []byte("pepper-b"), key)) {
		t.Fatal("different peppers produced the same digest")
	}
	if !VerifyGatewayKey(pepper, key, one) || VerifyGatewayKey(pepper, key, []byte("short")) {
		t.Fatal("digest verification result is incorrect")
	}
}

func TestMalformedGatewayKeyRejectedBeforeFingerprint(t *testing.T) {
	pepper := "pepper-that-must-not-leak"
	for _, malformed := range []string{
		"",
		"sk-gw-",
		"sk-gw-!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!",
		"sk-other-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"sk-gw-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		// The final base64 character has non-zero unused bits, so this is not
		// the canonical encoding of the decoded random material.
		"sk-gw-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAB",
	} {
		if _, err := FingerprintGatewayKey([]byte(pepper), malformed); !errors.Is(err, ErrInvalidGatewayKey) {
			t.Errorf("FingerprintGatewayKey(%q) error = %v, want malformed-key error", malformed, err)
		} else if strings.Contains(err.Error(), pepper) || (malformed != "" && strings.Contains(err.Error(), malformed)) {
			t.Errorf("malformed-key error contains credential material: %v", err)
		}
	}
}

func TestGenerationErrorsDoNotRevealCredentialMaterial(t *testing.T) {
	rawKey := "sk-gw-credential-material"
	pepper := "pepper-material"
	generator := newGatewayKeyGenerator(strings.NewReader("short"))
	_, err := generator.Generate([]byte(pepper))
	if err == nil || strings.Contains(err.Error(), rawKey) || strings.Contains(err.Error(), pepper) {
		t.Fatalf("unsafe generation error = %v", err)
	}
}

func TestInjectedRandomnessIsOnlyUsedForDeterministicGeneration(t *testing.T) {
	random := strings.NewReader(strings.Repeat("x", GatewayKeyRandomBytes))
	generated, err := newGatewayKeyGenerator(random).Generate([]byte("pepper"))
	if err != nil {
		t.Fatal(err)
	}
	if generated.RawKey != "sk-gw-eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHg" {
		t.Fatalf("deterministic key = %q", generated.RawKey)
	}
}

func TestGeneratedKeyDiagnosticsDoNotRevealRawKey(t *testing.T) {
	generated, err := GenerateGatewayKey([]byte("private-pepper"))
	if err != nil {
		t.Fatal(err)
	}
	for _, rendered := range []string{fmt.Sprintf("%v", generated), fmt.Sprintf("%+v", generated), fmt.Sprintf("%#v", generated)} {
		if strings.Contains(rendered, generated.RawKey) || strings.Contains(rendered, "private-pepper") {
			t.Fatalf("diagnostic contains secret: %q", rendered)
		}
	}
	encoded, err := json.Marshal(generated)
	if err != nil {
		t.Fatal(err)
	}
	// The one-time creation result intentionally contains the raw key in JSON;
	// its safe storage fields never do.
	if !strings.Contains(string(encoded), generated.RawKey) || strings.Contains(string(encoded), "private-pepper") {
		t.Fatalf("creation JSON = %s", encoded)
	}
}

func mustFingerprint(t *testing.T, pepper []byte, key string) []byte {
	t.Helper()
	digest, err := FingerprintGatewayKey(pepper, key)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
