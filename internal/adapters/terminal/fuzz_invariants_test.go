package terminal

import (
	"fmt"
	"math/rand"
	"testing"

	tea "charm.land/bubbletea/v2"
	rw "github.com/mattn/go-runewidth"
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
	if cell > visCells {
		check("CursorCell=%d beyond visible width=%d", cell, visCells)
	}

	val := g.value
	if g.pos < len(val) {
		if val[g.pos] != '\n' { // mid-line rune (line end points at \n or EOF)
			w := rw.RuneWidth(val[g.pos])
			if w <= g.width && cell+w > visCells {
				check("rune %q at cursor clipped: CursorCell=%d + w=%d > visible width=%d", string(val[g.pos]), cell, w, visCells)
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
	lineStart, lineEnd = g.currentLine(g.pos)
	line := g.value[lineStart:lineEnd]
	start := min(g.visStart, len(line))
	got := string(vis)
	want := string(line[start : start+len(vis)])
	if got != want {
		check("visible text drift: got %q, want %q (slice from visStart=%d)", got, want, start)
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
					g, _ = g.Update(tea.KeyPressMsg{Text: string(r), Code: r})
				case 3: // backspace / delete
					k := keys[6+rng.Intn(2)]
					desc = k
					g, _ = g.handleKeyMsg(tea.KeyPressMsg{Text: k, Code: 0})
				case 4, 5: // movement
					k := keys[rng.Intn(6)]
					desc = k
					g, _ = g.handleKeyMsg(tea.KeyPressMsg{Text: k, Code: 0})
				case 6: // paste with newlines
					content := string(chars[rng.Intn(len(chars))]) + "\n" + string(chars[rng.Intn(len(chars))])
					if rng.Intn(4) == 0 {
						content = "\n" + content // leading newline edge case
					}
					desc = fmt.Sprintf("paste %q", content)
					g, _ = g.Update(tea.PasteMsg{Content: content})
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
