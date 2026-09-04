package terminal

// Tests for the prompt's Ctrl+J line-break action (keybinds.go →
// handleInputKeys) and for the primitive behind it (input_field.go →
// insertNewline).
//
// Ctrl+J is the byte-accurate name for LF (0x0a): key_parser.go → decodeC0
// maps 0x0a to Key{'j', ModCtrl}, and this action claims that key. The binding
// exists for a host-independent reason. A terminal that implements bracketed
// paste delivers pasted text inside ESC[200~/ESC[201~ markers, so it arrives as
// PasteMsg; a terminal that does not (legacy Windows conhost, TERM=dumb, some
// tmux and xterm configurations) delivers the same bytes as ordinary
// keystrokes. LF is the line ending of pasted text on every non-Windows host,
// so routing it to "insert a line break" makes bulk-pasted multi-line text land
// in the input as content instead of firing one submit per line.
//
// Enter keeps submitting: the Enter key produces CR, never LF, so the two
// actions cannot be confused with each other.

import (
	"testing"
)

// keyCtrlJMsg builds the KeyMsg a terminal delivers for Ctrl+J (LF).
func keyCtrlJMsg() KeyMsg { return KeyPressMsg(Key{Code: 'j', Mod: ModCtrl}) }

// TestInputFieldCtrlJNotHandledGenerically pins the scope of the Ctrl+J
// binding: it lives in the prompt's key routing, not in InputField's generic
// key path.
//
// This matters because the overlay filter boxes (model selector, theme
// selector, help window, attachment window) embed their own InputField and read
// Enter as "accept the current match" — see filtered_list.go. If the line break
// were inserted by InputField.handleKeyMsg, pasting multi-line text into a
// filter box would put a newline into the filter string, where it can never
// match an item, instead of leaving the filter alone.
func TestInputFieldCtrlJNotHandledGenerically(t *testing.T) {
	f := NewInputField().Focus().WithWidth(20)
	f = f.WithValue("ab").CursorEnd()

	after, _ := f.Update(keyCtrlJMsg())
	if after.Value() != "ab" {
		t.Errorf("value = %q, want %q: Ctrl+J must not be handled generically", after.Value(), "ab")
	}
	if after.CursorPos() != 2 {
		t.Errorf("cursor = %d, want 2", after.CursorPos())
	}
}

// TestInputFieldLoneNewlineIsDropped pins what the generic path does with the
// key: it is filtered out, not converted into a printable character. Before
// this change the same byte was silently swallowed by handleInsertion's
// printableRune check, which is why pasting multi-line text on a host without
// bracketed paste produced one long glued line.
func TestInputFieldLoneNewlineIsDropped(t *testing.T) {
	f := NewInputField().Focus().WithWidth(20)

	after, _ := f.Update(keyCtrlJMsg())
	if after.Value() != "" {
		t.Errorf("value = %q, want empty (the generic path must not insert printable text for a control key)", after.Value())
	}
}

// TestPromptCtrlJInsertsNewlineWithoutSubmitting verifies the prompt action:
// a line break is inserted and no task is started.
func TestPromptCtrlJInsertsNewlineWithoutSubmitting(t *testing.T) {
	m := newTestTerminal()

	m, cmd := m.handleInputKeys(keyCtrlJMsg())
	if cmd != nil {
		t.Error("Ctrl+J returned a Cmd; it must not start a task — that is what Enter is for")
	}
	if got := m.input.Value(); got != "\n" {
		t.Errorf("value = %q, want %q", got, "\n")
	}

	// Text typed after the break continues on the new line.
	m, _ = m.handleInputKeys(KeyPressMsg(Key{Text: "b"}))
	if got := m.input.Value(); got != "\nb" {
		t.Errorf("value after typing = %q, want %q", got, "\nb")
	}
}

// TestPromptEnterStillSubmits guards the other half of the split: Enter (CR)
// still submits, so the new binding did not take over the submit key.
func TestPromptEnterStillSubmits(t *testing.T) {
	m := newTestTerminal()
	m.input = m.input.WithValue("hello")

	updated, cmd := m.handleInputKeys(KeyPressMsg(Key{Code: KeyEnter}))
	if cmd == nil {
		t.Fatal("Enter produced no command; a submit must be scheduled")
	}
	if updated.input.Value() != "" {
		t.Errorf("Enter left %q in the input; a submit clears it", updated.input.Value())
	}
}

// TestInputFieldLoneNewlinePasteIsTrimmed documents why insertNewline is its
// own primitive rather than handlePaste(PasteMsg{Content: "\n"}): a paste of
// nothing but a line break is normalized and then loses its trailing newline,
// so it is a no-op. That policy is right for paste (a pasted block must not
// leave the caret on an empty line) and wrong for an explicit request to break
// the line.
func TestInputFieldLoneNewlinePasteIsTrimmed(t *testing.T) {
	f := NewInputField().Focus().WithWidth(20)

	if after := f.handlePaste(PasteMsg{Content: "\n"}); after.Value() != "" {
		t.Errorf("lone-newline paste produced %q, want the field left empty", after.Value())
	}
	// ... while a real multi-line paste keeps its inner breaks.
	if after := f.handlePaste(PasteMsg{Content: "a\nb\n"}); after.Value() != "a\nb" {
		t.Errorf("multi-line paste produced %q, want %q", after.Value(), "a\nb")
	}
}

// TestInputFieldInsertNewline covers the primitive: content, cursor position,
// the goalCol reset, insertion at an arbitrary offset, and repeated presses.
func TestInputFieldInsertNewline(t *testing.T) {
	f := NewInputField().Focus().WithWidth(20).
		WithValue("ab").CursorEnd()
	f.goalCol = 7 // a stale up/down goal column from earlier line navigation

	f = f.insertNewline()
	if f.Value() != "ab\n" {
		t.Errorf("value = %q, want %q", f.Value(), "ab\n")
	}
	if f.CursorPos() != 3 {
		t.Errorf("cursor = %d, want 3 (moved past the inserted break)", f.CursorPos())
	}
	if f.goalCol != -1 {
		t.Errorf("goalCol = %d, want -1: the old goal belonged to the previous line", f.goalCol)
	}

	// Insert at an arbitrary offset: the break lands at the cursor and splits
	// the line.
	g := NewInputField().Focus().WithWidth(20).
		WithValue("abcdef").WithCursorPos(2).insertNewline()
	if g.Value() != "ab\ncdef" {
		t.Errorf("mid-value insert = %q, want %q", g.Value(), "ab\ncdef")
	}
	if g.CursorPos() != 3 {
		t.Errorf("mid-value cursor = %d, want 3", g.CursorPos())
	}

	// Repeated presses stack lines: the multi-line value the field is built
	// for (up/down navigate lines) is reachable without a terminal that
	// supports bracketed paste.
	h := NewInputField().Focus().WithWidth(20).
		WithValue("x").CursorEnd().
		insertNewline().insertNewline().insertNewline()
	if h.Value() != "x\n\n\n" {
		t.Errorf("three inserts = %q, want %q", h.Value(), "x\n\n\n")
	}
}

// TestPasteThenEnterSubmitsEverything is the behavior the split is for: on a
// host without bracketed paste, pasted text arrives as keystrokes, and the
// line endings in it must not submit anything. Submitting afterwards sends the
// whole block.
func TestPasteThenEnterSubmitsEverything(t *testing.T) {
	m := newTestTerminal()

	// "line1\nline2\n" as raw keystrokes: chars, LF, chars, LF.
	for _, r := range "line1\nline2\n" {
		var msg KeyMsg
		if r == '\n' {
			msg = keyCtrlJMsg()
		} else {
			msg = KeyPressMsg(Key{Text: string(r), Code: r})
		}
		var cmd Cmd
		m, cmd = m.handleInputKeys(msg)
		if cmd != nil {
			t.Fatalf("pasted byte %q triggered a command; nothing may submit until Enter", r)
		}
	}

	if got := m.input.Value(); got != "line1\nline2\n" {
		t.Errorf("value = %q, want %q", got, "line1\nline2\n")
	}

	_, cmd := m.handleInputKeys(KeyPressMsg(Key{Code: KeyEnter}))
	if cmd == nil {
		t.Fatal("Enter after a pasted block did not submit")
	}
}

// TestBlockText covers the rule for text that arrives as a block rather than as
// keystrokes — the content between bracketed-paste markers, and the finished
// buffer of an external editor (input_field.go → blockText). The two share one
// rule because they are the same category of input, and because on Windows both
// hand over CRLF where every other source hands over LF.
func TestBlockText(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"plain", "abc", "abc"},
		{"crlf becomes lf", "a\r\nb\r\nc", "a\nb\nc"},
		{"lone cr is a line ending too", "a\rb", "a\nb"},
		{"the editor's final newline is trimmed", "a\nb\n", "a\nb"},
		{"a buffer that only held newlines", "\r\n", ""},
		{"tabs go, as they do for a paste", "a\tb", "ab"},
		{"escape bytes are not pasted as keys", "\x1b[31mred", "[31mred"},
		{"a nul cannot reach the screen", "a\x00b", "ab"},
		{"unicode survives", "你好 😀", "你好 😀"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(blockText(tt.content)); got != tt.want {
				t.Errorf("blockText(%q) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}

// TestPasteUsesTheBlockRule keeps the two callers of the rule from drifting: what
// a paste inserts is exactly what blockText returns.
func TestPasteUsesTheBlockRule(t *testing.T) {
	f := NewInputField().Focus().WithWidth(20)
	after := f.handlePaste(PasteMsg{Content: "one\r\ntwo\r\n"})
	if got, want := after.Value(), string(blockText("one\r\ntwo\r\n")); got != want {
		t.Errorf("pasted value = %q, want %q", got, want)
	}
}
