package terminal

// The live-edge row: the transcript's closing line, drawn on the one row
// between the last message and the input box's top rule.
//
//	- following -            new output lands right here
//	- 12 lines below -       you are scrolled back; that much is hidden
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
// Both states are worth the row. Following and a hidden tail are normally
// mutually exclusive — auto-follow pins the viewport to the document bottom
// in the same update that appends content — but `liveEdgeText` lets the
// label win if the two ever disagree, because "more is coming here" is the
// fact a user needs while output is streaming. Nothing is drawn when neither
// holds: reaching the last line with `J` after scrolling up (only `G`
// re-enables follow) leaves the whole transcript on screen, and rule 4 of
// the glyph policy (constants.go) — a marker that never changes is
// decoration — is why the row keeps quiet there instead of stating what is
// not the case.
//
// Color: dim. This row sits directly above the input box's top rule, and
// dim is what that rule and every other border is drawn with, so the label
// recedes into the frame rather than competing with it — the point: this
// marker is on most of the time and should be the quietest thing on screen.
// It shares the theme's dim with the chrome around it rather than the muted
// secondary-label color the status segment it replaced used. No bold either
// — accent+bold belongs to the running-task dot.
//
// Case: lowercase. Uppercase in this UI is a block heading (`USER PROMPT`,
// `TOOL CALL`, padded to CollapsedLabelWidth), and a heading here would be
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
	// to the newest line. The word is the one the docs use for `G`
	// ("Follow the last window"), so pressing it and watching this appear
	// explains the key without a manual.
	liveEdgeFollowing = "following"
)

// lineCountText spells a count of hidden lines out: "1 line above", "12
// lines above". Singular for one — "1 lines above" is the kind of thing a
// reader notices — and shared by both places that count hidden transcript
// lines (the live edge's "below", the pinned row's "above"), so the two
// cannot disagree about how to say it.
//
// They count different things and say so: the live edge counts DOCUMENT
// lines under the viewport (any window), the pinned row counts the lines of
// THE ONE window whose own line it is drawing (the message you are in the
// middle of). See liveEdgeText and Window.pinnedLine0.
func lineCountText(n int, direction string) string {
	if n == 1 {
		return "1 line " + direction
	}
	return fmt.Sprintf("%d lines %s", n, direction)
}

// liveEdgeText returns the plain label for the current display state, or
// "" when the row has nothing to report.
func liveEdgeText(following bool, linesBelow int) string {
	switch {
	case following:
		return liveEdgeFollowing
	case linesBelow > 0:
		return lineCountText(linesBelow, "below")
	default:
		return ""
	}
}

// renderLiveEdge renders the live-edge row, or "" when it has nothing to
// say (the row is still reserved — see liveEdgeRows).
//
// Read from the display model at render time rather than baked into a
// cached string: the "F↓" this replaces lived inside m.statusLeft, which
// updateStatus rebuilds only when the session status version moves — while
// the state it shows flips on j/k/G/scroll, none of which move that
// version. That mismatch was a real staleness bug and needed a
// last-rendered-value tracker to patch; View() re-derives the row from the
// live state, so the bug cannot recur here
// (TestLiveEdgeReadsStateAtRenderTime is its regression test).
func (m Terminal) renderLiveEdge() string {
	if m.windowWidth <= 0 {
		return ""
	}
	label := liveEdgeText(m.display.shouldFollow(), m.display.LinesBelow())
	if label == "" {
		return ""
	}

	line := liveEdgeFrame + " " + label + " " + liveEdgeFrame
	// The row must not overflow its width: a soft-wrapped marker adds a row
	// to the frame and breaks the height the layout reserved. The bare label
	// is tried before truncating it, because a cut label ("- 12 li…") says
	// less than a full one without the frame. Both forms are pure ASCII, so
	// there is no ambiguous-glyph allowance to bill — the box-drawing frame
	// this replaced needed one.
	if Width(line) > m.windowWidth {
		line = label
	}
	if Width(line) > m.windowWidth {
		// The truncation ellipsis is Ambiguous — leave it a cell.
		line = truncateWithSuffix(line, max(0, m.windowWidth-1))
	}

	// Dim in every state: it is the color of the rules and borders, and
	// this row is the input box's top rule's neighbor. An overlay dims the
	// whole background to the same color, so there is nothing to switch.
	return NewStyle().Foreground(m.styles.ColorDim).Render(line)
}
