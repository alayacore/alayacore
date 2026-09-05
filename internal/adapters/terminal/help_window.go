package terminal

// HelpWindow manages a help overlay that displays keybindings and commands.
// It provides a filter input to search items, following the same pattern
// as ModelSelector.

import (
	"fmt"
	"strings"
)

// HelpItemType classifies a HelpItem as either a command or a key binding.
type HelpItemType int

const (
	HelpItemKey HelpItemType = iota
	HelpItemCommand
)

// HelpItem represents a single help entry with a key and description.
type HelpItem struct {
	ID          int
	Key         string
	Description string
	IsSection   bool
	Type        HelpItemType
	searchStr   string
}

// HelpWindow manages a help overlay that displays keybindings and commands.
// HelpWindow is an overlay showing keybindings and commands.
//
// All fields are Elm UI state (value types, copied on every WithXxx).
// No external dependencies — help items are built from static definitions.
type HelpWindow struct {
	FilteredListCore

	items          []HelpItem
	filteredItems  []HelpItem
	keyColumnWidth int
}

// NewHelpWindow creates a new help window.
func NewHelpWindow(styles *Styles) HelpWindow {
	input := newFilterInput("Filter command or key...")
	hw := HelpWindow{
		items: buildHelpItems(),
	}
	hw.Width = 72
	hw.Height = 20
	hw.HasFocus = true
	hw.FilterInput = input
	hw.lastFilterValue = "\x00"
	hw.Styles = styles
	hw = hw.recalculateColumnWidths()
	hw = hw.updateFilteredItems()
	return hw
}

func buildHelpItems() []HelpItem {
	id := 0
	nextID := func() int {
		id++
		return id
	}
	items := []HelpItem{
		{ID: nextID(), IsSection: true, Description: "Commands"},
		{ID: nextID(), Key: ":mcp_confirm <server> <code> <redirect_uri>", Description: "Confirm MCP OAuth authorization", Type: HelpItemCommand},
		{ID: nextID(), Key: ":mcp_decline <server>", Description: "Decline MCP OAuth authorization", Type: HelpItemCommand},
		{ID: nextID(), Key: ":mcp_cancel", Description: "Cancel MCP initialization", Type: HelpItemCommand},
		{ID: nextID(), Key: ":tool_confirm <id>", Description: "Confirm pending tool execution", Type: HelpItemCommand},
		{ID: nextID(), Key: ":tool_decline <id>", Description: "Decline pending tool execution", Type: HelpItemCommand},
		{ID: nextID(), Key: ":continue", Description: "Retry last prompt", Type: HelpItemCommand},
		{ID: nextID(), Key: ":reason <0|1|2>", Description: "Set reasoning level", Type: HelpItemCommand},
		{ID: nextID(), Key: ":cancel", Description: "Cancel current task", Type: HelpItemCommand},
		{ID: nextID(), Key: ":summarize", Description: "Summarize/Compress history", Type: HelpItemCommand},
		{ID: nextID(), Key: ":theme_set <name>", Description: "Switch theme by name", Type: HelpItemCommand},
		{ID: nextID(), Key: ":model_set <id>", Description: "Switch model by ID", Type: HelpItemCommand},
		{ID: nextID(), Key: ":model_load", Description: "Reload model config", Type: HelpItemCommand},
		{ID: nextID(), Key: ":model_sync", Description: "Apply edited model config", Type: HelpItemCommand},
		{ID: nextID(), Key: ":save [filename]", Description: "Save session", Type: HelpItemCommand},
		{ID: nextID(), Key: ":fork <id> <filename>", Description: "Fork session up to content", Type: HelpItemCommand},
		{ID: nextID(), Key: ":video_config <fps> <0|1>", Description: "Set video FPS & resolution", Type: HelpItemCommand},
		{ID: nextID(), Key: ":suspend", Description: "Suspend process", Type: HelpItemCommand},
		{ID: nextID(), Key: ":quit", Description: "Exit application", Type: HelpItemCommand},
		{ID: nextID(), Key: ":help", Description: "Open help window", Type: HelpItemCommand},
		{ID: nextID(), IsSection: true, Description: "Global Shortcuts"},
		{ID: nextID(), Key: "Tab", Description: "Toggle focus display/input", Type: HelpItemKey},
		{ID: nextID(), Key: "Enter", Description: "Submit prompt or command", Type: HelpItemKey},
		{ID: nextID(), Key: "Ctrl+H", Description: "Open help window", Type: HelpItemKey},
		{ID: nextID(), Key: "Ctrl+G", Description: "Cancel current task", Type: HelpItemKey},
		{ID: nextID(), Key: "Ctrl+C", Description: "Clear text", Type: HelpItemKey},
		{ID: nextID(), Key: "Ctrl+S", Description: "Save session", Type: HelpItemKey},
		{ID: nextID(), Key: "Ctrl+O", Description: "Open prompt input in editor", Type: HelpItemKey},
		{ID: nextID(), Key: "Ctrl+A", Description: "Open attachment picker", Type: HelpItemKey},
		{ID: nextID(), Key: "Ctrl+L", Description: "Open model selector", Type: HelpItemKey},
		{ID: nextID(), Key: "Ctrl+R", Description: "Force redraw screen", Type: HelpItemKey},
		{ID: nextID(), Key: "Ctrl+P", Description: "Open theme selector", Type: HelpItemKey},
		{ID: nextID(), Key: "Ctrl+Z", Description: "Suspend process", Type: HelpItemKey},
		{ID: nextID(), IsSection: true, Description: "Display Mode"},
		{ID: nextID(), Key: "j/k", Description: "Move window cursor", Type: HelpItemKey},
		{ID: nextID(), Key: "J/K", Description: "Scroll one line", Type: HelpItemKey},
		{ID: nextID(), Key: "Ctrl+D/U", Description: "Scroll half screen", Type: HelpItemKey},
		{ID: nextID(), Key: "g", Description: "Go to first window", Type: HelpItemKey},
		{ID: nextID(), Key: "G", Description: "Follow the last window", Type: HelpItemKey},
		{ID: nextID(), Key: "H/L/M", Description: "Cursor top/btm/mid", Type: HelpItemKey},
		{ID: nextID(), Key: "e", Description: "Open in editor", Type: HelpItemKey},
		{ID: nextID(), Key: "f/b", Description: "Next/prev prompt", Type: HelpItemKey},
		{ID: nextID(), Key: ":", Description: "Enter command mode", Type: HelpItemKey},
		{ID: nextID(), Key: "Space", Description: "Toggle window fold", Type: HelpItemKey},
		{ID: nextID(), Key: "r", Description: "Toggle markdown rendering", Type: HelpItemKey},
		{ID: nextID(), Key: "Ctrl+F", Description: "Fork session from cursor", Type: HelpItemKey},
	}
	for i := range items {
		if !items[i].IsSection {
			items[i].searchStr = strings.ToLower(items[i].Key + " " + items[i].Description)
		}
	}
	return items
}

func (hw HelpWindow) recalculateColumnWidths() HelpWindow {
	maxKeyLen := 0
	maxDescLen := 0
	for _, item := range hw.items {
		if !item.IsSection {
			if w := Width(item.Key); w > maxKeyLen {
				maxKeyLen = w
			}
			if w := Width(item.Description); w > maxDescLen {
				maxDescLen = w
			}
		}
	}
	innerWidth := max(0, hw.Width)
	idealKeyWidth := innerWidth - 1 - maxDescLen
	hw.keyColumnWidth = min(
		max(maxKeyLen, idealKeyWidth),
		max(1, innerWidth-1),
	)
	return hw
}

func (hw HelpWindow) WithSize(width, height int) HelpWindow {
	hw.FilteredListCore = hw.FilteredListCore.WithSize(width, height)
	return hw.recalculateColumnWidths()
}

func (hw HelpWindow) WithStyles(styles *Styles) HelpWindow {
	hw.FilteredListCore = hw.FilteredListCore.WithStyles(styles)
	return hw.recalculateColumnWidths()
}

func (hw HelpWindow) WithFocus(focused bool) HelpWindow {
	hw.FilteredListCore = hw.FilteredListCore.WithFocus(focused)
	return hw
}

func (hw HelpWindow) Open() HelpWindow {
	hw.State = FilteredListOpen
	hw.FilterInput = hw.FilterInput.WithValue("")
	hw.lastFilterValue = "\x00"
	hw.FilterInputFocused = true
	hw.FilterInput = hw.FilterInput.Focus()
	hw.FilteredListCore = hw.FilteredListCore.updateFilterInputStyles()
	hw.ScrollIdx = 0
	hw = hw.updateFilteredItems()
	hw.SelectedIdx = hw.firstSelectableIdx()
	hw.ScrollIdx = 0
	return hw
}

func (hw HelpWindow) Close() HelpWindow {
	hw.FilteredListCore = hw.FilteredListCore.Close()
	return hw
}

func (hw HelpWindow) updateFilteredItems() HelpWindow {
	filter := strings.ToLower(hw.FilterInput.Value())
	if filter == hw.lastFilterValue {
		return hw
	}
	hw.lastFilterValue = filter

	if filter == "" {
		hw.filteredItems = hw.items
	} else {
		var result []HelpItem
		var currentSection []HelpItem
		var sectionHeader *HelpItem

		flushSection := func() {
			if sectionHeader != nil && len(currentSection) > 0 {
				result = append(result, *sectionHeader)
				result = append(result, currentSection...)
			}
			sectionHeader = nil
			currentSection = nil
		}

		for _, item := range hw.items {
			if item.IsSection {
				flushSection()
				h := item
				sectionHeader = &h
				continue
			}
			if FuzzyMatch(filter, item.searchStr) {
				currentSection = append(currentSection, item)
			}
		}
		flushSection()
		hw.filteredItems = result
	}

	hw.SelectedIdx = 0
	hw = hw.clampSelection()
	return hw
}

func (hw HelpWindow) filteredLen() int {
	return len(hw.filteredItems)
}

//nolint:gocyclo // key dispatch over filter/list/focus states; each case is simple
func (hw HelpWindow) Update(msg Msg) (HelpWindow, Cmd) {
	keyMsg, ok := msg.(KeyMsg)
	if !ok {
		return hw, nil
	}
	key := keyMsg.String()

	fl, result := hw.FilteredListCore.HandleKey(keyMsg)
	hw.FilteredListCore = fl

	// Check if Enter was pressed on a command item
	var pendingCmd string
	if key == keyEnter && result.Handled && !fl.FilterInputFocused {
		if hw.SelectedIdx >= 0 && hw.SelectedIdx < hw.filteredLen() {
			item := hw.filteredItems[hw.SelectedIdx]
			if !item.IsSection && item.Type == HelpItemCommand {
				parts := strings.Fields(item.Key)
				if len(parts) > 0 {
					pendingCmd = parts[0]
				} else {
					pendingCmd = item.Key
				}
			}
		}
	}

	// When search is focused, Enter selects the first filtered command
	if key == keyEnter && result.Handled && fl.FilterInputFocused && hw.filteredLen() > 0 {
		for _, item := range hw.filteredItems {
			if !item.IsSection && item.Type == HelpItemCommand {
				parts := strings.Fields(item.Key)
				if len(parts) > 0 {
					pendingCmd = parts[0]
				} else {
					pendingCmd = item.Key
				}
				break
			}
		}
	}

	if pendingCmd != "" {
		fl = fl.Close()
	}
	hw.FilteredListCore = fl

	if result.Handled {
		if result.FilterChanged {
			hw = hw.updateFilteredItems()
		}
		if !hw.FilterInputFocused {
			switch {
			case key == keyJ || key == keyDown:
				hw = hw.moveDown()
			case key == keyK || key == keyUp:
				hw = hw.moveUp()
			}
		}
		if pendingCmd != "" {
			return hw, func() Msg { return HelpCmdMsg{Command: pendingCmd} }
		}
		return hw, result.Cmd
	}
	return hw, nil
}

func (hw HelpWindow) moveDown() HelpWindow {
	for i := hw.SelectedIdx + 1; i < hw.filteredLen(); i++ {
		if !hw.filteredItems[i].IsSection {
			hw.SelectedIdx = i
			return hw.ensureVisible()
		}
	}
	return hw
}

func (hw HelpWindow) moveUp() HelpWindow {
	for i := hw.SelectedIdx - 1; i >= 0; i-- {
		if !hw.filteredItems[i].IsSection {
			hw.SelectedIdx = i
			return hw.ensureVisible()
		}
	}
	return hw
}

func (hw HelpWindow) firstSelectableIdx() int {
	for i, item := range hw.filteredItems {
		if !item.IsSection {
			return i
		}
	}
	return 0
}

func (hw HelpWindow) skipSectionHeaders() HelpWindow {
	if hw.SelectedIdx < 0 || hw.SelectedIdx >= hw.filteredLen() {
		return hw
	}
	if !hw.filteredItems[hw.SelectedIdx].IsSection {
		return hw
	}
	for i := hw.SelectedIdx; i < hw.filteredLen(); i++ {
		if !hw.filteredItems[i].IsSection {
			hw.SelectedIdx = i
			return hw
		}
	}
	for i := hw.SelectedIdx - 1; i >= 0; i-- {
		if !hw.filteredItems[i].IsSection {
			hw.SelectedIdx = i
			return hw
		}
	}
	return hw
}

func (hw HelpWindow) clampSelection() HelpWindow {
	hw.FilteredListCore = hw.FilteredListCore.ClampSelection(hw.filteredLen())
	hw = hw.skipSectionHeaders()
	return hw.ensureVisible()
}

func (hw HelpWindow) ensureVisible() HelpWindow {
	listHeight := SelectorListRows
	if hw.SelectedIdx <= hw.ScrollIdx {
		hw.ScrollIdx = max(0, hw.SelectedIdx-1)
	} else if hw.SelectedIdx >= hw.ScrollIdx+listHeight {
		hw.ScrollIdx = hw.SelectedIdx - listHeight + 1
	}
	return hw
}

func (hw HelpWindow) View() View {
	if hw.State == FilteredListClosed {
		return NewView("")
	}

	listHeight := SelectorListRows
	hw.FilteredListCore = hw.FilteredListCore.ClampScroll(hw.filteredLen())

	filterBox := hw.Styles.RenderOpenBox(hw.FilterInput.View(), hw.Width, hw.FilterBorderColor())

	var lines []string
	if hw.filteredLen() == 0 {
		lines = append(lines, hw.Styles.System.Render("No matching commands or keys."))
	} else {
		endIdx := min(hw.ScrollIdx+listHeight, hw.filteredLen())
		for i := hw.ScrollIdx; i < endIdx; i++ {
			lines = append(lines, hw.renderItem(hw.filteredItems[i], i == hw.SelectedIdx))
		}
	}

	for len(lines) < listHeight {
		lines = append(lines, "")
	}

	listBorderColor := hw.ListBorderColor()
	content := strings.Join(lines, "\n")
	listBox := hw.Styles.RenderOpenBox(content, hw.Width, listBorderColor, listHeight)

	titleStyle := NewStyle().Background(hw.Styles.ColorDim).Foreground(hw.Styles.ColorAccent).Bold(true)
	title := titleStyle.Render(fmt.Sprintf("%-*s", hw.Width, "Help"))

	helpStyle := NewStyle().Background(hw.Styles.ColorDim).Foreground(hw.Styles.ColorMuted)
	var help string
	if hw.FilterInputFocused {
		help = "  tab: list | esc: close"
	} else {
		base := "tab: filter | j/k: navigate"
		if hw.SelectedIdx >= 0 && hw.SelectedIdx < hw.filteredLen() &&
			hw.filteredItems[hw.SelectedIdx].Type == HelpItemCommand {
			base += " | enter: copy to input"
		}
		base += " | q/esc: close"
		help = "  " + base
	}
	helpBar := renderHelpBar(helpStyle, help, hw.Width)

	countStr := fmt.Sprintf("%d items", hw.filteredLen())
	infoLine := hw.Styles.System.Render(countStr)

	return NewView(title + "\n" + filterBox + "\n" + infoLine + "\n" + listBox + "\n" + helpBar)
}

func (hw HelpWindow) renderItem(item HelpItem, selected bool) string {
	innerWidth := max(0, hw.Width)

	if item.IsSection {
		content := "── " + item.Description
		content = truncateWithSuffix(content, innerWidth)
		return hw.Styles.System.Bold(true).Render(content)
	}

	keyMaxWidth := hw.keyColumnWidth
	descMaxWidth := max(0, innerWidth-1-keyMaxWidth)

	key := item.Key
	if keyMaxWidth > 0 {
		key = truncateWithSuffix(key, keyMaxWidth)
	}

	desc := item.Description
	if descMaxWidth > 0 {
		desc = truncateWithSuffix(desc, descMaxWidth)
	}

	padding := max(0, keyMaxWidth-Width(key))
	line := key + strings.Repeat(" ", padding)
	if descMaxWidth > 0 {
		line += " " + desc
	}

	if selected {
		// Selected row is bold; rows are flush left with no indent.
		return NewStyle().Bold(true).Render(line)
	}
	return hw.Styles.System.Render(line)
}

func (hw HelpWindow) RenderOverlay(baseContent string, screenWidth, screenHeight int) string {
	if hw.State == FilteredListClosed {
		return baseContent
	}
	return renderOverlay(baseContent, hw.View().Content, screenWidth, screenHeight, 0)
}

// CursorPosition returns the screen position of the filter input's real
// terminal cursor when the overlay is open and the filter has focus.
func (hw HelpWindow) CursorPosition(screenWidth, screenHeight int) (x, y int, ok bool) {
	return hw.FilteredListCore.CursorPosition(hw.View().Content, screenWidth, screenHeight)
}
