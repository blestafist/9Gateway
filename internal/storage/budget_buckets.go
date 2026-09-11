package storage

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"
)

// BudgetBucketDelta is a signed change to one lifetime (total) budget bucket.
// Lifetime buckets are identified by stable API-key ID; credentials and policy
// data never cross this persistence boundary.
type BudgetBucketDelta struct {
	APIKeyID   string
	SpentDelta int64
}

// BudgetBucket is the durable committed portion of a lifetime budget. Active
// reservations are process-local and are deliberately not persisted.
type BudgetBucket struct {
	APIKeyID    string
	SpentMicros int64
}

var (
	ErrInvalidBudgetBucket     = errors.New("invalid budget bucket")
	ErrBudgetBucketUnavailable = errors.New("budget bucket repository unavailable")
	ErrBudgetBucketUnderflow   = errors.New("budget bucket underflow")
	ErrBudgetBucketOverflow    = errors.New("budget bucket overflow")
	ErrBudgetBucketUnknownKey  = errors.New("budget bucket key unavailable")
)

// BudgetBucketRepository is the narrow SQLite boundary for lifetime spend.
//
// Provenance review: Bifrost commit 03ab391865710462302bbcf52dca2f32682b91b5
// (branch dev), .references/bifrost/plugins/governance/store.go (CheckBudget,
// BumpBudgetUsage), .references/bifrost/plugins/governance/tracker.go, and
// .references/bifrost/framework/configstore/tables/budget.go were inspected.
// The reference is Apache-2.0 under .references/bifrost/LICENSE, with its
// notices in .references/bifrost/THIRD_PARTY_NOTICES.md. No Bifrost source,
// data, architecture, or dependency is copied or adapted here.
type BudgetBucketRepository struct {
	database dbQueries
	beginner txBeginner
}

// BudgetRepository is the concise repository name used by embedders.
type BudgetRepository = BudgetBucketRepository

func NewBudgetBucketRepository(database dbQueries) *BudgetBucketRepository {
	repository := &BudgetBucketRepository{database: database}
	if beginner, ok := database.(txBeginner); ok {
		repository.beginner = beginner
	}
	return repository
}

func NewBudgetRepository(database dbQueries) *BudgetRepository {
	return NewBudgetBucketRepository(database)
}

// ApplyDeltas atomically applies all nonzero deltas. A negative delta can only
// update an existing row with enough spend; it can never create debt. Any
// failure rolls back every row in the batch.
func (repository *BudgetBucketRepository) ApplyDeltas(ctx context.Context, deltas []BudgetBucketDelta) error {
	if ctx == nil {
		return errors.New("apply budget buckets: nil context")
	}
	if repository == nil || repository.database == nil || repository.beginner == nil {
		return ErrBudgetBucketUnavailable
	}
	valid := make([]BudgetBucketDelta, 0, len(deltas))
	for _, delta := range deltas {
		if delta.SpentDelta == 0 {
			continue
		}
		if delta.APIKeyID == "" {
			return ErrInvalidBudgetBucket
		}
		if delta.SpentDelta == math.MinInt64 {
			return ErrBudgetBucketUnderflow
		}
		valid = append(valid, delta)
	}
	if len(valid) == 0 {
		return nil
	}
	tx, err := repository.beginner.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("apply budget buckets: begin failed")
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()

	// The timestamp is operational metadata only. It is intentionally not
	// surfaced in errors or used as part of the bucket identity.
	const totalStart int64 = 0
	now := time.Now().UTC().Unix()
	knownKeys := make(map[string]struct{}, len(valid))
	for _, delta := range valid {
		if _, checked := knownKeys[delta.APIKeyID]; !checked {
			var present int
			if err := tx.QueryRowContext(ctx, `SELECT 1 FROM api_keys WHERE id = ?`, delta.APIKeyID).Scan(&present); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return ErrBudgetBucketUnknownKey
				}
				return errors.New("apply budget buckets: key check failed")
			}
			knownKeys[delta.APIKeyID] = struct{}{}
		}
		if delta.SpentDelta < 0 {
			result, execErr := tx.ExecContext(ctx, `
				UPDATE budget_buckets
				SET spent_micros = spent_micros + ?, updated_at = ?
				WHERE api_key_id = ? AND period_kind = 'total' AND period_start = 0
				  AND spent_micros >= ?`,
				delta.SpentDelta, now, delta.APIKeyID, -delta.SpentDelta)
			if execErr != nil {
				return errors.New("apply budget buckets: write failed")
			}
			rows, rowsErr := result.RowsAffected()
			if rowsErr != nil || rows != 1 {
				var present int
				if queryErr := tx.QueryRowContext(ctx, `SELECT 1 FROM budget_buckets WHERE api_key_id = ? AND period_kind = 'total' AND period_start = 0`, delta.APIKeyID).Scan(&present); errors.Is(queryErr, sql.ErrNoRows) {
					return ErrBudgetBucketUnknownKey
				}
				return ErrBudgetBucketUnderflow
			}
			continue
		}

		result, execErr := tx.ExecContext(ctx, `
			INSERT INTO budget_buckets
				(api_key_id, period_kind, period_start, spent_micros, created_at, updated_at)
			VALUES (?, 'total', ?, ?, ?, ?)
			ON CONFLICT(api_key_id, period_kind, period_start) DO UPDATE SET
				spent_micros = budget_buckets.spent_micros + excluded.spent_micros,
				updated_at = excluded.updated_at`,
			delta.APIKeyID, totalStart, delta.SpentDelta, now, now)
		if execErr != nil {
			return ErrBudgetBucketOverflow
		}
		if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
			return ErrBudgetBucketOverflow
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM budget_buckets WHERE period_kind = 'total' AND period_start = 0 AND spent_micros = 0`); err != nil {
		return errors.New("apply budget buckets: cleanup failed")
	}
	if err := tx.Commit(); err != nil {
		return errors.New("apply budget buckets: commit failed")
	}
	rollback = false
	return nil
}

func (repository *BudgetBucketRepository) ApplyDelta(ctx context.Context, delta BudgetBucketDelta) error {
	return repository.ApplyDeltas(ctx, []BudgetBucketDelta{delta})
}

func (repository *BudgetBucketRepository) UpsertSpentDelta(ctx context.Context, delta BudgetBucketDelta) error {
	return repository.ApplyDelta(ctx, delta)
}

// UpsertSpentDeltas is a descriptive alias for ApplyDeltas.
func (repository *BudgetBucketRepository) UpsertSpentDeltas(ctx context.Context, deltas []BudgetBucketDelta) error {
	return repository.ApplyDeltas(ctx, deltas)
}

// LoadTotal returns total rows in stable API-key order. Rows for future budget
// periods are not interpreted by this T116 repository.
func (repository *BudgetBucketRepository) LoadTotal(ctx context.Context) ([]BudgetBucket, error) {
	if ctx == nil {
		return nil, errors.New("load budget buckets: nil context")
	}
	if repository == nil || repository.database == nil {
		return nil, ErrBudgetBucketUnavailable
	}
	rows, err := repository.database.QueryContext(ctx, `
		SELECT budget_buckets.api_key_id, budget_buckets.spent_micros,
		       CASE WHEN api_keys.id IS NULL THEN 0 ELSE 1 END
		FROM budget_buckets
		LEFT JOIN api_keys ON api_keys.id = budget_buckets.api_key_id
		WHERE budget_buckets.period_kind = 'total' AND budget_buckets.period_start = 0
		ORDER BY budget_buckets.api_key_id ASC`)
	if err != nil {
		return nil, errors.New("load budget buckets: query failed")
	}
	defer rows.Close()
	result := make([]BudgetBucket, 0)
	for rows.Next() {
		var bucket BudgetBucket
		var knownKey int
		if err := rows.Scan(&bucket.APIKeyID, &bucket.SpentMicros, &knownKey); err != nil {
			return nil, ErrInvalidBudgetBucket
		}
		if bucket.APIKeyID == "" || bucket.SpentMicros < 0 || knownKey != 1 {
			return nil, ErrInvalidBudgetBucket
		}
		result = append(result, bucket)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("load budget buckets: rows failed")
	}
	return result, nil
}

func (repository *BudgetBucketRepository) Load(ctx context.Context) ([]BudgetBucket, error) {
	return repository.LoadTotal(ctx)
}

func (repository *BudgetBucketRepository) LoadSpent(ctx context.Context) ([]BudgetBucket, error) {
	return repository.LoadTotal(ctx)
}

func (repository *BudgetBucketRepository) LoadTotalSpent(ctx context.Context) ([]BudgetBucket, error) {
	return repository.LoadTotal(ctx)
}
