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
	"∙ R0 | 12.3K/128K | gpt…",
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
	"\x1b[38;2;49;50;68m∙\x1b[m \x1b[1mR0\x1b[m \x1b[38;2;49;50;68m│\x1b[m hi",
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
func TestCutNeverOverrunsItsBudget(t *testing.T) {
	for _, s := range allCorpus() {
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
	k := clusterCount(got)
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
