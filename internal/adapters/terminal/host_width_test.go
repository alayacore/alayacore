package terminal

// The containment contract, judged against a host whose width table is not
// ours.
//
// The frame is a raw passthrough: rows are written to the terminal and the
// terminal soft-wraps them, so a row boundary is the HOST's decision (docs/tui.md
// → Line Wrapping). A host that bills a cluster a different number of cells than
// width.go does therefore wraps a row somewhere else than the layout counted —
// and what this file checks is the part of that the frame controls: the damage
// must not reach the rows the frame owns from column 0, and a row a write
// touched must not leave a stray cell on the row below it.
//
// The host model bills the classes host_width.go holds — the ones a host was
// MEASURED to disagree about, with the numbers tmux 3.7c gave
// (misc/probe-tmux-widths.sh): one cell per cluster, and for the marks that ride
// a base character that is under one row's width, which is the bound the
// write-set closure in screen.go → diffFrameRows is sized for. The two classes
// that cost this table nothing on their own (the soft hyphen, the Arabic number
// sign) and the ZWJ sequences it clusters are why the model reaches past one
// successor: a spill can be wider than a row.
//
// The grid model's own displaywidth table is NOT replaced: this is the same
// model with a host's arithmetic, so the two can be compared row by row.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/wire"
	"github.com/alayacore/alayacore/internal/wiretest"
)

// hostCells returns the cells the measured host charges s — this table's width
// plus every delta hostDelta applies, positive or negative. It is the oracle the
// probe's recorded numbers are asserted against (TestHostDeltaMatchesTheMeasuredHost);
// a LAYOUT reserves space with hostMaxCells, because a reservation must never be
// short. Test-only: production needs the upper bound, not the measured sum.
func hostCells(s string) int {
	cells := 0
	walkCells(s, func(text string, w int) bool {
		cells += w + hostDelta(text)
		return true
	})
	return cells
}

// hostMayBillDifferently reports whether s carries a cluster whose width this
// package's table and a host's are measured — or recorded as likely — to
// disagree about, so a screen comparison that reads the cluster back cannot be
// judged on it:
//
//   - a spacing mark the host bills on its own (hostWiderByOne),
//   - the cell a variation selector joined (a host that ignores the request
//     draws one cell where the table reserves two — the glyph policy's exposure,
//     not a measured difference: tmux agrees with the table on every such pair),
//   - a zero-width joiner, one cluster here and several to a host that does not
//     cluster,
//   - U+3164 HANGUL FILLER or U+1160, which the host ignores, and a lone regional
//     indicator, which its width cache bills one cell.
//
// The list is the measured classes plus the two the glyph policy records as
// host-dependent. Everything else — ASCII, CJK, box drawing, braille, the tool
// indicators — is asserted where it is compared. Test-only: this is the waiver
// screen_tmux_replay_test.go needs, not something the adapter draws by.
func hostMayBillDifferently(s string) bool {
	if strings.ContainsAny(s, "\uFE0E\uFE0F\u20E3\u200D") {
		return true
	}
	for _, r := range s {
		if hostWiderByOne[r] || hostNarrowerBy[r] != 0 {
			return true
		}
		if r >= 0x1F1E6 && r <= 0x1F1FF {
			return true
		}
	}
	return false
}

// newHostGrid returns the screen model with a host's width arithmetic in place
// of this package's (see the file comment).
func newHostGrid(width, height int) *grid {
	g := newGrid(width, height)
	g.host = true
	return g
}

// streamText feeds a text DELTA to a window — the way the session streams. The
// authoritative content frame appendWin sends is only displayed when no window
// exists yet (output.go keeps it for the replay case), so a run that grows a
// window it already has sends the delta tag.
func (h *frameHarness) streamText(id, text string) {
	h.t.Helper()
	h.m.out.Write(frameBytes(wire.TagAssistantXDelta, wiretest.AssistantTextDelta(id, text)))
	h.m.out.FlushPendingDeltas()
}

// fillerRows builds a window body of n plain lines, so a fixture can place its
// offending row at a chosen row of the screen: the transcript fills the display
// down to the row before the chrome, which is what makes a wrapped row land on
// the live edge's row instead of on a blank one.
func fillerRows(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "line %d of the window\n", i)
	}
	return b.String()
}

// TestHostWidthDisagreementStaysOffTheChrome is the reported symptom, pinned on
// a host that bills differently: a transcript row that ends ON the frame's width
// and carries a Devanagari vowel sign is one cell wider to the host, so the host
// wraps it and the cell that does not fit lands on the row below it — which, for
// the last transcript row, is the live edge's row. The marker is constant while
// output streams, so the row diff does not rewrite that row, and the stray cell
// (then the EL that ends the wrapped write) sits on the chrome until a full
// repaint.
//
// The frame now re-establishes every row it owns below a written transcript row,
// so the chrome is the frame's own — which is what assertFrame checks on every
// frame of this run (screen_repaint_invariant_test.go → assertHostContainment).
func TestHostWidthDisagreementStaysOffTheChrome(t *testing.T) {
	const width, height = 40, 20
	h := newFrameHarnessWithHost(t, width, height)

	h.step("empty")
	// The transcript fills the display region (height − input box − live edge −
	// status bar = 15 rows here): one window whose own line plus body rows reach
	// the row above the live edge, with the offending row last. The initial
	// content stops one cluster short of the row, so the delta below is what
	// makes it overflow.
	h.appendWin(tagAssistantText, "w1", fillerRows(13)+strings.Repeat("x", 39)+"क")
	h.step("content")

	// The delta grows that row by the vowel sign: to this table the cluster
	// costs nothing (it is one cell with its base either way), to the host one
	// cell. Only that row's text changes, so only that row is written — and the
	// row below it, the live edge, keeps its text.
	h.streamText("w1", "\u093F")
	h.step("delta")

	// A navigation frame, which rewrites no transcript row at all.
	h.key('k', 0)
	h.key('j', 0)

	// Ctrl-R: the frame is written whole (ED2 + flow) instead of row by row, so
	// a spill reaches the chrome by the other route — the row above it flows
	// onto the row the frame painted, and only the row's own write can clear it.
	h.redraw()
	h.step("after Ctrl-R")
}

// TestAStrayCellOnTheNextLineIsRepaired pins the successor half of the closure:
// a row a write touches can push its tail onto the row below it, and the row
// below is — by construction — a row whose own text did NOT change (it is the
// next window's line). Nothing else would ever rewrite it, so the frame has to.
func TestAStrayCellOnTheNextLineIsRepaired(t *testing.T) {
	const width, height = 40, 20
	h := newFrameHarnessWithHost(t, width, height)

	h.step("empty")
	h.appendWin(tagAssistantText, "w1",
		fillerRows(6)+strings.Repeat("x", 39)+"क")
	h.appendWin(tagAssistantText, "w2", "the next window's first line\nthe next window's second line")
	h.step("two windows")

	h.streamText("w1", "\u093F")
	h.step("delta")

	// The only row that may differ is the row the delta rewrote (it is one cell
	// wider to the host, so the host cuts its last cell onto the row below —
	// which the closure then re-establishes). The next window's line, whose text
	// did not change in this frame and which nothing else would ever rewrite,
	// must be the frame's own.
	model, host := h.grid.text(), h.host.text()
	for row := range model {
		if row >= len(host) || model[row] == host[row] {
			continue
		}
		if !strings.Contains(model[row], "कि") {
			t.Errorf("the host damaged row %d, which this frame did not write:\n    the frame: %q\n    the host:  %q",
				row, model[row], host[row])
		}
	}
	for _, want := range []string{"the next window's first line", "the next window's second line"} {
		row := -1
		for r, text := range model {
			if text == want {
				row = r
				break
			}
		}
		if row < 0 {
			t.Fatalf("fixture: %q is not on screen", want)
		}
		if host[row] != want {
			t.Errorf("the row below the written one is %q, want %q — a stray cell from the row above", host[row], want)
		}
	}
}

// TestTheStatusRowReservesOnlyWhereAHostMayBillWider pins how the bottom row is
// kept from wrapping, and how much that costs.
//
// The bottom row is the one place a cell written past the last column has
// nowhere to go: it wraps, and the wrap scrolls the terminal, which moves rows a
// diff will never rewrite (screen_tmux_join_test.go measures the scroll itself).
// The status bar's right group carries the model name, which the session
// provides, so a name carrying a cluster a host bills wider would put the row
// one cell over — so the row is laid out against what a host may charge it
// (host_width.go → hostMaxCells). The reservation is then exactly the
// disagreement: a name no host can bill differently fills the window exactly, as
// it always did, and no session pays a blank cell for another session's name.
func TestTheStatusRowReservesOnlyWhereAHostMayBillWider(t *testing.T) {
	for _, tc := range []struct {
		name       string
		modelName  string
		reservedBy int // cells of the window the row does not spend
	}{
		{"plain name", "model-name", 0},
		{"name a host bills wider", "कि-model", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const width, height = 40, 20
			h := newFrameHarnessWithHost(t, width, height)

			h.step("empty")
			h.appendWin(tagAssistantText, "w1", "a line of the window\nand another")
			h.step("content")

			h.m.out.Write(encodeFrame(wire.TagSystemMsg,
				fmt.Sprintf(`{"type":"model","data":{"active_id":1,"active_name":%q,"context_limit":0}}`, tc.modelName)))
			h.m.out.FlushPendingDeltas()
			h.m = h.m.updateStatus()
			h.step("model name")

			row := stripANSI(h.m.renderStatusBar())
			if !strings.Contains(row, tc.modelName) {
				t.Fatalf("fixture: the model name is not in the status bar (%q)", row)
			}
			if got := hostMaxCells(row); got > width {
				t.Errorf("the status row is %d cells to a host, over the %d-cell window — the bottom row would wrap and scroll the frame", got, width)
			}
			if want := width - tc.reservedBy; Width(row) != want {
				t.Errorf("status row is %d cells wide, want %d (window %d, reserved %d)", Width(row), want, width, tc.reservedBy)
			}
		})
	}
}

// TestChromeSurvivesAHostThatBillsDifferently walks content of the measured
// classes — the classes a model or a paste can carry in ordinary text: Indic and
// Thai words, a soft hyphen, a Hangul filler, a row of ZWJ families — through a
// session on a host that bills them differently, and checks the containment
// contract after every frame. The prompt stays ASCII on purpose: a chrome row's
// text is app-owned, so the check is that the frame's own row is what is on
// screen, and typed content would put the host's arithmetic inside the row under
// test.
func TestChromeSurvivesAHostThatBillsDifferently(t *testing.T) {
	// Two sizes: the contract is about rows, not about a width, and a fixture
	// that only ever runs at one width cannot show that.
	for _, size := range [][2]int{{40, 20}, {60, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			hostSession(t, size[0], size[1])
		})
	}
}

func hostSession(t *testing.T, width, height int) {
	h := newFrameHarnessWithHost(t, width, height)

	h.step("empty")
	contents := []string{
		"步骤如下：\n1" + strings.Repeat("x", 37) + "कि\nand a last line",
		"यह एक हिंदी वाक्य है जो लाइन के अंत तक जाता है और आगे बढ़ता है",
		"ภาษาไทยเป็นภาษาที่มีเสียงวรรณยุกต์และสระหลายรูป",
		"a soft\u00ADhyphen in a word that is long enough to wrap over the frame width here",
		"a hangul filler \u3164 sits in this row and the host drops it entirely",
		"lone regional indicators \U0001F1E6 \U0001F1E7 and a pair \U0001F1E8\U0001F1F3",
		// A full row of ZWJ families: two cells each here, eight to a host that
		// does not cluster — so the row over-bills by more than its own width,
		// and what it wraps into is the chrome.
		strings.Repeat("👨\u200D👩\u200D👧\u200D👦", 20),
	}
	for i, content := range contents {
		h.appendWin(tagAssistantText, fmt.Sprintf("w%d", i), content)
		h.step(fmt.Sprintf("content %d", i))
	}
	// Scroll the whole transcript, so frames rewrite rows at every position
	// rather than only at the live edge.
	for i := 0; i < 6; i++ {
		h.key('k', 0)
	}
	for i := 0; i < 6; i++ {
		h.key('j', 0)
	}
	// A full repaint of the same session: ED2 + the frame flow, which is the
	// other way a spilled row reaches the chrome.
	h.redraw()
	h.step("after Ctrl-R")
}

// TestHostDeltaMatchesTheMeasuredHost pins host_width.go's classes to the numbers
// the probe took (misc/probe-tmux-widths.sh, tmux 3.7c): this table's cells for
// the bytes, and the cells the host drew for them. A class added, removed or
// re-numbered without re-taking the probe fails here, on the number, rather than
// in a reader's memory of one — and the assertion is on hostCells' output, not on
// the map, so a rule that stops being expressible as a per-code-point set is
// still checked.
//
// The agreeing rows are here on purpose: they are the classes a rule must NOT be
// invented for. Emoji presentation is the one that caught a wrong entry — a
// cluster ending in a variation selector is two cells to this table already, and
// two to tmux, so "a" + U+FE0F is 2 and 2, not the 1-and-2 a hand-written column
// claimed. The app draws CJK, box drawing and braille, and the model charges them
// what the table does.
//
// ZWJ sequences are NOT in this table: tmux clusters them (2 and 2, in the
// probe's agreeing group), while hostDelta models the recorded host that bills
// every emoji separately (docs/tui.md → the ZWJ family note). That pessimism is
// asserted on its own, in TestHostModelsTheWorseCaseForZWJSequences.
func TestHostDeltaMatchesTheMeasuredHost(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		app  int
		host int
	}{
		{"devanagari i-matra", "कि", 1, 2},
		{"devanagari aa-matra", "का", 1, 2},
		{"bengali i-matra", "কি", 1, 2},
		{"gurmukhi sihari", "ਕਿ", 1, 2},
		{"tamil aa-matra", "கா", 1, 2},
		{"malayalam i-matra", "കി", 1, 2},
		{"sinhala aeda", "\u0D9A\u0DDA", 1, 2},
		{"thai sara am", "กำ", 1, 2},
		{"khmer aa", "កា", 1, 2},
		{"soft hyphen after a", "a\u00AD", 1, 2},
		{"arabic number sign after a", "a\u0600", 1, 2},
		{"lone regional indicator", "\U0001F1E6", 2, 1},
		{"hangul filler", "\u3164", 2, 0},
		{"hangul jungseong filler", "\u1160", 1, 0},
		{"ascii a + vs16", "a\uFE0F", 2, 2},
		{"digit one + vs16", "1\uFE0F", 2, 2},
		{"bang + vs16", "!\uFE0F", 2, 2},
		{"keycap one", "1\uFE0F\u20E3", 2, 2},
		{"ascii a + vs15", "a\uFE0E", 1, 1},
		{"regional pair", "\U0001F1E8\U0001F1F3", 2, 2},
		{"hangul choseong filler", "\u115F", 2, 2},
		{"halfwidth hangul filler", "\uFFA0", 1, 1},
		{"cjk", "中文", 4, 4},
		{"box rule", "─", 1, 1},
		{"braille", "⠿", 1, 1},
	} {
		if got := cellWidth(tc.in); got != tc.app {
			t.Errorf("%s: this table bills %q %d cells, the recorded number is %d", tc.name, tc.in, got, tc.app)
			continue
		}
		if got := hostCells(tc.in); got != tc.host {
			t.Errorf("%s: the host model bills %q %d cells, the probe recorded %d", tc.name, tc.in, got, tc.host)
		}
	}
}

// TestHostModelsTheWorseCaseForZWJSequences pins the one rule in host_width.go
// that is not a measurement of tmux: a host that does not cluster bills every
// emoji in a ZWJ sequence, and the model charges accordingly. It is what makes a
// spill wider than a row reachable in the containment tests at all — every
// measured class over-bills by one cell per cluster, and a cluster costs at least
// a cell, so measured classes alone can never wrap past the row below.
func TestHostModelsTheWorseCaseForZWJSequences(t *testing.T) {
	const family = "👨\u200D👩\u200D👧\u200D👦"
	if cellWidth(family) != 2 {
		t.Fatalf("fixture: this table bills the family %d cells, want 2", cellWidth(family))
	}
	if got := hostCells(family); got <= cellWidth(family) {
		t.Errorf("the host model bills the family %d cells, want more than this table's %d (a host that does not cluster draws every emoji)", got, cellWidth(family))
	}
}

// TestHostMaxCellsIsAnUpperBound pins the one property a reservation needs: the
// space a caller reserves for a string must never be less than what the host
// charges it, for every class in the table and their mixtures.
func TestHostMaxCellsIsAnUpperBound(t *testing.T) {
	for _, in := range []string{
		"", "plain ascii", "कि", "กำ", "a\u00AD", "\U0001F1E6", "\u3164",
		"1\uFE0F\u20E3", "👨\u200D👩\u200D👧\u200D👦", "a\uFE0F", "中文 ─ ⠿",
		"model-name कि-model", "R0",
	} {
		if want, got := hostCells(in), hostMaxCells(in); got < want {
			t.Errorf("hostMaxCells(%q) = %d, less than the host's %d", in, got, want)
		}
	}
}
