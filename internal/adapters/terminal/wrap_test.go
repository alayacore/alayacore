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
