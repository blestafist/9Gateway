package storage

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pestit/9gateway/internal/observability"
	"github.com/pestit/9gateway/internal/security"
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
	database   dbQueries
	beginner   txBeginner
	cursorAEAD cipher.AEAD
}

func NewRequestHistoryRepository(database dbQueries) *RequestHistoryRepository {
	repository := &RequestHistoryRepository{database: database, cursorAEAD: newHistoryCursorAEAD()}
	if beginner, ok := database.(txBeginner); ok {
		repository.beginner = beginner
	}
	return repository
}

// ListRequestsFilter selects completed request metadata without selecting any
// request body columns. After and Before are inclusive completion-time bounds.
type ListRequestsFilter struct {
	KeyID  string
	After  *time.Time
	Before *time.Time
}

// RequestListRecord is the scalar, secret-safe projection of a requests row.
// OptionalInt64 preserves SQL NULL separately from a known zero.
type RequestListRecord struct {
	RequestID                   string
	APIKeyID                    string
	KeyName                     string
	Method                      string
	Path                        string
	Route                       string
	Model                       string
	RequestedMode               string
	UpstreamMode                string
	DeliveredMode               string
	DownstreamStatus            OptionalInt64
	UpstreamStatus              OptionalInt64
	TerminalOutcome             string
	UpstreamStarted             bool
	ErrorCode                   string
	ClientBytes                 OptionalInt64
	UpstreamBytes               OptionalInt64
	DeliveredBytes              OptionalInt64
	InputTokens                 OptionalInt64
	OutputTokens                OptionalInt64
	TotalTokens                 OptionalInt64
	CachedInputTokens           OptionalInt64
	ReasoningOutputTokens       OptionalInt64
	CostMicros                  OptionalInt64
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

// RequestDetailRecord is the scalar request projection plus the kinds of
// captured bodies available for a request. Body contents are deliberately not
// part of this record.
type RequestDetailRecord struct {
	RequestListRecord
	HasBodies []string
}

// BodyContent is one immutable captured request or response payload and its
// storage metadata. Bytes are returned exactly as stored; callers must not
// decode or otherwise transform them.
type BodyContent struct {
	Bytes        []byte
	OriginalSize int64
	Truncated    bool
}

type requestListCursor struct {
	// SnapshotSequence is the insertion high-water mark captured at the
	// beginning of a traversal. It makes timestamp+ID pagination a traversal of
	// one immutable initial population rather than a moving live table.
	SnapshotSequence int64
	FinishedAt       int64
	FinishedKnown    bool
	RequestID        string
}

const maxRequestCursorBytes = security.MaxCursorBytes

var requestCursorMagic = [3]byte{'r', 'q', 1}

// SetCursorSecret makes request cursors invalid after the server secret
// changes. It is called during process wiring, before the repository is shared.
func (repository *RequestHistoryRepository) SetCursorSecret(secret []byte) {
	if repository == nil || len(secret) == 0 {
		return
	}
	digest := sha256Sum(secret)
	if block, err := aes.NewCipher(digest[:]); err == nil {
		repository.cursorAEAD, _ = cipher.NewGCM(block)
	}
}

// ListRequests returns newest-first request metadata and an opaque bookmark
// for the following page. The query intentionally never touches
// request_bodies, including for existence checks.
func (repository *RequestHistoryRepository) ListRequests(ctx context.Context, filter ListRequestsFilter, limit int, cursor string) ([]RequestListRecord, string, error) {
	if repository == nil {
		return nil, "", ErrHistoryRepositoryUnavailable
	}
	return listRequests(repository.database, repository.cursorAEAD, ctx, filter, limit, cursor)
}

// GetRequestByID returns one request's metadata and the available body kinds.
// The metadata query is indexed by request_id and never selects body BLOBs.
// When the database supports transactions, both reads share a SQLite snapshot
// so a concurrent body cleanup cannot make the detail read fail or mix states.
func (repository *RequestHistoryRepository) GetRequestByID(ctx context.Context, requestID string) (*RequestDetailRecord, error) {
	if ctx == nil {
		return nil, errors.New("get request detail: nil context")
	}
	if !security.ValidateRequestID(requestID) {
		return nil, ErrHistoryInvalidRecord
	}
	if repository == nil || repository.database == nil {
		return nil, ErrHistoryRepositoryUnavailable
	}
	return getRequestByID(repository.database, repository.beginner, ctx, requestID)
}

// GetRequestBody returns one captured body after validating the storage
// invariants that protect the HTTP download boundary. A read transaction keeps
// the row and its bytes in one SQLite snapshot while retention may delete rows.
func (repository *RequestHistoryRepository) GetRequestBody(ctx context.Context, requestID, kind string) (*BodyContent, error) {
	if ctx == nil {
		return nil, errors.New("get request body: nil context")
	}
	if !security.ValidateRequestID(requestID) {
		return nil, ErrHistoryInvalidRecord
	}
	if !validRequestBodyKind(kind) {
		return nil, ErrHistoryInvalidBody
	}
	if repository == nil || repository.database == nil {
		return nil, ErrHistoryRepositoryUnavailable
	}
	return getRequestBody(repository.database, repository.beginner, ctx, requestID, kind)
}

const requestMetadataQuery = `SELECT request_id, api_key_id, key_name, method, path, route, model,
	requested_mode, upstream_mode, delivered_mode, downstream_status,
	upstream_status, terminal_outcome, upstream_started, error_code,
	client_bytes, upstream_bytes, delivered_bytes, input_tokens, output_tokens,
	total_tokens, cached_input_tokens, reasoning_output_tokens, cost_micros,
	started_at, upstream_started_at, upstream_headers_at, first_byte_at,
	finished_at, total_micros, time_to_upstream_headers_micros,
	time_to_first_byte_micros, stream_close_delay_micros FROM requests WHERE request_id = ?`

func getRequestByID(database dbQueries, beginner txBeginner, ctx context.Context, requestID string) (*RequestDetailRecord, error) {
	if beginner != nil {
		tx, err := beginner.BeginTx(ctx, nil)
		if err != nil {
			return nil, errors.New("get request detail: database read failed")
		}
		committed := false
		defer func() {
			if !committed {
				_ = tx.Rollback()
			}
		}()
		record, err := scanRequestDetail(tx.QueryRowContext(ctx, requestMetadataQuery, requestID), tx, ctx, requestID)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, errors.New("get request detail: database read failed")
		}
		committed = true
		return record, nil
	}
	return scanRequestDetail(database.QueryRowContext(ctx, requestMetadataQuery, requestID), database, ctx, requestID)
}

type sqlScanner interface {
	Scan(...any) error
}

func scanRequestDetail(metadata sqlScanner, bodyDB dbQueries, ctx context.Context, requestID string) (*RequestDetailRecord, error) {
	record, err := scanRequestMetadata(metadata)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	bodies, err := bodyKinds(bodyDB, ctx, requestID)
	if err != nil {
		return nil, errors.New("get request detail: database read failed")
	}
	record.HasBodies = bodies
	return record, nil
}

func scanRequestMetadata(scanner sqlScanner) (*RequestDetailRecord, error) {
	var record RequestDetailRecord
	var keyID, keyName, method, path, route, model, requested, upstream, delivered, outcome, errorCode sql.NullString
	var status, upstreamStatus, clientBytes, upstreamBytes, deliveredBytes, input, output, total, cached, reasoning, cost sql.NullInt64
	var started, upstreamStartedAt, headers, firstByte, finished, totalMicros, headerLatency, firstByteLatency, closeDelay sql.NullInt64
	var upstreamStarted int64
	err := scanner.Scan(&record.RequestID, &keyID, &keyName, &method, &path, &route, &model, &requested, &upstream, &delivered, &status, &upstreamStatus, &outcome, &upstreamStarted, &errorCode, &clientBytes, &upstreamBytes, &deliveredBytes, &input, &output, &total, &cached, &reasoning, &cost, &started, &upstreamStartedAt, &headers, &firstByte, &finished, &totalMicros, &headerLatency, &firstByteLatency, &closeDelay)
	if err != nil {
		return nil, err
	}
	if upstreamStarted != 0 && upstreamStarted != 1 {
		return nil, ErrHistoryInvalidRecord
	}
	text := func(value sql.NullString) string {
		if value.Valid {
			return value.String
		}
		return ""
	}
	number := func(value sql.NullInt64) OptionalInt64 {
		if value.Valid {
			return KnownInt64(value.Int64)
		}
		return OptionalInt64{}
	}
	record.APIKeyID, record.KeyName, record.Method, record.Path, record.Route, record.Model = text(keyID), text(keyName), text(method), text(path), text(route), text(model)
	record.RequestedMode, record.UpstreamMode, record.DeliveredMode, record.TerminalOutcome, record.ErrorCode = text(requested), text(upstream), text(delivered), text(outcome), text(errorCode)
	record.UpstreamStarted = upstreamStarted == 1
	record.DownstreamStatus, record.UpstreamStatus = number(status), number(upstreamStatus)
	record.ClientBytes, record.UpstreamBytes, record.DeliveredBytes = number(clientBytes), number(upstreamBytes), number(deliveredBytes)
	record.InputTokens, record.OutputTokens, record.TotalTokens, record.CachedInputTokens, record.ReasoningOutputTokens, record.CostMicros = number(input), number(output), number(total), number(cached), number(reasoning), number(cost)
	record.StartedAt, record.UpstreamStartedAt, record.UpstreamHeadersAt, record.FirstByteAt, record.FinishedAt = number(started), number(upstreamStartedAt), number(headers), number(firstByte), number(finished)
	record.TotalMicros, record.TimeToUpstreamHeadersMicros, record.TimeToFirstByteMicros, record.StreamCloseDelayMicros = number(totalMicros), number(headerLatency), number(firstByteLatency), number(closeDelay)
	return &record, nil
}

func bodyKinds(database dbQueries, ctx context.Context, requestID string) ([]string, error) {
	rows, err := database.QueryContext(ctx, `SELECT body_kind FROM request_bodies WHERE request_id = ? AND body_kind IN ('client_request', 'upstream_request', 'response') ORDER BY CASE body_kind WHEN 'client_request' THEN 1 WHEN 'upstream_request' THEN 2 WHEN 'response' THEN 3 END`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]string, 0, 3)
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			return nil, err
		}
		result = append(result, kind)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

const maxRequestBodyDownloadBytes int64 = 10 * 1024 * 1024

func getRequestBody(database dbQueries, beginner txBeginner, ctx context.Context, requestID, kind string) (*BodyContent, error) {
	if database == nil {
		return nil, ErrHistoryRepositoryUnavailable
	}
	const query = `SELECT body, original_size, truncated, typeof(body), typeof(original_size), typeof(truncated) FROM request_bodies WHERE request_id = ? AND body_kind = ?`
	if beginner != nil {
		tx, err := beginner.BeginTx(ctx, nil)
		if err != nil {
			return nil, errors.New("get request body: database read failed")
		}
		committed := false
		defer func() {
			if !committed {
				_ = tx.Rollback()
			}
		}()
		content, err := scanRequestBody(tx.QueryRowContext(ctx, query, requestID, kind))
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, errors.New("get request body: database read failed")
		}
		committed = true
		return content, nil
	}
	return scanRequestBody(database.QueryRowContext(ctx, query, requestID, kind))
}

func validRequestBodyKind(kind string) bool {
	switch kind {
	case "client_request", "upstream_request", "response":
		return true
	default:
		return false
	}
}

func scanRequestBody(scanner sqlScanner) (*BodyContent, error) {
	var content BodyContent
	var truncated int64
	var bodyType, originalType, truncatedType string
	if err := scanner.Scan(&content.Bytes, &content.OriginalSize, &truncated, &bodyType, &originalType, &truncatedType); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, errors.New("get request body: database read failed")
	}
	storedSize := int64(len(content.Bytes))
	if bodyType != "blob" || originalType != "integer" || truncatedType != "integer" || content.OriginalSize < 0 || storedSize > RequestBodySchemaSafetyMaxBytes || storedSize > maxRequestBodyDownloadBytes || (truncated != 0 && truncated != 1) {
		return nil, ErrHistoryInvalidBody
	}
	content.Truncated = truncated == 1
	if storedSize > content.OriginalSize || content.Truncated != (storedSize < content.OriginalSize) {
		return nil, ErrHistoryInvalidBody
	}
	return &content, nil
}

// listRequests is shared with the API-key repository because the admin handler
// already receives that repository while the history writer owns a separate
// repository value over the same database.
func listRequests(database dbQueries, signer cipher.AEAD, ctx context.Context, filter ListRequestsFilter, limit int, cursor string) ([]RequestListRecord, string, error) {
	if ctx == nil {
		return nil, "", errors.New("list requests: nil context")
	}
	if database == nil || signer == nil {
		return nil, "", ErrHistoryRepositoryUnavailable
	}
	if limit <= 0 || limit > 500 {
		return nil, "", ErrInvalidCursor
	}
	if filter.KeyID != "" && !validRequestKeyID(filter.KeyID) {
		return nil, "", ErrInvalidCursor
	}
	if filter.After != nil && filter.Before != nil && filter.After.After(*filter.Before) {
		return nil, "", ErrInvalidCursor
	}
	bookmark, err := decodeRequestCursor(signer, cursor)
	if err != nil {
		return nil, "", err
	}

	snapshotSequence := int64(0)
	if bookmark != nil {
		snapshotSequence = bookmark.SnapshotSequence
	} else {
		if err := database.QueryRowContext(ctx, `SELECT COALESCE(MAX(insertion_seq), 0) FROM requests`).Scan(&snapshotSequence); err != nil {
			return nil, "", errors.New("list requests: database read failed")
		}
	}
	query := `SELECT request_id, api_key_id, key_name, method, path, route, model,
		requested_mode, upstream_mode, delivered_mode, downstream_status,
		upstream_status, terminal_outcome, upstream_started, error_code,
		client_bytes, upstream_bytes, delivered_bytes, input_tokens, output_tokens,
		total_tokens, cached_input_tokens, reasoning_output_tokens, cost_micros,
		started_at, upstream_started_at, upstream_headers_at, first_byte_at,
		finished_at, total_micros, time_to_upstream_headers_micros,
		time_to_first_byte_micros, stream_close_delay_micros FROM requests`
	conditions := []string{"insertion_seq <= ?"}
	args := make([]any, 0, 10)
	args = append(args, snapshotSequence)
	if filter.KeyID != "" {
		conditions = append(conditions, "api_key_id = ?")
		args = append(args, filter.KeyID)
	}
	if filter.After != nil {
		conditions = append(conditions, "finished_at >= ?")
		args = append(args, filter.After.UTC().UnixMicro())
	}
	if filter.Before != nil {
		conditions = append(conditions, "finished_at <= ?")
		args = append(args, filter.Before.UTC().UnixMicro())
	}
	if bookmark != nil {
		if bookmark.FinishedKnown {
			conditions = append(conditions, "(finished_at IS NULL OR finished_at < ? OR (finished_at = ? AND request_id < ?))")
			args = append(args, bookmark.FinishedAt, bookmark.FinishedAt, bookmark.RequestID)
		} else {
			conditions = append(conditions, "finished_at IS NULL AND request_id < ?")
			args = append(args, bookmark.RequestID)
		}
	}
	if len(conditions) != 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY finished_at DESC, request_id DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", errors.New("list requests: database read failed")
	}
	defer rows.Close()
	result := make([]RequestListRecord, 0, limit)
	for rows.Next() {
		record, err := scanRequestListRecord(rows)
		if err != nil {
			return nil, "", errors.New("list requests: database read failed")
		}
		result = append(result, record)
	}
	if err := rows.Err(); err != nil {
		return nil, "", errors.New("list requests: database read failed")
	}
	if len(result) <= limit {
		return result, "", nil
	}
	last := result[limit-1]
	result = result[:limit]
	next, err := encodeRequestCursor(signer, requestListCursor{SnapshotSequence: snapshotSequence, FinishedAt: last.FinishedAt.Value, FinishedKnown: last.FinishedAt.Known, RequestID: last.RequestID})
	if err != nil {
		return nil, "", err
	}
	return result, next, nil
}

func scanRequestListRecord(rows *sql.Rows) (RequestListRecord, error) {
	var record RequestListRecord
	var keyID, keyName, method, path, route, model, requested, upstream, delivered, outcome, errorCode sql.NullString
	var status, upstreamStatus, clientBytes, upstreamBytes, deliveredBytes, input, output, total, cached, reasoning, cost sql.NullInt64
	var started, upstreamStartedAt, headers, firstByte, finished, totalMicros, headerLatency, firstByteLatency, closeDelay sql.NullInt64
	var upstreamStarted int64
	err := rows.Scan(&record.RequestID, &keyID, &keyName, &method, &path, &route, &model, &requested, &upstream, &delivered, &status, &upstreamStatus, &outcome, &upstreamStarted, &errorCode, &clientBytes, &upstreamBytes, &deliveredBytes, &input, &output, &total, &cached, &reasoning, &cost, &started, &upstreamStartedAt, &headers, &firstByte, &finished, &totalMicros, &headerLatency, &firstByteLatency, &closeDelay)
	if err != nil {
		return RequestListRecord{}, err
	}
	if upstreamStarted != 0 && upstreamStarted != 1 {
		return RequestListRecord{}, ErrHistoryInvalidRecord
	}
	text := func(value sql.NullString) string {
		if value.Valid {
			return value.String
		}
		return ""
	}
	number := func(value sql.NullInt64) OptionalInt64 {
		if value.Valid {
			return KnownInt64(value.Int64)
		}
		return OptionalInt64{}
	}
	record.APIKeyID, record.KeyName, record.Method, record.Path, record.Route, record.Model = text(keyID), text(keyName), text(method), text(path), text(route), text(model)
	record.RequestedMode, record.UpstreamMode, record.DeliveredMode, record.TerminalOutcome, record.ErrorCode = text(requested), text(upstream), text(delivered), text(outcome), text(errorCode)
	record.UpstreamStarted = upstreamStarted == 1
	record.DownstreamStatus, record.UpstreamStatus = number(status), number(upstreamStatus)
	record.ClientBytes, record.UpstreamBytes, record.DeliveredBytes = number(clientBytes), number(upstreamBytes), number(deliveredBytes)
	record.InputTokens, record.OutputTokens, record.TotalTokens, record.CachedInputTokens, record.ReasoningOutputTokens, record.CostMicros = number(input), number(output), number(total), number(cached), number(reasoning), number(cost)
	record.StartedAt, record.UpstreamStartedAt, record.UpstreamHeadersAt, record.FirstByteAt, record.FinishedAt = number(started), number(upstreamStartedAt), number(headers), number(firstByte), number(finished)
	record.TotalMicros, record.TimeToUpstreamHeadersMicros, record.TimeToFirstByteMicros, record.StreamCloseDelayMicros = number(totalMicros), number(headerLatency), number(firstByteLatency), number(closeDelay)
	return record, nil
}

func newHistoryCursorAEAD() cipher.AEAD {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil
	}
	result, err := cipher.NewGCM(block)
	if err != nil {
		return nil
	}
	return result
}

func validRequestKeyID(value string) bool {
	return security.ValidateIdentifier(value)
}

// sha256Sum is kept local to avoid making cursor cryptography part of the
// public storage API.
func sha256Sum(value []byte) [32]byte { return sha256.Sum256(value) }

func encodeRequestCursor(signer cipher.AEAD, cursor requestListCursor) (string, error) {
	if signer == nil || cursor.SnapshotSequence <= 0 || cursor.RequestID == "" || len(cursor.RequestID) > 256 {
		return "", ErrInvalidCursor
	}
	if !validHistoryRequestID(cursor.RequestID) {
		return "", ErrInvalidCursor
	}
	payload := make([]byte, 22+len(cursor.RequestID))
	copy(payload, requestCursorMagic[:])
	if cursor.FinishedKnown {
		payload[3] = 1
	}
	binary.BigEndian.PutUint64(payload[4:], uint64(cursor.SnapshotSequence))
	binary.BigEndian.PutUint64(payload[12:], uint64(cursor.FinishedAt))
	binary.BigEndian.PutUint16(payload[20:], uint16(len(cursor.RequestID)))
	copy(payload[22:], cursor.RequestID)
	nonce := make([]byte, signer.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", ErrInvalidCursor
	}
	return base64.RawURLEncoding.EncodeToString(signer.Seal(nonce, nonce, payload, nil)), nil
}

func decodeRequestCursor(signer cipher.AEAD, value string) (*requestListCursor, error) {
	if value == "" {
		return nil, nil
	}
	if len(value) > maxRequestCursorBytes || signer == nil || !security.ValidateCursorSyntax(value) {
		return nil, ErrInvalidCursor
	}
	sealed, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(sealed) <= signer.NonceSize() {
		return nil, ErrInvalidCursor
	}
	payload, err := signer.Open(nil, sealed[:signer.NonceSize()], sealed[signer.NonceSize():], nil)
	if err != nil || len(payload) < 22 || payload[0] != requestCursorMagic[0] || payload[1] != requestCursorMagic[1] || payload[2] != requestCursorMagic[2] || (payload[3] != 0 && payload[3] != 1) {
		return nil, ErrInvalidCursor
	}
	snapshotSequence := int64(binary.BigEndian.Uint64(payload[4:12]))
	idLength := int(binary.BigEndian.Uint16(payload[20:]))
	if snapshotSequence <= 0 || idLength == 0 || idLength != len(payload)-22 || idLength > security.MaxIdentifierBytes || !security.ValidateRequestID(string(payload[22:])) {
		return nil, ErrInvalidCursor
	}
	return &requestListCursor{SnapshotSequence: snapshotSequence, FinishedKnown: payload[3] == 1, FinishedAt: int64(binary.BigEndian.Uint64(payload[12:20])), RequestID: string(payload[22:])}, nil
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
			FROM requests
			JOIN request_bodies ON requests.request_id = request_bodies.request_id
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
	return security.ValidateRequestID(value)
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
