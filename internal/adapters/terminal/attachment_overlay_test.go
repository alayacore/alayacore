package terminal

// Regression tests for the attachment window's "bare input + flush rows"
// convention: the filter input carries no "F "/"U " prompt (it is a bare
// input like the model/theme selectors), and list rows render flush left
// with no "> " marker and no indent.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAttachmentOverlayBareInputPrompt(t *testing.T) {
	styles := DefaultStyles()
	aw := NewAttachmentOverlay(styles)

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

func TestAttachmentOverlayListRowsFlushLeft(t *testing.T) {
	// The picker lists the working directory, so give it one. The package
	// directory used to be the fixture: its contents are not ours to assert
	// on, and the skip below it meant the test ran nowhere once the directory
	// was empty.
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)

	styles := DefaultStyles()
	aw := NewAttachmentOverlay(styles).Open()
	if len(aw.filtered) == 0 {
		t.Fatal("the fixture's two files should be listed")
	}

	plain := stripANSI(aw.View().Content)
	lines := strings.Split(plain, "\n")

	// Rows hang below the filter box's closing rule — the second rule in the
	// overlay — and the help bar is the last line: the list has no rules of
	// its own any more.
	rules := 0
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "──") {
			rules++
			if rules == 2 {
				start = i + 1
				break
			}
		}
	}
	if start < 0 {
		t.Fatalf("could not locate the filter box's closing rule in: %q", plain)
	}
	for _, l := range lines[start : len(lines)-1] { // exclude the help bar
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
	var p KeyParser
	msgs := p.Parse([]byte{0x17})
	if len(msgs) != 1 {
		t.Fatalf("0x17 parsed to %d messages, want one key press", len(msgs))
	}
	killSegment, ok := msgs[0].(KeyPressMsg)
	if !ok {
		t.Fatalf("0x17 parsed to %T, want KeyPressMsg", msgs[0])
	}
	if got := killSegment.Chord(); got != keyCtrlW {
		t.Fatalf("0x17 reads as %q, want %q", got, keyCtrlW)
	}

	openWith := func(value string) AttachmentOverlay {
		aw := NewAttachmentOverlay(styles).Open()
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
