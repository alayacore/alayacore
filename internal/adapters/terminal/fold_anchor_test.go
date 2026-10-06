package terminal

// Tests for the fold/unfold anchor (DisplayModel.anchorCursorWindow): toggling
// a window with Space must not move the line under the cursor.
//
// The case that used to jump: a window taller than the viewport whose own line
// has scrolled above the top is drawn pinned to screen row 0 (see
// sticky_line_test.go); collapsing it leaves that single row entirely above the
// viewport, and EnsureCursorVisible then dragged the viewport to its bottom
// edge. The anchor pulls the offset up to the window's own line instead, so the
// header folds to the row the pin occupied — the rule is that a cursor window
// scrolled above the top returns to the top. Folding while the line is already
// visible, and unfolding, must leave the offset untouched. Auto-follow keeps
// its own contract (the live edge wins).

import (
	"fmt"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/tlv"
)

const (
	anchorLeads    = 10 // short user prompts before the tall message
	anchorViewport = 6  // viewport height these tests read at
)

// foldAnchorFixture builds anchorLeads short user prompts, a tall expanded
// assistant message (40 body rows), then `trails` short user prompts. The tall
// window sits at index anchorLeads; trailing content keeps the document long
// below it so its header can be scrolled to the screen top.
func foldAnchorFixture(t *testing.T, trails int) (*WindowBuffer, int) {
	t.Helper()
	wb := NewWindowBuffer(80, DefaultStyles())
	for i := 0; i < anchorLeads; i++ {
		wb.AppendOrUpdate(tlv.TagUserT, fmt.Sprintf("lead%d", i), "lead prompt")
	}
	var body strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&body, "row%02d\n", i)
	}
	wb.AppendOrUpdate(tlv.TagAssistantT, "tall", strings.TrimSuffix(body.String(), "\n"))
	for i := 0; i < trails; i++ {
		wb.AppendOrUpdate(tlv.TagUserT, fmt.Sprintf("trail%d", i), "trail prompt")
	}
	_ = wb.GetTotalLines()
	return wb, anchorLeads
}

// scrolledInto focuses the display on window idx and scrolls `lines` rows into
// the document (auto-follow off) — the state of a reader mid-message.
func scrolledInto(t *testing.T, wb *WindowBuffer, idx, lines int) DisplayModel {
	t.Helper()
	dm := NewDisplayModel(wb, DefaultStyles()).WithHeight(anchorViewport).WithDisplayFocused(true)
	dm = dm.WithWindowCursor(idx)
	dm = dm.MarkUserScrolled()
	dm = dm.updateContent()
	if lines > 0 {
		dm = dm.ScrollDown(lines)
		dm = dm.updateContent()
	}
	return dm
}

func topRow(dm DisplayModel) string {
	return firstRow(stripANSI(dm.View().Content))
}

func pressSpace(dm DisplayModel) DisplayModel {
	dm, _ = dm.Update(KeyPressMsg(Key{Code: KeySpace}))
	return dm
}

// TestFoldWhilePinnedKeepsTheHeaderAtTheTop: a window whose own line is pinned
// to the screen top folds to that same row, not to the viewport's bottom edge.
func TestFoldWhilePinnedKeepsTheHeaderAtTheTop(t *testing.T) {
	wb, idx := foldAnchorFixture(t, 10)
	dm := scrolledInto(t, wb, idx, 25)

	start, _ := wb.GetWindowLineRange(idx)
	if dm.YOffset() <= start {
		t.Fatalf("fixture: yOffset=%d must be below the window start %d for the pin to show", dm.YOffset(), start)
	}
	if row := topRow(dm); !strings.HasPrefix(row, unfoldArrow+" ASSISTANT") {
		t.Fatalf("fixture: expected the pinned ASSISTANT line at the top, got %q", row)
	}

	dm = pressSpace(dm)

	startAfter, _ := wb.GetWindowLineRange(idx)
	if got := startAfter - dm.YOffset(); got != 0 {
		t.Errorf("folded header is at screen row %d, want 0 (where the pinned row was); yOffset=%d start=%d",
			got, dm.YOffset(), startAfter)
	}
	if row := topRow(dm); !strings.HasPrefix(row, foldArrow+" ASSISTANT") {
		t.Errorf("top row after fold = %q, want the folded ASSISTANT line", row)
	}
}

// TestFoldWhileScrolledPastBringsTheHeaderToTheTop: the rule is that a cursor
// window scrolled above the top returns to the top — and it holds even when the
// window is entirely above the viewport (not pinned), where folding used to
// re-seat to the bottom edge instead.
func TestFoldWhileScrolledPastBringsTheHeaderToTheTop(t *testing.T) {
	wb, idx := foldAnchorFixture(t, 10)
	dm := scrolledInto(t, wb, idx, 61) // past the whole tall window

	start, end := wb.GetWindowLineRange(idx)
	if end > dm.YOffset() {
		t.Fatalf("fixture: window [%d,%d) is not entirely above yOffset=%d", start, end, dm.YOffset())
	}

	dm = pressSpace(dm)

	startAfter, _ := wb.GetWindowLineRange(idx)
	if got := startAfter - dm.YOffset(); got != 0 {
		t.Errorf("folded header is at screen row %d, want 0; yOffset=%d start=%d", got, dm.YOffset(), startAfter)
	}
}

// TestFoldWhileVisibleDoesNotMoveTheViewport: when the own line is already on
// screen at its natural row, folding leaves the offset (and that row) alone.
func TestFoldWhileVisibleDoesNotMoveTheViewport(t *testing.T) {
	wb, idx := foldAnchorFixture(t, 10)
	dm := scrolledInto(t, wb, idx, 18) // own line (doc 20) at screen row 2

	start, _ := wb.GetWindowLineRange(idx)
	if start <= dm.YOffset() || start >= dm.YOffset()+dm.GetHeight() {
		t.Fatalf("fixture: own line not visible (start=%d yOffset=%d h=%d)", start, dm.YOffset(), dm.GetHeight())
	}
	rowBefore, offsetBefore := start-dm.YOffset(), dm.YOffset()

	dm = pressSpace(dm)

	startAfter, _ := wb.GetWindowLineRange(idx)
	if dm.YOffset() != offsetBefore {
		t.Errorf("fold moved the viewport: yOffset %d → %d, want unchanged", offsetBefore, dm.YOffset())
	}
	if got := startAfter - dm.YOffset(); got != rowBefore {
		t.Errorf("header moved from screen row %d to %d on fold, want unchanged", rowBefore, got)
	}
}

// TestUnfoldDoesNotMoveTheViewport is the reverse: expanding a folded window
// whose line is on screen leaves it where it is.
func TestUnfoldDoesNotMoveTheViewport(t *testing.T) {
	wb, idx := foldAnchorFixture(t, 10)
	dm := scrolledInto(t, wb, idx, 18)

	dm = pressSpace(dm) // fold
	startFolded, _ := wb.GetWindowLineRange(idx)
	offsetFolded := dm.YOffset()
	rowFolded := startFolded - offsetFolded

	dm = pressSpace(dm) // unfold

	startAfter, _ := wb.GetWindowLineRange(idx)
	if dm.YOffset() != offsetFolded {
		t.Errorf("unfold moved the viewport: yOffset %d → %d, want unchanged", offsetFolded, dm.YOffset())
	}
	if got := startAfter - dm.YOffset(); got != rowFolded {
		t.Errorf("header moved from screen row %d to %d on unfold, want unchanged", rowFolded, got)
	}
}

// TestFoldNearTranscriptEndClampsToTheBottom: with less than a screen of content
// below the message, the shrunken document clamps the offset, so the header
// cannot reach row 0 — but it must still be on screen, and the viewport must sit
// at the document bottom.
func TestFoldNearTranscriptEndClampsToTheBottom(t *testing.T) {
	wb, idx := foldAnchorFixture(t, 1)
	dm := scrolledInto(t, wb, idx, 25)
	dm = pressSpace(dm)

	if want := max(0, wb.GetTotalLines()-dm.GetHeight()); dm.YOffset() != want {
		t.Errorf("yOffset = %d, want %d (clamped to the document bottom)", dm.YOffset(), want)
	}
	if got := stripANSI(dm.View().Content); !strings.Contains(got, foldArrow+" ASSISTANT") {
		t.Errorf("the folded header must still be on screen:\n%s", got)
	}
}

// TestFoldUnderAutoFollowKeepsTheLiveEdge: under auto-follow the viewport tracks
// the newest content, so folding the tall last window re-seats to the new
// bottom rather than anchoring — the anchor must not fight auto-follow.
func TestFoldUnderAutoFollowKeepsTheLiveEdge(t *testing.T) {
	wb, _ := foldAnchorFixture(t, 0)
	dm := NewDisplayModel(wb, DefaultStyles()).WithHeight(anchorViewport).WithDisplayFocused(true)
	dm = dm.WithCursorToLastWindow() // cursor on the tall last window + auto-follow on
	dm = dm.updateContent()

	if row := topRow(dm); !strings.HasPrefix(row, unfoldArrow+" ASSISTANT") {
		t.Fatalf("fixture: expected the pinned ASSISTANT line at the top, got %q", row)
	}

	dm = pressSpace(dm)

	if want := max(0, wb.GetTotalLines()-dm.GetHeight()); dm.YOffset() != want {
		t.Errorf("under auto-follow, yOffset = %d, want %d (the live edge)", dm.YOffset(), want)
	}
}

// TestMarkdownToggleWhilePinnedKeepsTheHeaderAtTheTop: 'r' shares the anchor.
// Markdown pads table cells, so toggling it can shrink a pinned table below the
// viewport top the same way a fold collapses a message — the header must stay
// on row 0 rather than being re-seated to the offset's bottom edge.
func TestMarkdownToggleWhilePinnedKeepsTheHeaderAtTheTop(t *testing.T) {
	var table strings.Builder
	table.WriteString("| name | gender | age |\n|---|---|---|\n")
	for i := 0; i < 2; i++ {
		fmt.Fprintf(&table, "| Walllace%d | male | %d |\n", i, 100+i)
	}

	wb := NewWindowBuffer(24, DefaultStyles())
	wb.AppendOrUpdate(tlv.TagUserT, "lead", "lead")
	wb.AppendOrUpdate(tlv.TagAssistantT, "table", table.String())
	for i := 0; i < 5; i++ {
		wb.AppendOrUpdate(tlv.TagUserT, fmt.Sprintf("trail%d", i), "trail")
	}
	_ = wb.GetTotalLines()

	// This viewport/scroll makes the taller markdown rendering pin the table's
	// own line; the raw rendering is short enough to fall above the top.
	dm := NewDisplayModel(wb, DefaultStyles()).WithHeight(10).WithDisplayFocused(true)
	dm = dm.WithWindowCursor(1) // the table window
	dm = dm.MarkUserScrolled()
	dm = dm.updateContent()
	dm = dm.ScrollDown(12)
	dm = dm.updateContent()

	start, _ := wb.GetWindowLineRange(1)
	if dm.YOffset() <= start {
		t.Fatalf("fixture: yOffset=%d must be below the window start %d for the pin to show", dm.YOffset(), start)
	}
	if row := topRow(dm); !strings.HasPrefix(row, unfoldArrow+" ASSISTANT") {
		t.Fatalf("fixture: expected the pinned ASSISTANT line at the top, got %q", row)
	}

	dm, _ = dm.Update(KeyPressMsg(Key{Code: 'r'}))

	startAfter, _ := wb.GetWindowLineRange(1)
	if got := startAfter - dm.YOffset(); got != 0 {
		t.Errorf("after 'r' the header is at screen row %d, want 0; yOffset=%d start=%d",
			got, dm.YOffset(), startAfter)
	}
	if row := topRow(dm); !strings.HasPrefix(row, unfoldArrow+" ASSISTANT") {
		t.Errorf("top row after 'r' = %q, want the ASSISTANT line", row)
	}
}
