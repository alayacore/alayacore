package terminal

import (
	"fmt"
	"strings"
	"testing"
)

// TestEndKeysJumpToFirstAndLastWindow pins the two end keys. They are the
// bindings a refactor is most likely to lose silently — an unbound key does
// exactly what the display's default case does (nothing), so only a test that
// presses the key and looks at the cursor can tell the difference between a
// binding and a gap.
func TestEndKeysJumpToFirstAndLastWindow(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())
	for i := range 5 {
		wb.AppendOrUpdate(tagAssistantText, fmt.Sprintf("window-%d", i), strings.Repeat("line\n", 10))
	}
	display = display.WithHeight(6)
	display = display.WithDisplayFocused(true)
	display = display.updateContent()

	// `>`: the last window, following again.
	display, _ = display.Update(KeyPressMsg(Key{Code: '>'}))
	if got, want := display.GetWindowCursor(), 4; got != want {
		t.Errorf("`>` moved the cursor to window %d, want %d (the last one)", got, want)
	}
	if !display.autoFollow {
		t.Error("`>` did not re-enable auto-follow")
	}
	if !display.AtBottom() {
		t.Errorf("`>` left the viewport at yOffset %d, want the bottom", display.YOffset())
	}

	// Move away, so the jump back to the first window has somewhere to go.
	display, _ = display.Update(KeyPressMsg(Key{Code: 'k'}))
	if display.autoFollow {
		t.Fatal("`k` did not disable auto-follow; the fixture is not in the state under test")
	}

	// `<`: the first window, scrolled to the top, and following stays off — a
	// jump to the head of the transcript is not a request to watch the live
	// edge.
	display, _ = display.Update(KeyPressMsg(Key{Code: '<'}))
	if got := display.GetWindowCursor(); got != 0 {
		t.Errorf("`<` moved the cursor to window %d, want 0 (the first one)", got)
	}
	if got := display.YOffset(); got != 0 {
		t.Errorf("`<` left the viewport at yOffset %d, want 0 (the top)", got)
	}
	if display.autoFollow {
		t.Error("`<` re-enabled auto-follow; only `>` does that")
	}
}

// TestFirstWindowKeyLeavesTheLiveEdge covers the case where the cursor is
// already on the first window: WithWindowCursor(0) then changes nothing, and
// preserving autoFollow would let updateContent pull the viewport back to the
// bottom in the same keypress. The jump would be invisible on a single window
// taller than the screen — the one shape where the cursor has nowhere to go
// and the scroll is the whole effect.
func TestFirstWindowKeyLeavesTheLiveEdge(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())
	wb.AppendOrUpdate(tagAssistantText, "window-0", strings.Repeat("line\n", 40))
	display = display.WithHeight(10)
	display = display.WithDisplayFocused(true)

	display, _ = display.Update(KeyPressMsg(Key{Code: '>'}))
	if !display.autoFollow {
		t.Fatal("`>` left the fixture outside the state under test (following, cursor on the first window)")
	}

	display, _ = display.Update(KeyPressMsg(Key{Code: '<'}))
	if got := display.YOffset(); got != 0 {
		t.Errorf("`<` left the viewport at yOffset %d, want 0: the live edge must not undo the jump", got)
	}
	if display.autoFollow {
		t.Error("`<` left auto-follow on; the next tick would put the viewport back at the bottom")
	}
}
