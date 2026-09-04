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
	"unicode/utf8"
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
	aw = aw.withListedDirValue()
	return aw.loadDir(aw.currentDir)
}

// withListedDirValue writes the directory being listed into the input, with the
// separator that opens the segment the user is about to type.
func (aw AttachmentWindow) withListedDirValue() AttachmentWindow {
	aw.FilterInput = aw.FilterInput.WithValue(dirValue(aw.currentDir))
	return aw
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

//nolint:gocyclo // key dispatch over local/URL/autocomplete modes; each case is simple
func (aw AttachmentWindow) updateForKeyMsg(msg KeyMsg) (AttachmentWindow, Cmd) {
	if aw.State == FilteredListClosed {
		return aw, nil
	}

	key := msg.String()

	if key == keyCtrlA {
		return aw.toggleMode(), nil
	}

	// Ctrl+W deletes a whole path segment; plain Backspace stays the
	// single-character delete every other box has. A control byte is what the
	// chord has to be: shift+backspace is a plain backspace on most terminals,
	// ctrl+backspace arrives as ctrl+h (the help window), and the CSI-u forms
	// that could carry either are ones key_parser.go does not read — see
	// docs/tui.md, "Why Shift+Enter is not the line break". This program binds
	// no Alt chords, so ESC-prefixed keys are not an option either. Local mode
	// only, and only while the path input has focus: URL text and the list
	// itself take the normal keys.
	if key == keyCtrlW && aw.mode == modeLocal && aw.FilterInputFocused {
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
		aw = aw.withListedDirValue()
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
	// currentDir is the directory on screen, so the chosen entry is appended to
	// it rather than to whatever partial text is in the box.
	aw.FilterInput = aw.FilterInput.WithValue(dirValue(filepath.Join(aw.currentDir, dirName)))
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

// navigateByPath makes the directory named by the input the one being listed,
// and returns whatever is left to filter it with. The split is filepath's own,
// so a Windows path typed with either separator and a POSIX path behave the
// same way. Text that names no place — and a directory that does not exist —
// stays a filter term for the listing already on screen.
func (aw AttachmentWindow) navigateByPath(search string) (AttachmentWindow, string) {
	dir, filter := filepath.Split(search)
	if !isRooted(dir) {
		return aw, search
	}

	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return aw, search
	}

	// Clean, so currentDir is always in the shape dirValue expects: no trailing
	// separator unless it is a root, ".." resolved lexically.
	aw.currentDir = filepath.Clean(dir)
	aw = aw.loadDir(aw.currentDir)
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

// pathSeps are the characters that separate path segments in the picker's
// input. '/' separates on every platform — Windows accepts it alongside its own
// '\' — while a backslash counts only where the operating system is the one
// that produces it, because on Unix it is an ordinary character inside a file
// name. The platform half is filepath's own constant, so the segmentation here
// cannot drift from the filepath calls this file also makes.
const pathSeps = "/" + string(filepath.Separator)

// isPathSep reports whether a byte of a path or a rune of the input's decoded
// text separates segments. Accepting both keeps one definition of the rule; the
// conversion is safe in each direction because both separators are ASCII.
func isPathSep[T byte | rune](c T) bool {
	return strings.ContainsRune(pathSeps, rune(c))
}

// isRooted reports whether a path names a place to look, rather than a term to
// match against the directory already being listed: it begins with a separator
// ("/usr", and on Windows the "\Users" of the current drive), or carries a
// volume that a separator follows (`C:\Users`, a UNC `\\server\share`).
//
// This is filepath.IsAbs plus one case: on Windows IsAbs answers false for a
// path that is rooted on the current drive, and the picker has to accept what
// os.Stat accepts. It deliberately excludes the drive-relative form ("C:file"),
// which names no directory this program could list.
func isRooted(path string) bool {
	if path == "" {
		return false
	}
	if isPathSep(path[0]) {
		return true
	}
	vol := filepath.VolumeName(path)
	return len(path) > len(vol) && isPathSep(path[len(vol)])
}

// dirValue renders a directory as the picker's input value: the directory plus
// the separator that opens the segment about to be typed. Roots that already
// carry their own separator — which is what filepath.Clean leaves "/" and `C:\`
// as — are left alone, so no "//" or `C:\\` is ever put in the box.
func dirValue(dir string) string {
	if dir == "" || isPathSep(dir[len(dir)-1]) {
		return dir
	}
	return dir + string(filepath.Separator)
}

// rootEnd is where the root of value ends, counted in runes: the volume and the
// separators after it (`C:` → `C:\`), or a leading separator where no volume
// owns the path. A segment delete never reaches before it, so the box always
// keeps a directory to list. Text with no root at all — a relative path, which
// the picker only holds if os.Getwd failed — has an end of 0 and deletes down to
// its start, as it always did.
func rootEnd(value string) int {
	n := len(filepath.VolumeName(value))
	for n < len(value) && isPathSep(value[n]) {
		n++
	}
	return utf8.RuneCountInString(value[:n])
}

// deletePathSegment deletes the path segment before the cursor, back to the
// separator that starts it, and never past the root of the path. The character
// immediately before the cursor is skipped when looking for that separator, so
// a trailing separator goes with the segment in front of it:
//
//	"/abc/def/"   (cursor at end)  →  "/abc/"
//	"/abc/def"    (cursor at end)  →  "/abc/"
//	"/abc/"       (cursor at end)  →  "/"
//	"abc/def/"    (cursor at end)  →  "abc/"
//	`C:\abc\def\` (cursor at end)  →  `C:\abc\`    (Windows)
//	"/"           (cursor at end)  →  "/"          (rootEnd protects the root)
func (aw AttachmentWindow) deletePathSegment() AttachmentWindow {
	val := aw.FilterInput.Value()
	runes := []rune(val)
	pos := aw.FilterInput.CursorPos()

	// At or before the end of the root there is no segment left to delete.
	floor := rootEnd(val)
	if pos <= floor {
		return aw
	}

	// Delete back to just after the separator that starts the segment; where
	// the segment begins at the root itself, rootEnd is already that index.
	deleteFrom := floor
	for i := pos - 2; i >= floor; i-- {
		if isPathSep(runes[i]) {
			deleteFrom = i + 1
			break
		}
	}

	aw.FilterInput = aw.FilterInput.WithValue(string(runes[:deleteFrom]) + string(runes[pos:]))
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
	// One line, one box width: the bar is truncated to the overlay's width, so a
	// hint that does not fit is a hint nobody sees. Each state lists only what
	// that state can do — which is also why the two paths through local mode
	// differ. TestOverlayHelpBarsFit pins the budget; see that test before
	// adding a fifth hint.
	switch {
	case aw.mode == modeURL:
		help = "  enter: add URL │ ctrl+a: switch to local │ esc: close"
	case aw.FilterInputFocused:
		help = "  tab: list │ enter: pick │ ctrl+w: up a level │ ctrl+a: url"
	default:
		help = "  tab: search │ j/k: navigate │ enter: pick │ esc: close"
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

		truncated := truncateWithSuffix(name, max(1, innerWidth))
		// Rows are flush left like every other overlay list — no "> "
		// marker, no indent. Selected row is bold.
		if isSelected {
			content.WriteString(NewStyle().Bold(true).Render(truncated))
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
