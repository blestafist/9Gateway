package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrOverDetailedRange is returned when a requested bucket resolution would produce
	// too many buckets for the given time range, or exceeds safe bucket boundaries.
	ErrOverDetailedRange = errors.New("requested range is over-detailed for bucket")

	// ErrInvalidBucket is returned when an unrecognized bucket identifier is provided.
	ErrInvalidBucket = errors.New("invalid bucket parameter")

	// ErrInvalidGroupBy is returned when an unrecognized group_by dimension is provided.
	ErrInvalidGroupBy = errors.New("invalid group_by parameter")
)

// UsageTimeseriesBucket represents an individual aggregated time bucket in a time series.
type UsageTimeseriesBucket struct {
	BucketStart              time.Time `json:"bucket_start"`
	BucketEnd                time.Time `json:"bucket_end"`
	TotalRequests            int64     `json:"total_requests"`
	SuccessfulRequests       int64     `json:"successful_requests"`
	ErrorRequests            int64     `json:"error_requests"`
	RejectedRequests         int64     `json:"rejected_requests"`
	InputTokens              int64     `json:"input_tokens"`
	CachedInputTokens        int64     `json:"cached_input_tokens"`
	OutputTokens             int64     `json:"output_tokens"`
	CostMicros               *int64    `json:"cost_micros"`
	AvgTotalLatencyMicros    *int64    `json:"avg_total_latency_micros"`
	TotalLatencySamples      int64     `json:"total_latency_samples"`
	AvgTTFBLatencyMicros     *int64    `json:"avg_ttfb_latency_micros"`
	TTFBLatencySamples       int64     `json:"ttfb_latency_samples"`
	AvgUpstreamLatencyMicros *int64    `json:"avg_upstream_latency_micros"`
	UpstreamLatencySamples   int64     `json:"upstream_latency_samples"`
}

// UsageTimeseriesData contains the full completed time series returned by storage.
type UsageTimeseriesData struct {
	RequestedAfter     *time.Time              `json:"requested_after"`
	EffectiveAfter     time.Time               `json:"effective_after"`
	Before             time.Time               `json:"before"`
	Bucket             string                  `json:"bucket"`
	RetentionLimited   bool                    `json:"retention_limited"`
	EarliestRetainedAt *time.Time              `json:"earliest_retained_at"`
	LatestRetainedAt   *time.Time              `json:"latest_retained_at"`
	Buckets            []UsageTimeseriesBucket `json:"buckets"`
}

// UsageBreakdownRow represents a single dimension aggregate row in a breakdown.
type UsageBreakdownRow struct {
	ID                 string  `json:"id"`
	Name               string  `json:"name"`
	KeyID              *string `json:"key_id,omitempty"`
	IsUnknown          bool    `json:"is_unknown"`
	IsDeleted          bool    `json:"is_deleted"`
	TotalRequests      int64   `json:"total_requests"`
	SuccessfulRequests int64   `json:"successful_requests"`
	ErrorRequests      int64   `json:"error_requests"`
	RejectedRequests   int64   `json:"rejected_requests"`
	InputTokens        int64   `json:"input_tokens"`
	CachedInputTokens  int64   `json:"cached_input_tokens"`
	OutputTokens       int64   `json:"output_tokens"`
	CostMicros         *int64  `json:"cost_micros"`
}

// UsageBreakdownTotal contains the untruncated overall aggregate for breakdown responses.
type UsageBreakdownTotal struct {
	TotalRequests      int64  `json:"total_requests"`
	SuccessfulRequests int64  `json:"successful_requests"`
	ErrorRequests      int64  `json:"error_requests"`
	RejectedRequests   int64  `json:"rejected_requests"`
	InputTokens        int64  `json:"input_tokens"`
	CachedInputTokens  int64  `json:"cached_input_tokens"`
	OutputTokens       int64  `json:"output_tokens"`
	CostMicros         *int64 `json:"cost_micros"`
}

// UsageBreakdownData contains the full completed breakdown data returned by storage.
type UsageBreakdownData struct {
	RequestedAfter     *time.Time          `json:"requested_after"`
	EffectiveAfter     time.Time           `json:"effective_after"`
	Before             time.Time           `json:"before"`
	GroupBy            string              `json:"group_by"`
	RetentionLimited   bool                `json:"retention_limited"`
	EarliestRetainedAt *time.Time          `json:"earliest_retained_at"`
	LatestRetainedAt   *time.Time          `json:"latest_retained_at"`
	Rows               []UsageBreakdownRow `json:"rows"`
	Other              UsageBreakdownRow   `json:"other"`
	Total              UsageBreakdownTotal `json:"total"`
}

// UsageRepository defines usage time series and breakdown aggregation operations.
type UsageRepository interface {
	GetUsageTimeseries(ctx context.Context, reqAfter *time.Time, before time.Time, bucket string) (*UsageTimeseriesData, error)
	GetUsageBreakdown(ctx context.Context, reqAfter *time.Time, before time.Time, groupBy string) (*UsageBreakdownData, error)
}

func queryEarliestAndLatestRetained(ctx context.Context, db dbQueries) (earliest *time.Time, latest *time.Time, deletionSeq int64, err error) {
	var minFin, maxFin sql.NullInt64
	row := db.QueryRowContext(ctx, `SELECT MIN(finished_at), MAX(finished_at) FROM requests WHERE finished_at IS NOT NULL`)
	if err := row.Scan(&minFin, &maxFin); err != nil {
		return nil, nil, 0, err
	}
	if minFin.Valid {
		t := time.UnixMicro(minFin.Int64).UTC()
		earliest = &t
	}
	if maxFin.Valid {
		t := time.UnixMicro(maxFin.Int64).UTC()
		latest = &t
	}

	rowDel := db.QueryRowContext(ctx, `SELECT COALESCE((SELECT deletion_seq FROM traversal_sequences WHERE table_name = 'requests'), 0)`)
	if err := rowDel.Scan(&deletionSeq); err != nil {
		return earliest, latest, 0, err
	}

	return earliest, latest, deletionSeq, nil
}

// FloorBucket returns the start of the UTC bucket containing t.
func FloorBucket(t time.Time, bucket string) time.Time {
	t = t.UTC()
	switch bucket {
	case "five_minutes":
		return t.Truncate(5 * time.Minute)
	case "hour":
		return t.Truncate(time.Hour)
	case "day":
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	case "week":
		weekday := int(t.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		daysToSubtract := weekday - 1
		d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		return d.AddDate(0, 0, -daysToSubtract)
	case "month":
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		return t.Truncate(time.Hour)
	}
}

// NextBucket advances t by one bucket duration in UTC.
func NextBucket(t time.Time, bucket string) time.Time {
	t = t.UTC()
	switch bucket {
	case "five_minutes":
		return t.Add(5 * time.Minute)
	case "hour":
		return t.Add(time.Hour)
	case "day":
		return t.AddDate(0, 0, 1)
	case "week":
		return t.AddDate(0, 0, 7)
	case "month":
		return t.AddDate(0, 1, 0)
	default:
		return t.Add(time.Hour)
	}
}

// SelectAutoBucket chooses the finest valid bucket producing <= 1000 points.
func SelectAutoBucket(duration time.Duration) string {
	if duration <= 24*time.Hour {
		return "five_minutes"
	}
	if duration <= 31*24*time.Hour {
		return "hour"
	}
	if duration <= 2*366*24*time.Hour {
		return "day"
	}
	if duration <= 10*366*24*time.Hour {
		return "week"
	}
	return "month"
}

// ValidateBucketRange validates that duration does not exceed the maximum allowed for bucket.
func ValidateBucketRange(duration time.Duration, bucket string) error {
	switch bucket {
	case "five_minutes":
		if duration > 24*time.Hour {
			return fmt.Errorf("%w: five_minutes exceeds 24 hours", ErrOverDetailedRange)
		}
	case "hour":
		if duration > 31*24*time.Hour {
			return fmt.Errorf("%w: hour exceeds 31 days", ErrOverDetailedRange)
		}
	case "day":
		if duration > 2*366*24*time.Hour {
			return fmt.Errorf("%w: day exceeds 2 years", ErrOverDetailedRange)
		}
	case "week":
		if duration > 10*366*24*time.Hour {
			return fmt.Errorf("%w: week exceeds 10 years", ErrOverDetailedRange)
		}
	case "month":
		// Allowed for longer / all retained
	default:
		return ErrInvalidBucket
	}
	return nil
}

// GetUsageTimeseries executes the usage time-series aggregation query.
func GetUsageTimeseries(ctx context.Context, db dbQueries, reqAfter *time.Time, before time.Time, bucket string) (*UsageTimeseriesData, error) {
	if db == nil {
		return nil, errors.New("nil database")
	}
	if ctx == nil {
		return nil, errors.New("nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	before = before.UTC().Truncate(time.Microsecond)
	earliest, latest, deletionSeq, err := queryEarliestAndLatestRetained(ctx, db)
	if err != nil {
		return nil, err
	}

	var effectiveAfter time.Time
	var retentionLimited bool

	if reqAfter != nil {
		effectiveAfter = reqAfter.UTC().Truncate(time.Microsecond)
		if deletionSeq > 0 {
			if earliest == nil || effectiveAfter.Before(*earliest) {
				retentionLimited = true
			}
		}
	} else {
		// All retained history requested
		if earliest != nil {
			effectiveAfter = *earliest
		} else {
			effectiveAfter = before
		}
		if deletionSeq > 0 {
			retentionLimited = true
		}
	}

	if effectiveAfter.After(before) {
		effectiveAfter = before
	}

	duration := before.Sub(effectiveAfter)

	if bucket == "" || bucket == "auto" {
		bucket = SelectAutoBucket(duration)
	}

	if err := ValidateBucketRange(duration, bucket); err != nil {
		return nil, err
	}

	// Empty history case
	if duration == 0 || effectiveAfter.Equal(before) {
		return &UsageTimeseriesData{
			RequestedAfter:     reqAfter,
			EffectiveAfter:     effectiveAfter,
			Before:             before,
			Bucket:             bucket,
			RetentionLimited:   retentionLimited,
			EarliestRetainedAt: earliest,
			LatestRetainedAt:   latest,
			Buckets:            []UsageTimeseriesBucket{},
		}, nil
	}

	// Generate expected buckets
	startBucket := FloorBucket(effectiveAfter, bucket)
	endBucket := FloorBucket(before, bucket)

	var expectedBuckets []time.Time
	for cur := startBucket; !cur.After(endBucket); cur = NextBucket(cur, bucket) {
		expectedBuckets = append(expectedBuckets, cur)
		if len(expectedBuckets) > 1000 {
			return nil, fmt.Errorf("%w: range produces more than 1000 buckets", ErrOverDetailedRange)
		}
	}

	// Determine SQLite bucket expression
	var bucketExpr string
	switch bucket {
	case "five_minutes":
		bucketExpr = "(finished_at / 300000000) * 300000000"
	case "hour":
		bucketExpr = "(finished_at / 3600000000) * 3600000000"
	case "day":
		bucketExpr = "(finished_at / 86400000000) * 86400000000"
	case "week":
		bucketExpr = "(((finished_at / 1000000 - 345600) / 604800) * 604800 + 345600) * 1000000"
	case "month":
		bucketExpr = "CAST(strftime('%s', date(finished_at / 1000000, 'unixepoch', 'start of month')) AS INTEGER) * 1000000"
	default:
		return nil, ErrInvalidBucket
	}

	query := fmt.Sprintf(`
		SELECT
			%s AS b_micros,
			COUNT(*),
			COALESCE(SUM(CASE WHEN terminal_outcome IN ('complete', 'custom_dispatch') THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN terminal_outcome NOT IN ('complete', 'custom_dispatch', 'pre_upstream') THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN terminal_outcome = 'pre_upstream' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(cached_input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			SUM(cost_micros),
			COUNT(cost_micros),
			ROUND(AVG(total_micros)),
			COUNT(total_micros),
			ROUND(AVG(time_to_first_byte_micros)),
			COUNT(time_to_first_byte_micros),
			ROUND(AVG(time_to_upstream_headers_micros)),
			COUNT(time_to_upstream_headers_micros)
		FROM requests
		WHERE finished_at >= ? AND finished_at <= ?
		GROUP BY b_micros
		ORDER BY b_micros ASC
	`, bucketExpr)

	rows, err := db.QueryContext(ctx, query, effectiveAfter.UnixMicro(), before.UnixMicro())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type aggRecord struct {
		totalRequests      int64
		successfulRequests int64
		errorRequests      int64
		rejectedRequests   int64
		inputTokens        int64
		cachedInputTokens  int64
		outputTokens       int64
		costMicros         *int64
		avgTotalLatency    *int64
		totalLatencyCount  int64
		avgTTFBLatency     *int64
		ttfbLatencyCount   int64
		avgUpstreamLatency *int64
		upstreamLatencyCnt int64
	}

	aggregates := make(map[int64]aggRecord)

	for rows.Next() {
		var (
			bMicros        int64
			totReq         int64
			succReq        int64
			errReq         int64
			rejReq         int64
			inTok          int64
			cacheTok       int64
			outTok         int64
			costSumNull    sql.NullInt64
			costCount      int64
			avgTotNull     sql.NullInt64
			totLatCount    int64
			avgTTFBNull    sql.NullInt64
			ttfbLatCount   int64
			avgUpstreamNul sql.NullInt64
			upLatCount     int64
		)
		if err := rows.Scan(
			&bMicros,
			&totReq,
			&succReq,
			&errReq,
			&rejReq,
			&inTok,
			&cacheTok,
			&outTok,
			&costSumNull,
			&costCount,
			&avgTotNull,
			&totLatCount,
			&avgTTFBNull,
			&ttfbLatCount,
			&avgUpstreamNul,
			&upLatCount,
		); err != nil {
			return nil, err
		}

		rec := aggRecord{
			totalRequests:      totReq,
			successfulRequests: succReq,
			errorRequests:      errReq,
			rejectedRequests:   rejReq,
			inputTokens:        inTok,
			cachedInputTokens:  cacheTok,
			outputTokens:       outTok,
			totalLatencyCount:  totLatCount,
			ttfbLatencyCount:   ttfbLatCount,
			upstreamLatencyCnt: upLatCount,
		}

		if costCount > 0 && costSumNull.Valid {
			v := costSumNull.Int64
			rec.costMicros = &v
		}
		if totLatCount > 0 && avgTotNull.Valid {
			v := avgTotNull.Int64
			rec.avgTotalLatency = &v
		}
		if ttfbLatCount > 0 && avgTTFBNull.Valid {
			v := avgTTFBNull.Int64
			rec.avgTTFBLatency = &v
		}
		if upLatCount > 0 && avgUpstreamNul.Valid {
			v := avgUpstreamNul.Int64
			rec.avgUpstreamLatency = &v
		}

		aggregates[bMicros] = rec
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	zeroCost := int64(0)
	resultBuckets := make([]UsageTimeseriesBucket, 0, len(expectedBuckets))

	for _, bStart := range expectedBuckets {
		bEnd := NextBucket(bStart, bucket)
		key := bStart.UnixMicro()

		bucketItem := UsageTimeseriesBucket{
			BucketStart: bStart,
			BucketEnd:   bEnd,
		}

		if rec, ok := aggregates[key]; ok {
			bucketItem.TotalRequests = rec.totalRequests
			bucketItem.SuccessfulRequests = rec.successfulRequests
			bucketItem.ErrorRequests = rec.errorRequests
			bucketItem.RejectedRequests = rec.rejectedRequests
			bucketItem.InputTokens = rec.inputTokens
			bucketItem.CachedInputTokens = rec.cachedInputTokens
			bucketItem.OutputTokens = rec.outputTokens
			bucketItem.CostMicros = rec.costMicros
			bucketItem.AvgTotalLatencyMicros = rec.avgTotalLatency
			bucketItem.TotalLatencySamples = rec.totalLatencyCount
			bucketItem.AvgTTFBLatencyMicros = rec.avgTTFBLatency
			bucketItem.TTFBLatencySamples = rec.ttfbLatencyCount
			bucketItem.AvgUpstreamLatencyMicros = rec.avgUpstreamLatency
			bucketItem.UpstreamLatencySamples = rec.upstreamLatencyCnt
		} else {
			// Filled missing bucket
			bucketItem.CostMicros = &zeroCost
		}

		resultBuckets = append(resultBuckets, bucketItem)
	}

	return &UsageTimeseriesData{
		RequestedAfter:     reqAfter,
		EffectiveAfter:     effectiveAfter,
		Before:             before,
		Bucket:             bucket,
		RetentionLimited:   retentionLimited,
		EarliestRetainedAt: earliest,
		LatestRetainedAt:   latest,
		Buckets:            resultBuckets,
	}, nil
}

// GetUsageBreakdown executes the usage breakdown aggregation query.
func GetUsageBreakdown(ctx context.Context, db dbQueries, reqAfter *time.Time, before time.Time, groupBy string) (*UsageBreakdownData, error) {
	if db == nil {
		return nil, errors.New("nil database")
	}
	if ctx == nil {
		return nil, errors.New("nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	before = before.UTC().Truncate(time.Microsecond)
	earliest, latest, deletionSeq, err := queryEarliestAndLatestRetained(ctx, db)
	if err != nil {
		return nil, err
	}

	var effectiveAfter time.Time
	var retentionLimited bool

	if reqAfter != nil {
		effectiveAfter = reqAfter.UTC().Truncate(time.Microsecond)
		if deletionSeq > 0 {
			if earliest == nil || effectiveAfter.Before(*earliest) {
				retentionLimited = true
			}
		}
	} else {
		// All retained history requested
		if earliest != nil {
			effectiveAfter = *earliest
		} else {
			effectiveAfter = before
		}
		if deletionSeq > 0 {
			retentionLimited = true
		}
	}

	if effectiveAfter.After(before) {
		effectiveAfter = before
	}

	zeroCost := int64(0)

	// Untruncated total query
	totalQuery := `
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN terminal_outcome IN ('complete', 'custom_dispatch') THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN terminal_outcome NOT IN ('complete', 'custom_dispatch', 'pre_upstream') THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN terminal_outcome = 'pre_upstream' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(cached_input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			SUM(cost_micros),
			COUNT(cost_micros)
		FROM requests
		WHERE finished_at >= ? AND finished_at <= ?
	`

	var (
		total        UsageBreakdownTotal
		totCostNull  sql.NullInt64
		totCostCount int64
	)

	row := db.QueryRowContext(ctx, totalQuery, effectiveAfter.UnixMicro(), before.UnixMicro())
	if err := row.Scan(
		&total.TotalRequests,
		&total.SuccessfulRequests,
		&total.ErrorRequests,
		&total.RejectedRequests,
		&total.InputTokens,
		&total.CachedInputTokens,
		&total.OutputTokens,
		&totCostNull,
		&totCostCount,
	); err != nil {
		return nil, err
	}

	if totCostCount > 0 && totCostNull.Valid {
		v := totCostNull.Int64
		total.CostMicros = &v
	} else if total.TotalRequests == 0 {
		total.CostMicros = &zeroCost
	}

	// Empty result check
	if total.TotalRequests == 0 {
		return &UsageBreakdownData{
			RequestedAfter:     reqAfter,
			EffectiveAfter:     effectiveAfter,
			Before:             before,
			GroupBy:            groupBy,
			RetentionLimited:   retentionLimited,
			EarliestRetainedAt: earliest,
			LatestRetainedAt:   latest,
			Rows:               []UsageBreakdownRow{},
			Other: UsageBreakdownRow{
				ID:         "other",
				Name:       "Other",
				CostMicros: &zeroCost,
			},
			Total: total,
		}, nil
	}

	var rows []UsageBreakdownRow
	var sumRows UsageBreakdownTotal
	var sumCostCount int64
	var sumKnownCost int64

	switch groupBy {
	case "model":
		query := `
			SELECT
				COALESCE(model, '') AS model_name,
				COUNT(*) AS total_reqs,
				COALESCE(SUM(CASE WHEN terminal_outcome IN ('complete', 'custom_dispatch') THEN 1 ELSE 0 END), 0),
				COALESCE(SUM(CASE WHEN terminal_outcome NOT IN ('complete', 'custom_dispatch', 'pre_upstream') THEN 1 ELSE 0 END), 0),
				COALESCE(SUM(CASE WHEN terminal_outcome = 'pre_upstream' THEN 1 ELSE 0 END), 0),
				COALESCE(SUM(input_tokens), 0),
				COALESCE(SUM(cached_input_tokens), 0),
				COALESCE(SUM(output_tokens), 0),
				SUM(cost_micros),
				COUNT(cost_micros)
			FROM requests
			WHERE finished_at >= ? AND finished_at <= ?
			GROUP BY model_name
			ORDER BY total_reqs DESC, model_name ASC
			LIMIT 20
		`
		sqlRows, err := db.QueryContext(ctx, query, effectiveAfter.UnixMicro(), before.UnixMicro())
		if err != nil {
			return nil, err
		}
		defer sqlRows.Close()

		for sqlRows.Next() {
			var (
				modelName string
				r         UsageBreakdownRow
				costNull  sql.NullInt64
				costCnt   int64
			)
			if err := sqlRows.Scan(
				&modelName,
				&r.TotalRequests,
				&r.SuccessfulRequests,
				&r.ErrorRequests,
				&r.RejectedRequests,
				&r.InputTokens,
				&r.CachedInputTokens,
				&r.OutputTokens,
				&costNull,
				&costCnt,
			); err != nil {
				return nil, err
			}

			if modelName == "" {
				r.ID = "unknown"
				r.Name = "unknown"
				r.IsUnknown = true
			} else {
				r.ID = modelName
				r.Name = modelName
			}

			if costCnt > 0 && costNull.Valid {
				v := costNull.Int64
				r.CostMicros = &v
				sumKnownCost += v
			} else if r.TotalRequests == 0 {
				r.CostMicros = &zeroCost
			}

			sumRows.TotalRequests += r.TotalRequests
			sumRows.SuccessfulRequests += r.SuccessfulRequests
			sumRows.ErrorRequests += r.ErrorRequests
			sumRows.RejectedRequests += r.RejectedRequests
			sumRows.InputTokens += r.InputTokens
			sumRows.CachedInputTokens += r.CachedInputTokens
			sumRows.OutputTokens += r.OutputTokens
			sumCostCount += costCnt

			rows = append(rows, r)
		}
		if err := sqlRows.Err(); err != nil {
			return nil, err
		}

	case "outcome":
		query := `
			SELECT
				COALESCE(terminal_outcome, 'unknown') AS outcome_val,
				COUNT(*) AS total_reqs,
				COALESCE(SUM(CASE WHEN terminal_outcome IN ('complete', 'custom_dispatch') THEN 1 ELSE 0 END), 0),
				COALESCE(SUM(CASE WHEN terminal_outcome NOT IN ('complete', 'custom_dispatch', 'pre_upstream') THEN 1 ELSE 0 END), 0),
				COALESCE(SUM(CASE WHEN terminal_outcome = 'pre_upstream' THEN 1 ELSE 0 END), 0),
				COALESCE(SUM(input_tokens), 0),
				COALESCE(SUM(cached_input_tokens), 0),
				COALESCE(SUM(output_tokens), 0),
				SUM(cost_micros),
				COUNT(cost_micros)
			FROM requests
			WHERE finished_at >= ? AND finished_at <= ?
			GROUP BY outcome_val
			ORDER BY total_reqs DESC, outcome_val ASC
			LIMIT 20
		`
		sqlRows, err := db.QueryContext(ctx, query, effectiveAfter.UnixMicro(), before.UnixMicro())
		if err != nil {
			return nil, err
		}
		defer sqlRows.Close()

		for sqlRows.Next() {
			var (
				outcomeVal string
				r          UsageBreakdownRow
				costNull   sql.NullInt64
				costCnt    int64
			)
			if err := sqlRows.Scan(
				&outcomeVal,
				&r.TotalRequests,
				&r.SuccessfulRequests,
				&r.ErrorRequests,
				&r.RejectedRequests,
				&r.InputTokens,
				&r.CachedInputTokens,
				&r.OutputTokens,
				&costNull,
				&costCnt,
			); err != nil {
				return nil, err
			}

			r.ID = outcomeVal
			r.Name = outcomeVal
			if outcomeVal == "unknown" {
				r.IsUnknown = true
			}

			if costCnt > 0 && costNull.Valid {
				v := costNull.Int64
				r.CostMicros = &v
				sumKnownCost += v
			} else if r.TotalRequests == 0 {
				r.CostMicros = &zeroCost
			}

			sumRows.TotalRequests += r.TotalRequests
			sumRows.SuccessfulRequests += r.SuccessfulRequests
			sumRows.ErrorRequests += r.ErrorRequests
			sumRows.RejectedRequests += r.RejectedRequests
			sumRows.InputTokens += r.InputTokens
			sumRows.CachedInputTokens += r.CachedInputTokens
			sumRows.OutputTokens += r.OutputTokens
			sumCostCount += costCnt

			rows = append(rows, r)
		}
		if err := sqlRows.Err(); err != nil {
			return nil, err
		}

	case "key":
		query := `
			SELECT
				CASE
					WHEN r.api_key_id IS NOT NULL THEN r.api_key_id
					WHEN r.key_name IS NOT NULL AND r.key_name != '' THEN 'deleted:' || r.key_name
					ELSE 'unknown'
				END AS group_key,
				COALESCE(MAX(r.api_key_id), '') AS key_id_val,
				COALESCE(MAX(COALESCE(k.name, r.key_name)), '') AS name_val,
				CASE
					WHEN r.api_key_id IS NULL AND (r.key_name IS NULL OR r.key_name = '') THEN 1
					ELSE 0
				END AS is_unknown,
				CASE
					WHEN r.api_key_id IS NOT NULL AND k.id IS NULL THEN 1
					WHEN r.api_key_id IS NULL AND r.key_name IS NOT NULL AND r.key_name != '' THEN 1
					ELSE 0
				END AS is_deleted,
				COUNT(*) AS total_reqs,
				COALESCE(SUM(CASE WHEN r.terminal_outcome IN ('complete', 'custom_dispatch') THEN 1 ELSE 0 END), 0),
				COALESCE(SUM(CASE WHEN r.terminal_outcome NOT IN ('complete', 'custom_dispatch', 'pre_upstream') THEN 1 ELSE 0 END), 0),
				COALESCE(SUM(CASE WHEN r.terminal_outcome = 'pre_upstream' THEN 1 ELSE 0 END), 0),
				COALESCE(SUM(r.input_tokens), 0),
				COALESCE(SUM(r.cached_input_tokens), 0),
				COALESCE(SUM(r.output_tokens), 0),
				SUM(r.cost_micros),
				COUNT(r.cost_micros)
			FROM requests r
			LEFT JOIN api_keys k ON r.api_key_id = k.id
			WHERE r.finished_at >= ? AND r.finished_at <= ?
			GROUP BY group_key
			ORDER BY total_reqs DESC, group_key ASC
			LIMIT 20
		`
		sqlRows, err := db.QueryContext(ctx, query, effectiveAfter.UnixMicro(), before.UnixMicro())
		if err != nil {
			return nil, err
		}
		defer sqlRows.Close()

		for sqlRows.Next() {
			var (
				groupKey string
				keyIDVal string
				nameVal  string
				isUnk    int
				isDel    int
				r        UsageBreakdownRow
				costNull sql.NullInt64
				costCnt  int64
			)
			if err := sqlRows.Scan(
				&groupKey,
				&keyIDVal,
				&nameVal,
				&isUnk,
				&isDel,
				&r.TotalRequests,
				&r.SuccessfulRequests,
				&r.ErrorRequests,
				&r.RejectedRequests,
				&r.InputTokens,
				&r.CachedInputTokens,
				&r.OutputTokens,
				&costNull,
				&costCnt,
			); err != nil {
				return nil, err
			}

			r.ID = groupKey
			if isUnk == 1 {
				r.ID = "unknown"
				r.Name = "unknown"
				r.IsUnknown = true
			} else {
				if nameVal != "" {
					r.Name = nameVal
				} else {
					r.Name = groupKey
				}
				if isDel == 1 {
					r.IsDeleted = true
				}
				if keyIDVal != "" {
					idStr := keyIDVal
					r.KeyID = &idStr
				}
			}

			if costCnt > 0 && costNull.Valid {
				v := costNull.Int64
				r.CostMicros = &v
				sumKnownCost += v
			} else if r.TotalRequests == 0 {
				r.CostMicros = &zeroCost
			}

			sumRows.TotalRequests += r.TotalRequests
			sumRows.SuccessfulRequests += r.SuccessfulRequests
			sumRows.ErrorRequests += r.ErrorRequests
			sumRows.RejectedRequests += r.RejectedRequests
			sumRows.InputTokens += r.InputTokens
			sumRows.CachedInputTokens += r.CachedInputTokens
			sumRows.OutputTokens += r.OutputTokens
			sumCostCount += costCnt

			rows = append(rows, r)
		}
		if err := sqlRows.Err(); err != nil {
			return nil, err
		}

	default:
		return nil, ErrInvalidGroupBy
	}

	if rows == nil {
		rows = []UsageBreakdownRow{}
	}

	// Calculate other aggregate
	otherTotalReq := total.TotalRequests - sumRows.TotalRequests
	other := UsageBreakdownRow{
		ID:                 "other",
		Name:               "Other",
		TotalRequests:      otherTotalReq,
		SuccessfulRequests: total.SuccessfulRequests - sumRows.SuccessfulRequests,
		ErrorRequests:      total.ErrorRequests - sumRows.ErrorRequests,
		RejectedRequests:   total.RejectedRequests - sumRows.RejectedRequests,
		InputTokens:        total.InputTokens - sumRows.InputTokens,
		CachedInputTokens:  total.CachedInputTokens - sumRows.CachedInputTokens,
		OutputTokens:       total.OutputTokens - sumRows.OutputTokens,
	}

	otherCostCount := totCostCount - sumCostCount
	if otherTotalReq == 0 {
		other.CostMicros = &zeroCost
	} else if otherCostCount == 0 {
		other.CostMicros = nil // Unknown cost
	} else if total.CostMicros != nil {
		diffCost := *total.CostMicros - sumKnownCost
		other.CostMicros = &diffCost
	}

	return &UsageBreakdownData{
		RequestedAfter:     reqAfter,
		EffectiveAfter:     effectiveAfter,
		Before:             before,
		GroupBy:            groupBy,
		RetentionLimited:   retentionLimited,
		EarliestRetainedAt: earliest,
		LatestRetainedAt:   latest,
		Rows:               rows,
		Other:              other,
		Total:              total,
	}, nil
}

// GetUsageTimeseries implements UsageRepository on APIKeyRepository.
func (repository *APIKeyRepository) GetUsageTimeseries(ctx context.Context, reqAfter *time.Time, before time.Time, bucket string) (*UsageTimeseriesData, error) {
	if repository == nil || repository.database == nil {
		return nil, ErrRepositoryUnavailable
	}
	return GetUsageTimeseries(ctx, repository.database, reqAfter, before, bucket)
}

// GetUsageBreakdown implements UsageRepository on APIKeyRepository.
func (repository *APIKeyRepository) GetUsageBreakdown(ctx context.Context, reqAfter *time.Time, before time.Time, groupBy string) (*UsageBreakdownData, error) {
	if repository == nil || repository.database == nil {
		return nil, ErrRepositoryUnavailable
	}
	return GetUsageBreakdown(ctx, repository.database, reqAfter, before, groupBy)
}

// GetUsageTimeseries implements UsageRepository on RequestHistoryRepository.
func (repository *RequestHistoryRepository) GetUsageTimeseries(ctx context.Context, reqAfter *time.Time, before time.Time, bucket string) (*UsageTimeseriesData, error) {
	if repository == nil || repository.database == nil {
		return nil, ErrHistoryUnavailable
	}
	return GetUsageTimeseries(ctx, repository.database, reqAfter, before, bucket)
}

// GetUsageBreakdown implements UsageRepository on RequestHistoryRepository.
func (repository *RequestHistoryRepository) GetUsageBreakdown(ctx context.Context, reqAfter *time.Time, before time.Time, groupBy string) (*UsageBreakdownData, error) {
	if repository == nil || repository.database == nil {
		return nil, ErrHistoryUnavailable
	}
	return GetUsageBreakdown(ctx, repository.database, reqAfter, before, groupBy)
}
