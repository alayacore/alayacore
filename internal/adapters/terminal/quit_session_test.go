package terminal

// The TUI's own quit path, as of C2d: confirming :quit asks the session to
// end and waits for its terminal frame. Exiting on the confirmation instead
// would orphan the tool processes of a task still running (they are in their
// own session and receive no terminal signal) and skip the auto-save at the
// end of that task.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/commands"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/tlv"
)

// captureWriteCloser records what the Terminal writes to the session's input
// pipe, and whether that pipe was closed.
type captureWriteCloser struct {
	bytes.Buffer
	closed bool
}

func (c *captureWriteCloser) Close() error {
	c.closed = true
	return nil
}

// quitConfirmTerminal returns a Terminal with ":q" typed and the quit dialog
// open, wired to a capture writer for its input pipe.
func quitConfirmTerminal(t *testing.T) (Terminal, *captureWriteCloser) {
	t.Helper()
	cap := &captureWriteCloser{}
	m := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), cap, nil, 80, 24, theme.DefaultTheme(), nil, "theme-dark")
	m.input = m.input.WithValue(":q")
	m = m.focusInput()

	model, _ := m.Update(KeyPressMsg(Key{Code: KeyEnter}))
	m = model.(Terminal)
	if !m.confirmOverlay.IsOpen() {
		t.Fatal(":q should open the quit confirmation dialog")
	}
	return m, cap
}

// Confirming quit sends the session's quit command and leaves the input pipe
// open: the session still has to finish whatever it is doing before it ends.
func TestConfirmQuitSendsQuitCommand(t *testing.T) {
	m, cap := quitConfirmTerminal(t)

	model, cmd := m.Update(KeyPressMsg(Key{Code: 'y'}))
	m = model.(Terminal)

	if !m.quitting {
		t.Error("confirming quit should mark the terminal as quitting")
	}
	if cmd == nil {
		t.Fatal("confirming quit should emit the quit command")
	}
	if cap.closed {
		t.Error("the input pipe must stay open until the session ends")
	}

	if msg := cmd(); msg != nil {
		t.Errorf("the quit command should not produce a message, got %T", msg)
	}
	tag, value, err := tlv.ReadTLV(cap)
	if err != nil {
		t.Fatalf("read the emitted command: %v", err)
	}
	if tag != tlv.TagCommandIn {
		t.Fatalf("tag = %s, want CI", tag)
	}
	var sent protocol.CmdMsg
	if err := json.Unmarshal([]byte(value), &sent); err != nil {
		t.Fatalf("CI payload is not CmdMsg JSON: %v", err)
	}
	if sent.Name != commands.CommandNameQuit || sent.Input != "" {
		t.Errorf("sent %+v, want name %q with no arguments", sent, commands.CommandNameQuit)
	}
}

// The session's terminal frame is what ends the TUI: the tick handler flushes
// the last task's output, releases the input pipe, and returns Quit.
func TestSessionClosedFrameEndsTheProgram(t *testing.T) {
	m := newTestTerminal()
	cap := &captureWriteCloser{}
	m.streamInput = cap

	// Nothing has been announced yet: the tick keeps the program alive.
	if _, cmd := m.handleTick(); cmd == nil {
		t.Fatal("a tick with no terminal frame must schedule the next tick")
	}

	// Now a streaming delta is still pending (a later tick would flush it, but
	// the session ends first). The exit must not lose it: the terminal frame is
	// itself an authoritative frame, so the outputWriter flushed the delta when
	// the frame arrived — this pins that the session's last words are in the
	// WindowBuffer by the time the program leaves.
	if _, err := m.out.Write(encodeTestTLV(tlv.TagAssistantTDelta, tlv.WrapID("1", "bye"))); err != nil {
		t.Fatalf("write delta frame: %v", err)
	}
	m.out.Write(encodeTestTLV(tlv.TagSystemMsg, `{"type":"session","data":{"state":"closed"}}`)) //nolint:errcheck // test frame

	after, cmd := m.handleTick()
	if cmd == nil {
		t.Fatal("the session's terminal frame should end the program")
	}
	if _, ok := cmd().(QuitMsg); !ok {
		t.Errorf("the tick should return Quit, got %T", cmd())
	}
	if !cap.closed {
		t.Error("the input pipe should be closed once the session is over")
	}
	if wb := after.out.WindowBuffer(); wb.WindowCount() == 0 {
		t.Error("the pending delta should have been flushed into a window")
	}
}

// While the session finishes the task that :quit asked about, the terminal
// shows a wait window instead of leaving silently — and 'c' cancels that task
// so the exit can happen now.
func TestQuitWaitingOverlayOffersCancel(t *testing.T) {
	m := newTestTerminal()
	m = m.updateComponentSizes(80, 24) // as the constructor does; overlays wrap at Width
	cap := &captureWriteCloser{}
	m.streamInput = cap
	m.quitting = true

	// A task in progress, as the session reports it.
	m.out.Write(encodeTestTLV(tlv.TagSystemMsg,
		`{"type":"task","data":{"in_progress":true,"current_step":3,"max_steps":10,"context":100}}`)) //nolint:errcheck // test frame

	after, cmd := m.handleTick()
	m = after
	if cmd == nil {
		t.Fatal("the tick must keep the program alive while the session runs")
	}
	if !m.confirmOverlay.IsOpen() || m.confirmOverlay.Kind() != ConfirmQuitWaiting {
		t.Fatalf("a pending quit with a task running should show the wait window: open=%v kind=%v",
			m.confirmOverlay.IsOpen(), m.confirmOverlay.Kind())
	}
	body := stripANSI(m.confirmOverlay.View().Content)
	for _, want := range []string{"Step 3/10", "Press c to cancel the task and exit now."} {
		if !strings.Contains(body, want) {
			t.Errorf("wait window is missing %q:\n%s", want, body)
		}
	}

	// 'c' cancels the task and the session ends on its own from there.
	model, cmd := m.Update(KeyPressMsg{Code: 'c'})
	m = model.(Terminal)
	if cmd == nil {
		t.Fatal("'c' should emit the cancel command")
	}
	cmd()
	tag, value, err := tlv.ReadTLV(cap)
	if err != nil {
		t.Fatalf("read the emitted command: %v", err)
	}
	if tag != tlv.TagCommandIn {
		t.Fatalf("tag = %s, want CI", tag)
	}
	var sent protocol.CmdMsg
	if err := json.Unmarshal([]byte(value), &sent); err != nil {
		t.Fatalf("CI payload is not CmdMsg JSON: %v", err)
	}
	if sent.Name != commands.CommandNameCancel {
		t.Errorf("sent %q, want %q", sent.Name, commands.CommandNameCancel)
	}
	if !m.quitting {
		t.Error("canceling the task must not un-quit: the quit is already sent")
	}
}

// An idle session has nothing to wait for, so the window never appears: the
// session's terminal frame arrives at once and the program leaves.
func TestQuitWaitingOverlayStaysHiddenWhenIdle(t *testing.T) {
	m := newTestTerminal()
	m.streamInput = &captureWriteCloser{}
	m.quitting = true

	if _, cmd := m.handleTick(); cmd == nil {
		t.Fatal("no terminal frame yet: the tick must keep the program alive")
	}
	if m.confirmOverlay.IsOpen() {
		t.Errorf("an idle session needs no wait window, got kind %v", m.confirmOverlay.Kind())
	}
}
