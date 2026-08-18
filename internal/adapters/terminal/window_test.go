package terminal

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
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
		wb.WithWidth(120)
		if wb.Width() != 120 {
			t.Errorf("Width() = %d, want 120", wb.Width())
		}
	})

	t.Run("append creates new window", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())
		wb.AppendOrUpdate(tlv.TagAssistantT, "window-1", "Hello")

		if wb.WindowCount() != 1 {
			t.Fatalf("len(Windows) = %d, want 1", wb.WindowCount())
		}
		if wb.WindowAt(0).ID != "window-1" {
			t.Errorf("Windows[0].ID = %q, want %q", wb.WindowAt(0).ID, "window-1")
		}
	})

	t.Run("update appends to existing window", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())
		wb.AppendOrUpdate(tlv.TagAssistantT, "window-1", "Hello")
		wb.AppendOrUpdate(tlv.TagAssistantT, "window-1", " World")

		if wb.WindowCount() != 1 {
			t.Fatalf("len(Windows) = %d, want 1", wb.WindowCount())
		}
		if !strings.Contains(wb.WindowAt(0).RawContent(), "Hello") {
			t.Error("Content should contain 'Hello'")
		}
		if !strings.Contains(wb.WindowAt(0).RawContent(), "World") {
			t.Error("Content should contain 'World'")
		}
	})

	t.Run("multiple windows", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())
		wb.AppendOrUpdate(tlv.TagAssistantT, "window-1", "First")
		wb.AppendOrUpdate(tlv.TagAssistantT, "window-2", "Second")
		wb.AppendOrUpdate(tlv.TagAssistantT, "window-3", "Third")

		if wb.WindowCount() != 3 {
			t.Fatalf("len(Windows) = %d, want 3", wb.WindowCount())
		}
	})

	t.Run("get window count", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())
		if wb.WindowCount() != 0 {
			t.Errorf("WindowCount() = %d, want 0", wb.WindowCount())
		}

		wb.AppendOrUpdate(tlv.TagAssistantT, "window-1", "First")
		if wb.WindowCount() != 1 {
			t.Errorf("WindowCount() = %d, want 1", wb.WindowCount())
		}

		wb.AppendOrUpdate(tlv.TagAssistantT, "window-2", "Second")
		if wb.WindowCount() != 2 {
			t.Errorf("WindowCount() = %d, want 2", wb.WindowCount())
		}
	})

	t.Run("get all returns empty for empty buffer", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())
		content := wb.GetAll(-1, false)
		if content != "" {
			t.Errorf("GetAll() = %q, want empty", content)
		}
	})

	t.Run("get all returns content", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())
		wb.AppendOrUpdate(tlv.TagAssistantT, "window-1", "Hello")
		content := wb.GetAll(-1, false)
		if content == "" {
			t.Error("GetAll() should not be empty")
		}
	})

	t.Run("delete window", func(t *testing.T) {
		// DeleteWindow is not exposed on WindowBuffer
		// This test verifies the buffer structure is correct
		wb := NewWindowBuffer(80, DefaultStyles())
		wb.AppendOrUpdate(tlv.TagAssistantT, "window-1", "First")
		wb.AppendOrUpdate(tlv.TagAssistantT, "window-2", "Second")

		if wb.WindowCount() != 2 {
			t.Fatalf("len(Windows) = %d, want 2", wb.WindowCount())
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
		wb.AppendOrUpdate(tlv.TagAssistantT, "window-1", strings.Repeat("line\n", 10))
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
		wb.HandleToolInputEvent(protocol.ToolInputData{ID: "diff-1", Name: "edit_file", Input: json.RawMessage(content)}, 0)

		if wb.WindowCount() != 1 {
			t.Fatalf("len(Windows) = %d, want 1", wb.WindowCount())
		}
		if wb.WindowAt(0).RawToolName() != "edit_file" {
			t.Errorf("ToolName = %s, want edit_file", wb.WindowAt(0).RawToolName())
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

		wb.HandleToolInputEvent(protocol.ToolInputData{ID: "diff-1", Name: "edit_file", Input: json.RawMessage(content.String())}, 0)

		// Verify window is folded by default
		if !wb.WindowAt(0).Folded {
			t.Error("Diff window should be folded by default")
		}

		// Render and check that it folds
		rendered := wb.GetAll(-1, false)
		renderedLines := strings.Split(rendered, "\n")

		// New collapsed design: folded windows are a single header line
		// (collapse arrow + "TOOL indicator edit_file …") with no border
		// and no fold-indicator row.
		if len(renderedLines) != 1 {
			t.Errorf("Rendered diff has %d lines, should collapse to 1", len(renderedLines))
		}
		if strings.Contains(rendered, "⁝") {
			t.Error("Collapsed window should not contain the old fold indicator")
		}
		if !strings.Contains(stripANSI(rendered), "TOOL") || !strings.Contains(stripANSI(rendered), "edit_file") {
			t.Errorf("Collapsed diff should show TOOL + tool name, got %q", stripANSI(rendered))
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

		wb.HandleToolInputEvent(protocol.ToolInputData{ID: "diff-1", Name: "edit_file", Input: json.RawMessage(content.String())}, 0)

		// Unfold the window
		wb.ToggleFold(0)

		// Render and check that it shows all lines
		rendered := wb.GetAll(-1, false)

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

		wb.HandleToolInputEvent(protocol.ToolInputData{ID: "diff-1", Name: "edit_file", Input: json.RawMessage(content)}, 0)

		// Unfold to see all lines
		wb.ToggleFold(0)

		rendered := wb.GetAll(-1, false)

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

		wb.HandleToolInputEvent(protocol.ToolInputData{ID: "diff-1", Name: "edit_file", Input: json.RawMessage(content.String())}, 0)

		// First render - should be folded (Folded=true)
		rendered1 := wb.GetAll(-1, false)
		removeCount1 := strings.Count(rendered1, "- ")
		if removeCount1 >= 10 {
			t.Errorf("Folded diff should fold lines, found %d - prefixes", removeCount1)
		}

		// Toggle fold
		wb.ToggleFold(0)

		// Second render - should be expanded (Folded=false)
		rendered2 := wb.GetAll(-1, false)
		removeCount2 := strings.Count(rendered2, "- ")
		if removeCount2 != 20 {
			t.Errorf("Unfolded diff should show all 20 lines with - prefix, found %d", removeCount2)
		}

		// Toggle back
		wb.ToggleFold(0)

		// Third render - should be folded again
		rendered3 := wb.GetAll(-1, false)
		removeCount3 := strings.Count(rendered3, "- ")
		if removeCount3 >= 10 {
			t.Errorf("Re-folded diff should fold lines again, found %d - prefixes", removeCount3)
		}
	})

	t.Run("user and assistant messages fold defaults", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())

		// Create windows for different tag types
		wb.AppendOrUpdate(tlv.TagUserT, "user-1", "User message")
		wb.AppendOrUpdate(tlv.TagAssistantT, "assistant-1", "Assistant message")
		wb.HandleToolInputEvent(protocol.ToolInputData{ID: "tool-1", Name: "test_tool", Input: json.RawMessage("Tool output")}, 0)
		wb.AppendOrUpdate(tlv.TagAssistantR, "reasoning-1", "Reasoning content")

		// User text is collapsed by default (like tools/reasoning).
		if !wb.WindowAt(0).Folded {
			t.Error("User window should be folded by default")
		}
		// Assistant text is the only window type expanded by default.
		if wb.WindowAt(1).Folded {
			t.Error("Assistant window should NOT be folded by default")
		}

		// Tool window should be folded
		if !wb.WindowAt(2).Folded {
			t.Error("Tool window should be folded by default")
		}
		// Reasoning should be folded
		if !wb.WindowAt(3).Folded {
			t.Error("Reasoning window should be folded by default")
		}
	})
}

func TestWindowBufferVisibility(t *testing.T) {
	t.Run("tool windows are always visible", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())
		wb.HandleToolInputEvent(protocol.ToolInputData{ID: "tool-1", Name: "execute_command", Input: json.RawMessage("")}, 0)

		if wb.WindowCount() != 1 {
			t.Fatalf("len(Windows) = %d, want 1", wb.WindowCount())
		}
		if !wb.WindowAt(0).Visible {
			t.Error("Tool window should always be visible, even with empty content")
		}
	})

	t.Run("delta window with content is visible", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())
		wb.AppendOrUpdate(tlv.TagAssistantT, "delta-1", "Hello")

		if wb.WindowCount() != 1 {
			t.Fatalf("len(Windows) = %d, want 1", wb.WindowCount())
		}
		if !wb.WindowAt(0).Visible {
			t.Error("Delta window with non-whitespace content should be visible")
		}
	})

	t.Run("delta window with only whitespace is not visible", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())
		wb.AppendOrUpdate(tlv.TagAssistantT, "delta-1", "   \n\t  ")

		if wb.WindowCount() != 1 {
			t.Fatalf("len(Windows) = %d, want 1", wb.WindowCount())
		}
		if wb.WindowAt(0).Visible {
			t.Error("Delta window with only whitespace should NOT be visible")
		}
	})

	t.Run("delta window with empty content is not visible", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())
		wb.AppendOrUpdate(tlv.TagAssistantT, "delta-1", "")

		if wb.WindowCount() != 1 {
			t.Fatalf("len(Windows) = %d, want 1", wb.WindowCount())
		}
		if wb.WindowAt(0).Visible {
			t.Error("Delta window with empty content should NOT be visible")
		}
	})

	t.Run("delta window becomes visible when non-whitespace added", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())

		// Start with whitespace-only content
		wb.AppendOrUpdate(tlv.TagAssistantT, "delta-1", "\n\n")
		if wb.WindowAt(0).Visible {
			t.Error("Delta window with only newlines should NOT be visible initially")
		}

		// Add actual content
		wb.AppendOrUpdate(tlv.TagAssistantT, "delta-1", "Hello")
		if !wb.WindowAt(0).Visible {
			t.Error("Delta window should become visible when non-whitespace content is added")
		}
	})

	t.Run("whitespace before content preserves visibility", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())

		// Start with whitespace
		wb.AppendOrUpdate(tlv.TagAssistantT, "delta-1", "  \n  ")
		if wb.WindowAt(0).Visible {
			t.Error("Delta window with only whitespace should NOT be visible")
		}

		// Add more whitespace - still not visible
		wb.AppendOrUpdate(tlv.TagAssistantT, "delta-1", "\t")
		if wb.WindowAt(0).Visible {
			t.Error("Delta window should still not be visible with only whitespace")
		}

		// Add actual content
		wb.AppendOrUpdate(tlv.TagAssistantT, "delta-1", "World")
		if !wb.WindowAt(0).Visible {
			t.Error("Delta window should be visible after adding non-whitespace")
		}

		// Content should include all the whitespace
		expected := "  \n  \tWorld"
		if wb.WindowAt(0).RawContent() != expected {
			t.Errorf("Content = %q, want %q", wb.WindowAt(0).RawContent(), expected)
		}
	})

	t.Run("non-visible windows are not rendered", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())

		// Create visible and non-visible windows
		wb.AppendOrUpdate(tlv.TagAssistantT, "delta-1", "\n\n")  // Not visible
		wb.AppendOrUpdate(tlv.TagAssistantT, "delta-2", "Hello") // Visible
		wb.AppendOrUpdate(tlv.TagAssistantT, "delta-3", "  ")    // Not visible
		wb.AppendOrUpdate(tlv.TagAssistantT, "delta-4", "World") // Visible

		rendered := wb.GetAll(-1, false)

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
		wb.AppendOrUpdate(tlv.TagAssistantT, "delta-1", "Hello\nWorld") // Visible, 2 lines
		wb.AppendOrUpdate(tlv.TagAssistantT, "delta-2", "\n\n\n")       // Not visible

		lines := wb.GetTotalLines()
		// Only delta-1 contributes to total lines (plus border = ~4 lines)
		if lines <= 0 {
			t.Errorf("GetTotalLines() = %d, should be > 0 (only visible windows)", lines)
		}

		// The invisible window should have 0 line height
		wb.ensureLineHeights(false)
		if wb.lineHeights[1] != 0 {
			t.Errorf("Invisible window lineHeight = %d, want 0", wb.lineHeights[1])
		}
	})

	t.Run("user message windows follow same visibility rules", func(t *testing.T) {
		wb := NewWindowBuffer(80, DefaultStyles())
		wb.AppendOrUpdate(tlv.TagUserT, "user-1", "  ") // whitespace only

		// User messages are delta windows (not tool windows), so they follow the same visibility rules
		if wb.WindowAt(0).Visible {
			t.Error("User message window should NOT be visible with only whitespace content")
		}

		// But should become visible when actual content is added
		wb.AppendOrUpdate(tlv.TagUserT, "user-1", "Hello")
		if !wb.WindowAt(0).Visible {
			t.Error("User message window should be visible when it has non-whitespace content")
		}
	})
}
