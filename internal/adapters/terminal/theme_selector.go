package terminal

// ThemeSelector provides a UI for selecting themes from a theme folder.
// It displays a searchable list of available themes and allows the user
// to preview and select themes in real-time.

import (
	"fmt"
	"image/color"
	"strings"

	"github.com/alayacore/alayacore/internal/theme"
)

// ThemeSelector manages theme selection UI.
// ThemeSelector is an overlay for browsing and previewing themes.
//
// Field groups:
//
//	Elm UI state  — value types / primitives (copied on every WithXxx).
//	Dependencies  — pointers to shared styles.
type ThemeSelector struct {
	FilteredListCore

	// ── Elm UI state (value types, copied on every WithXxx) ─
	themes            []ThemeEntry
	filteredThemes    []ThemeEntry
	previewTheme      *theme.Theme // preview theme (nil = no preview)
	previewThemeName  string
	originalThemeName string // theme name when opened (for cancel)
}

// NewThemeSelector creates a new theme selector.
func NewThemeSelector(styles *Styles) ThemeSelector {
	input := newFilterInput("Search themes...")
	ts := ThemeSelector{
		themes:         []ThemeEntry{},
		filteredThemes: []ThemeEntry{},
	}
	ts.Width = 60
	ts.Height = 20
	ts.HasFocus = true
	ts.FilterInput = input
	ts.lastFilterValue = "\x00"
	ts.Styles = styles
	return ts
}

// --- State Management ---

func (ts ThemeSelector) WithSize(width, height int) ThemeSelector {
	ts.FilteredListCore = ts.FilteredListCore.WithSize(width, height)
	return ts
}

func (ts ThemeSelector) WithStyles(styles *Styles) ThemeSelector {
	ts.FilteredListCore = ts.FilteredListCore.WithStyles(styles)
	return ts
}

func (ts ThemeSelector) WithFocus(focused bool) ThemeSelector {
	ts.FilteredListCore = ts.FilteredListCore.WithFocus(focused)
	return ts
}

func (ts ThemeSelector) Open(themes []ThemeEntry, activeTheme string) ThemeSelector {
	ts.themes = themes
	ts.State = FilteredListOpen
	ts.FilterInput = ts.FilterInput.WithValue("")
	ts.lastFilterValue = "\x00"
	ts.FilterInputFocused = true
	ts.FilterInput = ts.FilterInput.Focus()
	ts.FilteredListCore = ts.FilteredListCore.updateFilterInputStyles()
	ts.ScrollIdx = 0
	ts.SelectedIdx = 0
	ts.originalThemeName = activeTheme
	ts.previewTheme = nil
	ts.previewThemeName = ""

	ts = ts.updateFilteredThemes()
	ts = ts.selectThemeByName(activeTheme)
	ts = ts.loadPreviewTheme()
	return ts
}

func (ts ThemeSelector) Close() ThemeSelector {
	ts.State = FilteredListClosed
	ts.previewTheme = nil
	ts.previewThemeName = ""
	return ts
}

// selectThemeByName sets the selection to the theme with the given name.
func (ts ThemeSelector) selectThemeByName(name string) ThemeSelector {
	if name == "" {
		return ts
	}
	for i, t := range ts.filteredThemes {
		if t.Name == name {
			ts.SelectedIdx = i
			ts.FilteredListCore = ts.FilteredListCore.EnsureVisible()
			break
		}
	}
	return ts
}

// --- Theme Management ---

func (ts ThemeSelector) GetSelectedTheme() *ThemeEntry {
	if len(ts.filteredThemes) == 0 || ts.SelectedIdx < 0 || ts.SelectedIdx >= len(ts.filteredThemes) {
		return nil
	}
	return &ts.filteredThemes[ts.SelectedIdx]
}

func (ts ThemeSelector) GetPreviewTheme() *theme.Theme {
	return ts.previewTheme
}

func (ts ThemeSelector) GetOriginalThemeName() string {
	return ts.originalThemeName
}

// --- Init (unused, kept for interface compatibility) ---

func (ts ThemeSelector) Init() Cmd { return nil }

func (ts ThemeSelector) View() View {
	if ts.State == FilteredListClosed {
		return NewView("")
	}
	return NewView(ts.renderList())
}

// --- Key Handling ---

//nolint:gocyclo
func (ts ThemeSelector) Update(msg Msg) (ThemeSelector, Cmd) {
	if ts.State == FilteredListClosed {
		return ts, nil
	}

	keyMsg, ok := msg.(KeyMsg)
	if !ok {
		return ts, nil
	}
	key := keyMsg.String()

	fl, result := ts.FilteredListCore.HandleKey(keyMsg)
	ts.FilteredListCore = fl

	if result.Handled {
		// Close overlay.
		if !fl.FilterInputFocused && (key == keyQ || key == keyEsc) {
			return ts.Close(), func() Msg { return OverlayClosedMsg{} }
		}

		// Enter on list: select theme.
		if key == keyEnter && !fl.FilterInputFocused {
			if sel := ts.GetSelectedTheme(); sel != nil {
				ts = ts.loadPreviewTheme()
				ts.State = FilteredListClosed
				return ts, func() Msg { return ThemeSelectedMsg{Name: sel.Name} }
			}
		}

		// Enter on filter: select first matched theme.
		if key == keyEnter && fl.FilterInputFocused && len(ts.filteredThemes) > 0 {
			ts.SelectedIdx = 0
			sel := ts.filteredThemes[0]
			ts = ts.loadPreviewTheme()
			ts.State = FilteredListClosed
			return ts, func() Msg { return ThemeSelectedMsg{Name: sel.Name} }
		}

		// Filter changed: update filtered list and load preview for first result.
		if result.FilterChanged && ts.FilterInputFocused {
			ts = ts.updateFilteredThemes()
			ts = ts.loadPreviewTheme()
		}

		// List navigation: load preview.
		if !ts.FilterInputFocused {
			ts = ts.handleListKeys(key)
		}

		return ts, nil
	}

	if !ts.FilterInputFocused {
		ts = ts.handleListKeys(key)
	}
	return ts, nil
}

func (ts ThemeSelector) handleListKeys(key string) ThemeSelector {
	switch key {
	case keyJ, keyDown:
		if ts.SelectedIdx < len(ts.filteredThemes)-1 {
			ts.SelectedIdx++
			ts = ts.loadPreviewTheme()
		}
	case keyK, keyUp:
		if ts.SelectedIdx > 0 {
			ts.SelectedIdx--
			ts = ts.loadPreviewTheme()
		}
	}
	return ts
}

func (ts ThemeSelector) loadPreviewTheme() ThemeSelector {
	if len(ts.filteredThemes) == 0 || ts.SelectedIdx < 0 || ts.SelectedIdx >= len(ts.filteredThemes) {
		return ts
	}

	entry := ts.filteredThemes[ts.SelectedIdx]
	if entry.Name == ts.previewThemeName && ts.previewTheme != nil {
		return ts
	}

	ts.previewTheme = entry.Theme
	ts.previewThemeName = entry.Name
	return ts
}

// --- Filtering ---

func (ts ThemeSelector) updateFilteredThemes() ThemeSelector {
	search := ts.FilterInput.Value()
	if search == ts.lastFilterValue {
		return ts
	}
	ts.lastFilterValue = search

	if search == "" {
		ts.filteredThemes = make([]ThemeEntry, len(ts.themes))
		copy(ts.filteredThemes, ts.themes)
	} else {
		term := strings.ToLower(search)
		ts.filteredThemes = ts.filteredThemes[:0]
		for _, t := range ts.themes {
			if FuzzyMatch(term, strings.ToLower(t.Name)) {
				ts.filteredThemes = append(ts.filteredThemes, t)
			}
		}
	}

	ts.SelectedIdx = 0
	ts.ScrollIdx = 0
	ts.FilteredListCore = ts.FilteredListCore.ClampSelection(len(ts.filteredThemes))
	return ts
}

// --- Rendering ---

func (ts ThemeSelector) renderList() string {
	var sb strings.Builder

	titleStyle := NewStyle().Background(ts.Styles.ColorDim).Foreground(ts.Styles.ColorAccent).Bold(true)
	sb.WriteString(titleStyle.Render(fmt.Sprintf("%-*s", ts.Width, "Theme Selector")))
	sb.WriteString("\n")

	filterBox := ts.Styles.RenderOpenBox(ts.FilterInput.View(), ts.Width, ts.FilterBorderColor())
	sb.WriteString(filterBox)
	sb.WriteString("\n")

	sb.WriteString(ts.Styles.System.Render("Current: "))
	themeName := truncateWithSuffix(ts.originalThemeName, max(0, ts.Width-Width("Current: ")))
	sb.WriteString(ts.Styles.Text.Render(themeName))
	sb.WriteString("\n")

	listBorderColor := ts.ListBorderColor()
	sb.WriteString(ts.renderThemeList(Width(filterBox), listBorderColor))

	helpStyle := NewStyle().Background(ts.Styles.ColorDim).Foreground(ts.Styles.ColorMuted)
	var help string
	if ts.FilterInputFocused {
		help = "  tab: list │ enter: select │ esc: close"
	} else {
		help = "  tab: search │ j/k: navigate │ enter: select │ q/esc: close"
	}
	sb.WriteString("\n")
	sb.WriteString(renderHelpBar(helpStyle, help, ts.Width))

	return sb.String()
}

func (ts ThemeSelector) renderThemeList(width int, borderColor color.Color) string {
	var content strings.Builder
	listHeight := SelectorListRows
	innerWidth := max(0, width)

	switch {
	case len(ts.filteredThemes) == 0:
		content.WriteString(ts.Styles.System.Render("No themes match your search."))
	default:
		// Ensure selected item is visible.
		if ts.SelectedIdx < ts.ScrollIdx {
			ts.ScrollIdx = ts.SelectedIdx
		} else if ts.SelectedIdx >= ts.ScrollIdx+listHeight {
			ts.ScrollIdx = ts.SelectedIdx - listHeight + 1
		}
		for i := ts.ScrollIdx; i < min(ts.ScrollIdx+listHeight, len(ts.filteredThemes)); i++ {
			t := ts.filteredThemes[i]
			nameMaxWidth := max(0, innerWidth-2)
			themeName := t.Name
			if nameMaxWidth > 0 {
				themeName = truncateWithSuffix(themeName, nameMaxWidth)
			}
			if i == ts.SelectedIdx {
				// Selection is highlighted via the Text style; rows are
				// flush left with no indent.
				content.WriteString(ts.Styles.Text.Render(themeName))
			} else {
				content.WriteString(ts.Styles.System.Render(themeName))
			}
			if i < min(ts.ScrollIdx+listHeight, len(ts.filteredThemes))-1 {
				content.WriteString("\n")
			}
		}
	}

	return ts.Styles.RenderOpenBox(content.String(), width, borderColor, listHeight)
}

// RenderOverlay renders the theme selector as an overlay on top of base content.
func (ts ThemeSelector) RenderOverlay(baseContent string, screenWidth, screenHeight int) string {
	if ts.State == FilteredListClosed {
		return baseContent
	}
	return renderOverlay(baseContent, ts.View().Content, screenWidth, screenHeight, 0)
}

// CursorPosition returns the screen position of the filter input's real
// terminal cursor when the overlay is open and the filter has focus.
func (ts ThemeSelector) CursorPosition(screenWidth, screenHeight int) (x, y int, ok bool) {
	return ts.FilteredListCore.CursorPosition(ts.View().Content, screenWidth, screenHeight)
}
