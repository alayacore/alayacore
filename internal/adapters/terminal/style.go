package terminal

// Style layer (module 6, S3): replaces lipgloss with a minimal self-built
// implementation. The SGR output is byte-compatible with lipgloss v2 — both
// build on github.com/charmbracelet/x/ansi, and the canonical attribute
// order (bold, italic, underline, strikethrough, then fg/bg) matches
// lipgloss's Render and the pen-style String() in wrap.go.
//
// The Styles struct in styles.go keeps its exact field set; only the
// underlying type changed from Style to this Style.

import (
	"image/color"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Color parses a color spec into an image/color.Color (nil = no color):
// "#RRGGBB"/"#RGB" hex, or a decimal ANSI value (0-15 basic colors,
// 16-255 extended colors, >255 packed RGB). Mirrors lipgloss.Color.
func Color(s string) color.Color {
	if strings.HasPrefix(s, "#") {
		c, err := parseHexColor(s)
		if err != nil {
			return nil
		}
		return c
	}

	i, err := strconv.Atoi(s)
	if err != nil {
		return nil
	}
	if i < 0 {
		i = -i // only positive numbers
	}
	switch {
	case i < 16:
		return ansi.BasicColor(i) //nolint:gosec // G115: value bounded by switch
	case i < 256:
		return ansi.IndexedColor(i) //nolint:gosec // G115: value bounded by switch
	default:
		return color.RGBA{
			R: uint8(i >> 16), //nolint:gosec // G115: intentional packing
			G: uint8(i >> 8),  //nolint:gosec // G115: intentional packing
			B: uint8(i),       //nolint:gosec // G115: intentional packing
			A: 0xff,
		}
	}
}

// parseHexColor parses "#RRGGBB" or "#RGB" into a color.RGBA.
//
//nolint:gocyclo // hex nibble dispatch
func parseHexColor(s string) (color.RGBA, error) {
	var c color.RGBA
	c.A = 0xff

	if len(s) == 0 || s[0] != '#' {
		return c, errInvalidHex
	}

	hexToByte := func(b byte) (byte, bool) {
		switch {
		case b >= '0' && b <= '9':
			return b - '0', true
		case b >= 'a' && b <= 'f':
			return b - 'a' + 10, true
		case b >= 'A' && b <= 'F':
			return b - 'A' + 10, true
		}
		return 0, false
	}

	switch len(s) {
	case 4: // #RGB
		for i := 1; i < 4; i++ {
			v, ok := hexToByte(s[i])
			if !ok {
				return c, errInvalidHex
			}
			v |= v << 4 // expand 4-bit to 8-bit
			switch i {
			case 1:
				c.R = v
			case 2:
				c.G = v
			case 3:
				c.B = v
			}
		}
	case 7: // #RRGGBB
		for i := 1; i < 7; i += 2 {
			hi, ok1 := hexToByte(s[i])
			lo, ok2 := hexToByte(s[i+1])
			if !ok1 || !ok2 {
				return c, errInvalidHex
			}
			v := hi<<4 | lo
			switch i {
			case 1:
				c.R = v
			case 3:
				c.G = v
			case 5:
				c.B = v
			}
		}
	default:
		return c, errInvalidHex
	}
	return c, nil
}

// errInvalidHex is returned when a color string is not valid hex.
var errInvalidHex = errString("invalid hex color")

type errString string

func (e errString) Error() string { return string(e) }

// Style is an immutable, chainable text style. It renders ANSI SGR
// sequences byte-compatible with lipgloss v2.
type Style struct {
	fg, bg        color.Color
	bold          bool
	italic        bool
	underline     bool
	strikethrough bool
	width         int
	inline        bool
}

// NewStyle returns an empty style.
func NewStyle() Style { return Style{} }

// Foreground sets the foreground color (nil clears it).
func (s Style) Foreground(c color.Color) Style { s.fg = c; return s }

// Background sets the background color (nil clears it).
func (s Style) Background(c color.Color) Style { s.bg = c; return s }

// GetForeground returns the foreground color (nil when unset).
func (s Style) GetForeground() color.Color { return s.fg }

// Bold sets the bold attribute.
func (s Style) Bold(b bool) Style { s.bold = b; return s }

// Italic sets the italic attribute.
func (s Style) Italic(b bool) Style { s.italic = b; return s }

// Underline sets the underline attribute.
func (s Style) Underline(b bool) Style { s.underline = b; return s }

// Strikethrough sets the strikethrough attribute.
func (s Style) Strikethrough(b bool) Style { s.strikethrough = b; return s }

// Width sets the block width: every rendered line is padded with spaces to
// this width (matching lipgloss's left-aligned block padding; the padding
// carries the fg/bg colors).
func (s Style) Width(w int) Style { s.width = w; return s }

// Inline renders the string as a single line: newlines are stripped so the
// whole content is wrapped in one SGR pair (no per-line resets).
func (s Style) Inline(b bool) Style { s.inline = b; return s }

// Render applies the style to the given strings (joined with spaces) and
// returns the styled result. Multi-line strings are styled per line — each
// line is self-contained (SGR prefix + reset), which is what the soft-wrap
// fragment pipeline relies on.
//
//nolint:gocyclo // mirrors lipgloss Render's branch order
func (s Style) Render(strs ...string) string {
	str := strings.Join(strs, " ")

	// Single-line mode: strip newlines (the per-line styling below would
	// otherwise emit stray resets between the concatenated lines).
	if s.inline {
		str = strings.ReplaceAll(str, "\n", "")
	}

	// Word wrap at the block width (lipgloss parity; the app only sets
	// Width on single-line content, so this is normally a no-op).
	if !s.inline && s.width > 0 {
		str = Wrap(str, s.width, "")
	}

	// Build the SGR style in lipgloss's canonical order: bold/italic first,
	// underline early AND late (lipgloss appends Underline(true) before the
	// colors and UnderlineStyle after them), colors, then strikethrough.
	var te ansi.Style
	if s.bold {
		te = te.Bold()
	}
	if s.italic {
		te = te.Italic(true)
	}
	if s.underline {
		te = te.Underline(true)
	}
	if s.fg != nil {
		te = te.ForegroundColor(s.fg)
	}
	if s.bg != nil {
		te = te.BackgroundColor(s.bg)
	}
	if s.underline {
		te = te.UnderlineStyle(ansi.UnderlineSingle)
	}
	if s.strikethrough {
		te = te.Strikethrough(true)
	}

	// Style each line separately: every row is self-contained.
	var b strings.Builder
	isFirst := true
	for line := range strings.SplitSeq(str, "\n") {
		if isFirst {
			isFirst = false
		} else {
			b.WriteByte('\n')
		}
		b.WriteString(te.Styled(line))
	}
	str = b.String()

	// Block width: pad each line to the width. lipgloss styles the padding
	// with a "whitespace style" that only carries bg + underline color (fg
	// joins it only when reverse, which this style layer does not support),
	// so the common case is plain spaces.
	if s.width > 0 {
		var ws ansi.Style
		if s.bg != nil {
			ws = ws.BackgroundColor(s.bg)
		}
		lines := strings.Split(str, "\n")
		for i, l := range lines {
			if pad := s.width - ansi.StringWidth(l); pad > 0 {
				spaces := strings.Repeat(" ", pad)
				if len(ws) > 0 {
					spaces = ws.Styled(spaces)
				}
				lines[i] = l + spaces
			}
		}
		str = strings.Join(lines, "\n")
	}

	return str
}

// Width returns the cell width of the widest line in str. ANSI sequences
// are ignored; wide characters (CJK, emoji) count as their terminal width.
func Width(str string) int {
	width := 0
	for l := range strings.SplitSeq(str, "\n") {
		if w := ansi.StringWidth(l); w > width {
			width = w
		}
	}
	return width
}

// Height returns the number of lines in str (count of '\n' + 1).
func Height(str string) int {
	return strings.Count(str, "\n") + 1
}
