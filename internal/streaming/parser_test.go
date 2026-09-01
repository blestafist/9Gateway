package streaming

import (
	"io"
	"strings"
	"testing"
)

func TestNewReaderRejectsInvalidInput(t *testing.T) {
	for _, test := range []struct {
		name          string
		input         io.Reader
		maxEventSize  int
		wantErrorText string
	}{
		{name: "nil input", maxEventSize: 1024, wantErrorText: "nil input"},
		{name: "zero limit", input: strings.NewReader(""), wantErrorText: "positive"},
		{name: "negative limit", input: strings.NewReader(""), maxEventSize: -1, wantErrorText: "positive"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewReader(test.input, test.maxEventSize)
			if err == nil || !strings.Contains(err.Error(), test.wantErrorText) {
				t.Fatalf("NewReader error = %v, want text %q", err, test.wantErrorText)
			}
		})
	}
}

func TestReaderEmptyInputReturnsEOFWithoutFabricatingEvent(t *testing.T) {
	reader, err := NewReader(strings.NewReader(""), 1024)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	event, err := reader.Next()
	if event != (SSEEvent{}) {
		t.Fatalf("event = %#v, want empty event", event)
	}
	if err != io.EOF {
		t.Fatalf("Next error = %v, want io.EOF", err)
	}
}

func TestReaderReturnsUnnamedAndNamedEventsAsIndependentValues(t *testing.T) {
	unnamed := SSEEvent{Data: "unnamed"}
	named := SSEEvent{Event: "message", Data: "named"}

	if unnamed.Event != "" || named.Event != "message" {
		t.Fatalf("event shapes = %#v and %#v", unnamed, named)
	}
	if unnamed.Data == named.Data {
		t.Fatal("event values unexpectedly share data")
	}
}

func TestReaderReturnsEOFAfterItsInputIsConsumed(t *testing.T) {
	reader, err := NewReader(strings.NewReader("data: input\n\n"), 1024)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	if _, err := reader.Next(); err != nil {
		t.Fatalf("first Next error = %v, want no error", err)
	}
	if _, err := reader.Next(); err != io.EOF {
		t.Fatalf("second Next error = %v, want io.EOF", err)
	}
}

func TestReaderEmitsCompleteFramesSequentially(t *testing.T) {
	reader, err := NewReader(strings.NewReader("data: first\n\ndata: second\n\n"), 1024)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	if _, err := reader.Next(); err != nil {
		t.Fatalf("first Next error = %v, want no error", err)
	}
	if _, err := reader.Next(); err != nil {
		t.Fatalf("second Next error = %v, want no error", err)
	}
	if _, err := reader.Next(); err != io.EOF {
		t.Fatalf("third Next error = %v, want io.EOF", err)
	}
}

func TestReaderParsesDataAndEventFields(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
		want  SSEEvent
	}{
		{name: "one data line", input: "data: hello\n\n", want: SSEEvent{Data: "hello"}},
		{name: "named event", input: "event: update\ndata: hello\n\n", want: SSEEvent{Event: "update", Data: "hello"}},
		{name: "multiple data lines", input: "data: first\ndata: second\n\n", want: SSEEvent{Data: "first\nsecond"}},
		{name: "empty data", input: "data:\n\n", want: SSEEvent{Data: ""}},
		{name: "unknown field", input: "id: ignored\ndata: kept\nretry: 10\n\n", want: SSEEvent{Data: "kept"}},
		{name: "CRLF", input: "event: update\r\ndata: hello\r\n\r\n", want: SSEEvent{Event: "update", Data: "hello"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader, err := NewReader(strings.NewReader(test.input), 1024)
			if err != nil {
				t.Fatalf("NewReader: %v", err)
			}

			got, err := reader.Next()
			if err != nil {
				t.Fatalf("first Next error = %v", err)
			}
			if got != test.want {
				t.Fatalf("first event = %#v, want %#v", got, test.want)
			}
			if _, err := reader.Next(); err != io.EOF {
				t.Fatalf("second Next error = %v, want io.EOF", err)
			}
		})
	}
}

func TestReaderParsesMultipleEventsSequentially(t *testing.T) {
	reader, err := NewReader(strings.NewReader("event: first\ndata: one\n\ndata: two\n\n"), 1024)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	for _, want := range []SSEEvent{{Event: "first", Data: "one"}, {Data: "two"}} {
		got, err := reader.Next()
		if err != nil {
			t.Fatalf("Next error = %v, want no error", err)
		}
		if got != want {
			t.Fatalf("event = %#v, want %#v", got, want)
		}
	}
	if _, err := reader.Next(); err != io.EOF {
		t.Fatalf("final Next error = %v, want io.EOF", err)
	}
}

func TestReaderIgnoresCommentsAndAppliesFieldSyntax(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
		want  SSEEvent
	}{
		{name: "empty comment", input: ":\n\ndata: value\n\n", want: SSEEvent{Data: "value"}},
		{name: "heartbeat only", input: ": heartbeat\n:\n\n", want: SSEEvent{}},
		{name: "comments between data lines", input: "data: first\n: heartbeat\ndata: second\n\n", want: SSEEvent{Data: "first\nsecond"}},
		{name: "without space after colon", input: "data:value\n\n", want: SSEEvent{Data: "value"}},
		{name: "with one space after colon", input: "data: value\n\n", want: SSEEvent{Data: "value"}},
		{name: "unknown field", input: "id: ignored\nretry: 1000\ndata: value\n\n", want: SSEEvent{Data: "value"}},
		{name: "unknown fields only", input: "id: ignored\nretry: 1000\n\n", want: SSEEvent{}},
		{name: "done is ordinary data", input: "data: [DONE]\n\n", want: SSEEvent{Data: "[DONE]"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader, err := NewReader(strings.NewReader(test.input), 1024)
			if err != nil {
				t.Fatalf("NewReader: %v", err)
			}

			got, err := reader.Next()
			if test.want == (SSEEvent{}) {
				if err != io.EOF || got != (SSEEvent{}) {
					t.Fatalf("comment-only result = %#v, %v, want empty event and io.EOF", got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("first Next error = %v", err)
			}
			if got != test.want {
				t.Fatalf("event = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestReaderIgnoresInputReadBoundaries(t *testing.T) {
	for _, test := range []struct {
		name   string
		chunks []string
		want   []SSEEvent
	}{
		{
			name:   "split field name",
			chunks: []string{"da", "ta: value\n\n"},
			want:   []SSEEvent{{Data: "value"}},
		},
		{
			name:   "split field value",
			chunks: []string{"data: va", "lue\n\n"},
			want:   []SSEEvent{{Data: "value"}},
		},
		{
			name:   "split LF terminator",
			chunks: []string{"data: value", "\n", "\n"},
			want:   []SSEEvent{{Data: "value"}},
		},
		{
			name:   "split CRLF pair",
			chunks: []string{"data: value\r", "\n", "\r", "\n"},
			want:   []SSEEvent{{Data: "value"}},
		},
		{
			name:   "split event separator",
			chunks: []string{"data: one\n", "\n", "data: two\n\n"},
			want:   []SSEEvent{{Data: "one"}, {Data: "two"}},
		},
		{
			name:   "coalesced events",
			chunks: []string{"data: one\n\ndata: two\n\ndata: three\n\n"},
			want:   []SSEEvent{{Data: "one"}, {Data: "two"}, {Data: "three"}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader, err := NewReader(&chunkReader{chunks: test.chunks}, 1024)
			if err != nil {
				t.Fatalf("NewReader: %v", err)
			}

			for index, want := range test.want {
				got, err := reader.Next()
				if err != nil {
					t.Fatalf("Next %d error = %v, want no error", index, err)
				}
				if got != want {
					t.Fatalf("event %d = %#v, want %#v", index, got, want)
				}
			}
			if _, err := reader.Next(); err != io.EOF {
				t.Fatalf("final Next error = %v, want io.EOF", err)
			}
		})
	}
}

type chunkReader struct {
	chunks []string
	index  int
}

func (reader *chunkReader) Read(destination []byte) (int, error) {
	if reader.index == len(reader.chunks) {
		return 0, io.EOF
	}
	n := copy(destination, reader.chunks[reader.index])
	if n == len(reader.chunks[reader.index]) {
		reader.index++
	}
	return n, nil
}

func TestReaderRejectsFramesOverConfiguredSize(t *testing.T) {
	reader, err := NewReader(strings.NewReader("12345\n\n"), 5)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	if _, err := reader.Next(); err != ErrEventTooLarge {
		t.Fatalf("Next error = %v, want %v", err, ErrEventTooLarge)
	}
}

func TestReaderEnforcesFramedEventSizeBoundaries(t *testing.T) {
	input := "data: x\n\n"
	for _, test := range []struct {
		name      string
		maxSize   int
		wantEvent SSEEvent
		wantError error
	}{
		{name: "below limit", maxSize: len(input) - 1, wantError: ErrEventTooLarge},
		{name: "exactly at limit", maxSize: len(input), wantEvent: SSEEvent{Data: "x"}},
		{name: "above limit", maxSize: len(input) + 1, wantEvent: SSEEvent{Data: "x"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader, err := NewReader(strings.NewReader(input), test.maxSize)
			if err != nil {
				t.Fatalf("NewReader: %v", err)
			}

			got, err := reader.Next()
			if err != test.wantError {
				if test.wantError == nil {
					t.Fatalf("Next error = %v, want no error", err)
				}
				t.Fatalf("Next error = %v, want %v", err, test.wantError)
			}
			if test.wantError == nil && got != test.wantEvent {
				t.Fatalf("event = %#v, want %#v", got, test.wantEvent)
			}
		})
	}
}

func TestReaderRejectsOversizedUnterminatedInput(t *testing.T) {
	input := "data: unterminated"
	reader, err := NewReader(strings.NewReader(input), len(input)-1)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	if _, err := reader.Next(); err != ErrEventTooLarge {
		t.Fatalf("Next error = %v, want %v", err, ErrEventTooLarge)
	}
}

func TestReaderCountsCumulativeMultiLineEventBytes(t *testing.T) {
	input := "data: one\ndata: two\n\n"
	reader, err := NewReader(strings.NewReader(input), len(input)-1)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	if _, err := reader.Next(); err != ErrEventTooLarge {
		t.Fatalf("Next error = %v, want %v", err, ErrEventTooLarge)
	}
}

func TestReaderSizeErrorIsTerminal(t *testing.T) {
	input := &countingReader{reader: strings.NewReader("data: too large\n\ndata: later\n\n")}
	reader, err := NewReader(input, 5)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	first, firstErr := reader.Next()
	second, secondErr := reader.Next()
	if first != (SSEEvent{}) || firstErr != ErrEventTooLarge {
		t.Fatalf("first result = %#v, %v, want terminal size error", first, firstErr)
	}
	readsAfterFirstError := input.reads
	if second != (SSEEvent{}) || secondErr != ErrEventTooLarge {
		t.Fatalf("second result = %#v, %v, want same terminal size error", second, secondErr)
	}
	if input.reads != readsAfterFirstError {
		t.Fatalf("underlying reads after terminal error = %d, want %d", input.reads, readsAfterFirstError)
	}
}

type countingReader struct {
	reader *strings.Reader
	reads  int
}

func (reader *countingReader) Read(destination []byte) (int, error) {
	reader.reads++
	return reader.reader.Read(destination)
}
