package httpserver

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/accounting"
)

type traceTestClock struct {
	mu   sync.Mutex
	wall time.Time
	mono time.Time
}

func (clock *traceTestClock) now() (time.Time, time.Time) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.wall, clock.mono
}

func (clock *traceTestClock) advance(wall, mono time.Duration) {
	clock.mu.Lock()
	clock.wall = clock.wall.Add(wall)
	clock.mono = clock.mono.Add(mono)
	clock.mu.Unlock()
}

func traceTestState(t *testing.T) (*RequestTraceState, *traceTestClock) {
	t.Helper()
	clock := &traceTestClock{wall: time.Unix(100, 0).UTC(), mono: time.Unix(200, 0)}
	state, err := NewRequestTrace("0123456789abcdef0123456789abcdef", TraceClock{
		Wall:      func() time.Time { wall, _ := clock.now(); return wall },
		Monotonic: func() time.Time { _, mono := clock.now(); return mono },
	})
	if err != nil {
		t.Fatal(err)
	}
	return state, clock
}

func knownTraceUsage(t *testing.T, input, output int64) accounting.Usage {
	t.Helper()
	usage, err := accounting.NewUsage(accounting.UsageInput{Input: &input, Output: &output})
	if err != nil {
		t.Fatal(err)
	}
	return usage
}

func TestRequestTraceSettersAreFirstWriteWinsAndFreezeIsIdempotent(t *testing.T) {
	state, clock := traceTestState(t)
	if !state.SetAuthentication("key-1", "friendly") || state.SetAuthentication("key-2", "other") {
		t.Fatal("authentication setter did not preserve first write")
	}
	if !state.SetRequestMetadata("POST", RouteChatCompletions, "model-a", RequestModeJSON) || state.SetRequestMetadata("GET", RouteModels, "model-b", RequestModeSSE) {
		t.Fatal("metadata setter did not preserve first write")
	}
	if !state.SetUpstreamStart() || state.SetUpstreamStart() {
		t.Fatal("upstream start setter did not preserve first write")
	}
	clock.advance(3*time.Millisecond, 7*time.Millisecond)
	if !state.SetUpstreamHeaders(httpStatusOK) || state.SetUpstreamHeaders(299) {
		t.Fatal("upstream headers setter did not preserve first write")
	}
	if !state.SetUpstreamResponseMode(ResponseModeJSON) || state.SetUpstreamResponseMode(ResponseModeSSE) {
		t.Fatal("upstream mode setter did not preserve first write")
	}
	if !state.SetDeliveredMode(ResponseModeJSON) || state.SetDeliveredMode(ResponseModeSSE) {
		t.Fatal("delivered mode setter did not preserve first write")
	}
	clock.advance(2*time.Millisecond, 11*time.Millisecond)
	if !state.SetFirstDownstreamByte() || state.SetFirstDownstreamByte() {
		t.Fatal("first-byte setter did not preserve first write")
	}
	if !state.SetDownstreamStatus(httpStatusCreated) || state.SetDownstreamStatus(httpStatusNoContent) {
		t.Fatal("downstream status setter did not preserve first write")
	}
	if !state.SetUsage(knownTraceUsage(t, 3, 4)) || state.SetUsage(knownTraceUsage(t, 5, 6)) {
		t.Fatal("usage setter did not preserve first write")
	}
	if !state.SetErrorCode(ErrorCodeInternal) || state.SetErrorCode(ErrorCodeCancellation) {
		t.Fatal("error setter did not preserve first write")
	}
	if !state.SetTerminalOutcome(TerminalOutcomeResponseError) || state.SetTerminalOutcome(TerminalOutcomeComplete) {
		t.Fatal("terminal setter did not preserve first write")
	}
	clock.advance(4*time.Millisecond, 13*time.Millisecond)
	first, err := state.FreezeBase()
	if err != nil {
		t.Fatal(err)
	}
	second, err := state.FreezeBase()
	if err != nil {
		t.Fatal(err)
	}
	status, _ := first.UpstreamStatus.Value()
	if first != second || first.KeyID != "key-1" || status != httpStatusOK || first.Terminal.Outcome != TerminalOutcomeResponseError {
		t.Fatalf("freeze changed snapshot: first=%#v second=%#v", first, second)
	}
	if state.SetErrorCode(ErrorCodeCancellation) || state.SetDownstreamStatus(httpStatusOK) || state.SetFirstDownstreamByte() {
		t.Fatal("setter mutated frozen base")
	}
}

func TestRequestTraceFakeClockMissingAndBackwardMilestones(t *testing.T) {
	state, clock := traceTestState(t)
	if state.SetFirstDownstreamByte(false) || state.SetUpstreamStart(false) {
		t.Fatal("false milestone was recorded")
	}
	clock.advance(10*time.Millisecond, 10*time.Millisecond)
	clock.mu.Lock()
	clock.wall = clock.wall.Add(-time.Second)
	clock.mono = clock.mono.Add(-time.Second)
	clock.mu.Unlock()
	if !state.SetUpstreamStart() {
		t.Fatal("upstream start was not recorded")
	}
	if !state.SetUpstreamHeaders(httpStatusOK) {
		t.Fatal("upstream headers were not recorded")
	}
	record, err := state.Complete()
	if err != nil {
		t.Fatal(err)
	}
	if record.Timing.Total.Known() {
		value, _ := record.Timing.Total.Value()
		if value != 0 {
			t.Fatalf("total micros = %d, want 0 after backward clamp", value)
		}
	}
	if record.Timing.TimeToFirstByte.Known() {
		t.Fatal("missing first byte unexpectedly became known")
	}
	if record.Terminal.Outcome != TerminalOutcomeComplete || !record.Terminal.UpstreamStarted {
		t.Fatalf("terminal = %#v", record.Terminal)
	}
}

func TestRequestTraceEnrichmentIsIndependentAndOneShot(t *testing.T) {
	state, _ := traceTestState(t)
	state.SetRequestMetadata("POST", RouteResponses, "model", RequestModeJSON)
	state.SetUpstreamStart()
	state.SetUpstreamHeaders(httpStatusOK)
	state.SetUpstreamResponseMode(ResponseModeJSON)
	state.SetDeliveredMode(ResponseModeJSON)
	state.SetTerminalOutcome(TerminalOutcomeComplete)
	base, err := state.FreezeBase()
	if err != nil {
		t.Fatal(err)
	}
	usage := knownTraceUsage(t, 9, 2)
	cost, err := accounting.NewMoneyMicros(17)
	if err != nil {
		t.Fatal(err)
	}
	if !state.SetUsage(usage) || !state.SetCost(cost) || state.SetUsage(knownTraceUsage(t, 1, 1)) || state.SetCost(cost) {
		t.Fatal("enrichment setters are not one-shot")
	}
	enrichment := state.FreezeEnrichment()
	if state.SetUsage(usage) || state.SetCost(cost) {
		t.Fatal("post-freeze enrichment mutation succeeded")
	}
	final, err := MergeRequestTraceEnrichment(base, enrichment)
	if err != nil {
		t.Fatal(err)
	}
	if final == base || !final.Usage.Total().Known() || !final.Cost.Known() {
		t.Fatalf("final record was not an independent enriched record: %#v", final)
	}
	baseTotal, _ := base.Usage.Total().Value()
	finalTotal, _ := final.Usage.Total().Value()
	if baseTotal == finalTotal {
		t.Fatal("base snapshot was mutated by enrichment")
	}
}

func TestRequestTraceConcurrentSettersAndFreeze(t *testing.T) {
	state, _ := traceTestState(t)
	state.SetRequestMetadata("POST", RouteChatCompletions, "model", RequestModeJSON)
	state.SetUpstreamStart()
	state.SetUpstreamResponseMode(ResponseModeJSON)
	state.SetDeliveredMode(ResponseModeJSON)
	usage := knownTraceUsage(t, 1, 2)
	start := make(chan struct{})
	var group sync.WaitGroup
	for i := 0; i < 32; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			<-start
			state.SetUsage(usage)
			state.SetCost(accounting.Money{})
			state.SetFirstDownstreamByte()
			state.SetTerminalOutcome(TerminalOutcomeComplete)
			_, _ = state.FreezeBase()
		}(i)
	}
	close(start)
	group.Wait()
	record, err := state.FreezeBase()
	if err != nil {
		t.Fatal(err)
	}
	if record.RequestID != "0123456789abcdef0123456789abcdef" || record.Terminal.Outcome != TerminalOutcomeComplete {
		t.Fatalf("concurrent record incomplete: %#v", record)
	}
}

func TestRequestTraceContextIsolation(t *testing.T) {
	one, _ := traceTestState(t)
	two, _ := traceTestState(t)
	one.SetRequestMetadata("GET", RouteModels, "", RequestModeUnknown)
	two.SetRequestMetadata("POST", RouteResponses, "other", RequestModeJSON)
	ctx := withTrace(context.Background(), one)
	if TraceFromContext(ctx) != one || TraceFromContext(context.Background()) != nil {
		t.Fatal("trace context lookup is not isolated")
	}
	first, err := one.FreezeBase()
	if err != nil {
		t.Fatal(err)
	}
	second, err := two.FreezeBase()
	if err != nil {
		t.Fatal(err)
	}
	if first.Method == second.Method || first.Route == second.Route {
		t.Fatal("request traces share mutable metadata")
	}
}

func TestRequestIDBoundaryInstallsAndFreezesTrace(t *testing.T) {
	var state *RequestTraceState
	var gotID string
	recorder := httptest.NewRecorder()
	handler := withRequestID(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		state = TraceFromContext(request.Context())
		gotID = requestIDFromContext(request.Context())
		if state == nil {
			t.Fatal("request trace missing from context")
		}
		state.SetRequestMetadata("GET", RouteHealth, "", RequestModeUnknown)
		_, _ = response.Write([]byte("ok"))
	}))
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://gateway.test/health", nil))
	if state == nil || gotID == "" || recorder.Header().Get(requestIDHeader) != gotID {
		t.Fatalf("request ID/trace boundary mismatch: id=%q header=%q state=%p", gotID, recorder.Header().Get(requestIDHeader), state)
	}
	if state.SetRequestMetadata("POST", RouteResponses, "late", RequestModeJSON) {
		t.Fatal("trace was not frozen at handler end")
	}
	record, err := state.FreezeBase()
	if err != nil {
		t.Fatal(err)
	}
	if status, ok := record.DownstreamStatus.Value(); !ok || status != http.StatusOK {
		t.Fatalf("downstream status = %d/%v, want 200/true", status, ok)
	}
	if delivered, ok := record.DeliveredBytes.Value(); !ok || delivered != 2 {
		t.Fatalf("delivered bytes = %d/%v, want 2/true", delivered, ok)
	}
	if !record.Timing.FirstByteAt.Known() || !record.Timing.FinishedAt.Known() {
		t.Fatal("trace did not record body-byte and completion milestones")
	}
}

func TestRequestTraceRejectsUnsafeAndOverflowingInputs(t *testing.T) {
	if state := NewRequestTraceState("not-an-id"); state != nil {
		t.Fatal("malformed request ID was accepted")
	}
	state, _ := traceTestState(t)
	if state.SetAuthentication(string(make([]byte, 257)), "name") {
		t.Fatal("oversized key identity was accepted")
	}
	for name, set := range map[string]func(*RequestTraceState) bool{
		"key ID":   func(state *RequestTraceState) bool { return state.SetAuthentication("key\u0081", "name") },
		"key name": func(state *RequestTraceState) bool { return state.SetAuthentication("key", "name\u0085") },
		"model": func(state *RequestTraceState) bool {
			return state.SetRequestMetadata("POST", RouteChatCompletions, "model\u0081", RequestModeJSON)
		},
		"path": func(state *RequestTraceState) bool { return state.SetRouteMetadata("GET", "/v1/\u0085", RouteGeneric) },
	} {
		t.Run(name, func(t *testing.T) {
			state, _ := traceTestState(t)
			if set(state) {
				t.Fatalf("C1 control in %s was accepted", name)
			}
		})
	}
	if state.SetRequestMetadata("POST", RouteClass(99), "model", RequestModeJSON) {
		t.Fatal("invalid route was accepted")
	}
	if state.SetRequestMetadata("POST", RouteChatCompletions, string(make([]byte, 513)), RequestModeJSON) {
		t.Fatal("oversized model was accepted")
	}
	if state.SetUpstreamHeaders(-1) || state.SetDownstreamStatus(1000) {
		t.Fatal("invalid status was accepted")
	}
	if state.SetResponseMode(ResponseMode("secret"), ResponseModeJSON) || state.SetDeliveredMode(ResponseModeUnknown) {
		t.Fatal("invalid response mode was accepted")
	}
	if state.SetErrorCode(SafeErrorCode(255)) || state.SetTerminalOutcome(TerminalOutcome("raw error")) {
		t.Fatal("invalid bounded enum was accepted")
	}
	if state.SetByteCounts(ByteCount{}, ByteCount{}, ByteCount{}) {
		t.Fatal("all-unknown byte update was accepted")
	}
	if _, err := NewUnixMicros(time.Unix(math.MaxInt64, 0)); err == nil {
		t.Fatal("timestamp overflow was accepted")
	}
	if _, err := NewDurationMicros(-time.Microsecond); err == nil {
		t.Fatal("negative duration was accepted")
	}
}

func TestRequestTraceTextBoundsCountUnicodeCodePoints(t *testing.T) {
	state, _ := traceTestState(t)
	if !state.SetAuthentication(strings.Repeat("界", 256), "名") {
		t.Fatal("Unicode key identity at the boundary was rejected")
	}
	if state.SetRequestMetadata("POST", RouteChatCompletions, strings.Repeat("界", 513), RequestModeJSON) {
		t.Fatal("Unicode model beyond the code-point boundary was accepted")
	}
	path := "/" + strings.Repeat("界", 2047)
	if !state.SetRouteMetadata("GET", path, RouteGeneric) {
		t.Fatal("Unicode path at the code-point boundary was rejected")
	}
}

func TestRequestTraceRecordsOnlyAcceptedDownstreamBytesAndTTFT(t *testing.T) {
	state, clock := traceTestState(t)
	clock.advance(4*time.Millisecond, 9*time.Millisecond)
	if state.recordDownstreamWrite(0, false, false) {
		t.Fatal("empty write recorded")
	}
	if !state.recordDownstreamWrite(0, false, true) {
		t.Fatal("successful empty write was not recorded")
	}
	if !state.recordDownstreamWrite(2, false, false) || state.recordDownstreamWrite(-1, false, false) {
		t.Fatal("accepted byte accounting rejected or invalid write recorded")
	}
	clock.advance(3*time.Millisecond, 6*time.Millisecond)
	if !state.recordDownstreamWrite(1, true, false) {
		t.Fatal("second accepted write was not recorded")
	}
	record, err := state.Complete()
	if err != nil {
		t.Fatal(err)
	}
	delivered, known := record.DeliveredBytes.Value()
	if !known || delivered != 3 {
		t.Fatalf("delivered bytes = %d/%v, want 3/true", delivered, known)
	}
	ttft, known := record.Timing.TimeToFirstByte.Value()
	if !known || ttft != 15_000 {
		t.Fatalf("TTFT = %d/%v, want 15000/true", ttft, known)
	}
}

func TestRequestTraceHeaderOnlyRetainsUnknownTTFT(t *testing.T) {
	state, _ := traceTestState(t)
	if !state.SetDownstreamStatus(httpStatusNoContent) {
		t.Fatal("status was not recorded")
	}
	record, err := state.Complete()
	if err != nil {
		t.Fatal(err)
	}
	if record.Timing.TimeToFirstByte.Known() || record.Timing.FirstByteAt.Known() {
		t.Fatal("header-only response unexpectedly has TTFT")
	}
}

const (
	httpStatusOK        = 200
	httpStatusCreated   = 201
	httpStatusNoContent = 204
)
