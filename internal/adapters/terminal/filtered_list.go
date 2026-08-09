package terminal

// FilteredListCore provides shared filtering, navigation, and overlay rendering
// for selector-style UI components (ModelSelector, HelpWindow, ThemeSelector,
// AttachmentWindow).
//
// All components follow the same pattern:
//   - Text input for filtering
//   - Scrollable list of items below
//   - Tab toggles focus between filter and list
//   - Arrow keys navigate the list
//   - Escape/q closes
//   - Same scroll clamping, border styling, overlay positioning
//
// Embedding types call the core methods and add their own item-specific
// filtering, rendering, and key handling.
//
// SINGLE-GOROUTINE: All methods of FilteredListCore are called exclusively
// from the Bubble Tea event loop. No mutex is needed.

import (
	"image/color"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// FilteredListState represents the current state of a filtered list.
type FilteredListState int

const (
	FilteredListClosed FilteredListState = iota
	FilteredListOpen
)

// FilteredListCore holds shared state and methods for filtered list components.
type FilteredListCore struct {
	State       FilteredListState
	SelectedIdx int
	ScrollIdx   int
	Width       int
	Height      int
	Styles      *Styles
	HasFocus    bool

	FilterInput        InputField
	FilterInputFocused bool
	lastFilterValue    string
}

func (fl FilteredListCore) IsOpen() bool { return fl.State != FilteredListClosed }

// Close closes the filtered list.
func (fl FilteredListCore) Close() FilteredListCore {
	fl.State = FilteredListClosed
	return fl
}

// WithSize updates the width and height of the filtered list.
func (fl FilteredListCore) WithSize(width, height int) FilteredListCore {
	if width > 0 {
		fl.Width = width
		fl.FilterInput = fl.FilterInput.WithWidth(max(0, width-InputPaddingH))
	}
	fl.Height = min(height-LayoutGap, SelectorMaxHeight)
	return fl
}

// WithStyles updates the styles and re-applies them to the filter input.
func (fl FilteredListCore) WithStyles(styles *Styles) FilteredListCore {
	fl.Styles = styles
	return fl.updateFilterInputStyles()
}

// Set list focus state for visual styling.
func (fl FilteredListCore) WithFocus(hasFocus bool) FilteredListCore {
	fl.HasFocus = hasFocus
	return fl.updateFilterInputStyles()
}

// CursorPosition returns the screen position of the filter input's real
// terminal cursor for an overlay rendered via renderOverlay with the given
// box content. ok is false when the list is closed, the app is unfocused,
// or the list (not the filter) has focus — the real cursor should be hidden
// in those states.
//
// Box layout: title (line 0), filter box top border (line 1), filter input
// content (line 2); content starts after left border + left padding (2 cells).
func (fl FilteredListCore) CursorPosition(box string, screenWidth, screenHeight int) (x, y int, ok bool) {
	if fl.State == FilteredListClosed || !fl.HasFocus || !fl.FilterInputFocused {
		return 0, 0, false
	}
	x0, y0 := overlayOrigin(box, screenWidth, screenHeight)
	_, cell := fl.FilterInput.CursorCell()
	x = x0 + 2 + ansi.StringWidth(fl.FilterInput.Prompt) + cell
	y = y0 + 2
	return x, y, true
}

// updateFilterInputStyles applies current styles to the filter input.
func (fl FilteredListCore) updateFilterInputStyles() FilteredListCore {
	fl.FilterInput = fl.FilterInput.WithStyles(
		inputFieldStyle{
			Prompt:      fl.Styles.Input.Foreground(fl.Styles.BorderFocused),
			Text:        fl.Styles.Text,
			Placeholder: fl.Styles.System,
		},
		inputFieldStyle{
			Prompt:      fl.Styles.Input.Foreground(fl.Styles.BorderBlurred),
			Text:        fl.Styles.System,
			Placeholder: fl.Styles.System,
		},
	)
	return fl
}

// HandleTabKey toggles focus between the filter input and the list.
func (fl FilteredListCore) HandleTabKey() FilteredListCore {
	fl.FilterInputFocused = !fl.FilterInputFocused
	if fl.FilterInputFocused {
		fl.FilterInput = fl.FilterInput.Focus()
	} else {
		fl.FilterInput = fl.FilterInput.Blur()
	}
	return fl.updateFilterInputStyles()
}

// HandleFilterCtrlC clears the filter input value.
func (fl FilteredListCore) HandleFilterCtrlC() FilteredListCore {
	fl.FilterInput = fl.FilterInput.WithValue("")
	return fl
}

// FilteredListResult describes what happened during HandleKey.
type FilteredListResult struct {
	// Handled is true if the key was consumed.
	Handled bool
	// FilterChanged is true if the filter value changed.
	FilterChanged bool
	// Cmd is a tea.Cmd from the inner InputField (cursor blink, etc.).
	Cmd tea.Cmd
}

// HandleKey processes a key event for the filtered list.
func (fl FilteredListCore) HandleKey(msg tea.KeyMsg) (FilteredListCore, FilteredListResult) {
	keyStr := msg.String()

	if keyStr == keyTab {
		return fl.HandleTabKey(), FilteredListResult{Handled: true}
	}

	if fl.FilterInputFocused {
		return fl.handleFilterFocusedKey(msg, keyStr)
	}

	return fl.handleListFocusedKey(keyStr)
}

// handleFilterFocusedKey handles keys when the filter input is focused.
func (fl FilteredListCore) handleFilterFocusedKey(msg tea.KeyMsg, key string) (FilteredListCore, FilteredListResult) {
	if key == keyEsc {
		fl.State = FilteredListClosed
		return fl, FilteredListResult{Handled: true}
	}

	if key == keyCtrlC {
		fl = fl.HandleFilterCtrlC()
		return fl, FilteredListResult{Handled: true, FilterChanged: true}
	}

	if key == keyCtrlU || key == keyCtrlD {
		return fl, FilteredListResult{Handled: true}
	}

	oldValue := fl.FilterInput.Value()
	var cmd tea.Cmd
	fl.FilterInput, cmd = fl.FilterInput.Update(msg)
	return fl, FilteredListResult{Handled: true, FilterChanged: oldValue != fl.FilterInput.Value(), Cmd: cmd}
}

// handleListFocusedKey handles keys when the list is focused.
func (fl FilteredListCore) handleListFocusedKey(key string) (FilteredListCore, FilteredListResult) {
	switch key {
	case keyQ, keyEsc:
		fl.State = FilteredListClosed
		return fl, FilteredListResult{Handled: true}
	case keyJ, keyDown:
		return fl, FilteredListResult{Handled: true}
	case keyK, keyUp:
		return fl, FilteredListResult{Handled: true}
	case keyEnter:
		return fl, FilteredListResult{Handled: true}
	}
	return fl, FilteredListResult{}
}

// ClampSelection clamps the selected index to valid bounds.
func (fl FilteredListCore) ClampSelection(filteredLen int) FilteredListCore {
	if filteredLen == 0 {
		fl.SelectedIdx = 0
		fl.ScrollIdx = 0
		return fl
	}
	if fl.SelectedIdx >= filteredLen {
		fl.SelectedIdx = filteredLen - 1
	}
	return fl
}

// EnsureVisible adjusts ScrollIdx so the selected item is visible.
func (fl FilteredListCore) EnsureVisible() FilteredListCore {
	listHeight := SelectorListRows
	if fl.SelectedIdx < fl.ScrollIdx {
		fl.ScrollIdx = fl.SelectedIdx
	} else if fl.SelectedIdx >= fl.ScrollIdx+listHeight {
		fl.ScrollIdx = fl.SelectedIdx - listHeight + 1
	}
	return fl
}

// ClampScroll ensures ScrollIdx is valid for the given filtered length.
func (fl FilteredListCore) ClampScroll(filteredLen int) FilteredListCore {
	maxScroll := max(0, filteredLen-SelectorListRows)
	if fl.ScrollIdx > maxScroll {
		fl.ScrollIdx = maxScroll
	}
	if fl.ScrollIdx < 0 {
		fl.ScrollIdx = 0
	}
	return fl
}

// RenderOverlay renders the filtered list as an overlay on top of base content.
func (fl FilteredListCore) RenderOverlay(baseContent, renderedList string, screenWidth, screenHeight int) string {
	if fl.State == FilteredListClosed {
		return baseContent
	}
	return renderOverlay(baseContent, renderedList, screenWidth, screenHeight, 0)
}

// FilterBorderColor returns the border color for the filter input based on focus state.
func (fl FilteredListCore) FilterBorderColor() color.Color {
	if !fl.HasFocus || !fl.FilterInputFocused {
		return fl.Styles.BorderBlurred
	}
	return fl.Styles.BorderFocused
}

// ListBorderColor returns the border color for the list based on focus state.
func (fl FilteredListCore) ListBorderColor() color.Color {
	if !fl.HasFocus || fl.FilterInputFocused {
		return fl.Styles.BorderBlurred
	}
	return fl.Styles.BorderFocused
}
