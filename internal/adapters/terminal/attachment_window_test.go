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
