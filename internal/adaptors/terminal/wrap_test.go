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
		wantMin int // minimum expected lines (could be more due to wrapping)
		wantMax int // maximum expected lines
	}{
		{
			name:    "short content fits one line",
			content: "Hello world",
			width:   80,
			wantMin: 1,
			wantMax: 1,
		},
		{
			name:    "content wraps to multiple lines",
			content: "This is a longer piece of text that should wrap",
			width:   20,
			wantMin: 2,
			wantMax: 4,
		},
		{
			name:    "content with newlines",
			content: "Line 1\nLine 2\nLine 3",
			width:   80,
			wantMin: 3,
			wantMax: 3,
		},
		{
			name:    "empty content",
			content: "",
			width:   80,
			wantMin: 1, // Split of empty string returns [""]
			wantMax: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := wrapLines(tt.content, tt.width)
			if len(lines) < tt.wantMin || len(lines) > tt.wantMax {
				t.Errorf("wrapLines(%q, %d) returned %d lines, want between %d and %d",
					tt.content, tt.width, len(lines), tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestAppendDeltaToLines(t *testing.T) {
	tests := []struct {
		name     string
		initial  []string
		delta    string
		width    int
		validate func(t *testing.T, result []string)
	}{
		{
			name:    "append to empty lines",
			initial: nil,
			delta:   "Hello",
			width:   80,
			validate: func(t *testing.T, result []string) {
				if len(result) != 1 || result[0] != "Hello" {
					t.Errorf("expected [Hello], got %v", result)
				}
			},
		},
		{
			name:    "append short text to single line",
			initial: []string{"Hello"},
			delta:   " world",
			width:   80,
			validate: func(t *testing.T, result []string) {
				if len(result) != 1 || result[0] != "Hello world" {
					t.Errorf("expected [Hello world], got %v", result)
				}
			},
		},
		{
			name:    "append text that causes wrap",
			initial: []string{"Hello"},
			delta:   " world this is a longer line that should wrap",
			width:   20,
			validate: func(t *testing.T, result []string) {
				// Should have multiple lines after wrapping
				if len(result) < 2 {
					t.Errorf("expected multiple lines, got %v", result)
				}
				// Verify content is preserved
				joined := strings.Join(result, "")
				if !strings.Contains(joined, "Hello") {
					t.Errorf("content should contain 'Hello', got %v", result)
				}
			},
		},
		{
			name:    "append text with newline",
			initial: []string{"Line 1"},
			delta:   "\nLine 2",
			width:   80,
			validate: func(t *testing.T, result []string) {
				if len(result) < 2 {
					t.Errorf("expected at least 2 lines, got %v", result)
				}
				if result[0] != "Line 1" {
					t.Errorf("first line should be 'Line 1', got %v", result)
				}
			},
		},
		{
			name:    "append multiple newlines",
			initial: []string{"Start"},
			delta:   "\nLine 2\nLine 3",
			width:   80,
			validate: func(t *testing.T, result []string) {
				if len(result) < 3 {
					t.Errorf("expected at least 3 lines, got %v", result)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := appendDeltaToLines(tt.initial, tt.delta, tt.width)
			tt.validate(t, result)
		})
	}
}

func TestIncrementalWrap(t *testing.T) {
	// Test that incremental wrap produces same results as full wrap
	width := 40
	content := "This is a test sentence that should be wrapped at the specified width."

	// Full wrap
	fullLines := wrapLines(content, width)

	// Incremental wrap: add word by word
	words := strings.Fields(content)
	var incrementalLines []string
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

func TestWindowLinesCache(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Add content incrementally
	wb.AppendOrUpdate("test", "assistant", "Hello")
	wb.AppendOrUpdate("test", "assistant", " world")
	wb.AppendOrUpdate("test", "assistant", " this is a test")

	// Get window
	w := wb.Windows[0]

	// Lines should be cached after render
	innerWidth := 80 - 4 // width minus padding/border
	_ = wb.renderWindowContent(w, innerWidth)

	// Verify lines are cached
	if len(w.Lines) == 0 {
		t.Error("expected lines to be cached after render")
	}

	// Verify cache is used on subsequent calls (LineWidth should be set)
	if w.LineWidth != innerWidth {
		t.Errorf("expected LineWidth to be %d, got %d", innerWidth, w.LineWidth)
	}
}

func TestWindowLinesCacheInvalidation(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Add content and render
	wb.AppendOrUpdate("test", "assistant", "Hello")
	w := wb.Windows[0]
	innerWidth := 80 - 4
	_ = wb.renderWindowContent(w, innerWidth)

	// Cache should be valid
	if w.LineWidth != innerWidth {
		t.Errorf("expected LineWidth to be %d, got %d", innerWidth, w.LineWidth)
	}

	// Change width - should invalidate cache
	wb.SetWidth(100)
	newInnerWidth := 100 - 4
	if w.LineWidth != 0 {
		t.Errorf("expected LineWidth to be 0 after resize, got %d", w.LineWidth)
	}

	// Render with new width - should rebuild cache
	_ = wb.renderWindowContent(w, newInnerWidth)
	if w.LineWidth != newInnerWidth {
		t.Errorf("expected LineWidth to be %d after render, got %d", newInnerWidth, w.LineWidth)
	}
}

func BenchmarkFullWrap(b *testing.B) {
	// Simulate streaming: each iteration appends a small delta to a large content
	content := strings.Repeat("This is a test sentence for wrapping. ", 100) // ~4KB
	width := 80

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Full wrap each time (old approach)
		_ = wrapLines(content, width)
	}
}

func BenchmarkIncrementalWrap(b *testing.B) {
	// Same scenario but with incremental wrap
	baseContent := strings.Repeat("This is a test sentence for wrapping. ", 99)
	delta := "This is a test sentence for wrapping. "
	width := 80

	// Start with pre-wrapped base content
	lines := wrapLines(baseContent, width)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Incremental wrap (new approach)
		lines = appendDeltaToLines(lines, delta, width)
	}
}
