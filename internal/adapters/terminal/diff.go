package terminal

// Line-level diff for the edit_file window: the model's old_string and
// new_string are split into lines, matched with an LCS, and drawn as rows — the
// change, its context, and one chrome row for every stretch the block does not
// draw.

// The row markers. Two cells each, and they are the whole of what tells one
// block row from another — the handler writes them and RenderDiffContent reads
// them, so they are named here rather than spelled at both ends.
//
// The elided rows take "…", the truncation marker the rest of this UI uses
// (folded summaries, delta previews). No diff row can start with it, which is
// the point: a row that is not a line of either input must not be drawn as one
// — and must not be *read* as one either, by a reader or by the renderer's
// color rules.
const (
	diffMarkerContext = "  "
	diffMarkerRemoved = "- "
	diffMarkerAdded   = "+ "
	diffMarkerElided  = "… "
)

// diffRowKind is what a row is.
type diffRowKind uint8

const (
	rowContext         diffRowKind = iota // a line both inputs have, drawn
	rowRemoved                            // a line only old_string has
	rowAdded                              // a line only new_string has
	rowElidedUnchanged                    // unchanged lines, not drawn
	rowElidedTooLarge                     // the arguments undiffed: too large to diff
)

// diffRow is one row of the block.
//
// The kind is stored, not inferred from the text, and that is the whole reason
// this type is not a pair of strings. An empty line is a line like any other, so
// a removed blank line and an unchanged one are the same text — "" — and any
// rule that reads the kind back out of that text has to call one of them the
// other. The same holds for the rows that stand in for lines nobody draws: what
// they say is derived from the kind and the count, never parsed back out of it.
type diffRow struct {
	kind  diffRowKind
	line  string // the line itself; empty for the rows that stand in for lines not drawn
	count int    // how many lines an elided row stands for
}

// marker is the row's two-cell prefix.
func (r diffRow) marker() string {
	switch r.kind {
	case rowRemoved:
		return diffMarkerRemoved
	case rowAdded:
		return diffMarkerAdded
	case rowElidedUnchanged, rowElidedTooLarge:
		return diffMarkerElided
	default:
		return diffMarkerContext
	}
}

// text is what the row says after its marker: the line itself, or — for the rows
// that stand in for lines the block does not draw — the count that makes the
// omission account for something. A hidden row that does not say how much it
// hides is the same failure as a change drawn as no change, one level up.
//
// The count comes through lineCountText, the one place that knows a count of one
// is "1 line" (live_edge.go), so the three counters in this UI cannot disagree
// about how to say it.
func (r diffRow) text() string {
	switch r.kind {
	case rowElidedUnchanged:
		return lineCountText(r.count, "hidden")
	case rowElidedTooLarge:
		return lineCountText(r.count, "hidden") + ", too large to diff"
	default:
		return r.line
	}
}

// spell is the row as the block carries it: marker, then text.
func (r diffRow) spell() string { return r.marker() + r.text() }

// diffContext is how many unchanged rows a hunk keeps on each side of a change.
// Three is what a patch viewer shows, for the reason a patch viewer shows it:
// enough to read a replacement where it lands, few enough that a window stays a
// window.
const diffContext = 3

// maxDiffLines caps the LCS table for edit_file diffs. The old/new strings come
// from the model's tool-call arguments and can be arbitrarily large — an
// unbounded m×n table would exhaust memory on huge inputs (10k × 10k lines =
// 800MB of cells, which OOM'd a session once). Above this cap computeDiff
// answers with a row that says so, and no table at all.
//
// The cap sits here, with the table it protects, rather than at the call site:
// a bound is only as good as the next caller that remembers to apply it. It
// applies to what is left after the shared ends are trimmed (see computeDiff),
// so a large block that changed in one place is still diffed.
const maxDiffLines = 2000

// computeDiff returns the rows the edit_file block draws for the two arguments:
// the change with its context, and a chrome row for every stretch it does not
// draw. What the block shows is a decision, not a transcript of the inputs, and
// these are the three terms of it:
//
//   - the ends both inputs share are context rows. They need no table (they are
//     equal by inspection) and trimming them first is what keeps a big block
//     with a small change out of the fallback below;
//   - a run of unchanged rows longer than a hunk's context is drawn as one row
//     that counts it, so the block is as long as the change and not as long as
//     the arguments;
//   - above maxDiffLines the pair is not diffed, and one row says so. Drawing
//     every line as removed and added instead would be a claim about the change
//     — "all of it changed" — that nobody made, and it is the longest possible
//     way to say nothing.
func computeDiff(oldLines, newLines []string) []diffRow {
	prefix, suffix := sharedEnds(oldLines, newLines)
	oldMid := oldLines[prefix : len(oldLines)-suffix]
	newMid := newLines[prefix : len(newLines)-suffix]

	rows := make([]diffRow, 0, len(oldLines)+len(newLines))
	for _, line := range oldLines[:prefix] {
		rows = append(rows, diffRow{kind: rowContext, line: line})
	}
	if len(oldMid) > maxDiffLines || len(newMid) > maxDiffLines {
		rows = append(rows, diffRow{kind: rowElidedTooLarge, count: len(oldMid) + len(newMid)})
	} else {
		rows = append(rows, lcsRows(oldMid, newMid)...)
	}
	for _, line := range oldLines[len(oldLines)-suffix:] {
		rows = append(rows, diffRow{kind: rowContext, line: line})
	}

	return elideUnchangedRuns(rows)
}

// sharedEnds returns how many lines the two inputs have in common at the start
// and at the end, without overlapping: the caller can slice both inputs by
// (prefix, suffix) and be left with the two middles that actually differ.
func sharedEnds(oldLines, newLines []string) (prefix, suffix int) {
	limit := min(len(oldLines), len(newLines))
	for prefix < limit && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	for suffix < limit-prefix && oldLines[len(oldLines)-1-suffix] == newLines[len(newLines)-1-suffix] {
		suffix++
	}
	return prefix, suffix
}

// elideUnchangedRuns replaces each run of unchanged rows longer than a hunk's
// context with one row that counts it — the drawn rows on either side stay, so
// every change keeps its context, and the count is what keeps the omission
// honest. A run is only folded when folding saves more rows than it costs:
// diffContext rows are kept at each end, so the shortest folded run is
// 2*diffContext+1 rows of window for 1.
//
// This is what bounds the block. The rows above are proportional to the
// arguments; the rows below are proportional to the number of changes, which is
// the thing the reader came for.
func elideUnchangedRuns(rows []diffRow) []diffRow {
	out := make([]diffRow, 0, len(rows))
	for i := 0; i < len(rows); {
		if rows[i].kind != rowContext {
			out = append(out, rows[i])
			i++
			continue
		}
		run := 0
		for i+run < len(rows) && rows[i+run].kind == rowContext {
			run++
		}
		if run <= 2*diffContext+1 {
			out = append(out, rows[i:i+run]...)
		} else {
			out = append(out, rows[i:i+diffContext]...)
			out = append(out, diffRow{kind: rowElidedUnchanged, count: run - 2*diffContext})
			out = append(out, rows[i+run-diffContext:i+run]...)
		}
		i += run
	}
	return out
}

// lcsRows returns the rows that turn oldLines into newLines: unchanged lines as
// context, the rest as removals and additions covering the longest common
// subsequence's gaps. A changed line is a removal followed by the addition that
// replaced it, so it reads "- old" over "+ new". No row here stands in for a
// line it does not show; bounding what is drawn is elideUnchangedRuns' job.
func lcsRows(oldLines, newLines []string) []diffRow {
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
			rows = append(rows, diffRow{kind: rowContext, line: oldLines[i]})
			i++
			j++
		case j < n && (i == m || lcs[i][j+1] > lcs[i+1][j]):
			rows = append(rows, diffRow{kind: rowAdded, line: newLines[j]})
			j++
		default:
			rows = append(rows, diffRow{kind: rowRemoved, line: oldLines[i]})
			i++
		}
	}

	return rows
}

// lcsSuffixLengths returns lcs, where lcs[i][j] is the length of the longest
// common subsequence of oldLines[i:] and newLines[j:] — the suffix table,
// because lcsRows walks forward and asks what is still matchable *after* the
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
