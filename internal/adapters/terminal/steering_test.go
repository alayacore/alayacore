package terminal

// A prompt submitted while a task is running is sent to the session, which
// splices it into the running task (steering). The TUI used to refuse it here
// and keep the text in the input box — which, with steering in the core, would
// mean the core's queue could never be reached from the TUI at all.

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/commands"
)

type bufferWriteCloser struct{ *bytes.Buffer }

func (bufferWriteCloser) Close() error { return nil }

var _ io.WriteCloser = bufferWriteCloser{}

// A cancel with words still queued asks twice: first the cancel (the dialog that
// has always been there), then what happens to the words. Nothing goes on the
// wire until the second answer, which is what makes "cancel" reversible right up
// to the moment it is not — and the three answers mean three things.
func TestCancelWithQueuedSteeringAsksTwice(t *testing.T) {
	cases := []struct {
		name string
		key  Key
		want string // the frame the answer must produce; "" = nothing sent
	}{
		{"y drops the words", Key{Code: 'y'}, `"name":"cancel"}`},
		{"n keeps them for the next prompt", Key{Code: 'n'}, `"name":"cancel","args":"` + commands.CancelArgKeepSteering + `"`},
		{"esc withdraws the cancel", Key{Code: KeyEscape}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestTerminal()
			var sent bytes.Buffer
			m.sessionPipe = bufferWriteCloser{&sent}
			m.inProgress = true
			steeringPhase(t, m.out, "queued", "")
			steeringPhase(t, m.out, "queued", "")

			// Ctrl+G: the first question, unchanged.
			model, cmd := m.Update(KeyPressMsg(Key{Code: 'g', Mod: ModCtrl}))
			m = model.(Terminal)
			if cmd != nil {
				t.Fatalf("Ctrl+G produced %T before the question was answered", cmd)
			}
			if got := m.confirmOverlay.Kind(); got != ConfirmCancel {
				t.Fatalf("first step kind = %v, want ConfirmCancel", got)
			}

			// y: the cancel is confirmed, so the queue is what is left to decide.
			model, cmd = m.Update(KeyPressMsg(Key{Code: 'y'}))
			m = model.(Terminal)
			if cmd != nil {
				t.Fatal("the first answer sent the cancel before the queue was decided")
			}
			if got := m.confirmOverlay.Kind(); got != ConfirmCancelSteering {
				t.Fatalf("second step kind = %v, want ConfirmCancelSteering — queued steering makes a second question", got)
			}
			if sent.Len() != 0 {
				t.Fatalf("a frame went out before the second question was answered: %q", sent.String())
			}

			model, cmd = m.Update(KeyPressMsg(tc.key))
			m = model.(Terminal)

			if tc.want == "" {
				if cmd != nil {
					t.Fatal("abandoning the cancel produced a command")
				}
				if m.confirmOverlay.IsOpen() {
					t.Error("Esc left the dialog open")
				}
				if sent.Len() != 0 {
					t.Fatalf("abandoning the cancel sent %q, want nothing", sent.String())
				}
				return
			}
			if cmd == nil {
				t.Fatal("the second answer produced no command")
			}
			runSubmit(t, cmd)
			if got := sent.String(); !strings.Contains(got, tc.want) {
				t.Fatalf("sent %q, want it to carry %s", got, tc.want)
			}
			// Both answers send one frame and nothing follows it: the question
			// is over, and a second frame would be a cancel the user did not ask
			// for.
			if n := strings.Count(sent.String(), `"name":"cancel"`); n != 1 {
				t.Fatalf("%d cancel frames sent, want exactly 1: %q", n, sent.String())
			}
		})
	}
}

// Without queued steering the cancel stays one question: the second step only
// exists when there is something to lose, so the common case costs no keystroke.
func TestCancelWithoutQueuedSteeringAsksOnce(t *testing.T) {
	m := newTestTerminal()
	var sent bytes.Buffer
	m.sessionPipe = bufferWriteCloser{&sent}
	m.inProgress = true

	model, _ := m.Update(KeyPressMsg(Key{Code: 'g', Mod: ModCtrl}))
	m = model.(Terminal)
	model, cmd := m.Update(KeyPressMsg(Key{Code: 'y'}))
	m = model.(Terminal)

	if cmd == nil {
		t.Fatal("a cancel with nothing queued must send the cancel at once")
	}
	runSubmit(t, cmd)
	if got := sent.String(); !strings.Contains(got, `"name":"cancel"`) || strings.Contains(got, "keep_steering") {
		t.Fatalf("sent %q, want a bare :cancel", got)
	}
	if m.confirmOverlay.IsOpen() {
		t.Error("the dialog should be closed")
	}
}

// The second step's text names both outcomes and repeats the count the live edge
// is showing — "y / n" alone would make the reader guess which answer drops
// their words.
func TestCancelSteeringDialogNamesBothOutcomes(t *testing.T) {
	cd := NewConfirmDialog(DefaultStyles()).WithSize(60, 24).OpenCancelSteering(2)
	got := strings.Join(confirmDialogRows(t, cd), "\n")
	for _, want := range []string{
		"Discard the queued steering?",
		"2 steering messages are queued.",
		"y drops them, n runs them as the next prompt",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the second step is missing %q:\n%s", want, got)
		}
	}

	single := strings.Join(confirmDialogRows(t, NewConfirmDialog(DefaultStyles()).WithSize(60, 24).OpenCancelSteering(1)), "\n")
	if !strings.Contains(single, "1 steering message is queued.") {
		t.Errorf("one queued message should read as a singular:\n%s", single)
	}
}

// runSubmit runs the submit command out of a submit batch — Batch(submit, tick) —
// without waiting out the tick, which only schedules a screen refresh.
func runSubmit(t *testing.T, cmd Cmd) {
	t.Helper()
	msg := cmd()
	batch, ok := msg.(BatchMsg)
	if !ok {
		return // a single command already ran
	}
	if len(batch) == 0 || batch[0] == nil {
		t.Fatal("the submit batch carried no command")
	}
	batch[0]()
}

func TestSubmitDuringRunIsSentForSteering(t *testing.T) {
	m := newTestTerminal()
	var sent bytes.Buffer
	m.sessionPipe = bufferWriteCloser{&sent}
	m.promptBox = m.promptBox.WithValue("use Y instead")
	m.inProgress = true

	m2, cmd := m.handleSubmit()
	if cmd == nil {
		t.Fatal("handleSubmit returned no command for a prompt submitted during a run")
	}

	batch, ok := cmd().(BatchMsg)
	if !ok {
		t.Fatalf("handleSubmit during a run returned %T, want the submit batch (not a refusal)", cmd())
	}
	if len(batch) == 0 {
		t.Fatal("handleSubmit returned an empty batch")
	}
	// batch[0] is the submit command; the rest schedule the tick. Running the
	// submit command is what writes the frames, and it must not report a
	// refusal back to the UI.
	if msg := batch[0](); msg != nil {
		t.Fatalf("submitting during a run produced %T, want no message", msg)
	}

	if !strings.Contains(sent.String(), "use Y instead") {
		t.Fatalf("the prompt never reached the session: %q", sent.String())
	}
	if m2.promptBox.Value() != "" {
		t.Fatalf("the input box still holds %q after a submit", m2.promptBox.Value())
	}
}
