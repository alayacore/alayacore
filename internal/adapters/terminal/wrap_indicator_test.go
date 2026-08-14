package terminal

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
)

func TestFoldIndicator(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Create a tool window with VERY long content that will definitely wrap
	// to more than 3 lines (the fold threshold) and produce a wide fold row
	// At 76 chars inner width, we need more than 380 characters to get 6+ lines
	longContent := strings.Repeat("This is a test sentence that will wrap. ", 12)
	wb.HandleToolInputEvent(protocol.ToolInputData{ID: "tool123", Name: "test_tool", Input: json.RawMessage(longContent)}, 0)

	// Set to folded mode
	wb.WindowAt(0).Folded = true

	// Render the window
	rendered := wb.GetAll(-1, false)

	// Should contain the horizontal rule separator (full row)
	if !strings.Contains(rendered, DefaultStyles().FoldIndicator) {
		t.Errorf("Expected fold indicator %q, got: %s", DefaultStyles().FoldIndicator, rendered)
	}

	// Count the indicators - should have many (full row of ~76)
	indicatorCount := strings.Count(rendered, DefaultStyles().FoldIndicator)
	if indicatorCount < 50 {
		t.Errorf("Expected many fold indicators (full row), got %d", indicatorCount)
	}
}

func TestFoldIndicatorColor(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Create a diff window with many lines
	var content strings.Builder
	content.WriteString("edit_file: test.txt\n")
	for i := 0; i < 20; i++ {
		content.WriteString("- old line ")
		content.WriteString(string(rune('0' + i%10)))
		content.WriteString("\n+ new line ")
		content.WriteString(string(rune('0' + i%10)))
		content.WriteString("\n")
	}
	wb.HandleToolInputEvent(protocol.ToolInputData{ID: "diff123", Name: "edit_file", Input: json.RawMessage(content.String())}, 0)

	// Render the folded diff
	rendered := wb.GetAll(-1, false)

	// Should contain horizontal rule separator
	if !strings.Contains(rendered, DefaultStyles().FoldIndicator) {
		t.Errorf("Expected fold indicator %q in folded diff, got: %s", DefaultStyles().FoldIndicator, rendered)
	}

	// Verify it folds to fewer lines than the full diff
	renderedLines := strings.Split(rendered, "\n")
	if len(renderedLines) > 10 {
		t.Errorf("Folded diff should fold to ~5 lines, got %d", len(renderedLines))
	}
}
