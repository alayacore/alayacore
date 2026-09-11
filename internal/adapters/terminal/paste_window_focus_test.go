package terminal

// The terminal window's OS-level focus and the paste path.
//
// Focus reporting (DEC mode 1004, enabled by Screen.Start) tells the program
// that its window is not the one the operating system has focused. That is a
// fact about drawing. It was also a fact about input, because the blur blurred
// the prompt's box and a box without focus ignores every message it is sent —
// so a paste arriving during a blur was deleted, silently.
//
// The arriving case is the terminal's own context menu: every host that has one
// offers "Paste" there, the menu takes the window's focus while it is open, and
// the clipboard is written to the pty before that focus comes back. Middle-click
// never loses the focus, so the same paste worked by mouse and not by menu — and
// worked by menu in the attachment window's URL box, whose filter never gated
// input on the window either. The rule the fix settles on: window focus moves
// the caret and the colors, and nothing else; which box takes text is decided by
// the pane focus the user chose.

import "testing"

// feed runs one message through the real Terminal.Update and keeps what comes
// back, so these tests go through the same dispatch the event loop does.
func feed(tb *testing.T, m *Terminal, msg Msg) {
	tb.Helper()
	next, _ := m.Update(msg)
	tm, ok := next.(Terminal)
	if !ok {
		tb.Fatalf("Update(%T) returned %T, want a Terminal", msg, next)
	}
	*m = tm
}

// TestPasteLandsWhileTheWindowIsUnfocused is the reported bug.
func TestPasteLandsWhileTheWindowIsUnfocused(t *testing.T) {
	m := newTestTerminal()
	feed(t, &m, BlurMsg{})
	if m.hasFocus {
		t.Fatal("the fixture's BlurMsg did not register as a focus loss")
	}

	feed(t, &m, PasteMsg{Content: "pasted from the menu"})
	if got := m.input.Value(); got != "pasted from the menu" {
		t.Errorf("prompt after a paste during the blur = %q, want the pasted text", got)
	}

	// The same answer for a keystroke: the pty is this program's, so anything
	// that arrives on it was addressed to it. Gating that on window focus is
	// what deleted the paste.
	feed(t, &m, KeyPressMsg{Code: '!'})
	if got, want := m.input.Value(), "pasted from the menu!"; got != want {
		t.Errorf("prompt after a key during the blur = %q, want %q", got, want)
	}

	// And the focus that comes back when the menu closes must not repeat it.
	feed(t, &m, FocusMsg{})
	if got, want := m.input.Value(), "pasted from the menu!"; got != want {
		t.Errorf("prompt after the focus returned = %q, want it unchanged at %q", got, want)
	}
}

// TestOverlayFilterAlsoTakesThePasteWhileUnfocused pins the half of the report
// that made the prompt's behavior the odd one out: the box the same menu paste
// reached fine. Both boxes must agree, so both are asserted here.
func TestOverlayFilterAlsoTakesThePasteWhileUnfocused(t *testing.T) {
	m := newTestTerminal()
	feed(t, &m, KeyPressMsg{Code: 'a', Mod: ModCtrl}) // Ctrl+A: the attachment picker
	if !m.attachmentWindow.IsOpen() {
		t.Fatal("Ctrl+A did not open the attachment window")
	}
	feed(t, &m, KeyPressMsg{Code: 'a', Mod: ModCtrl}) // and Ctrl+A again: URL mode
	if m.attachmentWindow.mode != modeURL {
		t.Fatalf("mode = %v, want %v", m.attachmentWindow.mode, modeURL)
	}

	feed(t, &m, BlurMsg{})
	const url = "https://example.org/a.png"
	feed(t, &m, PasteMsg{Content: url})
	if got := m.attachmentWindow.FilterInput.Value(); got != url {
		t.Errorf("URL box after a paste during the blur = %q, want %q", got, url)
	}
	if m.input.Value() != "" {
		t.Errorf("the prompt took text meant for the open overlay: %q", m.input.Value())
	}
}

// TestPaneFocusStillDecidesWhereAPasteGoes: the window's focus no longer gates
// the input path, but the user's does. With the display pane in charge there is
// no prompt to paste into, and the text must not appear there.
func TestPaneFocusStillDecidesWhereAPasteGoes(t *testing.T) {
	m := newTestTerminal()
	m = m.focusDisplay()

	feed(t, &m, PasteMsg{Content: "not for the prompt"})
	if got := m.input.Value(); got != "" {
		t.Errorf("a paste landed in the prompt while the display had focus: %q", got)
	}

	// Back on the prompt, the same message does land — the gate is the pane,
	// not the window.
	m = m.focusInput()
	feed(t, &m, PasteMsg{Content: "for the prompt"})
	if got := m.input.Value(); got != "for the prompt" {
		t.Errorf("prompt = %q, want %q", got, "for the prompt")
	}
}

// TestWindowBlurKeepsTheVisualCue: giving up the input gate must not also give
// up the cue. An unfocused window draws no real caret (IME anchors on it) and
// draws its prompt in the blurred register.
func TestWindowBlurKeepsTheVisualCue(t *testing.T) {
	m := newTestTerminal()
	if m.View().Cursor == nil {
		t.Fatal("the fixture starts with no caret to lose")
	}
	focused := m.input.View().Content

	m = m.focusInput()
	m.input = m.input.WithWindowFocus(false)
	if m.View().Cursor != nil {
		t.Error("the real caret stayed on the screen while the window was unfocused")
	}
	if m.input.View().Content == focused {
		t.Error("the prompt rendered identically focused and unfocused; the blur is invisible")
	}

	// The focus returning puts both back, without help from anything else.
	feed(t, &m, FocusMsg{})
	if m.View().Cursor == nil {
		t.Error("the caret did not come back with the window focus")
	}
	if got := m.input.View().Content; got != focused {
		t.Error("the prompt did not come back to its focused register with the window focus")
	}
}
