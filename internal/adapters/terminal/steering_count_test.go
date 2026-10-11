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

// The count is shown on the live-edge row, above the input box, only while it
// is non-zero, and it rides alongside the position fact rather than replacing
// it.
func TestSteeringCountRendersOnLiveEdge(t *testing.T) {
	m := liveEdgeFixture(20)
	if got := stripANSI(m.renderLiveEdge()); got != "- following -" {
		t.Fatalf("idle live edge = %q, want the bare position label", got)
	}

	steeringPhase(t, m.out, "queued")
	steeringPhase(t, m.out, "queued")
	if got := stripANSI(m.renderLiveEdge()); got != "- following, 2 steering -" {
		t.Fatalf("live edge = %q, want the count joined to the position", got)
	}

	// The words are still waiting, so the count survives scrolling — the
	// position fact is the one that changes with it.
	m.display = m.display.MarkUserScrolled().ScrollUp(5)
	m = m.updateDisplayHeight()
	if got := stripANSI(m.renderLiveEdge()); got != "- 5 lines below, 2 steering -" {
		t.Fatalf("scrolled live edge = %q, want both facts", got)
	}

	steeringPhase(t, m.out, "delivered")
	if got := stripANSI(m.renderLiveEdge()); got != "- 5 lines below -" {
		t.Fatalf("after delivery: live edge = %q, want the count gone", got)
	}
}

// The count is read at render time, not folded into the status bar's
// version-checked rebuild: the frame that carries it is the frame after the SM
// arrived, whether or not updateStatus has run again.
func TestSteeringCountReadsStateAtRenderTime(t *testing.T) {
	m := liveEdgeFixture(20)
	steeringPhase(t, m.out, "queued")
	if got := stripANSI(m.renderLiveEdge()); !strings.Contains(got, "1 steering") {
		t.Fatalf("live edge = %q, want the count straight after the SM", got)
	}
}

// The status bar does not carry the count. The row below the input box is not
// where a reader watching their own words looks (live_edge.go).
func TestSteeringCountIsNotInTheStatusBar(t *testing.T) {
	m := newTestTerminal()
	m.windowWidth = 200 // ample room, so an absent segment cannot be a truncation

	steeringPhase(t, m.out, "queued")
	steeringPhase(t, m.out, "queued")
	m = m.updateStatus()
	if strings.Contains(m.statusLeft, "steering") {
		t.Errorf("statusLeft = %q, want no steering segment", m.statusLeft)
	}
	if bar := stripANSI(m.renderStatusBar()); strings.Contains(bar, "steering") {
		t.Errorf("status bar = %q, want no steering segment", bar)
	}
}
