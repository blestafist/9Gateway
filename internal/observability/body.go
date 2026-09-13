// Package observability contains bounded, protocol-independent observation
// values used by the gateway.
package observability

import (
	"errors"
	"fmt"
	"math"
)

// BodyKind identifies the only body sources that may be persisted. Keeping
// this closed set here prevents a body from silently losing its direction when
// it is handed to later observability stages.
type BodyKind string

const (
	BodyKindClientRequest   BodyKind = "client_request"
	BodyKindUpstreamRequest BodyKind = "upstream_request"
	BodyKindResponse        BodyKind = "response"
)

var (
	ErrInvalidBodyKind      = errors.New("observability: invalid body kind")
	ErrInvalidObservedCount = errors.New("observability: invalid observed byte count")
	ErrOriginalSizeOverflow = errors.New("observability: original body size overflow")
	ErrFinalized            = errors.New("observability: body recorder is finalized")
)

// BodySnapshot is an immutable-by-ownership view of a body capture. Bytes is
// always a separate, cap=len copy made by Snapshot or Finalize; callers may
// mutate their copy without changing the recorder or another snapshot.
// Captured distinguishes a body known to be empty from a recorder that has not
// observed a body yet.
type BodySnapshot struct {
	Kind         BodyKind
	Bytes        []byte
	OriginalSize int64
	Truncated    bool
	Captured     bool
}

// BodyRecorder retains a prefix of an observed body without interpreting it.
// It is deliberately not safe for concurrent use; callers must provide any
// needed synchronization at the observation boundary.
type BodyRecorder struct {
	kind      BodyKind
	maxBytes  int64
	bytes     []byte
	original  int64
	captured  bool
	finalized bool
}

// NewBodyRecorder creates a recorder for kind with a fixed retention bound.
// A zero bound is disabled and does not allocate a backing array.
func NewBodyRecorder(kind BodyKind, maxBytes int64) (*BodyRecorder, error) {
	if !validBodyKind(kind) {
		return nil, ErrInvalidBodyKind
	}
	if maxBytes < 0 {
		return nil, fmt.Errorf("observability: negative body bound %d", maxBytes)
	}
	if maxBytes > int64(maxInt()) {
		return nil, fmt.Errorf("observability: body bound %d is too large", maxBytes)
	}
	recorder := &BodyRecorder{kind: kind, maxBytes: maxBytes}
	if maxBytes != 0 {
		recorder.bytes = make([]byte, 0, int(maxBytes))
	}
	return recorder, nil
}

// Write records all of p as observed bytes and implements io.Writer.
// Once finalized, writes are rejected with ErrFinalized and do not change the
// recorder. Finalization is intentionally terminal so later transport code
// cannot silently alter a handed-off snapshot.
func (recorder *BodyRecorder) Write(p []byte) (int, error) {
	if err := recorder.writeObserved(p, len(p)); err != nil {
		return 0, err
	}
	return len(p), nil
}

// WriteObserved records observed bytes from the prefix p[:accepted]. It is
// used when the caller has a short accepted count for a larger input buffer.
// accepted must be between zero and len(p), inclusive.
func (recorder *BodyRecorder) WriteObserved(p []byte, accepted int) error {
	return recorder.writeObserved(p, accepted)
}

func (recorder *BodyRecorder) writeObserved(p []byte, accepted int) error {
	if recorder == nil {
		return ErrFinalized
	}
	if recorder.finalized {
		return ErrFinalized
	}
	if accepted < 0 || accepted > len(p) {
		return ErrInvalidObservedCount
	}
	if int64(accepted) > math.MaxInt64-recorder.original {
		return ErrOriginalSizeOverflow
	}

	recorder.captured = true
	recorder.original += int64(accepted)
	if len(recorder.bytes) >= int(recorder.maxBytes) || accepted == 0 {
		return nil
	}
	remaining := int(recorder.maxBytes) - len(recorder.bytes)
	if accepted < remaining {
		remaining = accepted
	}
	recorder.bytes = append(recorder.bytes, p[:remaining]...)
	return nil
}

// Snapshot returns the current capture. It does not finalize the recorder.
func (recorder *BodyRecorder) Snapshot() BodySnapshot {
	if recorder == nil {
		return BodySnapshot{}
	}
	return recorder.snapshot()
}

// Finalize marks the recorder terminal and returns its final capture. Repeated
// calls are safe and return independent snapshots. A recorder finalized without
// writes is a known empty capture.
func (recorder *BodyRecorder) Finalize() BodySnapshot {
	if recorder == nil {
		return BodySnapshot{}
	}
	if !recorder.finalized {
		recorder.finalized = true
		recorder.captured = true
	}
	return recorder.snapshot()
}

func (recorder *BodyRecorder) snapshot() BodySnapshot {
	bytes := make([]byte, len(recorder.bytes))
	copy(bytes, recorder.bytes)
	return BodySnapshot{
		Kind:         recorder.kind,
		Bytes:        bytes,
		OriginalSize: recorder.original,
		Truncated:    recorder.original > int64(len(recorder.bytes)),
		Captured:     recorder.captured,
	}
}

func validBodyKind(kind BodyKind) bool {
	switch kind {
	case BodyKindClientRequest, BodyKindUpstreamRequest, BodyKindResponse:
		return true
	default:
		return false
	}
}

func maxInt() int {
	return int(^uint(0) >> 1)
}
