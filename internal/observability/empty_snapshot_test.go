package observability

import (
	"testing"
)

// TestBodyRecorderEmptySnapshotAliasing verifies that empty snapshots are
// independently owned and that appending to one cannot affect another.
// This is the regression test for issue 1.
func TestBodyRecorderEmptySnapshotAliasing(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*BodyRecorder) error
	}{
		{
			name: "explicit empty write",
			setup: func(r *BodyRecorder) error {
				_, err := r.Write([]byte(""))
				return err
			},
		},
		{
			name: "positive bound with zero bytes",
			setup: func(r *BodyRecorder) error {
				_, err := r.Write([]byte{})
				return err
			},
		},
		{
			name: "finalize without writes",
			setup: func(r *BodyRecorder) error {
				r.FinalizeForHandoff()
				return nil
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder, err := NewBodyRecorder(BodyKindResponse, 100)
			if err != nil {
				t.Fatal(err)
			}

			if err := test.setup(recorder); err != nil {
				t.Fatal(err)
			}

			// Get two snapshots of the empty capture
			snap1 := recorder.Finalize()
			snap2 := recorder.Snapshot()

			// Verify both are captured empty snapshots
			if !snap1.Captured || len(snap1.Bytes) != 0 {
				t.Fatalf("snap1 = captured:%v len:%d, want captured:true len:0",
					snap1.Captured, len(snap1.Bytes))
			}
			if !snap2.Captured || len(snap2.Bytes) != 0 {
				t.Fatalf("snap2 = captured:%v len:%d, want captured:true len:0",
					snap2.Captured, len(snap2.Bytes))
			}

			// Verify capacity equals length (no excess backing array)
			if cap(snap1.Bytes) != len(snap1.Bytes) {
				t.Errorf("snap1: cap=%d len=%d, want cap==len",
					cap(snap1.Bytes), len(snap1.Bytes))
			}
			if cap(snap2.Bytes) != len(snap2.Bytes) {
				t.Errorf("snap2: cap=%d len=%d, want cap==len",
					cap(snap2.Bytes), len(snap2.Bytes))
			}

			// Append to snap1 and verify snap2 is unaffected
			snap1.Bytes = append(snap1.Bytes, 'x', 'y', 'z')

			if len(snap2.Bytes) != 0 {
				t.Errorf("after append to snap1, snap2.Bytes = %q, want empty",
					snap2.Bytes)
			}

			// Get a third snapshot and verify it's also unaffected
			snap3 := recorder.Finalize()
			if len(snap3.Bytes) != 0 {
				t.Errorf("snap3.Bytes = %q, want empty", snap3.Bytes)
			}
			if cap(snap3.Bytes) != len(snap3.Bytes) {
				t.Errorf("snap3: cap=%d len=%d, want cap==len",
					cap(snap3.Bytes), len(snap3.Bytes))
			}
		})
	}
}

// TestBodyRecorderUntouchedSnapshotRemainNil verifies that snapshots from
// untouched recorders (no writes, no finalize) have nil byte slices.
func TestBodyRecorderUntouchedSnapshotRemainNil(t *testing.T) {
	recorder, err := NewBodyRecorder(BodyKindResponse, 100)
	if err != nil {
		t.Fatal(err)
	}

	snap := recorder.Snapshot()

	if snap.Captured {
		t.Errorf("untouched snapshot: Captured=%v, want false", snap.Captured)
	}
	if snap.Bytes != nil {
		t.Errorf("untouched snapshot: Bytes=%#v, want nil", snap.Bytes)
	}
	if cap(snap.Bytes) != 0 {
		t.Errorf("untouched snapshot: cap(Bytes)=%d, want 0", cap(snap.Bytes))
	}
}
