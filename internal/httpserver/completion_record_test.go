package httpserver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/accounting"
)

func validCompletionInput(t *testing.T) CompletionRecordInput {
	t.Helper()
	usage, err := accounting.NewUsage(accounting.UsageInput{Input: ptrInt64(0), Output: ptrInt64(4), Total: ptrInt64(4)})
	if err != nil {
		t.Fatal(err)
	}
	cost, err := accounting.NewMoneyMicros(0)
	if err != nil {
		t.Fatal(err)
	}
	status, err := NewStatus(200)
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := NewByteCount(0)
	if err != nil {
		t.Fatal(err)
	}
	started := NewUnixMicrosValue(1_000_000)
	finished := NewUnixMicrosValue(2_000_000)
	duration, err := NewDurationMicros(time.Microsecond)
	if err != nil {
		t.Fatal(err)
	}
	return CompletionRecordInput{
		RequestID: "0123456789abcdef0123456789abcdef", KeyID: "key-id", KeyName: "friendly",
		Method: "POST", Route: RouteClassChatCompletions, Model: "model",
		RequestedMode: RequestModeJSON, UpstreamMode: ResponseModeJSON, DeliveredMode: ResponseModeJSON,
		DownstreamStatus: status, UpstreamStatus: status,
		Terminal:    TerminalMetadata{Outcome: TerminalOutcomeComplete, UpstreamStarted: true},
		ClientBytes: bytes, UpstreamBytes: bytes, DeliveredBytes: bytes, Usage: usage, Cost: cost,
		Timing: CompletionTiming{StartedAt: started, FinishedAt: finished, Total: duration, TimeToFirstByte: duration},
	}
}

func ptrInt64(value int64) *int64 { return &value }

func TestCompletionRecordScenariosAndKnownZero(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CompletionRecordInput)
	}{
		{name: "complete JSON", mutate: func(input *CompletionRecordInput) {}},
		{name: "rejected", mutate: func(input *CompletionRecordInput) {
			input.Terminal = TerminalMetadata{Outcome: TerminalOutcomePreUpstream}
		}},
		{name: "pre-upstream", mutate: func(input *CompletionRecordInput) {
			input.Terminal = TerminalMetadata{Outcome: TerminalOutcomePreUpstream}
			input.UpstreamMode = ResponseModeUnknown
			input.DeliveredMode = ResponseModeUnknown
		}},
		{name: "cancelled", mutate: func(input *CompletionRecordInput) {
			input.Terminal = TerminalMetadata{Outcome: TerminalOutcomeCancelled, UpstreamStarted: true}
			input.ErrorCode = ErrorCodeCancellation
		}},
		{name: "opaque", mutate: func(input *CompletionRecordInput) {
			input.RequestedMode = RequestModeUnknown
			input.UpstreamMode = ResponseModeOpaque
			input.DeliveredMode = ResponseModeOpaque
		}},
		{name: "transparent SSE", mutate: func(input *CompletionRecordInput) {
			input.RequestedMode = RequestModeSSE
			input.UpstreamMode = ResponseModeSSE
			input.DeliveredMode = ResponseModeSSE
		}},
		{name: "converted SSE", mutate: func(input *CompletionRecordInput) {
			input.RequestedMode = RequestModeJSON
			input.UpstreamMode = ResponseModeSSE
			input.DeliveredMode = ResponseModeJSON
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validCompletionInput(t)
			test.mutate(&input)
			record, err := NewCompletionRecord(input)
			if err != nil {
				t.Fatal(err)
			}
			if err := record.Validate(); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(record)
			if err != nil || !strings.Contains(string(encoded), `"cost":"0"`) {
				t.Fatalf("JSON = %s, err = %v", encoded, err)
			}
		})
	}
}

func TestCompletionRecordUnknownDiffersFromKnownZero(t *testing.T) {
	input := validCompletionInput(t)
	unknown, err := NewCompletionRecord(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Cost = accounting.UnknownMoney()
	input.ClientBytes = UnknownByteCount()
	input.DownstreamStatus = UnknownStatus()
	input.Timing.Total = UnknownDurationMicros()
	input.Timing.StartedAt = UnknownUnixMicros()
	input.UpstreamMode = ResponseModeUnknown
	input.DeliveredMode = ResponseModeUnknown
	input.Terminal = TerminalMetadata{Outcome: TerminalOutcomePreUpstream}
	input.Usage, err = accounting.NewUsage(accounting.UsageInput{})
	if err != nil {
		t.Fatal(err)
	}
	unknownRecord, err := NewCompletionRecord(input)
	if err != nil {
		t.Fatal(err)
	}
	knownJSON, _ := json.Marshal(unknown)
	unknownJSON, _ := json.Marshal(unknownRecord)
	if strings.Contains(string(knownJSON), `"cost":null`) || !strings.Contains(string(unknownJSON), `"cost":null`) {
		t.Fatalf("known/unknown cost JSON = %s / %s", knownJSON, unknownJSON)
	}
}

func TestCompletionRecordRejectsInvalidAndImpossibleValues(t *testing.T) {
	tests := []struct {
		name   string
		change func(*CompletionRecordInput)
	}{
		{name: "malformed request id", change: func(input *CompletionRecordInput) { input.RequestID = "credential-body" }},
		{name: "invalid route", change: func(input *CompletionRecordInput) { input.Route = RouteClass(99) }},
		{name: "invalid response mode", change: func(input *CompletionRecordInput) { input.UpstreamMode = ResponseMode("secret-body") }},
		{name: "finish before start", change: func(input *CompletionRecordInput) { input.Timing.FinishedAt = NewUnixMicrosValue(0) }},
		{name: "SSE delivery without SSE upstream", change: func(input *CompletionRecordInput) { input.DeliveredMode = ResponseModeSSE }},
		{name: "opaque transformation", change: func(input *CompletionRecordInput) {
			input.UpstreamMode = ResponseModeOpaque
			input.DeliveredMode = ResponseModeJSON
		}},
		{name: "negative byte count", change: func(input *CompletionRecordInput) { input.ClientBytes = ByteCount{value: -1, known: true} }},
		{name: "invalid terminal outcome", change: func(input *CompletionRecordInput) { input.Terminal.Outcome = TerminalOutcome("raw error body") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validCompletionInput(t)
			test.change(&input)
			if _, err := NewCompletionRecord(input); err == nil {
				t.Fatal("invalid input was accepted")
			}
		})
	}
}

func TestCompletionRecordStringAndJSONDoNotExposeSecrets(t *testing.T) {
	input := validCompletionInput(t)
	input.KeyID = "stable-id"
	input.KeyName = "safe-name"
	input.Model = "model"
	record, err := NewCompletionRecord(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{record.String(), string(mustJSON(t, record))} {
		for _, forbidden := range []string{"sk-gw-", "Authorization", "prompt body", "pricing rule", "SELECT", "rate"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%q exposed in %s", forbidden, text)
			}
		}
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
