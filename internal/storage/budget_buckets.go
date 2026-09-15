package storage

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/pestit/9gateway/internal/limiter"
)

// BudgetBucketDelta is a signed change to one lifetime (total) budget bucket.
// Lifetime buckets are identified by stable API-key ID; credentials and policy
// data never cross this persistence boundary.
type BudgetBucketDelta struct {
	APIKeyID    string
	SpentDelta  int64
	Period      limiter.BudgetPeriod
	PeriodStart time.Time
}

// BudgetBucket is the durable committed portion of a lifetime budget. Active
// reservations are process-local and are deliberately not persisted.
type BudgetBucket struct {
	APIKeyID    string
	SpentMicros int64
	Period      limiter.BudgetPeriod
	PeriodStart time.Time
}

var (
	ErrInvalidBudgetBucket     = errors.New("invalid budget bucket")
	ErrBudgetBucketUnavailable = errors.New("budget bucket repository unavailable")
	ErrBudgetBucketUnderflow   = errors.New("budget bucket underflow")
	ErrBudgetBucketOverflow    = errors.New("budget bucket overflow")
	ErrBudgetBucketUnknownKey  = errors.New("budget bucket key unavailable")
)

const maxBudgetUnixTimestamp int64 = 253402300799

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
		if delta.Period == "" {
			delta.Period = limiter.BudgetPeriodTotal
		}
		// Total buckets have one in-memory identity: the zero time. SQLite
		// stores that identity as Unix epoch (0), but accepting epoch here as a
		// second representation would make equal buckets distinguishable to
		// accumulator callers and hide malformed input.
		if delta.Period == limiter.BudgetPeriodTotal && !delta.PeriodStart.IsZero() {
			return ErrInvalidBudgetBucket
		}
		if delta.Period != limiter.BudgetPeriodTotal && (delta.Period == limiter.BudgetPeriodDay && !validDayStart(delta.PeriodStart) || delta.Period == limiter.BudgetPeriodMonth && !validMonthStart(delta.PeriodStart) || delta.Period != limiter.BudgetPeriodDay && delta.Period != limiter.BudgetPeriodMonth) {
			return ErrInvalidBudgetBucket
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
	unlock := lockStorageWrite(repository.database)
	defer unlock()
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
				WHERE api_key_id = ? AND period_kind = ? AND period_start = ?
				  AND spent_micros >= ?`,
				delta.SpentDelta, now, delta.APIKeyID, delta.Period, bucketStart(delta), -delta.SpentDelta)
			if execErr != nil {
				return errors.New("apply budget buckets: write failed")
			}
			rows, rowsErr := result.RowsAffected()
			if rowsErr != nil || rows != 1 {
				var present int
				if queryErr := tx.QueryRowContext(ctx, `SELECT 1 FROM budget_buckets WHERE api_key_id = ? AND period_kind = ? AND period_start = ?`, delta.APIKeyID, delta.Period, bucketStart(delta)).Scan(&present); errors.Is(queryErr, sql.ErrNoRows) {
					return ErrBudgetBucketUnknownKey
				}
				return ErrBudgetBucketUnderflow
			}
			continue
		}

		result, execErr := tx.ExecContext(ctx, `
			INSERT INTO budget_buckets
				(api_key_id, period_kind, period_start, spent_micros, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(api_key_id, period_kind, period_start) DO UPDATE SET
				spent_micros = budget_buckets.spent_micros + excluded.spent_micros,
				updated_at = excluded.updated_at`,
			delta.APIKeyID, delta.Period, bucketStart(delta), delta.SpentDelta, now, now)
		if execErr != nil {
			return ErrBudgetBucketOverflow
		}
		if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
			return ErrBudgetBucketOverflow
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM budget_buckets WHERE spent_micros = 0`); err != nil {
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
	if err := repository.validateAll(ctx); err != nil {
		return nil, err
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
		bucket.Period = limiter.BudgetPeriodTotal
		bucket.PeriodStart = time.Time{}
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

func currentDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
func validDayStart(t time.Time) bool { return !t.IsZero() && t.Equal(currentDay(t)) }
func bucketStart(delta BudgetBucketDelta) int64 {
	if delta.Period == limiter.BudgetPeriodDay || delta.Period == limiter.BudgetPeriodMonth {
		return delta.PeriodStart.UTC().Unix()
	}
	return 0
}

// LoadDay restores one current UTC calendar-day bucket per key.
func (repository *BudgetBucketRepository) LoadDay(ctx context.Context, now time.Time) ([]BudgetBucket, error) {
	if ctx == nil || repository == nil || repository.database == nil {
		return nil, ErrBudgetBucketUnavailable
	}
	if err := repository.validateAll(ctx); err != nil {
		return nil, err
	}
	start := currentDay(now)
	rows, err := repository.database.QueryContext(ctx, `SELECT budget_buckets.api_key_id, budget_buckets.spent_micros, CASE WHEN api_keys.id IS NULL THEN 0 ELSE 1 END FROM budget_buckets LEFT JOIN api_keys ON api_keys.id=budget_buckets.api_key_id WHERE period_kind='day' AND period_start=? ORDER BY budget_buckets.api_key_id`, start.Unix())
	if err != nil {
		return nil, ErrBudgetBucketUnavailable
	}
	defer rows.Close()
	result := []BudgetBucket{}
	for rows.Next() {
		var b BudgetBucket
		var known int
		if err := rows.Scan(&b.APIKeyID, &b.SpentMicros, &known); err != nil || b.APIKeyID == "" || b.SpentMicros < 0 || known != 1 {
			return nil, ErrInvalidBudgetBucket
		}
		b.Period = limiter.BudgetPeriodDay
		b.PeriodStart = start
		result = append(result, b)
	}
	return result, rows.Err()
}
func (repository *BudgetBucketRepository) DeleteExpiredDays(ctx context.Context, before time.Time) error {
	if ctx == nil || repository == nil || repository.database == nil {
		return ErrBudgetBucketUnavailable
	}
	if err := repository.validateAll(ctx); err != nil {
		return err
	}
	_, err := repository.database.ExecContext(ctx, `DELETE FROM budget_buckets WHERE period_kind='day' AND period_start < ?`, currentDay(before).Unix())
	return err
}

func currentMonth(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}
func validMonthStart(t time.Time) bool { return !t.IsZero() && t.Equal(currentMonth(t)) }

// validateAll checks every durable row before any loader narrows the result to
// a current period. Filtering first would allow a malformed expired row (or a
// row for an unknown key) to disappear during startup and leave its debt
// unaccounted for if cleanup or a future clock change later exposed it.
func (repository *BudgetBucketRepository) validateAll(ctx context.Context) error {
	if ctx == nil {
		return errors.New("validate budget buckets: nil context")
	}
	rows, err := repository.database.QueryContext(ctx, `
		SELECT b.api_key_id, b.period_kind, b.period_start, b.spent_micros,
		       b.created_at, b.updated_at,
		       CASE WHEN k.id IS NULL THEN 0 ELSE 1 END,
		       typeof(b.api_key_id), typeof(b.period_kind), typeof(b.period_start),
		       typeof(b.spent_micros), typeof(b.created_at), typeof(b.updated_at)
		FROM budget_buckets AS b
		LEFT JOIN api_keys AS k ON k.id = b.api_key_id
		ORDER BY b.api_key_id, b.period_kind, b.period_start`)
	if err != nil {
		return errors.New("validate budget buckets: query failed")
	}
	defer rows.Close()
	seen := make(map[string]struct{})
	for rows.Next() {
		var apiKeyID, period string
		var start, spent, created, updated int64
		var known int
		var apiKeyType, periodType, startType, spentType, createdType, updatedType string
		if err := rows.Scan(&apiKeyID, &period, &start, &spent, &created, &updated, &known,
			&apiKeyType, &periodType, &startType, &spentType, &createdType, &updatedType); err != nil {
			return ErrInvalidBudgetBucket
		}
		if apiKeyType != "text" || periodType != "text" || startType != "integer" ||
			spentType != "integer" || createdType != "integer" || updatedType != "integer" ||
			strings.TrimSpace(apiKeyID) == "" || known != 1 || spent < 0 || created < 0 ||
			updated < created || created > maxBudgetUnixTimestamp || updated > maxBudgetUnixTimestamp ||
			start < 0 || start > maxBudgetUnixTimestamp {
			return ErrInvalidBudgetBucket
		}
		var validStart bool
		switch limiter.BudgetPeriod(period) {
		case limiter.BudgetPeriodTotal:
			validStart = start == 0
		case limiter.BudgetPeriodDay:
			value := time.Unix(start, 0).UTC()
			validStart = value.Unix() == start && validDayStart(value)
		case limiter.BudgetPeriodMonth:
			value := time.Unix(start, 0).UTC()
			validStart = value.Unix() == start && validMonthStart(value)
		default:
			return ErrInvalidBudgetBucket
		}
		if !validStart {
			return ErrInvalidBudgetBucket
		}
		identity := apiKeyID + "\x00" + period + "\x00" + strconv.FormatInt(start, 10)
		if _, duplicate := seen[identity]; duplicate {
			return ErrInvalidBudgetBucket
		}
		seen[identity] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return errors.New("validate budget buckets: rows failed")
	}
	return nil
}

// LoadMonth restores only the current UTC calendar-month bucket.
func (repository *BudgetBucketRepository) LoadMonth(ctx context.Context, now time.Time) ([]BudgetBucket, error) {
	if ctx == nil || repository == nil || repository.database == nil {
		return nil, ErrBudgetBucketUnavailable
	}
	if err := repository.validateAll(ctx); err != nil {
		return nil, err
	}
	start := currentMonth(now)
	rows, err := repository.database.QueryContext(ctx, `SELECT budget_buckets.api_key_id, budget_buckets.spent_micros, CASE WHEN api_keys.id IS NULL THEN 0 ELSE 1 END FROM budget_buckets LEFT JOIN api_keys ON api_keys.id=budget_buckets.api_key_id WHERE period_kind='month' AND period_start=? ORDER BY budget_buckets.api_key_id`, start.Unix())
	if err != nil {
		return nil, ErrBudgetBucketUnavailable
	}
	defer rows.Close()
	result := []BudgetBucket{}
	for rows.Next() {
		var b BudgetBucket
		var known int
		if err := rows.Scan(&b.APIKeyID, &b.SpentMicros, &known); err != nil || b.APIKeyID == "" || b.SpentMicros < 0 || known != 1 {
			return nil, ErrInvalidBudgetBucket
		}
		b.Period = limiter.BudgetPeriodMonth
		b.PeriodStart = start
		result = append(result, b)
	}
	return result, rows.Err()
}
func (repository *BudgetBucketRepository) DeleteExpiredMonths(ctx context.Context, before time.Time) error {
	if ctx == nil || repository == nil || repository.database == nil {
		return ErrBudgetBucketUnavailable
	}
	if err := repository.validateAll(ctx); err != nil {
		return err
	}
	_, err := repository.database.ExecContext(ctx, `DELETE FROM budget_buckets WHERE period_kind='month' AND period_start < ?`, currentMonth(before).Unix())
	return err
}
