package terminal

// Terminal-grid simulation for the overlay-renderer residue tests: paints
// rendered frames onto a rune grid and asserts no old content survives.

import (
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/tlv"
)

// applyFrame paints a rendered frame onto a terminal grid ([][]rune rows).
// Handles the sequences the renderer emits: '\r' column 0, '\n' next row,
// ESC[K (EL) clears the current row tail, ESC[2J clears the screen,
// ESC[H homes. UTF-8 runes are 1 column each.
func applyFrame(grid [][]rune, frame string, width int) [][]rune {
	row := 0
	col := 0
	clearScreen := func() {
		for i := range grid {
			grid[i] = make([]rune, width)
			for j := range grid[i] {
				grid[i][j] = ' '
			}
		}
	}
	ensure := func() {
		for len(grid) <= row {
			grid = append(grid, make([]rune, width))
			for j := range grid[len(grid)-1] {
				grid[len(grid)-1][j] = ' '
			}
		}
	}
	blank := func() []rune {
		r := make([]rune, width)
		for j := range r {
			r[j] = ' '
		}
		return r
	}
	runes := []rune(frame)
	i := 0
	for i < len(runes) {
		b := runes[i]
		switch {
		case b == '\r':
			col = 0
			i++
		case b == '\n':
			row++
			col = 0
			i++
		case b == 0x1b && i+1 < len(runes) && runes[i+1] == '[':
			j := i + 2
			for j < len(runes) && (runes[j] < 0x40 || runes[j] > 0x7e) {
				j++
			}
			final := rune(0)
			if j < len(runes) {
				final = runes[j]
			}
			params := string(runes[i+2 : j])
			switch final {
			case 'J':
				if params == "2" {
					clearScreen()
				} else {
					ensure()
					for c := col; c < width; c++ {
						grid[row][c] = ' '
					}
					for r := row + 1; r < len(grid); r++ {
						grid[r] = blank()
					}
				}
			case 'K':
				ensure()
				for c := col; c < width; c++ {
					grid[row][c] = ' '
				}
			case 'H':
				if strings.Contains(params, ";") {
					var r, c int
					_, _ = fmtSscan(params, &r, &c)
					if r > 0 && c > 0 {
						row, col = r-1, c-1
					}
				} else {
					row, col = 0, 0
				}
			}
			i = j + 1
		default:
			// Printable rune (1 column). A pending wrap (col == width)
			// fires only when a character is written — '\r' first
			// returns to column 0, so a full-width row followed by
			// '\r\n' (the renderer's newline) stays one row.
			if col >= width {
				col = 0
				row++
			}
			ensure()
			grid[row][col] = b
			col++
			i++
		}
	}
	return grid
}

// fmtSscan parses "row;col" into two ints (test helper).
func fmtSscan(s string, a, b *int) (int, error) {
	parts := strings.Split(s, ";")
	if len(parts) != 2 {
		return 0, errFmt
	}
	for _, p := range parts {
		v := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				return 0, errFmt
			}
			v = v*10 + int(c-'0')
		}
		if a != nil {
			*a = v
			a = nil
		} else if b != nil {
			*b = v
			b = nil
		}
	}
	return 2, nil
}

type fmtErr struct{}

func (fmtErr) Error() string { return "fmt" }

var errFmt = fmtErr{}

// TestNoResidueAcrossFrames simulates the scroll complaint: frame 1 shows
// a window with long rows; frame 2 (scrolled / different windows) shows
// short rows on the same screen positions. No old characters may survive.
func TestNoResidueAcrossFrames(t *testing.T) {
	const width = 40
	wb := NewWindowBuffer(width, DefaultStyles())
	wb.AppendOrUpdate(tlv.TagAssistantT, "a1", strings.Repeat("word ", 25)) // long rows
	wb.SetViewportPosition(0, 6)
	frame1 := wb.GetAll(-1, false)

	// Frame 2: different windows, short rows + blank padding.
	wb.Clear()
	wb.AppendOrUpdate(tlv.TagAssistantR, "r1", "short")
	wb.AppendOrUpdate(tlv.TagUserT, "u1", "hi")
	wb.SetViewportPosition(0, 6)
	frame2 := wb.GetAll(-1, false)

	// Apply frame1 then frame2 to the grid; no 'word' may survive.
	grid := applyFrame(nil, frame1, width)
	grid = applyFrame(grid, frame2, width)
	for r, row := range grid {
		if strings.Contains(string(row), "word") {
			t.Errorf("row %d trails old content: %q", r, string(row))
		}
	}
}
