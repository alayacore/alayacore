package terminal

// This package implements the terminal UI adaptor for AlayaCore.
// It uses Bubble Tea for the TUI framework and handles:
//   - Display of assistant output with virtual scrolling
//   - User input with external editor support
//   - Model selection and task queue management
//   - TLV protocol communication with the session

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/stream"
)

// emitCommand sends a user-level command to the session via TLV.
// Errors are logged but not propagated — commands are best-effort.
func (m *Terminal) emitCommand(cmd string) {
	_ = m.streamInput.EmitTLV(stream.TagTextUser, cmd)
}

// ============================================================================
// Constants
// ============================================================================

const (
	DefaultWidth  = 80
	DefaultHeight = 20

	// Row allocation: input box, status bar, newlines
	InputRows  = 3
	StatusRows = 1
	LayoutGap  = 4 // input + status + newlines between sections

	// Component sizing
	InputPaddingH     = 8  // horizontal padding for input fields (border + padding both sides)
	SelectorMaxHeight = 30 // maximum height for model selector and similar overlays
	SelectorListRows  = 8  // content rows inside selector borders
)

// Timing constants
const (
	UpdateThrottleInterval = 100 * time.Millisecond // batch rapid display updates
	TickInterval           = 250 * time.Millisecond // polling during streaming
	FlusherInterval        = 50 * time.Millisecond  // update flusher tick
	SubmitTickDelay        = 50 * time.Millisecond  // delay before first tick after submit
)

// Focus constants
const (
	focusInput   = "input"
	focusDisplay = "display"
)

// ============================================================================
// Confirmation Dialog
// ============================================================================

// confirmKind represents the type of active confirmation dialog.
type confirmKind int

const (
	confirmNone      confirmKind = iota // No dialog active
	confirmQuit                         // Confirm exit
	confirmCancel                       // Confirm cancel current request
	confirmCancelAll                    // Confirm cancel all queued requests
)

// ============================================================================
// Terminal Model
// ============================================================================

// Terminal is the main Bubble Tea model that composes display, input, and status components.
// It serves as the central coordinator for the terminal UI, managing:
//   - User input and keyboard shortcuts (delegated to keybinds.go)
//   - Display updates from the agent session
//   - Model selection and switching
//   - Theme selection and switching
//   - Window focus management
type Terminal struct {
	// Core components
	session     *agentpkg.Session
	out         OutputWriter
	streamInput *stream.ChanInput
	appConfig   *app.Config
	editor      *Editor

	// UI components
	display       DisplayModel
	input         InputModel
	modelSelector *ModelSelector
	queueManager  *QueueManager
	themeSelector *ThemeSelector
	themeManager  *ThemeManager

	// Status bar state (simplified - no separate struct)
	statusText string
	inProgress bool

	// State
	quitting           bool
	confirmDialog      confirmKind // active confirmation dialog (confirmNone when inactive)
	confirmFromCommand bool        // tracks if cancel came from :cancel command (vs Ctrl+G)
	focusedWindow      string      // "input" or "display"
	windowWidth        int
	windowHeight       int
	styles             *Styles
	hasFocus           bool // tracks whether the terminal has application focus

	// Theme preview debouncing
	themePreviewID int // ID of the current pending theme preview
}

// NewTerminal creates a new Terminal model with all components initialized.
func NewTerminal(
	session *agentpkg.Session,
	out OutputWriter,
	inputStream *stream.ChanInput,
	appCfg *app.Config,
	initialWidth, initialHeight int,
) *Terminal {
	return NewTerminalWithTheme(session, out, inputStream, appCfg, initialWidth, initialHeight, DefaultTheme(), nil)
}

// NewTerminalWithTheme creates a new Terminal model with a custom theme.
func NewTerminalWithTheme(
	session *agentpkg.Session,
	out OutputWriter,
	inputStream *stream.ChanInput,
	appCfg *app.Config,
	initialWidth, initialHeight int,
	theme *Theme,
	themeManager *ThemeManager,
) *Terminal {
	styles := NewStyles(theme)

	editor := NewEditor()

	m := &Terminal{
		session:       session,
		out:           out,
		streamInput:   inputStream,
		appConfig:     appCfg,
		editor:        editor,
		display:       NewDisplayModel(out.WindowBuffer(), styles),
		input:         NewInputModel(styles),
		modelSelector: NewModelSelector(styles),
		queueManager:  NewQueueManager(styles),
		themeSelector: NewThemeSelector(styles),
		themeManager:  themeManager,
		windowWidth:   initialWidth,
		windowHeight:  initialHeight,
		styles:        styles,
		focusedWindow: "input",
		hasFocus:      true,
	}

	// Initialize component widths
	m.display.SetWidth(initialWidth)
	m.input.SetWidth(initialWidth)
	m.modelSelector.SetSize(initialWidth, initialHeight)
	m.queueManager.SetSize(initialWidth, initialHeight)
	m.themeSelector.SetSize(initialWidth, initialHeight)
	m.updateDisplayHeight()

	return m
}

// Init starts the periodic tick loop for processing session updates.
func (m *Terminal) Init() tea.Cmd {
	// Display any buffered warnings from initialization
	if m.themeManager != nil {
		if warnings := m.themeManager.GetWarnings(); len(warnings) > 0 {
			for _, w := range warnings {
				m.out.AppendError("%s", w.Message)
			}
		}
	}

	return tea.Tick(TickInterval, func(_ time.Time) tea.Msg {
		return tickMsg{}
	})
}

// Update handles all incoming messages and routes them to appropriate handlers.
// Messages are processed in order of priority:
//  1. KeyMsg - keyboard input (highest priority for responsiveness)
//  2. WindowSizeMsg - terminal resize
//  3. tickMsg - periodic updates for display and model switching
//  4. Editor messages - external editor completion
//  5. Focus/Blur - application focus changes
//  6. Paste - clipboard paste
func (m *Terminal) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)

	case tea.WindowSizeMsg:
		return m.handleWindowSize(msg)

	case tickMsg:
		return m.handleTick()

	case themePreviewMsg:
		return m.handleThemePreview(msg)

	case editorStartMsg:
		return m.handleEditorStart(msg)

	case editorFinishedMsg:
		return m.handleEditorFinished(msg)

	case displayEditorFinishedMsg:
		return m.handleDisplayEditorFinished(msg)

	case FileEditorFinishedMsg:
		return m.handleFileEditorFinished(msg)

	case tea.BlurMsg:
		return m.handleBlur()

	case tea.FocusMsg:
		return m.handleFocus()

	case tea.PasteMsg:
		m.input.updateFromMsg(msg)
		return m, nil
	}

	// Default: pass to input component
	m.input.updateFromMsg(msg)
	return m, nil
}

// tickMsg is sent periodically to update the display.
type tickMsg struct{}

// handleWindowSize handles terminal resize events.
func (m *Terminal) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.windowWidth = msg.Width
	m.windowHeight = msg.Height

	// Update all components
	m.out.SetWindowWidth(max(0, msg.Width))
	m.display.SetWidth(max(0, msg.Width))
	m.input.SetWidth(max(0, msg.Width))
	m.modelSelector.SetSize(msg.Width, msg.Height)
	m.queueManager.SetSize(msg.Width, msg.Height)
	m.themeSelector.SetSize(msg.Width, msg.Height)
	m.updateDisplayHeight()

	// Validate cursor position after resize (window heights may have changed)
	m.display.ValidateCursor()

	// Re-render display content with new width (windowBuffer was marked dirty by SetWindowWidth)
	m.display.updateContent()

	return m, nil
}

// handleTick processes periodic updates for display and model switching.
func (m *Terminal) handleTick() (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	// Drain pending display updates (non-blocking)
	select {
	case <-m.out.UpdateChan():
		if m.out.WindowBuffer().GetWindowCount() > 0 {
			m.updateStatus()
			m.updateDisplayHeight()
			if m.display.shouldFollow() {
				m.display.SetCursorToLastWindow()
			}
			m.display.updateContent()
		}

		// Update model selector if models changed
		modelSnap := m.out.SnapshotModels()
		cmd = m.modelSelector.LoadModels(modelSnap.Models, modelSnap.ActiveID)

		// Check for queue items update
		if queueItems := m.out.GetQueueItems(); queueItems != nil {
			m.queueManager.SetItems(queueItems)
			// Update display to show new items
			m.display.updateContent()
		}

	default:
		m.updateStatus()
	}

	// Continue ticking
	return m, tea.Batch(
		tea.Tick(TickInterval, func(_ time.Time) tea.Msg {
			return tickMsg{}
		}),
		cmd,
	)
}

// handleEditorFinished handles completion of the external editor.
func (m *Terminal) handleEditorFinished(msg editorFinishedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.out.AppendError("Editor error: %v", msg.err)
	} else if msg.content != "" {
		// Strip trailing newlines that text editors add by default
		content := strings.TrimRight(msg.content, "\n")
		m.input.editorContent = content
		m.input.SetValue(FormatEditorContent(content))
		m.input.CursorEnd()
		m.focusInput()
	}
	return m, nil
}

// handleDisplayEditorFinished handles completion of the external editor for display viewing.
// This is a no-op - we just opened the editor to view content, nothing to do when it closes.
func (m *Terminal) handleDisplayEditorFinished(msg displayEditorFinishedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.out.AppendError("Editor error: %v", msg.err)
	}
	return m, nil
}

// handleEditorStart handles the lazy start of the external editor.
// This is where the temp file is actually created, ensuring cleanup happens properly.
func (m *Terminal) handleEditorStart(msg editorStartMsg) (tea.Model, tea.Cmd) {
	// Create temp file lazily
	tmpFileName, err := m.editor.createTempFile()
	if err != nil {
		m.out.AppendError("Failed to create temp file: %v", err)
		return m, nil
	}

	//nolint:gosec // G204: Editor command from user config is intentional
	cmd := exec.Command(msg.editorCmd, tmpFileName)

	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer os.Remove(tmpFileName)

		if err != nil {
			if msg.forDisplay {
				return displayEditorFinishedMsg{err: err}
			}
			return editorFinishedMsg{content: "", err: err}
		}

		// For display viewing, we don't need to read the content back
		if msg.forDisplay {
			return displayEditorFinishedMsg{err: nil}
		}

		content, readErr := os.ReadFile(tmpFileName)
		if readErr != nil {
			return editorFinishedMsg{content: "", err: readErr}
		}

		return editorFinishedMsg{content: string(content), err: nil}
	})
}

// handleFileEditorFinished handles completion of file editing (e.g., model config).
func (m *Terminal) handleFileEditorFinished(msg FileEditorFinishedMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		m.out.WriteNotify(fmt.Sprintf("Error editing file %s: %v", msg.Path, msg.Err))
	}

	// Reload models if the model config file was edited
	if strings.HasSuffix(msg.Path, "model.conf") {
		m.emitCommand(":model_load")
	}

	return m, nil
}

// updateDisplayHeight updates the display viewport height based on window size.
func (m *Terminal) updateDisplayHeight() {
	m.display.UpdateHeight(m.windowHeight)
}

// updateStatus updates the status bar state from the output writer.
func (m *Terminal) updateStatus() {
	snap := m.out.SnapshotStatus()

	// Build status segments - each rendered separately with appropriate colors
	var segments []string

	// Queue segment - prefix dimmed, count highlighted
	if snap.QueueCount > 0 {
		prefix := m.styles.Status.Render("Queued(Ctrl-Q):")
		count := m.styles.Status.Foreground(m.styles.ColorAccent).Render(fmt.Sprintf("%d", snap.QueueCount))
		segments = append(segments, prefix+" "+count)
	}

	// Steps segment (always show)
	var stepsPart string
	if snap.LastMaxSteps > 0 {
		stepsPart = fmt.Sprintf("Steps: %d/%d", snap.LastCurrentStep, snap.LastMaxSteps)
	} else {
		stepsPart = fmt.Sprintf("Steps: %d/%d", snap.CurrentStep, snap.MaxSteps)
	}
	segments = append(segments, m.styles.Status.Render(stepsPart))

	// Context segment (dimmed)
	if snap.ContextStatus != "" {
		segments = append(segments, m.styles.Status.Render(snap.ContextStatus))
	}

	// Join segments with dimmed separator
	var status string
	if len(segments) > 0 {
		separator := m.styles.Status.Render("|")
		status = segments[0]
		for i := 1; i < len(segments); i++ {
			status += " " + separator + " " + segments[i]
		}
	}

	m.statusText = status
	m.inProgress = snap.InProgress
}

// View renders the complete terminal UI.
func (m *Terminal) View() tea.View {
	var sb strings.Builder

	// Display area
	sb.WriteString(m.display.View().Content)
	sb.WriteString("\n")

	// Input area with optional confirmation dialog
	confirmText := ""
	switch m.confirmDialog {
	case confirmQuit:
		confirmText = "Confirm exit? Press y/n"
	case confirmCancel:
		confirmText = "Confirm cancel? Press y/n"
	case confirmCancelAll:
		confirmText = "Confirm cancel all? Press y/n"
	}
	sb.WriteString(m.input.RenderWithBorder(m.confirmDialog != confirmNone, confirmText))

	// Status bar (simplified - just render directly)
	sb.WriteString("\n")
	sb.WriteString(m.renderStatusBar())

	baseContent := sb.String()

	// Render model selector overlay if open
	if m.modelSelector.IsOpen() {
		fullContent := m.modelSelector.RenderOverlay(baseContent, m.windowWidth, m.windowHeight)
		v := tea.NewView(fullContent)
		v.AltScreen = true
		v.ReportFocus = true
		return v
	}

	// Render theme selector overlay if open
	if m.themeSelector.IsOpen() {
		fullContent := m.themeSelector.RenderOverlay(baseContent, m.windowWidth, m.windowHeight)
		v := tea.NewView(fullContent)
		v.AltScreen = true
		v.ReportFocus = true
		return v
	}

	// Render queue manager overlay if open
	if m.queueManager.IsOpen() {
		fullContent := m.queueManager.RenderOverlay(baseContent, m.windowWidth, m.windowHeight)
		v := tea.NewView(fullContent)
		v.AltScreen = true
		v.ReportFocus = true
		return v
	}

	v := tea.NewView(baseContent)
	v.AltScreen = true
	v.ReportFocus = true
	return v
}

// renderStatusBar renders the status bar line.
func (m *Terminal) renderStatusBar() string {
	var indicator string
	if m.inProgress {
		indicator = m.styles.Status.Foreground(m.styles.ColorSuccess).Render("•")
	} else {
		indicator = m.styles.Status.Foreground(m.styles.ColorDim).Render("·")
	}

	if m.statusText != "" {
		padding := m.styles.Status.Padding(0, 2)
		return padding.Render(indicator + " " + m.statusText)
	}
	return m.styles.Status.Padding(0, 2).Render(indicator)
}

// Ensure Terminal implements tea.Model
var _ tea.Model = (*Terminal)(nil)

// ============================================================================
// Focus Management
// ============================================================================

// toggleFocus switches between display and input windows.
func (m *Terminal) toggleFocus() {
	if m.focusedWindow == focusDisplay {
		m.focusInput()
	} else {
		m.focusDisplay()
	}
	m.display.updateContent()
}

// focusInput switches focus to the input window.
func (m *Terminal) focusInput() {
	m.focusedWindow = focusInput
	m.display.SetDisplayFocused(false)
	m.input.Focus()
}

// focusDisplay switches focus to the display window.
func (m *Terminal) focusDisplay() {
	m.focusedWindow = focusDisplay
	m.display.SetDisplayFocused(true)
	m.input.Blur()
	if m.display.GetWindowCursor() < 0 {
		m.display.SetCursorToLastWindow()
	}
}

// openModelSelector opens the model selector UI.
func (m *Terminal) openModelSelector() {
	m.modelSelector.Open()
	m.input.Blur()
	m.display.SetDisplayFocused(false)
	m.display.updateContent()
}

// restoreFocus restores focus to the previously focused window after an overlay closes.
func (m *Terminal) restoreFocus() {
	if m.focusedWindow == focusDisplay {
		m.focusDisplay()
	} else {
		m.focusInput()
	}
	m.display.updateContent()
}

// openQueueManager opens the queue manager UI.
func (m *Terminal) openQueueManager() {
	m.emitCommand(":taskqueue_get_all")
	m.queueManager.Open()
	m.input.Blur()
	m.display.SetDisplayFocused(false)
	m.display.updateContent()
}

// openThemeSelector opens the theme selector UI.
func (m *Terminal) openThemeSelector() {
	if m.themeManager == nil {
		return
	}

	activeTheme := m.session.GetRuntimeManager().GetActiveTheme()
	m.themeSelector.Open(m.themeManager.GetThemes(), activeTheme)
	m.input.Blur()
	m.display.SetDisplayFocused(false)
	m.display.updateContent()
}

// applyTheme applies a new theme to all UI components.
func (m *Terminal) applyTheme(theme *Theme) {
	m.styles = NewStyles(theme)
	m.out.SetStyles(m.styles)
	m.display.SetStyles(m.styles)
	m.input.SetStyles(m.styles)
	m.modelSelector.SetStyles(m.styles)
	m.queueManager.SetStyles(m.styles)
	m.themeSelector.SetStyles(m.styles)
	m.display.updateContent()
}

// handleBlur handles loss of application focus.
func (m *Terminal) handleBlur() (tea.Model, tea.Cmd) {
	m.hasFocus = false
	m.display.SetDisplayFocused(false)
	m.input.Blur()
	m.modelSelector.SetHasFocus(false)
	m.queueManager.SetHasFocus(false)
	m.themeSelector.SetHasFocus(false)
	m.display.updateContent()
	return m, nil
}

// handleFocus handles gain of application focus.
func (m *Terminal) handleFocus() (tea.Model, tea.Cmd) {
	m.hasFocus = true

	m.modelSelector.SetHasFocus(true)
	m.queueManager.SetHasFocus(true)
	m.themeSelector.SetHasFocus(true)

	if m.modelSelector.IsOpen() {
		m.display.updateContent()
		return m, nil
	}

	if m.queueManager.IsOpen() {
		m.display.updateContent()
		return m, nil
	}

	if m.themeSelector.IsOpen() {
		m.display.updateContent()
		return m, nil
	}

	if m.focusedWindow == focusDisplay {
		m.focusDisplay()
	} else {
		m.focusInput()
	}
	m.display.updateContent()

	return m, nil
}

// ============================================================================
// External Editor
// ============================================================================

// editorFinishedMsg is sent when external editor closes (for input)
type editorFinishedMsg struct {
	content string
	err     error
}

// displayEditorFinishedMsg is sent when external editor closes (for display window viewing)
type displayEditorFinishedMsg struct {
	err error
}

// editorStartMsg is sent to trigger actual editor execution (lazy temp file creation)
type editorStartMsg struct {
	editorCmd   string
	tmpFileName string
	forDisplay  bool // true if opening display window content (don't populate input)
}

// FileEditorFinishedMsg is sent when external editor closes for a specific file
type FileEditorFinishedMsg struct {
	Path string
	Err  error
}

// Editor handles external editor operations
type Editor struct {
	tempFilePrefix string
	content        string
}

// NewEditor creates a new editor handler
func NewEditor() *Editor {
	return &Editor{
		tempFilePrefix: "alayacore-input-*.txt",
	}
}

// Open opens an external editor for multi-line input.
// The temp file is created lazily when the command executes, not during construction.
func (e *Editor) Open(currentContent string) tea.Cmd {
	editorCmd := getEditorCommand(os.Getenv("EDITOR"))

	if editorCmd == "" {
		return func() tea.Msg {
			return editorFinishedMsg{content: "", err: fmt.Errorf("no editor found (tried: vim, vi, nano)")}
		}
	}

	// Store content for lazy temp file creation
	e.content = currentContent

	// Return a command that creates the temp file and runs the editor
	return func() tea.Msg {
		return editorStartMsg{
			editorCmd:   editorCmd,
			tmpFileName: "", // Will be created in handleEditorStart
			forDisplay:  false,
		}
	}
}

// OpenForDisplay opens an external editor to view display window content.
// Unlike Open, this does not populate the input box when the editor closes.
func (e *Editor) OpenForDisplay(content string) tea.Cmd {
	editorCmd := getEditorCommand(os.Getenv("EDITOR"))

	if editorCmd == "" {
		return func() tea.Msg {
			return displayEditorFinishedMsg{err: fmt.Errorf("no editor found (tried: vim, vi, nano)")}
		}
	}

	// Store content for lazy temp file creation
	e.content = content

	// Return a command that creates the temp file and runs the editor
	return func() tea.Msg {
		return editorStartMsg{
			editorCmd:   editorCmd,
			tmpFileName: "", // Will be created in handleEditorStart
			forDisplay:  true,
		}
	}
}

// createTempFile creates a temp file with the editor content.
// This is called lazily when the editor is actually executed.
func (e *Editor) createTempFile() (string, error) {
	tmpFile, err := os.CreateTemp("", e.tempFilePrefix)
	if err != nil {
		return "", err
	}
	tmpFileName := tmpFile.Name()

	if e.content != "" {
		if _, err := tmpFile.WriteString(e.content); err != nil {
			tmpFile.Close()
			os.Remove(tmpFileName)
			return "", err
		}
	}
	tmpFile.Close()

	return tmpFileName, nil
}

// OpenFile opens an external editor for a specific file path
func (e *Editor) OpenFile(path string) tea.Cmd {
	editorCmd := getEditorCommand(os.Getenv("EDITOR"))

	if editorCmd == "" {
		return func() tea.Msg {
			return FileEditorFinishedMsg{Path: path, Err: fmt.Errorf("no editor found (tried: vim, vi, nano)")}
		}
	}

	cmd := exec.Command(editorCmd, path)

	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return FileEditorFinishedMsg{Path: path, Err: err}
	})
}

// FormatEditorContent formats editor content for preview in the input field
func FormatEditorContent(content string) string {
	lineCount := strings.Count(content, "\n") + 1

	// For single-line content, show it just like regular user input (no suffix)
	if lineCount == 1 {
		return content
	}

	// For multi-line content, show summary with line count and preview
	preview := strings.Fields(content)
	var previewText string
	switch {
	case len(preview) > 0 && len(preview[0]) > 20:
		previewText = preview[0][:20] + "..."
	case len(preview) > 0:
		previewText = preview[0]
	default:
		previewText = "(empty)"
	}
	return fmt.Sprintf("[%d lines] %s (press Enter to send)", lineCount, previewText)
}

// getEditorCommand returns the editor command to use
func getEditorCommand(editorCmd string) string {
	if editorCmd != "" {
		return editorCmd
	}

	for _, editor := range []string{"vim", "vi", "nano"} {
		path, err := exec.LookPath(editor)
		if err == nil {
			return path
		}
	}

	return ""
}

// hasEditorPrefix checks if the value has an editor content prefix.
func hasEditorPrefix(value string) bool {
	return len(value) > 0 && value[0] == '['
}
