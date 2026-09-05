package auth

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"strings"
	"sync/atomic"
	"time"
)

var (
	// ErrInvalidCredential is returned for malformed, unknown, or incorrectly
	// fingerprinted credentials. It intentionally does not identify which
	// part of authentication failed.
	ErrInvalidCredential = errors.New("invalid gateway credential")
	// ErrDisabledCredential and ErrExpiredCredential are kept distinct for
	// policy and metrics, while their messages contain no credential detail.
	ErrDisabledCredential = errors.New("gateway credential disabled")
	ErrExpiredCredential  = errors.New("gateway credential expired")
)

// Clock supplies the time used for expiration checks. A nil clock means
// time.Now and is useful for production callers; tests can provide a fixed
// clock without changing the authentication path.
type Clock func() time.Time

// Record is the storage-independent input to an authentication snapshot. It
// contains no raw key or pepper. Policy and Digest are copied while loading.
// PolicyJSON is the canonical policy field. Policy is accepted as a concise
// alias for callers that already have decoded policy bytes.
type Record struct {
	ID            string
	Name          string
	DisplayPrefix string
	// Prefix is a compatibility spelling for DisplayPrefix. If both are set,
	// they must agree.
	Prefix string
	Digest []byte
	// KeyHash is a compatibility spelling for Digest. If both are set, they
	// must agree.
	KeyHash    []byte
	Enabled    bool
	ExpiresAt  *time.Time
	PolicyJSON []byte
	Policy     []byte
}

// AuthRecord is a descriptive alias for Record.
type AuthRecord = Record

// Principal is the safe identity returned after authentication. Its byte field
// is owned by the caller and can be changed without changing the published
// snapshot; Policy is a value with private compiled state and copy-returning
// accessors.
type Principal struct {
	ID            string
	Name          string
	DisplayPrefix string
	PolicyJSON    []byte
	Policy        EffectivePolicy
}

type snapshotRecord struct {
	id         string
	name       string
	digest     [sha256.Size]byte
	enabled    bool
	expiresAt  *time.Time
	policyJSON []byte
	policy     EffectivePolicy
}

// Snapshot is an immutable collection of authentication candidates. The map
// and all records are private by design; only Authenticate can read it.
// Collision candidates are retained in insertion order under one prefix.
type Snapshot struct {
	byPrefix map[string][]snapshotRecord
}

// Authenticator owns the process pepper and atomically publishes complete
// snapshots. The pepper is deliberately not part of Snapshot.
type Authenticator struct {
	pepper    []byte
	now       Clock
	tokenMode TokenMode
	state     atomic.Pointer[Snapshot]
}

// NewAuthenticator creates an authenticator with an initially empty snapshot.
func NewAuthenticator(pepper []byte, now Clock, tokenModes ...TokenMode) (*Authenticator, error) {
	if len(pepper) == 0 {
		return nil, ErrInvalidPepper
	}
	tokenMode := TokenModeEstimate
	if len(tokenModes) > 1 || len(tokenModes) == 1 && tokenModes[0] != TokenModeUsageOnly && tokenModes[0] != TokenModeEstimate {
		return nil, ErrInvalidPolicy
	}
	if len(tokenModes) == 1 {
		tokenMode = tokenModes[0]
	}
	authenticator := &Authenticator{pepper: append([]byte(nil), pepper...), now: now, tokenMode: tokenMode}
	authenticator.state.Store(emptySnapshot())
	return authenticator, nil
}

// NewSnapshotAuthenticator is an equivalent descriptive constructor.
func NewSnapshotAuthenticator(pepper []byte, now Clock, tokenModes ...TokenMode) (*Authenticator, error) {
	return NewAuthenticator(pepper, now, tokenModes...)
}

// Load validates and builds a complete replacement before publishing it. If
// any record is invalid, the currently published snapshot is left untouched.
func (authenticator *Authenticator) Load(records []Record) error {
	if authenticator == nil || len(authenticator.pepper) == 0 {
		return ErrInvalidPepper
	}
	next, err := authenticator.Prepare(records)
	if err != nil {
		return err
	}
	authenticator.Publish(next)
	return nil
}

// Prepare validates and builds a complete immutable snapshot without changing
// the currently published state. The returned snapshot is safe to publish
// atomically after a related durable mutation succeeds.
func (authenticator *Authenticator) Prepare(records []Record) (*Snapshot, error) {
	if authenticator == nil || len(authenticator.pepper) == 0 {
		return nil, ErrInvalidPepper
	}
	next, err := buildSnapshot(records, authenticator.tokenMode)
	if err != nil {
		return nil, err
	}
	return next, nil
}

// Publish atomically replaces the complete authentication snapshot. A
// snapshot can only be obtained from Prepare or Snapshot, and its internals
// are private, so publication itself cannot fail.
func (authenticator *Authenticator) Publish(snapshot *Snapshot) {
	if authenticator == nil || snapshot == nil {
		return
	}
	authenticator.state.Store(snapshot)
}

// Replace is the mutation-oriented spelling of Load.
func (authenticator *Authenticator) Replace(records []Record) error {
	return authenticator.Load(records)
}

// Refresh is an alias used by startup/admin refresh callers.
func (authenticator *Authenticator) Refresh(records []Record) error {
	return authenticator.Load(records)
}

// Snapshot returns the currently published immutable snapshot. Its contents
// cannot be accessed or modified directly; retaining the value is safe while
// a later Load publishes a different snapshot.
func (authenticator *Authenticator) Snapshot() *Snapshot {
	if authenticator == nil {
		return emptySnapshot()
	}
	current := authenticator.state.Load()
	if current == nil {
		return emptySnapshot()
	}
	return current
}

// Authenticate performs format validation, prefix lookup, HMAC-SHA256, and a
// constant-time digest comparison without consulting storage. A matching
// disabled or expired candidate is reported distinctly; malformed, unknown,
// and wrong-digest credentials deliberately share ErrInvalidCredential.
func (authenticator *Authenticator) Authenticate(rawKey string) (Principal, error) {
	if authenticator == nil || len(authenticator.pepper) == 0 {
		return Principal{}, ErrInvalidCredential
	}
	current := authenticator.state.Load()
	if current == nil {
		return Principal{}, ErrInvalidCredential
	}
	return authenticator.authenticateSnapshot(current, rawKey)
}

func (authenticator *Authenticator) authenticateSnapshot(current *Snapshot, rawKey string) (Principal, error) {
	if current == nil {
		return Principal{}, ErrInvalidCredential
	}
	parsed, err := ParseGatewayKey(rawKey)
	if err != nil {
		return Principal{}, ErrInvalidCredential
	}
	candidates := current.byPrefix[parsed.DisplayPrefix]
	if len(candidates) == 0 {
		return Principal{}, ErrInvalidCredential
	}
	digest := digestKey(authenticator.pepper, rawKey)
	var disabled, expired bool
	for _, candidate := range candidates {
		if !hmac.Equal(digest, candidate.digest[:]) {
			continue
		}
		if !candidate.enabled {
			disabled = true
			continue
		}
		if candidate.expiresAt != nil && !authenticator.currentTime().Before(*candidate.expiresAt) {
			expired = true
			continue
		}
		return principalFor(candidate, parsed.DisplayPrefix), nil
	}
	if disabled {
		return Principal{}, ErrDisabledCredential
	}
	if expired {
		return Principal{}, ErrExpiredCredential
	}
	return Principal{}, ErrInvalidCredential
}

func buildSnapshot(records []Record, tokenMode TokenMode) (*Snapshot, error) {
	next := emptySnapshot()
	for _, record := range records {
		prefix := record.DisplayPrefix
		if prefix == "" {
			prefix = record.Prefix
		} else if record.Prefix != "" && record.Prefix != prefix {
			return nil, errors.New("invalid authentication record")
		}
		digest := record.Digest
		if len(digest) == 0 {
			digest = record.KeyHash
		} else if len(record.KeyHash) != 0 && !hmac.Equal(record.KeyHash, digest) {
			return nil, errors.New("invalid authentication record")
		}
		if !validDisplayPrefix(prefix) || strings.TrimSpace(record.ID) == "" || len(digest) != sha256.Size {
			return nil, errors.New("invalid authentication record")
		}
		policy := record.PolicyJSON
		if len(policy) == 0 {
			policy = record.Policy
		} else if len(record.Policy) != 0 && !bytes.Equal(record.Policy, policy) {
			return nil, errors.New("invalid authentication record")
		}
		effectivePolicy, err := ParsePolicyJSONWithTokenMode(policy, tokenMode)
		if err != nil {
			return nil, err
		}
		candidate := snapshotRecord{
			id:         record.ID,
			name:       record.Name,
			enabled:    record.Enabled,
			policyJSON: append([]byte(nil), policy...),
			policy:     effectivePolicy,
		}
		copy(candidate.digest[:], digest)
		if record.ExpiresAt != nil {
			expiresAt := record.ExpiresAt.UTC()
			candidate.expiresAt = &expiresAt
		}
		next.byPrefix[prefix] = append(next.byPrefix[prefix], candidate)
	}
	return next, nil
}

func principalFor(candidate snapshotRecord, prefix string) Principal {
	return Principal{
		ID:            candidate.id,
		Name:          candidate.name,
		DisplayPrefix: prefix,
		PolicyJSON:    append([]byte(nil), candidate.policyJSON...),
		Policy:        candidate.policy,
	}
}

func emptySnapshot() *Snapshot {
	return &Snapshot{byPrefix: make(map[string][]snapshotRecord)}
}

func validDisplayPrefix(prefix string) bool {
	if len(prefix) != prefixLength || !strings.HasPrefix(prefix, GatewayKeyNamespace) {
		return false
	}
	for _, character := range prefix[len(GatewayKeyNamespace):] {
		if !(character >= 'A' && character <= 'Z') && !(character >= 'a' && character <= 'z') &&
			!(character >= '0' && character <= '9') && character != '-' && character != '_' {
			return false
		}
	}
	return true
}

func (authenticator *Authenticator) currentTime() time.Time {
	if authenticator.now == nil {
		return time.Now().UTC()
	}
	return authenticator.now().UTC()
}
