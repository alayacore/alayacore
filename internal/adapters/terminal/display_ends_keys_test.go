package terminal

// The transcript's two ends are one key: `G` follows the last window, and a
// counted `G` is an absolute window — `1G` the first one. Both are pressed here
// rather than called, because an unbound key does exactly what the display's
// default case does (nothing), so only a press can tell a binding from a gap.

import (
	"fmt"
	"strings"
	"testing"
)

// windowOnScreen reports whether any row of [start,end) is inside the viewport.
func windowOnScreen(m DisplayModel, start, end int) bool {
	top := m.YOffset()
	return start < top+m.GetHeight() && end > top
}

// TestEndKeysJumpToFirstAndLastWindow pins the two end gestures: bare `G` is the
// last window and a request to follow it, `1G` is the first window and leaves
// following alone.
func TestEndKeysJumpToFirstAndLastWindow(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())
	for i := range 5 {
		wb.AppendOrUpdate(tagAssistantText, fmt.Sprintf("window-%d", i), strings.Repeat("line\n", 10))
	}
	display = display.WithHeight(6)
	display = display.WithDisplayFocused(true)
	display = display.updateContent()

	// `G`: the last window, following again.
	display = pressWithCount(t, display, 0, Key{Code: 'G'})
	if got, want := display.GetWindowCursor(), 4; got != want {
		t.Errorf("`G` moved the cursor to window %d, want %d (the last one)", got, want)
	}
	if !display.autoFollow {
		t.Error("`G` did not engage auto-follow")
	}
	if !display.AtBottom() {
		t.Errorf("`G` left the viewport at yOffset %d, want the bottom", display.YOffset())
	}

	// Move away, so the jump back to the first window has somewhere to go.
	display, _ = display.Update(KeyPressMsg(Key{Code: 'k'}))
	if display.autoFollow {
		t.Fatal("`k` did not disable auto-follow; the fixture is not in the state under test")
	}

	// `1G`: the first window. Following stays off — a counted G is a jump, and a
	// jump to an absolute window is never a request to watch the live edge.
	display = pressWithCount(t, display, 1, Key{Code: 'G'})
	if got := display.GetWindowCursor(); got != 0 {
		t.Errorf("`1G` moved the cursor to window %d, want 0 (the first one)", got)
	}
	if display.autoFollow {
		t.Error("`1G` engaged auto-follow; only bare `G` does that")
	}
	if start, end := wb.GetWindowLineRange(0); !windowOnScreen(display, start, end) {
		t.Errorf("`1G` left the first window off screen (yOffset %d, height %d, window %d..%d)",
			display.YOffset(), display.GetHeight(), start, end)
	}
}

// TestFirstWindowJumpLeavesTheLiveEdge covers the case where the cursor is
// already on the first window: WithWindowCursor then changes nothing, and
// preserving auto-follow would let updateContent — and every tick after it —
// pull the viewport straight back to the bottom. That is every single-window
// transcript taller than the screen, the shape where the cursor has nowhere to
// go and the jump is the whole effect.
func TestFirstWindowJumpLeavesTheLiveEdge(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())
	wb.AppendOrUpdate(tagAssistantText, "window-0", strings.Repeat("line\n", 40))
	display = display.WithHeight(10)
	display = display.WithDisplayFocused(true)

	display = pressWithCount(t, display, 0, Key{Code: 'G'})
	if !display.autoFollow || display.GetWindowCursor() != 0 {
		t.Fatal("`G` left the fixture outside the state under test (following, cursor on the first window)")
	}

	display = pressWithCount(t, display, 1, Key{Code: 'G'})
	if display.autoFollow {
		t.Error("`1G` left auto-follow on; the next tick would put the viewport back at the bottom")
	}
}

// TestGAtTheTopOnlyWhenTheFirstMessageFits: a counted `G` reuses the placement
// rule the other pointer motions use, so `1G` reaches the document's first row
// exactly when the first window fits the viewport. When that message is taller
// than the screen the viewport shows its tail and the pin names it — that is the
// rule, not a missing special case, and the scroll keys reach the rest.
func TestGAtTheTopOnlyWhenTheFirstMessageFits(t *testing.T) {
	// The first message fits: `1G` is the top of the transcript.
	short := NewWindowBuffer(80, DefaultStyles())
	short.AppendOrUpdate(tagAssistantText, "first", strings.Repeat("line\n", 3))
	short.AppendOrUpdate(tagAssistantText, "second", strings.Repeat("line\n", 40))
	m := NewDisplayModel(short, DefaultStyles()).WithHeight(10).WithDisplayFocused(true)
	m = pressWithCount(t, m, 0, Key{Code: 'G'})
	m = pressWithCount(t, m, 1, Key{Code: 'G'})
	if got := m.YOffset(); got != 0 {
		t.Errorf("`1G` with a first message that fits: yOffset %d, want 0 (the top)", got)
	}

	// Taller than the viewport: the message's last line lands on the bottom
	// edge, which is what one `k`-step into view puts there too.
	tall := NewWindowBuffer(80, DefaultStyles())
	tall.AppendOrUpdate(tagAssistantText, "first", strings.Repeat("line\n", 40))
	m = NewDisplayModel(tall, DefaultStyles()).WithHeight(10).WithDisplayFocused(true)
	m = pressWithCount(t, m, 0, Key{Code: 'G'})
	m = pressWithCount(t, m, 1, Key{Code: 'G'})
	_, end := tall.GetWindowLineRange(0)
	if got, want := m.YOffset(), end-m.GetHeight(); got != want {
		t.Errorf("`1G` with a first message taller than the viewport: yOffset %d, want %d", got, want)
	}
	if got := m.GetWindowCursor(); got != 0 {
		t.Errorf("`1G` left the cursor on window %d, want 0", got)
	}
}

// TestZeroIsNotACount: vi reads a leading `0` as a column motion and this UI has
// no columns, so `0` starts nothing — which makes `0G` the same gesture as `G`
// rather than a jump to a zeroth window.
func TestZeroIsNotACount(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	for i := range 3 {
		wb.AppendOrUpdate(tagAssistantText, fmt.Sprintf("w-%d", i), strings.Repeat("line\n", 4))
	}
	m := NewDisplayModel(wb, DefaultStyles()).WithHeight(5).WithDisplayFocused(true)
	m, _ = m.Update(KeyPressMsg(Key{Code: 'k'})) // leave the live edge

	m, _ = m.Update(KeyPressMsg(Key{Code: '0'}))
	if got := m.PendingCount(); got != 0 {
		t.Fatalf("`0` armed a count of %d; a leading zero is not a count", got)
	}

	m = pressWithCount(t, m, 0, Key{Code: 'G'})
	if got, want := m.GetWindowCursor(), 2; got != want {
		t.Errorf("`0G` left the cursor on window %d, want %d (the same as `G`)", got, want)
	}
	if !m.autoFollow {
		t.Error("`0G` should follow the last window, as `G` does")
	}
}
