package terminal

import (
	"fmt"
	"math/rand"
	"testing"
)

// assertInputFieldInvariants checks the rendering/cursor invariants after
// every operation in the fuzz test:
//
//  1. The visible text never exceeds the viewport width.
//  2. The cursor never sits beyond the rendered visible text.
//  3. A rune at the cursor is never clipped by the right edge (when it could
//     fit in the viewport at all).
//  4. At end of line, the cursor sits exactly after the last visible rune.
//  5. The rendered visible text is exactly the contiguous slice of the
//     current line starting at the visible start (no drift between
//     buildVisibleText and CursorCell).
//  6. visStart is a rune index within the current line and belongs to that
//     line (visLine): the visible start is inherently a rune boundary, so
//     a wide character can never be split at the left edge.
func assertInputFieldInvariants(t *testing.T, ctx string, g InputField) {
	t.Helper()
	check := func(format string, args ...any) {
		t.Fatalf("%s\n  state: value=%q pos=%d width=%d visLine=%d visStart=%d CursorCell=%d visible=%q visCells=%d\n  "+format,
			append([]any{ctx, string(g.value), g.pos, g.width, g.visLine, g.visStart, g.CursorCell(), string(g.buildVisibleText()), runesWidth(g.buildVisibleText())}, args...)...)
	}
	if g.pos < 0 || g.pos > len(g.value) {
		check("pos=%d out of range [0,%d]", g.pos, len(g.value))
	}
	lineStart, lineEnd := g.currentLine(g.pos)
	lineLen := lineEnd - lineStart
	if g.visLine != lineStart {
		check("visLine=%d != lineStart=%d (stale visible start)", g.visLine, lineStart)
	}
	if g.visStart < 0 || g.visStart > lineLen {
		check("visStart=%d out of range [0,%d] for line %q", g.visStart, lineLen, string(g.value[lineStart:lineEnd]))
	}

	vis := g.buildVisibleText()
	visCells := runesWidth(vis)
	if visCells > g.width {
		check("visible width=%d exceeds viewport width=%d", visCells, g.width)
	}

	cell := g.CursorCell()

	val := g.value
	// Visible-start cell of the current line (shared by the cluster checks).
	lineStart, lineEnd = g.currentLine(g.pos)
	line := g.value[lineStart:lineEnd]
	startCell := runesWidth(line[:min(g.visStart, len(line))])
	// Width of the grapheme cluster at the cursor; 0 means the cursor is at
	// end of line. When it exceeds the viewport width the cluster cannot
	// physically fit, so cursor/cluster visibility are best-effort.
	cursorClusterW := 0
	if g.pos < len(val) && val[g.pos] != '\n' {
		for _, c := range graphemeClusters(line) {
			if g.pos-lineStart >= c.start && g.pos-lineStart < c.end {
				cursorClusterW = c.width
				break
			}
		}
	}
	physicallyUnfit := cursorClusterW > g.width

	if cell > visCells && !physicallyUnfit {
		check("CursorCell=%d beyond visible width=%d", cell, visCells)
	}

	if g.pos < len(val) {
		if val[g.pos] != '\n' { // mid-line rune (line end points at \n or EOF)
			// The grapheme cluster at the cursor must be fully visible (when
			// it can fit in the viewport), even when the cursor sits inside
			// it (e.g. between ❤ and its variation selector). Measure from
			// the cluster start, not from the cursor itself.
			if cursorClusterW > 0 {
				cs := 0
				for _, c := range graphemeClusters(line) {
					if g.pos-lineStart >= c.start && g.pos-lineStart < c.end {
						cs = runesWidth(line[:c.start]) - startCell
						break
					}
				}
				if !physicallyUnfit && cs+cursorClusterW > visCells {
					check("cluster %q at cursor clipped: clusterStart=%d + w=%d > visible width=%d", string(val[g.pos]), cs, cursorClusterW, visCells)
				}
			}
		} else if cell != visCells {
			// Cursor sits at end of line (before the newline): it must hug
			// the last visible rune exactly.
			check("end of line (before \\n): CursorCell=%d != visible width=%d", cell, visCells)
		}
	} else if cell != visCells {
		check("end of line: CursorCell=%d != visible width=%d", cell, visCells)
	}

	// Invariant 5: visible text must be the exact slice starting at the
	// visible start (visStart is a rune index, so it is directly usable).
	start := min(g.visStart, len(line))
	got := string(vis)
	want := string(line[start : start+len(vis)])
	if got != want {
		check("visible text drift: got %q, want %q (slice from visStart=%d)", got, want, start)
	}

	// Invariant 7: truncation never splits a grapheme cluster — the right
	// edge of the visible text must be a cluster boundary (or the line end).
	if end := start + len(vis); end < len(line) {
		onBoundary := false
		for _, c := range graphemeClusters(line) {
			if c.start == end {
				onBoundary = true
				break
			}
		}
		if !onBoundary {
			check("visible text right edge %d splits a cluster (line %q, visible %q)", end, string(line), string(vis))
		}
	}
}

// TestInputFieldFuzzInvariants drives the input field through thousands of
// random operations (inserts of mixed ASCII/CJK, deletions, movement, pastes,
// cursor jumps, value resets) across random viewport widths and asserts the
// rendering/cursor invariants after every single step.
func TestInputFieldFuzzInvariants(t *testing.T) {
	chars := []rune("ab你cd好e世fg界h")
	keys := []string{"left", "right", "up", "down", "home", "end", "backspace", "delete"}

	for _, seed := range []int64{1, 7, 42, 99, 1234, 2024, 31337, 55555, 77777, 99999} {
		rng := rand.New(rand.NewSource(seed))
		for iter := 0; iter < 8000; iter++ {
			g := NewInputField()
			g = g.WithWidth(1 + rng.Intn(15)) // width 1..15

			steps := 1 + rng.Intn(30)
			var ops []string
			for s := 0; s < steps; s++ {
				op := rng.Intn(10)
				desc := ""
				switch op {
				case 0, 1, 2: // insert printable rune
					r := chars[rng.Intn(len(chars))]
					desc = fmt.Sprintf("insert %q", string(r))
					g, _ = g.Update(KeyPressMsg{Text: string(r), Code: r})
				case 3: // backspace / delete
					k := keys[6+rng.Intn(2)]
					desc = k
					g, _ = g.handleKeyMsg(KeyPressMsg{Text: k, Code: 0})
				case 4, 5: // movement
					k := keys[rng.Intn(6)]
					desc = k
					g, _ = g.handleKeyMsg(KeyPressMsg{Text: k, Code: 0})
				case 6: // paste with newlines (and multi-rune grapheme clusters)
					// Multi-rune clusters exercise the grapheme-aware width
					// model: ❤️ (VS16), family emoji (ZWJ), ✔️ (VS16),
					// e + combining acute, skin tone, Devanagari Mc mark,
					// Hangul Jamo, keycap.
					clusters := []string{"❤️", "👨‍👩‍👧‍👦", "✔️", "e\u0301", "👧\U0001F3FB", "कि", "\u1100\u1161", "1\uFE0F\u20E3", "\u05D1\u0591", "\u0600", "a", "你"}
					content := clusters[rng.Intn(len(clusters))] + "\n" + clusters[rng.Intn(len(clusters))]
					if rng.Intn(4) == 0 {
						content = "\n" + content // leading newline edge case
					}
					desc = fmt.Sprintf("paste %q", content)
					g, _ = g.Update(PasteMsg{Content: content})
				case 7: // cursor jump
					desc = "setpos"
					g = g.WithCursorPos(rng.Intn(len(g.value) + 1))
				case 8: // reset value
					n := rng.Intn(10)
					s := ""
					for i := 0; i < n; i++ {
						s += string(chars[rng.Intn(len(chars))])
					}
					if rng.Intn(4) == 0 {
						s += "\n" + s // newline boundaries
					}
					desc = fmt.Sprintf("reset %q", s)
					g = g.WithValue(s).CursorEnd()
					// Reference-implementation check: after replacing the
					// value, the visible start must equal what a fresh
					// InputField with the same width and value computes.
					// (Without this, a stale visStart could survive when the
					// new value coincidentally shares the old lineStart —
					// all internal invariants would still pass.)
					fresh := NewInputField().WithWidth(g.width).WithValue(s).CursorEnd()
					if fresh.visStart != g.visStart || fresh.visLine != g.visLine {
						t.Fatalf("seed=%d iter=%d: reset leaked visible start: g.visLine=%d visStart=%d, fresh visLine=%d visStart=%d (value=%q width=%d)",
							seed, iter, g.visLine, g.visStart, fresh.visLine, fresh.visStart, s, g.width)
					}
				case 9: // resize
					desc = "resize"
					g = g.WithWidth(1 + rng.Intn(15))
				}
				ops = append(ops, desc)
				ctx := fmt.Sprintf("seed=%d iter=%d step=%d ops=%v", seed, iter, s, ops)
				assertInputFieldInvariants(t, ctx, g)
			}
		}
	}
}
