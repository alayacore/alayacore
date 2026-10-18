package terminal

// The two invariants a raw-passthrough renderer lives or dies by, checked
// against a terminal model rather than against the renderer's own arithmetic.
//
//  1. A frame fills exactly the screen. The base region must soft-wrap to
//     precisely the rows the layout reserved for it, so the transcript's last
//     row is the row above the live edge. The renderer never asks the terminal
//     how many rows a frame took — it counts them itself (positionedRows) and
//     pads to the viewport height — so a row that reaches the terminal wider
//     than the budget is a row the layout did not know about, and everything
//     below it lands one row low.
//
//  2. Repainting only what changed leaves the screen a full repaint would
//     leave. This is the promise Ctrl-R exists to restore: if the diff path and
//     the ED2 path can disagree, the disagreement is on the glass until the
//     user asks for a redraw, because a row whose text did not change is a row
//     the diff never touches again.
//
// Both were violated in normal use, and both violations looked like a terminal
// problem rather than an arithmetic one. See TestKeycapRowKeepsTheFrameHeight
// and TestOverlayBoxFitsANarrowPane for the two reports.

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/wire"
)

// frameHarness drives a real Terminal model and a real Screen, keeping the
// bytes Screen emits and the screen a terminal would show as a result.
type frameHarness struct {
	t             *testing.T
	width, height int
	m             Terminal
	screen        *Screen
	sink          *bytes.Buffer
	grid          *grid

	// host is the same bytes painted by a host whose width table is not
	// width.go's (nil unless the run is judging the containment contract —
	// see host_width_test.go). Every frame asserts the chrome contract on it,
	// and hostBefore is its screen as it stood before the frame being
	// asserted (nil for a frame that painted nothing).
	host       *grid
	hostBefore []string

	// written is every byte the Screen emitted, frame by frame, so a run can
	// be replayed into a real terminal (screen_tmux_replay_test.go).
	written [][]byte

	steps                  int
	diffFrames, fullFrames int
	lastView               *View // mirrors Program.lastView

	// noOverlayRowWraps is set by a run whose subject is that an overlay's rows
	// fit the pane (TestOverlayBoxFitsANarrowPane). A row wider than the screen
	// wraps into a second terminal row, and the wrapped remainder is exactly
	// the residue that test was written for, so every frame it paints is checked
	// for it.
	noOverlayRowWraps bool
}

func newFrameHarness(t *testing.T, width, height int) *frameHarness {
	t.Helper()
	h := &frameHarness{t: t, width: width, height: height, sink: &bytes.Buffer{}}
	out := NewTerminalOutput(DefaultStyles())
	m := NewTerminalWithTheme(out, nopWriteCloser{}, nil, width, height, theme.DefaultTheme(), nil, "theme-dark")
	m.out.SetScreenWidth(width)
	mm, _ := m.Update(ScreenSizeMsg{Width: width, Height: height})
	h.m = mm.(Terminal)
	h.screen = &Screen{out: h.sink, width: width, height: height}
	h.grid = newGrid(width, height)
	return h
}

// newFrameHarnessWithHost is newFrameHarness plus a screen model with a host's
// width arithmetic, so the run also checks the containment contract
// (host_width_test.go).
func newFrameHarnessWithHost(t *testing.T, width, height int) *frameHarness {
	t.Helper()
	h := newFrameHarness(t, width, height)
	h.host = newHostGrid(width, height)
	return h
}

// step advances the display the way a tick does, renders one frame through the
// persistent Screen (so the diff path is what runs), and checks both
// invariants.
func (h *frameHarness) step(label string) {
	h.t.Helper()
	h.steps++
	h.m = h.m.handleDisplayRefresh()
	h.render(label)
}

// render mirrors Program.render — the identity skip included, because a frame
// the program declines to paint is a frame the screen must already be right
// about — then checks the result.
func (h *frameHarness) render(label string) {
	h.t.Helper()
	v := h.m.View()
	if h.lastView != nil &&
		h.lastView.Content == v.Content &&
		screenCursorsEqual(h.lastView.ScreenCursor, v.ScreenCursor) &&
		h.lastView.AltScreen == v.AltScreen &&
		h.lastView.Raw == v.Raw &&
		h.lastView.FullScreen == v.FullScreen {
		h.assertFrame(v, label+" [not repainted]")
		return
	}
	h.sink.Reset()
	h.hostBefore = nil
	if h.host != nil {
		h.hostBefore = h.host.text()
	}
	if err := h.screen.Render(v.Content, v.ScreenCursor, v.FullScreen); err != nil {
		h.t.Fatalf("%s: render: %v", label, err)
	}
	// Every frame this harness paints carries the synchronized-update wrapper
	// (screen_test.go) — checked here, once, instead of at each call site.
	if h.sink.Len() > 0 {
		assertSyncFrame(h.t, h.sink.String())
	}
	h.written = append(h.written, append([]byte(nil), h.sink.Bytes()...))
	h.grid.write(h.sink.Bytes())
	if h.host != nil {
		h.host.write(h.sink.Bytes())
	}
	switch {
	case bytes.Contains(h.sink.Bytes(), []byte("\x1b[2J")):
		h.fullFrames++
	case h.sink.Len() > 0:
		h.diffFrames++
	}
	last := v
	h.lastView = &last
	h.assertFrame(v, label)
}

// repaint is what Ctrl-R produces: a Screen with no history, so Render takes
// the ED2 + home + content path.
func (h *frameHarness) repaint(content string) *grid {
	var b bytes.Buffer
	s := &Screen{out: &b, width: h.width, height: h.height}
	_ = s.Render(content, nil, true)
	g := newGrid(h.width, h.height)
	g.write(b.Bytes())
	return g
}

func (h *frameHarness) assertFrame(v View, label string) {
	h.t.Helper()
	h.assertFillsScreen(v, label)
	if d := gridDiff(h.grid, h.repaint(v.Content)); len(d) > 0 {
		h.fail(label, "the painted screen is not the screen a full repaint of the same frame leaves:\n"+
			strings.Join(d, "\n"))
	}
	if h.host != nil {
		h.assertHostContainment(v, label, lastFrame(h.written), h.hostBefore)
	}
	if h.noOverlayRowWraps {
		h.assertOverlayRowsAreOneRowTall(v, label)
	}
}

// assertOverlayRowsAreOneRowTall fails when a row the frame anchors with an
// absolute cursor position — the overlay box, and nothing in the transcript —
// is wider than the screen and therefore wraps. positionedRows works that out
// the way a terminal would, so the check needs no knowledge of which component
// drew the row.
func (h *frameHarness) assertOverlayRowsAreOneRowTall(v View, label string) {
	h.t.Helper()
	for _, r := range positionedRows(v.Content, h.width) {
		if r.base || r.terminalRows == 1 {
			continue
		}
		h.fail(label, fmt.Sprintf("an overlay row is %d terminal rows tall on a %d-column screen, "+
			"so its tail wraps onto the row below: %q", r.terminalRows, h.width, r.frameRow.text))
	}
}

// lastFrame returns the most recent frame the Screen wrote, or nil.
func lastFrame(written [][]byte) []byte {
	if len(written) == 0 {
		return nil
	}
	return written[len(written)-1]
}

// chromeRows returns the terminal rows the frame owns from column 0 below the
// transcript region: the live edge, the input box and the status bar (tui.go →
// View). They are the rows a host's width disagreement must never be visible on.
func chromeRows(content string, width int) []int {
	rows := positionedRows(content, width)
	baseEnd := 0
	for _, r := range rows {
		if r.base {
			baseEnd = max(baseEnd, r.frameRow.row+r.terminalRows)
		}
	}
	var out []int
	for _, r := range rows {
		if r.base || r.frameRow.col != 0 || r.frameRow.row < baseEnd {
			continue
		}
		for row := r.frameRow.row; row < r.frameRow.row+r.terminalRows; row++ {
			out = append(out, row)
		}
	}
	return out
}

// assertHostContainment requires the two per-frame halves of the containment
// contract on a host whose width table disagrees with ours (docs/tui.md → Line
// Wrapping):
//
//  1. A frame's writes cannot reach ABOVE the topmost row it wrote. That is the
//     property that keeps a host's width error out of the rows the frame did not
//     touch — and it is the one a cell written past the bottom row breaks, since
//     that scrolls the terminal and moves every row up. before is the host's
//     screen as it stood before this frame.
//  2. The chrome rows — the rows the frame owns from column 0 — must be exactly
//     what the frame drew, whether or not this frame wrote them.
//
// What is deliberately NOT asserted here: the transcript rows a write can
// reach. A host that wraps a row elsewhere re-flows that row's line, and the
// re-flow can reach the row below it; that is the damage the contract confines
// to the transcript region, and the closure in screen.go → diffFrameRows repairs
// the part of it that lands on a row the frame does not own. That repair is
// pinned by the scenario tests in host_width_test.go, which can name the row
// they mean.
func (h *frameHarness) assertHostContainment(v View, label string, frame []byte, before []string) {
	h.t.Helper()
	if len(frame) == 0 || before == nil {
		return
	}
	wrote := rowsWritten(frame, h.height)
	if len(wrote) == 0 {
		return
	}
	top := h.height
	for row := range wrote {
		top = min(top, row)
	}
	after := h.host.text()
	for row := 0; row < top && row < len(before) && row < len(after); row++ {
		if before[row] != after[row] {
			h.fail(label, fmt.Sprintf(
				"a write at row %d changed row %d, which is above it:\n    before: %q\n    after:  %q",
				top, row, before[row], after[row]))
		}
	}
	h.assertChrome(v, label)
}

// rowsWritten returns the terminal rows a frame's bytes address: every row its
// CUPs name, or — for a frame that re-establishes the whole screen (ED2, or the
// home-and-flow first paint) — every row of the screen.
func rowsWritten(frame []byte, height int) map[int]bool {
	out := make(map[int]bool, height)
	if bytes.Contains(frame, []byte("\x1b[2J")) || bytes.Contains(frame, []byte("\x1b[H")) {
		for row := 0; row < height; row++ {
			out[row] = true
		}
		return out
	}
	for i := 0; i+1 < len(frame); i++ {
		if frame[i] != 0x1b || frame[i+1] != '[' {
			continue
		}
		j := i + 2
		for j < len(frame) && (frame[j] == ';' || (frame[j] >= '0' && frame[j] <= '9')) {
			j++
		}
		if j < len(frame) && frame[j] == 'H' {
			if row, _, ok := parseCUP(string(frame[i+1 : j+1])); ok {
				out[row] = true
			}
			i = j
		}
	}
	return out
}

// assertChrome requires the chrome rows to be exactly what the frame draws, on a
// host whose width table disagrees with ours. Those rows are positioned at
// absolute rows and cleared to their own end, so nothing a transcript row flowed
// onto them can survive beside what the frame drew — the containment contract
// (docs/tui.md → Line Wrapping; screen.go → diffFrameRows closes the write set
// downwards for the same reason).
//
// Only the chrome is required to match the frame here. The transcript region is
// allowed to differ: a host that wraps a row somewhere else re-flows that row's
// line, and no table choice can prevent that — the point of the contract is that
// the damage stays inside the transcript.
func (h *frameHarness) assertChrome(v View, label string) {
	h.t.Helper()
	want, got := h.grid.text(), h.host.text()
	for _, row := range chromeRows(v.Content, h.width) {
		if row >= len(want) || row >= len(got) {
			continue
		}
		if want[row] != got[row] {
			h.fail(label, fmt.Sprintf(
				"a host that bills a cluster differently damaged chrome row %d, which the frame owns from column 0:\n"+
					"    the frame: %q\n    the host:  %q", row, want[row], got[row]))
		}
	}
}

// assertFillsScreen checks invariant 1: the base rows end exactly where the
// first CUP-anchored row (the live edge) begins, and inside the screen.
func (h *frameHarness) assertFillsScreen(v View, label string) {
	h.t.Helper()
	rows := positionedRows(v.Content, h.width)
	baseEnd, firstCUP := 0, -1
	for _, r := range rows {
		if r.base {
			baseEnd = max(baseEnd, r.frameRow.row+r.terminalRows)
			continue
		}
		if firstCUP < 0 {
			firstCUP = r.frameRow.row
		}
	}
	if firstCUP >= 0 && baseEnd > firstCUP {
		h.fail(label, fmt.Sprintf(
			"the base region runs to terminal row %d, past the first CUP-anchored row %d: "+
				"%d row(s) of transcript land on the live edge and push the frame off the screen\n%s",
			baseEnd-1, firstCUP, baseEnd-firstCUP, rowDump(rows, firstCUP)))
	}
	if baseEnd > h.height {
		h.fail(label, fmt.Sprintf("the base region runs to terminal row %d on a %d-row screen",
			baseEnd-1, h.height))
	}
}

// rowDump lists the rows around the boundary a frame overran, so a failure
// names the row that did it instead of only the count.
func rowDump(rows []positionedRow, around int) string {
	var b strings.Builder
	for _, r := range rows {
		if r.frameRow.row+r.terminalRows < around-2 || r.frameRow.row > around+2 {
			continue
		}
		kind := "base"
		if !r.base {
			kind = "CUP "
		}
		fmt.Fprintf(&b, "    %s row=%d span=%d col=%d cells=%d %q\n",
			kind, r.frameRow.row, r.terminalRows, r.frameRow.col, cellWidth(r.frameRow.text), r.frameRow.text)
	}
	return b.String()
}

func (h *frameHarness) fail(label, why string) {
	h.t.Helper()
	h.t.Errorf("step %d (%s) at %dx%d: %s", h.steps, label, h.width, h.height, why)
}

// key presses and content, the two ways a frame changes.

func (h *frameHarness) key(code rune, mod KeyMod) {
	h.t.Helper()
	mm, _ := h.m.Update(KeyPressMsg(Key{Code: code, Mod: mod}))
	h.m = mm.(Terminal)
	h.step(fmt.Sprintf("key %q mod %d", code, mod))
}

// appendWin feeds content the way the session does — a frame through the
// output writer — so the dirty flags the refresh path gates on are the real
// ones.
func (h *frameHarness) appendWin(tag, id, content string) {
	h.t.Helper()
	h.m.out.Write(testFrame(tag, id, content))
	h.m.out.FlushPendingDeltas()
}

// resize mirrors the program's resize path: the model learns the new size and
// the Screen drops its caches, which is what forces the next frame to clear.
// The grid keeps its cells, clipped and extended, because that is what a
// terminal does to a screen it is told is now a different size.
func (h *frameHarness) resize(width, height int) {
	h.t.Helper()
	h.width, h.height = width, height
	mm, _ := h.m.Update(ScreenSizeMsg{Width: width, Height: height})
	h.m = mm.(Terminal)
	h.screen.Resize(width, height)
	old := h.grid
	g := newGrid(width, height)
	for r := 0; r < min(len(old.rows), height); r++ {
		for c := 0; c < min(len(old.rows[r]), width); c++ {
			g.rows[r][c] = old.rows[r][c]
		}
	}
	h.grid = g
	if h.host != nil {
		oldHost := h.host
		hg := newHostGrid(width, height)
		for r := 0; r < min(len(oldHost.rows), height); r++ {
			for c := 0; c < min(len(oldHost.rows[r]), width); c++ {
				hg.rows[r][c] = oldHost.rows[r][c]
			}
		}
		h.host = hg
	}
	h.step(fmt.Sprintf("resize %dx%d", width, height))
}

// redraw is Ctrl-R: both caches dropped, so the next frame is a full repaint.
func (h *frameHarness) redraw() {
	h.t.Helper()
	h.lastView = nil
	h.screen.Reset()
}

// ---------------------------------------------------------------------------
// The two reports these invariants exist for.
// ---------------------------------------------------------------------------

// TestKeycapRowKeepsTheFrameHeight is the report behind invariant 1: "a line of
// content squeezed onto the '- following -' row". A wrapped row reached the
// terminal one cell wider than its budget, so it took two terminal rows while
// the layout charged it one, and every row below landed one low. A full repaint
// hid it — the live edge is CUP-anchored and paints over whatever flowed onto
// its row — and the diff left it there, because the row's text had not changed.
//
// The width disagreement that produced the over-wide row is width.go's header,
// point 3. What this test adds is the frame-level consequence, and the reason
// it needed no unusual configuration: numbered lists written with keycap emoji
// are ordinary model output, and a pane narrow enough to reach the edge is a
// tmux split.
func TestKeycapRowKeepsTheFrameHeight(t *testing.T) {
	for _, tc := range []struct {
		name    string
		width   int
		content string
	}{
		{"keycap run", 40, strings.Repeat("1️⃣", 30)},
		{"keycap list", 40, "步骤如下：\n1️⃣ 第一步：安装依赖并确认环境变量已经正确设置好了\n2️⃣ 第二步：运行测试\n3️⃣ 第三步：部署上线\n"},
		{"ascii plus VS16", 24, strings.Repeat("a\uFE0F", 20)},
		{"keycap at the edge", 20, strings.Repeat("9️⃣", 15)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newFrameHarness(t, tc.width, 20)
			h.step("empty")
			h.appendWin(tagAssistantText, "w1", tc.content)
			h.step("content")

			// Every visual row the wrapper produced must fit the budget it
			// was given — the property whose failure the frame height then
			// reports as a misplaced row.
			for _, vl := range wrapVisualLines(tc.content, tc.width) {
				if w := cellWidth(vl.Text); w > tc.width {
					t.Errorf("wrapVisualLines returned a row of %d cells for a %d-cell budget: %q",
						w, tc.width, vl.Text)
				}
			}
			// And the frame must survive a scroll through it: the overflow
			// shows up wherever the viewport clips, not only at the bottom.
			for i := 0; i < 4; i++ {
				h.key('k', 0)
			}
			for i := 0; i < 4; i++ {
				h.key('j', 0)
			}
		})
	}
}

// TestOverlayBoxFitsANarrowPane is the overlay row that was never drawn. A box
// wider than the pane made every one of its rows a soft-wrap run, the runs'
// spans overlapped, and the diff skipped each row a previous run covered — so
// the box showed a blank where its rule belonged, or kept the tail of the row
// it replaced, until a full redraw.
//
// Two things changed and this test holds the first: renderOverlay bounds a row
// to the columns the box has, so a box on a narrow pane no longer wraps at all.
// The second — the diff's skip rule, which still has to be right for the one
// deliberate overlay run (ConfirmDialog.RenderOverlay) — is
// TestDiffRepaintsEveryOverlappingOverlayRun, in screen_diff_overlay_test.go.
// The panes here are narrower than the model selector's empty-list row.
func TestOverlayBoxFitsANarrowPane(t *testing.T) {
	for _, tc := range []struct{ width, height int }{{20, 40}, {24, 24}, {26, 30}} {
		t.Run(fmt.Sprintf("%dx%d", tc.width, tc.height), func(t *testing.T) {
			h := newFrameHarness(t, tc.width, tc.height)
			h.noOverlayRowWraps = true
			for i := 0; i < 8; i++ {
				h.appendWin(tagAssistantText, fmt.Sprintf("w%d", i),
					strings.Repeat(fmt.Sprintf("message %d line of text that wraps around. ", i), 3))
				h.step(fmt.Sprintf("build %d", i))
			}
			// The model selector's empty-list row ("No models match your
			// search.", 28 cells) is the row no component clamps, so on a
			// pane narrower than that the box is wider than the screen.
			h.key('l', ModCtrl)
			h.key('j', 0)
			h.key('k', 0)
			for _, r := range "gpt" {
				h.key(r, 0)
			}
			h.key(KeyEsc, 0)
			h.key(KeyF1, 0)
			h.key(KeyEsc, 0)
			h.key('p', ModCtrl)
			h.key(KeyEsc, 0)
		})
	}
}

// TestOverlayRowFitsTheScreen pins the clamp itself: no row renderOverlay
// emits may be wider than the columns it has, whatever the box asked for.
func TestOverlayRowFitsTheScreen(t *testing.T) {
	box := strings.Join([]string{
		"┌────────────────────────────┐",
		"│ No models match your search.│",
		"└────────────────────────────┘",
	}, "\n")
	for _, screenWidth := range []int{12, 20, 28, 34, 80} {
		frame := renderOverlay("base", box, screenWidth, 24, 0)
		for _, r := range positionedRows(frame, screenWidth) {
			if r.base {
				continue
			}
			if w := cellWidth(r.frameRow.text); w > screenWidth {
				t.Errorf("screen %d wide: overlay row is %d cells and would soft-wrap: %q",
					screenWidth, w, r.frameRow.text)
			}
			if r.terminalRows != 1 {
				t.Errorf("screen %d wide: overlay row spans %d terminal rows, want 1",
					screenWidth, r.terminalRows)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// The same two invariants over a long random session.
// ---------------------------------------------------------------------------

// TestFrameInvariantsOverRandomSessions walks random sessions — content of the
// shapes that stress the row accounting, navigation, folds, overlays, typing,
// resizes and redraws — and checks both invariants after every frame. The
// shapes are chosen rather than uniform: rows that land exactly on the width,
// one cell either side of it, clusters the width libraries disagree about, and
// tabs (which the wrapper unfolds before it measures).
func TestFrameInvariantsOverRandomSessions(t *testing.T) {
	sizes := [][2]int{{24, 12}, {28, 24}, {37, 13}, {40, 10}, {60, 18}, {80, 24}, {100, 30}, {20, 40}}
	seeds := 12
	if testing.Short() {
		seeds = 3
	}
	for seed := 1; seed <= seeds; seed++ {
		sz := sizes[seed%len(sizes)]
		t.Run(fmt.Sprintf("seed %d at %dx%d", seed, sz[0], sz[1]), func(t *testing.T) {
			h := newFrameHarness(t, sz[0], sz[1])
			randomSession(h, rand.New(rand.NewSource(int64(seed))))
			t.Logf("%d frames: %d diffed, %d cleared", h.steps, h.diffFrames, h.fullFrames)
			if h.diffFrames == 0 {
				t.Error("the run never took the diff path, so it checked nothing")
			}
		})
	}
}

// sessionContent is one random window body: the shapes that stress the row
// accounting, weighted towards the ones a real transcript produces.
func sessionContent(rng *rand.Rand, width int) string {
	var b strings.Builder
	for i, n := 0, 1+rng.Intn(8); i < n; i++ {
		switch rng.Intn(10) {
		case 0:
			b.WriteString(strings.Repeat("x", width)) // exactly the budget
		case 1:
			b.WriteString(strings.Repeat("y", width-1))
		case 2:
			b.WriteString(strings.Repeat("z", width+1))
		case 3:
			b.WriteString(strings.Repeat("中文测试内容", 1+rng.Intn(max(1, width/4))))
		case 4:
			b.WriteString("col\t" + strings.Repeat("t\t", 1+rng.Intn(6)) + "end")
		case 5:
			b.WriteString(strings.Repeat("word ", 1+rng.Intn(max(1, width/3))))
		case 6:
			b.WriteString("")
		case 7:
			b.WriteString(strings.Repeat("q", 1+rng.Intn(width*3)))
		case 8:
			b.WriteString(strings.Repeat("1️⃣", 1+rng.Intn(max(1, width/2))))
		case 9:
			b.WriteString("步骤 " + strings.Repeat("2️⃣ 中文 ", 1+rng.Intn(max(1, width/6))))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// randomSession drives one session. The action mix is weighted towards content
// arriving, because that is what a session spends its time doing.
//
// one dispatch over the things a user can do; each case is a call
func randomSession(h *frameHarness, rng *rand.Rand) {
	nav := []rune{'j', 'k', 'J', 'K', 'G', 'g', 'b', 'e', 'r', 'R', 'f'}
	tags := []string{tagAssistantText, tagUserText, tagAssistantReasoning}
	typed := []string{"the ", "quick ", "中文 ", "テスト ", "x", "  ", "1️⃣", "\n"}

	steps := 250
	if testing.Short() {
		steps = 60
	}
	for step := 0; step < steps; step++ {
		switch rng.Intn(24) {
		case 0, 1, 2, 3:
			h.appendWin(tags[rng.Intn(len(tags))], fmt.Sprintf("w%d", rng.Intn(14)),
				sessionContent(rng, h.width))
		case 4:
			h.appendWin(tagAssistantText, fmt.Sprintf("w%d", rng.Intn(14)),
				strings.Repeat("q", 1+rng.Intn(20)))
		case 5:
			h.m.out.Write(toolCallFrames(fmt.Sprintf("t%d", rng.Intn(6)),
				"execute_command", sessionContent(rng, h.width), sessionContent(rng, h.width)))
			h.m.out.FlushPendingDeltas()
		case 6, 7, 8:
			h.key(nav[rng.Intn(len(nav))], 0)
		case 9:
			h.key(KeyTab, 0)
		case 10:
			h.key(KeyEsc, 0)
		case 11:
			h.key(KeyF1, 0)
		case 12:
			h.key('l', ModCtrl)
		case 13:
			h.key('p', ModCtrl)
		case 14:
			h.key('g', ModCtrl)
		case 15:
			for i, n := 0, 1+rng.Intn(10); i < n; i++ {
				w := typed[rng.Intn(len(typed))]
				if w == "\n" {
					h.key(KeyEnter, 0)
					continue
				}
				for _, r := range w {
					h.key(r, 0)
				}
			}
		case 16:
			h.key(KeyBackspace, 0)
		case 17:
			if wb := h.m.out.WindowBuffer(); wb.WindowCount() > 0 {
				wb.ToggleFold(rng.Intn(wb.WindowCount()))
			}
		case 18:
			if wb := h.m.out.WindowBuffer(); wb.WindowCount() > 0 {
				wb.ToggleMarkdownMode(rng.Intn(wb.WindowCount()))
			}
		case 19:
			h.resize(20+rng.Intn(120), 6+rng.Intn(40))
		case 20:
			h.redraw()
		case 21:
			tm, _ := h.m.handleTick()
			h.m = tm
		case 22:
			for i, n := 0, 1+rng.Intn(5); i < n; i++ {
				h.key(nav[rng.Intn(len(nav))], 0)
			}
		case 23:
			h.key(KeyEnter, 0)
		}
		h.step(fmt.Sprintf("step %d", step))
	}
}

// toolCallFrames builds the AF/UF pair a session sends for one tool call.
func toolCallFrames(id, name, input, output string) []byte {
	var b []byte
	b = append(b, testFrame(wire.TagAssistantF, id,
		fmt.Sprintf(`{"id":%s,"name":%s,"input":%s}`, jsonString(id), jsonString(name), jsonString(input)))...)
	b = append(b, testFrame(wire.TagUserF, id,
		fmt.Sprintf(`{"id":%s,"content":[{"type":"text","text":%s}],"is_error":false}`,
			jsonString(id), jsonString(output)))...)
	return b
}

// jsonString quotes s as a JSON string, escaping what a frame body cannot
// carry raw.
func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
