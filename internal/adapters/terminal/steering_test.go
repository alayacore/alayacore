package terminal

// A prompt submitted while a task is running is sent to the session, which
// splices it into the running turn (steering). The TUI used to refuse it here
// and keep the text in the input box — which, with steering in the core, would
// mean the core's queue could never be reached from the TUI at all.

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

type bufferWriteCloser struct{ *bytes.Buffer }

func (bufferWriteCloser) Close() error { return nil }

var _ io.WriteCloser = bufferWriteCloser{}

func TestSubmitDuringRunIsSentForSteering(t *testing.T) {
	m := newTestTerminal()
	var sent bytes.Buffer
	m.streamInput = bufferWriteCloser{&sent}
	m.input = m.input.WithValue("use Y instead")
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
	if m2.input.Value() != "" {
		t.Fatalf("the input box still holds %q after a submit", m2.input.Value())
	}
}
