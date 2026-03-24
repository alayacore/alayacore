package terminal

import (
	"strings"
	"testing"
)

func TestFoldIndicator(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Create a tool window with VERY long content that will definitely wrap to more than 5 lines
	// At 76 chars inner width, we need more than 380 characters to get 6+ lines
	longContent := strings.Repeat("This is a test sentence that will wrap. ", 12)
	wb.AppendToolCall("tool123", "test_tool", longContent)

	// Set to folded mode
	wb.Windows[0].Folded = true

	// Render the window
	rendered := wb.GetAll(-1)

	// Should contain the tricolon separator (full row)
	if !strings.Contains(rendered, "⁝") {
		t.Errorf("Expected fold indicator '⁝', got: %s", rendered)
	}

	// Count the tricolons - should have many (full row of ~76)
	tricolonCount := strings.Count(rendered, "⁝")
	if tricolonCount < 50 {
		t.Errorf("Expected many tricolons (full row), got %d", tricolonCount)
	}
}

func TestFoldIndicatorColor(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Create a diff window with many lines
	lines := make([]DiffLinePair, 20)
	for i := 0; i < 20; i++ {
		lines[i] = DiffLinePair{
			Old: "old line " + string(rune('0'+i%10)),
			New: "new line " + string(rune('0'+i%10)),
		}
	}
	wb.AppendDiff("diff123", "test.txt", lines)

	// Render the folded diff
	rendered := wb.GetAll(-1)

	// Should contain tricolon separator
	if !strings.Contains(rendered, "⁝") {
		t.Errorf("Expected tricolon separator in folded diff, got: %s", rendered)
	}

	// Verify it folds to fewer lines than the full diff
	renderedLines := strings.Split(rendered, "\n")
	if len(renderedLines) > 10 {
		t.Errorf("Folded diff should fold to ~7-8 lines, got %d", len(renderedLines))
	}
}
