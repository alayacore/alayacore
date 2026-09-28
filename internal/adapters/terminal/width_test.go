package terminal

// width_test.go holds the invariants of the single cell-arithmetic table
// (width.go). The point of these tests is not that displaywidth's numbers
// are right — no table is right for every terminal — but that the adapter
// uses exactly one of them for measuring and for cutting, and that no
// environment setting can move one half of that pair without the other.

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// widthCorpus is the text that reaches the cutters: model output and user
// paste, containing the clusters that width libraries disagree about.
var widthCorpus = []string{
	"",
	"hello",
	"a你b",                // CJK: 2 cells under every table
	"\\nescaped newline", // what tailParts/takeCells actually receive
	"1️⃣ abcd",           // keycap: 1 cell to uniseg, 2 here (the mismatch that overflowed)
	"✓️ done",            // text glyph + VS16: 1 to uniseg, 2 here
	"❤️ heart",           // VS16 emoji: 2 under both
	"👨‍👩‍👧‍👦 family",     // ZWJ: one cluster of 2 under both
	"e\u0301o",           // combining acute: one cluster of 1
	"कि indic",           // Devanagari Mc: 2 to uniseg, 1 here
	"🇨🇳🇧🇷 flags",         // regional indicator pairs
	"⠿ R0 | 12.3K/128K | gpt…",
	"─────",
	"a\tb",
}

// styledCorpus is kept separate: a row that already carries SGR or OSC-8
// sequences is cut by the escape-aware fallback, not by clustering, so the
// cluster-boundary and cluster-sum tests do not apply to it. Measuring is
// the same promise either way.
var styledCorpus = []string{
	"\x1b[31mred\x1b[0m reset",
	"\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\",
	"\x1b[38;2;49;50;68m⠿\x1b[m \x1b[1mR0\x1b[m \x1b[38;2;49;50;68m│\x1b[m hi",
}

// allCorpus is widthCorpus plus the styled rows.
func allCorpus() []string { return append(append([]string{}, widthCorpus...), styledCorpus...) }

// TestCellWidthMatchesAnsi pins width.go's table against the library the
// rest of the ecosystem measures with, so swapping the two halves of a
// measurement could not silently change any number that already had both
// answers. It only holds while RUNEWIDTH_EASTASIAN is unset — that is the
// default, and the point of the next test.
func TestCellWidthMatchesAnsi(t *testing.T) {
	if os.Getenv("RUNEWIDTH_EASTASIAN") != "" {
		t.Skip("RUNEWIDTH_EASTASIAN set: ansi.StringWidth is deliberately not the pinned table")
	}
	for _, s := range allCorpus() {
		if got, want := cellWidth(s), ansi.StringWidth(s); got != want {
			t.Errorf("cellWidth(%q) = %d, ansi.StringWidth = %d", s, got, want)
		}
	}
}

// TestCellWidthIgnoresRunewidthEastAsian proves the second reason width.go
// exists: charmbracelet/x/ansi reads RUNEWIDTH_EASTASIAN in its own package
// init and charges East-Asian-Ambiguous glyphs two cells when it is true,
// and nothing we can do afterwards turns that back off (an init in this
// package runs later; os.Unsetenv in main runs later still — measured). The
// adapter's own numbers must not move.
//
// The child is this same test binary re-executed with the variable set.
func TestCellWidthIgnoresRunewidthEastAsian(t *testing.T) {
	if os.Getenv("ALAYACORE_WIDTH_CHILD") == "1" {
		// Child: report both measurements under the env var.
		fmt.Printf("cell=%d ansi=%d\n", cellWidth("─"), ansi.StringWidth("─"))
		return
	}
	out, err := runWidthChild()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	var cell, ansiW int
	if _, cerr := fmt.Sscanf(strings.TrimSpace(out), "cell=%d ansi=%d", &cell, &ansiW); cerr != nil {
		t.Fatalf("child output %q: %v", out, cerr)
	}
	if cell != 1 {
		t.Errorf("with RUNEWIDTH_EASTASIAN=1: cellWidth(%q) = %d, want 1 — the pinned table leaked the environment", "─", cell)
	}
	if ansiW != 2 {
		t.Skipf("the library itself did not widen under RUNEWIDTH_EASTASIAN (ansi=%d); nothing to prove", ansiW)
	}
}

func runWidthChild() (string, error) {
	cmd := exec.Command(os.Args[0], "-test.run=TestCellWidthIgnoresRunewidthEastAsian", "-test.v=false")
	cmd.Env = append(os.Environ(), "RUNEWIDTH_EASTASIAN=1", "ALAYACORE_WIDTH_CHILD=1")
	b, err := cmd.CombinedOutput()
	// The child prints one line before the test binary's own summary.
	for _, ln := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(ln, "cell=") {
			return ln, err
		}
	}
	return string(b), fmt.Errorf("no measurement line in output: %w", err)
}

// TestCutNeverOverrunsItsBudget is the invariant every cutter must hold: a
// string cut to N cells measures at most N cells. It used to be violated by
// one cell for a keycap or a text-plus-VS16 cluster, because the budget was
// counted in one table and the cut in another. This test, run against the
// previous implementation, fails.
//
// It runs over breakerCorpus — long runs, styled rows and all three forms of a
// C1 control included — because the budget is the one promise that has to hold
// on every route through the cutters, whichever of them a given string takes.
func TestCutNeverOverrunsItsBudget(t *testing.T) {
	for _, s := range breakerCorpus() {
		full := cellWidth(s)
		for n := 0; n <= full+2; n++ {
			if got := cellWidth(takeCells(s, n)); got > n {
				t.Errorf("takeCells(%q, %d) measures %d cells — over budget", s, n, got)
			}
			if got := cellWidth(tailCells(s, n)); got > n {
				t.Errorf("tailCells(%q, %d) measures %d cells — over budget", s, n, got)
			}
		}
	}
}

// TestCutIsMaximalAndClusterAligned is the other half of the guarantee: a
// cut lands on a cluster boundary, and it takes as much as fits — one more
// cluster would overrun the budget. A cut that split a cluster (garbage on
// screen) or left a cell unused (a ragged right edge) fails here.
func TestCutIsMaximalAndClusterAligned(t *testing.T) {
	for _, s := range widthCorpus { // plain text only: see styledCorpus
		cs := clusters(s)
		full := cellWidth(s)
		for n := 1; n <= full+1; n++ {
			h := takeCells(s, n)
			checkCut(t, "takeCells", s, n, h, cs, true)
			tl := tailCells(s, n)
			checkCut(t, "tailCells", s, n, tl, cs, false)
		}
	}
}

// checkCut verifies one cut: got must be the concatenation of a whole number
// of clusters taken from the wanted end of cs, and must be maximal — adding
// the next cluster from that end would overrun the budget of n cells.
func checkCut(t *testing.T, what, s string, n int, got string, cs []cluster, fromHead bool) {
	t.Helper()
	if !utf8.ValidString(got) {
		t.Errorf("%s(%q, %d) = %q is not valid UTF-8 — a cluster was split", what, s, n, got)
	}
	// How many clusters the result is made of.
	k := len(clusters(got))
	var whole string
	if fromHead {
		whole = concatClusters(cs[:k])
	} else {
		whole = concatClusters(cs[len(cs)-k:])
	}
	if got != whole {
		t.Errorf("%s(%q, %d) = %q, which is not %d whole clusters from the %s (nearest whole cut: %q)",
			what, s, n, got, k, map[bool]string{true: "head", false: "tail"}[fromHead], whole)
		return
	}
	// Maximality: one more cluster from that end must not fit.
	next := -1
	if fromHead && k < len(cs) {
		next = k
	} else if !fromHead && k < len(cs) {
		next = len(cs) - k - 1
	}
	if next >= 0 && cellWidth(whole)+cs[next].cells <= n {
		t.Errorf("%s(%q, %d) = %q left %d cells unused that the next cluster (%d) would have used",
			what, s, n, got, n-cellWidth(whole), cs[next].cells)
	}
}

func concatClusters(cs []cluster) string {
	var b strings.Builder
	for _, c := range cs {
		b.WriteString(c.text)
	}
	return b.String()
}

// cluster is one grapheme cluster: its text, the cells it occupies, and its
// rune range within the string it came from.
//
// This is the test oracle, not production code. width.go answers every
// question with a fold over walkCells — a sum, a max, a prefix, a suffix, one
// position — and never builds this list, because building it is O(len(s)) in
// time and ~120 B per cluster in garbage for answers that are O(1). The tests
// keep the materializing form on purpose: an oracle that shares an
// implementation with the code under test cannot disagree with it, and the
// disagreements are the whole subject of this file.
type cluster struct {
	text               string
	cells              int
	runeStart, runeEnd int // rune indices, runeEnd exclusive
}

// clusters returns s's grapheme clusters in order, over plain text: escape
// sequences are not text and are not treated as units here (takeCells and
// tailCells route styled strings elsewhere). Cutting a string to a cell
// budget must never split a cluster — a base character and its
// combining mark, or an emoji and its variation selector, are drawn as one
// unit and half of one is garbage on screen — and the input chain moves the
// caret by whole clusters, which is why the rune range comes along too.
func clusters(s string) []cluster {
	if s == "" {
		return nil
	}
	it := widthModel.StringGraphemes(s)
	var out []cluster
	runes := 0
	for it.Next() {
		v := it.Value()
		n := utf8.RuneCountInString(v)
		out = append(out, cluster{text: v, cells: it.Width(), runeStart: runes, runeEnd: runes + n})
		runes += n
	}
	return out
}

// TestCutsCostTheCutNotTheString is the allocation contract behind the
// cutters, and the guard on the bug it replaced. takeCells and tailCells used
// to answer from a materialized []cluster of the whole string, so a 30-cell
// head of a long message cost one struct and one substring per cluster —
// 245 KB and 45μs for a 2 KB string, twice, on the per-frame path of every
// folded text window, and ~1 ms and 4 MB per keystroke at the end of a long
// prompt line (docs/internal/virtual-rendering-performance.md records both).
//
// A cut's cost has to follow its own budget, not the length of what it cuts.
// Time is machine-dependent and the budgets above are already covered by
// TestCutIsMaximalAndClusterAligned; the allocation count is the part a test
// can state exactly, and it is the part that regressed: one string returned,
// whatever the input, and zero for a fold that returns a number.
func TestCutsCostTheCutNotTheString(t *testing.T) {
	inputs := []struct {
		name string
		s    string
	}{
		{"100 wide clusters", strings.Repeat("中", 100)},
		{"10000 wide clusters", strings.Repeat("中", 10000)},
		{"200 ascii bytes", strings.Repeat("a", 200)},
		{"20000 ascii bytes", strings.Repeat("a", 20000)},
	}
	cuts := []struct {
		name string
		fn   func(string, int) string
	}{
		{"takeCells", takeCells},
		{"tailCells", tailCells},
	}
	// The bound is a ceiling rather than an exact count because AllocsPerRun
	// reads process-wide mallocs, and a -race build can add one of its own mid-
	// measurement. It does not need to be tight to do its job: an implementation
	// that materializes the clusters reports one allocation per cluster, so
	// ~10,000 on the long inputs against a ceiling of 8. Same budget, 100x the
	// clusters, same bound — that pairing is what makes the cost follow the cut.
	const ceiling = 8
	for _, cut := range cuts {
		for _, budget := range []int{1, 20} {
			for _, in := range inputs {
				n := testing.AllocsPerRun(10, func() { _ = cut.fn(in.s, budget) })
				if n > ceiling {
					t.Errorf("%s(%s, %d) allocated %.0f times; a cut returns one string, it does not build a cluster per cell",
						cut.name, in.name, budget, n)
				}
			}
		}
	}
	folds := []struct {
		name string
		fn   func(string) int
	}{
		{"widestCellCluster", widestCellCluster},
		{"cellWidth", cellWidth},
	}
	for _, fold := range folds {
		for _, in := range inputs {
			if n := testing.AllocsPerRun(10, func() { _ = fold.fn(in.s) }); n > ceiling {
				t.Errorf("%s(%s) allocated %.0f times; a fold over clusters builds nothing", fold.name, in.name, n)
			}
		}
	}
}

// TestClustersMatchCellWidth guards against a table split inside width.go
// itself: summing per-cluster widths must equal the whole-string measure.
func TestClustersMatchCellWidth(t *testing.T) {
	for _, s := range widthCorpus { // plain text only: clusters() is not escape-aware
		sum := 0
		for _, c := range clusters(s) {
			sum += c.cells
		}
		if got := cellWidth(s); got != sum {
			t.Errorf("cluster sum for %q = %d, cellWidth = %d — two paths, one table, disagreeing", s, sum, got)
		}
	}
}

// TestStyledCutsKeepTheirBudgetAndTheirEscapes covers the fallback route: a
// row that already carries escapes must come back within budget, with whole
// escape sequences, and showing a prefix (head) or suffix (tail) of the
// visible text. Clustering alone would cut "\x1b[3" in half and repaint the
// rest of the window in the wrong color.
func TestStyledCutsKeepTheirBudgetAndTheirEscapes(t *testing.T) {
	for _, s := range styledCorpus {
		full := cellWidth(s)
		plain := ansi.Strip(s)
		for n := 1; n <= full; n++ {
			h := takeCells(s, n)
			if got := cellWidth(h); got > n {
				t.Errorf("takeCells(%q, %d) measures %d cells — over budget", s, n, got)
			}
			if !escapesWhole(h) {
				t.Errorf("takeCells(%q, %d) = %q — a truncated escape sequence", s, n, h)
			}
			if hp := ansi.Strip(h); !strings.HasPrefix(plain, hp) {
				t.Errorf("takeCells(%q, %d) = %q — visible text is not a prefix of %q", s, n, h, plain)
			}
			tl := tailCells(s, n)
			if got := cellWidth(tl); got > n {
				t.Errorf("tailCells(%q, %d) measures %d cells — over budget", s, n, got)
			}
			if !escapesWhole(tl) {
				t.Errorf("tailCells(%q, %d) = %q — a truncated escape sequence", s, n, tl)
			}
			if tp := ansi.Strip(tl); !strings.HasSuffix(plain, tp) {
				t.Errorf("tailCells(%q, %d) = %q — visible text is not a suffix of %q", s, n, tl, plain)
			}
		}
	}
}

// escapesWhole reports whether every escape sequence started in s also ends
// in s. CSI is terminated by a byte in 0x40-0x7e, OSC by BEL or ST (ESC \).
func escapesWhole(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b {
			continue
		}
		if i+1 >= len(s) {
			return false // dangling ESC
		}
		switch s[i+1] {
		case '[':
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j >= len(s) {
				return false // CSI never reached its final byte
			}
			i = j
		case ']':
			j := i + 2
			for j < len(s) && s[j] != 0x07 && !(s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\') {
				j++
			}
			if j >= len(s) {
				return false // OSC never terminated
			}
			i = j + 1
		default:
			return false // unknown/unterminated introducer
		}
	}
	return true
}

// TestWidestCellClusterFeedsTheTableShrinker documents why the markdown
// table layout asks this question at all: a column narrower than the widest
// cluster in its cells cannot hold them, and no shrink step may go below it.
func TestWidestCellClusterFeedsTheTableShrinker(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{"abc", 1},
		{"a你b", 2},
		{"👨‍👩‍👧‍👦", 2},
		{"1️⃣ ab", 2},
		{"", 0},
	}
	for _, c := range cases {
		if got := widestCellCluster(c.s); got != c.want {
			t.Errorf("widestCellCluster(%q) = %d, want %d", c.s, got, c.want)
		}
	}
}

// The breakers — hardwrapCells, keepCells/dropCells/cutCells — are held to the
// same two promises the cutters already were: nothing they return may measure
// wider than the budget it was given, and nothing they return may be half a
// character. Why they are this file's and not the library's is point 3 of
// width.go's header; these are the tests that make the promise checkable.

// breakerCorpus is the corpus plus the shapes a wrapper meets and a cutter
// does not: runs long enough to wrap, and the same runs already styled.
func breakerCorpus() []string {
	out := allCorpus()
	// The three forms an ECMA-48 C1 control can arrive in. A raw 0x9B byte is
	// the one that caught a real disagreement: hasEscape recognizes only the
	// UTF-8 form, so cellWidth never strips the raw one, and a walker with
	// ControlSequences8Bit enabled folded it into a zero-width cluster instead
	// of billing it the visible characters cellWidth saw (see breakerModel).
	out = append(out,
		"a"+string([]byte{0x9b})+"[m b",
		"a\u009b[m b",
		"a\x1b[31mb",
	)
	for _, s := range []string{
		strings.Repeat("1️⃣", 40),
		strings.Repeat("中", 40),
		strings.Repeat("a\uFE0F", 40),
		strings.Repeat("word ", 40),
		"1️⃣ 第一步：安装依赖\n2️⃣ 第二步：运行测试\n3️⃣ 第三步：部署上线",
		strings.Repeat("👨‍👩‍👧‍👦", 20),
	} {
		out = append(out, s, "\x1b[31m"+s+"\x1b[0m")
	}
	return out
}

// TestHardwrapNeverOverrunsItsBudget is the invariant the frame's height
// depends on: every row hardwrapCells returns fits the width it was given, so
// the row count the layout charges is the row count the terminal draws.
func TestHardwrapNeverOverrunsItsBudget(t *testing.T) {
	for _, s := range breakerCorpus() {
		for width := 1; width <= 80; width++ {
			got := hardwrapCells(s, width)
			for i, line := range strings.Split(got, "\n") {
				// A single cluster wider than the budget cannot be
				// broken; that row is alone and documented.
				if widestCellCluster(line) > width {
					continue
				}
				if w := cellWidth(line); w > width {
					t.Errorf("hardwrapCells(%q, %d) row %d measures %d cells: %q", s, width, i, w, line)
				}
			}
			// Half a character is only a defect if the input was whole
			// characters: the corpus carries a raw C1 byte, which is not
			// valid UTF-8 to begin with and cannot be made so by a wrapper.
			if utf8.ValidString(s) && !utf8.ValidString(got) {
				t.Errorf("hardwrapCells(%q, %d) is not valid UTF-8: %q", s, width, got)
			}
		}
	}
}

// TestHardwrapKeepsTheContent says what the budget must not cost: wrapping
// inserts newlines and nothing else. Strip them and the text is the input —
// every character, every escape, in order.
func TestHardwrapKeepsTheContent(t *testing.T) {
	for _, s := range breakerCorpus() {
		for _, width := range []int{1, 3, 7, 12, 20, 40, 80} {
			got := strings.ReplaceAll(hardwrapCells(s, width), "\n", "")
			want := strings.ReplaceAll(s, "\n", "")
			if got != want {
				t.Errorf("hardwrapCells(%q, %d) changed the content:\n got %q\nwant %q", s, width, got, want)
			}
		}
	}
}

// TestCutCellsFitsBudgetAndKeepsRunesWhole covers the cutter the diff renderer
// slices a soft-wrapped row with. A window whose edges fall on cluster
// boundaries — which is what a soft-wrap row boundary always is, the rows
// being padded to the exact width — must come back exactly that many cells;
// an edge inside a wide cluster cannot be honored, and the guarantee then is
// the absolute one: never more than `right` cells from the start of the
// string, and never half a character.
func TestCutCellsFitsBudgetAndKeepsRunesWhole(t *testing.T) {
	for _, s := range breakerCorpus() {
		full := cellWidth(s)
		edges := clusterEdges(ansi.Strip(s))
		for left := 0; left <= full+2; left++ {
			for right := left; right <= full+2; right++ {
				got := cutCells(s, left, right)
				// As above: a raw C1 byte in the corpus is not valid UTF-8
				// before the cut either.
				if utf8.ValidString(s) && !utf8.ValidString(got) {
					t.Fatalf("cutCells(%q, %d, %d) is not valid UTF-8: %q", s, left, right, got)
				}
				if w := cellWidth(got); w > min(right, full) {
					t.Errorf("cutCells(%q, %d, %d) measures %d cells, past the right edge", s, left, right, w)
				}
				if edges[left] && edges[right] && right <= full {
					if w := cellWidth(got); w != right-left {
						t.Errorf("cutCells(%q, %d, %d) on cluster edges measures %d cells, want %d",
							s, left, right, w, right-left)
					}
				}
				if cellWidth(keepCells(s, right)) > right {
					t.Errorf("keepCells(%q, %d) measures %d cells — over budget",
						s, right, cellWidth(keepCells(s, right)))
				}
			}
		}
	}
}

// clusterEdges marks the cell offsets in plain s that fall on a cluster
// boundary, so a test can tell an exactable window from one that cuts a wide
// cluster in half.
func clusterEdges(s string) map[int]bool {
	edges := map[int]bool{0: true}
	at := 0
	for _, c := range clusters(s) {
		at += c.cells
		edges[at] = true
	}
	return edges
}

// TestKeepAndDropPartitionTheString is what makes cutCells exact rather than
// approximate: the two halves meet at one cluster boundary and put the whole
// string back together.
func TestKeepAndDropPartitionTheString(t *testing.T) {
	for _, s := range widthCorpus { // plain text: the escape-preserving halves do not re-join byte-for-byte
		full := cellWidth(s)
		for n := 0; n <= full; n++ {
			keep, drop := keepCells(s, n), dropCells(s, n)
			if keep+drop != s {
				t.Errorf("keepCells(%q,%d)=%q + dropCells=%q does not reassemble the input", s, n, keep, drop)
			}
			if cellWidth(keep) > n {
				t.Errorf("keepCells(%q, %d) measures %d cells", s, n, cellWidth(keep))
			}
			if cellWidth(keep)+cellWidth(drop) != full {
				t.Errorf("the halves of %q at %d measure %d cells, the whole measures %d",
					s, n, cellWidth(keep)+cellWidth(drop), full)
			}
		}
	}
}

// TestWalkCellsAgreesWithCellWidth is the guard on the escape-aware route
// itself: summing the clusters walkCells yields must give the number cellWidth
// reports, or the breakers would be measuring with a second table again —
// which is the defect they were written to end.
func TestWalkCellsAgreesWithCellWidth(t *testing.T) {
	for _, s := range breakerCorpus() {
		sum := 0
		walkCells(s, func(_ string, cells int) bool { sum += cells; return true })
		if sum != cellWidth(s) {
			t.Errorf("walkCells(%q) sums to %d cells, cellWidth says %d", s, sum, cellWidth(s))
		}
	}
}

// TestWrapVisualLinesRowsFitTheBudget is the same promise at the level the
// frame is built from: every visual row a window produces fits the width the
// viewport will pad it to. renderVirtual pads a row to the width only when it
// is under it, so a row that arrives over budget is a row the terminal wraps
// into a line the layout never counted.
func TestWrapVisualLinesRowsFitTheBudget(t *testing.T) {
	for _, s := range breakerCorpus() {
		for _, width := range []int{1, 3, 7, 12, 20, 24, 40, 80} {
			for i, vl := range wrapVisualLines(s, width) {
				if widestCellCluster(vl.Text) > width {
					continue // unbreakable cluster: documented, alone on its row
				}
				if w := cellWidth(vl.Text); w > width {
					t.Errorf("wrapVisualLines(%q, %d) row %d measures %d cells: %q", s, width, i, w, vl.Text)
				}
			}
		}
	}
}

// TestHardwrapIsGreedyAndPathIndependent holds walkCells' two speeds to each
// other. The break rule exists once, but the byte walk for plain ASCII and the
// cluster walk for everything else are two tokenizers, and a token they
// disagreed about would move a break. Two properties are asked, and together
// they pin a wrap completely:
//
//   - GREEDY: a break happens only where the next cluster would not fit, so no
//     line can absorb the first cluster of the one below it. A tokenizer that
//     billed a cluster low would break late and overflow the budget — which
//     TestHardwrapNeverOverrunsItsBudget catches — and one that billed it high
//     would break early, which satisfies the budget and is still wrong: rows
//     the layout never reserved. Only greediness catches that half.
//   - PATH-INDEPENDENT: the same text with every "a" replaced by "ä" (one cell
//     either way, two bytes, so the ASCII route declines it) breaks in the same
//     places.
//
// Greediness is asked per SOURCE line: a newline in the input is a break the
// wrapper must keep, and telling it apart from a break the wrapper chose is
// not possible from the output alone. That is sound because the wrapper resets
// its column at every newline, so each source line wraps independently.
func TestHardwrapIsGreedyAndPathIndependent(t *testing.T) {
	corpus := append(breakerCorpus(),
		// the ASCII route's own shapes
		strings.Repeat("abcdefghij", 12),
		strings.Repeat("line\n", 20),
		"tab\there\tagain",
		strings.Repeat("x", 240),
	)
	for _, s := range corpus {
		for width := 1; width <= 40; width++ {
			for _, src := range strings.Split(s, "\n") {
				lines := strings.Split(hardwrapCells(src, width), "\n")
				for i := 0; i+1 < len(lines); i++ {
					next := firstClusterOf(lines[i+1])
					if next == "" {
						continue
					}
					if cellWidth(lines[i])+cellWidth(next) <= width {
						t.Errorf("hardwrapCells(%q, %d) broke early: row %d (%d cells) could have taken %q (%d cells) from row %d",
							src, width, i, cellWidth(lines[i]), next, cellWidth(next), i+1)
					}
				}
			}
			if plainASCIIFast(s) {
				want := strings.ReplaceAll(hardwrapCells(s, width), "a", "ä")
				if got := hardwrapCells(strings.ReplaceAll(s, "a", "ä"), width); got != want {
					t.Errorf("hardwrapCells(%q, %d) breaks differently from its ASCII route:\n got %q\nwant %q",
						s, width, got, want)
				}
			}
		}
	}
}

// firstClusterOf returns s's first grapheme cluster, or "" for an empty s.
func firstClusterOf(s string) string {
	if s == "" {
		return ""
	}
	c := clusters(s)
	if len(c) == 0 {
		return ""
	}
	return c[0].text
}
