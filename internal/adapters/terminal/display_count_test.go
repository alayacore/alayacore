package terminal

// The count prefix: a digit run typed in the display pane repeats the next
// motion. `N<key>` is defined as N presses of that key, so these tests compare
// against presses rather than against a number — where a press stops (a
// boundary, a missing prompt) the count stops too.

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// pressWithCount types the digits of count (nothing when count <= 0) and then
// the key — one KeyMsg each, which is the way the prefix arrives.
func pressWithCount(t *testing.T, m DisplayModel, count int, k Key) DisplayModel {
	t.Helper()
	if count > 0 {
		for _, d := range strconv.Itoa(count) {
			m, _ = m.Update(KeyPressMsg(Key{Code: d}))
		}
		if got := m.PendingCount(); got != count {
			t.Fatalf("typing %d left %d armed", count, got)
		}
	}
	msg := KeyPressMsg(k)
	m, _ = m.Update(msg)
	if got := m.PendingCount(); got != 0 {
		t.Fatalf("the count outlived %v: %d still armed", msg.Chord(), got)
	}
	return m
}

// countFixture builds a transcript of n windows, each three rows tall (its own
// line and two body lines), at the given viewport height.
func countFixture(n, height int) DisplayModel {
	wb := NewWindowBuffer(40, DefaultStyles())
	for i := 0; i < n; i++ {
		wb.AppendOrUpdate(tagAssistantText, fmt.Sprintf("w%02d", i), "a\nb")
	}
	return NewDisplayModel(wb, DefaultStyles()).WithHeight(height).WithDisplayFocused(true)
}

// parkedFixture is countFixture with the cursor placed on window start and the
// viewport showing it. WithWindowCursor is how a pointer gets made without
// pressing a key, and it leaves auto-follow off — the state a count is normally
// typed in.
func parkedFixture(n, start int) DisplayModel {
	return countFixture(n, 6).WithWindowCursor(start).EnsureWindowCursorVisible().updateContent()
}

// TestCountIsNPresses is the definition, checked against the thing it is defined
// by: the counted motion and N presses must end in the same place, viewport
// included, and both must stop at the buffer's end.
func TestCountIsNPresses(t *testing.T) {
	const start = 3
	steps := func(key rune, count, presses, wantCursor int) DisplayModel {
		m := parkedFixture(20, start)
		if count > 0 {
			m = pressWithCount(t, m, count, Key{Code: key})
		} else {
			for i := 0; i < presses; i++ {
				m, _ = m.Update(KeyPressMsg(Key{Code: key}))
			}
		}
		if got := m.GetWindowCursor(); got != wantCursor {
			t.Fatalf("%d presses of %q left the cursor on %d, want %d", presses, key, got, wantCursor)
		}
		return m
	}

	counted := steps('j', 9, 0, start+9)
	repeated := steps('j', 0, 9, start+9)
	if counted.YOffset() != repeated.YOffset() {
		t.Errorf("`9j` left yOffset %d, nine presses left %d: a count is N presses",
			counted.YOffset(), repeated.YOffset())
	}

	// Past the end: nine `k` presses from window 3 stop at the first window, and
	// so does the counted motion.
	stopped := steps('k', 9, 0, 0)
	if got := steps('k', 0, 9, 0).YOffset(); got != stopped.YOffset() {
		t.Errorf("`9k` and nine presses disagree past the first window: yOffset %d vs %d",
			stopped.YOffset(), got)
	}
}

// TestCountOnTheViewportMotions checks the family that clamps instead of
// failing: the line keys (both spellings) and the half-screen keys move N of
// themselves, and the count is multiplied rather than looped, so a huge one
// costs one clamped scroll.
func TestCountOnTheViewportMotions(t *testing.T) {
	m := parkedFixture(20, 0)
	base := m.YOffset()

	line := pressWithCount(t, m, 3, Key{Code: KeyDown})
	if got, want := line.YOffset(), base+3; got != want {
		t.Errorf("`3<down>` scrolled to %d, want %d", got, want)
	}
	ctrlE := pressWithCount(t, m, 3, Key{Code: 'e', Mod: ModCtrl})
	if ctrlE.YOffset() != line.YOffset() {
		t.Errorf("`3<C-e>` scrolled to %d, `3<down>` to %d: same motion, same result",
			ctrlE.YOffset(), line.YOffset())
	}
	back := pressWithCount(t, line, 3, Key{Code: KeyUp})
	if got := back.YOffset(); got != base {
		t.Errorf("`3<up>` from %d left %d, want %d", line.YOffset(), got, base)
	}
	if ctrlY := pressWithCount(t, line, 3, Key{Code: 'y', Mod: ModCtrl}); ctrlY.YOffset() != back.YOffset() {
		t.Errorf("`3<C-y>` scrolled to %d, `3<up>` to %d", ctrlY.YOffset(), back.YOffset())
	}

	// A count past the document is one clamped scroll, not a million steps: the
	// product saturates and ScrollDown clamps.
	huge := pressWithCount(t, m, maxCount, Key{Code: KeyDown})
	if !huge.AtBottom() {
		t.Errorf("`%d<down>` left the viewport at %d, want the bottom", maxCount, huge.YOffset())
	}
}

// TestCountStopsAtTheBufferEnds: a relative motion is N presses, so a count past
// the end stops on the last window — the operation stays possible all the way.
func TestCountStopsAtTheBufferEnds(t *testing.T) {
	m := parkedFixture(20, 3)

	if got := pressWithCount(t, m, 999, Key{Code: 'j'}).GetWindowCursor(); got != 19 {
		t.Errorf("`999j` left the cursor on window %d, want 19 (the last one)", got)
	}
	if got := pressWithCount(t, m, 999, Key{Code: 'k'}).GetWindowCursor(); got != 0 {
		t.Errorf("`999k` left the cursor on window %d, want 0 (the first one)", got)
	}
}

// TestCountedGCountsDownFromTheTop: `NG` counts messages down from the first one
// rather than stepping from the cursor, and the count is taken as far as the
// transcript goes — `999G` in a twenty-window session is the twentieth. It is
// still a jump, so following stays off however far the count reached.
func TestCountedGCountsDownFromTheTop(t *testing.T) {
	m := parkedFixture(20, 3)

	if got := pressWithCount(t, m, 2, Key{Code: 'G'}).GetWindowCursor(); got != 1 {
		t.Errorf("`2G` left the cursor on window %d, want 1 (the second window)", got)
	}
	if got := pressWithCount(t, m, 20, Key{Code: 'G'}).GetWindowCursor(); got != 19 {
		t.Errorf("`20G` left the cursor on window %d, want 19 (the last)", got)
	}
	if last := pressWithCount(t, m, 20, Key{Code: 'G'}); last.autoFollow {
		t.Error("`20G` engaged auto-follow; only bare `G` does that")
	}

	// Past the end: the last window, and still a jump.
	past := pressWithCount(t, m, 999, Key{Code: 'G'})
	if got := past.GetWindowCursor(); got != 19 {
		t.Errorf("`999G` left the cursor on window %d, want 19 (the count ran out there)", got)
	}
	if past.autoFollow {
		t.Error("`999G` engaged auto-follow; a counted G never does")
	}
	if !past.AtBottom() {
		t.Errorf("`999G` left the viewport at %d, want the last window in view", past.YOffset())
	}

	// While following, the same: the jump takes the end and stops following, which
	// is what makes a big count the way to say "the newest, and hold still".
	following := countFixture(20, 6).FollowLatest()
	after := pressWithCount(t, following, 999, Key{Code: 'G'})
	if after.autoFollow {
		t.Error("`999G` left auto-follow on")
	}
	if got := after.GetWindowCursor(); got != 19 {
		t.Errorf("`999G` from the live edge left the cursor on window %d, want 19", got)
	}

	// An empty transcript has no window to count to, so `1G` is the degenerate
	// case every motion has: nothing moves.
	empty := NewDisplayModel(NewWindowBuffer(40, DefaultStyles()), DefaultStyles()).
		WithHeight(6).WithDisplayFocused(true)
	after = pressWithCount(t, empty, 1, Key{Code: 'G'})
	if after.GetWindowCursor() != empty.GetWindowCursor() || after.autoFollow != empty.autoFollow {
		t.Errorf("`1G` on an empty transcript changed the display: cursor %d→%d, follow %v→%v",
			empty.GetWindowCursor(), after.GetWindowCursor(), empty.autoFollow, after.autoFollow)
	}
}

// TestCountOnHandL is the on-screen ordinal: H counts down from the viewport's
// top edge, L up from its bottom edge, and a count past the far edge clamps to
// the window the other key starts at.
func TestCountOnHandL(t *testing.T) {
	// Two three-row windows fill a six-row viewport exactly.
	m := parkedFixture(6, 0)
	if got := pressWithCount(t, m, 2, Key{Code: 'H'}).GetWindowCursor(); got != 1 {
		t.Errorf("`2H` left the cursor on window %d, want 1 (the second on screen)", got)
	}
	if got := pressWithCount(t, m, 2, Key{Code: 'L'}).GetWindowCursor(); got != 0 {
		t.Errorf("`2L` left the cursor on window %d, want 0 (second from the bottom)", got)
	}
	if got := pressWithCount(t, m, 999, Key{Code: 'H'}).GetWindowCursor(); got != 1 {
		t.Errorf("`999H` left the cursor on window %d, want 1 (L's window)", got)
	}
	if got := pressWithCount(t, m, 999, Key{Code: 'L'}).GetWindowCursor(); got != 0 {
		t.Errorf("`999L` left the cursor on window %d, want 0 (H's window)", got)
	}

	// M has no ordinal space: the count is spent and dropped.
	plain, _ := m.Update(KeyPressMsg(Key{Code: 'M'}))
	counted := pressWithCount(t, m, 7, Key{Code: 'M'})
	if plain.GetWindowCursor() != counted.GetWindowCursor() {
		t.Errorf("`7M` moved to %d, `M` to %d: M takes no count",
			counted.GetWindowCursor(), plain.GetWindowCursor())
	}
}

// TestCountOnPromptJumps: f and b count prompts and move as far as the prompts
// exist — a count whose last step finds nothing stays on the prompt it reached,
// because that is what pressing the key that many times does.
func TestCountOnPromptJumps(t *testing.T) {
	wb := NewWindowBuffer(40, DefaultStyles())
	// Three user prompts, with an answer under each.
	for i := range 3 {
		wb.AppendOrUpdate(tagUserText, fmt.Sprintf("u%d", i), "ask")
		wb.AppendOrUpdate(tagAssistantText, fmt.Sprintf("a%d", i), "answer")
	}
	m := NewDisplayModel(wb, DefaultStyles()).WithHeight(6).WithDisplayFocused(true)
	m = m.WithWindowCursor(0).EnsureWindowCursorVisible().updateContent()

	if got := pressWithCount(t, m, 2, Key{Code: 'f'}).GetWindowCursor(); got != 4 {
		t.Errorf("`2f` left the cursor on window %d, want 4 (the second prompt below)", got)
	}
	past := pressWithCount(t, m, 9, Key{Code: 'f'})
	if got := past.GetWindowCursor(); got != 4 {
		t.Errorf("`9f` left the cursor on window %d, want 4 (the last prompt)", got)
	}
	if got := pressWithCount(t, past, 9, Key{Code: 'b'}).GetWindowCursor(); got != 0 {
		t.Errorf("`9b` left the cursor on window %d, want 0 (the first prompt)", got)
	}
}

// TestEscCancelsTheCount: the one key that spends a count without acting, so
// abandoning a mistyped prefix does not have to trigger a motion — or worse,
// `e` or `r`.
func TestEscCancelsTheCount(t *testing.T) {
	m := parkedFixture(10, 2)
	before := m.GetWindowCursor()
	offset := m.YOffset()

	for _, d := range "35" {
		m, _ = m.Update(KeyPressMsg(Key{Code: d}))
	}
	if got := m.PendingCount(); got != 35 {
		t.Fatalf("typing 35 left %d armed", got)
	}
	m, _ = m.Update(KeyPressMsg(Key{Code: KeyEscape}))
	if got := m.PendingCount(); got != 0 {
		t.Fatalf("`esc` left %d armed", got)
	}
	if m.GetWindowCursor() != before || m.YOffset() != offset {
		t.Errorf("`esc` moved something: cursor %d→%d, yOffset %d→%d",
			before, m.GetWindowCursor(), offset, m.YOffset())
	}
	// The next key is uncounted.
	m = pressWithCount(t, m, 0, Key{Code: 'j'})
	if got := m.GetWindowCursor(); got != before+1 {
		t.Errorf("`j` after `esc` moved to %d, want %d", got, before+1)
	}
}

// TestAnyOtherKeySpendsTheCount: a key that has no use for the count drops it,
// so it cannot be spent by a later key the user did not type it for. The
// comparison is a walk with no digits in front of it — same keys, same landing
// — because M moves the cursor too and only the *difference* is the count's.
func TestAnyOtherKeySpendsTheCount(t *testing.T) {
	walk := func(armed bool) DisplayModel {
		m := parkedFixture(10, 2)
		if armed {
			for _, d := range "7" {
				m, _ = m.Update(KeyPressMsg(Key{Code: d}))
			}
		}
		m = pressWithCount(t, m, 0, Key{Code: 'M'}) // M takes no count: spent, dropped
		return pressWithCount(t, m, 0, Key{Code: 'j'})
	}

	plain, armed := walk(false), walk(true)
	if got, want := armed.GetWindowCursor(), plain.GetWindowCursor(); got != want {
		t.Errorf("`7M j` landed on window %d, `M j` on %d: the count survived M", got, want)
	}
	if armed.YOffset() != plain.YOffset() {
		t.Errorf("`7M j` left yOffset %d, `M j` %d", armed.YOffset(), plain.YOffset())
	}
}

// TestCountSaturates: the parse cannot overflow into a negative count, which
// would walk backwards, and the echo stays seven cells wide.
func TestCountSaturates(t *testing.T) {
	m := parkedFixture(5, 1)
	for range 12 {
		m, _ = m.Update(KeyPressMsg(Key{Code: '9'}))
	}
	if got := m.PendingCount(); got != maxCount {
		t.Errorf("twelve nines armed %d, want %d (the saturating ceiling)", got, maxCount)
	}
	if got := len(countText(m.PendingCount())); got > 7 {
		t.Errorf("the echo is %d cells wide, want at most 7", got)
	}

	// The motion still lands on the last window rather than somewhere backwards.
	m, _ = m.Update(KeyPressMsg(Key{Code: 'j'}))
	if got, want := m.GetWindowCursor(), 4; got != want {
		t.Errorf("a saturated count moved to window %d, want %d", got, want)
	}
}

// TestStatusBarEchoesTheCount: the digits are shown right after the indicator,
// and only while a count is armed. The bar is a pure function of its cache key,
// so this also pins that the count is part of that key.
func TestStatusBarEchoesTheCount(t *testing.T) {
	m := newTestTerminal()
	plain := stripANSI(m.renderStatusBar())
	if strings.Contains(plain, "12x") {
		t.Fatalf("the row already claims a count: %q", plain)
	}

	m.display = m.display.pushCount(1).pushCount(2)
	armed := stripANSI(m.renderStatusBar())
	if !strings.HasPrefix(armed, statusIdleGlyph+" 12x") {
		t.Errorf("the count should follow the indicator: %q", armed)
	}
	if armed == plain {
		t.Error("the row did not change: the count is missing from the render inputs")
	}

	m.display = m.display.ClearCount()
	if got := stripANSI(m.renderStatusBar()); got != plain {
		t.Errorf("dropping the count did not restore the row:\n  got:  %q\n  want: %q", got, plain)
	}
}

// TestCountDoesNotSurviveLeavingTheDisplay: a count belongs to the display's next
// key, so a key that lands in another pane drops it — including a round trip
// through the prompt, which must not carry the digits back.
func TestCountDoesNotSurviveLeavingTheDisplay(t *testing.T) {
	m := newTestTerminal().focusDisplay()
	m.display = m.display.pushCount(2).pushCount(5)
	if got := m.display.PendingCount(); got != 25 {
		t.Fatalf("fixture armed %d, want 25", got)
	}

	m, _ = m.handleKeyMsg(KeyPressMsg(Key{Code: KeyTab}))
	if m.focusedWindow != focusPrompt {
		t.Fatalf("tab left focus on %q, want the prompt", m.focusedWindow)
	}
	m, _ = m.handleKeyMsg(KeyPressMsg(Key{Code: 'x'}))
	if got := m.display.PendingCount(); got != 0 {
		t.Errorf("a key in the prompt left %d armed", got)
	}

	// And back to the display: nothing to carry back.
	m, _ = m.handleKeyMsg(KeyPressMsg(Key{Code: KeyTab}))
	if got := m.display.PendingCount(); got != 0 {
		t.Errorf("a round trip through the prompt carried %d back to the display", got)
	}
}
