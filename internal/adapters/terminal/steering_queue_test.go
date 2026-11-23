package terminal

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/wire"
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
	if err := wire.WriteFrame(out, wire.TagSystemMsg, `{"type":"steering","data":`+string(data)+`}`); err != nil {
		t.Fatalf("write steering %q: %v", phase, err)
	}
}

// A "steering" SM keeps one bit — whether words are waiting — and that is the
// whole of what the UI asks it. "queued" sets it, "delivered"/"dropped" clear it
// (the session takes the batch whole, so it is all-or-nothing), and "refused"
// never entered, so it changes nothing.
//
// The two session writers can invert a batch's frames: a "delivered" written
// before the "queued" it belongs to. With one bit that inversion lands on the
// safe side — the flag stays set, so a cancel offers to keep words that already
// went, which costs one wasted dialog. The other direction would be a stale
// "nothing waiting", which silently drops the words, and it cannot arise: only
// "queued" sets the flag, and only the batch it announced can clear it.
func TestSteeringSMKeepsWhetherWordsAreWaiting(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	waiting := func() bool { return out.SnapshotStatus().SteeringQueued }

	if waiting() {
		t.Fatal("a fresh session reports words as waiting")
	}
	steeringPhase(t, out, "queued", "")
	if !waiting() {
		t.Fatal("queued did not mark words as waiting")
	}
	steeringPhase(t, out, "delivered", "")
	if waiting() {
		t.Fatal("delivered left words waiting")
	}
	steeringPhase(t, out, "queued", "")
	steeringPhase(t, out, "refused", "")
	if !waiting() {
		t.Fatal("refused cleared words the queue still holds")
	}
	steeringPhase(t, out, "dropped", "")
	if waiting() {
		t.Fatal("dropped left words waiting")
	}
	steeringPhase(t, out, "delivered", "")
	steeringPhase(t, out, "queued", "")
	if !waiting() {
		t.Fatal("a delivered that overtook its queued must leave the flag set")
	}
}

// The queue is announced as a state, never as a number, and only while it is
// true. The words are the sender's whole question — "did my prompt get taken?" —
// so the row says "steering queued" and drops it the moment the batch is settled;
// how many prompts are waiting is not a fact a reader can act on, and a count was
// what it looked like. The status bar carries neither: it describes the run.
func TestSteeringIsAFactOnTheRowAndNowhereElse(t *testing.T) {
	m := liveEdgeFixture(20)
	m.screenWidth = 200

	steeringPhase(t, m.out, "queued", "")
	if got := stripANSI(m.renderLiveEdge()); got != "- following, steering queued -" {
		t.Errorf("live edge = %q, want the waiting fact beside the position", got)
	}

	steeringPhase(t, m.out, "queued", "") // a second submission: still one fact
	if got := stripANSI(m.renderLiveEdge()); got != "- following, steering queued -" {
		t.Errorf("live edge after a second submission = %q, want the same one fact", got)
	}

	steeringPhase(t, m.out, "delivered", "")
	if got := stripANSI(m.renderLiveEdge()); strings.Contains(got, "steering") {
		t.Errorf("live edge after delivery = %q, want the position only", got)
	}

	m = m.updateStatus()
	if strings.Contains(m.statusLeft, "steering") {
		t.Errorf("statusLeft = %q, want no steering segment", m.statusLeft)
	}
}

// Which phases reach the transcript is not the same set for every phase. A phase
// whose text only repeats that words are waiting ("steering queued — …") says
// nothing the dialog does not, so it is not written; a phase whose text says
// something no state can — that the words are gone, or that this one was never
// taken — gets a line, in the register its meaning belongs to.
func TestSteeringPhasesReachTheTranscriptOnlyWhenTheTextSaysSomethingNew(t *testing.T) {
	m := liveEdgeFixture(20)
	windows := func() []*Window { return m.out.WindowBuffer().AllWindows() }

	queuedText := "steering queued — delivered into the running task, or as your next prompt"
	steeringPhase(t, m.out, "queued", queuedText)
	if n := len(windows()); n != 20 {
		t.Errorf("a queued frame put %d window(s) in the transcript, want 0 more than the 20 in the fixture", n)
	}

	// Refused: the submission never entered the queue, and the line — the only
	// thing that says so — reaches the transcript in the error register.
	refusedText := "Too many prompts are already queued for the running task; this one was not queued."
	steeringPhase(t, m.out, "refused", refusedText)
	if n := len(windows()); n != 21 {
		t.Fatalf("a refused frame put %d window(s) in the transcript, want 1 more", n)
	}
	if last := windows()[20]; last.RawTag() != TagWindowSE || !strings.Contains(last.RawContent(), refusedText) {
		t.Errorf("refused notice = (%s, %q), want an SE window carrying the text", last.RawTag(), last.RawContent())
	}

	// Dropped: the words are gone, which no state can say, so the line goes to
	// the transcript in the notify register.
	droppedText := "steering dropped — the task ended before it could be delivered"
	steeringPhase(t, m.out, "dropped", droppedText)
	if n := len(windows()); n != 22 {
		t.Fatalf("a dropped frame put %d window(s) in the transcript, want 1 more", n)
	}
	if last := windows()[21]; last.RawTag() != TagWindowSN || !strings.Contains(last.RawContent(), droppedText) {
		t.Errorf("drop notice = (%s, %q), want an SN window carrying the text", last.RawTag(), last.RawContent())
	}
}
