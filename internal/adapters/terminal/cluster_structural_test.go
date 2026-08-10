package terminal

import (
	"math/rand"
	"testing"
)

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
