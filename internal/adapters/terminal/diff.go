package terminal

// Line-level diff for the edit_file window: the model's old_string and
// new_string are split into lines, matched with an LCS, and every line comes out
// as a row that says what happened to it.

// diffRowKind is what happened to a line.
type diffRowKind uint8

const (
	rowContext diffRowKind = iota // present in both, unchanged
	rowRemoved                    // present in old_string only
	rowAdded                      // present in new_string only
)

// diffRow is one row of the displayed diff: the kind of change, and the line it
// shows.
//
// The kind is stored, not inferred from the text, and that is the whole reason
// this type is not a pair of strings. An empty line is a line like any other, so
// a removed blank line and an unchanged one are the same text — "" — and any
// rule that reads the kind back out of that text has to call one of them the
// other.
type diffRow struct {
	kind diffRowKind
	text string
}

// marker is the row's two-character prefix — "  " context, "- " removed, "+ "
// added — and the whole of what RenderDiffContent reads to pick a color.
func (r diffRow) marker() string {
	switch r.kind {
	case rowRemoved:
		return "- "
	case rowAdded:
		return "+ "
	default:
		return "  "
	}
}

// maxDiffLines caps the LCS table for edit_file diffs. The old/new strings come
// from the model's tool-call arguments and can be arbitrarily large — an
// unbounded m×n table would exhaust memory on huge inputs (10k × 10k lines =
// 800MB of cells). Above this cap computeDiff answers with a degenerate
// "every line changed" diff, which is faithful to the two inputs and costs O(n)
// time and no table at all.
//
// The cap sits here, with the table it protects, rather than at the call site:
// a bound is only as good as the next caller that remembers to apply it.
const maxDiffLines = 2000

// computeDiff returns the rows that turn oldLines into newLines: unchanged lines
// as context, the rest as removals and additions covering the longest common
// subsequence's gaps. A changed line is a removal followed by the addition that
// replaced it, so it reads "- old" over "+ new".
func computeDiff(oldLines, newLines []string) []diffRow {
	if len(oldLines) > maxDiffLines || len(newLines) > maxDiffLines {
		rows := make([]diffRow, 0, len(oldLines)+len(newLines))
		for _, line := range oldLines {
			rows = append(rows, diffRow{rowRemoved, line})
		}
		for _, line := range newLines {
			rows = append(rows, diffRow{rowAdded, line})
		}
		return rows
	}

	lcs := lcsSuffixLengths(oldLines, newLines)

	// Walk the table once, forward, emitting a row per line as it goes. Each
	// step consumes the line whose removal or addition leaves the LCS as long as
	// the table says it is, so no matched line is ever lost and no row is
	// invented: what the walk emits is exactly the two inputs, marked.
	m, n := len(oldLines), len(newLines)
	rows := make([]diffRow, 0, m+n)
	for i, j := 0, 0; i < m || j < n; {
		switch {
		case i < m && j < n && oldLines[i] == newLines[j]:
			rows = append(rows, diffRow{rowContext, oldLines[i]})
			i++
			j++
		case j < n && (i == m || lcs[i][j+1] > lcs[i+1][j]):
			rows = append(rows, diffRow{rowAdded, newLines[j]})
			j++
		default:
			rows = append(rows, diffRow{rowRemoved, oldLines[i]})
			i++
		}
	}

	return rows
}

// lcsSuffixLengths returns lcs, where lcs[i][j] is the length of the longest
// common subsequence of oldLines[i:] and newLines[j:] — the suffix table,
// because computeDiff walks forward and asks what is still matchable *after* the
// line it is looking at.
func lcsSuffixLengths(oldLines, newLines []string) [][]int {
	m, n := len(oldLines), len(newLines)
	lcs := make([][]int, m+1)
	for i := range lcs {
		lcs[i] = make([]int, n+1)
	}
	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	return lcs
}
