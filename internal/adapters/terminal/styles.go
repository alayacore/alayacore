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
	// Label is the style for the chrome of a window's own line (its marker,
	// label, timestamp and, on the pinned row, the hidden-line count:
	// "ASSISTANT", "REASONING", "TOOL CALL", "USER PROMPT", …) in both fold
	// states. It is a field of its own — not derived from System at the call
	// site — because the cursor highlight recolors exactly this: the chrome
	// that names the window, never the content underneath it, and not the
	// tool name either, which is the row's payload (see Styles.Selected and
	// toolNameStyle, window.go).
	Label Style
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
// visual line array: [top rule, ...content lines, bottom rule].
//
// This is the box a FLOATING surface draws — a confirm dialog, the prompt
// input, and the filter box at the top of a selector overlay (model, theme,
// attachment, help). The standalone surfaces need the closing rule because
// nothing follows them: the box is the whole of what the reader sees. In a
// selector the closing rule is instead the one divider between the search
// and the list, which hangs bare under it (RenderListBody).
//
// Transcript windows deliberately do NOT use it. A window is opened by a
// rule carrying its label (Window.buildExpandRule) and the next window is
// opened by its own, so a closing rule under the content would repeat a
// delimiter that is already there — see Window.Render. Callers must wrap
// (wrapContent) and truncate (truncateWithSuffix) every content line
// themselves, and the content's wrap width is the FULL box width. Trailing
// padding is unnecessary: terminals ignore trailing whitespace, so content
// lines may be shorter than the box.
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

// RenderOpenBox renders a bordered box for a FLOATING surface — the prompt
// input, a confirm dialog, or a selector overlay's filter box — with only
// top/bottom rules and NO side borders ("open" style). A standalone surface
// has nothing after it, so it has to bracket itself:
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
	// Overlay boxes render every row as a hard line (no soft-wrap runs)
	// here: each original line becomes a standalone box row. An overlay
	// that wants a soft-wrap run — ConfirmDialog.RenderOverlay joins two
	// description rows of a long single-line preview — re-joins rows at
	// emission time, after this box is built.
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

// RenderListBody renders the rows of a selector overlay's list with NO rules
// of its own. The filter box above already ends in a rule, and that single
// rule is the whole delimiter the search and the list need between them; the
// help bar is what closes the surface below. A rule on either edge of the list
// would only repeat one of those, so the list hangs bare under the filter box.
//
// Content is padded to a fixed height so the overlay keeps its size whether
// the list is full, short, or empty — the same reason RenderOpenBox's height
// argument exists. Callers wrap and truncate every row themselves.
func (s *Styles) RenderListBody(content string, height int) string {
	lines := strings.Split(content, "\n")
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// NewStyles creates a Styles instance from a Theme
func NewStyles(t *theme.Theme) *Styles {
	baseStyle := NewStyle()
	return &Styles{
		// Output text styles
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
		// Label: the muted color, like System — kept as its own field so
		// the cursor highlight can recolor the label without touching the
		// content summary that shares the muted color.
		Label: baseStyle.Foreground(Color(t.Muted)),
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
		// Label dims with the rest of the chrome. (Under an overlay it
		// therefore reads the same as the cursor's own register, which is
		// how the highlight is hidden while a modal owns the screen.)
		Label: s.Label.Foreground(s.ColorDim),
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

// Selected returns a copy of Styles in which the window-chrome styles carry
// the selection color: the register a window's own line is drawn in while
// the display cursor is on it.
//
// Only the styles that name a window are swapped — Label (every window
// line's default color) and Error (SYSTEM ERROR's line). They are exactly
// the colors lineStyleForTag can return, so every glyph of the highlighted
// row's chrome — marker, label, timestamp — comes out in the selection color
// from one styles swap. Everything else keeps the color it has: System,
// which carries the collapsed line's content summary, and ToolContent, which
// carries a tool window's name and its arguments. The highlight marks the
// window, never what it says — and the name is on the row at both fold
// states, so swapping it would repaint the row when the reader folds it
// (toolNameStyle, window.go). (Prompt is deliberately not swapped:
// Styles.Prompt belongs to the prompt box and the overlay filter fields, and
// those are never window rows.)
//
// The counterpart of Dimmed() in the same spirit — a derived Styles rather
// than a flag threaded through every renderer, so that "who is the cursor"
// reaches the labels through the one channel the renderers already take
// their colors from.
func (s *Styles) Selected() *Styles {
	if s == nil {
		return nil
	}
	c := *s
	c.Label = c.Label.Foreground(s.BorderCursor)
	c.Error = c.Error.Foreground(s.BorderCursor)
	return &c
}
