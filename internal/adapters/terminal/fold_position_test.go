package terminal

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// TestHMLPositioningWithFoldedWindows verifies that H (top), M (center) and
// L (bottom) positioning account for the folded/unfolded state of windows:
// a folded window occupies exactly 1 line (collapse arrow header), an
// expanded window occupies header + rules + content lines. Folding a window
// must shift the visual rows of everything below it, so M's target changes
// accordingly.
func TestHMLPositioningWithFoldedWindows(t *testing.T) {
	styles := DefaultStyles()
	wb := NewWindowBuffer(80, styles)

	// Window 0: folded tool (default) — 1 line.
	wb.HandleToolInputEvent(protocol.ToolInputData{ID: "t0", Name: "read_file", Input: json.RawMessage("read_file: /a.txt")}, 0)
	wb.HandleToolOutput("t0", "ok", false, 0)

	// Window 1: expanded assistant (default unfolded) — 3 content lines →
	// header + top rule + 3 + bottom rule = 6 lines.
	wb.AppendOrUpdate(tlv.TagAssistantT, "a1", "line one\nline two\nline three")

	// Window 2: folded tool — 1 line.
	wb.HandleToolInputEvent(protocol.ToolInputData{ID: "t2", Name: "grep", Input: json.RawMessage("grep: foo")}, 2)
	wb.HandleToolOutput("t2", "ok", false, 2)

	// Window 3: folded tool — 1 line.
	wb.HandleToolInputEvent(protocol.ToolInputData{ID: "t3", Name: "cat", Input: json.RawMessage("cat: /b.txt")}, 3)
	wb.HandleToolOutput("t3", "ok", false, 3)

	// Viewport of 4 rows: only the top part of the session is visible.
	dm := NewDisplayModel(wb, styles)
	dm = dm.WithHeight(4).WithWidth(80).WithDisplayFocused(true)
	dm = dm.MarkUserScrolled() // H/M/L require auto-follow off
	dm = dm.updateContent()

	// Row layout while a1 is expanded (9 total rows):
	//   0: collapse-arrow TOOL read_file
	//   1: expand-arrow ASSISTANT
	//   2: ─────────────
	//   3-5: content
	//   6: ─────────────
	//   7: collapse-arrow TOOL grep
	//   8: collapse-arrow TOOL cat
	// Viewport [0,4): rows 0,1,2,3.

	// H: top visible window is window 0.
	dm, _ = dm.MoveWindowCursorToTop()
	if got := dm.GetWindowCursor(); got != 0 {
		t.Errorf("H with a1 expanded: cursor = %d, want 0", got)
	}

	// M: viewport center row = 4/2 = 2 → inside window 1 (rows 1-6).
	dm, _ = dm.MoveWindowCursorToCenter()
	if got := dm.GetWindowCursor(); got != 1 {
		t.Errorf("M with a1 expanded: cursor = %d, want 1", got)
	}

	// L: bottommost visible window in [0,4) is window 1 (its rows 1-6
	// overlap the viewport; windows 2/3 start at rows 7/8, below).
	dm, _ = dm.MoveWindowCursorToBottom()
	if got := dm.GetWindowCursor(); got != 1 {
		t.Errorf("L with a1 expanded: cursor = %d, want 1", got)
	}

	// Fold window 1: total rows shrink to 4 (all windows 1 line each).
	wb.ToggleFold(1)
	dm = dm.updateContent()

	// M: center row = 4/2 = 2 → now inside window 2 (rows 2-3).
	dm, _ = dm.MoveWindowCursorToCenter()
	if got := dm.GetWindowCursor(); got != 2 {
		t.Errorf("M with a1 folded: cursor = %d, want 2", got)
	}

	// L: bottommost visible window is now window 3.
	dm, _ = dm.MoveWindowCursorToBottom()
	if got := dm.GetWindowCursor(); got != 3 {
		t.Errorf("L with a1 folded: cursor = %d, want 3", got)
	}
}

// TestHMLLineRangeConsistency verifies that after every fold toggle the
// line ranges used by H/M/L (cumulative over visible windows) stay
// consistent, and that H/M/L always land on a visible window.
func TestHMLLineRangeConsistency(t *testing.T) {
	styles := DefaultStyles()
	wb := NewWindowBuffer(80, styles)

	// Mixed session: folded tools + expanded assistant + folded reasoning.
	wb.HandleToolInputEvent(protocol.ToolInputData{ID: "t0", Name: "edit_file", Input: json.RawMessage("edit_file: /x.css")}, 0)
	wb.HandleToolOutput("t0", "done", false, 0)
	wb.AppendOrUpdate(tlv.TagAssistantT, "a1", "multi\nline\nassistant\nreply")
	wb.AppendOrUpdate(tlv.TagAssistantR, "r2", "reasoning text that should be folded")
	wb.HandleToolInputEvent(protocol.ToolInputData{ID: "t3", Name: "execute_command", Input: json.RawMessage("execute_command: ls")}, 3)
	wb.HandleToolOutput("t3", "ok", false, 3)

	_ = wb.GetTotalLines()

	dm := NewDisplayModel(wb, styles)
	dm = dm.WithHeight(30).WithWidth(80).WithDisplayFocused(true)
	dm = dm.MarkUserScrolled()
	dm = dm.updateContent()

	// After every fold toggle: (1) line ranges must be cumulative and
	// positive, (2) H/M/L must land on a visible window.
	for _, foldIdx := range []int{1, 0, 2, 3} {
		wb.ToggleFold(foldIdx)
		dm = dm.updateContent()

		prevEnd := 0
		visited := 0
		for i := 0; i < wb.WindowCount(); i++ {
			w := wb.WindowAt(i)
			if w == nil || !w.Visible {
				continue
			}
			start, end := wb.GetWindowLineRange(i)
			if start != prevEnd {
				t.Errorf("fold toggle %d: window %d starts at %d, want %d (cumulative)", foldIdx, i, start, prevEnd)
			}
			prevEnd = end
			if end <= start {
				t.Errorf("fold toggle %d: window %d has non-positive height [%d,%d)", foldIdx, i, start, end)
			}
			visited++
		}
		if visited != wb.GetVisibleWindowCount() {
			t.Errorf("fold toggle %d: visited %d visible windows, want %d", foldIdx, visited, wb.GetVisibleWindowCount())
		}

		// H/M/L must land on a visible window after every toggle (they may
		// legitimately not "move" if the cursor is already on the target).
		for name, fn := range map[string]func() (DisplayModel, bool){
			"H": dm.MoveWindowCursorToTop,
			"M": dm.MoveWindowCursorToCenter,
			"L": dm.MoveWindowCursorToBottom,
		} {
			after, _ := fn()
			idx := after.GetWindowCursor()
			if idx < 0 || !wb.WindowAt(idx).Visible {
				t.Errorf("fold toggle %d: %s landed on invalid window %d", foldIdx, name, idx)
			}
			dm = after
		}
	}
}

// TestHMLFoldedScrollClamp verifies that folding all windows shrinks the
// total height and the viewport offset is clamped so H/M/L still work.
func TestHMLFoldedScrollClamp(t *testing.T) {
	styles := DefaultStyles()
	wb := NewWindowBuffer(80, styles)

	// Many expanded windows, scrolled to the bottom.
	for i := 0; i < 5; i++ {
		wb.AppendOrUpdate(tlv.TagAssistantT, fmt.Sprintf("a%d", i), "some\ncontent\nlines")
	}
	_ = wb.GetTotalLines()

	dm := NewDisplayModel(wb, styles)
	dm = dm.WithHeight(4).WithWidth(80).WithDisplayFocused(true)
	dm = dm.MarkUserScrolled()
	dm = dm.updateContent()
	dm = dm.GotoBottom()

	// Fold every window: 5×5=25 lines → 5 lines, viewport 4 → offset clamped.
	for i := 0; i < 5; i++ {
		wb.ToggleFold(i)
	}
	dm = dm.updateContent()

	if y := dm.YOffset(); y > 1 {
		t.Errorf("after folding all windows, YOffset = %d, want <= 1 (5 lines, viewport 4)", y)
	}
	dm, moved := dm.MoveWindowCursorToBottom()
	if !moved || dm.GetWindowCursor() != 4 {
		t.Errorf("L after folding all: cursor = %d (moved=%v), want 4", dm.GetWindowCursor(), moved)
	}
}
