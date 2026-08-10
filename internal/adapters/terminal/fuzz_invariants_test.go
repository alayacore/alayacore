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
//     current line starting at visibleStartCell (no drift between
//     buildVisibleText and CursorCell).
func assertInputFieldInvariants(t *testing.T, ctx string, g InputField) {
	t.Helper()
	check := func(format string, args ...any) {
		t.Fatalf("%s\n  state: value=%q pos=%d width=%d offset=%d startCell=%d CursorCell=%d visible=%q visCells=%d\n  "+format,
			append([]any{ctx, string(g.value), g.pos, g.width, g.offset, g.visibleStartCell(), g.CursorCell(), string(g.buildVisibleText()), runesWidth(g.buildVisibleText())}, args...)...)
	}
	if g.offset < 0 {
		check("negative offset=%d", g.offset)
	}
	if g.pos < 0 || g.pos > len(g.value) {
		check("pos=%d out of range [0,%d]", g.pos, len(g.value))
	}
	// The scroll offset must always be a rune boundary of the current line
	// (or past its end): a mid-rune offset would split a wide character at
	// the left edge of the viewport.
	if g.visibleStartCell() != g.offset {
		check("offset=%d is not a rune boundary (visibleStartCell=%d)", g.offset, g.visibleStartCell())
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

	// Invariant 5: visible text must be the exact slice starting at
	// visibleStartCell.
	lineStart, lineEnd := g.currentLine(g.pos)
	line := g.value[lineStart:lineEnd]
	startCell := g.visibleStartCell()
	if len(line) == 0 || startCell >= runesWidth(line) {
		if len(vis) != 0 {
			check("startCell=%d at/past end of line %q but visible=%q", startCell, string(line), string(vis))
		}
		return
	}
	idx := -1
	for cells, i := 0, 0; i < len(line); i++ {
		w := rw.RuneWidth(line[i])
		if cells == startCell {
			idx = i
			break
		}
		if cells+w > startCell {
			idx = i
			break
		}
		cells += w
	}
	if idx < 0 {
		check("could not locate startCell=%d in line %q", startCell, string(line))
	}
	got := string(vis)
	want := string(line[idx : idx+len(vis)])
	if got != want {
		check("visible text drift: got %q, want %q (slice from startCell=%d)", got, want, startCell)
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
