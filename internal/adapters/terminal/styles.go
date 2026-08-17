package terminal

// Theme and styling for the terminal UI.
// The Theme struct, DefaultTheme(), and LoadTheme() now live in
// internal/theme — this file only keeps lipgloss-specific style derivation.

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/alayacore/alayacore/internal/theme"
)

// ============================================================================
// Styles - Derived Lipgloss Styles
// ============================================================================

// Styles holds all lipgloss styles for the terminal UI.
//
// IMMUTABILITY: Styles is created by NewStyles and never modified after
// construction. When the theme changes, a new Styles instance is created
// and swapped in atomically via atomic.Pointer in outputWriter. Storing
// a pointer obtained from to.styles.Load() and reading its fields is safe
// because the underlying struct is never mutated in-place — SetStyles
// always replaces the entire instance.
type Styles struct {
	// Output text styles
	Text        lipgloss.Style
	UserInput   lipgloss.Style
	Tool        lipgloss.Style
	ToolContent lipgloss.Style
	Reasoning   lipgloss.Style
	Error       lipgloss.Style
	System      lipgloss.Style
	Prompt      lipgloss.Style
	Attachment  lipgloss.Style
	DiffRemove  lipgloss.Style
	DiffAdd     lipgloss.Style

	// Display styles
	Input     lipgloss.Style
	Status    lipgloss.Style
	Separator lipgloss.Style
	Confirm   lipgloss.Style

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
	ColorError   color.Color
	ColorSuccess color.Color
	CursorColor  color.Color

	// Fold-state arrow glyphs (from the theme; single codepoint).
	FoldArrow   string // collapsed-window arrow
	UnfoldArrow string // expanded-window arrow
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
// Because there are no side borders, lipgloss's border renderer can no
// longer pad/truncate content to the box width — callers must wrap
// (wrapContent) and truncate (truncateWithSuffix) every content line
// themselves, and the content's wrap width is the FULL box width.
// Trailing padding is unnecessary: terminals ignore
// trailing whitespace, so content lines may be shorter than the box.
// height, when given, pads the content area to a fixed number of rows.
func (s *Styles) RenderOpenBox(content string, width int, borderColor color.Color, height ...int) string {
	rule := strings.Repeat("─", max(0, width))
	bottom := strings.Repeat("─", max(0, width))
	borderStyle := lipgloss.NewStyle().Foreground(borderColor)
	rule = borderStyle.Render(rule)
	bottom = borderStyle.Render(bottom)

	lines := strings.Split(content, "\n")
	var sb strings.Builder
	sb.Grow(len(content) + 2*width + 8)
	sb.WriteString(rule)
	for _, ln := range lines {
		sb.WriteString("\n")
		sb.WriteString(ln)
	}
	if len(height) > 0 {
		for i := len(lines); i < height[0]; i++ {
			sb.WriteString("\n")
		}
	}
	sb.WriteString("\n")
	sb.WriteString(bottom)
	return sb.String()
}

// NewStyles creates a Styles instance from a Theme
func NewStyles(t *theme.Theme) *Styles {
	baseStyle := lipgloss.NewStyle()
	return &Styles{
		// Output text styles
		Text:        baseStyle.Foreground(lipgloss.Color(t.Text)).Bold(true),
		UserInput:   baseStyle.Bold(true),
		Tool:        baseStyle.Foreground(lipgloss.Color(t.Tool)),
		ToolContent: baseStyle.Foreground(lipgloss.Color(t.Muted)),
		Reasoning:   baseStyle.Foreground(lipgloss.Color(t.Muted)).Italic(true),
		Error:       baseStyle.Foreground(lipgloss.Color(t.Error)),
		System:      baseStyle.Foreground(lipgloss.Color(t.Muted)),
		Prompt:      baseStyle.Foreground(lipgloss.Color(t.Primary)).Bold(true),
		Attachment:  baseStyle.Foreground(lipgloss.Color(t.Warning)).Bold(true),
		DiffRemove:  baseStyle.Foreground(lipgloss.Color(t.Removed)),
		DiffAdd:     baseStyle.Foreground(lipgloss.Color(t.Added)),

		// Display styles
		Input:     baseStyle,
		Status:    baseStyle.Foreground(lipgloss.Color(t.Dim)),
		Separator: baseStyle.Foreground(lipgloss.Color(t.Dim)),
		Confirm:   baseStyle.Foreground(lipgloss.Color(t.Warning)).Bold(true),

		// Component-specific colors
		BorderFocused: lipgloss.Color(t.Primary),
		BorderBlurred: lipgloss.Color(t.Dim),
		BorderCursor:  lipgloss.Color(t.Selection),

		ColorAccent:  lipgloss.Color(t.Primary),
		ColorDim:     lipgloss.Color(t.Dim),
		ColorMuted:   lipgloss.Color(t.Muted),
		ColorWarning: lipgloss.Color(t.Warning),
		ColorError:   lipgloss.Color(t.Error),
		ColorSuccess: lipgloss.Color(t.Success),
		CursorColor:  lipgloss.Color(t.Cursor),

		FoldArrow:   t.FoldArrow,
		UnfoldArrow: t.UnfoldArrow,
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
		Text:        s.Text.Foreground(s.ColorDim),
		UserInput:   s.UserInput.Foreground(s.ColorDim),
		Tool:        s.Tool.Foreground(s.ColorDim),
		ToolContent: s.ToolContent.Foreground(s.ColorDim),
		Reasoning:   s.Reasoning.Foreground(s.ColorDim),
		Error:       s.Error.Foreground(s.ColorDim),
		System:      s.System.Foreground(s.ColorDim),
		Prompt:      s.Prompt.Foreground(s.ColorDim),
		Attachment:  s.Attachment.Foreground(s.ColorDim),
		DiffRemove:  s.DiffRemove.Foreground(s.ColorDim),
		DiffAdd:     s.DiffAdd.Foreground(s.ColorDim),

		// Display styles
		Input:     s.Input.Foreground(s.ColorDim),
		Status:    s.Status.Foreground(s.ColorDim),
		Separator: s.Separator.Foreground(s.ColorDim),
		Confirm:   s.Confirm.Foreground(s.ColorDim),

		// Colors — unchanged (used as dynamic color references)
		BorderFocused: s.ColorDim,
		BorderBlurred: s.ColorDim,
		BorderCursor:  s.ColorDim,

		ColorAccent:  s.ColorDim,
		ColorDim:     s.ColorDim,
		ColorMuted:   s.ColorDim,
		ColorWarning: s.ColorDim,
		ColorError:   s.ColorDim,
		ColorSuccess: s.ColorDim,
		CursorColor:  s.ColorDim,

		// Glyphs — unchanged (dimming affects color, not characters)
		FoldArrow:   s.FoldArrow,
		UnfoldArrow: s.UnfoldArrow,
	}
}
