package terminal

// Benchmarks for the folded-window redesign.
//
// These simulate a realistic long agent session like the one in the
// user's screenshot: the majority of windows are folded tool calls
// (AF) and reasoning (AR) collapsed to a single header line, with a
// handful of unfolded user (UT) / assistant (AT) messages.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/tlv"
)

// makeFoldedSession builds a WindowBuffer resembling a long agent session:
//   - 80 folded tool windows (AF) with realistic input + output
//   - 20 folded reasoning windows (AR)
//   - 10 unfolded assistant windows (AT)
//   - 10 unfolded user windows (UT)
//
// Total 120 windows, 110 folded. Width 120 like a wide terminal.
func makeFoldedSession() *WindowBuffer {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(120, styles)

	// Folded tool windows: input line + long-ish output (command results).
	for i := 0; i < 80; i++ {
		id := fmt.Sprintf("call-%03d", i)
		input := fmt.Sprintf("execute_command: grep -rn \"fontWeight\\|font-weight\" src-elm/src src-elm/*.js 2>/dev/null | grep -v style.css | head -20 (step %d)", i)
		wb.HandleToolInputEvent(protocol.ToolInputData{
			ID:    id,
			Name:  "execute_command",
			Input: json.RawMessage(input),
		}, uint64(i))
		// Command output — a few lines.
		output := strings.Repeat("  some output line from command execution\n", 4)
		wb.HandleToolOutput(id, output, false, uint64(i))
	}

	// Folded reasoning windows.
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("reason-%03d", i)
		content := strings.Repeat("reasoning about the task and planning the next steps\n", 3)
		wb.AppendOrUpdate(tlv.TagAssistantR, id, content)
	}

	// Unfolded assistant messages.
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("assist-%03d", i)
		content := strings.Repeat("All edits are in place. Let me do a final sanity check — brace balance, light-mode conflicts.\n", 5)
		wb.AppendOrUpdate(tlv.TagAssistantT, id, content)
	}

	// Unfolded user messages.
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("user-%03d", i)
		wb.AppendOrUpdate(tlv.TagUserT, id, fmt.Sprintf("Please fix issue #%d in the style sheet", i))
	}

	return wb
}

// BenchmarkFoldedSessionGetAll renders the whole visible viewport of a
// folded-heavy session. This is what runs on every display refresh.
func BenchmarkFoldedSessionGetAll(b *testing.B) {
	wb := makeFoldedSession()
	wb.SetViewportPosition(0, 40)
	_ = wb.GetTotalLines()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = wb.GetAll(-1, false)
	}
}

// BenchmarkFoldedSessionCursorMovement moves the window cursor through the
// session (20 moves per iteration), like pressing j/k repeatedly. Includes
// the EnsureCursorVisible + updateContent path.
func BenchmarkFoldedSessionCursorMovement(b *testing.B) {
	wb := makeFoldedSession()
	wb.SetViewportPosition(0, 40)
	_ = wb.GetTotalLines()

	dm := NewDisplayModel(wb, NewStyles(theme.DefaultTheme()))
	dm = dm.WithHeight(40)
	dm = dm.WithWidth(120)
	dm = dm.WithDisplayFocused(true)
	dm = dm.updateContent()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 0; j < 20; j++ {
			dm = dm.WithWindowCursor(j)
			dm = dm.EnsureCursorVisible()
			dm = dm.updateContent()
		}
	}
}

// BenchmarkFoldedToolStreamingDelta simulates the streaming hot path:
// a tool call is running, its window is folded, and Uf preview snapshots
// keep arriving every tick. Each arrival marks the window dirty and
// triggers line-height recomputation.
func BenchmarkFoldedToolStreamingDelta(b *testing.B) {
	wb := makeFoldedSession()
	wb.SetViewportPosition(0, 40)
	_ = wb.GetTotalLines()

	// The currently-streaming tool window.
	wb.HandleToolInputEvent(protocol.ToolInputData{
		ID:   "call-live",
		Name: "write_file",
	}, 0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wb.HandleToolOutputDelta("call-live", fmt.Sprintf(" 42%% [████████░░░░░░░░] chunk %d", i), 0)
		_ = wb.GetTotalLines()
	}
}
