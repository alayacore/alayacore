package terminal

import (
	"math/rand"
	"strings"
	"testing"
)

// clusterInfo describes one grapheme cluster: its rune range within a line
// and its terminal display width in cells.
//
// Like clusters in width_test.go, this is the materializing oracle for the
// streaming walker the input chain actually uses (walkLineClusters). The
// structural test below runs the two against each other over random lines; an
// oracle built out of the code under test would prove nothing.
type clusterInfo struct {
	start, end int // rune indices, end exclusive
	width      int // display width in cells
}

// graphemeClusters splits line into grapheme clusters and returns each
// cluster's rune range and terminal display width, from the same table
// walkLineClusters walks (width.go).
func graphemeClusters(line []rune) []clusterInfo {
	if len(line) == 0 {
		return nil
	}
	cs := clusters(string(line))
	out := make([]clusterInfo, 0, len(cs))
	for _, c := range cs {
		out = append(out, clusterInfo{start: c.runeStart, end: c.runeEnd, width: c.cells})
	}
	return out
}

// TestGraphemeClustersStructural verifies the two width entry points
// (runesWidth vs graphemeClusters) agree on arbitrary lines and that
// graphemeClusters covers the line exactly: no gaps, no overlaps, first
// start=0, last end=len.
func TestGraphemeClustersStructural(t *testing.T) {
	pool := []rune("a你中❤️👨\u200d👩\u200d👧\u200d👦e\u0301कि\u05D1\u0591\u1100\u1161\u0600a1\uFE0F\u20E3\uFF9E ")
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 20000; iter++ {
		n := 1 + rng.Intn(12)
		line := make([]rune, n)
		for i := range line {
			line[i] = pool[rng.Intn(len(pool))]
		}
		clusters := graphemeClusters(line)
		if len(clusters) == 0 {
			t.Fatalf("empty clusters for %q", string(line))
		}
		if clusters[0].start != 0 {
			t.Fatalf("first cluster start=%d, want 0 (%q)", clusters[0].start, string(line))
		}
		prev := clusters[0].end
		for i, c := range clusters[1:] {
			if c.start != prev {
				t.Fatalf("gap/overlap at cluster %d: start=%d prev end=%d (%q)", i+1, c.start, prev, string(line))
			}
			if c.end <= c.start {
				t.Fatalf("cluster %d empty range [%d,%d)", i+1, c.start, c.end)
			}
			prev = c.end
		}
		if clusters[len(clusters)-1].end != len(line) {
			t.Fatalf("last cluster end=%d, want %d (%q)", clusters[len(clusters)-1].end, len(line), string(line))
		}
		// Both width entries must agree.
		sum := 0
		for _, c := range clusters {
			sum += c.width
		}
		if sum != runesWidth(line) {
			t.Fatalf("runesWidth(%q)=%d != sum(cluster widths)=%d", string(line), runesWidth(line), sum)
		}
		// Widths must be non-negative.
		for _, c := range clusters {
			if c.width < 0 {
				t.Fatalf("negative cluster width %d for %q", c.width, string(line))
			}
		}
	}
}

// TestLineQueriesCostOneEncoding pins the input chain's half of the contract
// width_test.go's TestCutsCostTheCutNotTheString pins for the cutters: a
// question about a line is one walk, and the only thing a walk allocates is the
// string the table segments (the field holds runes, the table reads strings).
//
// Before walkLineClusters every one of these built a []clusterInfo for the
// whole line — a struct and a substring per cluster — and ensureCursorVisible
// asks five of them per keystroke, which is what made typing at the end of a
// 4000-cell line cost ~1ms and ~4MB. One allocation each, whatever the line
// holds, is the property that keeps it from coming back.
func TestLineQueriesCostOneEncoding(t *testing.T) {
	lines := []struct {
		name string
		line []rune
	}{
		{"100 runes", []rune(strings.Repeat("中a", 50))},
		{"10000 runes", []rune(strings.Repeat("中a", 5000))},
	}
	queries := []struct {
		name string
		run  func([]rune)
	}{
		{"runesWidth", func(l []rune) { _ = runesWidth(l) }},
		{"probeLine", func(l []rune) { _ = probeLine(l, len(l)/2, len(l)/3, 60) }},
		{"firstRuneStartAtLeast", func(l []rune) { _, _ = firstRuneStartAtLeast(l, len(l)/2) }},
		{"runeIndexAtWidth", func(l []rune) { _ = runeIndexAtWidth(l, len(l)/2) }},
	}
	// A ceiling, not an exact count: AllocsPerRun reads process-wide mallocs and
	// a -race build can add one of its own. The regression it guards reported one
	// allocation per cluster — ~10,000 on the long line against this.
	const ceiling = 8
	for _, q := range queries {
		for _, in := range lines {
			line := in.line
			n := testing.AllocsPerRun(10, func() { q.run(line) })
			if n > ceiling {
				t.Errorf("%s over a %s line allocated %.0f times; one walk, one encoding",
					q.name, in.name, n)
			}
		}
	}
}
