package storage

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pestit/9gateway/internal/auth"
)

const (
	// HMACDigestSize is the required size of an HMAC-SHA256 digest.
	HMACDigestSize = 32
	hmacDigestSize = HMACDigestSize
)

// APIKeyRecord is the storage-independent representation of a gateway key.
// It deliberately contains only the display prefix and keyed digest; a raw
// gateway key and the authentication pepper never cross this boundary.
//
// Digest and KeyHash are copied when a record enters or leaves the repository. Callers may
// therefore reuse or modify their input buffer without changing persisted
// state, and may safely modify a returned record.
type APIKeyRecord struct {
	ID            string
	Name          string
	DisplayPrefix string
	Digest        []byte
	Enabled       bool
	ExpiresAt     *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
	PolicyJSON    string

	// Prefix and KeyHash are retained as clear migration aliases for callers
	// that use the schema terminology. New code should use DisplayPrefix and
	// Digest. Both forms are normalized at the repository boundary.
	Prefix  string
	KeyHash []byte
}

var (
	// ErrNotFound is returned when an API key identity does not exist.
	ErrNotFound = errors.New("api key not found")
	// ErrConflict identifies a uniqueness conflict on API key identity.
	ErrConflict = errors.New("api key already exists")
	// ErrDuplicate and ErrUniquenessConflict are descriptive aliases for
	// callers that want to name the kind of conflict they handle.
	ErrDuplicate          = ErrConflict
	ErrUniquenessConflict = ErrConflict
	ErrAlreadyExists      = ErrConflict
	// ErrInvalidRecord identifies a record or lookup identity that cannot be
	// represented by the API-key schema.
	ErrInvalidRecord = errors.New("invalid api key record")
	// ErrRepositoryUnavailable indicates a nil or closed repository handle.
	ErrRepositoryUnavailable = errors.New("api key repository unavailable")
	// ErrInvalidCursor indicates a malformed, tampered, or incompatible page cursor.
	ErrInvalidCursor = errors.New("invalid api key cursor")
)

const (
	defaultAPIKeyListLimit = 50
	maxAPIKeyListLimit     = 500
	maxAPIKeyCursorBytes   = 512
)

// KeyPolicySummary contains only the policy facts safe for the list endpoint.
type KeyPolicySummary struct {
	AllowModels     bool
	DenyModels      bool
	LogRequestBody  bool
	LogResponseBody bool
}

// KeyListRecord is the safe, metadata-only representation used by admin key
// listing. It intentionally has no digest, raw key, or policy document.
type KeyListRecord struct {
	ID            string
	Name          string
	DisplayPrefix string
	Enabled       bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
	ExpiresAt     *time.Time
	PolicySummary KeyPolicySummary
}

// KeyDetailRecord is the safe, fully compiled representation used by the
// administrative detail endpoint. It deliberately contains neither the key
// digest nor the stored policy document.
type KeyDetailRecord struct {
	ID            string
	Name          string
	DisplayPrefix string
	Enabled       bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
	ExpiresAt     *time.Time
	Policy        auth.EffectivePolicy
}

type apiKeyListCursor struct {
	CreatedAt int64
	ID        string
}

// Validate checks all invariants which the repository requires of a record.
// PolicyJSON is intentionally opaque and is not parsed or otherwise
// interpreted here.
func (record APIKeyRecord) Validate() error {
	prefix, digest, ok := record.identityValues()
	if strings.TrimSpace(record.ID) == "" || strings.TrimSpace(record.Name) == "" || strings.TrimSpace(prefix) == "" || !ok {
		return ErrInvalidRecord
	}
	if len(digest) != hmacDigestSize {
		return ErrInvalidRecord
	}
	if !validTimestamp(record.CreatedAt) || !validTimestamp(record.UpdatedAt) {
		return ErrInvalidRecord
	}
	if record.UpdatedAt.Before(record.CreatedAt) {
		return ErrInvalidRecord
	}
	if record.ExpiresAt != nil && !validTimestamp(*record.ExpiresAt) {
		return ErrInvalidRecord
	}
	return nil
}

func (record APIKeyRecord) identityValues() (string, []byte, bool) {
	prefix := record.DisplayPrefix
	if prefix == "" {
		prefix = record.Prefix
	} else if record.Prefix != "" && record.Prefix != prefix {
		return "", nil, false
	}
	digest := record.Digest
	if len(digest) == 0 {
		digest = record.KeyHash
	} else if len(record.KeyHash) != 0 && !bytes.Equal(record.KeyHash, digest) {
		return "", nil, false
	}
	return prefix, digest, true
}

func validTimestamp(value time.Time) bool {
	return !value.IsZero() && value.Nanosecond() == 0
}

// dbQueries is the small SQL capability needed by this repository. Keeping it
// as an internal interface makes SQL details an implementation concern while
// allowing storage integration tests to use either *DB or *sql.DB.
type dbQueries interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// APIKeyRepository persists APIKeyRecords in the schema installed by T066.
type APIKeyRepository struct {
	database   dbQueries
	beginner   txBeginner
	cursorAEAD cipher.AEAD
	tokenMode  auth.TokenMode
}

// ListRequests shares the API-key repository's database and cursor signer with
// the admin read handler. It still uses the request-history repository's
// metadata-only projection and never selects request bodies.
func (repository *APIKeyRepository) ListRequests(ctx context.Context, filter ListRequestsFilter, limit int, cursor string) ([]RequestListRecord, string, error) {
	if repository == nil {
		return nil, "", ErrRepositoryUnavailable
	}
	return listRequests(repository.database, repository.cursorAEAD, ctx, filter, limit, cursor)
}

// NewAPIKeyRepository creates a repository over an opened storage database.
// The argument is accepted as the narrow query capability so SQLite-specific
// handles do not become part of the repository's domain API.
func NewAPIKeyRepository(database dbQueries) *APIKeyRepository {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		// Failure of the system CSPRNG is not recoverable for a cursor signer.
		// Keep construction backwards-compatible while making all resulting
		// cursors fail closed rather than using a predictable fallback.
		key = nil
	}
	return newAPIKeyRepository(database, key)
}

func newAPIKeyRepository(database dbQueries, key []byte) *APIKeyRepository {
	var aead cipher.AEAD
	if len(key) == 32 {
		if block, err := aes.NewCipher(key); err == nil {
			aead, _ = cipher.NewGCM(block)
		}
	}
	repository := &APIKeyRepository{database: database, cursorAEAD: aead, tokenMode: auth.TokenModeEstimate}
	if beginner, ok := database.(txBeginner); ok {
		repository.beginner = beginner
	}
	return repository
}

// GetRequestByID reads request metadata and body-kind availability through the
// shared history projection. It intentionally does not expose key material or
// body contents.
func (repository *APIKeyRepository) GetRequestByID(ctx context.Context, requestID string) (*RequestDetailRecord, error) {
	if ctx == nil {
		return nil, errors.New("get request detail: nil context")
	}
	if !validHistoryRequestID(requestID) {
		return nil, ErrHistoryInvalidRecord
	}
	if repository == nil || repository.database == nil {
		return nil, ErrRepositoryUnavailable
	}
	return getRequestByID(repository.database, repository.beginner, ctx, requestID)
}

// GetRequestBody shares the request-history body projection while keeping SQL
// and body validation behind the storage boundary used by the admin handler.
func (repository *APIKeyRepository) GetRequestBody(ctx context.Context, requestID, kind string) (*BodyContent, error) {
	if ctx == nil {
		return nil, errors.New("get request body: nil context")
	}
	if !validHistoryRequestID(requestID) {
		return nil, ErrHistoryInvalidRecord
	}
	if !validRequestBodyKind(kind) {
		return nil, ErrHistoryInvalidBody
	}
	if repository == nil || repository.database == nil {
		return nil, ErrRepositoryUnavailable
	}
	return getRequestBody(repository.database, repository.beginner, ctx, requestID, kind)
}

// SetTokenMode configures the deployment default used when a stored policy
// omits token_mode. It is called during process wiring, before requests are
// served, and keeps detail reads consistent with authentication enforcement.
func (repository *APIKeyRepository) SetTokenMode(mode auth.TokenMode) {
	if repository == nil || (mode != auth.TokenModeUsageOnly && mode != auth.TokenModeEstimate) {
		return
	}
	repository.tokenMode = mode
}

// SetCursorSecret scopes cursors to the server's existing secret. It is called
// during handler construction, before the repository is shared by requests.
func (repository *APIKeyRepository) SetCursorSecret(secret []byte) {
	if repository == nil || len(secret) == 0 {
		return
	}
	keyDigest := sha256.Sum256(secret)
	key := keyDigest[:]
	if block, err := aes.NewCipher(key); err == nil {
		repository.cursorAEAD, _ = cipher.NewGCM(block)
	}
}

// Repository is the short name for APIKeyRepository.
type Repository = APIKeyRepository

// NewRepository is an equivalent concise constructor.
func NewRepository(database dbQueries) *Repository {
	return NewAPIKeyRepository(database)
}

// Insert stores one validated API-key record.
func (repository *APIKeyRepository) Insert(ctx context.Context, record APIKeyRecord) error {
	if ctx == nil {
		return errors.New("insert api key: nil context")
	}
	if err := record.Validate(); err != nil {
		return err
	}
	if repository == nil || repository.database == nil {
		return ErrRepositoryUnavailable
	}

	// Make the copy before handing the value to database/sql. This keeps the
	// boundary explicit even if the driver changes how BLOB arguments are
	// handled in the future.
	prefix, digest, _ := record.identityValues()
	keyHash := append([]byte(nil), digest...)
	var expiresAt any
	if record.ExpiresAt != nil {
		expiresAt = record.ExpiresAt.Unix()
	}
	_, err := repository.database.ExecContext(ctx, `
		INSERT INTO api_keys
			(id, name, prefix, key_hash, enabled, expires_at, created_at, updated_at, policy_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.Name, prefix, keyHash, boolInt(record.Enabled), expiresAt,
		record.CreatedAt.Unix(), record.UpdatedAt.Unix(), record.PolicyJSON)
	if err != nil {
		return insertError(err)
	}
	return nil
}

// LookupByDisplayPrefix finds the record identified by its indexed display
// prefix.
func (repository *APIKeyRepository) LookupByDisplayPrefix(ctx context.Context, prefix string) (APIKeyRecord, error) {
	if strings.TrimSpace(prefix) == "" {
		return APIKeyRecord{}, ErrInvalidRecord
	}
	return repository.lookup(ctx, `SELECT id, name, prefix, key_hash, enabled, expires_at, created_at, updated_at, policy_json
		FROM api_keys WHERE prefix = ?`, prefix, "lookup api key")
}

// GetByPrefix is a compatibility spelling for LookupByDisplayPrefix.
func (repository *APIKeyRepository) GetByPrefix(ctx context.Context, prefix string) (APIKeyRecord, error) {
	return repository.LookupByDisplayPrefix(ctx, prefix)
}

// FindByDisplayPrefix is a compatibility spelling for LookupByDisplayPrefix.
func (repository *APIKeyRepository) FindByDisplayPrefix(ctx context.Context, prefix string) (APIKeyRecord, error) {
	return repository.LookupByDisplayPrefix(ctx, prefix)
}

// LookupByPrefix is a concise spelling for LookupByDisplayPrefix.
func (repository *APIKeyRepository) LookupByPrefix(ctx context.Context, prefix string) (APIKeyRecord, error) {
	return repository.LookupByDisplayPrefix(ctx, prefix)
}

// GetByID finds a record by its stable ID.
func (repository *APIKeyRepository) GetByID(ctx context.Context, id string) (APIKeyRecord, error) {
	if strings.TrimSpace(id) == "" {
		return APIKeyRecord{}, ErrInvalidRecord
	}
	return repository.lookup(ctx, `SELECT id, name, prefix, key_hash, enabled, expires_at, created_at, updated_at, policy_json
		FROM api_keys WHERE id = ?`, id, "get api key")
}

// GetAPIKeyByID returns one safe, typed detail record. The policy is parsed as
// part of the same SQLite row read, so callers never combine metadata from one
// committed row with policy from another. Stored policy corruption is returned
// as auth.ErrInvalidPolicy and is therefore an internal error to HTTP callers.
func (repository *APIKeyRepository) GetAPIKeyByID(ctx context.Context, id string) (*KeyDetailRecord, error) {
	if ctx == nil {
		return nil, errors.New("get api key detail: nil context")
	}
	if strings.TrimSpace(id) == "" {
		return nil, ErrInvalidRecord
	}
	if repository == nil || repository.database == nil {
		return nil, ErrRepositoryUnavailable
	}
	var record KeyDetailRecord
	var enabled int64
	var expiresAt sql.NullInt64
	var createdAt, updatedAt int64
	var policyJSON string
	err := repository.database.QueryRowContext(ctx, `
		SELECT id, name, prefix, enabled, expires_at, created_at, updated_at, policy_json
		FROM api_keys WHERE id = ?`, id).Scan(
		&record.ID, &record.Name, &record.DisplayPrefix, &enabled, &expiresAt,
		&createdAt, &updatedAt, &policyJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, errors.New("get api key detail: database read failed")
	}
	if enabled != 0 && enabled != 1 {
		return nil, ErrInvalidRecord
	}
	created, createdOK := unixTimestamp(createdAt)
	updated, updatedOK := unixTimestamp(updatedAt)
	if !createdOK || !updatedOK || updated.Before(created) {
		return nil, ErrInvalidRecord
	}
	if expiresAt.Valid {
		expires := time.Unix(expiresAt.Int64, 0).UTC()
		record.ExpiresAt = &expires
	}
	record.Enabled = enabled == 1
	record.CreatedAt = created
	record.UpdatedAt = updated
	mode := repository.tokenMode
	if mode == "" {
		mode = auth.TokenModeEstimate
	}
	record.Policy, err = auth.ParsePolicyJSONWithTokenMode([]byte(policyJSON), mode)
	if err != nil {
		return nil, err
	}
	return &record, nil
}

// Get is a concise spelling for GetByID.
func (repository *APIKeyRepository) Get(ctx context.Context, id string) (APIKeyRecord, error) {
	return repository.GetByID(ctx, id)
}

// FindByID is a compatibility spelling for GetByID.
func (repository *APIKeyRepository) FindByID(ctx context.Context, id string) (APIKeyRecord, error) {
	return repository.GetByID(ctx, id)
}

func (repository *APIKeyRepository) lookup(ctx context.Context, query, identity, operation string) (APIKeyRecord, error) {
	if ctx == nil {
		return APIKeyRecord{}, errors.New(operation + ": nil context")
	}
	if repository == nil || repository.database == nil {
		return APIKeyRecord{}, ErrRepositoryUnavailable
	}
	var record APIKeyRecord
	var keyHash []byte
	var enabled int64
	var expiresAt sql.NullInt64
	var createdAt, updatedAt int64
	err := repository.database.QueryRowContext(ctx, query, identity).Scan(
		&record.ID, &record.Name, &record.Prefix, &keyHash, &enabled, &expiresAt,
		&createdAt, &updatedAt, &record.PolicyJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return APIKeyRecord{}, ErrNotFound
	}
	if err != nil {
		return APIKeyRecord{}, errors.New(operation + ": database read failed")
	}
	if enabled != 0 && enabled != 1 {
		return APIKeyRecord{}, ErrInvalidRecord
	}
	record.Enabled = enabled == 1
	if expiresAt.Valid {
		expires := time.Unix(expiresAt.Int64, 0).UTC()
		record.ExpiresAt = &expires
	}
	created, createdOK := unixTimestamp(createdAt)
	updated, updatedOK := unixTimestamp(updatedAt)
	if !createdOK || !updatedOK {
		return APIKeyRecord{}, ErrInvalidRecord
	}
	record.CreatedAt = created
	record.UpdatedAt = updated
	record.KeyHash = append([]byte(nil), keyHash...)
	record.Digest = append([]byte(nil), keyHash...)
	record.DisplayPrefix = record.Prefix
	// Keep the migration alias populated too, so every returned representation
	// has the same independent byte-slice ownership semantics.
	record.Prefix = record.DisplayPrefix
	if err := record.Validate(); err != nil {
		return APIKeyRecord{}, err
	}
	return record, nil
}

// List returns all records in stable ID order.
func (repository *APIKeyRepository) List(ctx context.Context) ([]APIKeyRecord, error) {
	if ctx == nil {
		return nil, errors.New("list api keys: nil context")
	}
	if repository == nil || repository.database == nil {
		return nil, ErrRepositoryUnavailable
	}
	rows, err := repository.database.QueryContext(ctx, `
		SELECT id, name, prefix, key_hash, enabled, expires_at, created_at, updated_at, policy_json
		FROM api_keys ORDER BY id ASC`)
	if err != nil {
		return nil, errors.New("list api keys: database read failed")
	}
	defer rows.Close()
	records := make([]APIKeyRecord, 0)
	for rows.Next() {
		record, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("list api keys: database read failed")
	}
	return records, nil
}

// ListAPIKeys returns safe metadata ordered newest-first. The query deliberately
// selects neither key_hash nor policy_json; policy booleans are extracted by
// SQLite into typed integer columns. A limit-sized-plus-one read determines
// whether a following page exists without a count query.
func (repository *APIKeyRepository) ListAPIKeys(ctx context.Context, limit int, cursor string) ([]KeyListRecord, string, error) {
	if ctx == nil {
		return nil, "", errors.New("list api keys: nil context")
	}
	if repository == nil || repository.database == nil || repository.cursorAEAD == nil {
		return nil, "", ErrRepositoryUnavailable
	}
	if limit <= 0 || limit > maxAPIKeyListLimit {
		return nil, "", fmt.Errorf("%w: limit", ErrInvalidCursor)
	}
	bookmark, err := repository.decodeListCursor(cursor)
	if err != nil {
		return nil, "", err
	}
	query := `SELECT id, name, prefix, enabled, expires_at, created_at, updated_at,
			CASE WHEN json_array_length(json_extract(policy_json, '$.allowed_models')) > 0 THEN 1 ELSE 0 END,
			CASE WHEN json_array_length(json_extract(policy_json, '$.denied_models')) > 0 THEN 1 ELSE 0 END,
			CASE WHEN json_extract(policy_json, '$.log_request_body') = 1 THEN 1 ELSE 0 END,
			CASE WHEN json_extract(policy_json, '$.log_response_body') = 1 THEN 1 ELSE 0 END
		FROM api_keys`
	args := []any{}
	if bookmark != nil {
		query += ` WHERE (created_at < ? OR (created_at = ? AND id < ?))`
		args = append(args, bookmark.CreatedAt, bookmark.CreatedAt, bookmark.ID)
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := repository.database.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", errors.New("list api keys: database read failed")
	}
	defer rows.Close()
	result := make([]KeyListRecord, 0, limit)
	for rows.Next() {
		var record KeyListRecord
		var enabled, allow, deny, logRequest, logResponse int64
		var expires sql.NullInt64
		var created, updated int64
		if err := rows.Scan(&record.ID, &record.Name, &record.DisplayPrefix, &enabled, &expires, &created, &updated, &allow, &deny, &logRequest, &logResponse); err != nil {
			return nil, "", errors.New("list api keys: database read failed")
		}
		if (enabled != 0 && enabled != 1) || created <= 0 || updated <= 0 || updated < created {
			return nil, "", ErrInvalidRecord
		}
		record.Enabled = enabled == 1
		record.CreatedAt = time.Unix(created, 0).UTC()
		record.UpdatedAt = time.Unix(updated, 0).UTC()
		if expires.Valid {
			value := time.Unix(expires.Int64, 0).UTC()
			record.ExpiresAt = &value
		}
		record.PolicySummary = KeyPolicySummary{AllowModels: allow == 1, DenyModels: deny == 1, LogRequestBody: logRequest == 1, LogResponseBody: logResponse == 1}
		result = append(result, record)
	}
	if err := rows.Err(); err != nil {
		return nil, "", errors.New("list api keys: database read failed")
	}
	if len(result) <= limit {
		return result, "", nil
	}
	last := result[limit-1]
	result = result[:limit]
	next, err := repository.encodeListCursor(apiKeyListCursor{CreatedAt: last.CreatedAt.Unix(), ID: last.ID})
	if err != nil {
		return nil, "", err
	}
	return result, next, nil
}

func (repository *APIKeyRepository) encodeListCursor(cursor apiKeyListCursor) (string, error) {
	if repository.cursorAEAD == nil || cursor.CreatedAt <= 0 || cursor.ID == "" {
		return "", ErrInvalidCursor
	}
	payload := make([]byte, 10+len(cursor.ID))
	binary.BigEndian.PutUint64(payload, uint64(cursor.CreatedAt))
	binary.BigEndian.PutUint16(payload[8:], uint16(len(cursor.ID)))
	copy(payload[10:], cursor.ID)
	nonce := make([]byte, repository.cursorAEAD.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", ErrInvalidCursor
	}
	sealed := repository.cursorAEAD.Seal(nonce, nonce, payload, nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (repository *APIKeyRepository) decodeListCursor(value string) (*apiKeyListCursor, error) {
	if value == "" {
		return nil, nil
	}
	if len(value) > maxAPIKeyCursorBytes || repository.cursorAEAD == nil {
		return nil, ErrInvalidCursor
	}
	sealed, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(sealed) <= repository.cursorAEAD.NonceSize() {
		return nil, ErrInvalidCursor
	}
	nonce := sealed[:repository.cursorAEAD.NonceSize()]
	payload, err := repository.cursorAEAD.Open(nil, nonce, sealed[repository.cursorAEAD.NonceSize():], nil)
	if err != nil || len(payload) < 10 {
		return nil, ErrInvalidCursor
	}
	idLength := int(binary.BigEndian.Uint16(payload[8:10]))
	if idLength == 0 || idLength != len(payload)-10 || idLength > 256 {
		return nil, ErrInvalidCursor
	}
	created := int64(binary.BigEndian.Uint64(payload[:8]))
	if created <= 0 {
		return nil, ErrInvalidCursor
	}
	return &apiKeyListCursor{CreatedAt: created, ID: string(payload[10:])}, nil
}

// SetEnabled changes the active state and refreshes the record timestamp.
func (repository *APIKeyRepository) SetEnabled(ctx context.Context, id string, enabled bool) error {
	if ctx == nil {
		return errors.New("update api key: nil context")
	}
	if strings.TrimSpace(id) == "" {
		return ErrInvalidRecord
	}
	if repository == nil || repository.database == nil {
		return ErrRepositoryUnavailable
	}
	result, err := repository.database.ExecContext(ctx,
		"UPDATE api_keys SET enabled = ?, updated_at = max(updated_at, created_at, ?) WHERE id = ?",
		boolInt(enabled), time.Now().UTC().Truncate(time.Second).Unix(), id)
	if err != nil {
		return errors.New("update api key: database write failed")
	}
	count, err := result.RowsAffected()
	if err != nil {
		return errors.New("update api key: database result unavailable")
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}

// UpdateEnabled is a descriptive spelling for SetEnabled.
func (repository *APIKeyRepository) UpdateEnabled(ctx context.Context, id string, enabled bool) error {
	return repository.SetEnabled(ctx, id, enabled)
}

// UpdatePolicy atomically replaces a key's enabled state and complete policy
// document. Policy validation belongs to the auth/admin boundary; this method
// only persists the already validated replacement as one SQLite update.
func (repository *APIKeyRepository) UpdatePolicy(ctx context.Context, id string, enabled bool, policyJSON string) error {
	if ctx == nil {
		return errors.New("update api key policy: nil context")
	}
	if strings.TrimSpace(id) == "" {
		return ErrInvalidRecord
	}
	if repository == nil || repository.database == nil {
		return ErrRepositoryUnavailable
	}
	result, err := repository.database.ExecContext(ctx,
		"UPDATE api_keys SET enabled = ?, policy_json = ?, updated_at = max(updated_at, created_at, ?) WHERE id = ?",
		boolInt(enabled), policyJSON, time.Now().UTC().Truncate(time.Second).Unix(), id)
	if err != nil {
		return errors.New("update api key policy: database write failed")
	}
	count, err := result.RowsAffected()
	if err != nil {
		return errors.New("update api key policy: database result unavailable")
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}

// UpdatePolicyRecord atomically replaces a key's enabled state and complete
// policy document and returns the record as persisted. This lets callers
// report SQLite's durable timestamp when the monotonic timestamp guard keeps
// an existing value.
func (repository *APIKeyRepository) UpdatePolicyRecord(ctx context.Context, id string, enabled bool, policyJSON string) (APIKeyRecord, error) {
	if ctx == nil {
		return APIKeyRecord{}, errors.New("update api key policy: nil context")
	}
	if strings.TrimSpace(id) == "" {
		return APIKeyRecord{}, ErrInvalidRecord
	}
	if repository == nil || repository.database == nil {
		return APIKeyRecord{}, ErrRepositoryUnavailable
	}
	// UPDATE ... RETURNING makes the durable mutation and the result one SQL
	// statement. In particular, cancellation cannot commit the UPDATE and then
	// cancel a separate GetByID, leaving callers with no safe publication result.
	updatedAt := time.Now().UTC().Truncate(time.Second).Unix()
	row := repository.database.QueryRowContext(ctx, `
		UPDATE api_keys
		SET enabled = ?, policy_json = ?, updated_at = max(updated_at, created_at, ?)
		WHERE id = ?
		RETURNING id, name, prefix, key_hash, enabled, expires_at, created_at, updated_at, policy_json`,
		boolInt(enabled), policyJSON, updatedAt, id)
	record, err := scanAPIKey(row)
	if errors.Is(err, ErrNotFound) {
		return APIKeyRecord{}, ErrNotFound
	}
	if err != nil {
		return APIKeyRecord{}, errors.New("update api key policy: database write failed")
	}
	return record, nil
}

// UpdateEnabledAndPolicy is a descriptive alias for UpdatePolicy.
func (repository *APIKeyRepository) UpdateEnabledAndPolicy(ctx context.Context, id string, enabled bool, policyJSON string) error {
	return repository.UpdatePolicy(ctx, id, enabled, policyJSON)
}

func scanAPIKey(scanner interface{ Scan(...any) error }) (APIKeyRecord, error) {
	var record APIKeyRecord
	var keyHash []byte
	var enabled int64
	var expiresAt sql.NullInt64
	var createdAt, updatedAt int64
	if err := scanner.Scan(&record.ID, &record.Name, &record.Prefix, &keyHash, &enabled, &expiresAt,
		&createdAt, &updatedAt, &record.PolicyJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return APIKeyRecord{}, ErrNotFound
		}
		return APIKeyRecord{}, errors.New("read api key: database row is invalid")
	}
	if enabled != 0 && enabled != 1 {
		return APIKeyRecord{}, ErrInvalidRecord
	}
	if expiresAt.Valid {
		expires := time.Unix(expiresAt.Int64, 0).UTC()
		record.ExpiresAt = &expires
	}
	created, createdOK := unixTimestamp(createdAt)
	updated, updatedOK := unixTimestamp(updatedAt)
	if !createdOK || !updatedOK {
		return APIKeyRecord{}, ErrInvalidRecord
	}
	record.CreatedAt = created
	record.UpdatedAt = updated
	record.Enabled = enabled == 1
	record.KeyHash = append([]byte(nil), keyHash...)
	record.Digest = append([]byte(nil), keyHash...)
	record.DisplayPrefix = record.Prefix
	if err := record.Validate(); err != nil {
		return APIKeyRecord{}, err
	}
	return record, nil
}

func unixTimestamp(value int64) (time.Time, bool) {
	converted := time.Unix(value, 0).UTC()
	if converted.Unix() != value || converted.IsZero() {
		return time.Time{}, false
	}
	return converted, true
}

func boolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func insertError(err error) error {
	if strings.Contains(strings.ToLower(err.Error()), "constraint") {
		return ErrConflict
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return errors.New("insert api key: database write failed")
}
