package terminal

import (
	"testing"

	ansi "github.com/charmbracelet/x/ansi"

	"github.com/alayacore/alayacore/internal/theme"
)

// Fold-arrow values for header assertions.
//
// The glyphs live in the theme, so the tests read them from the default
// theme rather than spelling out a literal: retuning the default arrow
// (internal/theme/arrows.go) no longer means editing every header
// assertion in this package. The glyph shape itself is pinned once, by
// TestDefaultArrowGlyphs in the theme package.
//
// Same source as DefaultStyles() — NewStyles(theme.DefaultTheme()) — so
// what these names hold is exactly what the windows render.
var (
	foldArrow   = theme.DefaultTheme().FoldArrow
	unfoldArrow = theme.DefaultTheme().UnfoldArrow
)

// A theme arrow that cannot fit the single cell the header layout
// reserves must never reach Styles: it would shift the label column and
// the content column of every window in the buffer.
func TestNewStylesReplacesUnusableArrows(t *testing.T) {
	cases := []struct {
		name string
		fold string
		want string
	}{
		{"empty falls back", "", theme.DefaultFoldArrow},
		{"multi-cell emoji", "▶️", theme.DefaultFoldArrow}, // ▶ + VS16: emoji presentation, 2 cells
		{"ascii bracket pair", "[+]", theme.DefaultFoldArrow},
		{"two glyphs", "▸▾", theme.DefaultFoldArrow},
		{"trigram (two cells here)", "☰", theme.DefaultFoldArrow},
		{"usable ascii kept", ">", ">"},
		{"text-presentation triangle kept", "▶︎", "▶︎"}, // ▶ + VS15: still one cell
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			th := theme.DefaultTheme()
			th.FoldArrow = tc.fold
			st := NewStyles(th)
			if st.FoldArrow != tc.want {
				t.Errorf("FoldArrow = %q, want %q", st.FoldArrow, tc.want)
			}
			if w := ansi.StringWidth(st.FoldArrow); w != 1 {
				t.Errorf("FoldArrow %q is %d cells wide, layout reserves 1", st.FoldArrow, w)
			}
		})
	}
}

// Dimmed() must carry the glyphs through unchanged — it recolors, and an
// arrow lost under an overlay would change the content column.
func TestDimmedKeepsArrows(t *testing.T) {
	st := DefaultStyles().Dimmed()
	if st.FoldArrow != foldArrow || st.UnfoldArrow != unfoldArrow {
		t.Errorf("dimmed arrows = %q/%q, want %q/%q", st.FoldArrow, st.UnfoldArrow, foldArrow, unfoldArrow)
	}
}
