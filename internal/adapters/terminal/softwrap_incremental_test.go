package terminal

// Regression test: the incremental streaming path (appendDeltaToVisualLines)
// must produce exactly the same visual rows as a full re-wrap of the entire
// accumulated content. Previously, tabs were expanded per-delta (column
// starting at 0) in the incremental path but per-line in the full path — a
// delta starting with '\t' mid-line expanded to 8 spaces instead of the
// correct 2–4, shifting wrap points and sometimes splitting a single line
// into two (a visible hard wrap).

import (
	"fmt"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/theme"
)

func sameVisualLines(a, b []visualLine) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Text != b[i].Text || a[i].Cont != b[i].Cont {
			return false
		}
	}
	return true
}

func dumpVisual(label string, lines []visualLine) string {
	var sb strings.Builder
	sb.WriteString(label + ": " + fmt.Sprint(len(lines)) + " rows\n")
	for i, l := range lines {
		mark := "H" // hard break before this row (new original line)
		if l.Cont {
			mark = "S" // soft continuation
		}
		sb.WriteString(fmt.Sprintf("  [%02d %s] %q\n", i, mark, l.Text))
	}
	return sb.String()
}

// runIncrementalConsistencyCase feeds deltas through the incremental path
// and compares against a full re-wrap of all accumulated content after
// every delta.
func runIncrementalConsistencyCase(t *testing.T, name string, deltas []string) {
	t.Helper()
	styles := NewStyles(theme.DefaultTheme())
	all := make([]string, 0, len(deltas))
	r := &textRenderer{tag: "AT"}
	for i, d := range deltas {
		all = append(all, d)
		r.AppendFromTLV("AT", d)

		incLines, _ := r.BuildInner(80, false, styles)
		fullLines := wrapVisualLines(stripANSI(strings.Join(all, "")), 80)

		if !sameVisualLines(incLines, fullLines) {
			t.Errorf("=== %s: MISMATCH after delta %d (%q) ===\n%s\n%s",
				name, i, d, dumpVisual("incremental", incLines), dumpVisual("full rewrap", fullLines))
			return
		}
	}
}

func TestIncrementalMatchesFullRewrap(t *testing.T) {
	longLine := strings.Repeat("word ", 40) // 200 chars, wraps to 3 rows @80

	// 1. token-by-token streaming of one long line (realistic LLM)
	runIncrementalConsistencyCase(t, "token streaming", []string{
		"The", " quick", " brown", " fox", " jumps", " over", " the", " lazy", " dog ", "and", " keeps", " running",
	})

	// 2. long line split across many small deltas with \n at the end
	runIncrementalConsistencyCase(t, "long line + newline", []string{
		longLine,
		"\nsecond paragraph " + longLine,
		"\nthird",
	})

	// 3. \r\n (CRLF) mid-stream
	runIncrementalConsistencyCase(t, "CRLF", []string{
		"line one\r\nline two\r\n" + longLine,
	})

	// 4. delta starts with newline (paragraph breaks)
	runIncrementalConsistencyCase(t, "delta starting with \\n", []string{
		"first paragraph",
		"\n\nsecond paragraph " + strings.Repeat("b", 90),
	})

	// 5. tab mid-line across delta boundary (the original bug)
	runIncrementalConsistencyCase(t, "tab at delta start", []string{
		"prefix text " + strings.Repeat("a", 10),
		"\tindented",
	})

	// 6. tab mid-line, NOT at delta boundary (tab inside a single delta)
	runIncrementalConsistencyCase(t, "tab inside delta", []string{
		"long code line with\ttab and " + strings.Repeat("z", 70) + "\n",
	})

	// 7. CJK wide chars across delta boundary
	runIncrementalConsistencyCase(t, "CJK across deltas", []string{
		strings.Repeat("汉", 39), // 78 cols
		"字",                     // 2 cols → 80, exactly full
		"更多内容" + strings.Repeat("汉", 40),
	})

	// 8. code-like: indentation tabs at line starts (each delta a full line)
	runIncrementalConsistencyCase(t, "code lines with tabs", []string{
		"func main() {\n",
		"\tprintln(\"hello\")\n",
		"\tfor i := 0; i < 10; i++ {\n",
		"\t\tfmt.Println(i)\n",
		"\t}\n",
		"}\n",
	})

	// 9. trailing space then continuation (hardwrap preserveSpace)
	runIncrementalConsistencyCase(t, "trailing spaces", []string{
		"line with trailing spaces    ",
		"continued",
	})

	// 10. very long single token (no spaces) > width
	runIncrementalConsistencyCase(t, "long token", []string{
		"https://example.com/" + strings.Repeat("a", 120),
		"/path",
	})

	// 11–14. tab at delta boundary pushing the line over the wrap point —
	// incremental used to expand the tab from col 0 (8 spaces) instead of
	// the line's actual column (2–4 spaces), shifting wrap points and
	// occasionally splitting one visual row into two.
	for _, n := range []int{78, 77, 76, 79} {
		runIncrementalConsistencyCase(t, fmt.Sprintf("tab shifts wrap point (%d cols)", n), []string{
			strings.Repeat("a", n),
			"\tb",
		})
	}
}
