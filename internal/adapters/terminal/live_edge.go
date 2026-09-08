package terminal

// The live-edge row: the transcript's closing line, drawn on the one row
// between the last message and the input box's top rule.
//
//	── following ──          new output lands right here
//	── 12 lines below ──     you are scrolled back; that much is hidden
//
// It replaces the "F↓" segment of the status bar, which had two problems.
// It reported a fact about the transcript from the row *below the input
// box*, so its arrow pointed at the prompt while the thing it meant was
// above it — and "F" asked the reader to already know it stands for
// "follow". Here the position is the meaning: the marker sits exactly on
// the line the newest content arrives at. The other problem was the glyph:
// U+2193 is East-Asian Ambiguous and was the one entry on the waiver list
// in glyphs_test.go that carried its own "candidate for an ASCII
// replacement" note. The only non-ASCII glyph on this row is the framing
// rule, already on the box-drawing waiver, so the arrow has left the list.
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
// Color: muted, not dim. Dim is what the rules and borders are drawn with
// (theme conf: "unfocused borders"), and this row sits directly above the
// input box's top rule — in dim it reads as a piece of that frame instead
// of a label. Muted is the theme's secondary-label color, and it is the
// color the "F↓" it replaces had: status segments are muted, only the bar's
// separators and base are dim. No bold either — accent+bold belongs to the
// running-task dot, and a marker that is on most of the time must be the
// quietest thing on the screen.
//
// Case: lowercase. Uppercase in this UI is a block heading (`USER PROMPT`,
// `TOOL CALL`, padded to CollapsedLabelWidth), and a heading here would be
// read as one more window title, with the eye looking for its content box
// underneath. Runtime readouts are lowercase throughout — the status
// segments, the placeholder, the help bars.
//
// The framing dashes are the box-drawing rule, not ASCII "-": a chrome line
// must not be spellable as content (the reason `Separator` is "───" — see
// constants.go), and "- x -" is a markdown list item. That is not academic
// here: the frame is written in raw passthrough mode so that a screen
// selection copies cleanly, and this row would travel with the transcript.
//
// The user-facing description is "Live Edge" in docs/tui.md.

import (
	"fmt"
)

const (
	// liveEdgeRows is the display height the layout spends on the row.
	liveEdgeRows = 1

	// liveEdgeFrame is the rule run on each side of the label — two cells,
	// where `Separator` uses three: this is a label with an edge, not a
	// divider spanning a box.
	liveEdgeFrame = "──"

	// liveEdgeFrameCells is the display width of liveEdgeFrame, stated
	// separately because the width check above has to bill it twice (once
	// per side) at an Ambiguous glyph's worst-case cost. live_edge_test.go
	// pins it against the frame so the two cannot drift.
	liveEdgeFrameCells = 2

	// liveEdgeFollowing is the label while auto-follow pins the viewport
	// to the newest line. The word is the one the docs use for `G`
	// ("Follow the last window"), so pressing it and watching this appear
	// explains the key without a manual.
	liveEdgeFollowing = "following"
)

// liveEdgeText returns the plain label for the current display state, or
// "" when the row has nothing to report.
func liveEdgeText(following bool, linesBelow int) string {
	switch {
	case following:
		return liveEdgeFollowing
	case linesBelow == 1:
		return "1 line below"
	case linesBelow > 1:
		return fmt.Sprintf("%d lines below", linesBelow)
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
	// is tried before truncating it, because a cut label ("── 12 li…") says
	// less than a full one without the frame.
	//
	// The allowance is for the framing rule: U+2500 is East-Asian Ambiguous
	// (policy waiver 2a, constants.go) and a terminal configured for
	// double-width ambiguous characters draws each of the four dashes two
	// cells. Counting them at their worst cost keeps the framed form only
	// where it still fits doubled; the bare label is pure ASCII and cannot
	// move at all.
	if Width(line)+2*liveEdgeFrameCells > m.windowWidth {
		line = label
	}
	if Width(line) > m.windowWidth {
		// The truncation ellipsis is Ambiguous too — leave it a cell.
		line = truncateWithSuffix(line, max(0, m.windowWidth-1))
	}

	// Muted; dim under an overlay, where the whole background recedes
	// (same treatment the status bar gives its segments).
	style := NewStyle().Foreground(m.styles.ColorMuted)
	if m.isBlocked() {
		style = NewStyle().Foreground(m.styles.ColorDim)
	}
	return style.Render(line)
}
