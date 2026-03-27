package terminal

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
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
	wb.AppendOrUpdate("test", "assistant", "Hello world")
	w := wb.Windows[0]

	// First render - should populate cache
	styles := DefaultStyles()
	borderStyle := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(styles.ColorDim).Padding(0, 1)
	cursorStyle := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(styles.BorderCursor).Padding(0, 1)

	_ = w.Render(80, false, styles, borderStyle, cursorStyle)

	// Cache should be valid
	if !w.cache.valid {
		t.Error("expected cache to be valid after render")
	}

	// Render again - should use cache
	rendered1 := w.Render(80, false, styles, borderStyle, cursorStyle)
	rendered2 := w.Render(80, false, styles, borderStyle, cursorStyle)

	if rendered1 != rendered2 {
		t.Error("expected same result from cached render")
	}
}

func TestWindowRenderCacheInvalidation(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Add content and render
	wb.AppendOrUpdate("test", "assistant", "Hello")
	w := wb.Windows[0]

	styles := DefaultStyles()
	borderStyle := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(styles.ColorDim).Padding(0, 1)
	cursorStyle := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(styles.BorderCursor).Padding(0, 1)

	_ = w.Render(80, false, styles, borderStyle, cursorStyle)

	// Cache should be valid
	if !w.cache.valid {
		t.Error("expected cache to be valid after render")
	}

	// Check initial wrappedLines contains "Hello"
	if len(w.cache.wrappedLines) == 0 {
		t.Fatal("expected wrappedLines to be populated")
	}
	if !strings.Contains(w.cache.wrappedLines[0], "Hello") {
		t.Errorf("expected 'Hello' in wrappedLines[0], got %q", w.cache.wrappedLines[0])
	}

	// AppendContent with incremental update (window has styles and wrappedLines)
	w.AppendContent(" world", 76)

	// Content should include both parts
	if !strings.Contains(w.Content, "Hello world") {
		t.Errorf("expected 'Hello world' in content, got %q", w.Content)
	}

	// wrappedLines should be updated incrementally (should now contain "world")
	if !strings.Contains(w.cache.wrappedLines[0], "world") {
		t.Errorf("expected 'world' in wrappedLines[0] after incremental update, got %q", w.cache.wrappedLines[0])
	}

	// Render again - should use cached wrappedLines, not re-wrap
	rendered := w.Render(80, false, styles, borderStyle, cursorStyle)

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
