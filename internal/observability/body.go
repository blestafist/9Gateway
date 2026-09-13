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

// String intentionally reports metadata only. Body payloads must not appear
// in debug output, including output produced while inspecting a snapshot.
func (snapshot BodySnapshot) String() string {
	return fmt.Sprintf("body snapshot{kind=%s bytes=%d original_size=%d truncated=%t captured=%t}", snapshot.Kind, len(snapshot.Bytes), snapshot.OriginalSize, snapshot.Truncated, snapshot.Captured)
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
	frozen    *BodySnapshot
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

// Snapshot returns the current capture and finalizes the recorder. This makes
// every snapshot a terminal handoff: later writes return ErrFinalized and
// cannot change the captured bytes. Repeated snapshots remain safe.
func (recorder *BodyRecorder) Snapshot() BodySnapshot {
	if recorder == nil {
		return BodySnapshot{}
	}
	recorder.freeze(false)
	return recorder.snapshot()
}

// Finalize marks the recorder terminal and returns its final capture. Repeated
// calls are safe and return independent snapshots. A recorder finalized without
// writes is a known empty capture.
func (recorder *BodyRecorder) Finalize() BodySnapshot {
	if recorder == nil {
		return BodySnapshot{}
	}
	recorder.freeze(true)
	return recorder.snapshot()
}

// FinalizeForHandoff makes the recorder terminal without copying its retained
// prefix. It is for internal handoffs that transfer the finalized recorder's
// ownership; Snapshot or Finalize should be used when a value is needed.
func (recorder *BodyRecorder) FinalizeForHandoff() {
	if recorder == nil {
		return
	}
	recorder.freeze(true)
}

func (recorder *BodyRecorder) freeze(finalize bool) {
	if recorder.finalized {
		return
	}
	if finalize {
		// Finalize establishes that body observation completed, including for
		// an otherwise untouched recorder. Snapshot intentionally does not.
		recorder.captured = true
	}
	recorder.finalized = true
	snapshot := BodySnapshot{
		Kind:         recorder.kind,
		Bytes:        recorder.bytes,
		OriginalSize: recorder.original,
		Truncated:    recorder.original > int64(len(recorder.bytes)),
		Captured:     recorder.captured,
	}
	recorder.frozen = &snapshot
}

func (recorder *BodyRecorder) snapshot() BodySnapshot {
	if recorder.frozen != nil {
		snapshot := *recorder.frozen
		if len(snapshot.Bytes) != 0 {
			snapshot.Bytes = make([]byte, len(snapshot.Bytes))
			copy(snapshot.Bytes, recorder.frozen.Bytes)
		} else if cap(snapshot.Bytes) > 0 {
			// Bytes has a backing array but is empty. Return an independent
			// slice: nil for uncaptured, zero-cap for captured.
			if snapshot.Captured {
				snapshot.Bytes = make([]byte, 0)
			} else {
				snapshot.Bytes = nil
			}
		}
		return snapshot
	}
	var bytes []byte
	if len(recorder.bytes) != 0 {
		bytes = make([]byte, len(recorder.bytes))
		copy(bytes, recorder.bytes)
	} else if cap(recorder.bytes) > 0 {
		// Bytes has a backing array but is empty. Return an independent
		// slice: nil for uncaptured, zero-cap for captured.
		if recorder.captured {
			bytes = make([]byte, 0)
		}
	}
	return BodySnapshot{
		Kind:         recorder.kind,
		Bytes:        bytes,
		OriginalSize: recorder.original,
		Truncated:    recorder.original > int64(len(recorder.bytes)),
		Captured:     recorder.captured,
	}
}

// String intentionally reports metadata only and never formats retained body
// bytes. It is safe to use in errors and debug logs.
func (recorder *BodyRecorder) String() string {
	if recorder == nil {
		return "body recorder{nil}"
	}
	return fmt.Sprintf("body recorder{kind=%s bytes=%d original_size=%d truncated=%t captured=%t finalized=%t}", recorder.kind, len(recorder.bytes), recorder.original, recorder.original > int64(len(recorder.bytes)), recorder.captured, recorder.finalized)
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
