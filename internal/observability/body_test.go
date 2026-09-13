package observability

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"testing"
)

func TestBodyRecorderTable(t *testing.T) {
	tests := []struct {
		name         string
		bound        int64
		writes       []string
		wantBytes    []byte
		wantSize     int64
		wantTrunc    bool
		wantCaptured bool
	}{
		{name: "disabled", bound: 0, writes: []string{"binary\x00"}, wantSize: 7, wantTrunc: true, wantCaptured: true},
		{name: "empty", bound: 8, writes: []string{""}, wantBytes: []byte{}, wantCaptured: true},
		{name: "under bound", bound: 8, writes: []string{"abc"}, wantBytes: []byte("abc"), wantSize: 3, wantCaptured: true},
		{name: "exact bound", bound: 3, writes: []string{"abc"}, wantBytes: []byte("abc"), wantSize: 3, wantCaptured: true},
		{name: "over bound", bound: 3, writes: []string{"abcd"}, wantBytes: []byte("abc"), wantSize: 4, wantTrunc: true, wantCaptured: true},
		{name: "fragmented", bound: 5, writes: []string{"a", "bc", "def"}, wantBytes: []byte("abcde"), wantSize: 6, wantTrunc: true, wantCaptured: true},
		{name: "large write", bound: 4, writes: []string{"0123456789"}, wantBytes: []byte("0123"), wantSize: 10, wantTrunc: true, wantCaptured: true},
		{name: "binary", bound: 8, writes: []string{"\x00\xff\xc3\x28"}, wantBytes: []byte{0, 255, 195, 40}, wantSize: 4, wantCaptured: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder, err := NewBodyRecorder(BodyKindResponse, test.bound)
			if err != nil {
				t.Fatal(err)
			}
			for _, write := range test.writes {
				if _, err := recorder.Write([]byte(write)); err != nil {
					t.Fatal(err)
				}
			}
			got := recorder.Snapshot()
			if !bytes.Equal(got.Bytes, test.wantBytes) || got.OriginalSize != test.wantSize || got.Truncated != test.wantTrunc || got.Captured != test.wantCaptured {
				t.Fatalf("snapshot = %#v, want bytes %q size %d truncated %t captured %t", got, test.wantBytes, test.wantSize, test.wantTrunc, test.wantCaptured)
			}
		})
	}
}

func TestBodyRecorderShortObservedCount(t *testing.T) {
	recorder, err := NewBodyRecorder(BodyKindClientRequest, 4)
	if err != nil {
		t.Fatal(err)
	}
	input := []byte("abcdef")
	if err := recorder.WriteObserved(input, 2); err != nil {
		t.Fatal(err)
	}
	got := recorder.Snapshot()
	if !bytes.Equal(got.Bytes, []byte("ab")) || got.OriginalSize != 2 || got.Truncated {
		t.Fatalf("short observation = %#v", got)
	}
	input[0] = 'z'
	if got.Bytes[0] != 'a' {
		t.Fatal("recorder retained caller input")
	}
	other := []byte("xy")
	recorder, err = NewBodyRecorder(BodyKindClientRequest, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.WriteObserved(other, 3); !errors.Is(err, ErrInvalidObservedCount) {
		t.Fatalf("invalid short count error = %v", err)
	}
	if got := recorder.Snapshot(); got.Captured || got.OriginalSize != 0 {
		t.Fatalf("invalid short count mutated recorder = %#v", got)
	}
	if _, err := recorder.Write(input); !errors.Is(err, ErrFinalized) {
		t.Fatalf("post-snapshot write error = %v", err)
	}
}

func TestBodyRecorderKindsAndBounds(t *testing.T) {
	for _, kind := range []BodyKind{BodyKindClientRequest, BodyKindUpstreamRequest, BodyKindResponse} {
		recorder, err := NewBodyRecorder(kind, 1)
		if err != nil {
			t.Fatal(err)
		}
		if got := recorder.Finalize(); got.Kind != kind {
			t.Fatalf("kind = %q, want %q", got.Kind, kind)
		}
	}
	if _, err := NewBodyRecorder(BodyKind("other"), 1); !errors.Is(err, ErrInvalidBodyKind) {
		t.Fatalf("invalid kind error = %v", err)
	}
	if _, err := NewBodyRecorder(BodyKindResponse, -1); err == nil {
		t.Fatal("negative bound accepted")
	}
}

func TestBodyRecorderSnapshotOwnershipAndFinalize(t *testing.T) {
	for _, test := range []struct {
		name      string
		bound     int64
		write     string
		wantBytes string
		captured  bool
		truncated bool
		size      int64
	}{
		{name: "untouched", bound: 4},
		{name: "empty-write", bound: 4, write: "", captured: true},
		{name: "under-bound", bound: 4, write: "abc", wantBytes: "abc", captured: true, size: 3},
		{name: "over-bound", bound: 4, write: "abcde", wantBytes: "abcd", captured: true, truncated: true, size: 5},
		{name: "zero-bound", bound: 0, write: "abc", captured: true, truncated: true, size: 3},
	} {
		for _, firstCall := range []string{"snapshot", "finalize"} {
			t.Run(test.name+"/"+firstCall, func(t *testing.T) {
				recorder, err := NewBodyRecorder(BodyKindUpstreamRequest, test.bound)
				if err != nil {
					t.Fatal(err)
				}
				if test.name != "untouched" {
					if _, err := recorder.Write([]byte(test.write)); err != nil {
						t.Fatal(err)
					}
				}
				var first BodySnapshot
				if firstCall == "snapshot" {
					first = recorder.Snapshot()
				} else {
					first = recorder.Finalize()
				}
				second := recorder.Finalize()
				third := recorder.Snapshot()
				fourth := recorder.Finalize()
				for name, got := range map[string]BodySnapshot{"finalize": second, "snapshot again": third, "finalize again": fourth} {
					if !bytes.Equal(got.Bytes, first.Bytes) || got.Kind != first.Kind || got.OriginalSize != first.OriginalSize || got.Truncated != first.Truncated || got.Captured != first.Captured {
						t.Fatalf("%s = %#v, first = %#v", name, got, first)
					}
				}
				wantCaptured := test.captured || (test.name == "untouched" && firstCall == "finalize")
				if !bytes.Equal(first.Bytes, []byte(test.wantBytes)) || first.Captured != wantCaptured || first.Truncated != test.truncated || first.OriginalSize != test.size {
					t.Fatalf("snapshot = %#v", first)
				}
				if _, err := recorder.Write([]byte("late")); !errors.Is(err, ErrFinalized) {
					t.Fatalf("post-finalize write error = %v", err)
				}
				fourth.Bytes = append(fourth.Bytes, 'x')
				if got := recorder.Snapshot(); !bytes.Equal(got.Bytes, first.Bytes) {
					t.Fatalf("final snapshot changed after caller mutation = %#v", got)
				}
			})
		}
	}

	recorder, err := NewBodyRecorder(BodyKindResponse, 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Write([]byte("abcd")); err != nil {
		t.Fatal(err)
	}
	one := recorder.Snapshot()
	one.Bytes[0] = 'x'
	two := recorder.Snapshot()
	if !bytes.Equal(two.Bytes, []byte("abcd")) || cap(two.Bytes) != len(two.Bytes) {
		t.Fatalf("snapshot ownership/capacity = %q cap %d", two.Bytes, cap(two.Bytes))
	}
}

func TestBodyRecorderDebugFormattingDoesNotContainPayload(t *testing.T) {
	recorder, err := NewBodyRecorder(BodyKindResponse, 8)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Write([]byte("secret\x00")); err != nil {
		t.Fatal(err)
	}
	snapshot := recorder.Snapshot()
	for name, formatted := range map[string]string{
		"recorder": fmt.Sprintf("%v", recorder),
		"snapshot": fmt.Sprintf("%v", snapshot),
	} {
		if bytes.Contains([]byte(formatted), []byte("secret")) {
			t.Fatalf("%s formatting leaked payload: %q", name, formatted)
		}
	}
}

func TestBodyRecorderDisabledDoesNotAllocate(t *testing.T) {
	recorder, err := NewBodyRecorder(BodyKindResponse, 0)
	if err != nil {
		t.Fatal(err)
	}
	if recorder.bytes != nil || cap(recorder.bytes) != 0 {
		t.Fatalf("disabled recorder allocated backing storage: %#v", recorder.bytes)
	}
	if _, err := recorder.Write([]byte("payload")); err != nil {
		t.Fatal(err)
	}
	if recorder.bytes != nil {
		t.Fatal("disabled recorder allocated after write")
	}
}

func TestBodyRecorderOriginalSizeOverflow(t *testing.T) {
	recorder, err := NewBodyRecorder(BodyKindResponse, 8)
	if err != nil {
		t.Fatal(err)
	}
	recorder.original = math.MaxInt64 - 1
	if err := recorder.WriteObserved([]byte("ab"), 2); !errors.Is(err, ErrOriginalSizeOverflow) {
		t.Fatalf("overflow error = %v", err)
	}
	if recorder.original != math.MaxInt64-1 || recorder.captured {
		t.Fatalf("overflow mutated recorder: %#v", recorder)
	}
}

func FuzzBodyRecorderPrefix(f *testing.F) {
	f.Add([]byte("hello"), uint8(3))
	f.Add([]byte{0, 255, 1, 2}, uint8(1))
	f.Fuzz(func(t *testing.T, input []byte, bound uint8) {
		limit := int64(bound)
		recorder, err := NewBodyRecorder(BodyKindClientRequest, limit)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := recorder.Write(input); err != nil {
			t.Fatal(err)
		}
		got := recorder.Snapshot()
		wantLen := len(input)
		if wantLen > int(limit) {
			wantLen = int(limit)
		}
		if !bytes.Equal(got.Bytes, input[:wantLen]) || got.OriginalSize != int64(len(input)) || got.Truncated != (len(input) > wantLen) {
			t.Fatalf("snapshot = %#v, want prefix length %d", got, wantLen)
		}
		if cap(recorder.bytes) > int(limit) {
			t.Fatalf("capacity %d exceeds bound %d", cap(recorder.bytes), limit)
		}
	})
}
