package httpserver

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/pestit/9gateway/internal/accounting"
)

// TraceClock is the small clock boundary used by request traces. Wall and
// monotonic readings are deliberately separate: wall time is persisted while
// monotonic time is used only for elapsed durations.
//
// A nil function uses time.Now. Tests can provide both functions without
// sleeping or relying on the machine clock.
type TraceClock struct {
	Wall         func() time.Time
	Monotonic    func() time.Time
	WallNow      func() time.Time
	MonotonicNow func() time.Time
}

// RequestTraceClock is a descriptive spelling retained for callers that use
// the request-oriented name.
type RequestTraceClock = TraceClock

// Clock is a compatibility spelling for the injectable trace clock.
type Clock = TraceClock

func NewTraceClock(wall, monotonic func() time.Time) TraceClock {
	return TraceClock{Wall: wall, Monotonic: monotonic}
}

func defaultTraceClock() TraceClock {
	return TraceClock{Wall: time.Now, Monotonic: time.Now}
}

func (clock TraceClock) readings() (time.Time, time.Time) {
	wall := clock.Wall
	if wall == nil {
		wall = clock.WallNow
	}
	if wall == nil {
		wall = time.Now
	}
	monotonic := clock.Monotonic
	if monotonic == nil {
		monotonic = clock.MonotonicNow
	}
	if monotonic == nil {
		monotonic = time.Now
	}
	return wall(), monotonic()
}

// TraceRequestMetadata contains only bounded request facts. It is a value
// type so a trace never retains an HTTP request, URL, or header map.
type TraceRequestMetadata struct {
	Method        string
	Route         RouteClass
	Model         string
	RequestedMode RequestMode
}

// RequestTraceEnrichment is the immutable, observation-side portion of a
// request trace. Observation may complete after the handler has returned;
// this value is constructed once and merged into a separate final record.
type RequestTraceEnrichment struct {
	Usage accounting.Usage
	Cost  accounting.Money
}

// TraceEnrichment is a descriptive alias.
type TraceEnrichment = RequestTraceEnrichment

type traceContextKey struct{}

// RequestTraceState is request-local mutable state. It is safe for transport,
// cancellation, and deferred observation paths to call setters concurrently.
// After FreezeBase, base fields are immutable; usage and cost may still be
// supplied through the separate enrichment path.
type RequestTraceState struct {
	mu sync.Mutex

	clock TraceClock
	input CompletionRecordInput

	startedMono time.Time
	lastMono    time.Time
	lastWall    time.Time
	started     bool
	upstream    bool
	headers     bool
	firstByte   bool
	finished    bool
	terminalSet bool
	authSet     bool
	metadataSet bool
	usageSet    bool
	costSet     bool
	errorSet    bool

	base        CompletionRecord
	baseInvalid bool
	baseFrozen  bool

	enrichmentMu     sync.Mutex
	enrichment       RequestTraceEnrichment
	enrichmentUsage  bool
	enrichmentCost   bool
	enrichmentSnap   RequestTraceEnrichment
	enrichmentFrozen bool
}

// NewRequestTraceState creates a request-local trace. The request ID is
// validated at construction, and the initial reading is the request start.
// A malformed ID produces a nil state; the middleware handles that as an
// internal gateway failure rather than retaining unsafe text.
func NewRequestTraceState(id RequestID, clocks ...TraceClock) *RequestTraceState {
	clock := defaultTraceClock()
	if len(clocks) != 0 {
		clock = clocks[0]
	}
	if _, err := NewRequestID(id); err != nil {
		return nil
	}
	wall, mono := clock.readings()
	state := &RequestTraceState{clock: clock, input: CompletionRecordInput{RequestID: id}, started: true, startedMono: mono, lastMono: mono, lastWall: wall}
	state.input.Timing.StartedAt = state.wallStamp(wall)
	return state
}

// NewRequestTrace is the error-returning constructor for embedders that want
// malformed request IDs reported directly.
func NewRequestTrace(id RequestID, clocks ...TraceClock) (*RequestTraceState, error) {
	state := NewRequestTraceState(id, clocks...)
	if state == nil {
		return nil, errMalformedTraceRequestID
	}
	return state, nil
}

var errMalformedTraceRequestID = traceError("trace: malformed request ID")

type traceError string

func (err traceError) Error() string { return string(err) }

func TraceFromContext(ctx context.Context) *RequestTraceState {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(traceContextKey{}).(*RequestTraceState)
	return state
}

// RequestTraceFromContext is the descriptive context accessor.
func RequestTraceFromContext(ctx context.Context) *RequestTraceState { return TraceFromContext(ctx) }

func withTrace(ctx context.Context, state *RequestTraceState) context.Context {
	return context.WithValue(ctx, traceContextKey{}, state)
}

func (state *RequestTraceState) wallStamp(at time.Time) UnixMicros {
	stamp, err := NewUnixMicros(at.UTC())
	if err != nil {
		return UnknownUnixMicros()
	}
	return stamp
}

// reading obtains a non-decreasing pair. Clamping here makes a badly behaved
// test clock harmless and keeps finish-before-start validation from becoming a
// source of telemetry failures.
func (state *RequestTraceState) reading() (time.Time, time.Time) {
	wall, mono := state.clock.readings()
	if !state.lastWall.IsZero() && wall.Before(state.lastWall) {
		wall = state.lastWall
	}
	if !state.lastMono.IsZero() && mono.Before(state.lastMono) {
		mono = state.lastMono
	}
	state.lastWall, state.lastMono = wall, mono
	return wall, mono
}

func durationMicros(from, to time.Time) DurationMicros {
	if from.IsZero() || to.Before(from) {
		return UnknownDurationMicros()
	}
	delta := to.Sub(from)
	if delta < 0 {
		return UnknownDurationMicros()
	}
	// time.Duration is nanoseconds and can be larger than the useful
	// persistence range only by conversion. Saturate rather than wrap.
	micros := delta / time.Microsecond
	if micros < 0 || int64(micros) < 0 {
		value, _ := NewDurationMicrosValue(math.MaxInt64)
		return value // unreachable on current Go, but safe
	}
	value, _ := NewDurationMicrosValue(int64(micros))
	return value
}

func validTraceText(value string, max int) bool { return validBoundedText(value, max) }

// SetAuthentication records the first safe key identity only.
func (state *RequestTraceState) SetAuthentication(keyID, keyName string) bool {
	if state == nil || !validTraceText(keyID, 256) || !validTraceText(keyName, 256) {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.baseFrozen || state.authSet {
		return false
	}
	state.input.KeyID, state.input.KeyName = keyID, keyName
	state.authSet = true
	return true
}

func (state *RequestTraceState) SetAuth(keyID, keyName string) bool {
	return state.SetAuthentication(keyID, keyName)
}

// SetRequestMetadata records bounded request metadata once.
func (state *RequestTraceState) SetRequestMetadata(method string, route RouteClass, model string, requestedMode RequestMode) bool {
	if state == nil || !validTraceText(method, 32) || !validTraceText(model, 512) || route > RouteClassAdmin || requestedMode > RequestModeSSE {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.baseFrozen || state.metadataSet {
		return false
	}
	state.input.Method, state.input.Route, state.input.Model, state.input.RequestedMode = method, route, model, requestedMode
	state.metadataSet = true
	return true
}

func (state *RequestTraceState) SetRequestMetadataValue(metadata TraceRequestMetadata) bool {
	return state.SetRequestMetadata(metadata.Method, metadata.Route, metadata.Model, metadata.RequestedMode)
}

// SetUpstreamStart records the first upstream-start milestone.
func (state *RequestTraceState) SetUpstreamStart(started ...bool) bool {
	if state == nil || len(started) != 0 && !started[0] {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.baseFrozen || state.upstream {
		return false
	}
	state.upstream, state.input.Terminal.UpstreamStarted = true, true
	return true
}

// SetUpstreamHeaders records the first upstream status and its timestamp.
func (state *RequestTraceState) SetUpstreamHeaders(status int) bool {
	value, err := NewStatus(status)
	if err != nil {
		return false
	}
	return state.SetUpstreamHeadersStatus(value)
}

func (state *RequestTraceState) SetUpstreamHeadersStatus(status OptionalStatus) bool {
	if state == nil || !status.Known() {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.baseFrozen || state.headers {
		return false
	}
	wall, _ := state.reading()
	state.headers = true
	state.upstream = true
	state.input.Terminal.UpstreamStarted = true
	state.input.UpstreamStatus = status
	state.input.Timing.UpstreamHeadersAt = state.wallStamp(wall)
	return true
}

func (state *RequestTraceState) SetUpstreamStatus(status int) bool {
	return state.SetUpstreamHeaders(status)
}

func (state *RequestTraceState) SetDownstreamStatus(status int) bool {
	value, err := NewStatus(status)
	if err != nil || state == nil {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.baseFrozen || state.input.DownstreamStatus.Known() {
		return false
	}
	state.input.DownstreamStatus = value
	return true
}

func (state *RequestTraceState) SetByteCounts(client, upstream, delivered ByteCount) bool {
	if state == nil || !client.Known() && !upstream.Known() && !delivered.Known() {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.baseFrozen || state.input.ClientBytes.Known() || state.input.UpstreamBytes.Known() || state.input.DeliveredBytes.Known() {
		return false
	}
	state.input.ClientBytes, state.input.UpstreamBytes, state.input.DeliveredBytes = client, upstream, delivered
	return true
}

func (state *RequestTraceState) SetClientBytes(count ByteCount) bool {
	if state == nil {
		return false
	}
	return state.setByteCount(&state.input.ClientBytes, count)
}

func (state *RequestTraceState) SetUpstreamBytes(count ByteCount) bool {
	if state == nil {
		return false
	}
	return state.setByteCount(&state.input.UpstreamBytes, count)
}

func (state *RequestTraceState) SetDeliveredBytes(count ByteCount) bool {
	if state == nil {
		return false
	}
	return state.setByteCount(&state.input.DeliveredBytes, count)
}

func (state *RequestTraceState) setByteCount(destination *ByteCount, count ByteCount) bool {
	if state == nil || !count.Known() {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.baseFrozen || destination.Known() {
		return false
	}
	*destination = count
	return true
}

// SetResponseMode records actual upstream and delivered representation modes.
// SetDeliveredMode can be used when the two become known at different points.
func (state *RequestTraceState) SetResponseMode(upstream, delivered ResponseMode) bool {
	if state == nil || upstream == ResponseModeUnknown || delivered == ResponseModeUnknown || !validResponseMode(upstream) || !validResponseMode(delivered) {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.baseFrozen || state.input.UpstreamMode != ResponseModeUnknown || state.input.DeliveredMode != ResponseModeUnknown {
		return false
	}
	state.input.UpstreamMode, state.input.ActualUpstreamMode, state.input.DeliveredMode = upstream, upstream, delivered
	state.upstream = true
	state.input.Terminal.UpstreamStarted = true
	return true
}

func (state *RequestTraceState) SetUpstreamResponseMode(mode ResponseMode) bool {
	if state == nil || mode == ResponseModeUnknown || !validResponseMode(mode) {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.baseFrozen || state.input.UpstreamMode != ResponseModeUnknown {
		return false
	}
	state.input.UpstreamMode, state.input.ActualUpstreamMode = mode, mode
	state.upstream = true
	state.input.Terminal.UpstreamStarted = true
	return true
}

func (state *RequestTraceState) SetDeliveredMode(mode ResponseMode) bool {
	if state == nil || mode == ResponseModeUnknown || !validResponseMode(mode) {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.baseFrozen || state.input.DeliveredMode != ResponseModeUnknown {
		return false
	}
	state.input.DeliveredMode = mode
	return true
}

// SetFirstDownstreamByte records only the first successful downstream-byte
// milestone. It intentionally does not inspect or retain those bytes.
func (state *RequestTraceState) SetFirstDownstreamByte(written ...bool) bool {
	if state == nil || len(written) != 0 && !written[0] {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.baseFrozen || state.firstByte {
		return false
	}
	wall, mono := state.reading()
	state.firstByte = true
	state.input.Timing.FirstByteAt = state.wallStamp(wall)
	state.input.Timing.TimeToFirstByte = durationMicros(state.startedMono, mono)
	return true
}

func (state *RequestTraceState) setUsageLocked(usage accounting.Usage) bool {
	if state.usageSet {
		return false
	}
	state.input.Usage, state.usageSet = usage, true
	return true
}

func (state *RequestTraceState) SetUsage(usage accounting.Usage) bool {
	if state == nil {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.baseFrozen {
		return state.setUsageLocked(usage)
	}
	state.enrichmentMu.Lock()
	defer state.enrichmentMu.Unlock()
	if state.enrichmentFrozen || state.enrichmentUsage {
		return false
	}
	state.enrichment.Usage, state.enrichmentUsage = usage, true
	return true
}

func (state *RequestTraceState) SetCost(cost accounting.Money) bool {
	if state == nil {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.baseFrozen {
		if state.costSet {
			return false
		}
		state.input.Cost, state.costSet = cost, true
		return true
	}
	state.enrichmentMu.Lock()
	defer state.enrichmentMu.Unlock()
	if state.enrichmentFrozen || state.enrichmentCost {
		return false
	}
	state.enrichment.Cost, state.enrichmentCost = cost, true
	return true
}

func (state *RequestTraceState) SetUsageCost(usage accounting.Usage, cost accounting.Money) bool {
	usageSet := state.SetUsage(usage)
	costSet := state.SetCost(cost)
	return usageSet || costSet
}

func (state *RequestTraceState) SetErrorCode(code SafeErrorCode) bool {
	if state == nil || code == ErrorCodeUnknown || !validErrorCode(code) {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.baseFrozen || state.errorSet {
		return false
	}
	state.input.ErrorCode, state.input.SafeErrorCode = code, code
	state.errorSet = true
	return true
}

func (state *RequestTraceState) SetError(code SafeErrorCode) bool { return state.SetErrorCode(code) }

func (state *RequestTraceState) SetTerminalMetadata(metadata TerminalMetadata) bool {
	if state == nil || !validTerminalOutcome(metadata.Outcome) {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.baseFrozen || state.terminalSet || metadata.UpstreamStarted != state.upstream || metadata.Outcome == TerminalOutcomePreUpstream && state.upstream || metadata.Outcome != TerminalOutcomeUnknown && metadata.Outcome != TerminalOutcomePreUpstream && !state.upstream {
		return false
	}
	state.input.Terminal, state.terminalSet = metadata, true
	return true
}

func (state *RequestTraceState) SetTerminalOutcome(outcome TerminalOutcome) bool {
	if state == nil {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if !validTerminalOutcome(outcome) || state.baseFrozen || state.terminalSet {
		return false
	}
	started := state.upstream
	state.input.Terminal, state.terminalSet = TerminalMetadata{Outcome: outcome, UpstreamStarted: started}, true
	return true
}

func (state *RequestTraceState) completeLocked() {
	if state.finished {
		return
	}
	wall, mono := state.reading()
	state.finished = true
	state.input.Timing.FinishedAt = state.wallStamp(wall)
	state.input.Timing.Total = durationMicros(state.startedMono, mono)
	if !state.terminalSet {
		outcome := TerminalOutcomePreUpstream
		if state.upstream {
			outcome = TerminalOutcomeComplete
		}
		state.input.Terminal = TerminalMetadata{Outcome: outcome, UpstreamStarted: state.upstream}
		state.terminalSet = true
	}
}

// Complete records the handler-end completion milestone and freezes the base
// snapshot. It is idempotent; callers may safely invoke it from competing
// cleanup paths.
func (state *RequestTraceState) Complete() (CompletionRecord, error) { return state.FreezeBase() }

func (state *RequestTraceState) Freeze() (CompletionRecord, error) { return state.FreezeBase() }

func (state *RequestTraceState) FreezeBase() (CompletionRecord, error) {
	if state == nil {
		return CompletionRecord{}, traceError("trace: nil state")
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.baseFrozen {
		if state.baseInvalid {
			return state.base, errInvalidTraceRecord
		}
		return state.base, nil
	}
	state.completeLocked()
	record, err := NewCompletionRecord(state.input)
	state.base, state.baseInvalid, state.baseFrozen = record, err != nil, true
	if err != nil {
		return record, errInvalidTraceRecord
	}
	return record, err
}

func (state *RequestTraceState) BaseSnapshot() (CompletionRecord, error) {
	return state.FreezeBase()
}

// FreezeEnrichment snapshots observation-only usage and cost exactly once.
func (state *RequestTraceState) FreezeEnrichment() RequestTraceEnrichment {
	if state == nil {
		return RequestTraceEnrichment{}
	}
	state.enrichmentMu.Lock()
	defer state.enrichmentMu.Unlock()
	if !state.enrichmentFrozen {
		state.enrichmentSnap, state.enrichmentFrozen = state.enrichment, true
	}
	return state.enrichmentSnap
}

func MergeRequestTraceEnrichment(base CompletionRecord, enrichment RequestTraceEnrichment) (CompletionRecord, error) {
	input := base.input()
	if enrichment.Usage.Input().Known() || enrichment.Usage.Output().Known() || enrichment.Usage.Total().Known() || enrichment.Usage.CachedInput().Known() || enrichment.Usage.ReasoningOutput().Known() {
		input.Usage = enrichment.Usage
	}
	if enrichment.Cost.Known() {
		input.Cost = enrichment.Cost
	}
	return NewCompletionRecord(input)
}

// Final returns a newly constructed record. Neither the base record nor the
// enrichment snapshot is modified by the merge.
func (state *RequestTraceState) Final() (CompletionRecord, error) {
	base, err := state.FreezeBase()
	if err != nil {
		return CompletionRecord{}, err
	}
	return MergeRequestTraceEnrichment(base, state.FreezeEnrichment())
}

func validResponseMode(mode ResponseMode) bool {
	return mode == ResponseModeUnknown || mode == ResponseModeJSON || mode == ResponseModeOpaque || mode == ResponseModeSSE
}

var errInvalidTraceRecord = traceError("trace: invalid completion record")
