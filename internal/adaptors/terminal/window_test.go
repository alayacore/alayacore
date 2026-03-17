package terminal

import (
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/stream"
)

func TestWindowBuffer(t *testing.T) {
	t.Run("new buffer has correct width", func(t *testing.T) {
		wb := NewWindowBuffer(80)
		if wb.Width() != 80 {
			t.Errorf("Width() = %d, want 80", wb.Width())
		}
	})

	t.Run("set width updates width", func(t *testing.T) {
		wb := NewWindowBuffer(80)
		wb.SetWidth(120)
		if wb.Width() != 120 {
			t.Errorf("Width() = %d, want 120", wb.Width())
		}
	})

	t.Run("append creates new window", func(t *testing.T) {
		wb := NewWindowBuffer(80)
		wb.AppendOrUpdate("window-1", stream.TagTextAssistant, "Hello")

		if len(wb.Windows) != 1 {
			t.Fatalf("len(Windows) = %d, want 1", len(wb.Windows))
		}
		if wb.Windows[0].ID != "window-1" {
			t.Errorf("Windows[0].ID = %q, want %q", wb.Windows[0].ID, "window-1")
		}
	})

	t.Run("update appends to existing window", func(t *testing.T) {
		wb := NewWindowBuffer(80)
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
		wb := NewWindowBuffer(80)
		wb.AppendOrUpdate("window-1", stream.TagTextAssistant, "First")
		wb.AppendOrUpdate("window-2", stream.TagTextAssistant, "Second")
		wb.AppendOrUpdate("window-3", stream.TagTextAssistant, "Third")

		if len(wb.Windows) != 3 {
			t.Fatalf("len(Windows) = %d, want 3", len(wb.Windows))
		}
	})

	t.Run("get window count", func(t *testing.T) {
		wb := NewWindowBuffer(80)
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
		wb := NewWindowBuffer(80)
		content := wb.GetAll(-1)
		if content != "" {
			t.Errorf("GetAll() = %q, want empty", content)
		}
	})

	t.Run("get all returns content", func(t *testing.T) {
		wb := NewWindowBuffer(80)
		wb.AppendOrUpdate("window-1", stream.TagTextAssistant, "Hello")
		content := wb.GetAll(-1)
		if content == "" {
			t.Error("GetAll() should not be empty")
		}
	})

	t.Run("delete window", func(t *testing.T) {
		// DeleteWindow is not exposed on WindowBuffer
		// This test verifies the buffer structure is correct
		wb := NewWindowBuffer(80)
		wb.AppendOrUpdate("window-1", stream.TagTextAssistant, "First")
		wb.AppendOrUpdate("window-2", stream.TagTextAssistant, "Second")

		if len(wb.Windows) != 2 {
			t.Fatalf("len(Windows) = %d, want 2", len(wb.Windows))
		}
	})
}

func TestWindowBufferViewport(t *testing.T) {
	t.Run("set viewport position", func(_ *testing.T) {
		wb := NewWindowBuffer(80)
		wb.SetViewportPosition(10, 20)
		// Should not panic
	})

	t.Run("get total lines virtual", func(t *testing.T) {
		wb := NewWindowBuffer(80)
		wb.AppendOrUpdate("window-1", stream.TagTextAssistant, strings.Repeat("line\n", 10))
		// Should return total lines
		lines := wb.GetTotalLinesVirtual()
		if lines <= 0 {
			t.Errorf("GetTotalLinesVirtual() = %d, want > 0", lines)
		}
	})
}

func TestWindowBufferDiff(t *testing.T) {
	t.Run("append diff content", func(t *testing.T) {
		wb := NewWindowBuffer(80)
		// Diff windows are created differently, this tests the structure
		wb.AppendOrUpdate("diff-1", stream.TagFunctionShow, "diff content")

		if len(wb.Windows) != 1 {
			t.Fatalf("len(Windows) = %d, want 1", len(wb.Windows))
		}
	})

	t.Run("diff window folds when wrapped", func(t *testing.T) {
		wb := NewWindowBuffer(80)

		// Create a diff with many lines
		lines := make([]DiffLinePair, 20)
		for i := 0; i < 20; i++ {
			lines[i] = DiffLinePair{
				Old: string(rune('a' + i%26)),
				New: string(rune('b' + i%26)),
			}
		}

		wb.AppendDiff("diff-1", "test.txt", lines)

		// Verify window is wrapped by default
		if !wb.Windows[0].Wrapped {
			t.Error("Diff window should be wrapped by default")
		}

		// Render and check that it folds
		rendered := wb.GetAll(-1)
		renderedLines := strings.Split(rendered, "\n")

		// Should fold to ~5 lines of content (header + first + separator + last 3)
		// Plus border lines, so approximately 7-8 lines total
		if len(renderedLines) > 10 {
			t.Errorf("Rendered diff has %d lines, should be folded to ~7-8", len(renderedLines))
		}

		// Verify it contains the fold indicator
		hasIndicator := strings.Contains(rendered, "···") || strings.Contains(rendered, "·")
		if !hasIndicator {
			t.Error("Folded diff should contain dotted separator")
		}
	})

	t.Run("diff window expands when unwrapped", func(t *testing.T) {
		wb := NewWindowBuffer(80)

		// Create a diff with many lines
		lines := make([]DiffLinePair, 10)
		for i := 0; i < 10; i++ {
			lines[i] = DiffLinePair{
				Old: string(rune('a' + i%26)),
				New: string(rune('b' + i%26)),
			}
		}

		wb.AppendDiff("diff-1", "test.txt", lines)

		// Unwrap the window
		wb.ToggleWrap(0)

		// Render and check that it shows all lines
		rendered := wb.GetAll(-1)

		// Should show all 10 diff lines plus header
		// Count the diff separators (|) to count diff lines
		sepCount := strings.Count(rendered, "|")
		if sepCount != 10 {
			t.Errorf("Unwrapped diff should show 10 lines, found %d separators", sepCount)
		}
	})
}
