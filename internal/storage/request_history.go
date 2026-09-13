package storage

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pestit/9gateway/internal/observability"
)

// OptionalInt64 is a storage-owned optional integer. Known zero is deliberately
// different from an unknown value; unknown values are written as SQL NULL.
type OptionalInt64 struct {
	Value int64
	Known bool
}

func KnownInt64(value int64) OptionalInt64 { return OptionalInt64{Value: value, Known: true} }

// HistoryRecord is the scalar projection of a canonical completion record.
// It contains no HTTP, policy, parser, logger, limiter, or credential values.
// Empty strings represent unknown optional text/enums.
type HistoryRecord struct {
	RequestID string
	APIKeyID  string
	// KeyID is the canonical-record spelling. APIKeyID is the schema spelling;
	// if both are supplied they must agree.
	KeyID   string
	KeyName string
	Method  string
	Path    string
	Route   string
	Model   string

	RequestedMode string
	UpstreamMode  string
	DeliveredMode string

	DownstreamStatus OptionalInt64
	UpstreamStatus   OptionalInt64
	TerminalOutcome  string
	UpstreamStarted  bool
	ErrorCode        string

	ClientBytes    OptionalInt64
	UpstreamBytes  OptionalInt64
	DeliveredBytes OptionalInt64

	InputTokens           OptionalInt64
	OutputTokens          OptionalInt64
	TotalTokens           OptionalInt64
	CachedInputTokens     OptionalInt64
	ReasoningOutputTokens OptionalInt64
	CostMicros            OptionalInt64

	StartedAt                   OptionalInt64
	UpstreamStartedAt           OptionalInt64
	UpstreamHeadersAt           OptionalInt64
	FirstByteAt                 OptionalInt64
	FinishedAt                  OptionalInt64
	TotalMicros                 OptionalInt64
	TimeToUpstreamHeadersMicros OptionalInt64
	TimeToFirstByteMicros       OptionalInt64
	StreamCloseDelayMicros      OptionalInt64
}

// RequestHistoryRecord and CompletionRecord are descriptive names for the
// storage projection. The canonical HTTP package supplies this projection at
// its boundary; storage never imports that package.
type RequestHistoryRecord = HistoryRecord
type StoredCompletionRecord = HistoryRecord
type CompletionRecord = HistoryRecord
type RequestRecord = HistoryRecord

var (
	ErrHistoryRepositoryUnavailable = errors.New("request history repository unavailable")
	ErrHistoryInvalidRecord         = errors.New("invalid request history record")
	ErrHistoryInvalidBody           = errors.New("invalid request history body")
	ErrHistoryInvalidKey            = errors.New("invalid request history key identity")
	ErrHistoryDuplicate             = errors.New("request history record already exists")
	ErrHistoryWrite                 = errors.New("request history write failed")
	ErrHistoryRetention             = errors.New("request history retention failed")
	ErrHistoryInvalidLimit          = errors.New("request history retention limit must be positive")
	ErrHistoryInvalidCutoff         = errors.New("request history retention cutoff is invalid")
)

// Compatibility spellings keep the error vocabulary discoverable without
// introducing an error containing driver, SQL, or payload details.
var (
	ErrInvalidHistoryRecord = ErrHistoryInvalidRecord
	ErrInvalidHistoryBody   = ErrHistoryInvalidBody
	ErrInvalidHistoryKey    = ErrHistoryInvalidKey
	ErrHistoryUnavailable   = ErrHistoryRepositoryUnavailable
)

// RequestHistoryRepository is the synchronous persistence boundary for request
// history. It does not schedule, retry, or retain jobs.
type RequestHistoryRepository struct {
	database dbQueries
	beginner txBeginner
}

// RequestRepository is the short name used by history-worker callers.
type RequestRepository = RequestHistoryRepository

func NewRequestHistoryRepository(database dbQueries) *RequestHistoryRepository {
	repository := &RequestHistoryRepository{database: database}
	if beginner, ok := database.(txBeginner); ok {
		repository.beginner = beginner
	}
	return repository
}

// NewHistoryRepository is the concise constructor spelling.
func NewHistoryRepository(database dbQueries) *RequestHistoryRepository {
	return NewRequestHistoryRepository(database)
}

func NewRequestRepository(database dbQueries) *RequestRepository {
	return NewRequestHistoryRepository(database)
}

// Persist inserts one record and its optional body captures atomically. At
// most one body of each of the three closed body kinds may be supplied.
func (repository *RequestHistoryRepository) Persist(ctx context.Context, record HistoryRecord, bodies []observability.BodySnapshot) error {
	if ctx == nil {
		return errors.New("persist request history: nil context")
	}
	record = normalizeHistoryRecord(record)
	if err := validateHistoryRecord(record); err != nil {
		return err
	}
	validBodies, err := validateHistoryBodies(bodies)
	if err != nil {
		return err
	}
	if repository == nil || repository.database == nil || repository.beginner == nil {
		return ErrHistoryRepositoryUnavailable
	}
	if err := ctx.Err(); err != nil {
		return historyContextError(err, ErrHistoryWrite)
	}

	tx, err := repository.beginner.BeginTx(ctx, nil)
	if err != nil {
		return historyOperationError(err, ErrHistoryWrite)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var present int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM requests WHERE request_id = ?", record.RequestID).Scan(&present); err != nil {
		return historyOperationError(err, ErrHistoryWrite)
	}
	if present != 0 {
		return ErrHistoryDuplicate
	}
	if record.APIKeyID != "" {
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM api_keys WHERE id = ?", record.APIKeyID).Scan(&present); err != nil {
			return historyOperationError(err, ErrHistoryWrite)
		}
		if present == 0 {
			return ErrHistoryInvalidKey
		}
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO requests (
			request_id, api_key_id, key_name, method, path, route, model,
			requested_mode, upstream_mode, delivered_mode, downstream_status,
			upstream_status, terminal_outcome, upstream_started, error_code,
			client_bytes, upstream_bytes, delivered_bytes, input_tokens,
			output_tokens, total_tokens, cached_input_tokens, reasoning_output_tokens,
			cost_micros, started_at, upstream_started_at, upstream_headers_at,
			first_byte_at, finished_at, total_micros, time_to_upstream_headers_micros,
			time_to_first_byte_micros, stream_close_delay_micros
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.RequestID, nullableText(record.APIKeyID), nullableText(record.KeyName),
		nullableText(record.Method), nullableText(record.Path), nullableText(record.Route), nullableText(record.Model),
		nullableText(record.RequestedMode), nullableText(record.UpstreamMode), nullableText(record.DeliveredMode),
		nullableInt(record.DownstreamStatus), nullableInt(record.UpstreamStatus), nullableText(record.TerminalOutcome),
		boolInt(record.UpstreamStarted), nullableText(record.ErrorCode), nullableInt(record.ClientBytes),
		nullableInt(record.UpstreamBytes), nullableInt(record.DeliveredBytes), nullableInt(record.InputTokens),
		nullableInt(record.OutputTokens), nullableInt(record.TotalTokens), nullableInt(record.CachedInputTokens),
		nullableInt(record.ReasoningOutputTokens), nullableInt(record.CostMicros), nullableInt(record.StartedAt),
		nullableInt(record.UpstreamStartedAt), nullableInt(record.UpstreamHeadersAt), nullableInt(record.FirstByteAt),
		nullableInt(record.FinishedAt), nullableInt(record.TotalMicros), nullableInt(record.TimeToUpstreamHeadersMicros),
		nullableInt(record.TimeToFirstByteMicros), nullableInt(record.StreamCloseDelayMicros)); err != nil {
		return historyOperationError(err, ErrHistoryWrite)
	}
	for _, body := range validBodies {
		payload := append([]byte(nil), body.Bytes...)
		if payload == nil {
			payload = []byte{}
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO request_bodies (request_id, body_kind, body, original_size, truncated)
			VALUES (?, ?, ?, ?, ?)`, record.RequestID, string(body.Kind), payload, body.OriginalSize, boolInt(body.Truncated)); err != nil {
			return historyOperationError(err, ErrHistoryWrite)
		}
	}
	if err := tx.Commit(); err != nil {
		return historyOperationError(err, ErrHistoryWrite)
	}
	committed = true
	return nil
}

// Insert is the repository's conventional method spelling.
func (repository *RequestHistoryRepository) Insert(ctx context.Context, record HistoryRecord, bodies []observability.BodySnapshot) error {
	return repository.Persist(ctx, record, bodies)
}

// PersistSnapshots is a convenience for callers that already have individual
// immutable snapshots while the primary API remains slice-based.
func (repository *RequestHistoryRepository) PersistSnapshots(ctx context.Context, record HistoryRecord, bodies ...observability.BodySnapshot) error {
	return repository.Persist(ctx, record, bodies)
}

// DeleteBodiesBefore removes at most maxRows body rows whose owning request's
// finished_at is strictly before cutoff. A row exactly at cutoff is retained.
// Ordering is deterministic by completion time, request ID, and body kind.
func (repository *RequestHistoryRepository) DeleteBodiesBefore(ctx context.Context, cutoff time.Time, maxRows int) (int64, error) {
	if err := validateRetentionArgs(ctx, cutoff, maxRows); err != nil {
		return 0, err
	}
	if repository == nil || repository.database == nil {
		return 0, ErrHistoryRepositoryUnavailable
	}
	result, err := repository.database.ExecContext(ctx, `
		DELETE FROM request_bodies
		WHERE rowid IN (
			SELECT request_bodies.rowid
			FROM request_bodies JOIN requests ON requests.request_id = request_bodies.request_id
			WHERE requests.finished_at IS NOT NULL AND requests.finished_at < ?
			ORDER BY requests.finished_at ASC, request_bodies.request_id ASC, request_bodies.body_kind ASC
			LIMIT ?
		)`, cutoff.UTC().UnixMicro(), maxRows)
	if err != nil {
		return 0, historyOperationError(err, ErrHistoryRetention)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, ErrHistoryRetention
	}
	return count, nil
}

// DeleteMetadataBefore removes at most maxRows request rows whose finished_at
// is strictly before cutoff. Cascading foreign keys remove remaining bodies.
func (repository *RequestHistoryRepository) DeleteMetadataBefore(ctx context.Context, cutoff time.Time, maxRows int) (int64, error) {
	if err := validateRetentionArgs(ctx, cutoff, maxRows); err != nil {
		return 0, err
	}
	if repository == nil || repository.database == nil {
		return 0, ErrHistoryRepositoryUnavailable
	}
	result, err := repository.database.ExecContext(ctx, `
		DELETE FROM requests
		WHERE request_id IN (
			SELECT request_id FROM requests
			WHERE finished_at IS NOT NULL AND finished_at < ?
			ORDER BY finished_at ASC, request_id ASC
			LIMIT ?
		)`, cutoff.UTC().UnixMicro(), maxRows)
	if err != nil {
		return 0, historyOperationError(err, ErrHistoryRetention)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, ErrHistoryRetention
	}
	return count, nil
}

func validateHistoryBodies(bodies []observability.BodySnapshot) ([]observability.BodySnapshot, error) {
	seen := make(map[observability.BodyKind]struct{}, len(bodies))
	valid := make([]observability.BodySnapshot, 0, len(bodies))
	for _, body := range bodies {
		switch body.Kind {
		case observability.BodyKindClientRequest, observability.BodyKindUpstreamRequest, observability.BodyKindResponse:
		default:
			return nil, ErrHistoryInvalidBody
		}
		if !body.Captured {
			continue
		}
		if _, ok := seen[body.Kind]; ok || !body.Captured || body.OriginalSize < 0 || int64(len(body.Bytes)) > body.OriginalSize || int64(len(body.Bytes)) > RequestBodySchemaSafetyMaxBytes || body.Truncated != (int64(len(body.Bytes)) < body.OriginalSize) {
			return nil, ErrHistoryInvalidBody
		}
		seen[body.Kind] = struct{}{}
		body.Bytes = append([]byte(nil), body.Bytes...)
		valid = append(valid, body)
	}
	return valid, nil
}

func validateHistoryRecord(record HistoryRecord) error {
	if record.APIKeyID != "" && record.KeyID != "" && record.APIKeyID != record.KeyID {
		return ErrHistoryInvalidKey
	}
	if record.APIKeyID == "" {
		record.APIKeyID = record.KeyID
	}
	if !validHistoryRequestID(record.RequestID) || !validHistoryText(record.APIKeyID, 256, true) || !validHistoryText(record.KeyName, 256, true) || !validHistoryText(record.Method, 32, true) || !validHistoryText(record.Path, 2048, true) || !validHistoryText(record.Model, 512, true) {
		return ErrHistoryInvalidRecord
	}
	if record.APIKeyID == "" && record.KeyName != "" {
		return ErrHistoryInvalidKey
	}
	if !oneOf(record.Route, "", "models", "chat_completions", "responses", "generic", "health", "admin") || !oneOf(record.RequestedMode, "", "json", "sse") || !oneOf(record.UpstreamMode, "", "json", "opaque", "sse") || !oneOf(record.DeliveredMode, "", "json", "opaque", "sse") || !oneOf(record.TerminalOutcome, "", "pre_upstream", "upstream_error", "response_error", "complete", "custom_dispatch", "cancelled") || !oneOf(record.ErrorCode, "", "invalid_api_key", "key_disabled", "key_expired", "invalid_request", "model_not_allowed", "request_limit_exceeded", "concurrency_limit_exceeded", "token_limit_exceeded", "budget_exceeded", "upstream_connection_error", "upstream_timeout", "response_transport_error", "conversion_error", "cancelled", "unsupported_response", "gateway_internal_error", "not_found", "conflict") {
		return ErrHistoryInvalidRecord
	}
	if record.UpstreamMode == "" && record.DeliveredMode != "" && record.DeliveredMode != "opaque" || record.DeliveredMode == "sse" && record.UpstreamMode != "sse" || record.UpstreamMode == "opaque" && record.DeliveredMode != "" && record.DeliveredMode != "opaque" || record.DeliveredMode == "opaque" && record.UpstreamMode != "" && record.UpstreamMode != "opaque" || record.RequestedMode == "json" && record.DeliveredMode == "sse" || record.RequestedMode == "sse" && record.DeliveredMode == "json" {
		return ErrHistoryInvalidRecord
	}
	if record.TerminalOutcome == "pre_upstream" && record.UpstreamStarted || record.TerminalOutcome != "" && record.TerminalOutcome != "pre_upstream" && !record.UpstreamStarted {
		return ErrHistoryInvalidRecord
	}
	for _, value := range []OptionalInt64{record.DownstreamStatus, record.UpstreamStatus} {
		if value.Known && (value.Value < 0 || value.Value > 999) {
			return ErrHistoryInvalidRecord
		}
	}
	for _, value := range []OptionalInt64{record.ClientBytes, record.UpstreamBytes, record.DeliveredBytes, record.InputTokens, record.OutputTokens, record.TotalTokens, record.CachedInputTokens, record.ReasoningOutputTokens, record.CostMicros, record.TotalMicros, record.TimeToUpstreamHeadersMicros, record.TimeToFirstByteMicros, record.StreamCloseDelayMicros} {
		if value.Known && value.Value < 0 {
			return ErrHistoryInvalidRecord
		}
	}
	if record.StartedAt.Known && record.FinishedAt.Known && record.FinishedAt.Value < record.StartedAt.Value {
		return ErrHistoryInvalidRecord
	}
	return nil
}

func normalizeHistoryRecord(record HistoryRecord) HistoryRecord {
	if record.APIKeyID == "" {
		record.APIKeyID = record.KeyID
	}
	if record.KeyID == "" {
		record.KeyID = record.APIKeyID
	}
	return record
}

func validateRetentionArgs(ctx context.Context, cutoff time.Time, maxRows int) error {
	if ctx == nil {
		return errors.New("request history retention: nil context")
	}
	if err := ctx.Err(); err != nil {
		return historyContextError(err, ErrHistoryRetention)
	}
	if cutoff.IsZero() || cutoff.Nanosecond()%1_000 != 0 {
		return ErrHistoryInvalidCutoff
	}
	if maxRows <= 0 {
		return ErrHistoryInvalidLimit
	}
	return nil
}

func historyContextError(err, fallback error) error {
	if errors.Is(err, context.Canceled) {
		return fmtHistoryError(fallback, context.Canceled)
	}
	return fmtHistoryError(fallback, context.DeadlineExceeded)
}

func historyOperationError(err error, fallback error) error {
	if errors.Is(err, context.Canceled) {
		return fmtHistoryError(fallback, context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmtHistoryError(fallback, context.DeadlineExceeded)
	}
	return fallback
}

type boundedHistoryError struct{ kind, cause error }

func (err boundedHistoryError) Error() string { return err.kind.Error() }
func (err boundedHistoryError) Unwrap() error { return err.cause }
func (err boundedHistoryError) Is(target error) bool {
	return target == err.kind || errors.Is(err.cause, target)
}
func fmtHistoryError(kind, cause error) error { return boundedHistoryError{kind: kind, cause: cause} }

func validHistoryRequestID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func validHistoryText(value string, max int, emptyOK bool) bool {
	if value == "" {
		return emptyOK
	}
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > max || strings.TrimSpace(value) == "" {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func oneOf(value string, choices ...string) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableInt(value OptionalInt64) any {
	if !value.Known {
		return nil
	}
	return value.Value
}
