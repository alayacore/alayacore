package terminal

// ThemeSelector provides a UI for selecting themes from a theme folder.
// It displays a list of available themes and allows the user to preview
// and select themes in real-time.

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// ThemeSelectorState represents the current state of the theme selector.
type ThemeSelectorState int

const (
	ThemeSelectorClosed ThemeSelectorState = iota
	ThemeSelectorOpen
)

// ThemeSelector manages theme selection UI.
type ThemeSelector struct {
	state       ThemeSelectorState
	themes      []ThemeInfo
	selectedIdx int
	scrollIdx   int
	width       int
	height      int
	styles      *Styles

	// Preview state
	previewTheme     *Theme
	previewTimer     *time.Timer
	previewThemeName string

	// Selection state
	themeJustSelected bool
	originalThemeName string // Theme name when selector was opened (for cancel)

	// App focus state
	hasFocus bool
}

// NewThemeSelector creates a new theme selector.
func NewThemeSelector(styles *Styles) *ThemeSelector {
	return &ThemeSelector{
		state:    ThemeSelectorClosed,
		themes:   []ThemeInfo{},
		styles:   styles,
		width:    60,
		height:   20,
		hasFocus: true,
	}
}

// --- State Management ---

func (ts *ThemeSelector) IsOpen() bool              { return ts.state != ThemeSelectorClosed }
func (ts *ThemeSelector) State() ThemeSelectorState { return ts.state }

func (ts *ThemeSelector) Open(themes []ThemeInfo, activeTheme string) {
	ts.themes = themes
	ts.state = ThemeSelectorOpen
	ts.scrollIdx = 0
	ts.selectedIdx = 0
	ts.originalThemeName = activeTheme // Save original theme for cancel
	ts.previewTheme = nil
	ts.previewThemeName = ""

	// Find active theme and position cursor
	for i, theme := range ts.themes {
		if theme.Name == activeTheme {
			ts.selectedIdx = i
			break
		}
	}

	// Ensure selected theme is visible
	ts.ensureVisible()
}

func (ts *ThemeSelector) Close() {
	ts.state = ThemeSelectorClosed
	ts.previewTheme = nil
	ts.previewThemeName = ""
	if ts.previewTimer != nil {
		ts.previewTimer.Stop()
		ts.previewTimer = nil
	}
}

func (ts *ThemeSelector) SetSize(width, height int) {
	if width > 0 {
		ts.width = width
	}
	ts.height = min(height-LayoutGap, SelectorMaxHeight)
}

func (ts *ThemeSelector) SetStyles(styles *Styles) {
	ts.styles = styles
}

func (ts *ThemeSelector) SetHasFocus(hasFocus bool) {
	ts.hasFocus = hasFocus
}

// --- Theme Management ---

func (ts *ThemeSelector) GetSelectedTheme() *ThemeInfo {
	if len(ts.themes) == 0 || ts.selectedIdx < 0 || ts.selectedIdx >= len(ts.themes) {
		return nil
	}
	return &ts.themes[ts.selectedIdx]
}

func (ts *ThemeSelector) GetPreviewTheme() *Theme {
	return ts.previewTheme
}

func (ts *ThemeSelector) GetOriginalThemeName() string {
	return ts.originalThemeName
}

func (ts *ThemeSelector) ConsumeThemeSelected() bool {
	if ts.themeJustSelected {
		ts.themeJustSelected = false
		return true
	}
	return false
}

// --- Bubble Tea Interface ---

func (ts *ThemeSelector) Init() tea.Cmd { return nil }

func (ts *ThemeSelector) Update(_ tea.Msg) (tea.Model, tea.Cmd) {
	if ts.state == ThemeSelectorClosed {
		return ts, nil
	}
	return ts, nil
}

func (ts *ThemeSelector) View() tea.View {
	if ts.state == ThemeSelectorClosed {
		return tea.NewView("")
	}
	return tea.NewView(lipgloss.NewStyle().Padding(1, 2).Render(ts.renderList()))
}

// --- Key Handling ---

func (ts *ThemeSelector) HandleKeyMsg(msg tea.KeyMsg, themeManager *ThemeManager) (*Theme, bool) {
	if ts.state == ThemeSelectorClosed {
		return nil, false
	}

	key := msg.String()
	var previewTheme *Theme

	switch key {
	case "up", "k":
		if ts.selectedIdx > 0 {
			ts.selectedIdx--
			ts.ensureVisible()
			previewTheme = ts.getPreviewTheme(themeManager)
		}
	case "down", "j":
		if ts.selectedIdx < len(ts.themes)-1 {
			ts.selectedIdx++
			ts.ensureVisible()
			previewTheme = ts.getPreviewTheme(themeManager)
		}
	case "enter":
		if len(ts.themes) > 0 && ts.selectedIdx >= 0 {
			ts.themeJustSelected = true
			ts.state = ThemeSelectorClosed
			previewTheme = ts.getPreviewTheme(themeManager)
			ts.previewTheme = nil
			return previewTheme, true
		}
	case "r":
		// Reload themes - signal to parent
		return nil, false // Parent will handle reload
	case "esc", "q":
		ts.Close()
		return nil, true
	}

	return previewTheme, true
}

func (ts *ThemeSelector) getPreviewTheme(themeManager *ThemeManager) *Theme {
	if themeManager == nil {
		return nil
	}

	if len(ts.themes) == 0 || ts.selectedIdx < 0 || ts.selectedIdx >= len(ts.themes) {
		return nil
	}

	themeName := ts.themes[ts.selectedIdx].Name
	if themeName == ts.previewThemeName && ts.previewTheme != nil {
		return ts.previewTheme
	}

	ts.previewTheme = themeManager.LoadTheme(themeName)
	ts.previewThemeName = themeName
	return ts.previewTheme
}

// --- Rendering ---

func (ts *ThemeSelector) renderList() string {
	var sb strings.Builder

	listHeight := SelectorListRows

	// Build content
	var lines []string

	switch {
	case len(ts.themes) == 0:
		lines = append(lines, ts.styles.System.Render("  No Theme"))
	default:
		ts.ensureVisible()

		for i := ts.scrollIdx; i < min(ts.scrollIdx+listHeight, len(ts.themes)); i++ {
			theme := ts.themes[i]
			if i == ts.selectedIdx {
				lines = append(lines, fmt.Sprintf("> %s", ts.styles.Text.Render(theme.Name)))
			} else {
				lines = append(lines, fmt.Sprintf("  %s", ts.styles.System.Render(theme.Name)))
			}
		}
	}

	// Pad lines to fill the list height (for consistent box height)
	for len(lines) < listHeight {
		lines = append(lines, "")
	}

	// Join lines
	content := strings.Join(lines, "\n")

	// Render with border
	borderColor := ts.styles.BorderFocused
	if !ts.hasFocus {
		borderColor = ts.styles.BorderBlurred
	}

	sb.WriteString(ts.styles.RenderBorderedBox(content, ts.width, borderColor, listHeight))

	// Compact command help
	sb.WriteString("\n")
	sb.WriteString(ts.styles.System.Render("j/k: navigate │ r: reload │ enter: select │ q/esc: close"))

	return sb.String()
}

func (ts *ThemeSelector) RenderOverlay(baseContent string, screenWidth, screenHeight int) string {
	if ts.state == ThemeSelectorClosed {
		return baseContent
	}
	return renderOverlay(baseContent, ts.renderList(), screenWidth, screenHeight)
}

// --- Helpers ---

func (ts *ThemeSelector) ensureVisible() {
	listHeight := SelectorListRows
	if ts.selectedIdx < ts.scrollIdx {
		ts.scrollIdx = ts.selectedIdx
	} else if ts.selectedIdx >= ts.scrollIdx+listHeight {
		ts.scrollIdx = ts.selectedIdx - listHeight + 1
	}
}

var _ tea.Model = (*ThemeSelector)(nil)
