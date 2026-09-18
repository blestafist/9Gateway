package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// OverviewAggregate contains aggregated statistics for a specific time window.
// Unknown tokens/cost are preserved as nil pointers (SQL NULL).
type OverviewAggregate struct {
	TotalRequests      int64  `json:"total_requests"`
	SuccessfulRequests int64  `json:"successful_requests"`
	ErrorRequests      int64  `json:"error_requests"`
	RejectedRequests   int64  `json:"rejected_requests"`
	InputTokens        *int64 `json:"input_tokens"`
	CachedInputTokens  *int64 `json:"cached_input_tokens"`
	OutputTokens       *int64 `json:"output_tokens"`
	CostMicros         *int64 `json:"cost_micros"`
}

// OverviewKeyCounts contains total and currently enabled API key counts.
type OverviewKeyCounts struct {
	Total   int64 `json:"total"`
	Enabled int64 `json:"enabled"`
}

// OverviewData contains the full completed overview data returned by storage.
type OverviewData struct {
	CurrentRangeStart  time.Time           `json:"current_range_start"`
	CurrentRangeEnd    time.Time           `json:"current_range_end"`
	PreviousRangeStart time.Time           `json:"previous_range_start"`
	PreviousRangeEnd   time.Time           `json:"previous_range_end"`
	DataTimestamp      time.Time           `json:"data_timestamp"`
	Current            OverviewAggregate   `json:"current"`
	Previous           OverviewAggregate   `json:"previous"`
	ActiveRequests     int64               `json:"active_requests"`
	KeyCounts          OverviewKeyCounts   `json:"key_counts"`
	RecentRequests     []RequestListRecord `json:"recent_requests"`
}

// OverviewRepository defines the overview aggregation operations needed by the admin overview API.
type OverviewRepository interface {
	GetOverview(ctx context.Context, after, before time.Time, activeRequests int64) (*OverviewData, error)
}

func aggregateRequests(ctx context.Context, db dbQueries, afterMicros, beforeMicros int64) (OverviewAggregate, error) {
	query := `
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN terminal_outcome IN ('complete', 'custom_dispatch') THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN terminal_outcome IS NULL OR terminal_outcome NOT IN ('complete', 'custom_dispatch', 'pre_upstream') THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN terminal_outcome = 'pre_upstream' THEN 1 ELSE 0 END), 0),
			CASE WHEN COUNT(input_tokens) < COUNT(*) THEN NULL ELSE SUM(input_tokens) END,
			CASE WHEN COUNT(cached_input_tokens) < COUNT(*) THEN NULL ELSE SUM(cached_input_tokens) END,
			CASE WHEN COUNT(output_tokens) < COUNT(*) THEN NULL ELSE SUM(output_tokens) END,
			CASE WHEN COUNT(cost_micros) < COUNT(*) THEN NULL ELSE SUM(cost_micros) END
		FROM requests
		WHERE finished_at >= ? AND finished_at <= ?
	`
	var (
		agg        OverviewAggregate
		inTokNull  sql.NullInt64
		cacheNull  sql.NullInt64
		outTokNull sql.NullInt64
		costNull   sql.NullInt64
	)
	row := db.QueryRowContext(ctx, query, afterMicros, beforeMicros)
	if err := row.Scan(
		&agg.TotalRequests,
		&agg.SuccessfulRequests,
		&agg.ErrorRequests,
		&agg.RejectedRequests,
		&inTokNull,
		&cacheNull,
		&outTokNull,
		&costNull,
	); err != nil {
		return OverviewAggregate{}, err
	}

	if inTokNull.Valid {
		v := inTokNull.Int64
		agg.InputTokens = &v
	}
	if cacheNull.Valid {
		v := cacheNull.Int64
		agg.CachedInputTokens = &v
	}
	if outTokNull.Valid {
		v := outTokNull.Int64
		agg.OutputTokens = &v
	}
	if costNull.Valid {
		v := costNull.Int64
		agg.CostMicros = &v
	}

	return agg, nil
}

func getKeyCounts(ctx context.Context, db dbQueries) (OverviewKeyCounts, error) {
	query := `
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN enabled = 1 THEN 1 ELSE 0 END), 0)
		FROM api_keys
	`
	var counts OverviewKeyCounts
	if err := db.QueryRowContext(ctx, query).Scan(&counts.Total, &counts.Enabled); err != nil {
		return OverviewKeyCounts{}, err
	}
	return counts, nil
}

func getRecentRequests(ctx context.Context, db dbQueries, limit int) ([]RequestListRecord, error) {
	if limit <= 0 {
		limit = 10
	}
	query := `
		SELECT
			request_id, api_key_id, key_name, method, path, route, model,
			requested_mode, upstream_mode, delivered_mode, downstream_status,
			upstream_status, terminal_outcome, upstream_started, error_code,
			client_bytes, upstream_bytes, delivered_bytes, input_tokens, output_tokens,
			total_tokens, cached_input_tokens, reasoning_output_tokens, cost_micros,
			started_at, upstream_started_at, upstream_headers_at, first_byte_at,
			finished_at, total_micros, time_to_upstream_headers_micros,
			time_to_first_byte_micros, stream_close_delay_micros
		FROM requests
		ORDER BY finished_at DESC, request_id DESC
		LIMIT ?
	`
	rows, err := db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := make([]RequestListRecord, 0, limit)
	for rows.Next() {
		record, scanErr := scanRequestListRecord(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

// GetOverview collects overview aggregates, key counts, and recent requests.
func GetOverview(ctx context.Context, db dbQueries, after, before time.Time, activeRequests int64) (*OverviewData, error) {
	if db == nil {
		return nil, errors.New("get overview: nil database")
	}
	if ctx == nil {
		return nil, errors.New("get overview: nil context")
	}

	after = after.UTC().Truncate(time.Microsecond)
	before = before.UTC().Truncate(time.Microsecond)
	duration := before.Sub(after)
	if duration <= 0 {
		return nil, errors.New("get overview: invalid range")
	}

	// Exact adjacent previous range: [after - duration, after - 1 microsecond]
	prevAfter := after.Add(-duration)
	prevBefore := after.Add(-time.Microsecond)

	currentAfterMicros := after.UnixMicro()
	currentBeforeMicros := before.UnixMicro()
	prevAfterMicros := prevAfter.UnixMicro()
	prevBeforeMicros := prevBefore.UnixMicro()

	currentAgg, err := aggregateRequests(ctx, db, currentAfterMicros, currentBeforeMicros)
	if err != nil {
		return nil, err
	}

	prevAgg, err := aggregateRequests(ctx, db, prevAfterMicros, prevBeforeMicros)
	if err != nil {
		return nil, err
	}

	keyCounts, err := getKeyCounts(ctx, db)
	if err != nil {
		return nil, err
	}

	recent, err := getRecentRequests(ctx, db, 10)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC().Truncate(time.Microsecond)

	return &OverviewData{
		CurrentRangeStart:  after,
		CurrentRangeEnd:    before,
		PreviousRangeStart: prevAfter,
		PreviousRangeEnd:   prevBefore,
		DataTimestamp:      now,
		Current:            currentAgg,
		Previous:           prevAgg,
		ActiveRequests:     activeRequests,
		KeyCounts:          keyCounts,
		RecentRequests:     recent,
	}, nil
}

// GetOverview implements OverviewRepository on APIKeyRepository.
func (repository *APIKeyRepository) GetOverview(ctx context.Context, after, before time.Time, activeRequests int64) (*OverviewData, error) {
	if repository == nil || repository.database == nil {
		return nil, ErrRepositoryUnavailable
	}
	return GetOverview(ctx, repository.database, after, before, activeRequests)
}

// GetOverview implements OverviewRepository on RequestHistoryRepository.
func (repository *RequestHistoryRepository) GetOverview(ctx context.Context, after, before time.Time, activeRequests int64) (*OverviewData, error) {
	if repository == nil || repository.database == nil {
		return nil, ErrHistoryUnavailable
	}
	return GetOverview(ctx, repository.database, after, before, activeRequests)
}
