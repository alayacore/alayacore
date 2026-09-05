package theme

import (
	"fmt"

	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

// Default fold-state arrow glyphs, used when a theme leaves the key unset
// and when a configured glyph cannot be drawn in the space the header
// layout reserves (see ArrowGlyphs).
//
// These two and not the heavier ▶ (U+25B6) / ▼ (U+25BC): both small
// triangles are East Asian Width "N" (U+25B6/U+25BC are "A", ambiguous —
// terminals configured for double-width ambiguous characters draw them
// two cells wide) and default to text presentation (U+25B6/U+25BC are
// Emoji_Presentation=Yes, so Windows Terminal, GNOME Console and any
// font stack with an emoji fallback paint them as a two-cell color
// emoji). Either case silently breaks header alignment.
const (
	DefaultFoldArrow   = "▸" // collapsed window: pointing right, can be opened
	DefaultUnfoldArrow = "▾" // expanded window: pointing down, showing content
)

// ArrowGlyphs returns the fold-state arrow glyphs for a theme, replacing
// a configured glyph with the default when it is empty or cannot be
// rendered in the single cell the layout gives it. The third result
// carries one message per replaced glyph (never for an empty value, which
// is an unset key rather than a bad one); callers that can show a warning
// pass them on, the rest discard them.
//
// A usable glyph is exactly one grapheme cluster one display cell wide.
// Collapsed headers are laid out as "arrow + space + label column", and
// the label column and the content column are computed from that fixed
// prefix — a wider arrow shifts, or with the truncation applied, silently
// eats the content of every window in the buffer.
func ArrowGlyphs(fold, unfold string) (string, string, []string) {
	var msgs []string
	if fold == "" {
		fold = DefaultFoldArrow
	} else if issue := cellIssue(fold); issue != "" {
		msgs = append(msgs, fmt.Sprintf("fold_arrow %q: %s; using %q", fold, issue, DefaultFoldArrow))
		fold = DefaultFoldArrow
	}
	if unfold == "" {
		unfold = DefaultUnfoldArrow
	} else if issue := cellIssue(unfold); issue != "" {
		msgs = append(msgs, fmt.Sprintf("unfold_arrow %q: %s; using %q", unfold, issue, DefaultUnfoldArrow))
		unfold = DefaultUnfoldArrow
	}
	return fold, unfold, msgs
}

// cellIssue describes why a glyph cannot occupy one display cell, or
// returns "" when it can.
func cellIssue(glyph string) string {
	if _, rest, _, _ := uniseg.FirstGraphemeClusterInString(glyph, -1); rest != "" {
		return fmt.Sprintf("not a single grapheme cluster (%d cells wide)", ansi.StringWidth(glyph))
	}
	if w := ansi.StringWidth(glyph); w != 1 {
		return fmt.Sprintf("renders %d cells wide, the header layout reserves 1", w)
	}
	return ""
}
