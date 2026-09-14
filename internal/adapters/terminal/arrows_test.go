package terminal

import (
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/tlv"
)

// The fold markers are pinned on purpose: they are the product's answer to
// "how does a collapsed window look, and how does an open one look", they
// sit at column 0 of every window row, and the label column's arithmetic
// depends on each being exactly one cell.
//
// They are ASCII. That is the whole argument: a marker on every row must
// measure one cell in every terminal and must be in every font, and only
// ASCII guarantees both. The pair they replaced (▸/▾, U+25B8/U+25BE) was
// chosen for width alone — East-Asian Neutral, outside
// Extended_Pictographic, measured and pinned in the glyph policy — which
// is a good reason and a weaker guarantee than the character set itself.
// A test cannot measure a font, so the pin is on ASCII-ness.
func TestFoldMarkersAreAscii(t *testing.T) {
	cases := []struct {
		name  string
		glyph string
		want  string
	}{
		{"foldArrow", foldArrow, "+"},
		{"unfoldArrow", unfoldArrow, "-"},
	}
	for _, tc := range cases {
		if tc.glyph != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.glyph, tc.want)
		}
		for _, r := range tc.glyph {
			if r > 0x7F {
				t.Errorf("%s = %q is not ASCII — the marker's width is then a table's opinion, not the character set's", tc.name, tc.glyph)
			}
		}
		if w := cellWidth(tc.glyph); w != arrowCellWidth {
			t.Errorf("%s measures %d cells, layout reserves %d", tc.name, w, arrowCellWidth)
		}
	}
	if collapsedPrefixWidth != arrowCellWidth+1 {
		t.Errorf("collapsedPrefixWidth = %d, want the marker (%d) + one separating space",
			collapsedPrefixWidth, arrowCellWidth)
	}
}

// The marker is geometry, not color: it must not depend on the theme, on
// whether the palette is dimmed under an overlay, or on styles being
// attached at all. (This is what the fold_arrow/unfold_arrow theme keys
// used to violate — they let a palette switch change a structural
// affordance, and let a user value break the label columns.)
func TestFoldMarkersIndependentOfTheme(t *testing.T) {
	other := &theme.Theme{
		Primary: "#1e66f5", Dim: "#ccd0da", Muted: "#9ca0b0", Warning: "#df8e1d",
		Error: "#d20f39", Selection: "#fe640b", Added: "#40a02b", Removed: "#d20f39",
		Tool: "#df8e1d",
	}
	styles := []*Styles{DefaultStyles(), DefaultStyles().Dimmed(), NewStyles(other)}
	for i, st := range styles {
		wb := NewWindowBuffer(40, st)
		wb.AppendOrUpdate(tlv.TagAssistantT, "a1", "hello there")
		wb.ToggleFold(0)
		first := firstRow(stripANSI(wb.GetAll(-1, false)))
		if !strings.HasPrefix(first, foldArrow) {
			t.Errorf("styles[%d]: collapsed line = %q, want it to start with %q", i, first, foldArrow)
		}
		if c := contentColumn(first); c != collapsedPrefixWidth+CollapsedLabelWidth {
			t.Errorf("styles[%d]: content column = %d, want %d: %q",
				i, c, collapsedPrefixWidth+CollapsedLabelWidth, first)
		}
	}
}

// TestFoldMarkersKeepTheLabelColumn pins the property that makes the two
// states one shape: the label starts at the same cell whether the window
// is open or closed, so folding never shifts the text the reader is
// scanning down.
func TestFoldMarkersKeepTheLabelColumn(t *testing.T) {
	wb := NewWindowBuffer(60, DefaultStyles())
	wb.AppendOrUpdate(tlv.TagAssistantT, "a1", "hello there")

	expanded := stripANSI(wb.GetAll(-1, false))
	if !strings.HasPrefix(expanded, unfoldArrow+" ASSISTANT") {
		t.Errorf("expanded line = %q, want %q + the label", firstRow(expanded), unfoldArrow)
	}

	wb.ToggleFold(0)
	collapsed := stripANSI(wb.GetAll(-1, false))
	if !strings.HasPrefix(collapsed, foldArrow+" ASSISTANT") {
		t.Fatalf("collapsed line = %q, want %q + the label", firstRow(collapsed), foldArrow)
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
