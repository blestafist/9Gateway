// Package streaming contains protocol-neutral helpers for server-sent events.
package streaming

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
)

// SSEEvent is one complete server-sent event. Data and Event are owned by the
// returned value and remain valid until the caller changes or discards them.
type SSEEvent struct {
	Event string
	Data  string
}

// ErrEventTooLarge indicates that a framed event exceeds the configured limit.
var ErrEventTooLarge = errors.New("streaming: event exceeds maximum size")

// Reader emits complete SSE events sequentially from a stream. It joins data
// fields with newlines, uses a blank line to complete an event, and ignores
// fields it does not understand. maxEventSize is measured in bytes of one
// framed event, including its input line endings.
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

// Next returns the next complete event. Data lines are joined with a newline,
// and an optional event field supplies the event name. An input stream with no
// event returns io.EOF, and EOF after a completed event is returned on the
// following call. Returned strings are independent of the reader's buffers.
func (reader *Reader) Next() (SSEEvent, error) {
	eventSize := 0
	hasContent := false
	eventName := ""
	dataLines := make([]string, 0, 1)
	line := make([]byte, 0, 2)

	for {
		part, readErr := reader.input.ReadSlice('\n')
		eventSize += len(part)
		if eventSize > reader.maxEventSize {
			return SSEEvent{}, ErrEventTooLarge
		}
		line = append(line, part...)

		if readErr == bufio.ErrBufferFull {
			continue
		}
		if readErr != nil && readErr != io.EOF {
			return SSEEvent{}, readErr
		}

		if isBlankLine(line) {
			if hasContent {
				return SSEEvent{Event: eventName, Data: strings.Join(dataLines, "\n")}, nil
			}
			eventSize = 0
			hasContent = false
			eventName = ""
			dataLines = dataLines[:0]
			line = line[:0]
		} else if readErr == nil {
			field, value := parseField(line)
			if isCommentLine(line) {
				line = line[:0]
				continue
			}
			hasContent = true
			switch field {
			case "data":
				dataLines = append(dataLines, value)
			case "event":
				eventName = value
			}
		}

		if readErr == io.EOF {
			return SSEEvent{}, io.EOF
		}

		line = line[:0]
	}
}

func isCommentLine(line []byte) bool {
	line = trimLineEnding(line)
	return len(line) > 0 && line[0] == ':'
}

func parseField(line []byte) (string, string) {
	line = trimLineEnding(line)
	colon := bytes.IndexByte(line, ':')
	if colon < 0 {
		return string(line), ""
	}
	value := line[colon+1:]
	if len(value) > 0 && value[0] == ' ' {
		value = value[1:]
	}
	return string(line[:colon]), string(value)
}

func trimLineEnding(line []byte) []byte {
	if len(line) > 0 && line[len(line)-1] == '\n' {
		line = line[:len(line)-1]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
	}
	return line
}

func isBlankLine(line []byte) bool {
	return len(line) == 1 && line[0] == '\n' || len(line) == 2 && line[0] == '\r' && line[1] == '\n'
}
