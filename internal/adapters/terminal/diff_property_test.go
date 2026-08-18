package terminal

// Property test: the row diff applied to a screen must produce exactly
// the same grid as a full repaint of the new frame. Generates random
// frame pairs (base rows + CUP-anchored rows that appear, disappear,
// move, or change) and asserts the diffed grid matches the reference
// full-render grid — any divergence is a residue/misalignment bug.

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"
)

// buildProbeFrame builds a frame from base rows plus a set of CUP rows:
// each CUP row is (row, col, text).
func buildProbeFrame(base []string, cups [][3]interface{}) string {
	var sb bytes.Buffer
	for i, l := range base {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(l)
	}
	for _, c := range cups {
		row := c[0].(int)
		col := c[1].(int)
		text := c[2].(string)
		sb.WriteString(fmt.Sprintf("\n\x1b[%d;%dH%s", row+1, col+1, text))
	}
	return sb.String()
}

func TestDiffMatchesFullRepaint(t *testing.T) {
	const W = 40
	rng := rand.New(rand.NewSource(20260818))
	baseTemplates := []string{
		"base0", "base1", "base2", "base3", "base4", "base5",
		// A soft-wrapped base row: 90 cells → 3 terminal rows at W=40.
		"012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789",
	}
	baseVariants := []string{"", "V0", "variant-one", "variant-two-long"}

	texts := []string{"OVERLAY", "short", "a-bit-longer-overlay", "x", ""}
	cols := []int{0, 5, 10, 20}
	rows := []int{1, 2, 3, 4}

	for iter := 0; iter < 500; iter++ {
		// Random base content (rows may change text between frames).
		mkBase := func() []string {
			b := make([]string, len(baseTemplates))
			for i := range b {
				b[i] = baseTemplates[i] + baseVariants[rng.Intn(len(baseVariants))]
			}
			return b
		}
		base1 := mkBase()
		base2 := mkBase()

		// Random overlay set for frame 1.
		var cups1 [][3]interface{}
		n1 := rng.Intn(5)
		used1 := map[[2]int]bool{}
		for k := 0; k < n1; k++ {
			r := rows[rng.Intn(len(rows))]
			c := cols[rng.Intn(len(cols))]
			if used1[[2]int{r, c}] {
				continue
			}
			used1[[2]int{r, c}] = true
			cups1 = append(cups1, [3]interface{}{r, c, texts[rng.Intn(len(texts))]})
		}

		// Frame 2 overlay set: keep some, drop some, add some, move some.
		var cups2 [][3]interface{}
		n2 := rng.Intn(5)
		for k := 0; k < n2; k++ {
			r := rows[rng.Intn(len(rows))]
			c := cols[rng.Intn(len(cols))]
			cups2 = append(cups2, [3]interface{}{r, c, texts[rng.Intn(len(texts))]})
		}

		frame1 := buildProbeFrame(base1, cups1)
		frame2 := buildProbeFrame(base2, cups2)

		s := &Screen{out: &bytes.Buffer{}}
		s.Resize(W, 8)

		if err := s.Render(frame1, nil, true); err != nil {
			t.Fatal(err)
		}
		grid := applyFrame(nil, s.out.(*bytes.Buffer).String(), W)
		s.out.(*bytes.Buffer).Reset()

		if err := s.Render(frame2, nil, true); err != nil {
			t.Fatal(err)
		}
		gridDiff := applyFrame(grid, s.out.(*bytes.Buffer).String(), W)
		gridFull := applyFrame(nil, frame2, W)

		for r := 0; r < 8; r++ {
			a := lineAt(gridDiff, r)
			b := lineAt(gridFull, r)
			if a != b {
				t.Fatalf("iter %d: diff/full mismatch at row %d:\n  diff: %q\n  full: %q\nframe1: %q\nframe2: %q",
					iter, r, a, b, frame1, frame2)
			}
		}
	}
}
