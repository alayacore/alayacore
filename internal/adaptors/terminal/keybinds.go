package terminal

// Key handling for the terminal UI.
// This file provides key constants, bindings, and the key handler.

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/alayacore/alayacore/internal/stream"
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
	{KeyJ, "Move window cursor down", "display"},
	{KeyK, "Move window cursor up", "display"},
	{KeyDown, "Move window cursor down", "display"},
	{KeyUp, "Move window cursor up", "display"},
	{KeyShiftJ, "Scroll down one line", "display"},
	{KeyShiftK, "Scroll up one line", "display"},
	{KeyE, "Open window content in external editor", "display"},
	{KeyG, "Go to bottom (last window)", "display"},
	{Keyg, "Go to top (first window)", "display"},
	{KeyShiftH, "Move cursor to top window", "display"},
	{KeyShiftL, "Move cursor to bottom window", "display"},
	{KeyShiftM, "Move cursor to middle window", "display"},
	{KeyColon, "Switch to input with command prefix", "display"},
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

	previewTheme, handled := m.themeSelector.HandleKeyMsg(msg, m.themeManager)
	if !handled {
		return m, nil
	}

	// Apply preview theme if changed
	if previewTheme != nil {
		m.applyTheme(previewTheme)
	}

	// Check if theme was selected
	if m.themeSelector.ConsumeThemeSelected() {
		selectedTheme := m.themeSelector.GetSelectedTheme()
		if selectedTheme != nil {
			// Save to runtime.conf
			_ = m.session.GetRuntimeManager().SetActiveTheme(selectedTheme.Name) //nolint:errcheck // best-effort save
		}
		m.restoreFocusAfterThemeSelector()
	}

	// Restore focus when theme selector closes
	if !m.themeSelector.IsOpen() {
		m.restoreFocusAfterThemeSelector()
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
		_ = m.streamInput.EmitTLV(stream.TagTextUser, ":model_load") //nolint:errcheck // best-effort input
	}

	// Restore focus when model selector closes
	if !m.modelSelector.IsOpen() {
		m.restoreFocusAfterSelector()
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
			_ = m.streamInput.EmitTLV(stream.TagTextUser, ":taskqueue_del "+selectedItem.QueueID) //nolint:errcheck // best-effort input
			// Request updated queue list
			_ = m.streamInput.EmitTLV(stream.TagTextUser, ":taskqueue_get_all") //nolint:errcheck // best-effort input
		}
		return m, nil
	}

	cmd := m.queueManager.HandleKeyMsg(msg)

	// Restore focus when queue manager closes
	if !m.queueManager.IsOpen() {
		m.restoreFocusAfterQueueManager()
	}

	return m, cmd
}

// handleConfirmDialog handles quit and cancel confirmation dialogs.
func (m *Terminal) handleConfirmDialog(msg tea.KeyMsg) (tea.Cmd, bool) {
	if m.confirmDialog {
		return m.handleQuitConfirm(msg)
	}

	if m.cancelConfirmDialog {
		return m.handleCancelConfirm(msg)
	}

	if m.cancelAllConfirmDialog {
		return m.handleCancelAllConfirm(msg)
	}

	return nil, false
}

// handleQuitConfirm handles the quit confirmation dialog.
func (m *Terminal) handleQuitConfirm(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case KeyY, "Y":
		m.quitting = true
		m.streamInput.Close()
		m.out.Close()
		return tea.Quit, true
	case KeyN, "N", KeyEsc, KeyCtrlC:
		m.confirmDialog = false
		m.input.SetValue("")
		return nil, true
	}
	return nil, true
}

// handleCancelConfirm handles the cancel confirmation dialog.
func (m *Terminal) handleCancelConfirm(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case KeyY, "Y":
		m.cancelConfirmDialog = false
		if m.cancelFromCommand {
			m.input.SetValue("")
		}
		return m.submitCommand("cancel", m.cancelFromCommand), true
	case KeyN, "N", KeyEsc, KeyCtrlC:
		m.cancelConfirmDialog = false
		if m.cancelFromCommand {
			m.input.SetValue("")
		}
		return nil, true
	}
	return nil, true
}

// handleCancelAllConfirm handles the cancel_all confirmation dialog.
func (m *Terminal) handleCancelAllConfirm(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case KeyY, "Y":
		m.cancelAllConfirmDialog = false
		if m.cancelFromCommand {
			m.input.SetValue("")
		}
		return m.submitCommand("cancel_all", m.cancelFromCommand), true
	case KeyN, "N", KeyEsc, KeyCtrlC:
		m.cancelAllConfirmDialog = false
		if m.cancelFromCommand {
			m.input.SetValue("")
		}
		return nil, true
	}
	return nil, true
}

// handleDisplayKeys handles key events when display window is focused.
//
// IMPORTANT: When moving the cursor, always call EnsureCursorVisible() BEFORE
// updateContent(). This ensures the viewport position is updated before content
// is regenerated, preventing blank areas in the virtual rendering.
//
//nolint:gocyclo // key handling requires many key cases
func (m *Terminal) handleDisplayKeys(msg tea.KeyMsg) (tea.Cmd, bool) {
	keyStr := msg.String()

	// Window cursor navigation
	switch keyStr {
	case KeyJ, KeyDown:
		if m.display.MoveWindowCursorDown() {
			m.display.EnsureCursorVisible()
			m.display.updateContent()
		}
		return nil, true

	case KeyK, KeyUp:
		if m.display.MoveWindowCursorUp() {
			m.display.EnsureCursorVisible()
			m.display.updateContent()
		}
		return nil, true

	case KeyShiftJ:
		m.display.MarkUserScrolled()
		m.display.ScrollDown(1)
		return nil, true

	case KeyShiftK:
		m.display.MarkUserScrolled()
		m.display.ScrollUp(1)
		return nil, true

	case KeyShiftH:
		if m.display.MoveWindowCursorToTop() {
			m.display.EnsureCursorVisible()
			m.display.updateContent()
		}
		return nil, true

	case KeyShiftL:
		if m.display.MoveWindowCursorToBottom() {
			m.display.EnsureCursorVisible()
			m.display.updateContent()
		}
		return nil, true

	case KeyShiftM:
		if m.display.MoveWindowCursorToCenter() {
			m.display.EnsureCursorVisible()
			m.display.updateContent()
		}
		return nil, true

	case KeyG:
		m.display.SetCursorToLastWindow()
		m.display.GotoBottom()
		m.display.updateContent()
		return nil, true

	case Keyg:
		m.display.SetWindowCursor(0)
		m.display.GotoTop()
		m.display.updateContent()
		return nil, true

	case KeyColon:
		// Switch to input with ":" prefix for command mode
		m.focusInput()
		m.input.SetValue(":")
		m.input.CursorEnd()
		return nil, true

	case KeySpace:
		if m.display.ToggleWindowFold() {
			m.display.updateContent()
		}
		return nil, true

	case KeyE:
		// Open current window content in external editor (view only, don't populate input)
		content := m.display.GetCursorWindowContent()
		if content != "" {
			return m.input.editor.OpenForDisplay(content), true
		}
		return nil, true
	}

	return nil, false
}

// handleGlobalKeys handles global keyboard shortcuts.
func (m *Terminal) handleGlobalKeys(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case KeyCtrlG:
		m.cancelConfirmDialog = true
		m.cancelFromCommand = false
		return nil, true

	case KeyCtrlC:
		if m.focusedWindow == focusInput {
			m.input.SetValue("")
			m.input.editorContent = ""
		}
		return nil, true

	case KeyCtrlU:
		// Reserved for future use
		return nil, true

	case KeyCtrlS:
		return m.submitCommand("save", false), true

	case KeyCtrlO:
		return m.input.OpenEditor(), true

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
	_ = m.streamInput.EmitTLV(stream.TagTextUser, prompt) //nolint:errcheck // best-effort input
	m.input.SetValue("")

	return scheduleTick()
}

// handleCommand processes a command string (without the ":" prefix).
func (m *Terminal) handleCommand(command string) tea.Cmd {
	// Quit command
	if command == "quit" || command == "q" {
		m.confirmDialog = true
		return nil
	}

	// Cancel command
	if command == "cancel" {
		m.cancelConfirmDialog = true
		m.cancelFromCommand = true
		return nil
	}

	// Cancel all command
	if command == "cancel_all" {
		m.cancelAllConfirmDialog = true
		m.cancelFromCommand = true
		return nil
	}

	// All other commands - pass through to session
	return m.submitCommand(command, true)
}

// submitCommand sends a command to the session and optionally clears input.
func (m *Terminal) submitCommand(command string, clearInput bool) tea.Cmd {
	_ = m.streamInput.EmitTLV(stream.TagTextUser, ":"+command) //nolint:errcheck // best-effort input
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
		_ = m.streamInput.EmitTLV(stream.TagTextUser, fmt.Sprintf(":model_set %d", selectedModel.ID)) //nolint:errcheck // best-effort input
	}
}

// openModelConfigFile opens the model config file with $EDITOR.
func (m *Terminal) openModelConfigFile() tea.Cmd {
	path := m.out.GetModelConfigPath()
	if path == "" {
		return func() tea.Msg {
			return FileEditorFinishedMsg{
				Path: "",
				Err:  fmt.Errorf("no model config file path configured"),
			}
		}
	}

	return m.input.editor.OpenFile(path)
}
