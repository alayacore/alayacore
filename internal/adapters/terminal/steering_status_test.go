package terminal

import (
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/tlv"
)

// steeringPhase feeds one "steering" SM frame the way the session writes it.
func steeringPhase(t *testing.T, out interface{ Write([]byte) (int, error) }, phase string) {
	t.Helper()
	frame := `{"type":"steering","data":{"phase":"` + phase + `"}}`
	if err := tlv.WriteTLV(out, tlv.TagSystemMsg, frame); err != nil {
		t.Fatalf("write steering %q: %v", phase, err)
	}
}

// A "steering" SM drives the queue count: queued counts in, delivered/dropped
// clear the batch (it is all-or-nothing), refused never entered, and a
// delivered written before its queued (the two session writers can invert)
// leaves no stuck count.
func TestSteeringSMDrivesQueuedCount(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	got := func() int { return out.SnapshotStatus().SteeringPending }

	steeringPhase(t, out, "queued")
	steeringPhase(t, out, "queued")
	if n := got(); n != 2 {
		t.Fatalf("after two queued: count = %d, want 2", n)
	}

	steeringPhase(t, out, "delivered")
	if n := got(); n != 0 {
		t.Fatalf("after delivered: count = %d, want 0", n)
	}

	// delivered ahead of its queued: the queued it belongs to must not count.
	steeringPhase(t, out, "delivered")
	steeringPhase(t, out, "queued")
	if n := got(); n != 0 {
		t.Fatalf("queued after a delivered-ahead: count = %d, want 0", n)
	}

	steeringPhase(t, out, "queued")
	steeringPhase(t, out, "refused")
	if n := got(); n != 1 {
		t.Fatalf("refused must not change the count: got %d, want 1", n)
	}

	steeringPhase(t, out, "dropped")
	if n := got(); n != 0 {
		t.Fatalf("after dropped: count = %d, want 0", n)
	}
}

// The count reaches the status bar as its own segment, only while it is
// non-zero.
func TestSteeringCountRendersInStatusBar(t *testing.T) {
	m := newTestTerminal()
	m.windowWidth = 200 // room for the segment without truncation

	if seg := m.updateStatus(); strings.Contains(seg.statusLeft, "steering") {
		t.Fatalf("idle statusLeft must not mention steering: %q", seg.statusLeft)
	}

	steeringPhase(t, m.out, "queued")
	steeringPhase(t, m.out, "queued")
	m = m.updateStatus()
	if !strings.Contains(m.statusLeft, "2 steering") {
		t.Fatalf("statusLeft = %q, want it to carry \"2 steering\"", m.statusLeft)
	}
	if bar := m.renderStatusBar(); !strings.Contains(bar, "2 steering") {
		t.Fatalf("status bar = %q, want it to show the steering count", bar)
	}
}
