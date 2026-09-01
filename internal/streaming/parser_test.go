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

func TestReaderRejectsFramesOverConfiguredSize(t *testing.T) {
	reader, err := NewReader(strings.NewReader("12345\n\n"), 5)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	if _, err := reader.Next(); err != ErrEventTooLarge {
		t.Fatalf("Next error = %v, want %v", err, ErrEventTooLarge)
	}
}
