package terminal

import (
	"strings"
	"testing"

	ansi "github.com/charmbracelet/x/ansi"

	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/tlv"
)

// The fold arrows are pinned by codepoint on purpose. They are the
// product's answer to "how does a folded window look", and the choice was
// made on what terminals can be relied on to draw in one cell — not on
// taste. Change them deliberately, and keep the two rules below true.
func TestFoldArrowGlyphs(t *testing.T) {
	cases := []struct {
		name  string
		glyph string
		want  rune
	}{
		{"foldArrow", foldArrow, '\u25B8'},     // ▸ small right triangle
		{"unfoldArrow", unfoldArrow, '\u25BE'}, // ▾ small down triangle
	}
	for _, tc := range cases {
		if r := []rune(tc.glyph); len(r) != 1 || r[0] != tc.want {
			t.Errorf("%s = %q, want the single rune %q", tc.name, tc.glyph, string(tc.want))
		}
		// The header layout reserves arrowCellWidth cells for the glyph —
		// collapsedPrefixWidth, the arithmetic in the collapsed builders,
		// and the content column the soft-wrap tests pin are all only
		// correct while this holds.
		if w := ansi.StringWidth(tc.glyph); w != arrowCellWidth {
			t.Errorf("%s measures %d cells, layout reserves %d", tc.name, w, arrowCellWidth)
		}
	}
	if collapsedPrefixWidth != arrowCellWidth+1 {
		t.Errorf("collapsedPrefixWidth = %d, want the arrow (%d) + one separating space",
			collapsedPrefixWidth, arrowCellWidth)
	}
}

// The heavier triangle and arrow pairs are exactly the ones the constants
// avoid, on two properties: East Asian Width "A" (a terminal configured for
// double-width ambiguous characters draws them two cells) and
// Emoji_Presentation=Yes (emoji display is their default per UTS#51, so a
// terminal that resolves them through an emoji font draws a two-cell color
// glyph). Both classes measure one cell to our own width model, so nothing
// at runtime can catch the damage — only the choice of glyph can. (The
// width property was checked against Unicode 15 data; the terminal
// consequence follows from the properties and has not been measured on a
// host here.) A default drifting back onto one of these is a regression,
// not a cosmetic change.
func TestArrowsAvoidUnreliableCodepoints(t *testing.T) {
	unreliable := map[rune]string{
		'\u25B6': "▶ ambiguous width AND emoji presentation",
		'\u25BC': "▼ ambiguous width AND emoji presentation",
		'\u25B2': "▲ ambiguous width AND emoji presentation",
		'\u25C0': "◀ ambiguous width AND emoji presentation",
		'\u23F5': "⏵ emoji presentation (media play key)",
		'\u23F7': "⏷ emoji presentation (media down key)",
		'\u25CF': "● ambiguous width",
		'\u25CB': "○ ambiguous width",
		'\u2192': "→ ambiguous width",
		'\u2193': "↓ ambiguous width",
		'\u2630': "☰ already two cells in our own width model",
	}
	for _, g := range []string{foldArrow, unfoldArrow} {
		for _, r := range g {
			if why, bad := unreliable[r]; bad {
				t.Errorf("fold arrow uses %q: %s", string(r), why)
			}
		}
	}
}

// The arrow is geometry, not color: it must not depend on the theme, on
// whether the palette is dimmed under an overlay, or on styles being
// attached at all. (This is what the fold_arrow/unfold_arrow theme keys
// used to violate — they let a palette switch change a structural
// affordance, and let a user value break the header columns.)
func TestArrowsIndependentOfTheme(t *testing.T) {
	other := &theme.Theme{
		Primary: "#1e66f5", Dim: "#ccd0da", Muted: "#9ca0b0", Warning: "#df8e1d",
		Error: "#d20f39", Selection: "#fe640b", Added: "#40a02b", Removed: "#d20f39",
		Tool: "#df8e1d",
	}
	styles := []*Styles{nil, DefaultStyles(), DefaultStyles().Dimmed(), NewStyles(other)}
	for _, folded := range []bool{true, false} {
		want := unfoldArrow
		if folded {
			want = foldArrow
		}
		for _, st := range styles {
			w := &Window{Folded: folded, styles: st}
			if got := w.arrowChar(); got != want {
				t.Errorf("arrowChar(folded=%v) = %q, want %q", folded, got, want)
			}
		}
	}
}

// End to end: both states render, and the collapsed content column is the
// prefix plus the label column — the number a two-cell arrow would move.
func TestFoldArrowRenderingKeepsHeaderColumns(t *testing.T) {
	wb := NewWindowBuffer(60, DefaultStyles())
	wb.AppendOrUpdate(tlv.TagAssistantT, "a1", "hello there")

	expanded := stripANSI(wb.GetAll(-1, false))
	if !strings.HasPrefix(expanded, unfoldArrow) {
		t.Errorf("expanded header = %q, want it to start with %q", firstRow(expanded), unfoldArrow)
	}

	wb.ToggleFold(0)
	collapsed := stripANSI(wb.GetAll(-1, false))
	if !strings.HasPrefix(collapsed, foldArrow) {
		t.Fatalf("collapsed line = %q, want it to start with %q", firstRow(collapsed), foldArrow)
	}
	if c := contentColumn(collapsed); c != collapsedPrefixWidth+CollapsedLabelWidth {
		t.Errorf("collapsed content column = %d, want %d: %q",
			c, collapsedPrefixWidth+CollapsedLabelWidth, collapsed)
	}
}

// firstRow returns the first line of a rendering, for readable failures.
func firstRow(out string) string {
	line, _, _ := strings.Cut(out, "\n")
	return line
}
