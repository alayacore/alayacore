package terminal

import (
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

// TestDiffRowsInvariants checks what no diff may get wrong, over random line
// pairs including blank ones rather than over examples. The examples above pin
// the shapes a reader sees; these pin the two rules behind them:
//
//  1. The rows account for every line and invent none: reading the removals
//     back (context and removed rows, in order) gives oldLines, and the
//     additions back (context and added rows) gives newLines.
//  2. A change is never drawn as no change, and no change is drawn as one:
//     the diff has a marked row exactly when the two line lists differ.
//
// Rule 2 is the one a blank line breaks when a row's kind is read back out of
// its text, since "removed blank" and "unchanged blank" are both "".
func TestDiffRowsInvariants(t *testing.T) {
	alphabet := []string{"", "a", "b"} // a third of every input is a blank line

	for _, seed := range []int64{1, 7, 42, 1234, 31337} {
		rng := rand.New(rand.NewSource(seed))
		for iter := 0; iter < 2000; iter++ {
			oldLines := randomLines(rng, alphabet)
			newLines := randomLines(rng, alphabet)

			var oldSeen, newSeen []string
			marked := false
			for _, row := range computeDiff(oldLines, newLines) {
				switch row.kind {
				case rowContext:
					oldSeen = append(oldSeen, row.text)
					newSeen = append(newSeen, row.text)
				case rowRemoved:
					oldSeen = append(oldSeen, row.text)
					marked = true
				case rowAdded:
					newSeen = append(newSeen, row.text)
					marked = true
				default:
					t.Fatalf("unknown row kind %d", row.kind)
				}
			}

			if !slices.Equal(oldSeen, oldLines) || !slices.Equal(newSeen, newLines) {
				t.Fatalf("seed %d: rows do not account for the lines:\n  old %q → %q\n  new %q → %q",
					seed, oldLines, oldSeen, newLines, newSeen)
			}
			if marked != !slices.Equal(oldLines, newLines) {
				t.Fatalf("seed %d: diff of %q → %q marked=%v",
					seed, oldLines, newLines, marked)
			}
		}
	}
}

// randomLines returns 0..6 lines drawn from alphabet, blank lines included.
func randomLines(rng *rand.Rand, alphabet []string) []string {
	lines := make([]string, 0, 6)
	for n := rng.Intn(7); n > 0; n-- {
		lines = append(lines, alphabet[rng.Intn(len(alphabet))])
	}
	return lines
}

// drawDiff renders rows the way the window does — marker, then text — so the
// expected diff can be written as the reader sees it, empty rows included.
func drawDiff(rows []diffRow) string {
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, row.marker()+row.text)
	}
	return strings.Join(lines, "\n")
}
