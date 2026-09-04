package auth

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAuthenticatorCredentialOutcomes(t *testing.T) {
	pepper := []byte("snapshot-test-pepper")
	first := generatedWithMaterial(t, pepper, bytes.Repeat([]byte{1}, GatewayKeyRandomBytes))
	collision := generatedWithMaterial(t, pepper, append(bytes.Repeat([]byte{1}, GatewayKeyRandomBytes-1), 2))
	disabled := generatedWithMaterial(t, pepper, bytes.Repeat([]byte{3}, GatewayKeyRandomBytes))
	expiration := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	expired := generatedWithMaterial(t, pepper, bytes.Repeat([]byte{4}, GatewayKeyRandomBytes))
	noExpiration := generatedWithMaterial(t, pepper, bytes.Repeat([]byte{5}, GatewayKeyRandomBytes))
	clock := func() time.Time { return expiration }
	authenticator, err := NewAuthenticator(pepper, clock)
	if err != nil {
		t.Fatal(err)
	}
	if err := authenticator.Load([]Record{
		{ID: "first", Name: "first", DisplayPrefix: first.DisplayPrefix, Digest: first.Digest, Enabled: true, PolicyJSON: []byte(`{"rpm":1}`)},
		// This candidate has the same indexed prefix as first and proves lookup
		// does not stop after the first digest mismatch.
		{ID: "collision", Name: "collision", DisplayPrefix: first.DisplayPrefix, Digest: collision.Digest, Enabled: true},
		{ID: "disabled", Name: "disabled", DisplayPrefix: disabled.DisplayPrefix, Digest: disabled.Digest, Enabled: false},
		{ID: "expired", Name: "expired", DisplayPrefix: expired.DisplayPrefix, Digest: expired.Digest, Enabled: true, ExpiresAt: &expiration},
		{ID: "none", Name: "none", DisplayPrefix: noExpiration.DisplayPrefix, Digest: noExpiration.Digest, Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}

	principal, err := authenticator.Authenticate(first.RawKey)
	if err != nil || principal.ID != "first" || string(principal.PolicyJSON) != `{"rpm":1}` {
		t.Fatalf("valid authentication = %#v, %v", principal, err)
	}
	principal.PolicyJSON[0] = 'X'
	again, err := authenticator.Authenticate(first.RawKey)
	if err != nil || string(again.PolicyJSON) != `{"rpm":1}` {
		t.Fatalf("principal mutated snapshot = %#v, %v", again, err)
	}
	if got, err := authenticator.Authenticate(collision.RawKey); err != nil || got.ID != "collision" {
		t.Fatalf("colliding-prefix authentication = %#v, %v", got, err)
	}
	if _, err := authenticator.Authenticate(disabled.RawKey); !errors.Is(err, ErrDisabledCredential) {
		t.Fatalf("disabled error = %v", err)
	}
	if _, err := authenticator.Authenticate(expired.RawKey); !errors.Is(err, ErrExpiredCredential) {
		t.Fatalf("expired error = %v", err)
	}
	if _, err := authenticator.Authenticate(noExpiration.RawKey); err != nil {
		t.Fatalf("no-expiration error = %v", err)
	}
	for _, credential := range []string{"", "not-a-key", first.RawKey[:len(first.RawKey)-1], "sk-gw-!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!", strings.Replace(first.RawKey, "A", "B", 1)} {
		if _, err := authenticator.Authenticate(credential); !errors.Is(err, ErrInvalidCredential) {
			t.Errorf("credential %q error = %v, want invalid", credential, err)
		}
	}
	if strings.Contains(authenticatorErrorText(authenticator, first.RawKey), first.RawKey) {
		t.Fatal("authentication error exposed raw credential")
	}
}

func TestAuthenticatorLoadIsAtomicAndConcurrentReadersSeeCompleteSnapshots(t *testing.T) {
	pepper := []byte("atomic-snapshot-pepper")
	oldKey := generatedWithMaterial(t, pepper, bytes.Repeat([]byte{6}, GatewayKeyRandomBytes))
	newKey := generatedWithMaterial(t, pepper, bytes.Repeat([]byte{7}, GatewayKeyRandomBytes))
	authenticator, err := NewAuthenticator(pepper, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := authenticator.Load([]Record{{ID: "old", DisplayPrefix: oldKey.DisplayPrefix, Digest: oldKey.Digest, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	if err := authenticator.Load([]Record{{ID: "broken", DisplayPrefix: newKey.DisplayPrefix, Digest: []byte{1}, Enabled: true}}); err == nil {
		t.Fatal("invalid replacement unexpectedly published")
	}
	if _, err := authenticator.Authenticate(oldKey.RawKey); err != nil {
		t.Fatalf("old snapshot lost after failed replacement: %v", err)
	}

	const readers = 8
	const iterations = 500
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	done.Add(readers)
	for i := 0; i < readers; i++ {
		go func() {
			defer done.Done()
			start.Wait()
			for j := 0; j < iterations; j++ {
				oldPrincipal, oldErr := authenticator.Authenticate(oldKey.RawKey)
				newPrincipal, newErr := authenticator.Authenticate(newKey.RawKey)
				if oldErr != nil && !errors.Is(oldErr, ErrInvalidCredential) || oldErr == nil && oldPrincipal.ID != "old" {
					t.Errorf("reader observed invalid old snapshot: %#v/%v", oldPrincipal, oldErr)
					return
				}
				if newErr != nil && !errors.Is(newErr, ErrInvalidCredential) || newErr == nil && newPrincipal.ID != "new" {
					t.Errorf("reader observed invalid new snapshot: %#v/%v", newPrincipal, newErr)
					return
				}
			}
		}()
	}
	start.Done()
	if err := authenticator.Load([]Record{{ID: "new", DisplayPrefix: newKey.DisplayPrefix, Digest: newKey.Digest, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	done.Wait()
}

func generatedWithMaterial(t *testing.T, pepper, material []byte) GeneratedGatewayKey {
	t.Helper()
	generated, err := newGatewayKeyGenerator(bytes.NewReader(material)).Generate(pepper)
	if err != nil {
		t.Fatal(err)
	}
	return generated
}

func authenticatorErrorText(authenticator *Authenticator, credential string) string {
	_, err := authenticator.Authenticate(credential)
	if err == nil {
		return ""
	}
	return err.Error()
}
