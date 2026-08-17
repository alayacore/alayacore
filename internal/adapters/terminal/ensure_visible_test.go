package terminal

import (
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/tlv"
)

// TestEnsureCursorVisible_OversizedWindowDoesNotOscillate verifies that
// repeatedly calling EnsureCursorVisible on a window taller than the
// viewport does not cause the YOffset to oscillate between two positions.
func TestEnsureCursorVisible_OversizedWindowDoesNotOscillate(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	// Window 0: small (1 line)
	// Window 1: oversized — 100 lines, much taller than the viewport
	wb.AppendOrUpdate(tlv.TagAssistantT, "window-1", "Small window")
	wb.AppendOrUpdate(tlv.TagAssistantT, "window-2", strings.Repeat("line\n", 100))

	// Set viewport height to 50 (smaller than window-2 which is ~100 lines)
	display = display.WithHeight(50)
	display = display.updateContent()

	// Place cursor on the oversized last window
	display = display.WithWindowCursor(1)

	// Simulate pressing "L" (MoveWindowCursorToBottom + EnsureCursorVisible + updateContent)
	// The first press scrolls to make the window visible
	display, _ = display.MoveWindowCursorToBottom()
	display = display.EnsureCursorVisible()
	display = display.updateContent()

	yOffsetAfterFirst := display.scrollView.YOffset()
	t.Logf("YOffset after 1st L: %d", yOffsetAfterFirst)

	// Press "L" a second time — should NOT change the YOffset
	display, _ = display.MoveWindowCursorToBottom()
	display = display.EnsureCursorVisible()
	display = display.updateContent()

	yOffsetAfterSecond := display.scrollView.YOffset()
	t.Logf("YOffset after 2nd L: %d", yOffsetAfterSecond)

	// Press "L" a third time — must stay stable
	display, _ = display.MoveWindowCursorToBottom()
	display = display.EnsureCursorVisible()
	display = display.updateContent()

	yOffsetAfterThird := display.scrollView.YOffset()
	t.Logf("YOffset after 3rd L: %d", yOffsetAfterThird)

	if yOffsetAfterSecond != yOffsetAfterThird {
		t.Errorf("YOffset oscillated: 2nd=%d, 3rd=%d (should be stable)",
			yOffsetAfterSecond, yOffsetAfterThird)
	}
}

// TestEnsureCursorVisible_OversizedWindowScrollsWhenOffScreen verifies that
// EnsureCursorVisible still scrolls when an oversized window is completely
// off-screen (entirely above or entirely below the viewport).
func TestEnsureCursorVisible_OversizedWindowScrollsWhenOffScreen(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	// Window 0: oversized — 100 lines
	// Window 1: small tail window
	wb.AppendOrUpdate(tlv.TagAssistantT, "window-1", strings.Repeat("line\n", 100))
	wb.AppendOrUpdate(tlv.TagAssistantT, "window-2", "Tail")

	display = display.WithHeight(50)
	display = display.updateContent()

	display = display.WithWindowCursor(0)

	// Scroll to the very bottom so window-0 is entirely above the viewport
	display.scrollView = display.scrollView.GotoBottom()
	display = display.updateContent()

	yOffsetBefore := display.scrollView.YOffset()
	t.Logf("YOffset before (at bottom): %d", yOffsetBefore)

	// Now ensure the oversized window is visible — it's entirely above,
	// so it should scroll to show its bottom edge
	display = display.EnsureCursorVisible()

	yOffsetAfter := display.scrollView.YOffset()
	t.Logf("YOffset after EnsureCursorVisible: %d", yOffsetAfter)

	startLine, endLine := wb.GetWindowLineRange(0)
	vpTop := yOffsetAfter
	vpBottom := vpTop + 50

	// The window should now be at least partially visible
	if endLine <= vpTop {
		t.Errorf("window still entirely above viewport: endLine=%d, viewportTop=%d", endLine, vpTop)
	}
	if startLine >= vpBottom {
		t.Errorf("window still entirely below viewport: startLine=%d, viewportBottom=%d", startLine, vpBottom)
	}
}

// TestEnsureCursorVisible_PartiallyVisibleWindowNotMoved verifies that a
// normal-sized window that is already partially visible is NOT scrolled.
func TestEnsureCursorVisible_PartiallyVisibleWindowNotMoved(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	// 20 windows, each ~8 rendered lines → ~160 total lines
	for i := range 20 {
		wb.AppendOrUpdate(tlv.TagAssistantT, "window-"+strings.Repeat("x", i+1),
			strings.Repeat("line\n", 5))
	}

	display = display.WithHeight(20) // viewport shows 20 lines
	display = display.updateContent()

	// Use window index 10 — NOT the last window, so autoFollow won't
	// force the viewport to the bottom.
	display = display.WithWindowCursor(10) // disables autoFollow

	startLine, endLine := wb.GetWindowLineRange(10)
	t.Logf("Window 10: lines %d-%d", startLine, endLine)

	// Position viewport so the window is partially visible
	partialOffset := startLine + 2
	display.scrollView = display.scrollView.WithYOffset(partialOffset)

	yOffsetBefore := display.scrollView.YOffset()
	t.Logf("YOffset before: %d (viewport %d-%d)", yOffsetBefore, yOffsetBefore, yOffsetBefore+20)

	display = display.EnsureCursorVisible()

	yOffsetAfter := display.scrollView.YOffset()
	t.Logf("YOffset after: %d", yOffsetAfter)

	if yOffsetAfter != yOffsetBefore {
		t.Errorf("YOffset changed from %d to %d for a partially visible window (should stay)",
			yOffsetBefore, yOffsetAfter)
	}
}

// TestEnsureCursorVisible_OffScreenWindowScrolls verifies that a window
// completely off-screen (below viewport) does get scrolled into view.
func TestEnsureCursorVisible_OffScreenWindowScrolls(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	for i := range 20 {
		wb.AppendOrUpdate(tlv.TagAssistantT, "window-"+strings.Repeat("x", i+1),
			strings.Repeat("line\n", 5))
	}

	display = display.WithHeight(20)
	display = display.updateContent()

	// Use window index 10 — NOT the last window to avoid autoFollow
	display = display.WithWindowCursor(10)

	// Start at top — window 10 is entirely below viewport
	display.scrollView = display.scrollView.WithYOffset(0)

	yOffsetBefore := display.scrollView.YOffset()
	t.Logf("YOffset before: %d", yOffsetBefore)

	display = display.EnsureCursorVisible()

	yOffsetAfter := display.scrollView.YOffset()
	t.Logf("YOffset after: %d", yOffsetAfter)

	if yOffsetAfter == yOffsetBefore {
		t.Errorf("YOffset unchanged at %d for off-screen window (should scroll)", yOffsetBefore)
	}

	startLine, _ := wb.GetWindowLineRange(10)
	vpTop := yOffsetAfter
	vpBottom := vpTop + 20

	if startLine >= vpBottom {
		t.Errorf("window still entirely below viewport: startLine=%d, viewportBottom=%d", startLine, vpBottom)
	}
}

// TestEnsureCursorVisible_EntirelyAboveScrolls verifies that a window
// completely above the viewport gets scrolled into view.
func TestEnsureCursorVisible_EntirelyAboveScrolls(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	for i := range 20 {
		wb.AppendOrUpdate(tlv.TagAssistantT, "window-"+strings.Repeat("x", i+1),
			strings.Repeat("line\n", 5))
	}

	display = display.WithHeight(20)
	display = display.updateContent()

	// Use window index 3 — NOT the last window
	display = display.WithWindowCursor(3)

	// Scroll to bottom so the window is entirely above the viewport
	display.scrollView = display.scrollView.GotoBottom()
	display = display.updateContent()

	display = display.EnsureCursorVisible()

	_, endLine := wb.GetWindowLineRange(3)
	vpTop := display.scrollView.YOffset()

	if endLine <= vpTop {
		t.Errorf("window still entirely above viewport: endLine=%d, viewportTop=%d", endLine, vpTop)
	}
}

// TestAutoFollow_OnlyGEnables verifies the new auto-follow design:
//   - Only G enables auto-follow
//   - All other navigation disables it
//   - auto-follow keeps viewport at bottom across updateContent calls
func TestAutoFollow_OnlyGEnables(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	wb.AppendOrUpdate(tlv.TagAssistantT, "w1", strings.Repeat("line\n", 5))
	wb.AppendOrUpdate(tlv.TagAssistantT, "w2", strings.Repeat("line\n", 5))
	wb.AppendOrUpdate(tlv.TagAssistantT, "w3", strings.Repeat("line\n", 5))

	display = display.WithHeight(10)
	display = display.updateContent()

	// Start: autoFollow is true by default (SetCursorToLastWindow from init)
	// Simulate G: enables auto-follow
	display = display.WithCursorToLastWindow()
	display = display.GotoBottom()
	display = display.updateContent()

	if !display.autoFollow {
		t.Fatal("expected autoFollow=true after G")
	}
	yOffsetAtBottom := display.scrollView.YOffset()
	t.Logf("After G: YOffset=%d, autoFollow=%v", yOffsetAtBottom, display.autoFollow)

	// Now navigate up with k — disables auto-follow
	display, _ = display.MoveWindowCursorUp()
	display = display.EnsureCursorVisible()
	display = display.updateContent()

	if display.autoFollow {
		t.Fatal("expected autoFollow=false after k")
	}

	// Simulate new content arriving — viewport should NOT jump to bottom
	wb.AppendOrUpdate(tlv.TagAssistantT, "w3", strings.Repeat("line\n", 20))
	display = display.updateContent()

	if display.scrollView.YOffset() != yOffsetAtBottom {
		t.Errorf("viewport moved after new content while autoFollow=false (YOffset changed from %d to %d)",
			yOffsetAtBottom, display.scrollView.YOffset())
	}

	// Now press G again — re-enables auto-follow
	display = display.WithCursorToLastWindow()
	display = display.GotoBottom()
	display = display.updateContent()

	if !display.autoFollow {
		t.Fatal("expected autoFollow=true after second G")
	}
	yOffsetNow := display.scrollView.YOffset()
	t.Logf("After second G: YOffset=%d", yOffsetNow)

	// More new content — should follow to bottom
	wb.AppendOrUpdate(tlv.TagAssistantT, "w3", strings.Repeat("line\n", 50))
	display = display.updateContent()

	yOffsetFinal := display.scrollView.YOffset()
	t.Logf("After new content (following): YOffset=%d", yOffsetFinal)

	totalLines := wb.GetTotalLines()
	expectedOffset := max(0, totalLines-display.GetHeight())
	if yOffsetFinal != expectedOffset {
		t.Errorf("expected YOffset=%d (bottom), got %d", expectedOffset, yOffsetFinal)
	}
}

// TestAutoFollow_JNoOpWhenNewWindowBetweenTicks is a regression test for a
// race where a new window is appended to the buffer between key events and
// the periodic tick.  Before the fix, pressing j while auto-following would
// silently move the cursor to the (invisible) new window and disable
// auto-follow, because MoveWindowCursorDown checked windowCount at the
// moment of the keypress rather than honoring the auto-follow invariant.
//
// Expected behavior: when auto-follow is active, j (MoveWindowCursorDown)
// and L (MoveWindowCursorToBottom) are always no-ops.
func TestAutoFollow_JNoOpWhenNewWindowBetweenTicks(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	wb.AppendOrUpdate(tlv.TagAssistantT, "w1", strings.Repeat("line\n", 5))
	wb.AppendOrUpdate(tlv.TagAssistantT, "w2", strings.Repeat("line\n", 5))
	wb.AppendOrUpdate(tlv.TagAssistantT, "w3", strings.Repeat("line\n", 5))

	display = display.WithHeight(20)
	display = display.updateContent()

	// Simulate G: cursor at last window, auto-follow enabled.
	display = display.WithCursorToLastWindow()
	display = display.GotoBottom()
	display = display.updateContent()

	if !display.autoFollow {
		t.Fatal("expected autoFollow=true after G")
	}
	cursorBefore := display.GetWindowCursor()
	t.Logf("After G: cursor=%d, autoFollow=%v", cursorBefore, display.autoFollow)

	// Press j while at the last window — should be a no-op.
	var moved bool
	display, moved = display.MoveWindowCursorDown()
	if moved {
		t.Fatal("j at last window with autoFollow should be a no-op")
	}
	if !display.autoFollow {
		t.Fatal("autoFollow should still be true after j at last window")
	}

	// Now a new window arrives between ticks (buffer updated, tick not yet fired).
	wb.AppendOrUpdate(tlv.TagAssistantT, "w4", strings.Repeat("line\n", 5))
	// Window count is now 4; cursor is still at old last (index 2).
	// Before the fix, pressing j here would move cursor to index 3 and
	// disable auto-follow.

	display, moved = display.MoveWindowCursorDown()
	if moved {
		t.Errorf("j with autoFollow should be no-op even after new window; cursor=%d, windowCount=%d",
			display.GetWindowCursor(), wb.WindowCount())
	}
	if !display.autoFollow {
		t.Error("autoFollow should NOT be disabled by j when a new window was appended between ticks")
	}
	if display.GetWindowCursor() != cursorBefore {
		t.Errorf("cursor should stay at %d, got %d", cursorBefore, display.GetWindowCursor())
	}

	// L (MoveWindowCursorToBottom) should also be a no-op while auto-following.
	display, moved = display.MoveWindowCursorToBottom()
	if moved {
		t.Errorf("L with autoFollow should be no-op; cursor=%d", display.GetWindowCursor())
	}
	if !display.autoFollow {
		t.Error("autoFollow should NOT be disabled by L when auto-following")
	}

	// Now simulate the tick handler: SetCursorToLastWindow advances cursor
	// to the new last window and keeps auto-follow.
	display = display.WithCursorToLastWindow()
	display = display.GotoBottom()
	display = display.updateContent()

	if display.GetWindowCursor() != 3 {
		t.Errorf("after tick, cursor should be at last window (3), got %d", display.GetWindowCursor())
	}
	if !display.autoFollow {
		t.Error("autoFollow should still be true after tick")
	}
}

// TestScrollCursorToTop_PositionsWindowAtTop verifies that ScrollCursorToTop
// scrolls the viewport so the cursor window's start line is at the top of the
// viewport, even when the window is already partially visible.
func TestScrollCursorToTop_PositionsWindowAtTop(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	// Create several windows with some content
	for i := range 10 {
		wb.AppendOrUpdate(tlv.TagUserT, "prompt-"+strings.Repeat("x", i+1),
			strings.Repeat("line\n", 5))
		wb.AppendOrUpdate(tlv.TagAssistantT, "response-"+strings.Repeat("x", i+1),
			strings.Repeat("line\n", 5))
	}

	display = display.WithHeight(20)
	display = display.updateContent()

	// Place cursor on window 8 (near the end) and bring it into view
	display = display.WithWindowCursor(8)
	display = display.EnsureCursorVisible()
	display = display.updateContent()

	// Verify the window is visible but NOT necessarily at the top
	startLine, _ := wb.GetWindowLineRange(8)
	viewportTop := display.scrollView.YOffset()
	t.Logf("Before ScrollCursorToTop: startLine=%d, viewportTop=%d", startLine, viewportTop)

	// Now use ScrollCursorToTop — should position the window at the top
	display = display.ScrollCursorToTop()
	display = display.updateContent()

	newViewportTop := display.scrollView.YOffset()
	t.Logf("After ScrollCursorToTop: startLine=%d, viewportTop=%d", startLine, newViewportTop)

	if newViewportTop != startLine {
		t.Errorf("expected viewport top at %d (window start), got %d", startLine, newViewportTop)
	}
}

// TestScrollCursorToTop_PartiallyVisibleWindowMovesToTop verifies that
// when a window is partially visible at the bottom of the viewport,
// ScrollCursorToTop scrolls it to the top (unlike EnsureCursorVisible
// which would leave it in place).
func TestScrollCursorToTop_PartiallyVisibleWindowMovesToTop(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	// Create windows (user text — folded by default, 1 line each)
	for i := range 10 {
		wb.AppendOrUpdate(tlv.TagUserT, "window-"+strings.Repeat("x", i+1),
			strings.Repeat("line\n", 5))
	}

	// Viewport of 5 rows: 10 folded windows (10 lines) overflow it, so
	// scrolling/offsets are meaningful (with a taller viewport the content
	// would fit and YOffset would be clamped to 0).
	display = display.WithHeight(5)
	display = display.updateContent()

	// Set cursor to a window and ensure it's visible (but not at top)
	display = display.WithWindowCursor(5)
	display = display.EnsureCursorVisible()
	display = display.updateContent()

	startLine5, endLine5 := wb.GetWindowLineRange(5)
	t.Logf("Window 5: lines %d-%d", startLine5, endLine5)

	// Position viewport so window 5 is partially visible at the bottom
	viewportHeight := display.GetHeight()
	display.scrollView = display.scrollView.WithYOffset(startLine5 - 2) // window starts 2 lines above viewport bottom
	display = display.updateContent()

	viewportTopBefore := display.scrollView.YOffset()
	t.Logf("Before: viewport=%d-%d, window starts at %d",
		viewportTopBefore, viewportTopBefore+viewportHeight, startLine5)

	// EnsureCursorVisible would NOT scroll (window is partially visible)
	display = display.EnsureCursorVisible()
	viewportTopAfterEnsure := display.scrollView.YOffset()
	if viewportTopAfterEnsure != viewportTopBefore {
		t.Fatalf("EnsureCursorVisible should not have scrolled (it did: %d -> %d)",
			viewportTopBefore, viewportTopAfterEnsure)
	}

	// ScrollCursorToTop SHOULD scroll to put window at top
	display = display.ScrollCursorToTop()
	viewportTopAfterScroll := display.scrollView.YOffset()
	t.Logf("After ScrollCursorToTop: viewportTop=%d, expected=%d", viewportTopAfterScroll, startLine5)

	if viewportTopAfterScroll != startLine5 {
		t.Errorf("expected viewport top at %d (window start), got %d", startLine5, viewportTopAfterScroll)
	}
}

// TestScrollCursorToTop_NoCursorDoesNothing verifies that ScrollCursorToTop
// is a no-op when no window cursor is set.
func TestScrollCursorToTop_NoCursorDoesNothing(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	wb.AppendOrUpdate(tlv.TagUserT, "window-1", "hello")
	display = display.WithHeight(10)
	display = display.updateContent()

	// No cursor set (-1 is the default)
	yOffsetBefore := display.scrollView.YOffset()
	display = display.ScrollCursorToTop()
	yOffsetAfter := display.scrollView.YOffset()

	if yOffsetBefore != yOffsetAfter {
		t.Errorf("ScrollCursorToTop should be no-op with no cursor; YOffset changed %d -> %d",
			yOffsetBefore, yOffsetAfter)
	}
}

// TestScrollCursorToTop_AlreadyAtTopIsStable verifies that calling
// ScrollCursorToTop when the window is already at the top is idempotent.
func TestScrollCursorToTop_AlreadyAtTopIsStable(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	for i := range 5 {
		wb.AppendOrUpdate(tlv.TagUserT, "window-"+strings.Repeat("x", i+1),
			strings.Repeat("line\n", 5))
	}

	display = display.WithHeight(20)
	display = display.updateContent()

	display = display.WithWindowCursor(2)
	display = display.ScrollCursorToTop()
	display = display.updateContent()

	yOffset1 := display.scrollView.YOffset()

	// Call again — should be stable
	display = display.ScrollCursorToTop()
	display = display.updateContent()

	yOffset2 := display.scrollView.YOffset()
	if yOffset1 != yOffset2 {
		t.Errorf("ScrollCursorToTop not stable: first=%d, second=%d", yOffset1, yOffset2)
	}
}
