package httpserver

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/pestit/9gateway/internal/accounting"
)

// Bifrost provenance review: reference commit
// 03ab391865710462302bbcf52dca2f32682b91b5 (branch dev) was inspected at
// .references/bifrost/plugins/logging/writer.go and strip.go for bounded
// completion/logging boundaries. The reference is Apache-2.0; its
// THIRD_PARTY_NOTICES.md was reviewed. This canonical record is an independent
// implementation, copies no source, and adds no dependency.

// RequestID is the gateway's opaque, non-secret request identifier. IDs issued
// by the gateway are 128-bit lowercase hexadecimal values. It is an alias so
// legacy logger callers can still provide string IDs while canonical builders
// validate the domain boundary.
type RequestID = string

// NewRequestTraceID is a descriptive alias for NewRequestID.
func NewRequestTraceID(value string) (RequestID, error) { return NewRequestID(value) }

// RouteClass is the bounded route vocabulary retained by request history. It
// is intentionally not an arbitrary URL or escaped path.
type RouteClass uint8

const (
	RouteClassUnknown RouteClass = iota
	RouteClassModels
	RouteClassChatCompletions
	RouteClassResponses
	RouteClassGeneric
	RouteClassHealth
	RouteClassAdmin
)

// Descriptive aliases keep route vocabulary readable at call sites.
const (
	RouteUnknown         = RouteClassUnknown
	RouteModels          = RouteClassModels
	RouteChatCompletions = RouteClassChatCompletions
	RouteResponses       = RouteClassResponses
	RouteGeneric         = RouteClassGeneric
	RouteHealth          = RouteClassHealth
	RouteAdmin           = RouteClassAdmin
)

// ClassifyRoute maps only known, bounded routes; arbitrary /v1 paths remain
// generic and are never retained as an unbounded route string.
func ClassifyRoute(method, path string) RouteClass {
	switch {
	case method == "GET" && path == "/health":
		return RouteClassHealth
	case method == "GET" && path == "/v1/models":
		return RouteClassModels
	case method == "POST" && path == "/v1/chat/completions":
		return RouteClassChatCompletions
	case method == "POST" && path == "/v1/responses":
		return RouteClassResponses
	case strings.HasPrefix(path, "/v1/"):
		return RouteClassGeneric
	case strings.HasPrefix(path, "/admin"):
		return RouteClassAdmin
	default:
		return RouteClassUnknown
	}
}

func (route RouteClass) String() string {
	switch route {
	case RouteClassModels:
		return "models"
	case RouteClassChatCompletions:
		return "chat_completions"
	case RouteClassResponses:
		return "responses"
	case RouteClassGeneric:
		return "generic"
	case RouteClassHealth:
		return "health"
	case RouteClassAdmin:
		return "admin"
	default:
		return "unknown"
	}
}

// RequestMode describes the mode requested by a client. Unknown is distinct
// from a known JSON or SSE request; opaque is not a valid requested mode.
type RequestMode uint8

const (
	RequestModeUnknown RequestMode = iota
	RequestModeJSON
	RequestModeSSE
)

type StreamMode = RequestMode

const (
	StreamModeUnknown = RequestModeUnknown
	StreamModeJSON    = RequestModeJSON
	StreamModeSSE     = RequestModeSSE
)

// ResponseMode identifies the actual or delivered response representation.
// The existing JSON, SSE and opaque values remain the transport classifier's
// values. Its zero value is unknown, not JSON or opaque.
const ResponseModeUnknown ResponseMode = ""

// OptionalStatus is an immutable HTTP status with an explicit presence bit.
// A known zero status is rejected because zero is not an HTTP status.
type OptionalStatus struct {
	value int
	known bool
}

type HTTPStatus = OptionalStatus

func UnknownStatus() OptionalStatus { return OptionalStatus{} }

func NewStatus(status int) (OptionalStatus, error) {
	if status < 0 || status > 999 {
		return OptionalStatus{}, errors.New("completion: invalid status")
	}
	return OptionalStatus{value: status, known: true}, nil
}

// NewRequestID validates and types an ID generated or received at the gateway
// boundary. It does not accept credentials, URLs, or arbitrary log text.
func NewRequestID(value string) (RequestID, error) {
	if !validRequestID(value) {
		return "", errors.New("completion: malformed request ID")
	}
	return value, nil
}

func (status OptionalStatus) Known() bool        { return status.known }
func (status OptionalStatus) Value() (int, bool) { return status.value, status.known }

// ByteCount is a checked, optional count of bytes. Counts use bytes, never
// wire chunks or tokens, and are bounded by SQLite's signed INTEGER range.
type ByteCount struct {
	value int64
	known bool
}

type OptionalByteCount = ByteCount

func UnknownByteCount() ByteCount { return ByteCount{} }

func NewByteCount(value int64) (ByteCount, error) {
	if value < 0 {
		return ByteCount{}, errors.New("completion: byte count is negative")
	}
	return ByteCount{value: value, known: true}, nil
}

func (count ByteCount) Known() bool          { return count.known }
func (count ByteCount) Value() (int64, bool) { return count.value, count.known }

// UnixMicros is an optional canonical UTC wall-clock timestamp. Persisted
// timestamps are signed Unix microseconds; no time.Location is retained.
type UnixMicros struct {
	value int64
	known bool
}

type WallTimestamp = UnixMicros

func UnknownUnixMicros() UnixMicros { return UnixMicros{} }

func NewUnixMicros(at time.Time) (UnixMicros, error) {
	seconds := at.Unix()
	nanos := int64(at.Nanosecond()) / 1_000
	maxSeconds := int64(math.MaxInt64 / 1_000_000)
	minSeconds := int64(math.MinInt64 / 1_000_000)
	if seconds > maxSeconds || seconds == maxSeconds && nanos > math.MaxInt64%1_000_000 || seconds < minSeconds || seconds == minSeconds && nanos < math.MinInt64%1_000_000 {
		return UnixMicros{}, errors.New("completion: timestamp overflow")
	}
	micros := seconds*1_000_000 + nanos
	return UnixMicros{value: micros, known: true}, nil
}

func NewUnixMicrosValue(value int64) UnixMicros { return UnixMicros{value: value, known: true} }
func (stamp UnixMicros) Known() bool            { return stamp.known }
func (stamp UnixMicros) Value() (int64, bool)   { return stamp.value, stamp.known }
func (stamp UnixMicros) Time() (time.Time, bool) {
	if !stamp.known {
		return time.Time{}, false
	}
	return time.Unix(stamp.value/1_000_000, (stamp.value%1_000_000)*1_000).UTC(), true
}

// DurationMicros is an optional persistable elapsed duration. It is always a
// non-negative integer number of microseconds. In-process callers may measure
// elapsed time using monotonic time before converting at the boundary.
type DurationMicros struct {
	value int64
	known bool
}

type ElapsedMicros = DurationMicros

func UnknownDurationMicros() DurationMicros { return DurationMicros{} }

func NewDurationMicros(duration time.Duration) (DurationMicros, error) {
	if duration < 0 || duration%time.Microsecond != 0 {
		return DurationMicros{}, errors.New("completion: duration must be non-negative whole microseconds")
	}
	return DurationMicros{value: int64(duration / time.Microsecond), known: true}, nil
}

func NewDurationMicrosValue(value int64) (DurationMicros, error) {
	if value < 0 {
		return DurationMicros{}, errors.New("completion: duration is negative")
	}
	return DurationMicros{value: value, known: true}, nil
}

func (duration DurationMicros) Known() bool          { return duration.known }
func (duration DurationMicros) Value() (int64, bool) { return duration.value, duration.known }
func (duration DurationMicros) Duration() (time.Duration, bool) {
	if !duration.known || duration.value > int64(math.MaxInt64)/int64(time.Microsecond) {
		return 0, false
	}
	return time.Duration(duration.value) * time.Microsecond, true
}

// CompletionTiming contains only scalar timing facts. Wall timestamps are
// canonical Unix microseconds; elapsed values are persistable microseconds.
type CompletionTiming struct {
	StartedAt         UnixMicros
	UpstreamHeadersAt UnixMicros
	FirstByteAt       UnixMicros
	FinishedAt        UnixMicros
	Total             DurationMicros
	TimeToFirstByte   DurationMicros
}

func (timing CompletionTiming) MarshalJSON() ([]byte, error) {
	optionalTimestamp := func(value UnixMicros) any {
		if !value.known {
			return nil
		}
		return value.value
	}
	optionalDuration := func(value DurationMicros) any {
		if !value.known {
			return nil
		}
		return value.value
	}
	return json.Marshal(struct {
		StartedAt             any `json:"started_at"`
		UpstreamHeadersAt     any `json:"upstream_headers_at"`
		FirstByteAt           any `json:"first_byte_at"`
		FinishedAt            any `json:"finished_at"`
		TotalMicros           any `json:"total_micros"`
		TimeToFirstByteMicros any `json:"time_to_first_byte_micros"`
	}{optionalTimestamp(timing.StartedAt), optionalTimestamp(timing.UpstreamHeadersAt), optionalTimestamp(timing.FirstByteAt), optionalTimestamp(timing.FinishedAt), optionalDuration(timing.Total), optionalDuration(timing.TimeToFirstByte)})
}

// SafeErrorCode is a closed, secret-safe error vocabulary. It never contains
// an error message, URL, SQL detail, credential, or payload fragment.
type SafeErrorCode uint8

// GatewayErrorCode is the public name for the closed gateway-owned error
// vocabulary. Values outside the constants are rejected at record boundaries.
type GatewayErrorCode = SafeErrorCode

const (
	ErrorCodeUnknown SafeErrorCode = iota
	ErrorCodeInvalidAPIKey
	ErrorCodeKeyDisabled
	ErrorCodeKeyExpired
	ErrorCodeInvalidRequest
	ErrorCodeModelNotAllowed
	ErrorCodeRequestLimit
	ErrorCodeConcurrencyLimit
	ErrorCodeTokenLimit
	ErrorCodeBudgetLimit
	ErrorCodeUpstreamConnection
	ErrorCodeUpstreamTimeout
	ErrorCodeResponseTransport
	ErrorCodeConversion
	ErrorCodeCancellation
	ErrorCodeUnsupportedResponse
	ErrorCodeInternal
	ErrorCodeNotFound
	ErrorCodeConflict
)

const (
	GatewayErrorUnknown             = ErrorCodeUnknown
	GatewayErrorInvalidAPIKey       = ErrorCodeInvalidAPIKey
	GatewayErrorKeyDisabled         = ErrorCodeKeyDisabled
	GatewayErrorKeyExpired          = ErrorCodeKeyExpired
	GatewayErrorInvalidRequest      = ErrorCodeInvalidRequest
	GatewayErrorModelNotAllowed     = ErrorCodeModelNotAllowed
	GatewayErrorRequestLimit        = ErrorCodeRequestLimit
	GatewayErrorConcurrencyLimit    = ErrorCodeConcurrencyLimit
	GatewayErrorTokenLimit          = ErrorCodeTokenLimit
	GatewayErrorBudgetLimit         = ErrorCodeBudgetLimit
	GatewayErrorUpstreamConnection  = ErrorCodeUpstreamConnection
	GatewayErrorUpstreamTimeout     = ErrorCodeUpstreamTimeout
	GatewayErrorResponseTransport   = ErrorCodeResponseTransport
	GatewayErrorConversion          = ErrorCodeConversion
	GatewayErrorCancellation        = ErrorCodeCancellation
	GatewayErrorUnsupportedResponse = ErrorCodeUnsupportedResponse
	GatewayErrorInternal            = ErrorCodeInternal
	GatewayErrorNotFound            = ErrorCodeNotFound
	GatewayErrorConflict            = ErrorCodeConflict
)

func (code SafeErrorCode) String() string {
	names := [...]string{"unknown", "invalid_api_key", "key_disabled", "key_expired", "invalid_request", "model_not_allowed", "request_limit_exceeded", "concurrency_limit_exceeded", "token_limit_exceeded", "budget_exceeded", "upstream_connection_error", "upstream_timeout", "response_transport_error", "conversion_error", "cancelled", "unsupported_response", "gateway_internal_error", "not_found", "conflict"}
	if int(code) >= len(names) {
		return "unknown"
	}
	return names[code]
}

// CompletionRecordInput is the only construction input for a canonical
// record. It contains values, not HTTP objects or ownership-bearing runtime
// handles. accounting.Usage and accounting.Money are immutable value types.
type CompletionRecordInput struct {
	RequestID RequestID
	KeyID     string
	KeyName   string
	Method    string
	Route     RouteClass
	// RouteClass is the descriptive spelling of Route. If both are supplied,
	// they must agree.
	RouteClass RouteClass
	Model      string

	RequestedMode RequestMode
	UpstreamMode  ResponseMode
	// ActualUpstreamMode is the descriptive spelling of UpstreamMode.
	ActualUpstreamMode ResponseMode
	DeliveredMode      ResponseMode

	DownstreamStatus OptionalStatus
	UpstreamStatus   OptionalStatus
	Terminal         TerminalMetadata
	ErrorCode        SafeErrorCode
	// SafeErrorCode is the descriptive spelling of ErrorCode.
	SafeErrorCode SafeErrorCode

	ClientBytes    ByteCount
	UpstreamBytes  ByteCount
	DeliveredBytes ByteCount
	Usage          accounting.Usage
	Cost           accounting.Money
	Timing         CompletionTiming
}

type RequestTraceInput = CompletionRecordInput

// CompletionRecord is an immutable, storage-independent canonical request
// trace. Deprecated fields are retained solely for source compatibility with
// the pre-T121 logger; canonical consumers must use the typed fields above.
type CompletionRecord struct {
	RequestID  RequestID
	KeyID      string
	KeyName    string
	Method     string
	Route      RouteClass
	RouteClass RouteClass
	Model      string

	RequestedMode      RequestMode
	UpstreamMode       ResponseMode
	ActualUpstreamMode ResponseMode
	DeliveredMode      ResponseMode

	DownstreamStatus OptionalStatus
	UpstreamStatus   OptionalStatus
	Terminal         TerminalMetadata
	ErrorCode        SafeErrorCode
	SafeErrorCode    SafeErrorCode

	ClientBytes    ByteCount
	UpstreamBytes  ByteCount
	DeliveredBytes ByteCount
	Usage          accounting.Usage
	Cost           accounting.Money
	Timing         CompletionTiming

	// Deprecated compatibility fields. They are copied scalar values and are
	// not used as canonical state.
	Path     string
	Status   int
	Duration time.Duration
}

// NewRequestTraceRecord is the storage-independent constructor spelling.
func NewRequestTraceRecord(input CompletionRecordInput) (CompletionRecord, error) {
	return NewCompletionRecord(input)
}

func NewCompletionRecord(input CompletionRecordInput) (CompletionRecord, error) {
	if input.Route != RouteClassUnknown && input.RouteClass != RouteClassUnknown && input.Route != input.RouteClass {
		return CompletionRecord{}, errors.New("completion: conflicting route classes")
	}
	if input.UpstreamMode != ResponseModeUnknown && input.ActualUpstreamMode != ResponseModeUnknown && input.UpstreamMode != input.ActualUpstreamMode {
		return CompletionRecord{}, errors.New("completion: conflicting upstream modes")
	}
	if input.ErrorCode != ErrorCodeUnknown && input.SafeErrorCode != ErrorCodeUnknown && input.ErrorCode != input.SafeErrorCode {
		return CompletionRecord{}, errors.New("completion: conflicting error codes")
	}
	input = normalizeCompletionInput(input)
	if err := validateCompletionInput(input); err != nil {
		return CompletionRecord{}, err
	}
	return CompletionRecord{
		RequestID: input.RequestID, KeyID: input.KeyID, KeyName: input.KeyName,
		Method: input.Method, Route: input.Route, Model: input.Model,
		RouteClass:    input.Route,
		RequestedMode: input.RequestedMode, UpstreamMode: input.UpstreamMode,
		ActualUpstreamMode: input.UpstreamMode,
		DeliveredMode:      input.DeliveredMode, DownstreamStatus: input.DownstreamStatus,
		UpstreamStatus: input.UpstreamStatus, Terminal: input.Terminal,
		ErrorCode: input.ErrorCode, SafeErrorCode: input.ErrorCode, ClientBytes: input.ClientBytes,
		UpstreamBytes: input.UpstreamBytes, DeliveredBytes: input.DeliveredBytes,
		Usage: input.Usage, Cost: input.Cost, Timing: input.Timing,
	}, nil
}

// Snapshot validates and returns an independent value copy. Since the record
// owns no slices, maps, pointers, or interfaces, this is also a defensive
// accessor boundary.
func (record CompletionRecord) Snapshot() (CompletionRecord, error) {
	return NewCompletionRecord(record.input())
}

func (record CompletionRecord) Validate() error { return validateCompletionInput(record.input()) }

func (record CompletionRecord) String() string {
	return fmt.Sprintf("completion request_id=%s route=%s outcome=%s", safeRequestID(record.RequestID), record.Route, record.Terminal.Outcome)
}

// RequestTraceRecord is the architecture-facing name for CompletionRecord.
type RequestTraceRecord = CompletionRecord

// CanonicalCompletionRecord is a descriptive alias used by storage and
// telemetry integrations without coupling them to HTTP handling.
type CanonicalCompletionRecord = CompletionRecord

func (record CompletionRecord) GoString() string { return record.String() }

// MarshalJSON emits only the canonical, bounded fields. Optional scalar
// values are null when unknown, preserving unknown from known zero without
// exposing implementation details of accounting values.
func (record CompletionRecord) MarshalJSON() ([]byte, error) {
	optionalInt := func(value int64, known bool) any {
		if !known {
			return nil
		}
		return value
	}
	optionalStatus := func(status OptionalStatus) any {
		if !status.known {
			return nil
		}
		return status.value
	}
	optionalMode := func(mode ResponseMode) any {
		if mode == ResponseModeUnknown {
			return nil
		}
		return mode
	}
	input, inputKnown := record.Usage.Input().Value()
	output, outputKnown := record.Usage.Output().Value()
	total, totalKnown := record.Usage.Total().Value()
	cached, cachedKnown := record.Usage.CachedInput().Value()
	reasoning, reasoningKnown := record.Usage.ReasoningOutput().Value()
	cost := any(nil)
	if record.Cost.Known() {
		cost = record.Cost.String()
	}
	return json.Marshal(struct {
		RequestID        string           `json:"request_id"`
		KeyID            string           `json:"key_id,omitempty"`
		KeyName          string           `json:"key_name,omitempty"`
		Method           string           `json:"method"`
		Route            string           `json:"route"`
		Model            string           `json:"model,omitempty"`
		RequestedMode    any              `json:"requested_mode"`
		UpstreamMode     any              `json:"upstream_mode"`
		DeliveredMode    any              `json:"delivered_mode"`
		DownstreamStatus any              `json:"downstream_status"`
		UpstreamStatus   any              `json:"upstream_status"`
		Terminal         TerminalMetadata `json:"terminal"`
		ErrorCode        string           `json:"error_code"`
		ClientBytes      any              `json:"client_bytes"`
		UpstreamBytes    any              `json:"upstream_bytes"`
		DeliveredBytes   any              `json:"delivered_bytes"`
		Usage            any              `json:"usage"`
		Cost             any              `json:"cost"`
		Timing           CompletionTiming `json:"timing"`
	}{
		RequestID: safeRequestID(record.RequestID), KeyID: record.KeyID, KeyName: record.KeyName,
		Method: record.Method, Route: record.Route.String(), Model: record.Model,
		RequestedMode: func() any {
			if record.RequestedMode == RequestModeUnknown {
				return nil
			}
			return record.RequestedMode.String()
		}(),
		UpstreamMode: optionalMode(record.UpstreamMode), DeliveredMode: optionalMode(record.DeliveredMode),
		DownstreamStatus: optionalStatus(record.DownstreamStatus), UpstreamStatus: optionalStatus(record.UpstreamStatus),
		Terminal: record.Terminal, ErrorCode: record.ErrorCode.String(),
		ClientBytes: optionalInt(record.ClientBytes.value, record.ClientBytes.known), UpstreamBytes: optionalInt(record.UpstreamBytes.value, record.UpstreamBytes.known), DeliveredBytes: optionalInt(record.DeliveredBytes.value, record.DeliveredBytes.known),
		Usage: struct {
			Input           any `json:"input"`
			Output          any `json:"output"`
			Total           any `json:"total"`
			CachedInput     any `json:"cached_input"`
			ReasoningOutput any `json:"reasoning_output"`
		}{optionalInt(input, inputKnown), optionalInt(output, outputKnown), optionalInt(total, totalKnown), optionalInt(cached, cachedKnown), optionalInt(reasoning, reasoningKnown)},
		Cost: cost, Timing: record.Timing,
	})
}

// String implements the requested mode's safe spelling.
func (mode RequestMode) String() string {
	switch mode {
	case RequestModeJSON:
		return "json"
	case RequestModeSSE:
		return "sse"
	default:
		return "unknown"
	}
}

func (record CompletionRecord) input() CompletionRecordInput {
	return CompletionRecordInput{
		RequestID: record.RequestID, KeyID: record.KeyID, KeyName: record.KeyName,
		Method: record.Method, Route: record.Route, RouteClass: record.RouteClass, Model: record.Model,
		RequestedMode: record.RequestedMode, UpstreamMode: record.UpstreamMode,
		ActualUpstreamMode: record.ActualUpstreamMode,
		DeliveredMode:      record.DeliveredMode, DownstreamStatus: record.DownstreamStatus,
		UpstreamStatus: record.UpstreamStatus, Terminal: record.Terminal,
		ErrorCode: record.ErrorCode, SafeErrorCode: record.SafeErrorCode, ClientBytes: record.ClientBytes,
		UpstreamBytes: record.UpstreamBytes, DeliveredBytes: record.DeliveredBytes,
		Usage: record.Usage, Cost: record.Cost, Timing: record.Timing,
	}
}

func normalizeCompletionInput(input CompletionRecordInput) CompletionRecordInput {
	if input.Route != RouteClassUnknown && input.RouteClass != RouteClassUnknown && input.Route != input.RouteClass {
		return input
	}
	if input.Route == RouteClassUnknown {
		input.Route = input.RouteClass
	}
	if input.UpstreamMode != ResponseModeUnknown && input.ActualUpstreamMode != ResponseModeUnknown && input.UpstreamMode != input.ActualUpstreamMode {
		return input
	}
	if input.UpstreamMode == ResponseModeUnknown {
		input.UpstreamMode = input.ActualUpstreamMode
	}
	if input.ErrorCode != ErrorCodeUnknown && input.SafeErrorCode != ErrorCodeUnknown && input.ErrorCode != input.SafeErrorCode {
		return input
	}
	if input.ErrorCode == ErrorCodeUnknown {
		input.ErrorCode = input.SafeErrorCode
	}
	return input
}

func validateCompletionInput(input CompletionRecordInput) error {
	if !validRequestID(input.RequestID) {
		return errors.New("completion: malformed request ID")
	}
	if input.KeyID != "" && !validBoundedText(input.KeyID, 256) || input.KeyName != "" && !validBoundedText(input.KeyName, 256) {
		return errors.New("completion: invalid key identity")
	}
	if !validBoundedText(input.Method, 32) || !validBoundedText(input.Model, 512) {
		return errors.New("completion: invalid method or model")
	}
	if input.Route > RouteClassAdmin || input.RequestedMode > RequestModeSSE || input.UpstreamMode != ResponseModeUnknown && input.UpstreamMode != ResponseModeJSON && input.UpstreamMode != ResponseModeOpaque && input.UpstreamMode != ResponseModeSSE || input.DeliveredMode != ResponseModeUnknown && input.DeliveredMode != ResponseModeJSON && input.DeliveredMode != ResponseModeOpaque && input.DeliveredMode != ResponseModeSSE {
		return errors.New("completion: invalid enum")
	}
	if input.UpstreamMode == ResponseModeUnknown && input.DeliveredMode != ResponseModeUnknown {
		return errors.New("completion: delivered mode requires upstream mode")
	}
	if input.DeliveredMode == ResponseModeSSE && input.UpstreamMode != ResponseModeSSE {
		return errors.New("completion: SSE delivery requires upstream SSE")
	}
	if input.DeliveredMode == ResponseModeOpaque && input.UpstreamMode != ResponseModeOpaque {
		return errors.New("completion: opaque delivery requires opaque upstream")
	}
	if input.UpstreamMode == ResponseModeOpaque && input.DeliveredMode != ResponseModeOpaque && input.DeliveredMode != ResponseModeUnknown {
		return errors.New("completion: opaque upstream cannot be transformed")
	}
	if input.RequestedMode == RequestModeJSON && input.DeliveredMode == ResponseModeSSE || input.RequestedMode == RequestModeSSE && input.DeliveredMode == ResponseModeJSON {
		return errors.New("completion: requested and delivered modes conflict")
	}
	if !validErrorCode(input.ErrorCode) || !validTerminalOutcome(input.Terminal.Outcome) {
		return errors.New("completion: invalid terminal metadata or error code")
	}
	if input.Terminal.Outcome == TerminalOutcomePreUpstream && input.Terminal.UpstreamStarted {
		return errors.New("completion: pre-upstream outcome cannot start upstream")
	}
	if input.Terminal.Outcome != TerminalOutcomeUnknown && input.Terminal.Outcome != TerminalOutcomePreUpstream && !input.Terminal.UpstreamStarted {
		return errors.New("completion: post-upstream outcome requires upstream start")
	}
	if err := validateStatuses(input.DownstreamStatus, input.UpstreamStatus); err != nil {
		return err
	}
	for _, count := range []ByteCount{input.ClientBytes, input.UpstreamBytes, input.DeliveredBytes} {
		if count.known && count.value < 0 {
			return errors.New("completion: byte count is negative")
		}
	}
	if err := validateTiming(input.Timing); err != nil {
		return err
	}
	return nil
}

func validRequestID(id RequestID) bool {
	if len(id) != 32 {
		return false
	}
	var decoded [16]byte
	_, err := hex.Decode(decoded[:], []byte(id))
	return err == nil && strings.ToLower(string(id)) == string(id)
}

func safeRequestID(id RequestID) string {
	if validRequestID(id) {
		return string(id)
	}
	return "<invalid>"
}

func validBoundedText(value string, max int) bool {
	if len(value) > max {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func validErrorCode(code SafeErrorCode) bool { return code <= ErrorCodeConflict }
func validTerminalOutcome(outcome TerminalOutcome) bool {
	switch outcome {
	case TerminalOutcomeUnknown, TerminalOutcomePreUpstream, TerminalOutcomeUpstreamError, TerminalOutcomeResponseError, TerminalOutcomeComplete, TerminalOutcomeCustomDispatch, TerminalOutcomeCancelled:
		return true
	default:
		return false
	}
}

func validateStatuses(statuses ...OptionalStatus) error {
	for _, status := range statuses {
		if status.known && (status.value < 0 || status.value > 999) {
			return errors.New("completion: invalid status")
		}
	}
	return nil
}

func validateTiming(timing CompletionTiming) error {
	for _, duration := range []DurationMicros{timing.Total, timing.TimeToFirstByte} {
		if duration.known && duration.value < 0 {
			return errors.New("completion: duration is negative")
		}
	}
	if timing.StartedAt.known && timing.FinishedAt.known && timing.FinishedAt.value < timing.StartedAt.value {
		return errors.New("completion: finish precedes start")
	}
	return nil
}
