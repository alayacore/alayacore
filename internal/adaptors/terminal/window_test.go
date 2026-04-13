package terminal

import (
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/stream"
)

func TestWindowBuffer(t *testing.T) {
	t.Run("new buffer has correct width", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())
		if wb.Width() != 80 {
			t.Errorf("Width() = %d, want 80", wb.Width())
		}
	})

	t.Run("set width updates width", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())
		wb.SetWidth(120)
		if wb.Width() != 120 {
			t.Errorf("Width() = %d, want 120", wb.Width())
		}
	})

	t.Run("append creates new window", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())
		wb.AppendOrUpdate("window-1", stream.TagTextAssistant, "Hello")

		if len(wb.Windows) != 1 {
			t.Fatalf("len(Windows) = %d, want 1", len(wb.Windows))
		}
		if wb.Windows[0].ID != "window-1" {
			t.Errorf("Windows[0].ID = %q, want %q", wb.Windows[0].ID, "window-1")
		}
	})

	t.Run("update appends to existing window", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())
		wb.AppendOrUpdate("window-1", stream.TagTextAssistant, "Hello")
		wb.AppendOrUpdate("window-1", stream.TagTextAssistant, " World")

		if len(wb.Windows) != 1 {
			t.Fatalf("len(Windows) = %d, want 1", len(wb.Windows))
		}
		if !strings.Contains(wb.Windows[0].Content, "Hello") {
			t.Error("Content should contain 'Hello'")
		}
		if !strings.Contains(wb.Windows[0].Content, "World") {
			t.Error("Content should contain 'World'")
		}
	})

	t.Run("multiple windows", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())
		wb.AppendOrUpdate("window-1", stream.TagTextAssistant, "First")
		wb.AppendOrUpdate("window-2", stream.TagTextAssistant, "Second")
		wb.AppendOrUpdate("window-3", stream.TagTextAssistant, "Third")

		if len(wb.Windows) != 3 {
			t.Fatalf("len(Windows) = %d, want 3", len(wb.Windows))
		}
	})

	t.Run("get window count", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())
		if wb.GetWindowCount() != 0 {
			t.Errorf("GetWindowCount() = %d, want 0", wb.GetWindowCount())
		}

		wb.AppendOrUpdate("window-1", stream.TagTextAssistant, "First")
		if wb.GetWindowCount() != 1 {
			t.Errorf("GetWindowCount() = %d, want 1", wb.GetWindowCount())
		}

		wb.AppendOrUpdate("window-2", stream.TagTextAssistant, "Second")
		if wb.GetWindowCount() != 2 {
			t.Errorf("GetWindowCount() = %d, want 2", wb.GetWindowCount())
		}
	})

	t.Run("get all returns empty for empty buffer", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())
		content := wb.GetAll(-1)
		if content != "" {
			t.Errorf("GetAll() = %q, want empty", content)
		}
	})

	t.Run("get all returns content", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())
		wb.AppendOrUpdate("window-1", stream.TagTextAssistant, "Hello")
		content := wb.GetAll(-1)
		if content == "" {
			t.Error("GetAll() should not be empty")
		}
	})

	t.Run("delete window", func(t *testing.T) {
		// DeleteWindow is not exposed on WindowBuffer
		// This test verifies the buffer structure is correct
		wb := NewWindowBuffer(80, DefaultStyles())
		wb.AppendOrUpdate("window-1", stream.TagTextAssistant, "First")
		wb.AppendOrUpdate("window-2", stream.TagTextAssistant, "Second")

		if len(wb.Windows) != 2 {
			t.Fatalf("len(Windows) = %d, want 2", len(wb.Windows))
		}
	})
}

func TestWindowBufferViewport(t *testing.T) {
	t.Run("set viewport position", func(_ *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())
		wb.SetViewportPosition(10, 20)
		// Should not panic
	})

	t.Run("get total lines virtual", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())
		wb.AppendOrUpdate("window-1", stream.TagTextAssistant, strings.Repeat("line\n", 10))
		// Should return total lines
		lines := wb.GetTotalLines()
		if lines <= 0 {
			t.Errorf("GetTotalLines() = %d, want > 0", lines)
		}
	})
}

func TestWindowBufferDiff(t *testing.T) {
	t.Run("append diff content", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())
		content := "edit_file: test.txt\n- old line\n+ new line\n"
		wb.AppendToolCall("diff-1", "edit_file", content)

		if len(wb.Windows) != 1 {
			t.Fatalf("len(Windows) = %d, want 1", len(wb.Windows))
		}
		if wb.Windows[0].ToolName != "edit_file" {
			t.Errorf("ToolName = %s, want edit_file", wb.Windows[0].ToolName)
		}
	})

	t.Run("diff window folds when wrapped", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())

		// Create a diff with many lines
		var content strings.Builder
		content.WriteString("edit_file: test.txt\n")
		for i := 0; i < 20; i++ {
			content.WriteString("- ")
			content.WriteString(string(rune('a' + i%26)))
			content.WriteString("\n+ ")
			content.WriteString(string(rune('b' + i%26)))
			content.WriteString("\n")
		}

		wb.AppendToolCall("diff-1", "edit_file", content.String())

		// Verify window is folded by default
		if !wb.Windows[0].Folded {
			t.Error("Diff window should be folded by default")
		}

		// Render and check that it folds
		rendered := wb.GetAll(-1)
		renderedLines := strings.Split(rendered, "\n")

		// Should fold to ~5 lines of content (header + first + separator + last 3)
		// Plus border lines, so approximately 7-8 lines total
		if len(renderedLines) > 10 {
			t.Errorf("Rendered diff has %d lines, should be folded to ~7-8", len(renderedLines))
		}

		// Verify it contains the fold indicator (tricolon)
		hasIndicator := strings.Contains(rendered, "⁝")
		if !hasIndicator {
			t.Error("Folded diff should contain tricolon (⁝) separator")
		}
	})

	t.Run("diff window expands when unfolded", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())

		// Create a diff with many lines
		var content strings.Builder
		content.WriteString("edit_file: test.txt\n")
		for i := 0; i < 10; i++ {
			content.WriteString("- ")
			content.WriteString(string(rune('a' + i%26)))
			content.WriteString("\n+ ")
			content.WriteString(string(rune('b' + i%26)))
			content.WriteString("\n")
		}

		wb.AppendToolCall("diff-1", "edit_file", content.String())

		// Unfold the window
		wb.ToggleFold(0)

		// Render and check that it shows all lines
		rendered := wb.GetAll(-1)

		// Should show all 10 lines with - prefix
		removeCount := strings.Count(rendered, "- ")
		if removeCount != 10 {
			t.Errorf("Unfolded diff should show 10 changed lines with - prefix, found %d", removeCount)
		}
	})

	t.Run("diff window shows minimal prefixes", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())

		// Create a diff with unchanged, added, and removed lines
		content := "edit_file: test.txt\n" +
			"  unchanged line 1\n" +
			"- old content\n" +
			"+ new content\n" +
			"  unchanged line 2\n" +
			"- removed line\n" +
			"+ added line\n"

		wb.AppendToolCall("diff-1", "edit_file", content)

		// Unfold to see all lines
		wb.ToggleFold(0)

		rendered := wb.GetAll(-1)

		// Check that unchanged lines have space prefix
		if !strings.Contains(rendered, "  unchanged line 1") {
			t.Error("Unchanged line 1 should have space prefix")
		}
		if !strings.Contains(rendered, "  unchanged line 2") {
			t.Error("Unchanged line 2 should have space prefix")
		}

		// Check that changed line shows - on one line, + on next
		if !strings.Contains(rendered, "- old content") {
			t.Error("Changed old content should have - prefix")
		}
		if !strings.Contains(rendered, "+ new content") {
			t.Error("Changed new content should have + prefix")
		}

		// Check that removed line shows - prefix
		if !strings.Contains(rendered, "- removed line") {
			t.Error("Removed line should have - prefix")
		}

		// Check that added line shows + prefix
		if !strings.Contains(rendered, "+ added line") {
			t.Error("Added line should have + prefix")
		}
	})

	t.Run("diff window cache invalidates when folded changes", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())

		// Create a diff with many lines
		var content strings.Builder
		content.WriteString("edit_file: test.txt\n")
		for i := 0; i < 20; i++ {
			content.WriteString("- ")
			content.WriteString(string(rune('a' + i%26)))
			content.WriteString("\n+ ")
			content.WriteString(string(rune('b' + i%26)))
			content.WriteString("\n")
		}

		wb.AppendToolCall("diff-1", "edit_file", content.String())

		// First render - should be folded (Folded=true)
		rendered1 := wb.GetAll(-1)
		removeCount1 := strings.Count(rendered1, "- ")
		if removeCount1 >= 10 {
			t.Errorf("Folded diff should fold lines, found %d - prefixes", removeCount1)
		}

		// Toggle fold
		wb.ToggleFold(0)

		// Second render - should be expanded (Folded=false)
		rendered2 := wb.GetAll(-1)
		removeCount2 := strings.Count(rendered2, "- ")
		if removeCount2 != 20 {
			t.Errorf("Unfolded diff should show all 20 lines with - prefix, found %d", removeCount2)
		}

		// Toggle back
		wb.ToggleFold(0)

		// Third render - should be folded again
		rendered3 := wb.GetAll(-1)
		removeCount3 := strings.Count(rendered3, "- ")
		if removeCount3 >= 10 {
			t.Errorf("Re-folded diff should fold lines again, found %d - prefixes", removeCount3)
		}
	})

	t.Run("user and assistant messages not folded by default", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())

		// Create windows for different tag types
		wb.AppendOrUpdate("user-1", stream.TagTextUser, "User message")
		wb.AppendOrUpdate("assistant-1", stream.TagTextAssistant, "Assistant message")
		wb.AppendToolCall("tool-1", "test_tool", "Tool output")
		wb.AppendOrUpdate("reasoning-1", stream.TagTextReasoning, "Reasoning content")

		// User and Assistant should NOT be folded (show full content)
		if wb.Windows[0].Folded {
			t.Error("User window should NOT be folded by default")
		}
		if wb.Windows[1].Folded {
			t.Error("Assistant window should NOT be folded by default")
		}

		// Tool window should be folded
		if !wb.Windows[2].Folded {
			t.Error("Tool window should be folded by default")
		}
		// Reasoning should be folded
		if !wb.Windows[3].Folded {
			t.Error("Reasoning window should be folded by default")
		}
	})
}

func TestWindowBufferVisibility(t *testing.T) {
	t.Run("tool windows are always visible", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())
		wb.AppendToolCall("tool-1", "shell", "")

		if len(wb.Windows) != 1 {
			t.Fatalf("len(Windows) = %d, want 1", len(wb.Windows))
		}
		if !wb.Windows[0].Visible {
			t.Error("Tool window should always be visible, even with empty content")
		}
	})

	t.Run("delta window with content is visible", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())
		wb.AppendOrUpdate("delta-1", stream.TagTextAssistant, "Hello")

		if len(wb.Windows) != 1 {
			t.Fatalf("len(Windows) = %d, want 1", len(wb.Windows))
		}
		if !wb.Windows[0].Visible {
			t.Error("Delta window with non-whitespace content should be visible")
		}
	})

	t.Run("delta window with only whitespace is not visible", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())
		wb.AppendOrUpdate("delta-1", stream.TagTextAssistant, "   \n\t  ")

		if len(wb.Windows) != 1 {
			t.Fatalf("len(Windows) = %d, want 1", len(wb.Windows))
		}
		if wb.Windows[0].Visible {
			t.Error("Delta window with only whitespace should NOT be visible")
		}
	})

	t.Run("delta window with empty content is not visible", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())
		wb.AppendOrUpdate("delta-1", stream.TagTextAssistant, "")

		if len(wb.Windows) != 1 {
			t.Fatalf("len(Windows) = %d, want 1", len(wb.Windows))
		}
		if wb.Windows[0].Visible {
			t.Error("Delta window with empty content should NOT be visible")
		}
	})

	t.Run("delta window becomes visible when non-whitespace added", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())

		// Start with whitespace-only content
		wb.AppendOrUpdate("delta-1", stream.TagTextAssistant, "\n\n")
		if wb.Windows[0].Visible {
			t.Error("Delta window with only newlines should NOT be visible initially")
		}

		// Add actual content
		wb.AppendOrUpdate("delta-1", stream.TagTextAssistant, "Hello")
		if !wb.Windows[0].Visible {
			t.Error("Delta window should become visible when non-whitespace content is added")
		}
	})

	t.Run("whitespace before content preserves visibility", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())

		// Start with whitespace
		wb.AppendOrUpdate("delta-1", stream.TagTextAssistant, "  \n  ")
		if wb.Windows[0].Visible {
			t.Error("Delta window with only whitespace should NOT be visible")
		}

		// Add more whitespace - still not visible
		wb.AppendOrUpdate("delta-1", stream.TagTextAssistant, "\t")
		if wb.Windows[0].Visible {
			t.Error("Delta window should still not be visible with only whitespace")
		}

		// Add actual content
		wb.AppendOrUpdate("delta-1", stream.TagTextAssistant, "World")
		if !wb.Windows[0].Visible {
			t.Error("Delta window should be visible after adding non-whitespace")
		}

		// Content should include all the whitespace
		expected := "  \n  \tWorld"
		if wb.Windows[0].Content != expected {
			t.Errorf("Content = %q, want %q", wb.Windows[0].Content, expected)
		}
	})

	t.Run("non-visible windows are not rendered", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())

		// Create visible and non-visible windows
		wb.AppendOrUpdate("delta-1", stream.TagTextAssistant, "\n\n")  // Not visible
		wb.AppendOrUpdate("delta-2", stream.TagTextAssistant, "Hello") // Visible
		wb.AppendOrUpdate("delta-3", stream.TagTextAssistant, "  ")    // Not visible
		wb.AppendOrUpdate("delta-4", stream.TagTextAssistant, "World") // Visible

		rendered := wb.GetAll(-1)

		// Should contain Hello and World, but not placeholders for invisible windows
		if !strings.Contains(rendered, "Hello") {
			t.Error("Rendered content should contain 'Hello'")
		}
		if !strings.Contains(rendered, "World") {
			t.Error("Rendered content should contain 'World'")
		}

		// Count windows - should only have 2 visible ones
		visibleCount := wb.GetVisibleWindowCount()
		if visibleCount != 2 {
			t.Errorf("GetVisibleWindowCount() = %d, want 2", visibleCount)
		}
	})

	t.Run("line heights only count visible windows", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())

		// Create visible and non-visible windows
		wb.AppendOrUpdate("delta-1", stream.TagTextAssistant, "Hello\nWorld") // Visible, 2 lines
		wb.AppendOrUpdate("delta-2", stream.TagTextAssistant, "\n\n\n")       // Not visible

		lines := wb.GetTotalLines()
		// Only delta-1 contributes to total lines (plus border = ~4 lines)
		if lines <= 0 {
			t.Errorf("GetTotalLines() = %d, should be > 0 (only visible windows)", lines)
		}

		// The invisible window should have 0 line height
		wb.ensureLineHeights()
		if wb.lineHeights[1] != 0 {
			t.Errorf("Invisible window lineHeight = %d, want 0", wb.lineHeights[1])
		}
	})

	t.Run("user message windows follow same visibility rules", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())
		wb.AppendOrUpdate("user-1", stream.TagTextUser, "  ") // whitespace only

		// User messages are delta windows (not tool windows), so they follow the same visibility rules
		if wb.Windows[0].Visible {
			t.Error("User message window should NOT be visible with only whitespace content")
		}

		// But should become visible when actual content is added
		wb.AppendOrUpdate("user-1", stream.TagTextUser, "Hello")
		if !wb.Windows[0].Visible {
			t.Error("User message window should be visible when it has non-whitespace content")
		}
	})
}
