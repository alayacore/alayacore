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
// The host model bills the classes that were MEASURED to disagree, with the
// numbers tmux 3.7c gave (misc/probe-tmux-widths.sh): a cluster carrying a
// spacing mark the host does not combine onto its base (the Indic and
// South-East Asian vowel signs), a soft hyphen or an Arabic number sign is one
// cell wider; the cell an emoji-presentation request (U+FE0F) joined is two
// cells wide to the host whatever this table made of it; a ZWJ sequence is
// several cells to a host that does not cluster; and a lone regional indicator
// and U+3164 HANGUL FILLER are narrower. Every class measured over-bills a
// cluster by at most one cell, and a cluster costs at least one cell — which is
// the bound the write-set closure in screen.go → diffFrameRows is sized for.
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

// hostWiderByOne are the code points the host billed one cell wider than this
// package's table, measured as part of a cluster. Each is a spacing mark the
// host does not combine onto the previous cell (this table's UAX #29 clustering
// does), or a format character this table charges nothing for.
var hostWiderByOne = map[rune]bool{
	0x093E: true, // devanagari aa-matra
	0x093F: true, // devanagari i-matra
	0x0940: true, // devanagari ii-matra
	0x09BE: true, // bengali aa-matra
	0x09BF: true, // bengali i-matra
	0x0A3F: true, // gurmukhi sihari
	0x0ABF: true, // gujarati i-matra
	0x0BBE: true, // tamil aa-matra
	0x0BBF: true, // tamil i-matra
	0x0D3E: true, // malayalam aa-matra
	0x0D3F: true, // malayalam i-matra
	0x0DDA: true, // sinhala aeda-pilla
	0x0E33: true, // thai sara am
	0x17B6: true, // khmer aa
	0x00AD: true, // soft hyphen
	0x0600: true, // arabic number sign
}

// hostNarrowerBy is the same, for the classes the host billed LESS than this
// table does: U+3164 HANGUL FILLER, which the host ignores entirely (0 cells
// where the table reserves two). A lone regional indicator is the other
// narrower class and is handled by hostDelta, because a PAIR of them agrees.
var hostNarrowerBy = map[rune]int{0x3164: 2}

// hostDelta returns the cells to add to the cells this package's table charges
// a cluster, to get the cells the measured host charges it.
func hostDelta(cluster string, cells int) int {
	delta := 0
	for _, r := range cluster {
		if hostWiderByOne[r] {
			delta++
		}
		delta -= hostNarrowerBy[r]
	}
	// A variation selector: the host makes the cell the selector joined TWO
	// cells wide (screen_write_combine forces the width when the sequence
	// should combine), whatever this table made of it. So a cluster this table
	// bills one cell is two to the host — measured on "a" + U+FE0F, which is
	// 1 here and 2 there — while a cluster it already bills two (a keycap, a
	// sun, a heart with the selector) agrees.
	if strings.Contains(cluster, "\uFE0F") && cells < 2 {
		delta += 2 - cells
	}
	// A zero-width joiner: a sequence this table draws as ONE cluster (two
	// cells, the first emoji's) is several to a host that does not cluster at
	// all — the class docs/tui.md records as "a ZWJ family is one cluster here
	// and several there", where such a host bills every emoji it can see. Each
	// joiner is one more emoji the host draws, at two cells.
	delta += 2 * strings.Count(cluster, "\u200D")
	if delta == 0 && isLoneRegionalIndicator(cluster) {
		// The host's default width cache bills a regional indicator one cell;
		// this table bills a lone one two (a pair is two on both).
		delta--
	}
	return delta
}

// isLoneRegionalIndicator reports whether the cluster is exactly one regional
// indicator code point.
func isLoneRegionalIndicator(cluster string) bool {
	runes := []rune(cluster)
	return len(runes) == 1 && runes[0] >= 0x1F1E6 && runes[0] <= 0x1F1FF
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

// TestAContestedModelNameCannotScrollTheFrame pins the one cell the status bar
// leaves unspent. The bar is the frame's BOTTOM row, and its content is not all
// the app's: the model name comes from the session. A name carrying a cluster the
// host bills one cell wider makes the row one cell wider than the frame's width,
// and a cell written past the bottom row scrolls the terminal — every row,
// including the rows a diff will never rewrite, moves up one.
func TestAContestedModelNameCannotScrollTheFrame(t *testing.T) {
	const width, height = 40, 20
	h := newFrameHarnessWithHost(t, width, height)

	h.step("empty")
	h.appendWin(tagAssistantText, "w1", "a line of the window\nand another")
	h.step("content")

	// The status bar's right group is server-provided text, so a cluster the
	// host bills wider can arrive in it.
	h.m.out.Write(encodeFrame(wire.TagSystemMsg,
		`{"type":"model","data":{"active_id":1,"active_name":"कि-model","context_limit":0}}`))
	h.m.out.FlushPendingDeltas()
	h.m = h.m.updateStatus()
	h.step("model name")
	if !strings.Contains(stripANSI(h.m.renderStatusBar()), "कि") {
		t.Fatalf("fixture: the model name is not in the status bar (%q)", stripANSI(h.m.renderStatusBar()))
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

// hostMayBillDifferently reports whether s carries a cluster whose width this
// package's table and a host's are measured — or recorded as likely — to
// disagree about, so a screen comparison that reads the cluster back cannot be
// judged on it:
//
//   - a spacing mark the host bills on its own (hostWiderByOne),
//   - the cell a variation selector joined, which the host forces to two cells,
//   - a zero-width joiner, one cluster here and several to a host that does not
//     cluster,
//   - U+3164 HANGUL FILLER, which the host ignores, and a lone regional
//     indicator, which its width cache bills one cell (hostNarrowerBy,
//     isLoneRegionalIndicator).
//
// The list is the measured classes (misc/probe-tmux-widths.sh, tmux 3.7c) plus
// the two the glyph policy already records as host-dependent (a variation
// selector and a ZWJ sequence). Everything else — ASCII, CJK, the box-drawing
// glyphs, braille, the tool indicators — is asserted where it is compared.
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
