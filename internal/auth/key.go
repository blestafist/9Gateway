// Package auth contains gateway-key identity and verification primitives.
package auth

import (
	"crypto/hmac"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

const (
	// GatewayKeyNamespace makes gateway credentials distinguishable from other
	// API-key formats while keeping them safe to put in an Authorization header.
	GatewayKeyNamespace = "sk-gw-"
	// GatewayKeyRandomBytes provides 256 bits of entropy per issued key.
	GatewayKeyRandomBytes = 32
	// GatewayKeyDisplayPrefixLength is the amount of encoded random material
	// retained for indexed candidate lookup. It is not used as a secret.
	GatewayKeyDisplayPrefixLength = 8

	encodedRandomLength = 43 // base64.RawURLEncoding of GatewayKeyRandomBytes
	keyLength           = len(GatewayKeyNamespace) + encodedRandomLength
	prefixLength        = len(GatewayKeyNamespace) + GatewayKeyDisplayPrefixLength
)

var (
	// ErrInvalidGatewayKey means the credential is not an issued gateway key.
	ErrInvalidGatewayKey = errors.New("invalid gateway key")
	// ErrInvalidPepper means no usable authentication pepper was supplied.
	ErrInvalidPepper = errors.New("invalid gateway key pepper")
)

// ParsedGatewayKey contains only the lookup identity derived from a valid
// raw key. It deliberately does not retain the raw credential.
type ParsedGatewayKey struct {
	DisplayPrefix string
}

// GeneratedGatewayKey is the one-time creation result. RawKey is present so a
// caller can reveal it at creation time; storage records must be built from
// DisplayPrefix and Digest instead. Its formatting methods never reveal RawKey.
type GeneratedGatewayKey struct {
	RawKey        string `json:"key"`
	DisplayPrefix string `json:"prefix"`
	Digest        []byte `json:"-"`
}

// String intentionally provides a safe diagnostic representation.
func (key GeneratedGatewayKey) String() string {
	return fmt.Sprintf("gateway key prefix=%s digest=<redacted>", key.DisplayPrefix)
}

// GoString keeps %#v diagnostics from accidentally displaying the one-time
// raw credential.
func (key GeneratedGatewayKey) GoString() string {
	return key.String()
}

// KeyGenerator issues gateway keys using its reader. Production callers should
// use NewGatewayKeyGenerator; the reader constructor is package-private so
// randomness is injectable only by deterministic package tests.
type KeyGenerator struct {
	random io.Reader
}

// NewGatewayKeyGenerator creates a generator backed by crypto/rand.Reader.
func NewGatewayKeyGenerator() *KeyGenerator {
	return &KeyGenerator{random: cryptorand.Reader}
}

// newGatewayKeyGenerator is intentionally package-private: production code
// cannot replace the cryptographically secure source by accident.
func newGatewayKeyGenerator(random io.Reader) *KeyGenerator {
	return &KeyGenerator{random: random}
}

// Generate creates a random key and its storage-safe fingerprint.
func (generator *KeyGenerator) Generate(pepper []byte) (GeneratedGatewayKey, error) {
	if len(pepper) == 0 {
		return GeneratedGatewayKey{}, ErrInvalidPepper
	}
	if generator == nil || generator.random == nil {
		return GeneratedGatewayKey{}, errors.New("gateway key generator unavailable")
	}
	randomMaterial := make([]byte, GatewayKeyRandomBytes)
	if _, err := io.ReadFull(generator.random, randomMaterial); err != nil {
		// Do not wrap reader errors: custom readers may include sensitive test
		// material in their error, and key-generation errors must be safe.
		return GeneratedGatewayKey{}, errors.New("generate gateway key: random source unavailable")
	}
	encoded := base64.RawURLEncoding.EncodeToString(randomMaterial)
	rawKey := GatewayKeyNamespace + encoded
	digest := digestKey(pepper, rawKey)
	return GeneratedGatewayKey{
		RawKey:        rawKey,
		DisplayPrefix: rawKey[:prefixLength],
		Digest:        digest,
	}, nil
}

// GenerateGatewayKey is the production convenience form.
func GenerateGatewayKey(pepper []byte) (GeneratedGatewayKey, error) {
	return NewGatewayKeyGenerator().Generate(pepper)
}

// ParseGatewayKey validates the exact issued-key format and returns its
// indexed display identity. Validation is independent of storage lookup.
func ParseGatewayKey(rawKey string) (ParsedGatewayKey, error) {
	if len(rawKey) != keyLength || len(rawKey) < prefixLength || rawKey[:len(GatewayKeyNamespace)] != GatewayKeyNamespace {
		return ParsedGatewayKey{}, ErrInvalidGatewayKey
	}
	encoded := rawKey[len(GatewayKeyNamespace):]
	randomMaterial, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(randomMaterial) != GatewayKeyRandomBytes || base64.RawURLEncoding.EncodeToString(randomMaterial) != encoded {
		return ParsedGatewayKey{}, ErrInvalidGatewayKey
	}
	return ParsedGatewayKey{DisplayPrefix: rawKey[:prefixLength]}, nil
}

// ValidateGatewayKey reports whether rawKey has the issued gateway format.
func ValidateGatewayKey(rawKey string) error {
	_, err := ParseGatewayKey(rawKey)
	return err
}

// DisplayPrefix returns the indexed identity for a valid raw key.
func DisplayPrefix(rawKey string) (string, error) {
	parsed, err := ParseGatewayKey(rawKey)
	if err != nil {
		return "", err
	}
	return parsed.DisplayPrefix, nil
}

// FingerprintGatewayKey computes HMAC-SHA256(pepper, rawKey). Format is
// validated before hashing, so malformed credentials can be rejected before a
// repository lookup.
func FingerprintGatewayKey(pepper []byte, rawKey string) ([]byte, error) {
	if _, err := ParseGatewayKey(rawKey); err != nil {
		return nil, err
	}
	if len(pepper) == 0 {
		return nil, ErrInvalidPepper
	}
	return digestKey(pepper, rawKey), nil
}

// VerifyGatewayKey validates the format, fingerprints the credential, and
// compares the fixed-size digests with hmac.Equal's constant-time primitive.
func VerifyGatewayKey(pepper []byte, rawKey string, expectedDigest []byte) bool {
	if len(expectedDigest) != sha256.Size {
		return false
	}
	digest, err := FingerprintGatewayKey(pepper, rawKey)
	return err == nil && hmac.Equal(digest, expectedDigest)
}

func digestKey(pepper []byte, rawKey string) []byte {
	mac := hmac.New(sha256.New, pepper)
	_, _ = mac.Write([]byte(rawKey))
	return mac.Sum(nil)
}
