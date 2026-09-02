package openai

import (
	"errors"
	"testing"

	"github.com/pestit/9gateway/internal/streaming"
)

func TestObserverAcceptsValidJSONChunk(t *testing.T) {
	observer := NewObserver()

	err := observer.Observe(streaming.SSEEvent{Data: `{"id":"chunk-1","object":"chat.completion.chunk","created":123,"model":"gpt-test","choices":[]}`})
	if err != nil {
		t.Fatalf("Observe error = %v, want nil", err)
	}
	if got := observer.State().EventsObserved; got != 1 {
		t.Fatalf("EventsObserved = %d, want 1", got)
	}
}

func TestObserverMalformedJSONDoesNotBecomeTerminal(t *testing.T) {
	observer := NewObserver()

	err := observer.Observe(streaming.SSEEvent{Data: `{"id":"incomplete"`})
	if !errors.Is(err, ErrMalformedStreamChunk) {
		t.Fatalf("malformed Observe error = %v, want errors.Is(..., ErrMalformedStreamChunk)", err)
	}
	if got := observer.State().EventsObserved; got != 0 {
		t.Fatalf("EventsObserved after malformed chunk = %d, want 0", got)
	}

	if err := observer.Observe(streaming.SSEEvent{Data: `{"id":"chunk-2","unknown":{"kept":true}}`}); err != nil {
		t.Fatalf("valid Observe after malformed chunk = %v, want nil", err)
	}
	if got := observer.State().EventsObserved; got != 1 {
		t.Fatalf("EventsObserved after later valid chunk = %d, want 1", got)
	}
}

func TestObserverIgnoresEventNamesAndUnknownJSONFields(t *testing.T) {
	observer := NewObserver()

	for _, event := range []streaming.SSEEvent{
		{Event: "message", Data: `{"id":"chunk-1","future":{"nested":[1,true,"value"]}}`},
		{Event: "delta", Data: `{"id":"chunk-2","another_unknown":null}`},
	} {
		if err := observer.Observe(event); err != nil {
			t.Fatalf("Observe(%q) error = %v, want nil", event.Event, err)
		}
	}

	if got := observer.State().EventsObserved; got != 2 {
		t.Fatalf("EventsObserved = %d, want 2", got)
	}
}

func TestObserverRejectsNonObjectJSONWithoutChangingState(t *testing.T) {
	observer := NewObserver()

	for _, data := range []string{"", "null", "[]", "[1]", "not-json"} {
		if err := observer.Observe(streaming.SSEEvent{Data: data}); !errors.Is(err, ErrMalformedStreamChunk) {
			t.Fatalf("Observe(%q) error = %v, want errors.Is(..., ErrMalformedStreamChunk)", data, err)
		}
	}
	if got := observer.State().EventsObserved; got != 0 {
		t.Fatalf("EventsObserved = %d, want 0", got)
	}
}
