package terminal

// AttachmentWindow is a file picker overlay for adding attachments to user input.
// It provides two modes:
//   - Local mode:  path input field with a filtered file list, similar to ModelSelector.
//   - URL mode:    URL input field for adding remote attachments.
// Users can toggle between modes with Ctrl+A.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type attachmentMode int

const (
	modeLocal attachmentMode = iota
	modeURL
)

// AttachmentWindow is an overlay for selecting file attachments.
//
// All fields are Elm UI state (value types, copied on every WithXxx).
// File system reads happen synchronously in the constructor or on directory
// navigation, not in Update.
type AttachmentWindow struct {
	FilteredListCore

	entries    []fileEntry
	filtered   []fileEntry
	currentDir string
	mode       attachmentMode

	// savedLocalPath preserves the local-mode input value when switching to URL
	// mode, so it can be restored when switching back.
	savedLocalPath string
	// savedURLPath preserves the URL-mode input value when switching to local
	// mode, so it can be restored when switching back.
	savedURLPath string

	// selectedPath stores the path selected by the user when Enter is pressed.
	// It is set by handleEnter/handleSearchEnter/handleURLEntry before closing
	// the window, and read by handleSelectorOverlayKeys to add the attachment
	// using the current *Terminal (avoiding stale closure captures).
	selectedPath string
}

type fileEntry struct {
	name      string
	nameLower string
	isDir     bool
}

func NewAttachmentWindow(styles *Styles) AttachmentWindow {
	input := newFilterInput("Search files...")
	aw := AttachmentWindow{
		entries:  []fileEntry{},
		filtered: []fileEntry{},
	}
	aw.Width = 60
	aw.Height = 20
	aw.HasFocus = true
	aw.FilterInput = input
	aw.lastFilterValue = "\x00"
	aw.Styles = styles
	return aw
}

func (aw AttachmentWindow) WithSize(width, height int) AttachmentWindow {
	aw.FilteredListCore = aw.FilteredListCore.WithSize(width, height)
	return aw
}

func (aw AttachmentWindow) WithStyles(styles *Styles) AttachmentWindow {
	aw.FilteredListCore = aw.FilteredListCore.WithStyles(styles)
	return aw
}

func (aw AttachmentWindow) WithFocus(focused bool) AttachmentWindow {
	aw.FilteredListCore = aw.FilteredListCore.WithFocus(focused)
	return aw
}

// SelectedPath returns the path that was selected when Enter was pressed,
// or empty string if no selection was made. Used by handleSelectorOverlayKeys
// to add the attachment to the current Terminal.
func (aw AttachmentWindow) SelectedPath() string { return aw.selectedPath }

func (aw AttachmentWindow) Open() AttachmentWindow {
	aw.State = FilteredListOpen
	aw.mode = modeLocal
	aw.lastFilterValue = "\x00"
	aw.FilterInputFocused = true
	aw.FilterInput = aw.FilterInput.Focus()
	aw.FilteredListCore = aw.FilteredListCore.updateFilterInputStyles()
	aw.ScrollIdx = 0
	aw.SelectedIdx = 0
	aw.selectedPath = ""
	aw.currentDir, _ = os.Getwd()
	if aw.currentDir == "/" {
		aw.FilterInput = aw.FilterInput.WithValue("/")
	} else {
		aw.FilterInput = aw.FilterInput.WithValue(aw.currentDir + "/")
	}
	return aw.loadDir(aw.currentDir)
}

func (aw AttachmentWindow) loadDir(dir string) AttachmentWindow {
	aw.entries = aw.readDir(dir)
	aw.filtered = make([]fileEntry, len(aw.entries))
	copy(aw.filtered, aw.entries)
	aw.SelectedIdx = 0
	aw.ScrollIdx = 0
	aw.FilteredListCore = aw.FilteredListCore.ClampSelection(len(aw.filtered))
	aw.FilteredListCore = aw.FilteredListCore.EnsureVisible()
	return aw
}

func (aw AttachmentWindow) readDir(dir string) []fileEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	result := []fileEntry{}
	for _, e := range entries {
		name := e.Name()
		result = append(result, fileEntry{
			name:      name,
			nameLower: strings.ToLower(name),
			isDir:     isDirEntry(dir, e),
		})
	}
	return result
}

// isDirEntry returns true if e is a directory or a symlink pointing to a directory.
// os.DirEntry.IsDir() returns false for symlinks even if the target is a directory,
// so we need to follow them explicitly.
func isDirEntry(dir string, e os.DirEntry) bool {
	if e.IsDir() {
		return true
	}
	if e.Type()&os.ModeSymlink != 0 {
		info, err := os.Stat(filepath.Join(dir, e.Name()))
		if err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

// Update handles all messages for the attachment window: key events and paste.
func (aw AttachmentWindow) Update(msg Msg) (AttachmentWindow, Cmd) {
	switch msg := msg.(type) {
	case KeyMsg:
		return aw.updateForKeyMsg(msg)
	case PasteMsg:
		if !aw.FilterInputFocused {
			return aw, nil
		}
		aw.FilterInput, _ = aw.FilterInput.Update(msg)
		if aw.mode == modeLocal {
			aw = aw.updateFiltered()
		}
		return aw, nil
	}
	return aw, nil
}

//nolint:gocyclo
func (aw AttachmentWindow) updateForKeyMsg(msg KeyMsg) (AttachmentWindow, Cmd) {
	if aw.State == FilteredListClosed {
		return aw, nil
	}

	key := msg.String()

	if key == keyCtrlA {
		return aw.toggleMode(), nil
	}

	// Backspace segment deletion: in local mode when filter input is focused,
	// delete back to the previous path separator instead of a single character.
	if key == "backspace" && aw.mode == modeLocal && aw.FilterInputFocused {
		aw = aw.deletePathSegment()
		aw = aw.updateFiltered()
		return aw, nil
	}

	// Ctrl+C: do nothing in local mode (neither clear input nor reset directory).
	if key == keyCtrlC && aw.mode == modeLocal && aw.FilterInputFocused {
		return aw, nil
	}

	if aw.mode == modeURL && key == keyEnter {
		aw = aw.handleURLEntry()
		if aw.selectedPath != "" {
			path := aw.selectedPath
			return aw, func() Msg { return AttachmentSelectedMsg{Path: path} }
		}
		return aw, nil
	}

	inputWasFocused := aw.FilterInputFocused

	fl, result := aw.FilteredListCore.HandleKey(msg)
	aw.FilteredListCore = fl

	// Handle Enter selection in the list after HandleKey returns.
	if key == keyEnter && result.Handled && !fl.FilterInputFocused {
		aw = aw.handleEnter()
		fl = aw.FilteredListCore
	} else if key == keyEsc && result.Handled {
		fl = fl.Close()
	}
	aw.FilteredListCore = fl

	if result.Handled {
		if aw.mode == modeLocal {
			aw = aw.handleLocalModeKeys(result.FilterChanged, key, inputWasFocused)
		}
		// If a path was selected, send it as a message
		if aw.selectedPath != "" {
			path := aw.selectedPath
			return aw, func() Msg { return AttachmentSelectedMsg{Path: path} }
		}
		return aw, result.Cmd
	}

	if aw.mode == modeLocal && !aw.FilterInputFocused {
		aw = aw.handleListKeys(key)
	}

	return aw, nil
}

func (aw AttachmentWindow) handleLocalModeKeys(filterChanged bool, key string, inputWasFocused bool) AttachmentWindow {
	if filterChanged && aw.FilterInputFocused {
		aw = aw.updateFiltered()
	}
	if !aw.FilterInputFocused {
		aw = aw.handleListKeys(key)
	}
	if aw.FilterInputFocused && key == keyEnter && len(aw.filtered) > 0 && inputWasFocused {
		aw = aw.handleSearchEnter()
	}
	return aw
}

func (aw AttachmentWindow) toggleMode() AttachmentWindow {
	aw.lastFilterValue = "\x00"
	if aw.mode == modeLocal {
		return aw.switchToURL()
	}
	return aw.switchToLocal()
}

// switchToURL transitions from local mode to URL mode, saving the local path
// for later restoration and restoring any previously saved URL.
func (aw AttachmentWindow) switchToURL() AttachmentWindow {
	aw.savedLocalPath = aw.FilterInput.Value()
	aw.mode = modeURL
	savedURL := aw.savedURLPath
	aw.savedURLPath = ""
	if savedURL != "" {
		aw.FilterInput = aw.FilterInput.WithValue(savedURL)
	} else {
		aw.FilterInput = aw.FilterInput.WithValue("")
	}
	aw.FilterInput = aw.FilterInput.Focus()
	aw.FilterInputFocused = true
	aw.FilteredListCore = aw.FilteredListCore.updateFilterInputStyles()
	return aw
}

// switchToLocal transitions from URL mode to local mode, saving the URL
// for later restoration and restoring the previously saved local path.
func (aw AttachmentWindow) switchToLocal() AttachmentWindow {
	aw.savedURLPath = aw.FilterInput.Value()
	aw.mode = modeLocal
	saved := aw.savedLocalPath
	aw.savedLocalPath = ""
	if saved != "" {
		aw.FilterInput = aw.FilterInput.WithValue(saved)
	} else {
		if aw.currentDir == "/" {
			aw.FilterInput = aw.FilterInput.WithValue("/")
		} else {
			aw.FilterInput = aw.FilterInput.WithValue(aw.currentDir + "/")
		}
	}
	aw = aw.updateFiltered()
	aw.FilterInput = aw.FilterInput.Focus()
	aw.FilterInputFocused = true
	aw.FilteredListCore = aw.FilteredListCore.updateFilterInputStyles()
	return aw
}

func (aw AttachmentWindow) handleURLEntry() AttachmentWindow {
	url := strings.TrimSpace(aw.FilterInput.Value())
	if url == "" {
		return aw
	}
	aw.selectedPath = url
	aw.State = FilteredListClosed
	return aw
}

func (aw AttachmentWindow) autocompleteDir(dirName string) AttachmentWindow {
	search := aw.FilterInput.Value()
	switch {
	case strings.Contains(search, "/"):
		prefix := search[:strings.LastIndex(search, "/")+1]
		aw.FilterInput = aw.FilterInput.WithValue(prefix + dirName + "/")
	default:
		aw.FilterInput = aw.FilterInput.WithValue(dirName + "/")
	}
	aw.lastFilterValue = "\x00"
	return aw.updateFiltered()
}

func (aw AttachmentWindow) handleEnter() AttachmentWindow {
	if len(aw.filtered) == 0 || aw.SelectedIdx < 0 || aw.SelectedIdx >= len(aw.filtered) {
		return aw
	}
	entry := aw.filtered[aw.SelectedIdx]
	if entry.isDir {
		aw.selectedPath = ""
		aw.FilterInputFocused = true
		aw.FilterInput = aw.FilterInput.Focus()
		aw.FilteredListCore = aw.FilteredListCore.updateFilterInputStyles()
		aw = aw.autocompleteDir(entry.name)
		return aw
	}
	fullPath := filepath.Join(aw.currentDir, entry.name)
	aw.selectedPath = fullPath
	aw.State = FilteredListClosed
	return aw
}

func (aw AttachmentWindow) handleSearchEnter() AttachmentWindow {
	if len(aw.filtered) == 0 || aw.SelectedIdx < 0 || aw.SelectedIdx >= len(aw.filtered) {
		return aw
	}
	entry := aw.filtered[aw.SelectedIdx]
	if entry.isDir {
		aw.selectedPath = ""
		return aw.autocompleteDir(entry.name)
	}
	fullPath := filepath.Join(aw.currentDir, entry.name)
	aw.selectedPath = fullPath
	aw.State = FilteredListClosed
	return aw
}

func (aw AttachmentWindow) handleListKeys(key string) AttachmentWindow {
	switch key {
	case keyJ, keyDown:
		if aw.SelectedIdx < len(aw.filtered)-1 {
			aw.SelectedIdx++
		}
	case keyK, keyUp:
		if aw.SelectedIdx > 0 {
			aw.SelectedIdx--
		}
	}
	aw.FilteredListCore = aw.FilteredListCore.EnsureVisible()
	aw.FilteredListCore = aw.FilteredListCore.ClampScroll(len(aw.filtered))
	return aw
}

func (aw AttachmentWindow) navigateByPath(search string) (AttachmentWindow, string) {
	var absDir, filter string

	switch {
	case strings.HasPrefix(search, "/"):
		if strings.HasSuffix(search, "/") {
			absDir = search
			filter = ""
		} else {
			absDir = filepath.Dir(search)
			filter = filepath.Base(search)
		}

	default:
		return aw, search
	}

	info, err := os.Stat(absDir)
	if err != nil || !info.IsDir() {
		return aw, search
	}

	aw.currentDir = absDir
	aw = aw.loadDir(absDir)
	aw.lastFilterValue = "\x00"
	return aw, filter
}

func (aw AttachmentWindow) updateFiltered() AttachmentWindow {
	search := aw.FilterInput.Value()
	if search == aw.lastFilterValue {
		return aw
	}

	var prevSelectedIdx int
	if aw.SelectedIdx >= 0 && aw.SelectedIdx < len(aw.filtered) {
		prevSelectedIdx = aw.SelectedIdx
	}

	aw.lastFilterValue = search

	var filter string
	aw, filter = aw.navigateByPath(search)

	if filter == "" {
		aw.filtered = make([]fileEntry, len(aw.entries))
		copy(aw.filtered, aw.entries)
	} else {
		term := strings.ToLower(filter)
		aw.filtered = aw.filtered[:0]
		for _, e := range aw.entries {
			if FuzzyMatch(term, e.nameLower) {
				aw.filtered = append(aw.filtered, e)
			}
		}
	}

	if prevSelectedIdx >= 0 && prevSelectedIdx < len(aw.filtered) {
		aw.SelectedIdx = prevSelectedIdx
	} else {
		aw.SelectedIdx = 0
	}
	aw.FilteredListCore = aw.FilteredListCore.ClampSelection(len(aw.filtered))
	aw.FilteredListCore = aw.FilteredListCore.EnsureVisible()
	aw.FilteredListCore = aw.FilteredListCore.ClampScroll(len(aw.filtered))
	return aw
}

// deletePathSegment deletes the path segment before the cursor, going back to
// (but not including) the previous '/' separator. If no separator is found,
// deletes to the beginning.
//
// Examples:
//
//	"/abc/def/"  (cursor at end)  →  "/abc/"
//	"/abc/def"   (cursor at end)  →  "/abc/"
//	"/abc/"      (cursor at end)  →  "/"
//	"abc/def/"   (cursor at end)  →  "abc/"
func (aw AttachmentWindow) deletePathSegment() AttachmentWindow {
	val := aw.FilterInput.Value()
	pos := aw.FilterInput.CursorPos()

	if pos <= 0 {
		return aw
	}

	// Don't delete the root path.
	if val == "/" {
		return aw
	}

	runes := []rune(val)
	// Find the previous '/' before cursor. We start from pos-2 to skip the
	// character immediately before the cursor, ensuring we delete the entire
	// current segment (including the trailing '/' if present).
	slashIdx := -1
	for i := pos - 2; i >= 0; i-- {
		if runes[i] == '/' {
			slashIdx = i
			break
		}
	}

	var deleteFrom int
	if slashIdx >= 0 {
		// Delete from right after the '/' (keeping the separator).
		deleteFrom = slashIdx + 1
	} else {
		// No '/' found — delete everything before cursor.
		deleteFrom = 0
	}

	if deleteFrom >= pos {
		return aw
	}

	newRunes := make([]rune, 0, len(runes)-(pos-deleteFrom))
	newRunes = append(newRunes, runes[:deleteFrom]...)
	newRunes = append(newRunes, runes[pos:]...)
	aw.FilterInput = aw.FilterInput.WithValue(string(newRunes))
	aw.FilterInput = aw.FilterInput.WithCursorPos(deleteFrom)
	return aw
}

func (aw AttachmentWindow) View() View {
	if aw.State == FilteredListClosed {
		return NewView("")
	}
	return NewView(aw.render())
}

func (aw AttachmentWindow) render() string {
	var sb strings.Builder

	titleStyle := NewStyle().Background(aw.Styles.ColorDim).Foreground(aw.Styles.ColorAccent).Bold(true)
	sb.WriteString(titleStyle.Render(fmt.Sprintf("%-*s", aw.Width, "Attachments")))
	sb.WriteString("\n")

	searchBox := aw.Styles.RenderOpenBox(aw.FilterInput.View(), aw.Width, aw.FilterBorderColor())
	sb.WriteString(searchBox)
	sb.WriteString("\n")

	boxWidth := Width(searchBox)

	if aw.mode == modeURL {
		aw.renderURLBody(&sb, boxWidth)
	} else {
		aw.renderLocalBody(&sb, boxWidth)
	}

	helpStyle := NewStyle().Background(aw.Styles.ColorDim).Foreground(aw.Styles.ColorMuted)
	var help string
	switch {
	case aw.mode == modeURL:
		help = "  enter: add URL | ctrl+a: switch to local | esc: close"
	case aw.FilterInputFocused:
		help = "  tab: list | enter: select dir | ctrl+a: switch to URL | esc: close"
	default:
		help = "  tab: search | j/k: navigate | enter: add file | enter on dir: browse | ctrl+a: switch to URL | esc: close"
	}
	sb.WriteString("\n")
	sb.WriteString(renderHelpBar(helpStyle, help, boxWidth))

	return sb.String()
}

func (aw AttachmentWindow) renderURLBody(sb *strings.Builder, _ int) {
	sb.WriteString(aw.Styles.System.Render("Enter a URL to attach (e.g. https://example.com/image.jpg)"))
	// Pad to match local mode height (file list box).
	sb.WriteString(strings.Repeat("\n", 10))
}

func (aw AttachmentWindow) renderLocalBody(sb *strings.Builder, boxWidth int) {
	countStr := fmt.Sprintf("%d items", len(aw.filtered))
	sb.WriteString(aw.Styles.System.Render(countStr))
	sb.WriteString("\n")

	listBorderColor := aw.ListBorderColor()
	listHeight := SelectorListRows
	innerWidth := max(0, boxWidth)

	var content strings.Builder
	for i := aw.ScrollIdx; i < min(aw.ScrollIdx+listHeight, len(aw.filtered)); i++ {
		e := aw.filtered[i]
		isSelected := i == aw.SelectedIdx

		name := e.name
		if e.isDir {
			name += "/"
		}

		truncated := truncateWithSuffix(name, max(1, innerWidth-2))
		// Rows are flush left like every other overlay list — no "> "
		// marker, no indent. Selection is highlighted via the Text style.
		if isSelected {
			content.WriteString(aw.Styles.Text.Render(truncated))
		} else {
			content.WriteString(aw.Styles.System.Render(truncated))
		}
		if i < min(aw.ScrollIdx+listHeight, len(aw.filtered))-1 {
			content.WriteString("\n")
		}
	}

	fileBox := aw.Styles.RenderOpenBox(content.String(), boxWidth, listBorderColor, listHeight)
	sb.WriteString(fileBox)
}

func (aw AttachmentWindow) RenderOverlay(baseContent string, screenWidth, screenHeight int) string {
	if aw.State == FilteredListClosed {
		return baseContent
	}
	return renderOverlay(baseContent, aw.View().Content, screenWidth, screenHeight, 0)
}

// CursorPosition returns the screen position of the filter input's real
// terminal cursor when the overlay is open and the filter has focus.
func (aw AttachmentWindow) CursorPosition(screenWidth, screenHeight int) (x, y int, ok bool) {
	return aw.FilteredListCore.CursorPosition(aw.View().Content, screenWidth, screenHeight)
}
