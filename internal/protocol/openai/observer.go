package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/pestit/9gateway/internal/streaming"
)

// ErrMalformedStreamChunk indicates that an SSE event did not contain a JSON
// object representing an OpenAI streaming chunk.
var ErrMalformedStreamChunk = errors.New("openai: malformed streaming chunk")

// ObserverState is the state accumulated by an Observer. It records only the
// successfully parsed chunks; it has no transport or completion semantics.
type ObserverState struct {
	EventsObserved int
}

// ResponseMetadata is the small set of response envelope fields observed by
// an Observer. Created is a pointer so an explicit zero remains distinct from
// an absent field.
type ResponseMetadata struct {
	ID      string `json:"id,omitempty"`
	Model   string `json:"model,omitempty"`
	Created *int64 `json:"created,omitempty"`
}

// Observer parses OpenAI streaming chunks from complete, protocol-neutral SSE
// events. It deliberately does not read from or write to a transport.
type Observer struct {
	state    ObserverState
	metadata ResponseMetadata
}

// NewObserver creates an empty OpenAI streaming observer.
func NewObserver() *Observer {
	return &Observer{}
}

// State returns a copy of the observer's current state.
func (observer *Observer) State() ObserverState {
	return observer.state
}

// Metadata returns a snapshot of the response envelope metadata observed so
// far. The Created pointer is copied so callers cannot mutate observer state.
func (observer *Observer) Metadata() ResponseMetadata {
	metadata := observer.metadata
	if metadata.Created != nil {
		created := *metadata.Created
		metadata.Created = &created
	}
	return metadata
}

// Observe parses one complete SSE event. Event names are transport metadata
// and do not affect OpenAI chunk parsing. A malformed event returns an error,
// but does not make the observer terminal; subsequent events can be observed.
func (observer *Observer) Observe(event streaming.SSEEvent) error {
	data := bytes.TrimSpace([]byte(event.Data))
	if len(data) == 0 || data[0] != '{' {
		return fmt.Errorf("%w: expected a JSON object", ErrMalformedStreamChunk)
	}

	var chunk streamChunk
	if err := json.Unmarshal(data, &chunk); err != nil {
		return fmt.Errorf("%w: %v", ErrMalformedStreamChunk, err)
	}

	observer.state.EventsObserved++
	observer.observeMetadata(chunk)
	return nil
}

func (observer *Observer) observeMetadata(chunk streamChunk) {
	if observer.metadata.ID == "" {
		var id string
		if err := json.Unmarshal(chunk.ID, &id); err == nil && id != "" {
			observer.metadata.ID = id
		}
	}
	if observer.metadata.Model == "" {
		var model string
		if err := json.Unmarshal(chunk.Model, &model); err == nil && model != "" {
			observer.metadata.Model = model
		}
	}
	if observer.metadata.Created == nil && len(chunk.Created) > 0 {
		var created int64
		value := bytes.TrimSpace(chunk.Created)
		if !bytes.Equal(value, []byte("null")) && json.Unmarshal(value, &created) == nil {
			observer.metadata.Created = &created
		}
	}
}

// streamChunk is intentionally limited to the response envelope. The
// observer validates JSON framing without modeling the full OpenAI schema or
// assigning meaning to fields that later observers may inspect.
type streamChunk struct {
	ID      json.RawMessage `json:"id"`
	Object  json.RawMessage `json:"object"`
	Created json.RawMessage `json:"created"`
	Model   json.RawMessage `json:"model"`
	Choices json.RawMessage `json:"choices"`
	Usage   json.RawMessage `json:"usage"`
}
