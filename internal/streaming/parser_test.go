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
	reader, err := NewReader(strings.NewReader("input\n\n"), 1024)
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
	reader, err := NewReader(strings.NewReader("first\n\nsecond\n\n"), 1024)
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

func TestReaderRejectsFramesOverConfiguredSize(t *testing.T) {
	reader, err := NewReader(strings.NewReader("12345\n\n"), 5)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	if _, err := reader.Next(); err != ErrEventTooLarge {
		t.Fatalf("Next error = %v, want %v", err, ErrEventTooLarge)
	}
}
