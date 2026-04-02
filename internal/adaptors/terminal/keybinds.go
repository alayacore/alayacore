package terminal

// Key handling for the terminal UI.
// This file provides key constants, bindings, and the key handler.

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// ============================================================================
// Key Constants
// ============================================================================

// Key string constants (as reported by tea.KeyMsg.String())
const (
	// Navigation keys
	KeyTab   = "tab"
	KeyEnter = "enter"
	KeyEsc   = "esc"
	KeySpace = "space"
	KeyUp    = "up"
	KeyDown  = "down"
	KeyLeft  = "left"
	KeyRight = "right"

	// Letter keys
	KeyA = "a"
	KeyB = "b"
	KeyC = "c"
	KeyD = "d"
	KeyE = "e"
	KeyG = "G"
	KeyH = "h"
	KeyI = "i"
	KeyJ = "j"
	KeyK = "k"
	KeyL = "l"
	KeyM = "m"
	KeyN = "n"
	KeyO = "o"
	KeyP = "p"
	KeyQ = "q"
	KeyR = "r"
	KeyS = "s"
	KeyT = "t"
	KeyU = "u"
	KeyV = "v"
	KeyW = "w"
	KeyX = "x"
	KeyY = "y"
	KeyZ = "z"

	// Shifted arrow keys
	KeyShiftUp   = "shift+up"
	KeyShiftDown = "shift+down"

	// Shifted letter keys
	KeyShiftA = "A"
	KeyShiftH = "H"
	KeyShiftJ = "J"
	KeyShiftK = "K"
	KeyShiftL = "L"
	KeyShiftM = "M"

	// Special keys
	KeyColon = ":"
	Keyg     = "g"

	// Control keys
	KeyCtrlA = "ctrl+a"
	KeyCtrlB = "ctrl+b"
	KeyCtrlC = "ctrl+c"
	KeyCtrlD = "ctrl+d"
	KeyCtrlE = "ctrl+e"
	KeyCtrlF = "ctrl+f"
	KeyCtrlG = "ctrl+g"
	KeyCtrlH = "ctrl+h"
	KeyCtrlI = "ctrl+i"
	KeyCtrlJ = "ctrl+j"
	KeyCtrlK = "ctrl+k"
	KeyCtrlL = "ctrl+l"
	KeyCtrlM = "ctrl+m"
	KeyCtrlN = "ctrl+n"
	KeyCtrlO = "ctrl+o"
	KeyCtrlP = "ctrl+p"
	KeyCtrlQ = "ctrl+q"
	KeyCtrlR = "ctrl+r"
	KeyCtrlS = "ctrl+s"
	KeyCtrlT = "ctrl+t"
	KeyCtrlU = "ctrl+u"
	KeyCtrlV = "ctrl+v"
	KeyCtrlW = "ctrl+w"
	KeyCtrlX = "ctrl+x"
	KeyCtrlY = "ctrl+y"
	KeyCtrlZ = "ctrl+z"
)

// ============================================================================
// Key Bindings
// ============================================================================

// KeyBinding represents a keyboard shortcut
type KeyBinding struct {
	Key         string // Key string as reported by tea.KeyMsg
	Description string // Human-readable description
	Context     string // Context where this binding is active
}

// Global key bindings - work from any context
var globalKeyBindings = []KeyBinding{
	{KeyTab, "Toggle focus between display and input", "global"},
	{KeyCtrlG, "Cancel current request (with confirmation)", "global"},
	{KeyCtrlC, "Clear input field", "global"},
	{KeyCtrlS, "Save session", "global"},
	{KeyCtrlO, "Open external editor", "global"},
	{KeyCtrlL, "Open model selector", "global"},
	{KeyCtrlP, "Open theme selector", "global"},
	{KeyCtrlQ, "Open queue manager", "global"},
	{KeyEnter, "Submit prompt/command", "global"},
}

// Display key bindings - only active when display is focused
var displayKeyBindings = []KeyBinding{
	// Move window cursor down
	{KeyJ, "Move window cursor down", "display"},
	{KeyDown, "Move window cursor down", "display"},
	// Move window cursor up
	{KeyK, "Move window cursor up", "display"},
	{KeyUp, "Move window cursor up", "display"},
	// Scroll down one line
	{KeyShiftJ, "Scroll down one line", "display"},
	{KeyShiftDown, "Scroll down one line", "display"},
	// Scroll up one line
	{KeyShiftK, "Scroll up one line", "display"},
	{KeyShiftUp, "Scroll up one line", "display"},
	// Scroll down half screen
	{KeyCtrlD, "Scroll down half screen", "display"},
	// Scroll up half screen
	{KeyCtrlU, "Scroll up half screen", "display"},
	// Go to bottom (last window)
	{KeyG, "Go to bottom (last window)", "display"},
	// Go to top (first window)
	{Keyg, "Go to top (first window)", "display"},
	// Move cursor to top window
	{KeyShiftH, "Move cursor to top window", "display"},
	// Move cursor to bottom window
	{KeyShiftL, "Move cursor to bottom window", "display"},
	// Move cursor to middle window
	{KeyShiftM, "Move cursor to middle window", "display"},
	// Open window content in external editor
	{KeyE, "Open window content in external editor", "display"},
	// Switch to input with command prefix
	{KeyColon, "Switch to input with command prefix", "display"},
	// Toggle window fold (expand/collapse)
	{KeySpace, "Toggle window fold (expand/collapse)", "display"},
}

// Model selector key bindings
var modelSelectorKeyBindings = []KeyBinding{
	{KeyUp, "Move selection up", "model-selector"},
	{KeyDown, "Move selection down", "model-selector"},
	{KeyEnter, "Select model", "model-selector"},
	{KeyEsc, "Close model selector", "model-selector"},
	{KeyTab, "Toggle focus between search and list", "model-selector"},
	{"e", "Edit model config file", "model-selector"},
	{"r", "Reload models from file", "model-selector"},
}

// Queue manager key bindings
var queueManagerKeyBindings = []KeyBinding{
	{KeyUp, "Move selection up", "queue-manager"},
	{KeyDown, "Move selection down", "queue-manager"},
	{KeyEsc, "Close queue manager", "queue-manager"},
	{"d", "Delete selected queue item", "queue-manager"},
}

// Theme selector key bindings
var themeSelectorKeyBindings = []KeyBinding{
	{KeyUp, "Move selection up", "theme-selector"},
	{KeyDown, "Move selection down", "theme-selector"},
	{"j", "Move selection down", "theme-selector"},
	{"k", "Move selection up", "theme-selector"},
	{KeyEnter, "Select theme", "theme-selector"},
	{KeyEsc, "Close theme selector", "theme-selector"},
	{"r", "Reload themes from folder", "theme-selector"},
	{"q", "Close theme selector", "theme-selector"},
}

// Confirmation dialog key bindings
var confirmDialogKeyBindings = []KeyBinding{
	{"y", "Confirm action", "confirm-dialog"},
	{"n", "Cancel action", "confirm-dialog"},
	{KeyEsc, "Cancel action", "confirm-dialog"},
	{KeyCtrlC, "Cancel action", "confirm-dialog"},
}

// GetAllKeyBindings returns all key bindings for help display
func GetAllKeyBindings() []KeyBinding {
	var all []KeyBinding
	all = append(all, globalKeyBindings...)
	all = append(all, displayKeyBindings...)
	all = append(all, modelSelectorKeyBindings...)
	all = append(all, queueManagerKeyBindings...)
	all = append(all, themeSelectorKeyBindings...)
	all = append(all, confirmDialogKeyBindings...)
	return all
}

// ============================================================================
// Key Handler
// ============================================================================

// handleKeyMsg routes keyboard input to the appropriate handler.
func (m *Terminal) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// 1. Theme selector takes precedence when open
	if m.themeSelector.IsOpen() {
		return m.handleThemeSelectorKeys(msg)
	}

	// 2. Model selector takes precedence when open
	if m.modelSelector.IsOpen() {
		return m.handleModelSelectorKeys(msg)
	}

	// 3. Queue manager takes precedence when open
	if m.queueManager.IsOpen() {
		return m.handleQueueManagerKeys(msg)
	}

	// 4. Confirmation dialogs block normal input
	if cmd, handled := m.handleConfirmDialog(msg); handled {
		return m, cmd
	}

	// 5. Tab toggles focus between display and input
	if msg.String() == KeyTab {
		m.toggleFocus()
		return m, nil
	}

	// 6. Display-specific keys when display is focused
	if m.focusedWindow == "display" {
		if cmd, handled := m.handleDisplayKeys(msg); handled {
			return m, cmd
		}
	}

	// 7. Global shortcuts (work from any context)
	if cmd, handled := m.handleGlobalKeys(msg); handled {
		return m, cmd
	}

	// 8. Default: pass to input
	return m.handleInputKeys(msg)
}

// handleThemeSelectorKeys handles input when theme selector is open.
func (m *Terminal) handleThemeSelectorKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Check if it's a reload request
	if msg.String() == "r" && m.themeManager != nil {
		m.themeManager.ReloadThemes()
		m.themeSelector.Open(m.themeManager.GetThemes(), m.session.GetRuntimeManager().GetActiveTheme())
		return m, nil
	}

	// Track if selector was open before handling key
	wasOpen := m.themeSelector.IsOpen()

	previewTheme, handled := m.themeSelector.HandleKeyMsg(msg, m.themeManager)
	if !handled {
		return m, nil
	}

	// Check if theme was selected (Enter key)
	if m.themeSelector.ConsumeThemeSelected() {
		if previewTheme != nil {
			m.applyTheme(previewTheme)
		}
		selectedTheme := m.themeSelector.GetSelectedTheme()
		if selectedTheme != nil {
			// Save to runtime.conf
			_ = m.session.GetRuntimeManager().SetActiveTheme(selectedTheme.Name) //nolint:errcheck // best-effort save
		}
		m.restoreFocus()
		return m, nil
	}

	// If selector closed without selection (ESC/q), restore original theme
	if wasOpen && !m.themeSelector.IsOpen() {
		originalThemeName := m.themeSelector.GetOriginalThemeName()
		originalTheme := m.themeManager.LoadTheme(originalThemeName)
		m.applyTheme(originalTheme)
		m.restoreFocus()
		return m, nil
	}

	// Apply preview theme if changed
	if previewTheme != nil {
		// For navigation keys, delay theme application to keep cursor responsive
		key := msg.String()
		if key == "j" || key == "k" || key == "up" || key == "down" {
			// Increment the ID to invalidate any pending preview
			m.themePreviewID++
			// Capture the current ID to check if this preview is still valid
			id := m.themePreviewID
			theme := previewTheme

			// Return a command that will apply the theme after a delay
			// This allows the cursor to update immediately
			return m, tea.Tick(150*time.Millisecond, func(_ time.Time) tea.Msg {
				return themePreviewMsg{theme: theme, id: id}
			})
		}
		// Apply immediately for other keys
		m.applyTheme(previewTheme)
	}

	return m, nil
}

// themePreviewMsg is sent when a theme preview should be applied
type themePreviewMsg struct {
	theme *Theme
	id    int // ID to check if this preview is still current
}

func (m *Terminal) handleThemePreview(msg themePreviewMsg) (tea.Model, tea.Cmd) {
	// Only apply if this preview is still the current one (debouncing)
	if msg.id == m.themePreviewID {
		m.applyTheme(msg.theme)
	}
	return m, nil
}

// handleModelSelectorKeys handles input when model selector is open.
func (m *Terminal) handleModelSelectorKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	cmd := m.modelSelector.HandleKeyMsg(msg)

	// Check if a model was selected
	if m.modelSelector.ConsumeModelSelected() {
		m.switchToSelectedModel()
	}

	// Check if user wants to open model file
	if m.modelSelector.ConsumeOpenModelFile() {
		return m, tea.Batch(cmd, m.openModelConfigFile())
	}

	// Check if user wants to reload models
	if m.modelSelector.ConsumeReloadModels() {
		m.emitCommand(":model_load")
	}

	// Restore focus when model selector closes
	if !m.modelSelector.IsOpen() {
		m.restoreFocus()
	}

	return m, cmd
}

// handleQueueManagerKeys handles input when queue manager is open.
func (m *Terminal) handleQueueManagerKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Handle 'd' key for delete
	if msg.String() == KeyD {
		selectedItem := m.queueManager.GetSelectedItem()
		if selectedItem != nil {
			// Send delete command to session
			m.emitCommand(":taskqueue_del " + selectedItem.QueueID)
			// Request updated queue list
			m.emitCommand(":taskqueue_get_all")
		}
		return m, nil
	}

	cmd := m.queueManager.HandleKeyMsg(msg)

	// Restore focus when queue manager closes
	if !m.queueManager.IsOpen() {
		m.restoreFocus()
	}

	return m, cmd
}

// handleConfirmDialog handles quit and cancel confirmation dialogs.
func (m *Terminal) handleConfirmDialog(msg tea.KeyMsg) (tea.Cmd, bool) {
	if m.confirmDialog == confirmNone {
		return nil, false
	}

	key := msg.String()

	switch key {
	case KeyY, "Y":
		kind := m.confirmDialog
		fromCmd := m.confirmFromCommand
		m.confirmDialog = confirmNone
		m.confirmFromCommand = false

		switch kind {
		case confirmQuit:
			m.quitting = true
			m.streamInput.Close()
			m.out.Close()
			return tea.Quit, true
		case confirmCancel:
			if fromCmd {
				m.input.SetValue("")
			}
			return m.submitCommand("cancel", fromCmd), true
		case confirmCancelAll:
			if fromCmd {
				m.input.SetValue("")
			}
			return m.submitCommand("cancel_all", fromCmd), true
		}

	case KeyN, "N", KeyEsc, KeyCtrlC:
		if m.confirmFromCommand {
			m.input.SetValue("")
		}
		m.confirmDialog = confirmNone
		m.confirmFromCommand = false
		return nil, true
	}

	return nil, true
}

// displayKeyMap maps display key strings to their handler functions.
// Each handler returns a tea.Cmd (may be nil) for follow-up commands.
var displayKeyMap = map[string]func(*Terminal) tea.Cmd{
	KeyJ: func(m *Terminal) tea.Cmd {
		if m.display.MoveWindowCursorDown() {
			m.display.EnsureCursorVisible()
			m.display.updateContent()
		}
		return nil
	},
	KeyDown: func(m *Terminal) tea.Cmd {
		if m.display.MoveWindowCursorDown() {
			m.display.EnsureCursorVisible()
			m.display.updateContent()
		}
		return nil
	},
	KeyK: func(m *Terminal) tea.Cmd {
		if m.display.MoveWindowCursorUp() {
			m.display.EnsureCursorVisible()
			m.display.updateContent()
		}
		return nil
	},
	KeyUp: func(m *Terminal) tea.Cmd {
		if m.display.MoveWindowCursorUp() {
			m.display.EnsureCursorVisible()
			m.display.updateContent()
		}
		return nil
	},
	KeyCtrlD: func(m *Terminal) tea.Cmd {
		m.display.MarkUserScrolled()
		m.display.ScrollDown(max(1, m.display.GetHeight()/2))
		return nil
	},
	KeyCtrlU: func(m *Terminal) tea.Cmd {
		m.display.MarkUserScrolled()
		m.display.ScrollUp(max(1, m.display.GetHeight()/2))
		return nil
	},
	KeyShiftJ: func(m *Terminal) tea.Cmd {
		m.display.MarkUserScrolled()
		m.display.ScrollDown(1)
		return nil
	},
	KeyShiftDown: func(m *Terminal) tea.Cmd {
		m.display.MarkUserScrolled()
		m.display.ScrollDown(1)
		return nil
	},
	KeyShiftK: func(m *Terminal) tea.Cmd {
		m.display.MarkUserScrolled()
		m.display.ScrollUp(1)
		return nil
	},
	KeyShiftUp: func(m *Terminal) tea.Cmd {
		m.display.MarkUserScrolled()
		m.display.ScrollUp(1)
		return nil
	},
	KeyShiftH: func(m *Terminal) tea.Cmd {
		if m.display.MoveWindowCursorToTop() {
			m.display.EnsureCursorVisible()
			m.display.updateContent()
		}
		return nil
	},
	KeyShiftL: func(m *Terminal) tea.Cmd {
		if m.display.MoveWindowCursorToBottom() {
			m.display.EnsureCursorVisible()
			m.display.updateContent()
		}
		return nil
	},
	KeyShiftM: func(m *Terminal) tea.Cmd {
		if m.display.MoveWindowCursorToCenter() {
			m.display.EnsureCursorVisible()
			m.display.updateContent()
		}
		return nil
	},
	KeyG: func(m *Terminal) tea.Cmd {
		m.display.SetCursorToLastWindow()
		m.display.GotoBottom()
		m.display.updateContent()
		return nil
	},
	Keyg: func(m *Terminal) tea.Cmd {
		m.display.SetWindowCursor(0)
		m.display.GotoTop()
		m.display.updateContent()
		return nil
	},
	KeyColon: func(m *Terminal) tea.Cmd {
		m.focusInput()
		m.input.SetValue(":")
		m.input.CursorEnd()
		return nil
	},
	KeySpace: func(m *Terminal) tea.Cmd {
		if m.display.ToggleWindowFold() {
			m.display.EnsureCursorVisible()
			m.display.updateContent()
		}
		return nil
	},
	KeyE: func(m *Terminal) tea.Cmd {
		content := m.display.GetCursorWindowContent()
		if content != "" {
			return m.editor.OpenForDisplay(content)
		}
		return nil
	},
}

// handleDisplayKeys handles key events when display window is focused.
//
// IMPORTANT: When moving the cursor, always call EnsureCursorVisible() BEFORE
// updateContent(). This ensures the viewport position is updated before content
// is regenerated, preventing blank areas in the virtual rendering.
func (m *Terminal) handleDisplayKeys(msg tea.KeyMsg) (tea.Cmd, bool) {
	handler, ok := displayKeyMap[msg.String()]
	if !ok {
		return nil, false
	}
	return handler(m), true
}

// handleGlobalKeys handles global keyboard shortcuts.
func (m *Terminal) handleGlobalKeys(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case KeyCtrlG:
		m.confirmDialog = confirmCancel
		m.confirmFromCommand = false
		return nil, true

	case KeyCtrlC:
		if m.focusedWindow == focusInput {
			m.input.SetValue("")
			m.input.editorContent = ""
		}
		return nil, true

	case KeyCtrlS:
		return m.submitCommand("save", false), true

	case KeyCtrlO:
		return m.OpenEditor(), true

	case KeyCtrlL:
		m.openModelSelector()
		return nil, true

	case KeyCtrlP:
		m.openThemeSelector()
		return nil, true

	case KeyCtrlQ:
		m.openQueueManager()
		return nil, true

	case KeyEnter:
		return m.handleSubmit(), true
	}

	return nil, false
}

// handleInputKeys handles keys when input is focused (default behavior).
func (m *Terminal) handleInputKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Block keys that would modify input content unexpectedly
	switch msg.String() {
	case KeyCtrlU, KeyCtrlD:
		// Swallow to prevent textinput's default clear-line / delete-char
		return m, nil
	}

	oldValue := m.input.Value()
	m.input.updateFromMsg(msg)
	newValue := m.input.Value()

	// Clear editor content if user manually edits the input
	if m.input.editorContent != "" && oldValue != newValue && !hasEditorPrefix(oldValue) {
		m.input.editorContent = ""
	}

	return m, nil
}

// ============================================================================
// Command Handling
// ============================================================================

// handleSubmit processes the input when Enter is pressed.
func (m *Terminal) handleSubmit() tea.Cmd {
	prompt := m.input.GetPrompt()
	m.input.editorContent = ""

	if prompt == "" {
		return nil
	}

	// Check if it's a command (starts with ":")
	if command, found := strings.CutPrefix(prompt, ":"); found {
		return m.handleCommand(command)
	}

	// Regular prompt - send to agent
	m.emitCommand(prompt)
	m.input.SetValue("")

	return scheduleTick()
}

// handleCommand processes a command string (without the ":" prefix).
func (m *Terminal) handleCommand(command string) tea.Cmd {
	// Quit command
	if command == "quit" || command == "q" {
		m.confirmDialog = confirmQuit
		return nil
	}

	// Cancel command
	if command == "cancel" {
		m.confirmDialog = confirmCancel
		m.confirmFromCommand = true
		return nil
	}

	// Cancel all command
	if command == "cancel_all" {
		m.confirmDialog = confirmCancelAll
		m.confirmFromCommand = true
		return nil
	}

	// All other commands - pass through to session
	return m.submitCommand(command, true)
}

// submitCommand sends a command to the session and optionally clears input.
func (m *Terminal) submitCommand(command string, clearInput bool) tea.Cmd {
	m.emitCommand(":" + command)
	if clearInput {
		m.input.SetValue("")
	}
	return scheduleTick()
}

// scheduleTick schedules a tick message for UI updates.
func scheduleTick() tea.Cmd {
	return tea.Tick(SubmitTickDelay, func(_ time.Time) tea.Msg {
		return tickMsg{}
	})
}

// switchToSelectedModel sends a model_set command to switch to the selected model.
func (m *Terminal) switchToSelectedModel() {
	selectedModel := m.modelSelector.GetActiveModel()
	if selectedModel == nil {
		return
	}

	// Send model_set command to session
	if selectedModel.ID != 0 {
		m.emitCommand(fmt.Sprintf(":model_set %d", selectedModel.ID))
	}
}

// openModelConfigFile opens the model config file with $EDITOR.
func (m *Terminal) openModelConfigFile() tea.Cmd {
	path := m.out.SnapshotModels().ConfigPath
	if path == "" {
		return func() tea.Msg {
			return FileEditorFinishedMsg{
				Path: "",
				Err:  fmt.Errorf("no model config file path configured"),
			}
		}
	}

	return m.editor.OpenFile(path)
}
