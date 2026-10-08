package terminal

import (
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"
)

// TestComputeDiff verifies the LCS diff marks insertions, deletions, and
// unchanged lines correctly — and that a blank line is marked like any other,
// whatever its text happens to be.
func TestComputeDiff(t *testing.T) {
	tests := []struct {
		name     string
		oldLines []string
		newLines []string
		want     string
	}{{
		name:     "changed, unchanged, inserted and deleted lines",
		oldLines: []string{"a", "b", "c", "d"},
		newLines: []string{"a", "x", "c", "e"},
		want:     "  a\n- b\n+ x\n  c\n- d\n+ e",
	}, {
		// The trailing newline case: old_string ended with one and new_string
		// did not, so the empty line after "b" was removed and nothing else
		// changed. Its row is a removal with an empty text — a blank line
		// entering the file is a "+ " row the same way.
		name:     "blank line removed at the end",
		oldLines: []string{"a", "b", ""},
		newLines: []string{"a", "b"},
		want:     "  a\n  b\n- ",
	}, {
		name:     "blank line removed in the middle",
		oldLines: []string{"a", "", "b"},
		newLines: []string{"a", "b"},
		want:     "  a\n- \n  b",
	}, {
		name:     "blank line added in the middle",
		oldLines: []string{"a", "b"},
		newLines: []string{"a", "", "b"},
		want:     "  a\n+ \n  b",
	}, {
		name:     "blank line added at the end",
		oldLines: []string{"a"},
		newLines: []string{"a", ""},
		want:     "  a\n+ ",
	}, {
		name:     "every line blank",
		oldLines: []string{"", "", ""},
		newLines: []string{"", ""},
		want:     "  \n  \n- ",
	}, {
		// A hunk keeps its context on both sides and folds the rest: the block
		// is as long as the change, not as long as the arguments.
		name:     "long unchanged stretches fold into counted rows",
		oldLines: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "old", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t"},
		newLines: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "new", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t"},
		want: "  a\n  b\n  c\n… 4 lines hidden\n  h\n  i\n  j\n" +
			"- old\n+ new\n" +
			"  k\n  l\n  m\n… 4 lines hidden\n  r\n  s\n  t",
	}, {
		// Folding only ever hides unchanged lines, and a short stretch is not
		// worth a row: a hunk's own context is drawn even when it is every line
		// the block has.
		name:     "a run of exactly the context is drawn",
		oldLines: []string{"a", "b", "c", "x", "d", "e", "f", "g"},
		newLines: []string{"a", "b", "c", "y", "d", "e", "f", "g"},
		want:     "  a\n  b\n  c\n- x\n+ y\n  d\n  e\n  f\n  g",
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := drawDiff(computeDiff(tt.oldLines, tt.newLines)); got != tt.want {
				t.Errorf("diff of %q → %q:\n  got:  %q\n  want: %q",
					tt.oldLines, tt.newLines, got, tt.want)
			}
		})
	}
}

// TestComputeDiffAboveTheCap pins what happens when the two middles are too
// large to diff, and what does not happen: no row claims a line was removed or
// added, because nothing was compared. The shared ends are still trimmed first,
// so a block that changed in one place is diffed even when both sides are over
// the cap — the cap is the table's bound, not the block's.
func TestComputeDiffAboveTheCap(t *testing.T) {
	t.Run("shared ends are trimmed before the cap", func(t *testing.T) {
		var oldLines, newLines []string
		for i := 0; i < maxDiffLines; i++ {
			oldLines = append(oldLines, fmt.Sprintf("head-%d", i))
			newLines = append(newLines, fmt.Sprintf("head-%d", i))
		}
		oldLines = append(oldLines, "old body")
		newLines = append(newLines, "new body")
		for i := 0; i < maxDiffLines; i++ {
			oldLines = append(oldLines, fmt.Sprintf("tail-%d", i))
			newLines = append(newLines, fmt.Sprintf("tail-%d", i))
		}
		if len(oldLines) <= maxDiffLines {
			t.Fatalf("the pair must be over the cap to exercise the trim, got %d lines", len(oldLines))
		}

		got := drawDiff(computeDiff(oldLines, newLines))
		if !strings.Contains(got, "- old body\n+ new body") {
			t.Errorf("a small change between two long shared ends must still be diffed, got:\n%s", got)
		}
		if strings.Contains(got, "too large") {
			t.Errorf("the pair is not too large to diff once its shared ends are trimmed, got:\n%s", got)
		}
		// One change with a hunk's context on each side, and one counted row per
		// folded stretch: the block is 16 rows for a 4001-line pair.
		wantRows := 2*(2*diffContext+1) + 2
		if rows := strings.Count(got, "\n") + 1; rows != wantRows {
			t.Errorf("the block is %d rows, want %d:\n%s", rows, wantRows, got)
		}
	})

	t.Run("too large to diff says so, and claims nothing else", func(t *testing.T) {
		oldLines := make([]string, 0, maxDiffLines+1)
		newLines := make([]string, 0, maxDiffLines+1)
		for i := 0; i <= maxDiffLines; i++ {
			oldLines = append(oldLines, fmt.Sprintf("old-%d", i))
			newLines = append(newLines, fmt.Sprintf("new-%d", i))
		}

		rows := computeDiff(oldLines, newLines)
		if len(rows) != 1 {
			t.Fatalf("an undiffed pair is one row, got %d: %q", len(rows), drawDiff(rows))
		}
		want := "… 4002 lines hidden, too large to diff"
		if got := drawDiff(rows); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
		if rows[0].kind != rowElidedTooLarge || rows[0].count != len(oldLines)+len(newLines) {
			t.Errorf("the row must account for both middles: kind=%d count=%d", rows[0].kind, rows[0].count)
		}
	})
}

// TestDiffRowsInvariants checks what the block may never get wrong, over random
// line pairs including blank ones rather than over examples. The examples above
// pin the shapes a reader sees; these pin the three rules behind them:
//
//  1. The rows account for every line and invent none: consuming them against
//     the inputs — a context row is the next line of both, a removal of the old
//     side, an addition of the new, an elided row that many lines that are equal
//     on both sides — reaches the end of both inputs, line for line.
//  2. A change is never drawn as no change, and no change is drawn as one: the
//     rows hold a marked row exactly when the two inputs differ.
//  3. No stretch of unchanged rows is longer than a hunk's context: the block is
//     bounded by the change, which is the whole reason the folding exists.
//
// Rule 2 is the one a blank line breaks when a row's kind is read back out of
// its text, since "removed blank" and "unchanged blank" are both "". Rule 1 is
// the one the folding could break, by folding rows it should have drawn or by
// counting wrong.
func TestDiffRowsInvariants(t *testing.T) {
	alphabet := []string{"", "a", "b"} // a third of every input is a blank line

	// Two size regimes: tiny inputs make the rows and the inputs nearly the same
	// length (so the accounting is checked line by line), and inputs of up to 60
	// lines make runs long enough that the folding actually fires.
	for _, maxLines := range []int{6, 60} {
		for _, seed := range []int64{1, 7, 42, 1234, 31337} {
			rng := rand.New(rand.NewSource(seed))
			for iter := 0; iter < 1000; iter++ {
				oldLines := randomLines(rng, alphabet, maxLines)
				newLines := randomLines(rng, alphabet, maxLines)
				rows := computeDiff(oldLines, newLines)

				consumedOld, consumedNew := 0, 0
				marked := false
				for _, row := range rows {
					switch row.kind {
					case rowContext:
						if !lineIs(oldLines, consumedOld, row.line) || !lineIs(newLines, consumedNew, row.line) {
							t.Fatalf("seed %d: context row %q is not line %d of both inputs:\n  old %q\n  new %q",
								seed, row.line, consumedOld, oldLines, newLines)
						}
						consumedOld++
						consumedNew++
					case rowRemoved:
						if !lineIs(oldLines, consumedOld, row.line) {
							t.Fatalf("seed %d: removed row %q is not old line %d:\n  old %q",
								seed, row.line, consumedOld, oldLines)
						}
						consumedOld++
						marked = true
					case rowAdded:
						if !lineIs(newLines, consumedNew, row.line) {
							t.Fatalf("seed %d: added row %q is not new line %d:\n  new %q",
								seed, row.line, consumedNew, newLines)
						}
						consumedNew++
						marked = true
					case rowElidedUnchanged:
						if !sameFrom(oldLines, newLines, consumedOld, consumedNew, row.count) {
							t.Fatalf("seed %d: elided row counts %d lines from (%d,%d), which are not equal on both sides:\n  old %q\n  new %q\n  rows %q",
								seed, row.count, consumedOld, consumedNew, oldLines, newLines, drawDiff(rows))
						}
						consumedOld += row.count
						consumedNew += row.count
						marked = true
					default:
						t.Fatalf("seed %d: row kind %d below the cap", seed, row.kind)
					}
				}

				if consumedOld != len(oldLines) || consumedNew != len(newLines) {
					t.Fatalf("seed %d: the rows account for %d of %d old lines and %d of %d new:\n  rows %q",
						seed, consumedOld, len(oldLines), consumedNew, len(newLines), drawDiff(rows))
				}
				if marked != !slices.Equal(oldLines, newLines) {
					t.Fatalf("seed %d: diff of %q → %q marked=%v", seed, oldLines, newLines, marked)
				}
				for _, run := range contextRuns(rows) {
					if run > 2*diffContext+1 {
						t.Fatalf("seed %d: %d unchanged rows in a row (max %d):\n  rows %q",
							seed, run, 2*diffContext+1, drawDiff(rows))
					}
				}
			}
		}
	}
}

// randomLines returns 0..maxLines lines drawn from alphabet, blank lines
// included.
func randomLines(rng *rand.Rand, alphabet []string, maxLines int) []string {
	lines := make([]string, 0, maxLines)
	for n := rng.Intn(maxLines + 1); n > 0; n-- {
		lines = append(lines, alphabet[rng.Intn(len(alphabet))])
	}
	return lines
}

// lineIs reports whether lines[i] is want.
func lineIs(lines []string, i int, want string) bool {
	return i < len(lines) && lines[i] == want
}

// sameFrom reports whether count lines from each input, starting at i and j, are
// equal line for line.
func sameFrom(oldLines, newLines []string, i, j, count int) bool {
	if i+count > len(oldLines) || j+count > len(newLines) {
		return false
	}
	for k := 0; k < count; k++ {
		if oldLines[i+k] != newLines[j+k] {
			return false
		}
	}
	return true
}

// contextRuns returns the length of every run of unchanged rows.
func contextRuns(rows []diffRow) []int {
	var runs []int
	for i := 0; i < len(rows); {
		if rows[i].kind != rowContext {
			i++
			continue
		}
		run := 0
		for i+run < len(rows) && rows[i+run].kind == rowContext {
			run++
		}
		runs = append(runs, run)
		i += run
	}
	return runs
}

// drawDiff renders rows the way the block carries them, so the expected diff can
// be written as the reader sees it, empty and elided rows included.
func drawDiff(rows []diffRow) string {
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, row.spell())
	}
	return strings.Join(lines, "\n")
}
