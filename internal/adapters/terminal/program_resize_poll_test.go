package terminal

// Tests for Program.refreshSize — the size re-read that runs on the model's
// tick (see program.go → run, case tickMsg).
//
// This is the only resize source on Windows: there is no SIGWINCH, and a
// pseudo console (Windows Terminal, and the ConPTY-backed conhost on Windows
// 11) delivers no resize notification either, so a dragged window would
// otherwise keep the width measured at startup for the rest of the session.
// The behavior under test is host-independent, so it runs on every platform —
// it does not need a Windows machine, and the CI matrix keeps it that way.

import (
	"testing"
)

// sizedTestProgram builds a Program whose screen reports a fixed size without
// a file descriptor to query: Screen.Size returns its cached pair whenever
// sizeFile is nil, which is exactly how these tests move the "terminal" size.
func sizedTestProgram(msgs chan Msg, screenW, screenH int) *Program {
	p, _ := newTestProgram(msgs)
	p.screen.width, p.screen.height = screenW, screenH
	return p
}

func TestRefreshSizeQueuesChangeAndTracksIt(t *testing.T) {
	msgs := make(chan Msg, 4)
	p := sizedTestProgram(msgs, 120, 40)
	p.width, p.height = 80, 24 // the size measured at startup

	p.refreshSize()

	select {
	case msg := <-msgs:
		ws, ok := msg.(WindowSizeMsg)
		if !ok {
			t.Fatalf("queued %T, want WindowSizeMsg", msg)
		}
		if ws.Width != 120 || ws.Height != 40 {
			t.Errorf("got %dx%d, want 120x40", ws.Width, ws.Height)
		}
	default:
		t.Fatal("a changed size queued nothing")
	}

	// The tracked size moves with the message, so the next tick is quiet:
	// one message per actual change, not one per tick.
	if p.width != 120 || p.height != 40 {
		t.Errorf("tracked size = %dx%d, want 120x40", p.width, p.height)
	}
	p.refreshSize()
	select {
	case msg := <-msgs:
		t.Errorf("second refreshSize queued %T with nothing changed", msg)
	default:
	}
}

func TestRefreshSizeIgnoresUnchangedSize(t *testing.T) {
	msgs := make(chan Msg, 4)
	p := sizedTestProgram(msgs, 80, 24)
	p.width, p.height = 80, 24

	p.refreshSize()

	select {
	case msg := <-msgs:
		t.Errorf("unchanged size queued %T, want nothing", msg)
	default:
	}
}

// TestRefreshSizeDoesNotTrackADroppedMessage is the retry property. The send is
// non-blocking because the loop that drains p.msgs is the caller, so a full
// buffer can drop the message — and if the tracked size had been updated
// anyway, the change would be swallowed: the next tick sees no difference, no
// WindowSizeMsg is ever sent, and Screen.Resize never clears the frame caches,
// leaving the layout on the old width for the rest of the session.
func TestRefreshSizeDoesNotTrackADroppedMessage(t *testing.T) {
	// Buffered to one, and that one already taken: the send cannot succeed.
	msgs := make(chan Msg, 1)
	msgs <- QuitMsg{}
	p := sizedTestProgram(msgs, 132, 43)
	p.width, p.height = 80, 24

	p.refreshSize()

	if p.width != 80 || p.height != 24 {
		t.Fatalf("a dropped message still moved the tracked size to %dx%d", p.width, p.height)
	}

	// Drain the queue: the next tick retries, because the tracked size is
	// still the old one.
	<-msgs
	p.refreshSize()

	msg := <-msgs
	ws, ok := msg.(WindowSizeMsg)
	if !ok {
		t.Fatalf("retry produced %T, want WindowSizeMsg", msg)
	}
	if ws.Width != 132 || ws.Height != 43 {
		t.Errorf("retry produced %dx%d, want 132x43", ws.Width, ws.Height)
	}
	if p.width != 132 || p.height != 43 {
		t.Errorf("tracked size after a delivered message = %dx%d, want 132x43", p.width, p.height)
	}
}

// resizeQuitModel quits the loop from the WindowSizeMsg itself, which is the
// only way to end the loop after a message the tick queued: anything the test
// put in the channel beforehand is drained first, so a QuitMsg sent up front
// would be handled before the resize and the loop would exit with the resize
// still sitting in the buffer — which is exactly the ordering this test needs
// to prove.
type resizeQuitModel struct {
	fakeModel
}

func (m *resizeQuitModel) Update(msg Msg) (Model, Cmd) {
	m.fakeModel.Update(msg)
	if _, ok := msg.(WindowSizeMsg); ok {
		return m, func() Msg { return QuitMsg{} }
	}
	return m, nil
}

// TestTickDrivesSizeRefresh runs the real loop: a tickMsg must be enough to
// deliver a WindowSizeMsg to the model, which is the whole point — nothing on
// Windows sends that message any other way.
func TestTickDrivesSizeRefresh(t *testing.T) {
	msgs := make(chan Msg, 8)
	p := sizedTestProgram(msgs, 100, 30)
	p.width, p.height = 80, 24
	m := &resizeQuitModel{}

	// One message only: the loop is what queues the resize, and the model
	// quits from inside it.
	msgs <- tickMsg{}

	if _, err := p.run(m); err != nil {
		t.Fatalf("run: %v", err)
	}

	var seen []WindowSizeMsg
	for _, msg := range m.updates {
		if ws, ok := msg.(WindowSizeMsg); ok {
			seen = append(seen, ws)
		}
	}
	if len(seen) != 1 {
		t.Fatalf("model saw %d WindowSizeMsg (%v), want exactly 1", len(seen), m.updates)
	}
	if seen[0].Width != 100 || seen[0].Height != 30 {
		t.Errorf("model saw %dx%d, want 100x30", seen[0].Width, seen[0].Height)
	}
	if p.width != 100 || p.height != 30 {
		t.Errorf("program tracked %dx%d, want 100x30", p.width, p.height)
	}
	// Screen.Resize is what clears the stale frame caches; if the loop's own
	// case did not run, the next frame could be painted over the old width.
	if p.screen.width != 100 || p.screen.height != 30 {
		t.Errorf("screen holds %dx%d, want 100x30", p.screen.width, p.screen.height)
	}
}
