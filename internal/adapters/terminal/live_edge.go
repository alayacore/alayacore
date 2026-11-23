package terminal

// The live-edge row: the transcript's closing line, drawn on the one row
// between the last message and the input box's top rule.
//
//	- following -                    new output lands right here
//	- 12 lines hidden -              you are scrolled back; that much is hidden
//	- following, steering queued -   your prompt is waiting for the running task
//
// It replaces the "F↓" segment of the status bar, which had two problems.
// It reported a fact about the transcript from the row *below the input
// box*, so its arrow pointed at the prompt while the thing it meant was
// above it — and "F" asked the reader to already know it stands for
// "follow". Here the position is the meaning: the marker sits exactly on
// the line the newest content arrives at. The other problem was the glyph:
// U+2193 is East-Asian Ambiguous and was the one entry on the waiver list
// in glyphs_test.go that carried its own "candidate for an ASCII
// replacement" note. This row now draws no waived glyph at all — the frame
// is an ASCII hyphen (see the framing note below) — so nothing on it can
// move a cell on a double-width-ambiguous terminal.
//
// Both position states are worth the row. Following and a hidden tail are
// normally mutually exclusive — auto-follow pins the viewport to the document
// bottom in the same update that appends content — but `liveEdgeText` lets the
// label win if the two ever disagree, because "more is coming here" is the
// fact a user needs while output is streaming. Nothing is drawn when neither
// holds: reaching the last line with `J` after scrolling up (only `>`
// re-enables follow) leaves the whole transcript on screen, and rule 4 of
// the glyph policy (constants.go) — a marker that never changes is
// decoration — is why the row keeps quiet there instead of stating what is
// not the case.
//
// The row carries a second kind of fact: `steering queued` while the session has
// accepted a prompt for the running task and has not delivered it yet — a word,
// never a number. The batch is taken whole (spliced into the next step's request,
// or dropped with the task), so "how many" is not something a reader can act on,
// and a count would suggest a queue that drains a prompt at a time. The one word
// says the two things the sender needs: your words were taken, and they are not
// lost in a void.
//
// It is here rather than in the status bar for one reason: where the eye is. A
// prompt is typed at the input box, and the status bar is the row *below* it —
// the one part of the frame a reader watching their own words has no reason to
// look at. This row is directly above the box, on the boundary between what came
// back and what is about to be sent, which is where "the words I typed are
// waiting" belongs. The fact clears on the session's own report that the batch
// was delivered or dropped, so it is present exactly while it is true, and the
// words themselves appear in the transcript at the splice — the row says
// "waiting", the next thing the reader sees is them arriving.
//
// Two facts, one line: they are comma-joined ("following, steering queued") and
// either stands alone when the other is silent. A comma, not the " | " a chrome
// row lists FIELDS with (the status bar, the help bars): this row is a lowercase
// phrase framed by two hyphens, and ", " reads as one.
//
// The row says what it says at every width. A terminal too narrow for the
// label cuts the phrase between the two hyphens — the frame stays, because it
// is what makes the row a marker rather than a stray line of text, and because
// the alternative is a readout whose content is a function of how the user
// sized their window ("12 lines hidden" one resize ago, "steering queued" the
// next). The phrase is cut from its end, so what a very narrow row loses is the
// second fact; a row too narrow to hold the frame and even one cell of label
// reports nothing and stays blank.
//
// Color: dim. This row sits directly above the input box's top rule, and
// dim is what that rule and every other border is drawn with, so the label
// recedes into the frame rather than competing with it — the point: this
// marker is on most of the time and should be the quietest thing on screen.
// It shares the theme's dim with the chrome around it rather than the muted
// secondary-label color the status segment it replaced used. No bold either
// — the running task is marked by the spinner's motion, not by a color.
//
// Case: lowercase. Uppercase in this UI is a block heading (`USER PROMPT`,
// `TOOL CALL`, padded to FoldedLabelWidth), and a heading here would be
// read as one more window title, with the eye looking for its content box
// underneath. Runtime readouts are lowercase throughout — the status
// segments, the placeholder, the help bars.
//
// The frame is one ASCII hyphen on each side: "- label -". ASCII "-" is one
// cell in every terminal, so the row cannot shift on a double-width-
// ambiguous host; the box-drawing rule it replaced ("──") is East-Asian
// Ambiguous and had to be billed at its worst-case width (see
// renderLiveEdge). The old rule was chosen because "a chrome line must not
// be spellable as content" — the reason `Separator` is still "───" — and
// "- x -" is a markdown list item. That trade-off is accepted here: the
// frame is written in raw passthrough mode and travels with a screen
// selection, but this row is an ambient readout, not content a reader is
// meant to copy.
//
// The user-facing description is "Live Edge" in docs/tui.md.

import (
	"fmt"
)

const (
	// liveEdgeRows is the display height the layout spends on the row.
	liveEdgeRows = 1

	// liveEdgeFrame is the mark on each side of the label — one ASCII
	// hyphen, so the row reads "- following -". ASCII is one cell in every
	// terminal (constants.go), which is why the box-drawing rule the old
	// frame used is gone: it is East-Asian Ambiguous and forced the width
	// check to bill each dash at two cells.
	liveEdgeFrame = "-"

	// liveEdgeFollowing is the label while auto-follow pins the viewport
	// to the newest line. The word is the one the docs use for `>`
	// ("Follow the last window"), so pressing it and watching this appear
	// explains the key without a manual.
	liveEdgeFollowing = "following"

	// liveEdgeSteering is the label while the session holds a prompt for the
	// running task. The phrase is the one the session reports the lifecycle
	// with — one status, one name — and it carries no number: see the note at
	// the top of this file for why a count would be a lie.
	liveEdgeSteering = "steering queued"

	// liveEdgeSeparator joins the row's two facts when both have something
	// to say. A comma, not the " | " of a chrome row's field list (the
	// status bar, the help bars): this row is a phrase, and ", " reads as
	// one. ASCII, so it is one cell in every terminal (constants.go).
	liveEdgeSeparator = ", "
)

// lineCountText spells a count of lines the frame did not draw out: "1 line
// hidden", "12 lines hidden". Singular for one — "1 lines hidden" is the kind of
// thing a reader notices — and shared by the three places that count such lines,
// so they cannot disagree about how to say it.
//
// They count different things and say the same true thing about each: the live
// edge counts DOCUMENT lines under the viewport (any window), the pinned row
// counts the lines of THE ONE window whose own line it is drawing (the message
// you are in the middle of), and the diff block counts unchanged lines between
// two hunks — the only one of the three where the hidden lines are known to be
// uninteresting. Which lines they are is the row's own context (see
// liveEdgeText, Window.pinnedLine0 and diffRow.text).
func lineCountText(n int) string {
	if n == 1 {
		return "1 line hidden"
	}
	return fmt.Sprintf("%d lines hidden", n)
}

// liveEdgeText returns the plain label for the current display state, or "" when
// the row has nothing to report: the viewport's position and, when a prompt is
// waiting, the fact that it is, comma-joined. `following` and `linesBelow`
// describe the same fact (where the viewport is), so they never both contribute.
func liveEdgeText(following bool, linesBelow int, steeringQueued bool) string {
	position := ""
	switch {
	case following:
		position = liveEdgeFollowing
	case linesBelow > 0:
		position = lineCountText(linesBelow)
	}
	steering := ""
	if steeringQueued {
		steering = liveEdgeSteering
	}
	switch {
	case position == "":
		return steering
	case steering == "":
		return position
	default:
		return position + liveEdgeSeparator + steering
	}
}

// renderLiveEdge renders the live-edge row, or "" when it has nothing to
// say (the row is still reserved — see liveEdgeRows).
//
// Both facts are read at render time rather than baked into a cached string: the
// "F↓" this replaces lived inside m.statusLeft, which updateStatus rebuilds only
// when the session status version moves — while the state it shows flips on
// j/k/G/scroll, none of which move that version. That mismatch was a real
// staleness bug and needed a last-rendered-value tracker to patch; View()
// re-derives the row from the live state, so the bug cannot recur here
// (TestLiveEdgeReadsStateAtRenderTime is its regression test). The steering fact
// comes from a status snapshot per render for the same reason — it is live state,
// not a cached segment — and that snapshot takes sessionState.mu on its own,
// never nested under WindowBuffer.mu (the lock order output.go records).
func (m Terminal) renderLiveEdge() string {
	if m.screenWidth <= 0 {
		return ""
	}
	label := liveEdgeText(m.display.shouldFollow(), m.display.LinesBelow(),
		m.out.SnapshotStatus().SteeringQueued)
	if label == "" {
		return ""
	}

	// The row must not overflow its width: a soft-wrapped marker adds a row
	// to the frame and breaks the height the layout reserved. The frame is
	// never dropped and the label is never swapped for a shorter one, though:
	// what the row says must not depend on how wide the terminal is, or the
	// same session reads as a different readout at a different width. What
	// gives way is the phrase between the two hyphens — truncated like any
	// other text, with the frame still closing the row. The row is pure ASCII,
	// so there is no ambiguous-glyph allowance to bill — the box-drawing frame
	// this replaced needed one.
	line := liveEdgeFrame + " " + label + " " + liveEdgeFrame
	if Width(line) > m.screenWidth {
		// The frame costs its two hyphens and the spaces around the label; the
		// rest is the phrase's. A row with no room for the frame and one cell
		// of phrase draws nothing at all: an empty frame ("-  -") would be a
		// readout of nothing, on a row too narrow to be reading status from.
		labelBudget := m.screenWidth - 2*Width(liveEdgeFrame) - 2
		if labelBudget <= 0 {
			return ""
		}
		// The truncation ellipsis is Ambiguous — truncateWithSuffix leaves it a
		// cell.
		line = liveEdgeFrame + " " + truncateWithSuffix(label, labelBudget) + " " + liveEdgeFrame
	}

	// Dim in every state: it is the color of the rules and borders, and
	// this row is the input box's top rule's neighbor. An overlay dims the
	// whole background to the same color, so there is nothing to switch.
	return NewStyle().Foreground(m.styles.ColorDim).Render(line)
}
