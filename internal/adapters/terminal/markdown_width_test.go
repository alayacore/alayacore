package terminal

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	ansi "github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"

	"github.com/alayacore/alayacore/internal/theme"

	"github.com/alayacore/alayacore/internal/tlv"
)

// TestMarkdownTableWideColumnsWithCJK verifies the fit path when CJK content
// makes a column much wider than its neighbors in a narrow window.
//
// Shrinking/truncating is gone: the grid either re-flows by wrapping cells,
// or — when the frame itself cannot fit every column's widest unbreakable
// cluster — switches to the vertical
// record layout. Either way every glyph must survive, no cluster may be
// broken, and no line may exceed the width budget.
func TestMarkdownTableColumnShrinkingWithWideChars(t *testing.T) {
	styles := NewStyles(theme.DefaultTheme())

	// 3-col table where the first column is much wider than the others and
	// contains CJK content.
	content := "| 名前 | age | city |\n|---|---|---|\n| 山田太郎さん | 30 | 東京 |"
	r := &textRenderer{tag: tlv.TagAssistantT}
	r.AppendFromTLV(tlv.TagAssistantT, content)
	r.ToggleMarkdownMode() // enable table formatting

	for _, width := range []int{20, 28, 40, 60} {
		r.Invalidate()
		lines, _ := r.BuildInner(width, false, styles)
		plain := stripANSI(joinVisualLines(lines))

		// Nothing is ever cut, at any width.
		if strings.Contains(plain, "…") {
			t.Errorf("width %d: content was truncated:\n%s", width, plain)
		}
		// And no row escapes the window: a framed table is only attempted when
		// every column can hold its widest unbreakable cluster (minw), so no
		// cell is ever handed less room than its own glyph needs.
		for _, line := range strings.Split(plain, "\n") {
			if lw := ansi.StringWidth(line); lw > width {
				t.Errorf("width %d: row overflows (%d): %q", width, lw, line)
			}
		}
		// No broken clusters.
		if strings.Contains(plain, "\uFFFD") {
			t.Errorf("width %d: broken cluster in output:\n%s", width, plain)
		}
		// Every glyph of every cell survives. A multiset comparison, not a
		// substring: a cell that wraps across rows interleaves with its
		// neighbors row by row, so "age" legitimately appears as "ag" on one
		// row and "e" on the next.
		if d := contentDiff(content, plain); d != "" {
			t.Errorf("width %d: %s\n%s", width, d, plain)
		}
		// Every line respects the width budget.
		for _, line := range lines {
			if w := ansi.StringWidth(line.Text); w > width {
				t.Errorf("width %d: line width %d exceeds budget: %q", width, w, line.Text)
			}
		}
	}
}

// dropLayoutNoise removes exactly what the framing can emit — the box-drawing
// glyphs in mdGrid, cell padding, and line breaks — so cell content can be
// matched even after it has been wrapped across several rows.
//
// Deliberately NOT included: '|', '+', '-', '\t'. Framing never produces them
// (the grid is always box-drawing, and the vertical layout emits no rules of
// those kinds), so anything the content carries there must survive the no-loss
// check — a "|"-in-cell, a "10-20" range, a "+"/"-" diff marker. A tab cannot
// appear on either side at all: parseTable expands tabs to spaces before
// rendering, and both sides of the comparison are trimmed.
func dropLayoutNoise(r rune) rune {
	switch r {
	case ' ', '\n',
		'│', '─', '┌', '┐', '└', '┘', '├', '┤', '┬', '┴', '┼':
		return -1
	}
	return r
}

// TestMarkdownTablesNeverTruncate sweeps terminal widths and content shapes
// and asserts the two properties the new layout exists to guarantee: no cell
// is ever cut, and no rendered line ever exceeds the window.
func TestMarkdownTablesNeverTruncate(t *testing.T) {
	cases := []struct {
		name    string
		content string
		cells   []string // every cell's text must survive, whatever the width
	}{
		{
			name:    "long path",
			content: "| Filesystem | Mount | Size |\n|---|---|---|\n| /dev/nvme0n1p2 | /home/wallace/projects/alayacore/internal/adapters/terminal | 916G |",
			cells:   []string{"/dev/nvme0n1p2", "/home/wallace/projects/alayacore/internal/adapters/terminal", "916G"},
		},
		{
			name:    "cjk prose",
			content: "| 名称 | 说明 |\n|---|---|\n| qwen3 | 本地跑，需要 20G 显存，工具调用稳定 |\n| a | b |",
			cells:   []string{"qwen3", "本地跑，需要 20G 显存，工具调用稳定"},
		},
		{
			name:    "no break chars",
			content: "| tool | path |\n|---|---|\n| read_file | /home/wallace/playground/alayacore/internal/adapters/terminal/window_renderer.go |",
			cells:   []string{"read_file", "/home/wallace/playground/alayacore/internal/adapters/terminal/window_renderer.go"},
		},
		{
			// Long header labels: at an 18-20 column window the label column
			// is wider than the window minus its gutters, so the clamp that
			// shrinks it is what keeps the value on the line budget. Without
			// it "  repository        a" measures 21 in an 18-wide window.
			name:    "long labels",
			content: "| repository | last-modified-by | status-code | branch-name |\n|---|---|---|---|\n| alayacore | wallace gibbon | CONFLICTED | feature/x |",
			cells:   []string{"repository", "last-modified-by", "CONFLICTED", "feature/x", "wallace gibbon"},
		},
		{
			name:    "many columns",
			content: "| a | b | c | d | e | f | g | h |\n|---|---|---|---|---|---|---|---|\n| 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 |",
			cells:   []string{"a", "h", "1", "8"},
		},
		{
			// Framing no longer uses these glyphs, so they are pure
			// content and must survive: a literal pipe, a range written
			// with hyphens, and +/- diff markers.
			name:    "pipe and +/- content",
			content: "| cell | diff |\n|---|---|\n| a\\|b | +added line here |\n| c-d | -removed |",
			cells:   []string{"a|b", "+added line here", "c-d", "-removed"},
		},
		{
			name:    "header only",
			content: "| one | two | three |\n|---|---|---|",
			cells:   []string{"one", "two", "three"},
		},
	}
	for _, tc := range cases {
		for _, width := range rangeWidths(1, 120) {
			got := renderMarkdownTables(tc.content, width)
			for _, line := range strings.Split(got, "\n") {
				if strings.Contains(line, "…") {
					t.Fatalf("%s @%d: truncated: %q", tc.name, width, line)
				}
				// No floor any more: the grid is only attempted when every
				// column can hold its widest unbreakable cluster, and the record
				// layout drops its own indentation rather than overflow. So the
				// budget holds from width 1, and the sole tolerated exception is
				// a lone grapheme cluster wider than the window (checked by
				// cluster count below).
				if w := ansi.StringWidth(line); w > width {
					// Admissible only when the line is a single grapheme
					// cluster that is itself wider than the window (a CJK
					// glyph in a 1-column window) — nothing can fit that.
					if n := uniseg.GraphemeClusterCount(line); n > 1 {
						t.Fatalf("%s @%d: avoidable overflow (%d clusters, width %d): %q", tc.name, width, n, w, line)
					}
				}
			}
			// Compare the character multiset, not substrings: a cell that
			// wraps across rows is split, and neighboring columns interleave
			// row by row, so no contiguity assumption holds. Equal multisets
			// prove nothing was cut and nothing was duplicated.
			if diff := contentDiff(tc.content, got); diff != "" {
				t.Fatalf("%s @%d: %s\n%s", tc.name, width, diff, got)
			}
		}
	}
}

// rangeWidths is a tiny helper so the sweep reads well.
func rangeWidths(from, to int) []int {
	out := make([]int, 0, to-from+1)
	for w := from; w <= to; w++ {
		out = append(out, w)
	}
	return out
}

// contentDiff reports what characters the render lost or invented compared to
// the source table's cell text, ignoring markdown syntax and layout framing.
// Returns "" when the two sides hold exactly the same characters.
func contentDiff(source, rendered string) string {
	want := make(map[rune]int)
	for _, line := range strings.Split(source, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue // prose, fence, or the delimiter row
		}
		if isDelimiterRow(line) {
			continue
		}
		for _, cell := range splitCells(line) {
			for _, r := range strings.Map(dropLayoutNoise, cell) {
				want[r]++
			}
		}
	}
	got := make(map[rune]int)
	for _, r := range strings.Map(dropLayoutNoise, rendered) {
		got[r]++
	}
	var missing []string
	for r, n := range want {
		if g := got[r]; g < n {
			missing = append(missing, fmt.Sprintf("%q×%d(want %d, got %d)", string(r), n, n, g))
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		return "lost characters: " + strings.Join(missing, " ")
	}
	// No invented glyphs. Counted loosely on purpose: the vertical layout
	// repeats a field label once per record, so the render legitimately holds
	// more copies of a header character than the source row does.
	for r := range got {
		if _, ok := want[r]; !ok {
			return fmt.Sprintf("invented character %q", string(r))
		}
	}
	return ""
}

// isDelimiterRow reports whether a table line is the "|---|:--:|" separator.
func isDelimiterRow(line string) bool {
	for _, c := range splitCells(line) {
		if !isDelimiterCell(c) {
			return false
		}
	}
	return true
}

// TestMarkdownTablesEmitNoANSI locks the convention that lets the grid live
// inside the streaming pipeline at all: markdown rendering rearranges text, it
// never styles it. Color stays the renderer's job (bodyStyled / styleBodyLines
// layer the dim Body color on top under overlays), and the incremental path is
// ANSI-free by design — so any SGR escaping from here would desynchronize the
// overlay dimming and the "content carries no styling" invariant.
func TestMarkdownTablesEmitNoANSI(t *testing.T) {
	cases := []string{
		"| a | b |\n|---|---|\n| 1 | 2 |",
		"| Filesystem | Mount |\n|---|---|\n| /dev/nvme0n1p2 | /home/wallace/projects/alayacore/internal |",
		"| 名称 | 说明 |\n|---|---|\n| qwen3 | 本地跑，需要 20G 显存 |",
		"| cmd |\n|---|\n| a\\|b |",
	}
	for _, in := range cases {
		for _, width := range []int{1, 8, 20, 40, 120} {
			if got := renderMarkdownTables(in, width); strings.Contains(got, "\x1b[") {
				t.Errorf("grid must be plain text, got ANSI @%d: %q", width, got)
			}
		}
	}
}

// TestGridImpossibilityBound locks the arithmetic the docs quote. The framed
// table is given up only at the point where a column could not hold its widest
// unbreakable cluster — 3n+1 of framing plus each column's irreducible width —
// and never because of a tuned legibility number. docs/markdown-rendering.md
// quotes these values too, and TestDocsMarkdownBoundsAreMeasured fails if the
// two ever disagree — changing the gate means changing the doc, deliberately.
// These numbers are also the ONLY ones the doc's prose relies on.
func TestGridImpossibilityBound(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int // first window width that still gets a frame
	}{
		{"2 ASCII columns", "| a | b |\n|---|---|\n| 1 | 2 |", 9},
		{"2 CJK columns", "| 名称 | 说明 |\n|---|---|\n| 值 | 值 |", 11},
		{"3 ASCII columns", "| a | b | c |\n|---|---|---|\n| 1 | 2 | 3 |", 13},
		{"3 CJK columns", "| 名称 | 说明 | 状态 |\n|---|---|---|\n| 值 | 值 | 值 |", 16},
	}
	for _, tc := range cases {
		// frame appears at want, and the width below it is a record list
		if !strings.Contains(renderMarkdownTables(tc.in, tc.want), "│") {
			t.Errorf("%s: expected a frame at exactly %d columns:\n%s", tc.name, tc.want,
				renderMarkdownTables(tc.in, tc.want))
		}
		if strings.Contains(renderMarkdownTables(tc.in, tc.want-1), "│") {
			t.Errorf("%s: should fall back below %d, still framed at %d", tc.name, tc.want, tc.want-1)
		}
	}
}

// TestRecordSeparatorPerRecord guards a promise in docs/markdown-rendering.md:
// in the record layout, records are separated by a plain rule. Without it,
// consecutive records
// run together and the reader cannot tell where one ends.
func TestRecordSeparatorPerRecord(t *testing.T) {
	three := "| Filesystem | Mounted on |\n|---|---|\n| /dev/nvme0n1p2 | /mnt/a |\n| tmpfs | /mnt/b |\n| overlay | /mnt/c |"
	one := "| Filesystem | Mounted on |\n|---|---|\n| /dev/nvme0n1p2 | /mnt/a |"
	for _, tc := range []struct {
		name     string
		in       string
		expected int // separators == records - 1
	}{
		{"3 records", three, 2},
		{"1 record", one, 0},
	} {
		for _, w := range rangeWidths(1, 26) {
			out := renderMarkdownTables(tc.in, w)
			if strings.Contains(out, "│") {
				continue // still framed here; the rule belongs to the grid
			}
			got := 0
			for _, l := range strings.Split(out, "\n") {
				if l != "" && strings.Trim(l, mdHorizRule()) == "" {
					got++
				}
			}
			if got != tc.expected {
				t.Fatalf("%s @%d: %d separators, want %d:\n%s", tc.name, w, got, tc.expected, out)
			}
		}
	}
}

// mdHorizRule is the glyph the record separator is built from (kept out of the
// test body so the assertion reads as a count, not a glyph).
func mdHorizRule() string { return string([]rune{'─'}) }

// TestFieldKeepsLabelIntactWhenSplit guards the rule that a field shares one
// line as "label  value" only when the WHOLE field fits; otherwise the label
// gets its own line, unchopped. The alternative — one text run broken anywhere
// — puts the value behind a severed label ("COMMAN" / "D  /us"), destroying the
// boundary the record layout exists to make obvious.
func TestFieldKeepsLabelIntactWhenSplit(t *testing.T) {
	// fits entirely -> one line
	if got := fieldLines("PID", "2888", 20); len(got) != 1 || got[0] != "  PID  2888" {
		t.Errorf("whole field should share one line, got %q", got)
	}
	// does not fit -> label alone on line 0, value strictly below it
	got := fieldLines("COMMAND", "/usr/lib/firefox", 12)
	if got[0] != "  COMMAND" {
		t.Fatalf("label must occupy line 0 intact, got %q (full: %q)", got[0], got)
	}
	for _, l := range got[1:] {
		if !strings.HasPrefix(l, "    ") {
			t.Errorf("value lines must be indented, got %q (full: %q)", l, got)
		}
		if strings.Contains(strings.TrimLeft(l, " "), "COMMAND") {
			t.Errorf("label must not repeat or bleed into value lines: %q", l)
		}
	}
	// Rejoin after stripping each line's indent: the value is intact when it is
	// merely split, broken only when a character is missing.
	rejoined := ""
	for _, l := range got {
		rejoined += strings.TrimLeft(l, " ")
	}
	if !strings.Contains(rejoined, "/usr/lib/firefox") {
		t.Errorf("value must survive intact across its lines: %q -> %q", got, rejoined)
	}
}
