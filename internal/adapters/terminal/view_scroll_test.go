package terminal

// The arrows move the viewport, not the pointer — and the reason is a mouse
// wheel, which this program never sees as a wheel.
//
// While the alternate screen is ours, no mouse reporting is requested
// (screen.go → mouseReportingOff), which is also what leaves click-and-drag
// text selection with the terminal. A host that lets the wheel move the screen
// in that state does it by writing the arrow keys' own bytes into the input
// stream, so a notch and a keystroke arrive as literally the same message:
// there is no second thing to bind. Whatever `↓`/`↑` do therefore *is* the
// wheel's behavior, and these tests drive the bytes rather than a chord
// assembled by hand, so the premise cannot rot into a comment.
//
// Bound to the pointer, that same stream produced a gesture that looked dead
// rolling down (auto-follow refuses MoveWindowCursorDown at the live edge) and
// jumped whole windows rolling up. Bound to the viewport it is one line per
// arrow a host produces for a notch — which is what `J`/`K` have always done,
// and the two must stay in step.
//
// The pointer's keys are `j`/`k` and the vim names (`H`/`M`/`L`, `g`/`G`,
// `f`/`b`). List overlays are not this file's subject: a picker has no viewport
// apart from its selection, so its arrows move the selection.

import (
	"fmt"
	"testing"

	"github.com/alayacore/alayacore/internal/tlv"
)

// tallTranscript builds a transcript that cannot fit its viewport: one line
// per window, so a window's height and the offset from the bottom are
// countable, and a viewport of ten rows that forty single-line windows overflow.
func tallTranscript(t *testing.T) DisplayModel {
	t.Helper()

	const windows, height = 40, 10
	wb := NewWindowBuffer(80, DefaultStyles())
	for i := range windows {
		wb.AppendOrUpdate(tlv.TagAssistantT, fmt.Sprintf("w%d", i), fmt.Sprintf("body %d", i))
	}
	return NewDisplayModel(wb, DefaultStyles()).WithHeight(height).updateContent()
}

// press parses raw terminal bytes — the same stream a host writes for a wheel
// notch or an arrow key — and applies every key it yields.
func press(m DisplayModel, bytes string) DisplayModel {
	var p InputParser
	for _, msg := range p.Parse([]byte(bytes)) {
		if km, ok := msg.(KeyPressMsg); ok {
			m, _ = m.Update(km)
		}
	}
	return m
}

func TestArrowDownScrollsTheViewportAndLeavesThePointerAlone(t *testing.T) {
	m := tallTranscript(t).GotoBottom()
	bottom := m.YOffset()
	if m.GetWindowCursor() != -1 {
		t.Fatalf("cursor = %d, want -1: a fresh transcript follows the tail", m.GetWindowCursor())
	}

	// One notch up first: at the live edge itself, down has nowhere to go.
	m = press(m, "\x1b[A")
	if got := m.YOffset(); got != bottom-1 {
		t.Fatalf("after up: YOffset = %d, want %d", got, bottom-1)
	}

	m = press(m, "\x1b[B")

	if got := m.YOffset(); got != bottom {
		t.Errorf("YOffset = %d, want %d: one notch, one line", got, bottom)
	}
	if m.GetWindowCursor() != -1 {
		t.Errorf("cursor = %d, want -1: an arrow must not move the pointer", m.GetWindowCursor())
	}
}

func TestArrowUpScrollsAndTurnsAutoFollowOff(t *testing.T) {
	m := tallTranscript(t).GotoBottom()
	before := m.YOffset()

	m = press(m, "\x1b[A")

	if got := m.YOffset(); got != before-1 {
		t.Errorf("YOffset = %d, want %d", got, before-1)
	}
	if m.autoFollow {
		t.Error("auto-follow still on: scrolling up must release it, or the next streamed line pulls the view back to the tail")
	}
}

func TestArrowDownAtTheLiveEdgeIsInert(t *testing.T) {
	m := tallTranscript(t).GotoBottom()
	before := m.YOffset()

	m = press(m, "\x1b[B\x1b[B\x1b[B")

	if m.YOffset() != before {
		t.Errorf("YOffset moved to %d from %d at the bottom", m.YOffset(), before)
	}
	if !m.autoFollow {
		t.Error("auto-follow off: rolling down at the live edge is not a departure from it")
	}
}

func TestShiftedArrowsAreBoundNowhere(t *testing.T) {
	// Dropping these bindings removed the one thing the table depended on a
	// terminal reporting specially — a modified arrow, which arrives as
	// `ESC [ 1;2 A/B` on xterm-like hosts and `ESC [ a/b` on urxvt. The parser
	// still decodes both (key_parser_test.go pins that); the UI just does not
	// answer to them, so a host that cannot send them loses no binding.
	m := tallTranscript(t).GotoBottom()
	offset, cursor, follow := m.YOffset(), m.GetWindowCursor(), m.autoFollow

	for _, seq := range []string{"\x1b[1;2A", "\x1b[1;2B", "\x1b[a", "\x1b[b"} {
		after := press(m, seq)
		if after.YOffset() != offset || after.GetWindowCursor() != cursor || after.autoFollow != follow {
			t.Errorf("%q moved the display (offset %d→%d, cursor %d→%d, follow %v→%v): a modified arrow is bound nowhere",
				seq, offset, after.YOffset(), cursor, after.GetWindowCursor(), follow, after.autoFollow)
		}
	}
}

func TestHomeRowScrollMatchesTheArrows(t *testing.T) {
	// `J`/`K` and the arrows are one motion in two places on the keyboard. If
	// they diverged, the wheel and the home row would disagree about the same
	// gesture — the failure this file exists to prevent.
	byArrow := press(tallTranscript(t).GotoBottom(), "\x1b[B\x1b[B\x1b[A")
	byHomeRow := press(tallTranscript(t).GotoBottom(), "JJK")

	if a, b := byArrow.YOffset(), byHomeRow.YOffset(); a != b {
		t.Errorf("arrows scrolled to %d, home row to %d: J/K and the arrows must be the same motion", a, b)
	}
	if a, b := byArrow.GetWindowCursor(), byHomeRow.GetWindowCursor(); a != -1 || b != -1 {
		t.Errorf("pointer moved by a viewport gesture: arrows left it at %d, home row at %d", a, b)
	}
}

func TestPointerKeysStillMoveThePointer(t *testing.T) {
	// The other half of the split: `j`/`k` keep the cursor, which is what every
	// cursor-gated action reads (Space folds, r toggles rendering, e opens the
	// editor, Ctrl+F forks).
	m := tallTranscript(t).GotoBottom()
	m = press(m, "\x1b[A\x1b[A") // leave the live edge, then the pointer exists to move
	if m.GetWindowCursor() != -1 {
		t.Fatalf("cursor = %d, want -1: scrolling the viewport does not place a pointer", m.GetWindowCursor())
	}

	m = press(m, "kk")
	if got := m.GetWindowCursor(); got == -1 {
		t.Fatal("k moved no pointer: the home row still owns the cursor")
	}
}
