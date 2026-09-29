package terminal

// The summary paths (headAndTailParts, tailParts) used to escape the WHOLE
// message and then cut ~75 cells out of it; they now cut, escape the cut, and
// cut again. This file is the evidence that the reorder changed no output, and
// the one place where it did change output is named as the bug it fixes.
//
// The oracles below are the previous implementations, verbatim. Keeping them is
// the point: a differential test against a copy of the old code proves the new
// one is the same function, which is what lets the reorder be reviewed on its
// own merits instead of on a reading of every corpus case.

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// oldHeadAndTailParts is headAndTailParts before the reorder: escape
// everything, then cut.
func oldHeadAndTailParts(content string, maxWidth int) (head, tail string, truncated bool) {
	if maxWidth <= 0 {
		return "", "", false
	}
	escaped := strings.ReplaceAll(content, "\n", "\\n")
	escaped = strings.ReplaceAll(escaped, "\r", "")

	if cellWidth(escaped) <= maxWidth {
		return escaped, "", false
	}
	if maxWidth <= 2 {
		return takeCells(escaped, maxWidth), "", true
	}
	headWidth := maxWidth * 40 / 100
	if headWidth < 1 {
		headWidth = 1
	}
	tailWidth := maxWidth - headWidth - 1
	if tailWidth < 1 {
		return takeCells(escaped, maxWidth), "", true
	}
	return takeCells(escaped, headWidth), tailCells(escaped, tailWidth), true
}

// oldTailParts is tailParts before the reorder: escape everything, convert to
// runes, then walk backwards one RUNE at a time measuring each on its own.
func oldTailParts(content string, maxWidth int) (string, bool) {
	if maxWidth <= 1 {
		return "", false
	}
	escaped := strings.ReplaceAll(content, "\n", "\\n")
	escaped = strings.ReplaceAll(escaped, "\r", "")
	if cellWidth(escaped) <= maxWidth {
		return escaped, false
	}
	runes := []rune(escaped)
	width := 0
	start := len(runes)
	for i := len(runes) - 1; i >= 0; i-- {
		w := cellWidth(string(runes[i]))
		if width+w > maxWidth {
			break
		}
		width += w
		start = i
	}
	return string(runes[start:]), true
}

// summaryCorpus is what reaches the summary paths: model prose and tool output,
// with the line breaks a summary has to flatten, the clusters width libraries
// disagree about, and runs long enough to need a real cut. Each entry is tested
// as given and as prepareContent'd, because callers hand over both shapes.
func summaryCorpus() []string {
	return []string{
		"",
		"hello",
		"a\nb",
		"a\r\nb",
		"a\rb",
		"\n\n\n\n\na",
		"abc\nd",
		"a\tb\nc",
		"中\n文",
		"a\nb\nc",
		"x y z\n\nq",
		`\nescaped newline`,
		strings.Repeat("x\n", 50),
		strings.Repeat("中", 40),
		strings.Repeat("word ", 40),
		strings.Repeat("reasoning about the task and planning the next steps\n", 40),
		"line one is long enough to need a cut\nline two",
		"1️⃣\n2️⃣\n3️⃣",
		"e\u0301\nf\u0301",
		"👨‍👩‍👧‍👦 family\nnext line",
		"aaaa 👨‍👩‍👧‍👦",
		"aaaa 1️⃣",
		"aaaa e\u0301",
		"aaaa ❤️",
		"aaaa कि",
	}
}

// TestSummaryEscapeOrderIsEquivalent is the equivalence proof for the reorder:
// cutting first and escaping the cut gives byte-for-byte what escaping first
// and cutting gave, at every budget, on both shapes of every corpus entry.
//
// The argument it checks is that escaping can only widen a character — a '\n'
// is 0 cells and its marker is 2, a '\r' is 0 cells and is deleted, everything
// else keeps its width — so the escaped text that fits a budget always sits
// inside the raw cut, and re-cutting from the same end with the same budget
// lands on the same boundary.
func TestSummaryEscapeOrderIsEquivalent(t *testing.T) {
	compared := 0
	for _, raw := range summaryCorpus() {
		for _, content := range []string{raw, prepareContent(raw)} {
			for w := 0; w <= 40; w++ {
				wantH, wantT, wantTrunc := oldHeadAndTailParts(content, w)
				gotH, gotT, gotTrunc := headAndTailParts(priceSummary(content), w)
				if gotH != wantH || gotT != wantT || gotTrunc != wantTrunc {
					t.Errorf("headAndTailParts(%q, %d):\n  escape-then-cut = (%q, %q, %v)\n  cut-then-escape = (%q, %q, %v)",
						content, w, wantH, wantT, wantTrunc, gotH, gotT, gotTrunc)
					continue
				}
				if cellWidth(gotH) > w || cellWidth(gotT) > w {
					t.Errorf("headAndTailParts(%q, %d) over budget: head %d cells, tail %d cells",
						content, w, cellWidth(gotH), cellWidth(gotT))
				}
				compared++
			}
		}
	}
	if compared == 0 {
		t.Fatal("compared nothing — the corpus or the width range is empty")
	}
	t.Logf("%d (content, budget) pairs agree between the two orders", compared)
}

// TestTailPartsEscapeOrderIsEquivalent is the same proof for tailParts, over
// the entries whose clusters are single runes. The multi-rune ones are
// TestTailPartsNeverSplitsACluster's subject: there the two orders are NOT
// equivalent, because the old one walked runes and the new one walks clusters,
// and that difference is the fix.
func TestTailPartsEscapeOrderIsEquivalent(t *testing.T) {
	compared := 0
	for _, raw := range summaryCorpus() {
		if hasMultiRuneCluster(raw) {
			continue
		}
		for _, content := range []string{raw, prepareContent(raw)} {
			for w := 0; w <= 40; w++ {
				want, wantTrunc := oldTailParts(content, w)
				got, gotTrunc := tailParts(content, w)
				if got != want || gotTrunc != wantTrunc {
					t.Errorf("tailParts(%q, %d):\n  escape-then-cut = (%q, %v)\n  cut-then-escape = (%q, %v)",
						content, w, want, wantTrunc, got, gotTrunc)
					continue
				}
				if cellWidth(got) > w {
					t.Errorf("tailParts(%q, %d) = %q measures %d cells — over budget",
						content, w, got, cellWidth(got))
				}
				compared++
			}
		}
	}
	if compared == 0 {
		t.Fatal("compared nothing — the corpus is empty")
	}
	t.Logf("%d (content, budget) pairs agree between the two orders", compared)
}

// TestTailPartsNeverSplitsACluster is the bug the reorder fixed on the way.
// tailParts walked []rune backwards, so a multi-rune cluster could be cut in
// half: tailParts("aaaa 👨‍👩‍👧‍👦", 2) returned ZWJ+boy, the back half of a
// family emoji, on the row a user watches while a command runs. The old
// implementation fails this test.
//
// The invariant is stated over the ESCAPED content, not the raw one, and the
// difference matters: a `\n` marker is two clusters once escaped, so a budget
// of 2 cells may legitimately keep only its "n". Cutting a marker in half is
// what both orders have always done; cutting a grapheme cluster in half is what
// only the rune walk did.
func TestTailPartsNeverSplitsACluster(t *testing.T) {
	for _, raw := range summaryCorpus() {
		if !hasMultiRuneCluster(raw) {
			continue
		}
		for _, content := range []string{raw, prepareContent(raw)} {
			cs := clusters(escapeBreaks(content))
			for w := 1; w <= 20; w++ {
				got, _ := tailParts(content, w)
				if !utf8.ValidString(got) {
					t.Errorf("tailParts(%q, %d) = %q is not valid UTF-8", content, w, got)
					continue
				}
				if cellWidth(got) > w {
					t.Errorf("tailParts(%q, %d) = %q measures %d cells — over budget",
						content, w, got, cellWidth(got))
				}
				whole := false
				for k := 0; k <= len(cs); k++ {
					if concatClusters(cs[len(cs)-k:]) == got {
						whole = true
						break
					}
				}
				if !whole {
					t.Errorf("tailParts(%q, %d) = %q — not a whole-cluster suffix of the escaped content",
						content, w, got)
				}
			}
		}
	}
}

// hasMultiRuneCluster reports whether s holds a grapheme cluster made of more
// than one rune — the shape a rune-wise walk splits and a cluster-wise one
// cannot.
func hasMultiRuneCluster(s string) bool {
	runes := utf8.RuneCountInString(s)
	return len(clusters(s)) != runes
}
