package terminal

// Theme and styling for the terminal UI.
// The Theme struct, DefaultTheme(), and LoadTheme() now live in
// internal/theme — this file derives the Styles set used by the UI.

import (
	"image/color"
	"strings"

	"github.com/alayacore/alayacore/internal/theme"
)

// ============================================================================
// Styles - Derived Style Set
// ============================================================================

// Styles holds all derived styles for the terminal UI.
//
// IMMUTABILITY: Styles is created by NewStyles and never modified after
// construction. When the theme changes, a new Styles instance is created
// and swapped in atomically via atomic.Pointer in outputWriter. Storing
// a pointer obtained from to.styles.Load() and reading its fields is safe
// because the underlying struct is never mutated in-place — SetStyles
// always replaces the entire instance.
type Styles struct {
	// Output text styles
	Tool        Style
	ToolContent Style
	Error       Style
	System      Style
	Prompt      Style
	Attachment  Style
	DiffRemove  Style
	DiffAdd     Style

	// Display styles
	Input   Style
	Status  Style
	Confirm Style
	// Body is the style for plain body text (assistant messages,
	// reasoning, user message text, tool input/output). It carries NO
	// foreground color in normal mode — body text renders in the
	// terminal's default color, exactly like a shell. Styles.Dimmed()
	// (overlay active) gives it the dim color so expanded window bodies
	// dim together with the chrome.
	Body Style

	// Component-specific colors (exposed as color.Color for dynamic use)
	// Border colors
	BorderFocused color.Color
	BorderBlurred color.Color
	BorderCursor  color.Color

	// Text colors for dynamic use
	ColorAccent  color.Color
	ColorDim     color.Color
	ColorMuted   color.Color
	ColorWarning color.Color
}

// RenderOpenBoxLines renders an open box from VISUAL content lines (each
// element one terminal row, no '\n' inside) and returns the box as a
// visual line array: [top rule, ...content lines, bottom rule]. The
// window pipeline uses this form — the viewport clips windows by visual
// lines, so the box must expose its rows as an array (docs/internal/virtual-rendering-performance.md).
// Callers must wrap (wrapContent) and truncate (truncateWithSuffix) every
// content line themselves, and the content's wrap width is the FULL box
// width. Trailing padding is unnecessary: terminals ignore trailing
// whitespace, so content lines may be shorter than the box.
//
//nolint:revive // visualLine is an internal render type
func (s *Styles) RenderOpenBoxLines(lines []visualLine, width int, borderColor color.Color) []visualLine {
	rule := strings.Repeat("─", max(0, width))
	borderStyle := NewStyle().Foreground(borderColor)
	rule = borderStyle.Render(rule)

	out := make([]visualLine, 0, len(lines)+2)
	out = append(out, visualLine{Text: rule})
	out = append(out, lines...)
	out = append(out, visualLine{Text: rule})
	return out
}

// RenderOpenBox renders a box with only top/bottom rules and NO side
// borders ("open" style) — the collapsed-window design language:
//
//	──────────────────────────────────────────
//	content line                           ← caller guarantees ≤ width
//	──────────────────────────────────────────
//
// Plain rules (no corner glyphs) read as clean dividers that bracket the
// content — the most minimal form. With no side borders, corners would
// only emphasize the missing sides; a bare rule avoids that entirely.
//
// Because there are no side borders, the box renderer can no longer
// pad/truncate content to the box width — callers must wrap
// (wrapContent) and truncate (truncateWithSuffix) every content line
// themselves, and the content's wrap width is the FULL box width.
// Trailing padding is unnecessary: terminals ignore
// trailing whitespace, so content lines may be shorter than the box.
// height, when given, pads the content area to a fixed number of rows.
func (s *Styles) RenderOpenBox(content string, width int, borderColor color.Color, height ...int) string {
	lines := strings.Split(content, "\n")
	if len(height) > 0 {
		for len(lines) < height[0] {
			lines = append(lines, "")
		}
	}
	// Overlay boxes render every row as a hard line (no soft-wrap runs):
	// each original line becomes a standalone row.
	vl := make([]visualLine, 0, len(lines))
	for _, l := range lines {
		vl = append(vl, visualLine{Text: l})
	}
	box := s.RenderOpenBoxLines(vl, width, borderColor)
	out := make([]string, 0, len(box))
	for _, b := range box {
		out = append(out, b.Text)
	}
	return strings.Join(out, "\n")
}

// NewStyles creates a Styles instance from a Theme
func NewStyles(t *theme.Theme) *Styles {
	baseStyle := NewStyle()
	return &Styles{
		// Output text styles
		Tool:        baseStyle.Foreground(Color(t.Tool)),
		ToolContent: baseStyle.Foreground(Color(t.Muted)),
		Error:       baseStyle.Foreground(Color(t.Error)),
		System:      baseStyle.Foreground(Color(t.Muted)),
		Prompt:      baseStyle.Foreground(Color(t.Primary)).Bold(true),
		Attachment:  baseStyle.Foreground(Color(t.Warning)).Bold(true),
		DiffRemove:  baseStyle.Foreground(Color(t.Removed)),
		DiffAdd:     baseStyle.Foreground(Color(t.Added)),

		// Display styles
		Input:   baseStyle,
		Status:  baseStyle.Foreground(Color(t.Dim)),
		Confirm: baseStyle.Foreground(Color(t.Warning)).Bold(true),
		// Body stays colorless (terminal default) — see Styles.Body.
		Body: baseStyle,

		// Component-specific colors
		BorderFocused: Color(t.Primary),
		BorderBlurred: Color(t.Dim),
		BorderCursor:  Color(t.Selection),

		ColorAccent:  Color(t.Primary),
		ColorDim:     Color(t.Dim),
		ColorMuted:   Color(t.Muted),
		ColorWarning: Color(t.Warning),
	}
}

// Dimmed returns a copy of Styles with all foreground colors replaced by
// ColorDim. Used to render content in a dimmed visual state when overlays
// are active. Preserves non-color attributes (bold, italic, border style, etc.).
func (s *Styles) Dimmed() *Styles {
	if s == nil {
		return nil
	}
	return &Styles{
		// Output text styles — all foreground → ColorDim
		Tool:        s.Tool.Foreground(s.ColorDim),
		ToolContent: s.ToolContent.Foreground(s.ColorDim),
		Error:       s.Error.Foreground(s.ColorDim),
		System:      s.System.Foreground(s.ColorDim),
		Prompt:      s.Prompt.Foreground(s.ColorDim),
		Attachment:  s.Attachment.Foreground(s.ColorDim),
		DiffRemove:  s.DiffRemove.Foreground(s.ColorDim),
		DiffAdd:     s.DiffAdd.Foreground(s.ColorDim),

		// Display styles
		Input:   s.Input.Foreground(s.ColorDim),
		Status:  s.Status.Foreground(s.ColorDim),
		Confirm: s.Confirm.Foreground(s.ColorDim),
		// Body gains the dim foreground under an overlay so plain body
		// text (which is colorless by default) dims with everything else.
		Body: s.Body.Foreground(s.ColorDim),

		// Colors — unchanged (used as dynamic color references)
		BorderFocused: s.ColorDim,
		BorderBlurred: s.ColorDim,
		BorderCursor:  s.ColorDim,

		ColorAccent:  s.ColorDim,
		ColorDim:     s.ColorDim,
		ColorMuted:   s.ColorDim,
		ColorWarning: s.ColorDim,
	}
}
