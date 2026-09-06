package storage

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"
)

// UsageBucketDelta is a committed token change for one exact limiter bucket.
// It contains a stable key ID only; credentials never enter this repository.
type UsageBucketDelta struct {
	APIKeyID       string
	BucketStart    time.Time
	BucketSeconds  int64
	BucketAmount   int64
	CommittedDelta int64
}

// UsageBucket is a persisted committed token aggregate. Active reservations
// are process-local and are deliberately not represented here.
type UsageBucket struct {
	APIKeyID        string
	BucketStart     time.Time
	BucketSeconds   int64
	BucketAmount    int64
	CommittedTokens int64
}

var (
	ErrInvalidUsageBucket     = errors.New("invalid usage bucket")
	ErrUsageBucketUnavailable = errors.New("usage bucket repository unavailable")
	ErrUsageBucketUnderflow   = errors.New("usage bucket underflow")
)

type txBeginner interface {
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

// UsageBucketRepository is the narrow SQLite persistence boundary for token
// aggregates. It is intentionally separate from key and policy repositories.
type UsageBucketRepository struct {
	database dbQueries
	beginner txBeginner
}

func NewUsageBucketRepository(database dbQueries) *UsageBucketRepository {
	repository := &UsageBucketRepository{database: database}
	if beginner, ok := database.(txBeginner); ok {
		repository.beginner = beginner
	}
	return repository
}

// UpsertCommittedDeltas atomically applies a batch. SQLite CHECK constraints
// and the guarded update make negative adjustments underflow-safe; a failed
// row rolls back the complete batch.
func (repository *UsageBucketRepository) UpsertCommittedDeltas(ctx context.Context, deltas []UsageBucketDelta) error {
	if ctx == nil {
		return errors.New("upsert usage buckets: nil context")
	}
	if repository == nil || repository.database == nil || repository.beginner == nil {
		return ErrUsageBucketUnavailable
	}
	valid := make([]UsageBucketDelta, 0, len(deltas))
	for _, delta := range deltas {
		if delta.CommittedDelta == 0 {
			continue
		}
		if delta.CommittedDelta == math.MinInt64 {
			return ErrUsageBucketUnderflow
		}
		if err := validateUsageBucketIdentity(delta.APIKeyID, delta.BucketStart, delta.BucketSeconds); err != nil {
			return err
		}
		if delta.BucketAmount <= 0 {
			return ErrInvalidUsageBucket
		}
		valid = append(valid, delta)
	}
	if len(valid) == 0 {
		return nil
	}
	tx, err := repository.beginner.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("upsert usage buckets: begin failed")
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()
	now := time.Now().UTC().Unix()
	for _, delta := range valid {
		var result sql.Result
		var execErr error
		if delta.CommittedDelta < 0 {
			// Negative deltas never use INSERT: SQLite CHECK constraints must
			// not turn an adjustment into a newly-created negative row.
			result, execErr = tx.ExecContext(ctx, `
				UPDATE usage_bucket_identities
				SET committed_tokens = committed_tokens + ?, updated_at = ?
				WHERE api_key_id = ? AND bucket_start = ? AND bucket_seconds = ? AND bucket_amount = ?
				  AND committed_tokens >= ?`,
				delta.CommittedDelta, now, delta.APIKeyID, delta.BucketStart.Unix(), delta.BucketSeconds, delta.BucketAmount, -delta.CommittedDelta)
		} else {
			// Positive deltas create the row and atomically add to an existing
			// row in the same transaction.
			result, execErr = tx.ExecContext(ctx, `
			INSERT INTO usage_bucket_identities
				(api_key_id, bucket_start, bucket_seconds, bucket_amount, committed_tokens, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(api_key_id, bucket_start, bucket_seconds, bucket_amount) DO UPDATE SET
				committed_tokens = usage_bucket_identities.committed_tokens + excluded.committed_tokens,
				updated_at = excluded.updated_at`,
				delta.APIKeyID, delta.BucketStart.Unix(), delta.BucketSeconds, delta.BucketAmount,
				delta.CommittedDelta, now, now)
		}
		if execErr != nil {
			if delta.CommittedDelta < 0 {
				return ErrUsageBucketUnderflow
			}
			return errors.New("upsert usage buckets: write failed")
		}
		if delta.CommittedDelta < 0 {
			rows, rowsErr := result.RowsAffected()
			if rowsErr != nil || rows != 1 {
				return ErrUsageBucketUnderflow
			}
		}
		// Keep the T098 shape current as a conservative compatibility
		// checkpoint. The identity table above is authoritative when a window
		// amount matters; this row lets older tooling and databases reopen.
		if delta.CommittedDelta < 0 {
			result, execErr = tx.ExecContext(ctx, `
				UPDATE usage_buckets SET committed_tokens = committed_tokens + ?, updated_at = ?
				WHERE api_key_id = ? AND bucket_start = ? AND bucket_seconds = ?
				  AND committed_tokens >= ?`, delta.CommittedDelta, now, delta.APIKeyID, delta.BucketStart.Unix(), delta.BucketSeconds, -delta.CommittedDelta)
			if execErr != nil {
				return ErrUsageBucketUnderflow
			}
			rows, rowsErr := result.RowsAffected()
			if rowsErr != nil || rows != 1 {
				return ErrUsageBucketUnderflow
			}
		} else if _, execErr = tx.ExecContext(ctx, `
			INSERT INTO usage_buckets (api_key_id, bucket_start, bucket_seconds, committed_tokens, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(api_key_id, bucket_start, bucket_seconds) DO UPDATE SET
				committed_tokens = usage_buckets.committed_tokens + excluded.committed_tokens,
				updated_at = excluded.updated_at`, delta.APIKeyID, delta.BucketStart.Unix(), delta.BucketSeconds, delta.CommittedDelta, now, now); execErr != nil {
			return errors.New("upsert usage buckets: compatibility write failed")
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM usage_bucket_identities WHERE committed_tokens = 0`); err != nil {
		return errors.New("upsert usage buckets: cleanup failed")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM usage_buckets WHERE committed_tokens = 0`); err != nil {
		return errors.New("upsert usage buckets: compatibility cleanup failed")
	}
	if err := tx.Commit(); err != nil {
		return errors.New("upsert usage buckets: commit failed")
	}
	rollback = false
	return nil
}

// UpsertCommittedDelta is the single-delta spelling retained for small
// embedders; production callers should batch through UpsertCommittedDeltas.
func (repository *UsageBucketRepository) UpsertCommittedDelta(ctx context.Context, delta UsageBucketDelta) error {
	return repository.UpsertCommittedDeltas(ctx, []UsageBucketDelta{delta})
}

// LoadUnexpired returns only current buckets in deterministic identity order.
func (repository *UsageBucketRepository) LoadUnexpired(ctx context.Context, now time.Time) ([]UsageBucket, error) {
	if ctx == nil {
		return nil, errors.New("load usage buckets: nil context")
	}
	if repository == nil || repository.database == nil {
		return nil, ErrUsageBucketUnavailable
	}
	rows, err := repository.database.QueryContext(ctx, `
		SELECT api_key_id, bucket_start, bucket_seconds, bucket_amount, committed_tokens
		FROM usage_bucket_identities
		WHERE bucket_start + bucket_seconds > ?
		ORDER BY api_key_id ASC, bucket_start ASC, bucket_seconds ASC, bucket_amount ASC`, now.UTC().Unix())
	if err != nil {
		return nil, errors.New("load usage buckets: query failed")
	}
	defer rows.Close()
	result := make([]UsageBucket, 0)
	for rows.Next() {
		var bucket UsageBucket
		var start, seconds int64
		if err := rows.Scan(&bucket.APIKeyID, &start, &seconds, &bucket.BucketAmount, &bucket.CommittedTokens); err != nil {
			return nil, errors.New("load usage buckets: row failed")
		}
		bucket.BucketStart = time.Unix(start, 0).UTC()
		bucket.BucketSeconds = seconds
		if err := validateUsageBucketIdentity(bucket.APIKeyID, bucket.BucketStart, bucket.BucketSeconds); err != nil || bucket.BucketAmount <= 0 || bucket.CommittedTokens < 0 {
			return nil, ErrInvalidUsageBucket
		}
		result = append(result, bucket)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("load usage buckets: rows failed")
	}
	return result, nil
}

// Load is a concise alias for LoadUnexpired.
func (repository *UsageBucketRepository) Load(ctx context.Context, now time.Time) ([]UsageBucket, error) {
	return repository.LoadUnexpired(ctx, now)
}

// LoadLegacyUnexpired reads v2 rows that intentionally lack bucket amount.
// Callers must resolve each row against the current strict policy before it is
// promoted to the identity table.
func (repository *UsageBucketRepository) LoadLegacyUnexpired(ctx context.Context, now time.Time) ([]UsageBucket, error) {
	if ctx == nil {
		return nil, errors.New("load legacy usage buckets: nil context")
	}
	if repository == nil || repository.database == nil {
		return nil, ErrUsageBucketUnavailable
	}
	rows, err := repository.database.QueryContext(ctx, `SELECT api_key_id, bucket_start, bucket_seconds, committed_tokens FROM usage_buckets WHERE bucket_start + bucket_seconds > ?`, now.UTC().Unix())
	if err != nil {
		return nil, errors.New("load legacy usage buckets: query failed")
	}
	defer rows.Close()
	var result []UsageBucket
	for rows.Next() {
		var bucket UsageBucket
		var start, seconds int64
		if err := rows.Scan(&bucket.APIKeyID, &start, &seconds, &bucket.CommittedTokens); err != nil {
			return nil, ErrInvalidUsageBucket
		}
		bucket.BucketStart, bucket.BucketSeconds = time.Unix(start, 0).UTC(), seconds
		if validateUsageBucketIdentity(bucket.APIKeyID, bucket.BucketStart, seconds) != nil || bucket.CommittedTokens < 0 {
			return nil, ErrInvalidUsageBucket
		}
		result = append(result, bucket)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("load legacy usage buckets: rows failed")
	}
	return result, nil
}

// PromoteLegacy atomically records a uniquely-resolved v2 bucket identity and
// removes its source row. It is deliberately not exposed as a generic write.
func (repository *UsageBucketRepository) PromoteLegacy(ctx context.Context, bucket UsageBucket, amount int64) error {
	if ctx == nil || repository == nil || repository.database == nil || repository.beginner == nil {
		return ErrUsageBucketUnavailable
	}
	if amount <= 0 || validateUsageBucketIdentity(bucket.APIKeyID, bucket.BucketStart, bucket.BucketSeconds) != nil {
		return ErrInvalidUsageBucket
	}
	tx, err := repository.beginner.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("promote legacy usage bucket: begin failed")
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()
	now := time.Now().UTC().Unix()
	if _, err = tx.ExecContext(ctx, `INSERT INTO usage_bucket_identities (api_key_id,bucket_start,bucket_seconds,bucket_amount,committed_tokens,created_at,updated_at) VALUES (?,?,?,?,?,?,?) ON CONFLICT(api_key_id,bucket_start,bucket_seconds,bucket_amount) DO UPDATE SET committed_tokens=committed_tokens+excluded.committed_tokens, updated_at=excluded.updated_at`, bucket.APIKeyID, bucket.BucketStart.Unix(), bucket.BucketSeconds, amount, bucket.CommittedTokens, now, now); err != nil {
		return errors.New("promote legacy usage bucket: identity write failed")
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM usage_buckets WHERE api_key_id=? AND bucket_start=? AND bucket_seconds=?`, bucket.APIKeyID, bucket.BucketStart.Unix(), bucket.BucketSeconds); err != nil {
		return errors.New("promote legacy usage bucket: source delete failed")
	}
	if err = tx.Commit(); err != nil {
		return errors.New("promote legacy usage bucket: commit failed")
	}
	rollback = false
	return nil
}

func (repository *UsageBucketRepository) DeleteExpired(ctx context.Context, now time.Time) error {
	if ctx == nil {
		return errors.New("delete expired usage buckets: nil context")
	}
	if repository == nil || repository.database == nil {
		return ErrUsageBucketUnavailable
	}
	if repository.beginner == nil {
		return ErrUsageBucketUnavailable
	}
	tx, err := repository.beginner.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("delete expired usage buckets: begin failed")
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `DELETE FROM usage_bucket_identities WHERE bucket_start + bucket_seconds <= ?`, now.UTC().Unix()); err != nil {
		return errors.New("delete expired usage buckets: delete failed")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM usage_buckets WHERE bucket_start + bucket_seconds <= ?`, now.UTC().Unix()); err != nil {
		return errors.New("delete expired usage buckets: compatibility delete failed")
	}
	if err := tx.Commit(); err != nil {
		return errors.New("delete expired usage buckets: commit failed")
	}
	rollback = false
	return nil
}

func validateUsageBucketIdentity(keyID string, start time.Time, seconds int64) error {
	if keyID == "" || start.Nanosecond() != 0 || start.Unix() < 0 || seconds <= 0 || seconds > math.MaxInt64/int64(time.Second) {
		return ErrInvalidUsageBucket
	}
	if start.Unix()%seconds != 0 {
		return ErrInvalidUsageBucket
	}
	return nil
}
