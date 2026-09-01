// Package streaming contains protocol-neutral helpers for server-sent events.
package streaming

import (
	"bufio"
	"errors"
	"io"
)

// SSEEvent is one complete server-sent event. Data and Event are owned by the
// returned value and remain valid until the caller changes or discards them.
type SSEEvent struct {
	Event string
	Data  string
}

// ErrEventTooLarge indicates that a framed event exceeds the configured limit.
var ErrEventTooLarge = errors.New("streaming: event exceeds maximum size")

// Reader emits complete SSE events sequentially from a stream. maxEventSize is
// measured in bytes of one framed event, including its input line endings.
type Reader struct {
	input        *bufio.Reader
	maxEventSize int
}

// NewReader creates an SSE reader with a positive maximum framed event size.
// The limit bounds the parser's retained input and is independent of protocol
// semantics or any transport buffer size.
func NewReader(input io.Reader, maxEventSize int) (*Reader, error) {
	if input == nil {
		return nil, errors.New("streaming: nil input")
	}
	if maxEventSize <= 0 {
		return nil, errors.New("streaming: max event size must be positive")
	}

	bufferSize := maxEventSize
	if bufferSize > 4*1024 {
		bufferSize = 4 * 1024
	}
	return &Reader{
		input:        bufio.NewReaderSize(input, bufferSize),
		maxEventSize: maxEventSize,
	}, nil
}

// Next returns the next complete event. An input stream with no event returns
// io.EOF, and EOF after a completed event is returned on the following call.
// Field interpretation is intentionally separate from this framing contract.
func (reader *Reader) Next() (SSEEvent, error) {
	eventSize := 0
	hasContent := false
	line := make([]byte, 0, 2)

	for {
		part, readErr := reader.input.ReadSlice('\n')
		eventSize += len(part)
		if eventSize > reader.maxEventSize {
			return SSEEvent{}, ErrEventTooLarge
		}
		line = append(line, part...)
		if len(line) > 0 && !(len(line) == 1 && line[0] == '\n') && !(len(line) == 2 && line[0] == '\r' && line[1] == '\n') {
			hasContent = true
		}

		if readErr == bufio.ErrBufferFull {
			continue
		}
		if readErr != nil && readErr != io.EOF {
			return SSEEvent{}, readErr
		}

		if len(line) == 1 && line[0] == '\n' || len(line) == 2 && line[0] == '\r' && line[1] == '\n' {
			if hasContent {
				return SSEEvent{}, nil
			}
			eventSize = 0
			line = line[:0]
		}

		if readErr == io.EOF {
			if !hasContent {
				return SSEEvent{}, io.EOF
			}
			return SSEEvent{}, nil
		}

		line = line[:0]
	}
}
