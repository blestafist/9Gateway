package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/pestit/9gateway/internal/streaming"
)

func TestAggregateSSEToJSONReconstructsChoicesUsageAndSplitToolCall(t *testing.T) {
	input := `comment: ignored
: keep-alive

data: {"id":"chat-1","created":7,"model":"model-1","choices":[{"index":0,"delta":{"role":"assistant"}},{"index":1,"delta":{"role":"assistant"}}],"unknown":true}

data: {"choices":[{"index":0,"delta":{"content":"hello"}},{"index":1,"delta":{"content":"second"}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":2,"id":"call-2","type":"function","function":{"name":"lookup","arguments":"{\"city\":\""}}]}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":2,"function":{"arguments":"Paris\"}"}}]}}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}

data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"},{"index":1,"delta":{},"finish_reason":"stop"}]}

data: [DONE]

data: {"choices":[{"index":0,"delta":{"content":"must-not-read"}}]}

`

	data, err := AggregateSSEToJSON(bytes.NewBufferString(input), 4096, 1024)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	if got["id"] != "chat-1" || got["model"] != "model-1" || got["created"] != float64(7) {
		t.Fatalf("metadata = %#v", got)
	}
	choices, ok := got["choices"].([]any)
	if !ok || len(choices) != 2 {
		t.Fatalf("choices = %#v, want two choices", got["choices"])
	}
	first := choices[0].(map[string]any)
	message := first["message"].(map[string]any)
	if message["content"] != "hello" || first["finish_reason"] != "tool_calls" {
		t.Fatalf("first choice = %#v", first)
	}
	toolCalls := message["tool_calls"].([]any)
	tool := toolCalls[0].(map[string]any)
	function := tool["function"].(map[string]any)
	if function["arguments"] != `{"city":"Paris"}` {
		t.Fatalf("tool function = %#v", function)
	}
	if choices[1].(map[string]any)["finish_reason"] != "stop" {
		t.Fatalf("second choice = %#v", choices[1])
	}
	if usage := got["usage"].(map[string]any); !reflect.DeepEqual(usage, map[string]any{
		"prompt_tokens": float64(3), "completion_tokens": float64(4), "total_tokens": float64(7),
	}) {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestAggregateSSEToJSONHandlesSplitReads(t *testing.T) {
	input := &oneByteReader{data: []byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")}
	data, err := AggregateSSEToJSON(input, 256, 16)
	if err != nil || !json.Valid(data) {
		t.Fatalf("one-byte input = (%s, %v), want valid JSON", data, err)
	}
}

func TestAggregateSSEToJSONReturnsControlledErrorsAndDoesNotReadAfterDONE(t *testing.T) {
	tests := []struct {
		name string
		data string
		want error
	}{
		{name: "empty", data: "", want: ErrEmptyStream},
		{name: "comments-only", data: ": ping\n\n", want: ErrEmptyStream},
		{name: "malformed", data: "data: {bad}\n\ndata: [DONE]\n\n", want: ErrMalformedStreamChunk},
		{name: "incomplete", data: "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"x\"}}]}\n\n", want: ErrStreamIncomplete},
		{name: "payload-overflow", data: "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"long\"}}]}\n\ndata: [DONE]\n\n", want: ErrAccumulatorOverflow},
		{name: "event-overflow", data: "data: {\"choices\":[]}\n\ndata: [DONE]\n\n", want: streaming.ErrEventTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			maxEvent := 4096
			maxPayload := int64(1024)
			if test.name == "event-overflow" {
				maxEvent = 5
			}
			if test.name == "payload-overflow" {
				maxPayload = 3
			}
			_, err := AggregateSSEToJSON(bytes.NewBufferString(test.data), maxEvent, maxPayload)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want errors.Is(..., %v)", err, test.want)
			}
		})
	}

	input := &chunkReader{chunks: [][]byte{
		[]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\n"),
		[]byte("data: [DONE]\n\n"),
		[]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"after\"}}]}\n\n"),
	}}
	if _, err := AggregateSSEToJSON(input, 4096, 32); err != nil {
		t.Fatal(err)
	}
	if input.reads != 2 {
		t.Fatalf("reads after DONE = %d, want exactly two event reads", input.reads)
	}
}

func TestAggregateSSEToJSONValidatesLimitsAndFinalState(t *testing.T) {
	for _, limits := range [][2]int64{{0, 1}, {1, 0}, {-1, 1}, {1, -1}} {
		if data, err := AggregateSSEToJSON(bytes.NewBuffer(nil), int(limits[0]), limits[1]); !errors.Is(err, ErrInvalidAggregationLimit) || data != nil {
			t.Fatalf("limits %v = (%s, %v), want invalid-limit error", limits, data, err)
		}
	}
	for _, data := range []string{
		"data: {\"choices\":[]}\n\ndata: [DONE]\n\n",
		"data: {\"usage\":{\"total_tokens\":1}}\n\ndata: [DONE]\n\n",
	} {
		if output, err := AggregateSSEToJSON(bytes.NewBufferString(data), 1024, 32); !errors.Is(err, ErrInvalidAccumulatorState) || output != nil {
			t.Fatalf("invalid final state = (%s, %v), want invalid-state error", output, err)
		}
	}
}

type oneByteReader struct{ data []byte }

func (reader *oneByteReader) Read(destination []byte) (int, error) {
	if len(reader.data) == 0 {
		return 0, io.EOF
	}
	destination[0] = reader.data[0]
	reader.data = reader.data[1:]
	return 1, nil
}

type chunkReader struct {
	chunks [][]byte
	reads  int
}

func (reader *chunkReader) Read(destination []byte) (int, error) {
	reader.reads++
	if len(reader.chunks) == 0 {
		return 0, io.EOF
	}
	chunk := reader.chunks[0]
	reader.chunks = reader.chunks[1:]
	copy(destination, chunk)
	return len(chunk), nil
}
