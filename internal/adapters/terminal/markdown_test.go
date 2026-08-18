package terminal

import (
	"strings"
	"testing"

	ansi "github.com/charmbracelet/x/ansi"
)

func TestRenderMarkdownTables_Basic(t *testing.T) {
	in := "| name | gender | age |\n|---|---|---|\n| Walllace Gibbon | male | 100 |\n| Harry Potter | male | 10 |"
	want := "| name            | gender | age |\n" +
		"|-----------------|--------|-----|\n" +
		"| Walllace Gibbon | male   | 100 |\n" +
		"| Harry Potter    | male   | 10  |"
	got := renderMarkdownTables(in, 120)
	if got != want {
		t.Errorf("renderMarkdownTables mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderMarkdownTables_SurroundedByText(t *testing.T) {
	in := "Here is a table:\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\nDone."
	want := "Here is a table:\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\nDone."
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
	want := "| x |\n|---|\n| 1 |\n\n```\n| x |\n|---|\n| 2 |\n```"
	got := renderMarkdownTables(in, 80)
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderMarkdownTables_MultibyteWidth(t *testing.T) {
	in := "| 名字 | 性别 |\n|---|---|\n| 大猩猩 | 男 |"
	// 大猩猩 = 3 CJK chars = 6 display cols; 名字 = 4 cols; 性别 = 4; 男 = 2.
	want := "| 名字   | 性别 |\n" +
		"|--------|------|\n" +
		"| 大猩猩 | 男   |"
	got := renderMarkdownTables(in, 80)
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderMarkdownTables_EscapedPipe(t *testing.T) {
	// "\|" is a literal pipe in the cell: parsed out of the source, the
	// rendered cell shows "a|b".
	in := "| cmd |\n|---|\n| a\\|b |"
	want := "| cmd |\n" +
		"|-----|\n" +
		"| a|b |"
	got := renderMarkdownTables(in, 80)
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderMarkdownTables_EmptyCells(t *testing.T) {
	in := "| a | b | c |\n|---|---|---|\n| 1 || 3 |"
	want := "| a | b | c |\n" +
		"|---|---|---|\n" +
		"| 1 |   | 3 |"
	got := renderMarkdownTables(in, 80)
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderMarkdownTables_HeaderOnlyNoBody(t *testing.T) {
	in := "| a | b |\n|---|---|"
	want := "| a | b |\n" +
		"|---|---|"
	got := renderMarkdownTables(in, 80)
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderMarkdownTables_Alignment(t *testing.T) {
	in := "| left | right | center |\n|:-----|------:|:------:|\n| a    |     b |   c    |"
	want := "| left | right | center |\n" +
		"|:-----|------:|:------:|\n" +
		"| a    |     b |   c    |"
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
	if !strings.Contains(got, "|") {
		t.Fatalf("expected table output, got %q", got)
	}
	// The cell "a\tb" must not survive as a raw tab.
	if strings.Contains(got, "\t") {
		t.Errorf("table output must not contain tabs: %q", got)
	}
}

func TestRenderMarkdownTables_FitToWidth(t *testing.T) {
	// Natural width: name col 15 + gender 6 + age 3 + 3*3+1 framing = 34.
	// Terminal 30 → shrink widest column (name 15→11): row = 30.
	in := "| name | gender | age |\n|---|---|---|\n| Walllace Gibbon | male | 100 |"
	got := renderMarkdownTables(in, 30)
	want := "| name        | gender | age |\n" +
		"|-------------|--------|-----|\n" +
		"| Walllace G… | male   | 100 |"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderMarkdownTables_FitToWidthTruncatesLongestCell(t *testing.T) {
	// A single over-long cell: that column shrinks and the cell truncates.
	// Row width = 15 + 3×1 + 1 = 19; at width 12 the column shrinks to 8.
	in := "| name |\n|---|\n| Walllace Gibbon |"
	got := renderMarkdownTables(in, 12)
	want := "| name     |\n" +
		"|----------|\n" +
		"| Walllac… |"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderMarkdownTables_FitToWidthFloor(t *testing.T) {
	// 5 columns cannot fit even at the 3-col floor in a 10-col terminal:
	// floor row width = 3*5 + 3*5 + 1 = 31 > 10 → left unshrunk (the
	// hard-wrap path handles the overflow; line heights stay correct).
	in := "| a | b | c | d | e |\n|---|---|---|---|---|\n| 1 | 2 | 3 | 4 | 5 |"
	got := renderMarkdownTables(in, 10)
	want := "| a | b | c | d | e |\n" +
		"|---|---|---|---|---|\n" +
		"| 1 | 2 | 3 | 4 | 5 |"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderMarkdownTables_CRLF(t *testing.T) {
	in := "| a | b |\r\n|---|---|\r\n| 1 | 2 |\r\n"
	got := renderMarkdownTables(in, 80)
	if strings.Contains(got, "\r") {
		t.Errorf("output must not contain CR: %q", got)
	}
	if !strings.Contains(got, "| a | b |") {
		t.Errorf("table must be transformed: %q", got)
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
			if gotW := ansi.StringWidth(line); gotW > w {
				t.Errorf("width %d: row width %d exceeds terminal: %q", w, gotW, line)
			}
		}
	}
}
