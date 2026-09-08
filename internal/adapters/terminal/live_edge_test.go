package terminal

// Tests for the live-edge row (live_edge.go): what it says in each display
// state, that it is read live at render time (the failure class "F↓" had as
// a segment baked into the cached status string), and that the row it takes
// is the row the layout reserved — without changing the frame's height.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/tlv"
)

// liveEdgeFixture returns a terminal with enough transcript to scroll, at
// the size newTestTerminal uses (80x24 → display 19 rows).
func liveEdgeFixture(windows int) Terminal {
	m := newTestTerminal()
	wb := m.out.WindowBuffer()
	for i := 0; i < windows; i++ {
		wb.AppendOrUpdate(tlv.TagAssistantT, "w"+itoa(i), "line "+itoa(i))
	}
	return m.updateDisplayHeight()
}

func TestLiveEdgeText(t *testing.T) {
	tests := []struct {
		name       string
		following  bool
		linesBelow int
		want       string
	}{
		{"following", true, 0, "following"},
		// The two cannot be true together (following pins the viewport to
		// the bottom), but following wins if a caller ever passes both.
		{"following wins over a count", true, 7, "following"},
		{"several lines below", false, 7, "7 lines below"},
		{"one line below", false, 1, "1 line below"},
		{"nothing below, nothing to say", false, 0, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := liveEdgeText(tc.following, tc.linesBelow); got != tc.want {
				t.Errorf("liveEdgeText(%v, %d) = %q, want %q", tc.following, tc.linesBelow, got, tc.want)
			}
		})
	}
}

func TestLiveEdgeFollows(t *testing.T) {
	m := liveEdgeFixture(20)
	if !m.display.shouldFollow() {
		t.Fatal("fixture: a fresh display follows the newest line")
	}
	if got := stripANSI(m.renderLiveEdge()); got != liveEdgeFrame+" following "+liveEdgeFrame {
		t.Errorf("renderLiveEdge() = %q, want the framed 'following'", got)
	}
}

// TestLiveEdgeCountsHiddenLines scrolls the viewport back and checks the row
// counts exactly what the viewport is hiding — the number is what makes the
// row worth its line, and it must be the lines BELOW the viewport, not the
// scroll offset or the document length.
func TestLiveEdgeCountsHiddenLines(t *testing.T) {
	m := liveEdgeFixture(20)
	m.display = m.display.MarkUserScrolled().ScrollUp(5)
	m = m.updateDisplayHeight()

	if got := m.display.LinesBelow(); got != 5 {
		t.Fatalf("LinesBelow() = %d after ScrollUp(5), want 5", got)
	}
	if got := stripANSI(m.renderLiveEdge()); got != "── 5 lines below ──" {
		t.Errorf("renderLiveEdge() = %q, want the framed line count", got)
	}

	// One line of slack reads singular, not "1 lines below".
	m.display = m.display.ScrollDown(4)
	m = m.updateDisplayHeight()
	if got := m.display.LinesBelow(); got != 1 {
		t.Fatalf("LinesBelow() = %d after scrolling 4 of 5 back down, want 1", got)
	}
	if got := stripANSI(m.renderLiveEdge()); got != "── 1 line below ──" {
		t.Errorf("one line below: got %q", got)
	}

	// Scrolled back to the very last line without re-enabling follow: the
	// viewport shows everything there is, so the row says nothing.
	m.display = m.display.GotoBottom()
	m = m.updateDisplayHeight()
	if m.display.shouldFollow() {
		t.Fatal("fixture: GotoBottom alone must not re-enable auto-follow")
	}
	if got := m.renderLiveEdge(); got != "" {
		t.Errorf("at the bottom but not following: renderLiveEdge() = %q, want blank", got)
	}
}

// TestLiveEdgeHiddenBelowShortDocument covers the transcript that fits on
// screen: nothing is hidden, so a scrolled-looking state must not invent a
// count (totalLines < viewport height would go negative unsupervised).
func TestLiveEdgeHiddenBelowShortDocument(t *testing.T) {
	m := liveEdgeFixture(1)
	if got := m.display.LinesBelow(); got != 0 {
		t.Errorf("LinesBelow() = %d with a transcript shorter than the viewport, want 0", got)
	}
}

// TestLiveEdgeReadsStateAtRenderTime is the regression that "F↓" carried:
// the indicator lived inside m.statusLeft, which updateStatus rebuilds only
// when the session status version moves. Navigation (j/k/scroll/G) moves no
// version, so the indicator went stale until the next session event. The
// marker is now derived from the display model while rendering, so a flip
// must show up without updateStatus running at all — and the status bar must
// not change, because it no longer carries the state.
func TestLiveEdgeReadsStateAtRenderTime(t *testing.T) {
	m := liveEdgeFixture(20)
	m = m.updateStatus()
	before := stripANSI(m.renderLiveEdge())
	statusBefore := stripANSI(m.renderStatusBar())

	// Scrolling back: the same public mutators the `K` key handler runs
	// (display.go — MarkUserScrolled + ScrollUp), without any session
	// event. updateStatus() is deliberately NOT called again.
	m.display = m.display.MarkUserScrolled().ScrollUp(5)
	m = m.updateDisplayHeight()

	after := stripANSI(m.renderLiveEdge())
	if before == after {
		t.Fatalf("live edge did not change after navigation (both %q)", after)
	}
	if !strings.Contains(after, "lines below") {
		t.Errorf("after scrolling back, live edge = %q, want the hidden-line count", after)
	}
	if status := stripANSI(m.renderStatusBar()); status != statusBefore {
		t.Errorf("status bar changed on navigation: %q → %q (the follow state must not live there)",
			statusBefore, status)
	}
	// The old marker is gone for good — the transcript state is nowhere in
	// the bar below the prompt.
	if strings.Contains(statusBefore, "F") || strings.Contains(after, "F↓") {
		t.Errorf("the 'F↓' marker reappeared: status=%q edge=%q", statusBefore, after)
	}
}

// TestLiveEdgeSitsAboveInputRule paints the frame and checks the row lands
// where the meaning is: the last row of the transcript area, immediately
// above the input box's top rule.
func TestLiveEdgeSitsAboveInputRule(t *testing.T) {
	m := liveEdgeFixture(20)
	const W, H = 80, 24

	v := m.View()
	grid := applyFrame(nil, v.Content, W)

	markerRow := H - m.input.Height() - 1 - liveEdgeRows // the reserved row
	if got := lineAt(grid, markerRow); got != "── following ──" {
		t.Errorf("live edge row %d = %q, want the marker", markerRow, got)
	}
	rule := lineAt(grid, markerRow+1)
	if !strings.HasPrefix(rule, "──") || Width(rule) != W {
		t.Errorf("row %d = %q, want the input box's full-width top rule", markerRow+1, rule)
	}
	// The display region must stop above the marker — otherwise the two
	// share a row and the shorter of the two leaves the other's tail on
	// screen.
	if got := m.display.GetHeight(); got != markerRow {
		t.Errorf("display height = %d, want %d (the marker row is outside it)", got, markerRow)
	}
}

// TestLiveEdgeKeepsFrameHeight pins the reason the row is reserved even when
// blank: the frame must soft-wrap to exactly the screen height in every
// state, or the input box and status bar drift off their rows.
func TestLiveEdgeKeepsFrameHeight(t *testing.T) {
	const H = 24

	following := liveEdgeFixture(20)
	if rows := len(applyFrame(nil, following.View().Content, 80)); rows != H {
		t.Errorf("frame height while following = %d rows, want %d", rows, H)
	}

	scrolled := following
	scrolled.display = scrolled.display.MarkUserScrolled().ScrollUp(3)
	scrolled = scrolled.updateDisplayHeight()
	if rows := len(applyFrame(nil, scrolled.View().Content, 80)); rows != H {
		t.Errorf("frame height while scrolled back = %d rows, want %d", rows, H)
	}

	// The blank case (nothing hidden, not following) draws no text but must
	// still consume its row.
	blank := following
	blank.display = blank.display.MarkUserScrolled().GotoBottom()
	blank = blank.updateDisplayHeight()
	if got := blank.renderLiveEdge(); got != "" {
		t.Fatalf("fixture: expected a blank live edge, got %q", got)
	}
	if rows := len(applyFrame(nil, blank.View().Content, 80)); rows != H {
		t.Errorf("frame height with a blank live edge = %d rows, want %d", rows, H)
	}
}

// TestLiveEdgeFitsItsWidth covers the row's own overflow rule: a marker that
// wrapped to a second row would push the frame past the height the layout
// reserved. The framed form gives way to the bare label, the bare label to a
// truncation, and nothing at all to a width with no room in it.
func TestLiveEdgeFitsItsWidth(t *testing.T) {
	m := liveEdgeFixture(20)

	// The framing glyph is Ambiguous, so it is billed at two cells per
	// dash: the framed form is only kept where it still fits doubled.
	full := liveEdgeFrame + " following " + liveEdgeFrame
	needed := Width(full) + 2*liveEdgeFrameCells
	for _, width := range []int{needed, needed - 1, Width(full), 12, 5, 1, 0} {
		m.windowWidth = width
		got := stripANSI(m.renderLiveEdge())
		if Width(got) > width {
			t.Errorf("width %d: rendered %q is %d cells, over the terminal", width, got, Width(got))
		}
		if width == 0 && got != "" {
			t.Errorf("width 0: got %q, want nothing rendered", got)
		}
		if width >= needed && got != full {
			t.Errorf("width %d: got %q, want the framed form %q", width, got, full)
		}
		if width < needed && width > 0 && got != "" && strings.HasPrefix(got, liveEdgeFrame) &&
			strings.HasSuffix(got, liveEdgeFrame) {
			t.Errorf("width %d: kept the frame without its ambiguity allowance: %q", width, got)
		}
	}
}

// TestLiveEdgeColors checks the two documented colors: muted as a secondary
// label, dim under an overlay where the whole background recedes. Both are
// compared against the styles pipeline rather than a hardcoded escape
// sequence, so a theme switch cannot make this test lie.
func TestLiveEdgeColors(t *testing.T) {
	m := liveEdgeFixture(20)
	line := liveEdgeFrame + " following " + liveEdgeFrame

	wantMuted := NewStyle().Foreground(m.styles.ColorMuted).Render(line)
	if got := m.renderLiveEdge(); got != wantMuted {
		t.Errorf("live edge style = %q, want the muted label style %q", got, wantMuted)
	}

	m.confirmOverlay = m.confirmOverlay.OpenQuit()
	if !m.isBlocked() {
		t.Fatal("fixture: the confirm dialog must block the background")
	}
	wantDim := NewStyle().Foreground(m.styles.ColorDim).Render(line)
	if got := m.renderLiveEdge(); got != wantDim {
		t.Errorf("live edge under an overlay = %q, want the dim style %q", got, wantDim)
	}
}

// TestLiveEdgeRepaintLeavesNoResidue covers the marker row through the
// screen's row diff. The row is CUP-anchored and its text changes length
// with the state — "── 23 lines below ──" shrinking to "── following ──", or
// the whole marker going quiet — and a repainted overlay row that shrinks
// must have its old tail erased, or the frame keeps a stale fragment of the
// previous count.
func TestLiveEdgeRepaintLeavesNoResidue(t *testing.T) {
	const W = 80
	m := liveEdgeFixture(20)
	markerRow := m.windowHeight - m.input.Height() - 1 - liveEdgeRows

	paint := func(t *testing.T, frames ...Terminal) [][]rune {
		t.Helper()
		s := &Screen{out: &bytes.Buffer{}}
		s.Resize(W, m.windowHeight)
		var grid [][]rune
		for i, f := range frames {
			s.out.(*bytes.Buffer).Reset()
			if err := s.Render(f.View().Content, nil, true); err != nil {
				t.Fatalf("frame %d: %v", i, err)
			}
			grid = applyFrame(grid, s.out.(*bytes.Buffer).String(), W)
		}
		return grid
	}

	// Quiet: the marker vanishes entirely.
	quiet := m
	quiet.display = quiet.display.MarkUserScrolled().GotoBottom()
	quiet = quiet.updateDisplayHeight()
	if got := quiet.renderLiveEdge(); got != "" {
		t.Fatalf("fixture: expected a blank live edge, got %q", got)
	}
	if got := lineAt(paint(t, m, quiet), markerRow); got != "" {
		t.Errorf("after the marker went quiet, row %d = %q, want blank", markerRow, got)
	}

	// Shrunk: a long count replaced by the short label.
	far := m
	far.display = far.display.MarkUserScrolled().ScrollUp(23)
	far = far.updateDisplayHeight()
	if got := stripANSI(far.renderLiveEdge()); got != "── 23 lines below ──" {
		t.Fatalf("fixture: got %q, want the 23-line count", got)
	}
	grid := paint(t, far, m)
	if got := lineAt(grid, markerRow); got != "── following ──" {
		t.Errorf("after the marker shrank, row %d = %q, want %q", markerRow, got, "── following ──")
	}
}

// TestLiveEdgeFrameCellsAgreesWithFrame keeps the constant the width check
// bills against equal to the glyph string it is billing.
func TestLiveEdgeFrameCellsAgreesWithFrame(t *testing.T) {
	if got := Width(liveEdgeFrame); got != liveEdgeFrameCells {
		t.Errorf("Width(liveEdgeFrame) = %d, liveEdgeFrameCells = %d", got, liveEdgeFrameCells)
	}
}
