package terminal

import (
	"strings"
	"testing"

	ansi "github.com/charmbracelet/x/ansi"

	"github.com/alayacore/alayacore/internal/tlv"
)

// TestUserPromptLongSingleLineSoftWraps is a regression test for the
// user echo window: a long SINGLE user line must use the same soft-wrap
// pipeline as the assistant text window — its continuation rows join
// without '\n' (the terminal soft-wraps them). Earlier, BuildInner
// pre-passed the text through wrapContent, which inserted hard '\n' at
// wrap points; wrapVisualLines then mistook those '\n' for original-line
// breaks and produced hard-wrapped output, so a terminal selection
// copied broken chunks instead of the original sentence.
func TestUserPromptLongSingleLineSoftWraps(t *testing.T) {
	original := strings.Repeat("word ", 25)
	ur := &userRenderer{textParts: []string{original}}
	lines, _ := ur.BuildInner(40, false, DefaultStyles())
	if len(lines) == 0 {
		t.Fatal("BuildInner returned no visual lines")
	}

	// Joined projection: must NOT contain a hard newline — the single
	// line's soft-wrap continuations join without '\n', so a terminal
	// selection restores the original text verbatim (trailing space
	// stripped by Hardwrap, like the AT regression test).
	joined := joinVisualLines(lines)
	if strings.Contains(joined, "\n") {
		t.Fatalf("user prompt soft-wrap broken — hard newlines in joined content: %q", joined)
	}
	if got := strings.TrimRight(joined, " "); got != strings.TrimRight(original, " ") {
		t.Errorf("long line must copy back intact:\n  got:  %q\n  want: %q", got, original)
	}

	// Continuation rows must carry Cont=true so the viewport pads them
	// to the box width (the soft-wrap break must land at the visual
	// boundary, never mid-cell).
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 visual rows for a 125-char line at width 40, got %d", len(lines))
	}
	if !lines[1].Cont {
		t.Errorf("row 1 should be a soft-wrap continuation (Cont=true), got Cont=%v: %q",
			lines[1].Cont, lines[1].Text)
	}
	if w := ansi.StringWidth(strings.TrimRight(joined, " ")); w != 124 {
		t.Errorf("joined display width = %d, want 124 (25 words × 5 cells − trailing space)", w)
	}
}

// TestUserPromptMultiLineContentStaysMultiLine verifies the
// complementary guarantee: when the user text has REAL hard newlines
// (multiple text parts joined by "───", or a single part with '\n'
// inside), each original line stays a separate visual row with hard
// separators — terminal selection copies the original multi-line
// structure back.
func TestUserPromptMultiLineContentStaysMultiLine(t *testing.T) {
	ur := &userRenderer{
		textParts: []string{"line one", "line two", "line three"},
	}
	lines, _ := ur.BuildInner(80, false, DefaultStyles())

	// 3 text parts separated by "───" → 5 visual rows:
	//   line one / --- / line two / --- / line three
	if len(lines) != 5 {
		t.Fatalf("expected 5 visual rows (3 parts + 2 separators), got %d: %q",
			len(lines), joinVisualLines(lines))
	}
	for i, l := range lines {
		if l.Cont {
			t.Errorf("row %d should be a fresh original line (Cont=false), got Cont=true: %q", i, l.Text)
		}
	}
	joined := stripANSI(joinVisualLines(lines))
	want := "line one\n───\nline two\n───\nline three"
	if joined != want {
		t.Errorf("multi-part content:\n  got:  %q\n  want: %q", joined, want)
	}
}

// TestUserPromptBottomRuleHardSeparation verifies the box structure:
// even when the user text soft-wraps to several terminal rows, the
// window's bottom rule is the start of a NEW original row (Cont=false)
// — joinVisualLines puts a hard '\n' before it, so the box ends with
// "…lastContent\nbottomRule" and the next window (or status bar) starts
// on a fresh row. The bottom rule itself does not pull in a trailing
// '\n'; the per-window '\n' separator is added by renderAll /
// renderVirtual between windows, and the TUI's View() adds another
// before the input box CUP (see tui.go — same workaround applied to
// every box type, not just the user echo window).
func TestUserPromptBottomRuleHardSeparation(t *testing.T) {
	ur := &userRenderer{
		textParts: []string{strings.Repeat("word ", 25)},
	}
	contentLines, _ := ur.BuildInner(40, false, DefaultStyles())
	if len(contentLines) == 0 {
		t.Fatal("BuildInner returned no content lines")
	}

	// Box construction (mirrors RenderOpenBoxLines): top rule, content,
	// bottom rule — all Cont=false (a fresh original row each).
	box := make([]visualLine, 0, len(contentLines)+2)
	box = append(box, visualLine{Text: "topRule"})
	box = append(box, contentLines...)
	box = append(box, visualLine{Text: "bottomRule"})

	// The bottom rule must be a fresh original row, not a continuation
	// of the soft-wrap tail — otherwise the rule would be glued to the
	// last soft-wrap row in the terminal display.
	last := box[len(box)-1]
	if last.Text != "bottomRule" {
		t.Fatalf("last box row should be the bottom rule, got %q", last.Text)
	}
	if last.Cont {
		t.Errorf("bottom rule must NOT be a continuation (Cont=false) — otherwise it merges into the soft-wrap tail: %q",
			last.Text)
	}

	// Joined projection: bottom rule is preceded by '\n' (Cont=false
	// marks the start of a new original row), but the soft-wrapped
	// content itself contains no '\n' (continuation rows join without
	// one). The trailing '\n' after the bottom rule belongs to the
	// inter-window separator, not the box itself.
	joined := joinVisualLines(box)
	if strings.Contains(contentLines[0].Text, "\n") {
		t.Errorf("soft-wrapped content must not contain hard newlines: %q", contentLines[0].Text)
	}
	if !strings.HasSuffix(joined, "\nbottomRule") {
		t.Errorf("bottom rule must be preceded by a hard newline: %q", joined)
	}
}

func TestCompactMediaSummary(t *testing.T) {
	labels := []string{
		tlv.MediaLabel(tlv.TagUserI),
		tlv.MediaLabel(tlv.TagUserA),
		tlv.MediaLabel(tlv.TagUserI),
		tlv.MediaLabel(tlv.TagUserD),
		tlv.MediaLabel(tlv.TagUserA),
	}

	got := compactMediaSummary(labels)
	want := "📷2 🎵2 📄1"
	if got != want {
		t.Errorf("compactMediaSummary() = %q, want %q", got, want)
	}
}

func TestUserPromptCollapsedMediaSummary(t *testing.T) {
	styles := DefaultStyles()
	ur := &userRenderer{
		textParts: []string{"analyze this"},
		mediaParts: []string{
			tlv.MediaLabel(tlv.TagUserI),
			tlv.MediaLabel(tlv.TagUserA),
			tlv.MediaLabel(tlv.TagUserI),
		},
	}

	line, count := ur.BuildCollapsed(100, styles)
	if count != 1 {
		t.Fatalf("collapsed lineCount = %d, want 1", count)
	}
	want := "USER PROMPT     📷2 🎵1 analyze this"
	if got := stripANSI(line); got != want {
		t.Errorf("BuildCollapsed() = %q, want %q", got, want)
	}
}

func TestUserPromptCollapsedMediaOnlySummary(t *testing.T) {
	ur := &userRenderer{
		mediaParts: []string{
			tlv.MediaLabel(tlv.TagUserI),
			tlv.MediaLabel(tlv.TagUserA),
			tlv.MediaLabel(tlv.TagUserI),
			tlv.MediaLabel(tlv.TagUserV),
		},
	}

	line, _ := ur.BuildCollapsed(30, DefaultStyles())
	want := "USER PROMPT     📷2 🎵1 🎬1"
	if got := stripANSI(line); got != want {
		t.Errorf("BuildCollapsed() = %q, want %q", got, want)
	}
	if width := ansi.StringWidth(stripANSI(line)); width > 28 {
		t.Errorf("collapsed media summary width = %d, want <= 28", width)
	}
}

func TestUserPromptCollapsedMediaSummaryFitsAndPrioritizesMedia(t *testing.T) {
	ur := &userRenderer{
		textParts: []string{strings.Repeat("long text ", 10)},
		mediaParts: []string{
			tlv.MediaLabel(tlv.TagUserI),
			tlv.MediaLabel(tlv.TagUserV),
			tlv.MediaLabel(tlv.TagUserA),
			tlv.MediaLabel(tlv.TagUserD),
		},
	}

	line, _ := ur.BuildCollapsed(34, DefaultStyles())
	plain := stripANSI(line)
	if !strings.Contains(plain, "📷1") {
		t.Errorf("collapsed media summary should retain the image badge, got %q", plain)
	}
	if width := ansi.StringWidth(plain); width > 32 {
		t.Errorf("collapsed media summary width = %d, want <= 32", width)
	}
	if strings.Contains(plain, "\n") {
		t.Errorf("collapsed media summary must remain one line, got %q", plain)
	}
}
