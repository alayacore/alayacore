package terminal

import (
	"reflect"
	"strings"
	"testing"
)

func TestRenderMarkdownTables_Basic(t *testing.T) {
	in := "| name | gender | age |\n|---|---|---|\n| Walllace Gibbon | male | 100 |\n| Harry Potter | male | 10 |"
	want := "┌─────────────────┬────────┬─────┐\n" +
		"│ name            │ gender │ age │\n" +
		"├─────────────────┼────────┼─────┤\n" +
		"│ Walllace Gibbon │ male   │ 100 │\n" +
		"├─────────────────┼────────┼─────┤\n" +
		"│ Harry Potter    │ male   │ 10  │\n" +
		"└─────────────────┴────────┴─────┘"
	got := renderMarkdownTables(in, 120)
	if got != want {
		t.Errorf("renderMarkdownTables mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderMarkdownTables_SurroundedByText(t *testing.T) {
	in := "Here is a table:\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\nDone."
	want := "Here is a table:\n\n┌───┬───┐\n│ a │ b │\n├───┼───┤\n│ 1 │ 2 │\n└───┴───┘\n\nDone."
	got := renderMarkdownTables(in, 80)
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderMarkdownTables_Passthrough(t *testing.T) {
	cases := []string{
		"plain text",
		"| not a table",
		"no | pipes | here",
		"| header |\nbut no delimiter row",
		"a | b\n---|---\n1 | 2", // no leading pipe on header → not a table
	}
	for _, in := range cases {
		if got := renderMarkdownTables(in, 80); got != in {
			t.Errorf("expected passthrough for %q, got %q", in, got)
		}
	}
}

func TestRenderMarkdownTables_FencedCodeUntouched(t *testing.T) {
	in := "```\n| name | age |\n|------|-----|\n| Bob  | 30  |\n```\n\ntext"
	want := in
	got := renderMarkdownTables(in, 80)
	if got != want {
		t.Errorf("fenced table must pass through:\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderMarkdownTables_FenceToggles(t *testing.T) {
	// Table before the fence is transformed, table inside is not.
	in := "| x |\n|---|\n| 1 |\n\n```\n| x |\n|---|\n| 2 |\n```"
	want := "┌───┐\n│ x │\n├───┤\n│ 1 │\n└───┘\n\n```\n| x |\n|---|\n| 2 |\n```"
	got := renderMarkdownTables(in, 80)
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderMarkdownTables_MultibyteWidth(t *testing.T) {
	in := "| 名字 | 性别 |\n|---|---|\n| 大猩猩 | 男 |"
	// 大猩猩 = 3 CJK chars = 6 display cols; 名字 = 4 cols; 性别 = 4; 男 = 2.
	want := "┌────────┬──────┐\n" +
		"│ 名字   │ 性别 │\n" +
		"├────────┼──────┤\n" +
		"│ 大猩猩 │ 男   │\n" +
		"└────────┴──────┘"
	got := renderMarkdownTables(in, 80)
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderMarkdownTables_EscapedPipe(t *testing.T) {
	// "\|" is a literal pipe in the cell: parsed out of the source, the
	// rendered cell shows "a|b".
	in := "| cmd |\n|---|\n| a\\|b |"
	want := "┌─────┐\n" +
		"│ cmd │\n" +
		"├─────┤\n" +
		"│ a|b │\n" +
		"└─────┘"
	got := renderMarkdownTables(in, 80)
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderMarkdownTables_EmptyCells(t *testing.T) {
	in := "| a | b | c |\n|---|---|---|\n| 1 || 3 |"
	want := "┌───┬───┬───┐\n" +
		"│ a │ b │ c │\n" +
		"├───┼───┼───┤\n" +
		"│ 1 │   │ 3 │\n" +
		"└───┴───┴───┘"
	got := renderMarkdownTables(in, 80)
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderMarkdownTables_HeaderOnlyNoBody(t *testing.T) {
	in := "| a | b |\n|---|---|"
	// A header-only table gets no dangling rule between header and bottom.
	want := "┌───┬───┐\n" +
		"│ a │ b │\n" +
		"└───┴───┘"
	got := renderMarkdownTables(in, 80)
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderMarkdownTables_Alignment(t *testing.T) {
	in := "| left | right | center |\n|:-----|------:|:------:|\n| a    |     b |   c    |"
	// Alignment comes from cell padding; the grid rule itself is uniform.
	want := "┌──────┬───────┬────────┐\n" +
		"│ left │ right │ center │\n" +
		"├──────┼───────┼────────┤\n" +
		"│ a    │     b │   c    │\n" +
		"└──────┴───────┴────────┘"
	got := renderMarkdownTables(in, 80)
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderMarkdownTables_Tabs(t *testing.T) {
	// A tab inside a cell is expanded from the line start (column of the
	// pipe, not the cell) — the parser expands the whole line first.
	in := "| a\tb | c |\n|---|---|\n| 1 | 2 |"
	got := renderMarkdownTables(in, 80)
	if !strings.Contains(got, "│ a     b │") {
		t.Fatalf("expected the tab-expanded cell in the grid, got %q", got)
	}
	// The cell "a\tb" must not survive as a raw tab.
	if strings.Contains(got, "\t") {
		t.Errorf("table output must not contain tabs: %q", got)
	}
}

func TestRenderMarkdownTables_FitsByWrapping(t *testing.T) {
	// Too wide for one line per row: the over-long cell wraps onto a
	// continuation row instead of being cut. Nothing is lost.
	in := "| name | gender | age |\n|---|---|---|\n| Walllace Gibbon | male | 100 |"
	got := renderMarkdownTables(in, 120)
	for _, want := range []string{"Walllace", "Gibbon", "male", "100"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "…") {
		t.Errorf("no cell may be truncated:\n%s", got)
	}
}

func TestRenderMarkdownTables_LongCellWraps(t *testing.T) {
	// A single over-long cell in a narrow terminal: hard-wrapped, exactly as
	// a terminal would break ordinary prose.
	in := "| name |\n|---|\n| Walllace Gibbon |"
	got := renderMarkdownTables(in, 40)
	if strings.Contains(got, "…") {
		t.Errorf("no cell may be truncated:\n%s", got)
	}
	if !strings.Contains(got, "Walllace Gibbon") {
		t.Errorf("cell must stay intact when it fits:\n%s", got)
	}
	// At a width the cell cannot fit on one row it splits across rows.
	got = renderMarkdownTables(in, 20)
	if strings.Contains(got, "…") {
		t.Errorf("no cell may be truncated at width 20:\n%s", got)
	}
	if !strings.Contains(got, "Walllace") || !strings.Contains(got, "Gibbon") {
		t.Errorf("both words must survive at width 20:\n%s", got)
	}
}

func TestRenderMarkdownTables_NarrowGoesVertical(t *testing.T) {
	// 5 columns need 3*5+1 = 16 cells of framing alone, so a 10-cell window
	// cannot hold the frame at all — before any question of legibility. The
	// layout therefore drops to one field per line.
	in := "| a | b | c | d | e |\n|---|---|---|---|---|\n| 1 | 2 | 3 | 4 | 5 |"
	got := renderMarkdownTables(in, 10)
	for _, want := range []string{"a", "1", "e", "5"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in vertical layout:\n%s", want, got)
		}
	}
	if strings.Contains(got, "…") {
		t.Errorf("vertical layout must not truncate:\n%s", got)
	}
}

// TestRenderMarkdownTables_FitToWidthInvariant is a property-style test:
// for any fitted table and any terminal width at or above the floor, every
// rendered row must fit within maxWidth display columns (so rows never
// mid-cell soft-wrap, keeping each table row exactly one visual line).
func TestRenderMarkdownTables_FitToWidthInvariant(t *testing.T) {
	in := "| name | gender | age |\n|---|---|---|\n| Walllace Gibbon | male | 100 |\n| Harry Potter | male | 10 |"
	// Floor row width = 3 cols × 3 + 3×3 + 1 = 19; test from there up.
	for w := 19; w <= 120; w++ {
		got := renderMarkdownTables(in, w)
		for _, line := range strings.Split(got, "\n") {
			if gotW := cellWidth(line); gotW > w {
				t.Errorf("width %d: row width %d exceeds terminal: %q", w, gotW, line)
			}
		}
	}
}

func TestWrapCellDropsTheSpaceItBrokeOn(t *testing.T) {
	// Deliberate, documented behavior (see wrapCell): a cell shares prose
	// break OFFSETS but not bytes. ansi.Hardwrap leaves the space it broke on
	// at the head of the continuation line; prose renders that stray leading
	// space, a table cell must not — it would shift the column by one cell.
	got := wrapCell("alpha beta gamma", 10)
	want := []string{"alpha beta", "gamma"}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
	// The wrap is character-boundary (no word detection), matching the
	// terminal: interior spaces survive, and a long word is split anywhere.
	got = wrapCell("alpha  beta  gamma", 14)
	want = []string{"alpha  beta  g", "amma"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestTableDetectionPredicatesAgree locks a coupling that is invisible from
// either function alone. A table row must start with '|' (GFM also allows
// omitting it) because the streaming path decides whether a delta could touch
// a table by testing for exactly that leading pipe. If the two predicates ever
// disagree, a delta carrying a real table row takes the O(delta) incremental
// append instead of a full re-render, and the table renders half-reflowed.
//
// Relaxing parser acceptance therefore REQUIRES relaxing deltaHasPipeLine in
// the same change — this test is the reminder.
func TestTableDetectionPredicatesAgree(t *testing.T) {
	lines := []string{
		"| a | b |", "  | a | b |", "\t| a | b |", "|", "| ",
		"a | b", "a|b", "", "   ", "not a table",
		"# heading", "|-|-|", "---|---", "x | y | z |",
	}
	for _, line := range lines {
		if want, got := isTableRow(line), deltaHasPipeLine(line); want != got {
			t.Errorf("predicates disagree on %q: isTableRow=%v deltaHasPipeLine=%v", line, want, got)
		}
	}
}

// TestWrappedAtLineBudgets guards per-line budgets in the record layout: the
// first line pays `first`, continuations pay `every`. Budgeting both against
// the wider prefix silently steals cells from line 0 — at a 10-cell window
// "COMMAND" (7) fits behind the 2-space indent, so splitting it is a defect,
// not a constraint.
func TestWrappedAtLineBudgets(t *testing.T) {
	cases := []struct {
		text         string
		maxWidth     int
		first, every string
		want         []string
	}{
		{"COMMAND", 10, "  ", "    ", []string{"  COMMAND"}},
		{"COMMAND", 9, "  ", "    ", []string{"  COMMAND"}},
		{"COMMAND", 8, "  ", "    ", []string{"  COMMAN", "    D"}},
	}
	for _, tc := range cases {
		if got := wrappedAt(tc.text, tc.maxWidth, tc.first, tc.every); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("wrappedAt(%q, %d) = %q, want %q", tc.text, tc.maxWidth, got, tc.want)
		}
	}
}

// TestRecordLayoutHasNoBlankLines guards against the wrapper emitting empty
// rows: ansi.Hardwrap yields an empty leading line when no grapheme cluster
// fits the requested width, which reads as a stray gap between fields.
func TestRecordLayoutHasNoBlankLines(t *testing.T) {
	// Every cell non-empty, so a blank output line is always a defect.
	in := "| 名称 | 说明 |\n|---|---|\n| 值 | 本地跑 |\n| 另一个 | 短 |"
	for _, w := range rangeWidths(1, 60) {
		for i, line := range strings.Split(renderMarkdownTables(in, w), "\n") {
			if strings.TrimSpace(line) == "" {
				t.Fatalf("width %d line %d is blank:\n%s", w, i, renderMarkdownTables(in, w))
			}
		}
	}
}

// TestRenderMarkdownTables_ContentGlyphsSurvives locks what the no-loss
// multiset check structurally cannot see: the box-drawing glyphs are also the
// framing glyphs, so dropLayoutNoise strips them from both sides and a lost
// "│" inside a cell would go unnoticed there. Exact rendered lines close that
// gap, and they double as the executable statement of the ambiguity documented
// in docs/markdown-rendering.md.
func TestRenderMarkdownTables_ContentGlyphsSurvive(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			// "\|" is an escaped pipe: the cell's content is a|b. The frame is
			// "│", so an ASCII pipe in data is now distinguishable from a
			// border — under the previous ASCII framing it was the same glyph.
			name: "escaped ASCII pipe in content",
			in:   "| cmd | out |\n|---|---|\n| a\\|b | ok |",
			want: []string{
				"┌─────┬─────┐",
				"│ cmd │ out │",
				"├─────┼─────┤",
				"│ a|b │ ok  │",
				"└─────┴─────┘",
			},
		},
		{
			// A real box-drawing vertical in the data. Widths stay correct, but
			// the row now shows four verticals where the header shows three, so
			// it reads as an extra column. Inherent to framing with glyphs that
			// data may also contain: the character must not be altered.
			name: "box-drawing vertical in content",
			in:   "| cmd | out |\n|---|---|\n| a│b | ok |",
			want: []string{
				"┌─────┬─────┐",
				"│ cmd │ out │",
				"├─────┼─────┤",
				"│ a│b │ ok  │",
				"└─────┴─────┘",
			},
		},
		{
			// Same for the horizontal rule glyph, which appears in the
			// separator rows.
			name: "box-drawing rule glyph in content",
			in:   "| a | b |\n|---|---|\n| x─y | z |",
			want: []string{
				"┌─────┬───┐",
				"│ a   │ b │",
				"├─────┼───┤",
				"│ x─y │ z │",
				"└─────┴───┘",
			},
		},
	}
	for _, tc := range cases {
		got := strings.Split(renderMarkdownTables(tc.in, 40), "\n")
		if len(got) != len(tc.want) {
			t.Fatalf("%s: got %d rows, want %d:\n%s", tc.name, len(got), len(tc.want), strings.Join(got, "\n"))
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Errorf("%s: row %d\n got %q\nwant %q", tc.name, i, got[i], tc.want[i])
			}
		}
		// The ambiguity is perceptual only: every row is still exactly as
		// wide as every other, so alignment and line heights are unaffected.
		w0 := cellWidth(got[0])
		for i, l := range got {
			if w := cellWidth(l); w != w0 {
				t.Errorf("%s: row %d is %d cells, row 0 is %d:\n%s", tc.name, i, w, w0, strings.Join(got, "\n"))
			}
		}
	}
}
