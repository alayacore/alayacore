package terminal

// Regression tests for the attachment window's "bare input + flush rows"
// convention: the filter input carries no "F "/"U " prompt (it is a bare
// input like the model/theme selectors), and list rows render flush left
// with no "> " marker and no indent.

import (
	"strings"
	"testing"
)

func TestAttachmentWindowBareInputPrompt(t *testing.T) {
	styles := DefaultStyles()
	aw := NewAttachmentWindow(styles)

	aw = aw.Open()
	if aw.FilterInput.Prompt != "" {
		t.Errorf("local mode prompt = %q, want bare input (no \"F \")", aw.FilterInput.Prompt)
	}

	aw = aw.switchToURL()
	if aw.FilterInput.Prompt != "" {
		t.Errorf("URL mode prompt = %q, want bare input (no \"U \")", aw.FilterInput.Prompt)
	}

	aw = aw.switchToLocal()
	if aw.FilterInput.Prompt != "" {
		t.Errorf("local mode prompt after switch = %q, want bare input (no \"F \")", aw.FilterInput.Prompt)
	}
}

func TestAttachmentWindowListRowsFlushLeft(t *testing.T) {
	styles := DefaultStyles()
	aw := NewAttachmentWindow(styles).Open()
	if len(aw.filtered) == 0 {
		t.Skip("current directory has no entries — nothing to render")
	}

	plain := stripANSI(aw.View().Content)
	lines := strings.Split(plain, "\n")

	// Rows live after the "N items" count line, before the help bar.
	start := -1
	for i, l := range lines {
		if strings.Contains(l, "items") {
			start = i + 1
			break
		}
	}
	if start < 0 {
		t.Fatalf("could not locate the item count line in: %q", plain)
	}
	for _, l := range lines[start:] {
		if strings.HasPrefix(l, "──") { // bottom box rule
			break
		}
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "> ") {
			t.Errorf("list row must not carry the \"> \" marker, got %q", l)
		}
		if strings.HasPrefix(l, "  ") {
			t.Errorf("list row must be flush left (no indent), got %q", l)
		}
	}
}

// TestSegmentDeleteKeyIsCtrlW pins which key deletes a path segment, and which
// does not. Plain Backspace had that job until it was moved: a box that edits a
// path is still a box, and a user correcting one mistyped letter should not lose
// the whole directory. Ctrl+W is the chord because it is a control byte — see
// the note in docs/tui.md, and the comment on the binding itself for why neither
// shift+backspace nor ctrl+backspace can be made to work here.
func TestSegmentDeleteKeyIsCtrlW(t *testing.T) {
	styles := DefaultStyles()

	// Ctrl+W as the keyboard delivers it, so the binding is checked against the
	// wire form rather than a hand-built Key.
	var p InputParser
	msgs := p.Parse([]byte{0x17})
	if len(msgs) != 1 {
		t.Fatalf("0x17 parsed to %d messages, want one key press", len(msgs))
	}
	killSegment, ok := msgs[0].(KeyPressMsg)
	if !ok {
		t.Fatalf("0x17 parsed to %T, want KeyPressMsg", msgs[0])
	}
	if got := killSegment.String(); got != keyCtrlW {
		t.Fatalf("0x17 reads as %q, want %q", got, keyCtrlW)
	}

	openWith := func(value string) AttachmentWindow {
		aw := NewAttachmentWindow(styles).Open()
		aw.FilterInput = aw.FilterInput.WithValue(value)
		return aw
	}

	aw := openWith("/home/wallace/projects/")
	aw, _ = aw.Update(KeyPressMsg{Text: "backspace", Code: KeyBackspace})
	if got := aw.FilterInput.Value(); got != "/home/wallace/projects" {
		t.Errorf("Backspace gave %q, want one character removed", got)
	}

	aw = openWith("/home/wallace/projects/")
	aw, _ = aw.Update(killSegment)
	if got := aw.FilterInput.Value(); got != "/home/wallace/" {
		t.Errorf("Ctrl+W gave %q, want the segment gone back to the separator", got)
	}

	// The chord belongs to the path box: URL text and the file list leave it
	// alone rather than swallowing it or deleting from it.
	url := openWith("").switchToURL()
	url.FilterInput = url.FilterInput.WithValue("https://example.com/a.png")
	url, _ = url.Update(killSegment)
	if got := url.FilterInput.Value(); got != "https://example.com/a.png" {
		t.Errorf("Ctrl+W in URL mode changed the text to %q, want it untouched", got)
	}

	list := openWith("/home/wallace/projects/")
	list.FilterInputFocused = false
	list, _ = list.Update(killSegment)
	if got := list.FilterInput.Value(); got != "/home/wallace/projects/" {
		t.Errorf("Ctrl+W with the list focused changed the text to %q, want it untouched", got)
	}
}
