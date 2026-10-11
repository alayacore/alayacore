package terminal

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/tlv"
)

// steeringPhase feeds one "steering" SM frame the way the session writes it:
// phase, and the human line the phase carries (empty for the phases that carry
// none).
func steeringPhase(t *testing.T, out interface{ Write([]byte) (int, error) }, phase, text string) {
	t.Helper()
	data, err := json.Marshal(struct {
		Phase string `json:"phase"`
		Text  string `json:"text,omitempty"`
	}{Phase: phase, Text: text})
	if err != nil {
		t.Fatalf("marshal steering %q: %v", phase, err)
	}
	if err := tlv.WriteTLV(out, tlv.TagSystemMsg, `{"type":"steering","data":`+string(data)+`}`); err != nil {
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

	steeringPhase(t, out, "queued", "")
	steeringPhase(t, out, "queued", "")
	if n := got(); n != 2 {
		t.Fatalf("after two queued: count = %d, want 2", n)
	}

	steeringPhase(t, out, "delivered", "")
	if n := got(); n != 0 {
		t.Fatalf("after delivered: count = %d, want 0", n)
	}

	// delivered ahead of its queued: the queued it belongs to must not count.
	steeringPhase(t, out, "delivered", "")
	steeringPhase(t, out, "queued", "")
	if n := got(); n != 0 {
		t.Fatalf("queued after a delivered-ahead: count = %d, want 0", n)
	}

	steeringPhase(t, out, "queued", "")
	steeringPhase(t, out, "refused", "")
	if n := got(); n != 1 {
		t.Fatalf("refused must not change the count: got %d, want 1", n)
	}

	steeringPhase(t, out, "dropped", "")
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

	steeringPhase(t, m.out, "queued", "")
	steeringPhase(t, m.out, "queued", "")
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

	steeringPhase(t, m.out, "delivered", "")
	if got := stripANSI(m.renderLiveEdge()); got != "- 5 lines below -" {
		t.Fatalf("after delivery: live edge = %q, want the count gone", got)
	}
}

// The count is read at render time, not folded into the status bar's
// version-checked rebuild: the frame that carries it is the frame after the SM
// arrived, whether or not updateStatus has run again.
func TestSteeringCountReadsStateAtRenderTime(t *testing.T) {
	m := liveEdgeFixture(20)
	steeringPhase(t, m.out, "queued", "")
	if got := stripANSI(m.renderLiveEdge()); !strings.Contains(got, "1 steering") {
		t.Fatalf("live edge = %q, want the count straight after the SM", got)
	}
}

// The status bar does not carry the count. The row below the input box is not
// where a reader watching their own words looks (live_edge.go).
func TestSteeringCountIsNotInTheStatusBar(t *testing.T) {
	m := newTestTerminal()
	m.windowWidth = 200 // ample room, so an absent segment cannot be a truncation

	steeringPhase(t, m.out, "queued", "")
	steeringPhase(t, m.out, "queued", "")
	m = m.updateStatus()
	if strings.Contains(m.statusLeft, "steering") {
		t.Errorf("statusLeft = %q, want no steering segment", m.statusLeft)
	}
	if bar := stripANSI(m.renderStatusBar()); strings.Contains(bar, "steering") {
		t.Errorf("status bar = %q, want no steering segment", bar)
	}
}

// Which phases reach the transcript is not the same set for every phase. The
// count is state and the row carries it; a phase whose text only repeats that
// state ("steering queued — …") is announced by the count alone and its text is
// dropped, while a phase whose text says something the count cannot — that the
// words are gone, or that this one was never taken — gets a line. Both surfaces
// carry their own half and no more.
func TestSteeringPhasesReachTheTranscriptOnlyWhenTheTextSaysSomethingNew(t *testing.T) {
	m := liveEdgeFixture(20)
	windows := func() []*Window { return m.out.WindowBuffer().AllWindows() }

	queuedText := "steering queued — delivered into the running turn, or as your next prompt"
	steeringPhase(t, m.out, "queued", queuedText)
	if n := len(windows()); n != 20 {
		t.Errorf("a queued frame put %d window(s) in the transcript, want 0 more than the 20 in the fixture", n)
	}
	if got := stripANSI(m.renderLiveEdge()); !strings.Contains(got, "1 steering") {
		t.Errorf("live edge = %q, want the state it announced", got)
	}

	// Refused: the submission never entered the queue, so the count is
	// untouched, and the line — the only thing that says so — reaches the
	// transcript in the error register.
	refusedText := "Too many prompts are already queued for the running task; this one was not queued."
	steeringPhase(t, m.out, "refused", refusedText)
	if n := len(windows()); n != 21 {
		t.Fatalf("a refused frame put %d window(s) in the transcript, want 1 more", n)
	}
	if last := windows()[20]; last.RawTag() != TagWindowSE || !strings.Contains(last.RawContent(), refusedText) {
		t.Errorf("refused notice = (%s, %q), want an SE window carrying the text", last.RawTag(), last.RawContent())
	}
	if got := stripANSI(m.renderLiveEdge()); !strings.Contains(got, "1 steering") {
		t.Errorf("live edge after a refusal = %q, want the count unchanged", got)
	}

	// Dropped: the words are gone, which no count can say, so the line goes to
	// the transcript in the notify register and the count goes to zero.
	droppedText := "steering dropped — the turn ended before it could be delivered"
	steeringPhase(t, m.out, "dropped", droppedText)
	if n := len(windows()); n != 22 {
		t.Fatalf("a dropped frame put %d window(s) in the transcript, want 1 more", n)
	}
	if last := windows()[21]; last.RawTag() != TagWindowSN || !strings.Contains(last.RawContent(), droppedText) {
		t.Errorf("drop notice = (%s, %q), want an SN window carrying the text", last.RawTag(), last.RawContent())
	}
	if got := stripANSI(m.renderLiveEdge()); strings.Contains(got, "steering") {
		t.Errorf("live edge after a drop = %q, want the count gone", got)
	}
}
