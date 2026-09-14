package terminal

import (
	"strings"
	"testing"
)

func TestWrapVisualLines(t *testing.T) {
	tests := []struct {
		name    string
		content string
		width   int
		wantMin int // minimum expected rows
	}{
		{"empty", "", 80, 1},
		{"short", "Hello", 80, 1},
		{"exact width", strings.Repeat("a", 80), 80, 1},
		{"over width", strings.Repeat("a", 81), 80, 2},
		{"with newlines", "Hello\nWorld", 80, 2},
		{"long with newlines", strings.Repeat("a", 81) + "\n" + strings.Repeat("b", 81), 80, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := wrapVisualLines(tt.content, tt.width)
			if len(lines) < tt.wantMin {
				t.Errorf("wrapVisualLines() returned %d rows, want at least %d", len(lines), tt.wantMin)
			}
		})
	}
}

// TestWrapLabelsTrailingEmpty verifies a trailing empty label does not
// drop the last non-empty line (the flush must run after the loop, not on
// the last-iteration check).
func TestWrapLabelsTrailingEmpty(t *testing.T) {
	labels := []string{"a.pdf", "b.png", ""}
	got := wrapLabels(labels, 20, NewStyle())
	lines := strings.Split(got, "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d: %q", len(lines), got)
	}
	if !strings.Contains(got, "a.pdf") || !strings.Contains(got, "b.png") {
		t.Errorf("expected both labels on the line, got %q", got)
	}

	// All-empty input still yields nothing.
	if got := wrapLabels([]string{"", ""}, 20, NewStyle()); got != "" {
		t.Errorf("all-empty labels = %q, want empty", got)
	}
}

func TestTruncateWithSuffix(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		maxWidth int
		want     string
	}{
		// Zero and negative width
		{"zero width", "hello", 0, ""},
		{"negative width", "hello", -1, ""},

		// Content fits without truncation
		{"fits exactly", "abc", 3, "abc"},
		{"fits with room", "a", 5, "a"},
		{"empty content", "", 5, ""},

		// maxWidth = 1
		{"single char fits", "a", 1, "a"},
		{"single char truncated", "ab", 1, "\u2026"},
		{"single char truncated long", "abcdef", 1, "\u2026"},

		// Normal truncation with …
		{"truncate short", "abcd", 3, "ab\u2026"},
		{"truncate longer", "hello world", 5, "hell\u2026"},
		{"truncate exactly at boundary", "abcdef", 5, "abcd\u2026"},

		// Exact boundary behavior
		{"exact fit no truncation", "hello", 5, "hello"},
		{"one over", "hello!", 5, "hell\u2026"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateWithSuffix(tt.content, tt.maxWidth)
			if got != tt.want {
				t.Errorf("truncateWithSuffix(%q, %d) = %q, want %q", tt.content, tt.maxWidth, got, tt.want)
			}
		})
	}
}

// TestStatusBarTruncatedEllipsisStyled locks the status bar's styling
// contract at the rendering level: a "…" inserted by truncation is
// always inside a styled run — never a bare ellipsis in the terminal
// default color. The status bar stores PLAIN text and styles each
// segment at render time, so the ellipsis color falls out of the render
// pipeline. This covers the cases that used to fail with raw ANSI
// handling: a segment cut mid-text, and truncation landing on either
// side of the " | " separator.
func TestStatusBarTruncatedEllipsisStyled(t *testing.T) {
	styles := DefaultStyles()
	segStyle := styles.Status.Foreground(styles.ColorMuted)
	// SGR open prefix of the segment style, e.g. "\x1b[38;2;108;112;134m".
	segSig := segStyle.Render("X")
	if i := strings.Index(segSig, "X"); i > 0 {
		segSig = segSig[:i]
	}

	// The two inputs below are the shape updateStatus produces: a context
	// segment on the left, and the model group — the model name with the
	// reasoning level fused to it (statusRightSegment) — on the right.
	const leftSeg, modelGroup = "12.3K/128K", "gpt-4o | R0"

	newTerm := func(width int, status, right string) Terminal {
		m := newTerminalForUpdateStatusTest(NewTerminalOutput(styles))
		m.windowWidth = width
		m.statusLeft = status
		m.statusRight = right
		m.inProgress = true
		return m
	}

	// ellipsisStyled reports whether a "…" in the rendered bar sits
	// inside a segStyle run: a segSig open before it with no reset in
	// between (a bare ellipsis has no open, or is preceded by a reset).
	ellipsisStyled := func(rendered string) bool {
		i := strings.Index(rendered, "\u2026")
		if i < 0 {
			return true // no ellipsis — nothing to check
		}
		head := rendered[:i]
		open := strings.LastIndex(head, segSig)
		if open < 0 {
			return false
		}
		return !strings.Contains(head[open+len(segSig):], "\x1b[m")
	}

	// Group merged into the left (gap ≤ 3) and the truncation landing
	// inside it: "…" must be muted, not bare. W=21 → "… | gpt-4…".
	m := newTerm(21, leftSeg, modelGroup)
	if rendered := m.renderStatusBar(); !ellipsisStyled(rendered) {
		t.Errorf("segment truncated mid-text: ellipsis not in segment style, got %q", rendered)
	}

	// Truncation landing just left of the " | " separator (the space
	// before it survives, the separator does not). W=14 → "…/128K …".
	m = newTerm(14, leftSeg, modelGroup)
	if rendered := m.renderStatusBar(); !ellipsisStyled(rendered) {
		t.Errorf("truncation left of separator: ellipsis not in segment style, got %q", rendered)
	}

	// Truncation landing just right of the " | " separator (the space
	// after it is the cell cut). W=15 → "…/128K |…".
	m = newTerm(15, leftSeg, modelGroup)
	if rendered := m.renderStatusBar(); !ellipsisStyled(rendered) {
		t.Errorf("truncation right of separator: ellipsis not in segment style, got %q", rendered)
	}

	// Sweep: no width may produce a bare (unstyled) ellipsis.
	for _, width := range []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10} {
		m = newTerm(width, leftSeg, modelGroup)
		if rendered := m.renderStatusBar(); !ellipsisStyled(rendered) {
			t.Errorf("width %d: bare ellipsis without segment style: %q", width, rendered)
		}
	}
}

// TestStatusBarModelSeparatorGap locks the single-threshold placement
// rule between the left segments and the model group (the active model
// name with the reasoning level fused to it — the shape updateStatus
// produces):
//
//   - ample space (gap > 3) → the group floats right-aligned after blank
//     padding, flush right
//   - tight space (gap ≤ 3) → no right-aligned element: the group
//     merges into the left segments (joined by " | "), truncated
//     together — a squeezed right-aligned group or a 1-3 cell gap never
//     renders
//
// In the merged branch the level, riding at the group's tail, is what
// truncation reaches first; the model name is what it costs next.
func TestStatusBarModelSeparatorGap(t *testing.T) {
	m := newTerminalForUpdateStatusTest(NewTerminalOutput(DefaultStyles()))
	m.statusLeft = "12.3K/128K"   // indicator + space + left = 12 cols
	m.statusRight = "gpt-4o | R0" // the group = 11 cols
	m.inProgress = false

	render := func(width int) string {
		m.windowWidth = width
		return stripANSI(m.renderStatusBar())
	}

	// Ample space (W=27, gap 4): the group floats, blank padding.
	if got := render(27); got != "⠿ 12.3K/128K    gpt-4o | R0" {
		t.Errorf("gap>3: got %q, want %q", got, "⠿ 12.3K/128K    gpt-4o | R0")
	}

	// Gap exactly 3 (W=26): merges into the left — reads like another
	// segment, group stays flush right.
	if got := render(26); got != "⠿ 12.3K/128K | gpt-4o | R0" {
		t.Errorf("gap==3: got %q, want %q", got, "⠿ 12.3K/128K | gpt-4o | R0")
	}

	// Tight space (W=24, gap 1): merged; the level's cells go first, the
	// model name is still whole.
	if got := render(24); got != "⠿ 12.3K/128K | gpt-4o |…" {
		t.Errorf("gap<3 with full level: got %q, want %q", got, "⠿ 12.3K/128K | gpt-4o |…")
	}

	// Tighter (W=21): the truncation reaches the model name.
	if got := render(21); got != "⠿ 12.3K/128K | gpt-4…" {
		t.Errorf("gap<3 with truncated model: got %q, want %q", got, "⠿ 12.3K/128K | gpt-4…")
	}

	// No room even merged (W=14): the left segment is truncated, the
	// group is cut away and "…" marks the truncation.
	if got := render(14); got != "⠿ 12.3K/128K …" {
		t.Errorf("no room for the group: got %q, want %q", got, "⠿ 12.3K/128K …")
	}
}

// TestStatusBarTruncatedSeparatorStyled covers the "|…" case: when
// truncation replaces the space after a "|" with "…", the "|" must still be
// rendered in the bar's one style (muted) — a " | "-based split would miss
// the separator and leave it bare. Segments and separators share that style,
// so the bar is a single color.
func TestStatusBarTruncatedSeparatorStyled(t *testing.T) {
	styles := DefaultStyles()
	sepSig := styles.Status.Foreground(styles.ColorMuted).Render("X")
	if i := strings.Index(sepSig, "X"); i > 0 {
		sepSig = sepSig[:i] // muted SGR open prefix
	}

	m := newTerminalForUpdateStatusTest(NewTerminalOutput(styles))
	m.statusLeft = "12.3K/128K"
	m.statusRight = "gpt-4o | R0"
	m.inProgress = false
	m.windowWidth = 15 // budget 13 → head "12.3K/128K |" + "…": the space after "|" is the cell cut

	rendered := m.renderStatusBar()
	if !strings.Contains(rendered, sepSig+"|") {
		t.Errorf("'|' before '…' not rendered in the bar style: %q", rendered)
	}
	if got := stripANSI(rendered); got != "⠿ 12.3K/128K |…" {
		t.Errorf("text = %q, want %q", got, "⠿ 12.3K/128K |…")
	}
}

func TestIncrementalWrapVisual(t *testing.T) {
	width := 80

	// Start with initial content
	lines := wrapVisualLines("Hello", width)
	if len(lines) != 1 {
		t.Errorf("Expected 1 row, got %d", len(lines))
	}

	// Append to same row (no newline)
	lines = appendDeltaToVisualLines(lines, " world", width)
	if len(lines) != 1 {
		t.Errorf("Expected 1 row, got %d", len(lines))
	}

	// Append with newline
	lines = appendDeltaToVisualLines(lines, "\nNew line", width)
	if len(lines) != 2 {
		t.Errorf("Expected 2 rows, got %d", len(lines))
	}
}

// The incremental-vs-full equivalence property is covered exhaustively (tabs,
// CRLF, CJK, long tokens, ...) by TestIncrementalMatchesFullRewrap in
// softwrap_incremental_test.go; the line-array variant that used to live here
// was removed with the wrapLines/appendDeltaToLines API it exercised.

func TestWindowRenderCaching(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Add content
	wb.AppendOrUpdate("assistant", "test", "Hello world")
	w := wb.WindowAt(0)

	// First render - should populate cache
	styles := DefaultStyles()

	_ = w.Render(80, false, styles, false)

	// Cache should be valid
	if !w.cache.valid {
		t.Error("expected cache to be valid after render")
	}

	// Render again - should use cache
	rendered1 := w.Render(80, false, styles, false)
	rendered2 := w.Render(80, false, styles, false)

	if rendered1 != rendered2 {
		t.Error("expected same result from cached render")
	}
}

func TestWindowRenderCacheInvalidation(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Add content and render
	wb.AppendOrUpdate("assistant", "test", "Hello")
	w := wb.WindowAt(0)

	styles := DefaultStyles()

	_ = w.Render(80, false, styles, false)

	// Cache should be valid
	if !w.cache.valid {
		t.Error("expected cache to be valid after render")
	}

	// Append more content to verify streaming accumulation
	w.AppendContent(" world")

	// Content should include both parts — AppendFromTLV accumulates deltas
	if !strings.Contains(w.RawContent(), "Hello world") {
		t.Errorf("expected 'Hello world' in content, got %q", w.RawContent())
	}

	// Render again — should use cached output, not re-wrap from scratch
	rendered := w.Render(80, false, styles, false)

	// Render should contain the styled content
	if !strings.Contains(rendered, "Hello") {
		t.Error("expected 'Hello' in rendered output")
	}
	if !strings.Contains(rendered, "world") {
		t.Error("expected 'world' in rendered output")
	}
}

func BenchmarkFullWrap(b *testing.B) {
	content := strings.Repeat("This is a test sentence for wrapping. ", 100)
	width := 80

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = wrapVisualLines(content, width)
	}
}

func BenchmarkIncrementalWrap(b *testing.B) {
	baseContent := strings.Repeat("This is a test sentence for wrapping. ", 99)
	delta := "This is a test sentence for wrapping. "
	width := 80

	lines := wrapVisualLines(baseContent, width)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lines = appendDeltaToVisualLines(lines, delta, width)
	}
}
