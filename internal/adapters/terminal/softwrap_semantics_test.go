package terminal

// Regression tests for the soft-wrap semantics requested by the user:
// only genuinely-long SINGLE original lines use soft-wrap (their
// continuation rows join without '\n'); ordinary multi-line content is
// separated by hard '\n', so terminal selection copies the original
// multi-line structure back.

import (
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/tlv"
)

// TestMultiLineContentStaysMultiLine verifies the core complaint: content
// that is NOT one long line must not merge into a single logical line.
func TestMultiLineContentStaysMultiLine(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	original := "line one\nline two\nline three"
	wb.AppendOrUpdate(tlv.TagAssistantT, "a1", original)
	wb.SetViewportPosition(0, 8)
	out := stripANSI(wb.GetAll(-1, false))

	// The content region (between the box rules) must keep the newlines:
	// each original line is a separate row.
	content := extractWindowContent(out)
	if content == "" {
		t.Fatal("content region not found")
	}
	content = strings.Trim(content, "\n")
	want := original
	if content != want {
		t.Errorf("multi-line content must copy back multi-line:\n  got:  %q\n  want: %q", content, want)
	}
}

// TestLongSingleLineSoftWraps verifies a long single line stays one
// logical line: its continuation rows join without '\n' (the terminal
// soft-wraps them), so a selection copies the original text.
func TestLongSingleLineSoftWraps(t *testing.T) {
	wb := NewWindowBuffer(40, DefaultStyles())
	original := strings.Repeat("word ", 25)
	wb.AppendOrUpdate(tlv.TagAssistantT, "a1", original)
	wb.SetViewportPosition(0, 8)
	out := stripANSI(wb.GetAll(-1, false))

	content := extractWindowContent(out)
	if content == "" {
		t.Fatal("content region not found")
	}
	content = strings.Trim(content, "\n")
	if strings.Contains(content, "\n") {
		t.Errorf("single-line content must not contain hard newlines: %q", content)
	}
	if strings.TrimRight(content, " ") != strings.TrimRight(original, " ") {
		t.Errorf("long line must copy back intact:\n  got:  %q\n  want: %q", content, original)
	}
	// It occupies several terminal rows (soft-wrap), not one.
	if w := cellWidth(content); w <= 40 {
		t.Errorf("long line should exceed one row, width = %d", w)
	}
}
