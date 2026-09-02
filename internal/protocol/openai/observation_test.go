package openai

import (
	"bytes"
	"errors"
	"testing"

	"github.com/pestit/9gateway/internal/streaming"
)

func TestObserveStreamContinuesAfterObserverError(t *testing.T) {
	input := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"first\"}}]}\n\n" +
		"data: {malformed\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"last\"}}]}\n\n"
	var reported []error

	result, err := ObserveStream(bytes.NewBufferString(input), 1024, func(err error) {
		reported = append(reported, err)
	})
	if err != nil {
		t.Fatalf("ObserveStream error = %v, want nil", err)
	}
	if len(reported) != 1 {
		t.Fatalf("reported errors = %d, want 1", len(reported))
	}
	if len(result.Errors) != 1 || !errors.Is(result.Errors[0], ErrMalformedStreamChunk) {
		t.Fatalf("collected errors = %v, want one malformed chunk error", result.Errors)
	}
	if result.State.EventsObserved != 2 {
		t.Fatalf("EventsObserved = %d, want 2", result.State.EventsObserved)
	}
	if len(result.State.Choices) != 2 {
		t.Fatalf("choices = %d, want 2", len(result.State.Choices))
	}
	if result.State.Choices[0].Delta.Content == nil || *result.State.Choices[0].Delta.Content != "first" {
		t.Fatalf("first observed content = %v, want first", result.State.Choices[0].Delta.Content)
	}
	if result.State.Choices[1].Delta.Content == nil || *result.State.Choices[1].Delta.Content != "last" {
		t.Fatalf("last observed content = %v, want last", result.State.Choices[1].Delta.Content)
	}
}

func TestObserveStreamStopsOnReaderSizeError(t *testing.T) {
	result, err := ObserveStream(bytes.NewBufferString("data: too large\n\ndata: later\n\n"), 5, nil)
	if !errors.Is(err, streaming.ErrEventTooLarge) {
		t.Fatalf("ObserveStream error = %v, want ErrEventTooLarge", err)
	}
	if result.State.EventsObserved != 0 || len(result.Errors) != 0 {
		t.Fatalf("partial result = %+v, want no observations", result)
	}
}

func TestObserveStreamDoesNotModifyCopiedSource(t *testing.T) {
	source := []byte("data: {\"id\":\"one\"}\n\ndata: {malformed\n\ndata: {\"id\":\"two\"}\n\n")
	wantSource := append([]byte(nil), source...)

	result, err := ObserveStream(bytes.NewReader(source), 1024, nil)
	if err != nil {
		t.Fatalf("ObserveStream error = %v, want nil", err)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("collected errors = %d, want 1", len(result.Errors))
	}
	if !bytes.Equal(source, wantSource) {
		t.Fatalf("source changed after observation: got %q, want %q", source, wantSource)
	}
}
