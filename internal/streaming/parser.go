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
	eventSize    int
	eventName    string
	dataLines    []string
	hasContent   bool
	line         []byte
	terminalErr  error
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
		dataLines:    make([]string, 0, 1),
	}, nil
}

// Next returns the next complete event. Data lines are joined with a newline,
// and an optional event field supplies the event name. An input stream with no
// event returns io.EOF, and EOF after a completed event is returned on the
// following call. Returned strings are independent of the reader's buffers.
func (reader *Reader) Next() (SSEEvent, error) {
	if reader.terminalErr != nil {
		return SSEEvent{}, reader.terminalErr
	}

	for {
		part, readErr := reader.input.ReadSlice('\n')
		reader.eventSize += len(part)
		if reader.eventSize > reader.maxEventSize {
			reader.terminalErr = ErrEventTooLarge
			return SSEEvent{}, ErrEventTooLarge
		}
		reader.line = append(reader.line, part...)

		if readErr == bufio.ErrBufferFull {
			continue
		}
		if readErr != nil && readErr != io.EOF {
			return SSEEvent{}, readErr
		}

		if isBlankLine(reader.line) {
			if reader.hasContent {
				event := SSEEvent{Event: reader.eventName, Data: strings.Join(reader.dataLines, "\n")}
				reader.resetEvent()
				return event, nil
			}
			reader.resetEvent()
		} else if readErr == nil {
			field, value := parseField(reader.line)
			if isCommentLine(reader.line) {
				reader.line = reader.line[:0]
				continue
			}
			reader.hasContent = true
			switch field {
			case "data":
				reader.dataLines = append(reader.dataLines, value)
			case "event":
				reader.eventName = value
			}
		}

		if readErr == io.EOF {
			reader.terminalErr = io.EOF
			return SSEEvent{}, io.EOF
		}

		reader.line = reader.line[:0]
	}
}

func (reader *Reader) resetEvent() {
	reader.eventSize = 0
	reader.hasContent = false
	reader.eventName = ""
	reader.dataLines = reader.dataLines[:0]
	reader.line = reader.line[:0]
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
