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

func TestObserverCapturesResponseMetadataTogetherAndAcrossChunks(t *testing.T) {
	observer := NewObserver()

	for _, data := range []string{
		`{"id":"response-1","model":"gpt-test","created":123}`,
		`{"choices":[],"id":"response-2","model":"other-model","created":456}`,
	} {
		if err := observer.Observe(streaming.SSEEvent{Data: data}); err != nil {
			t.Fatalf("Observe(%q) error = %v, want nil", data, err)
		}
	}

	metadata := observer.Metadata()
	if metadata.ID != "response-1" {
		t.Fatalf("metadata ID = %q, want %q", metadata.ID, "response-1")
	}
	if metadata.Model != "gpt-test" {
		t.Fatalf("metadata Model = %q, want %q", metadata.Model, "gpt-test")
	}
	if metadata.Created == nil || *metadata.Created != 123 {
		t.Fatalf("metadata Created = %v, want 123", metadata.Created)
	}
}

func TestObserverMetadataMissingFieldsAndFirstPresentValues(t *testing.T) {
	observer := NewObserver()

	for _, data := range []string{
		`{"choices":[]}`,
		`{"id":"","model":"","created":0}`,
		`{"id":"response-1"}`,
		`{"model":"gpt-test"}`,
	} {
		if err := observer.Observe(streaming.SSEEvent{Data: data}); err != nil {
			t.Fatalf("Observe(%q) error = %v, want nil", data, err)
		}
	}

	metadata := observer.Metadata()
	if metadata.ID != "response-1" {
		t.Fatalf("metadata ID = %q, want %q", metadata.ID, "response-1")
	}
	if metadata.Model != "gpt-test" {
		t.Fatalf("metadata Model = %q, want %q", metadata.Model, "gpt-test")
	}
	if metadata.Created == nil || *metadata.Created != 0 {
		t.Fatalf("metadata Created = %v, want present zero", metadata.Created)
	}
}

func TestObserverMetadataKeepsFirstConflictingValues(t *testing.T) {
	observer := NewObserver()

	for _, data := range []string{
		`{"id":"first-id","model":"first-model","created":1}`,
		`{"id":"later-id","model":"later-model","created":2}`,
	} {
		if err := observer.Observe(streaming.SSEEvent{Data: data}); err != nil {
			t.Fatalf("Observe(%q) error = %v, want nil", data, err)
		}
	}

	metadata := observer.Metadata()
	if metadata.ID != "first-id" || metadata.Model != "first-model" {
		t.Fatalf("metadata = %+v, want first ID and model", metadata)
	}
	if metadata.Created == nil || *metadata.Created != 1 {
		t.Fatalf("metadata Created = %v, want 1", metadata.Created)
	}
}

func TestObserverMetadataSnapshotDoesNotExposeObserverState(t *testing.T) {
	observer := NewObserver()
	if err := observer.Observe(streaming.SSEEvent{Data: `{"id":"response-1","model":"gpt-test","created":123}`}); err != nil {
		t.Fatalf("Observe error = %v, want nil", err)
	}

	metadata := observer.Metadata()
	metadata.Created = int64Pointer(999)
	*metadata.Created = 1000
	metadata.ID = "changed"
	metadata.Model = "changed"

	got := observer.Metadata()
	if got.ID != "response-1" || got.Model != "gpt-test" {
		t.Fatalf("observer metadata = %+v, changed through snapshot", got)
	}
	if got.Created == nil || *got.Created != 123 {
		t.Fatalf("observer Created = %v, changed through snapshot", got.Created)
	}
}

func TestObserverMalformedChunkDoesNotEraseResponseMetadata(t *testing.T) {
	observer := NewObserver()
	if err := observer.Observe(streaming.SSEEvent{Data: `{"id":"response-1","model":"gpt-test","created":123}`}); err != nil {
		t.Fatalf("initial Observe error = %v, want nil", err)
	}

	if err := observer.Observe(streaming.SSEEvent{Data: `{"id":"later-id","model":`}); !errors.Is(err, ErrMalformedStreamChunk) {
		t.Fatalf("malformed Observe error = %v, want errors.Is(..., ErrMalformedStreamChunk)", err)
	}

	metadata := observer.Metadata()
	if metadata.ID != "response-1" || metadata.Model != "gpt-test" {
		t.Fatalf("metadata = %+v, prior metadata was erased", metadata)
	}
	if metadata.Created == nil || *metadata.Created != 123 {
		t.Fatalf("metadata Created = %v, prior metadata was erased", metadata.Created)
	}
}

func int64Pointer(value int64) *int64 {
	return &value
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
