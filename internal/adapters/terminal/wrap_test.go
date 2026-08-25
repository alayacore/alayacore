package terminal

import (
	"strings"
	"testing"
)

func TestWrapLines(t *testing.T) {
	tests := []struct {
		name    string
		content string
		width   int
		wantMin int // minimum expected lines
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
			lines := wrapLines(tt.content, tt.width)
			if len(lines) < tt.wantMin {
				t.Errorf("wrapLines() returned %d lines, want at least %d", len(lines), tt.wantMin)
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
// handling: a segment squeezed to one column, and truncation landing on
// either side of the " | " separator.
func TestStatusBarTruncatedEllipsisStyled(t *testing.T) {
	styles := DefaultStyles()
	segStyle := styles.Status.Foreground(styles.ColorMuted)
	// SGR open prefix of the segment style, e.g. "\x1b[38;2;108;112;134m".
	segSig := segStyle.Render("X")
	if i := strings.Index(segSig, "X"); i > 0 {
		segSig = segSig[:i]
	}

	newTerm := func(width int, status, model string) Terminal {
		m := newTerminalForUpdateStatusTest(NewTerminalOutput(styles))
		m.windowWidth = width
		m.statusLeft = status
		m.statusRight = model
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

	// Model shown at a single column (separated by " | "): "…" must be
	// muted, not bare. W=9: left "• R0✦" (5) leaves 4 cells — the 3-cell
	// " | " separator plus a 1-column truncated model.
	m := newTerm(9, "R0✦", "gpt-4o")
	if rendered := m.renderStatusBar(); !ellipsisStyled(rendered) {
		t.Errorf("model at 1 column: ellipsis not in segment style, got %q", rendered)
	}

	// Truncation landing just left of the " | " separator.
	m = newTerm(6, "R0✦ | 123/128", "gpt-4o")
	if rendered := m.renderStatusBar(); !ellipsisStyled(rendered) {
		t.Errorf("truncation left of separator: ellipsis not in segment style, got %q", rendered)
	}

	// Truncation landing just right of the " | " separator.
	m = newTerm(8, "R0✦ | 123/128", "gpt-4o")
	if rendered := m.renderStatusBar(); !ellipsisStyled(rendered) {
		t.Errorf("truncation right of separator: ellipsis not in segment style, got %q", rendered)
	}

	// Sweep: no width may produce a bare (unstyled) ellipsis.
	for _, width := range []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10} {
		m = newTerm(width, "R0✦ | 123/128", "gpt-4o")
		if rendered := m.renderStatusBar(); !ellipsisStyled(rendered) {
			t.Errorf("width %d: bare ellipsis without segment style: %q", width, rendered)
		}
	}
}

// TestStatusBarModelSeparatorGap locks the gap rule between the left
// segments and the right-aligned model — a 1-2 cell gap never renders:
//
//   - gap exactly 3 → " | " (the same separator used between segments)
//   - gap > 3 → blank padding, model flush right
//   - gap < 3 with the full model → model truncated until the gap is
//     exactly 3
//   - no room for " | " + a 1-column model → model dropped
func TestStatusBarModelSeparatorGap(t *testing.T) {
	m := newTerminalForUpdateStatusTest(NewTerminalOutput(DefaultStyles()))
	m.statusLeft = "R0✦ | 12.3K/128K" // indicator + space + left = 18 cols
	m.statusRight = "gpt-4o"
	m.inProgress = false

	render := func(width int) string {
		m.windowWidth = width
		return stripANSI(m.renderStatusBar())
	}

	// Gap exactly 3: the model reads like another segment.
	if got := render(27); got != "· R0✦ | 12.3K/128K | gpt-4o" {
		t.Errorf("gap==3: got %q, want %q", got, "· R0✦ | 12.3K/128K | gpt-4o")
	}

	// Gap > 3: blank padding, no separator before the model.
	if got := render(30); got != "· R0✦ | 12.3K/128K      gpt-4o" {
		t.Errorf("gap>3: got %q, want %q", got, "· R0✦ | 12.3K/128K      gpt-4o")
	}

	// Full model would leave a 1-cell gap (W=25): truncate to reach 3.
	if got := render(25); got != "· R0✦ | 12.3K/128K | gpt…" {
		t.Errorf("gap<3 with full model: got %q, want %q", got, "· R0✦ | 12.3K/128K | gpt…")
	}

	// No room for the separator + one model column (W=20): model dropped.
	if got := render(20); got != "· R0✦ | 12.3K/128K" {
		t.Errorf("no room for model: got %q, want %q", got, "· R0✦ | 12.3K/128K")
	}
}

func TestIncrementalWrap(t *testing.T) {
	width := 80

	// Start with initial content
	lines := wrapLines("Hello", width)
	if len(lines) != 1 {
		t.Errorf("Expected 1 line, got %d", len(lines))
	}

	// Append to same line (no newline)
	lines = appendDeltaToLines(lines, " world", width)
	if len(lines) != 1 {
		t.Errorf("Expected 1 line, got %d", len(lines))
	}

	// Append with newline
	lines = appendDeltaToLines(lines, "\nNew line", width)
	if len(lines) != 2 {
		t.Errorf("Expected 2 lines, got %d", len(lines))
	}
}

func TestIncrementalWrapMatchesFullWrap(t *testing.T) {
	width := 40
	words := strings.Split("The quick brown fox jumps over the lazy dog and then some more words to make it longer", " ")

	// Full wrap at once
	fullContent := strings.Join(words, " ")
	fullLines := wrapLines(fullContent, width)

	// Incremental wrap
	incrementalLines := []string{}
	for i, word := range words {
		if i == 0 {
			incrementalLines = wrapLines(word, width)
		} else {
			incrementalLines = appendDeltaToLines(incrementalLines, " "+word, width)
		}
	}

	// Compare results
	joinedFull := strings.Join(fullLines, "\n")
	joinedIncremental := strings.Join(incrementalLines, "\n")

	if joinedFull != joinedIncremental {
		t.Errorf("Incremental wrap differs from full wrap:\nFull: %q\nIncremental: %q",
			joinedFull, joinedIncremental)
	}
}

func TestWindowRenderCaching(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Add content
	wb.AppendOrUpdate("assistant", "test", "Hello world")
	w := wb.WindowAt(0)

	// First render - should populate cache
	styles := DefaultStyles()
	borderStyle := NewStyle().Foreground(styles.ColorDim)

	_ = w.Render(80, false, styles, borderStyle, false)

	// Cache should be valid
	if !w.border.valid {
		t.Error("expected cache to be valid after render")
	}

	// Render again - should use cache
	rendered1 := w.Render(80, false, styles, borderStyle, false)
	rendered2 := w.Render(80, false, styles, borderStyle, false)

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
	borderStyle := NewStyle().Foreground(styles.ColorDim)

	_ = w.Render(80, false, styles, borderStyle, false)

	// Cache should be valid
	if !w.border.valid {
		t.Error("expected cache to be valid after render")
	}

	// Append more content to verify streaming accumulation
	w.AppendContent(" world")

	// Content should include both parts — AppendFromTLV accumulates deltas
	if !strings.Contains(w.RawContent(), "Hello world") {
		t.Errorf("expected 'Hello world' in content, got %q", w.RawContent())
	}

	// Render again — should use cached output, not re-wrap from scratch
	rendered := w.Render(80, false, styles, borderStyle, false)

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
		_ = wrapLines(content, width)
	}
}

func BenchmarkIncrementalWrap(b *testing.B) {
	baseContent := strings.Repeat("This is a test sentence for wrapping. ", 99)
	delta := "This is a test sentence for wrapping. "
	width := 80

	lines := wrapLines(baseContent, width)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lines = appendDeltaToLines(lines, delta, width)
	}
}
