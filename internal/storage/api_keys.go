package storage

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
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
)

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
	database dbQueries
}

// NewAPIKeyRepository creates a repository over an opened storage database.
// The argument is accepted as the narrow query capability so SQLite-specific
// handles do not become part of the repository's domain API.
func NewAPIKeyRepository(database dbQueries) *APIKeyRepository {
	return &APIKeyRepository{database: database}
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
		"UPDATE api_keys SET enabled = ?, updated_at = ? WHERE id = ?",
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

func scanAPIKey(scanner interface{ Scan(...any) error }) (APIKeyRecord, error) {
	var record APIKeyRecord
	var keyHash []byte
	var enabled int64
	var expiresAt sql.NullInt64
	var createdAt, updatedAt int64
	if err := scanner.Scan(&record.ID, &record.Name, &record.Prefix, &keyHash, &enabled, &expiresAt,
		&createdAt, &updatedAt, &record.PolicyJSON); err != nil {
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
