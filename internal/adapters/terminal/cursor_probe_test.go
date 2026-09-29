package terminal

import (
	"math/rand"
	"testing"
)

// ensureCursorVisible used to ask one question per walk — and a walk encodes
// the line as a string before it can segment it, so a keystroke at the end of a
// 4000-cell line encoded and segmented that line five to eight times. It now
// asks them all of a single pass (probeLine).
//
// The pass is a rewrite of arithmetic that decides where the caret is drawn, so
// this file keeps the old function verbatim as an oracle and holds the new one
// to it over every state the field can be in: exact equality of the visible
// start, not merely "the invariants still hold". The fuzz test in
// fuzz_invariants_test.go checks the properties; this checks the answer.

// oldEnsureCursorVisible is ensureCursorVisible before the single pass. Kept
// verbatim — same walks, same order, same arithmetic.
func oldEnsureCursorVisible(m InputField) InputField {
	if m.width <= 0 {
		return m
	}
	lineStart, lineEnd := m.currentLine(m.pos)
	line := m.value[lineStart:lineEnd]

	if lineStart != m.visLine {
		m.visLine = lineStart
		m.visStart = 0
	}
	if m.visStart > len(line) {
		m.visStart = len(line)
	}
	m.visStart = oldClusterStartAt(line, m.visStart)
	if len(line) == 0 || runesWidth(line) <= m.width {
		m.visStart = 0
		return m
	}

	relPos := m.pos - lineStart
	cursorCell := runesWidth(line[:relPos])
	need := 1
	if relPos < len(line) {
		walkLineClusters(line, func(start, end, width int) bool {
			if relPos >= start && relPos < end {
				need = width
				return false
			}
			return start <= relPos
		})
	}
	startCell := runesWidth(line[:m.visStart])

	switch {
	case cursorCell < startCell:
		m.visStart = oldClusterStartAt(line, relPos)
	case cursorCell+need > startCell+m.width:
		m.visStart = oldFirstRuneStartAtLeast(line, cursorCell+need-m.width)
		if runesWidth(line[:m.visStart]) > cursorCell {
			m.visStart = oldClusterStartAt(line, relPos)
		}
	}
	return m
}

// oldClusterStartAt is clusterStartAt, which the single pass made unnecessary
// in production: the pass is at the cluster the visible start fell inside
// anyway, so it reports that cluster's start on the way by.
func oldClusterStartAt(line []rune, pos int) int {
	if pos <= 0 || pos >= len(line) {
		return pos
	}
	start := pos
	walkLineClusters(line, func(a, b, _ int) bool {
		if pos >= a && pos < b {
			start = a
			return false
		}
		return a <= pos
	})
	return start
}

// oldFirstRuneStartAtLeast is firstRuneStartAtLeast before it also returned the
// cells before the index it found.
func oldFirstRuneStartAtLeast(line []rune, target int) int {
	if target <= 0 {
		return 0
	}
	idx := len(line)
	cells := 0
	walkLineClusters(line, func(start, _, width int) bool {
		if cells >= target {
			idx = start
			return false
		}
		cells += width
		return true
	})
	return idx
}

// cursorValues are the lines the comparison runs over: ASCII and CJK (one and
// two cells), combining marks and variation selectors and ZWJ sequences (one
// cluster, several runes), a zero-width-ish mark cluster, and line breaks so the
// multi-line field has more than one line to be on.
var cursorValues = []string{
	"",
	"a",
	"abcdefghij",
	"你你你你你你你你",
	"a你b好c世d界e",
	"e\u0301e\u0301e\u0301",
	"❤️❤️x",
	"👨‍👩‍👧‍👦ab",
	"किकिa",
	"\u1100\u1161\u1100\u1161",
	"1️⃣2️⃣3️⃣",
	"aaa\nbbb\nccc",
	"你\n好\n世\n界",
	"\u0600\u0600ab",
	"ab\n你e\u0301好\n👨‍👩‍👧‍👦cd",
	"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	"aaaaaaaaaa你你你你你你你你你你aaaaaaaaaa",
}

// TestEnsureCursorVisibleMatchesOracle sweeps every (value, width, pos,
// visLine, visStart) combination the field can be handed, including stale
// visible starts that fall inside a cluster — the case the re-anchor exists for
// — and requires the single pass to land on exactly the index the old walks
// landed on.
func TestEnsureCursorVisibleMatchesOracle(t *testing.T) {
	widths := []int{1, 2, 3, 4, 5, 8, 13, 40}
	checked := 0
	for _, v := range cursorValues {
		runes := []rune(v)
		// Visible starts to try: the ends, the thirds, and every rune index of
		// short values, so mid-cluster indices are covered where they exist.
		starts := []int{0, len(runes)}
		for _, f := range []float64{0.25, 0.5, 0.75} {
			starts = append(starts, int(f*float64(len(runes))))
		}
		if len(runes) <= 12 {
			for i := range runes {
				starts = append(starts, i)
			}
		}
		for _, width := range widths {
			for pos := 0; pos <= len(runes); pos++ {
				for _, visStart := range starts {
					for _, visLine := range []int{0, -1, len(runes) + 3} {
						for _, multiline := range []bool{false, true} {
							g := newProbeField(v, width, pos, visLine, visStart, multiline)
							got := g.ensureCursorVisible()
							want := oldEnsureCursorVisible(g)
							checked++
							if got.visStart != want.visStart || got.visLine != want.visLine {
								t.Fatalf("value=%q width=%d pos=%d visLine=%d visStart=%d multiline=%v:\n  single pass: visLine=%d visStart=%d\n  oracle:      visLine=%d visStart=%d",
									v, width, pos, visLine, visStart, multiline,
									got.visLine, got.visStart, want.visLine, want.visStart)
							}
						}
					}
				}
			}
		}
	}
	if checked < 10000 {
		t.Fatalf("the sweep only covered %d states; it is not exercising what it claims", checked)
	}
	t.Logf("compared %d states", checked)
}

// TestEnsureCursorVisibleMatchesOracleRandom is the same comparison over states
// the sweep does not reach: longer values, wider fields, and sequences of
// operations that leave the visible start wherever the previous operation put
// it.
func TestEnsureCursorVisibleMatchesOracleRandom(t *testing.T) {
	chars := []rune("ab你cd好e世fg界h\u0301❤️\u200d")
	rng := rand.New(rand.NewSource(20260929))
	for iter := 0; iter < 4000; iter++ {
		multiline := iter%2 == 0
		newField := NewInputField
		if multiline {
			newField = NewMultilineInputField
		}
		g := newField().WithWidth(1 + rng.Intn(20))
		for step := 0; step < 1+rng.Intn(25); step++ {
			switch rng.Intn(6) {
			case 0, 1, 2:
				r := chars[rng.Intn(len(chars))]
				g, _ = g.Update(KeyPressMsg{Text: string(r), Code: r})
			case 3:
				k := []string{"left", "right", "home", "end", "backspace", "delete"}[rng.Intn(6)]
				g, _ = g.handleKeyMsg(KeyPressMsg{Text: k, Code: 0})
			case 4:
				s := string(chars[rng.Intn(len(chars))]) + "\n" + string(chars[rng.Intn(len(chars))])
				g, _ = g.Update(PasteMsg{Content: s})
			case 5:
				g = g.WithWidth(1 + rng.Intn(20))
			}
			// Now force an arbitrary — possibly stale, possibly mid-cluster —
			// visible start, which is the state the re-anchor exists for.
			lineStart, lineEnd := g.currentLine(g.pos)
			line := g.value[lineStart:lineEnd]
			probe := g
			if len(line) > 0 {
				probe.visStart = rng.Intn(len(line) + 1)
			}
			got := probe.ensureCursorVisible()
			want := oldEnsureCursorVisible(probe)
			if got.visStart != want.visStart || got.visLine != want.visLine {
				t.Fatalf("iter=%d step=%d value=%q pos=%d width=%d visLine=%d visStart=%d multiline=%v:\n  single pass: visLine=%d visStart=%d\n  oracle:      visLine=%d visStart=%d",
					iter, step, string(g.value), g.pos, g.width, probe.visLine, probe.visStart, multiline,
					got.visLine, got.visStart, want.visLine, want.visStart)
			}
		}
	}
}

// newProbeField builds a field in an exact state, bypassing the constructors'
// own normalization: the comparison has to reach states no operation produces.
func newProbeField(value string, width, pos, visLine, visStart int, multiline bool) InputField {
	newField := NewInputField
	if multiline {
		newField = NewMultilineInputField
	}
	g := newField()
	g.width = width
	g.value = []rune(value)
	if pos > len(g.value) {
		pos = len(g.value)
	}
	g.pos = pos
	g.visLine = visLine
	if visStart < 0 {
		visStart = 0
	}
	g.visStart = visStart
	return g
}

// TestProbeLineAnswersThePrefixQuestions pins probeLine on its own, against the
// walks it replaced, so a disagreement names the quantity that moved instead of
// only the visible start.
func TestProbeLineAnswersThePrefixQuestions(t *testing.T) {
	for _, v := range cursorValues {
		line := []rune(v)
		for width := 1; width <= 12; width++ {
			for cursor := 0; cursor <= len(line); cursor++ {
				for vis := 0; vis <= len(line); vis++ {
					p := probeLine(line, cursor, vis, width)

					if want := oldClusterStartAt(line, vis); p.anchored != want {
						t.Errorf("probeLine(%q, cursor=%d, vis=%d, width=%d).anchored = %d, clusterStartAt = %d",
							v, cursor, vis, width, p.anchored, want)
					}
					// startCell is measured at the ANCHORED index, which is
					// where the field will actually start drawing.
					if want := runesWidth(line[:p.anchored]); p.startCell != want {
						t.Errorf("probeLine(%q, cursor=%d, vis=%d, width=%d).startCell = %d, runesWidth(line[:anchored=%d]) = %d",
							v, cursor, vis, width, p.startCell, p.anchored, want)
					}
					if want := runesWidth(line[:min(cursor, len(line))]); p.cursorCell != want {
						t.Errorf("probeLine(%q, cursor=%d, vis=%d, width=%d).cursorCell = %d, runesWidth = %d",
							v, cursor, vis, width, p.cursorCell, want)
					}
					// need: the width of the cluster the cursor is inside, or 1
					// at end-of-line where there is no cluster.
					wantNeed := 1
					if cursor < len(line) {
						walkLineClusters(line, func(a, b, w int) bool {
							if cursor >= a && cursor < b {
								wantNeed = w
								return false
							}
							return a <= cursor
						})
					}
					if p.need != wantNeed {
						t.Errorf("probeLine(%q, cursor=%d, vis=%d, width=%d).need = %d, the cluster at the cursor is %d",
							v, cursor, vis, width, p.need, wantNeed)
					}
					if want := oldClusterStartAt(line, cursor); p.clusterAt != want {
						t.Errorf("probeLine(%q, cursor=%d, vis=%d, width=%d).clusterAt = %d, clusterStartAt = %d",
							v, cursor, vis, width, p.clusterAt, want)
					}
					if want := runesWidth(line) <= width; p.fits != want {
						t.Errorf("probeLine(%q, cursor=%d, vis=%d, width=%d).fits = %v, runesWidth(line)=%d <= %d is %v",
							v, cursor, vis, width, p.fits, runesWidth(line), width, want)
					}
				}
			}
		}
	}
}
