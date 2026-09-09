package terminal

import (
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/tlv"
)

// The f/b keys jump the cursor between user prompts and re-seat the viewport
// so the prompt sits at the top of the screen, leaving its answer visible
// below it. That re-seat is the aggressive one: unlike the
// EnsureCursorVisible used by every other jump key, ScrollCursorToTop moves
// the viewport unconditionally, to the head of whichever window the cursor
// happens to rest on.
//
// So the jump keys have to re-seat only on a hit. When there is no prompt
// left in the direction asked for — the usual state at the end of a turn,
// where the cursor sits on the last assistant window, below the last prompt —
// they found nothing but re-seated anyway, yanking the viewport up to the
// head of the window already under the cursor. The reader lost their place
// without the cursor moving a line, and with auto-follow still engaged (the
// move never reached setCursor, which is what turns it off) the next tick
// dragged the view back to the bottom.

// promptJumpViewport is the display height the fixtures below use: short
// enough that a three-turn transcript overflows it, so a stray scroll shows
// up as a changed YOffset rather than being clamped away.
const promptJumpViewport = 10

// buildPromptTranscript lays out one user prompt per entry in
// answerLineCounts, each followed by an assistant answer of that many lines.
// The window shapes are the runtime ones: AppendOrUpdate starts assistant
// text unfolded and everything else folded, so a prompt costs a single row
// and an answer costs its line count.
func buildPromptTranscript(answerLineCounts ...int) (DisplayModel, *WindowBuffer) {
	wb := NewWindowBuffer(80, DefaultStyles())
	for i, lines := range answerLineCounts {
		wb.AppendOrUpdate(tlv.TagUserT, promptID(i), "prompt text")
		wb.AppendOrUpdate(tlv.TagAssistantT, answerID(i), strings.Repeat("answer line\n", lines))
	}
	d := NewDisplayModel(wb, DefaultStyles()).WithDisplayFocused(true).WithHeight(promptJumpViewport)
	return d.updateContent(), wb
}

// User prompt i and its answer i are windows 2i and 2i+1.
func promptID(i int) string    { return "prompt-" + string(rune('a'+i)) }
func answerID(i int) string    { return "answer-" + string(rune('a'+i)) }
func promptIndex(turn int) int { return 2 * turn }
func answerIndex(turn int) int { return 2*turn + 1 }

// TestPressFWithNoPromptBelowKeepsViewportPut verifies the reported bug: with
// the cursor parked below the last user prompt, f has nowhere to jump and
// must leave the screen exactly as it was.
func TestPressFWithNoPromptBelowKeepsViewportPut(t *testing.T) {
	display, wb := buildPromptTranscript(5, 5, 20)

	// Cursor on the final answer, the last window in the transcript, and the
	// view scrolled to the bottom of it. WithWindowCursor drops auto-follow,
	// which is what makes the yank stick instead of being silently
	// overwritten by the next follow-to-bottom render.
	display = display.WithWindowCursor(answerIndex(2))
	display = display.GotoBottom().updateContent()

	cursorBefore, offsetBefore := display.GetWindowCursor(), display.YOffset()
	if display.autoFollow {
		t.Fatal("fixture kept auto-follow on; the scrolled-back view is the state under test")
	}
	if offsetBefore == headLine(t, wb, cursorBefore) {
		t.Fatal("fixture is already seated at the window head; the yank would go undetected")
	}

	updated, _ := display.Update(KeyPressMsg(Key{Text: "f"}))

	if got := updated.GetWindowCursor(); got != cursorBefore {
		t.Errorf("cursor moved with no prompt below it: %d -> %d", cursorBefore, got)
	}
	if got := updated.YOffset(); got != offsetBefore {
		t.Errorf("viewport moved with the cursor pinned at the end: %d -> %d (window head is %d)",
			got, offsetBefore, headLine(t, wb, cursorBefore))
	}
}

// TestPressFFollowingLeavesAutoFollowOn covers the other end-of-transcript
// state: the reader never scrolled back, so f is pressed with auto-follow
// still on, and a jump that failed must not disturb following — turning it
// off lives in setCursor, which a miss never reaches.
//
// This one is not a regression guard for the yank. Under auto-follow,
// updateContent ends in GotoBottom, which silently undoes the stray scroll
// within the same keypress; the view only flickers. The scrolled-back state
// above is where the damage persists.
func TestPressFFollowingLeavesAutoFollowOn(t *testing.T) {
	display, _ := buildPromptTranscript(5, 5, 20)

	// G: cursor to the last window and follow it.
	display, _ = display.Update(KeyPressMsg(Key{Text: "G"}))
	display = display.updateContent()
	if !display.autoFollow {
		t.Fatal("fixture lost auto-follow; G should have engaged it")
	}

	cursorBefore, offsetBefore := display.GetWindowCursor(), display.YOffset()

	updated, _ := display.Update(KeyPressMsg(Key{Text: "f"}))

	if got := updated.GetWindowCursor(); got != cursorBefore {
		t.Errorf("cursor moved with no prompt below it: %d -> %d", cursorBefore, got)
	}
	if !updated.autoFollow {
		t.Error("a failed jump disabled auto-follow; nothing moved, so nothing should have been turned off")
	}
	if got := updated.YOffset(); got != offsetBefore {
		t.Errorf("a failed jump moved the viewport under auto-follow: %d -> %d", offsetBefore, got)
	}
}

// TestPressFRepeatedlyFromLastPromptIsStable walks f past the final prompt the
// way a user does: twice to reach it, then again onto the answer below it.
// Holds the two things the fix must not break by accident: the jump stops at
// the last prompt rather than wrapping round to the first, and repeating it
// leaves the view where it is.
func TestPressFRepeatedlyFromLastPromptIsStable(t *testing.T) {
	display, _ := buildPromptTranscript(5, 5, 20)
	display = display.WithWindowCursor(promptIndex(0))

	// First press: to prompt 1. Second: to prompt 2, the last one.
	display, _ = display.Update(KeyPressMsg(Key{Text: "f"}))
	display, _ = display.Update(KeyPressMsg(Key{Text: "f"}))
	if got := display.GetWindowCursor(); got != promptIndex(2) {
		t.Fatalf("expected the cursor on the last prompt (%d), got %d", promptIndex(2), got)
	}

	// Third press onward: no prompt below, so the cursor and the view hold
	// still.
	cursor, offset := display.GetWindowCursor(), display.YOffset()
	for range 5 {
		display, _ = display.Update(KeyPressMsg(Key{Text: "f"}))
		if got := display.GetWindowCursor(); got != cursor {
			t.Fatalf("cursor drifted on a press with nothing to jump to: %d -> %d", cursor, got)
		}
		if got := display.YOffset(); got != offset {
			t.Fatalf("viewport drifted on a press with nothing to jump to: %d -> %d", offset, got)
		}
	}
}

// TestPressBWithNoPromptAboveKeepsViewportPut is the mirror case: above the
// first prompt there is nothing to jump to either.
func TestPressBWithNoPromptAboveKeepsViewportPut(t *testing.T) {
	display, wb := buildPromptTranscript(5, 5, 20)

	display = display.WithWindowCursor(promptIndex(0))
	display = display.GotoBottom().updateContent()

	cursorBefore, offsetBefore := display.GetWindowCursor(), display.YOffset()

	updated, _ := display.Update(KeyPressMsg(Key{Text: "b"}))

	if got := updated.GetWindowCursor(); got != cursorBefore {
		t.Errorf("cursor moved with no prompt above it: %d -> %d", cursorBefore, got)
	}
	if got := updated.YOffset(); got != offsetBefore {
		t.Errorf("viewport moved with the cursor on the first prompt: %d -> %d (window head is %d)",
			got, offsetBefore, headLine(t, wb, cursorBefore))
	}
}

// TestPressFJumpsAndSeatsPromptAtTop guards the other half: a jump that does
// land must still re-seat, or the fix would have disabled the feature.
func TestPressFJumpsAndSeatsPromptAtTop(t *testing.T) {
	display, wb := buildPromptTranscript(5, 5, 20)
	display = display.WithWindowCursor(promptIndex(0))
	display = display.GotoTop().updateContent()

	display, _ = display.Update(KeyPressMsg(Key{Text: "f"}))

	if got := display.GetWindowCursor(); got != promptIndex(1) {
		t.Fatalf("f did not advance to the next prompt: cursor %d, want %d", got, promptIndex(1))
	}
	if got, want := display.YOffset(), headLine(t, wb, promptIndex(1)); got != want {
		t.Errorf("the landed prompt is not seated at the top: YOffset %d, want %d", got, want)
	}
	if display.autoFollow {
		t.Error("a jump that moved should have dropped auto-follow")
	}
}

// TestPressBJumpsAndSeatsPromptAtTop is the same guard for b.
func TestPressBJumpsAndSeatsPromptAtTop(t *testing.T) {
	display, wb := buildPromptTranscript(5, 5, 20)
	display = display.WithWindowCursor(answerIndex(2))
	display = display.GotoBottom().updateContent()

	display, _ = display.Update(KeyPressMsg(Key{Text: "b"}))

	if got := display.GetWindowCursor(); got != promptIndex(2) {
		t.Fatalf("b did not retreat to the previous prompt: cursor %d, want %d", got, promptIndex(2))
	}
	if got, want := display.YOffset(), headLine(t, wb, promptIndex(2)); got != want {
		t.Errorf("the landed prompt is not seated at the top: YOffset %d, want %d", got, want)
	}
}

// headLine is the document line a window starts on.
func headLine(t *testing.T, wb *WindowBuffer, index int) int {
	t.Helper()
	start, _ := wb.GetWindowLineRange(index)
	return start
}
