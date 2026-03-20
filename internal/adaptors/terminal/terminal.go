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
	"golang.org/x/term"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/stream"
)

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
// Terminal Model
// ============================================================================

// Terminal is the main Bubble Tea model that composes display, input, and status components.
// It serves as the central coordinator for the terminal UI, managing:
//   - User input and keyboard shortcuts (delegated to keybinds.go)
//   - Display updates from the agent session
//   - Model selection and switching
//   - Window focus management
type Terminal struct {
	// Core components
	session     *agentpkg.Session
	out         OutputWriter
	streamInput *stream.ChanInput
	appConfig   *app.Config

	// UI components
	display       DisplayModel
	input         InputModel
	status        StatusModel
	modelSelector *ModelSelector
	queueManager  *QueueManager

	// State
	quitting            bool
	confirmDialog       bool
	cancelConfirmDialog bool
	cancelFromCommand   bool
	focusedWindow       string // "input" or "display"
	windowWidth         int
	windowHeight        int
	styles              *Styles
	hasFocus            bool // tracks whether the terminal has application focus
}

// NewTerminal creates a new Terminal model with all components initialized.
func NewTerminal(
	session *agentpkg.Session,
	out OutputWriter,
	inputStream *stream.ChanInput,
	appCfg *app.Config,
	initialWidth, initialHeight int,
) *Terminal {
	return NewTerminalWithTheme(session, out, inputStream, appCfg, initialWidth, initialHeight, DefaultTheme())
}

// NewTerminalWithTheme creates a new Terminal model with a custom theme.
func NewTerminalWithTheme(
	session *agentpkg.Session,
	out OutputWriter,
	inputStream *stream.ChanInput,
	appCfg *app.Config,
	initialWidth, initialHeight int,
	theme *Theme,
) *Terminal {
	styles := NewStyles(theme)

	m := &Terminal{
		session:       session,
		out:           out,
		streamInput:   inputStream,
		appConfig:     appCfg,
		display:       NewDisplayModel(out.WindowBuffer(), styles),
		input:         NewInputModel(styles),
		status:        NewStatusModel(styles),
		modelSelector: NewModelSelector(styles),
		queueManager:  NewQueueManager(styles),
		windowWidth:   initialWidth,
		windowHeight:  initialHeight,
		styles:        styles,
		focusedWindow: "input",
		hasFocus:      true,
	}

	// Initialize component widths
	m.display.SetWidth(initialWidth)
	m.input.SetWidth(initialWidth)
	m.status.SetWidth(initialWidth)
	m.modelSelector.SetSize(initialWidth, initialHeight)
	m.queueManager.SetSize(initialWidth, initialHeight)
	m.updateDisplayHeight()

	return m
}

// Init starts the periodic tick loop for processing session updates.
func (m *Terminal) Init() tea.Cmd {
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

	case editorFinishedMsg:
		return m.handleEditorFinished(msg)

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
	m.status.SetWidth(max(0, msg.Width))
	m.modelSelector.SetSize(msg.Width, msg.Height)
	m.queueManager.SetSize(msg.Width, msg.Height)
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
			m.updateStatusWithQueue()
			m.updateDisplayHeight()
			if m.display.shouldFollow() {
				m.display.SetCursorToLastWindow()
			}
			m.display.updateContent()
		}

		// Update model selector if models changed
		cmd = m.modelSelector.LoadModels(m.out.GetModels(), m.out.GetActiveModelID())

		// Check for queue items update
		if queueItems := m.out.GetQueueItems(); queueItems != nil {
			m.queueManager.SetItems(queueItems)
			// Update display to show new items
			m.display.updateContent()
		}

	default:
		m.updateStatusWithQueue()
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
		m.input.editorContent = msg.content
		m.input.SetValue(FormatEditorContent(msg.content))
		m.input.CursorEnd()
		m.input.Focus()
	}
	return m, nil
}

// handleFileEditorFinished handles completion of file editing (e.g., model config).
func (m *Terminal) handleFileEditorFinished(msg FileEditorFinishedMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		m.out.WriteNotify(fmt.Sprintf("Error editing file %s: %v", msg.Path, msg.Err))
	}

	// Reload models if the model config file was edited
	if strings.HasSuffix(msg.Path, "model.conf") {
		_ = m.streamInput.EmitTLV(stream.TagTextUser, ":model_load") //nolint:errcheck // best-effort input
	}

	return m, nil
}

// updateDisplayHeight updates the display viewport height based on window size.
func (m *Terminal) updateDisplayHeight() {
	m.display.UpdateHeight(m.windowHeight)
}

// updateStatusWithQueue updates the status bar with queue count
func (m *Terminal) updateStatusWithQueue() {
	contextStatus := m.out.GetStatus()
	queueCount := m.out.GetQueueCount()

	// Add steps info if we're in progress
	currentStep := m.out.GetCurrentStep()
	maxSteps := m.out.GetMaxSteps()
	inProgress := m.out.IsInProgress()
	lastCurrentStep, lastMaxSteps := m.out.GetLastStepInfo()

	// Build status segments - each rendered separately with appropriate colors
	var segments []string

	// Queue segment - prefix dimmed, count highlighted
	if queueCount > 0 {
		prefix := m.styles.Status.Render("Queued(Ctrl-Q):")
		count := m.styles.Status.Foreground(m.styles.ColorAccent).Render(fmt.Sprintf("%d", queueCount))
		segments = append(segments, prefix+" "+count)
	}

	// Steps segment (always show)
	var stepsPart string
	if lastMaxSteps > 0 {
		stepsPart = fmt.Sprintf("Steps: %d/%d", lastCurrentStep, lastMaxSteps)
	} else {
		stepsPart = fmt.Sprintf("Steps: %d/%d", currentStep, maxSteps)
	}
	segments = append(segments, m.styles.Status.Render(stepsPart))

	// Context segment (dimmed)
	if contextStatus != "" {
		segments = append(segments, m.styles.Status.Render(contextStatus))
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

	m.status.SetStatus(status)
	m.status.SetInProgress(inProgress)
}

// View renders the complete terminal UI.
func (m *Terminal) View() tea.View {
	var sb strings.Builder

	// Display area
	sb.WriteString(m.display.View().Content)
	sb.WriteString("\n")

	// Input area with optional confirmation dialog
	confirmText := ""
	if m.confirmDialog {
		confirmText = "Confirm exit? Press y/n"
	} else if m.cancelConfirmDialog {
		confirmText = "Confirm cancel? Press y/n"
	}
	sb.WriteString(m.input.RenderWithBorder(m.confirmDialog || m.cancelConfirmDialog, confirmText))

	// Status bar
	sb.WriteString("\n")
	sb.WriteString(m.status.RenderString())

	baseContent := sb.String()

	// Render model selector overlay if open
	if m.modelSelector.IsOpen() {
		fullContent := m.modelSelector.RenderOverlay(baseContent, m.windowWidth, m.windowHeight)
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

// Ensure Terminal implements tea.Model
var _ tea.Model = (*Terminal)(nil)

// ============================================================================
// Terminal Adaptor (entry point for main/app)
// ============================================================================

// Adaptor starts the TUI; use from main/app.
type Adaptor struct {
	Config    *app.Config
	ThemePath string
}

// NewAdaptor creates a new Terminal adaptor.
func NewAdaptor(cfg *app.Config) *Adaptor {
	return &Adaptor{
		Config: cfg,
	}
}

// NewAdaptorWithTheme creates a new Terminal adaptor with a custom theme path.
func NewAdaptorWithTheme(cfg *app.Config, themePath string) *Adaptor {
	return &Adaptor{
		Config:    cfg,
		ThemePath: themePath,
	}
}

// Start runs the Terminal program.
func (a *Adaptor) Start() {
	// Load theme from config paths
	theme := LoadThemeFromPaths(a.ThemePath)
	styles := NewStyles(theme)

	inputStream := stream.NewChanInput(10)
	terminalOutput := NewTerminalOutput(styles)

	// Get terminal size before loading session (so session loads with correct dimensions)
	initialWidth, initialHeight := getTerminalSize()
	terminalOutput.SetWindowWidth(initialWidth)

	// Load session synchronously before starting the UI
	session, _ := agentpkg.LoadOrNewSession(
		a.Config.AgentTools,
		a.Config.SystemPrompt,
		a.Config.ExtraSystemPrompt,
		a.Config.MaxSteps,
		inputStream,
		terminalOutput,
		a.Config.Cfg.Session,
		a.Config.Cfg.ModelConfig,
		a.Config.Cfg.RuntimeConfig,
		a.Config.Cfg.DebugAPI,
		a.Config.Cfg.Proxy,
	)

	// Check if we have any models available.
	if !terminalOutput.HasModels() {
		// Print error to stderr and exit
		modelPath := terminalOutput.GetModelConfigPath()
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Error: No models configured.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Please edit the model config file:")
		fmt.Fprintf(os.Stderr, "  %s\n", modelPath)
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Example format:")
		fmt.Fprintln(os.Stderr, `name: "OpenAI GPT-4o"
protocol_type: "openai"
base_url: "https://api.openai.com/v1"
api_key: "your-api-key"
model_name: "gpt-4o"
context_limit: 128000
---
name: "Ollama GPT-OSS:20B"
protocol_type: "anthropic"
base_url: "https://127.0.0.1:11434"
api_key: "your-api-key"
model_name: "gpt-oss:20b"
context_limit: 32768`)
		fmt.Fprintln(os.Stderr, "")
		os.Exit(1)
	}

	// Create terminal with loaded session, initial window size, and theme
	t := NewTerminalWithTheme(session, terminalOutput, inputStream, a.Config, initialWidth, initialHeight, theme)

	// Create and run the program
	p := tea.NewProgram(t, tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout))
	_, _ = p.Run() //nolint:errcheck // terminal program run, error not critical
}

// getTerminalSize returns the current terminal size, or defaults if not a TTY.
func getTerminalSize() (width, height int) {
	if term.IsTerminal(int(os.Stdout.Fd())) {
		w, h, err := term.GetSize(int(os.Stdout.Fd()))
		if err == nil {
			return w, h
		}
	}
	return DefaultWidth, DefaultHeight
}

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

// restoreFocusAfterSelector restores focus after model selector closes.
func (m *Terminal) restoreFocusAfterSelector() {
	if m.focusedWindow == focusDisplay {
		m.display.SetDisplayFocused(true)
	} else {
		m.input.Focus()
	}
	m.display.updateContent()
}

// openQueueManager opens the queue manager UI.
func (m *Terminal) openQueueManager() {
	_ = m.streamInput.EmitTLV(stream.TagTextUser, ":taskqueue_get_all")
	m.queueManager.Open()
	m.input.Blur()
	m.display.SetDisplayFocused(false)
	m.display.updateContent()
}

// restoreFocusAfterQueueManager restores focus after queue manager closes.
func (m *Terminal) restoreFocusAfterQueueManager() {
	if m.focusedWindow == focusDisplay {
		m.display.SetDisplayFocused(true)
	} else {
		m.input.Focus()
	}
	m.display.updateContent()
}

// handleBlur handles loss of application focus.
func (m *Terminal) handleBlur() (tea.Model, tea.Cmd) {
	m.hasFocus = false
	m.display.SetDisplayFocused(false)
	m.input.Blur()
	m.modelSelector.SetHasFocus(false)
	m.queueManager.SetHasFocus(false)
	m.display.updateContent()
	return m, nil
}

// handleFocus handles gain of application focus.
func (m *Terminal) handleFocus() (tea.Model, tea.Cmd) {
	m.hasFocus = true

	m.modelSelector.SetHasFocus(true)
	m.queueManager.SetHasFocus(true)

	if m.modelSelector.IsOpen() {
		m.display.updateContent()
		return m, nil
	}

	if m.queueManager.IsOpen() {
		m.display.updateContent()
		return m, nil
	}

	if m.focusedWindow == focusDisplay {
		m.display.SetDisplayFocused(true)
	} else {
		m.input.Focus()
	}
	m.display.updateContent()

	return m, nil
}

// ============================================================================
// Status Bar
// ============================================================================

// StatusModel shows the status bar (token usage, etc).
type StatusModel struct {
	status     string
	inProgress bool
	styles     *Styles
	width      int
}

// NewStatusModel creates a new status model
func NewStatusModel(styles *Styles) StatusModel {
	return StatusModel{
		status: "",
		styles: styles,
		width:  DefaultWidth,
	}
}

// Init initializes the status model
func (m StatusModel) Init() tea.Cmd {
	return nil
}

// Update handles messages for the status model
func (m StatusModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if windowMsg, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = windowMsg.Width
	}
	return m, nil
}

// View renders the status bar
func (m StatusModel) View() tea.View {
	return tea.NewView(m.styles.Status.Render(m.status))
}

// SetStatus updates the status text
func (m *StatusModel) SetStatus(status string) {
	m.status = status
}

// SetInProgress updates the in-progress state
func (m *StatusModel) SetInProgress(inProgress bool) {
	m.inProgress = inProgress
}

// GetStatus returns the current status
func (m StatusModel) GetStatus() string {
	return m.status
}

// SetWidth sets the width for rendering
func (m *StatusModel) SetWidth(width int) {
	m.width = width
}

// RenderString returns the rendered status string
func (m StatusModel) RenderString() string {
	var indicator string
	if m.inProgress {
		indicator = m.styles.Status.Foreground(m.styles.ColorSuccess).Render("•")
	} else {
		indicator = m.styles.Status.Foreground(m.styles.ColorDim).Render("·")
	}

	if m.status != "" {
		padding := m.styles.Status.Padding(0, 2)
		return padding.Render(indicator + " " + m.status)
	}
	return m.styles.Status.Padding(0, 2).Render(indicator)
}

// ============================================================================
// External Editor
// ============================================================================

// editorFinishedMsg is sent when external editor closes
type editorFinishedMsg struct {
	content string
	err     error
}

// FileEditorFinishedMsg is sent when external editor closes for a specific file
type FileEditorFinishedMsg struct {
	Path string
	Err  error
}

// Editor handles external editor operations
type Editor struct {
	tempFilePrefix string
}

// NewEditor creates a new editor handler
func NewEditor() *Editor {
	return &Editor{
		tempFilePrefix: "alayacore-input-*.txt",
	}
}

// Open opens an external editor for multi-line input
func (e *Editor) Open(currentContent string) tea.Cmd {
	editorCmd := getEditorCommand(os.Getenv("EDITOR"))

	if editorCmd == "" {
		return func() tea.Msg {
			return editorFinishedMsg{content: "", err: fmt.Errorf("no editor found (tried: vim, vi, nano)")}
		}
	}

	tmpFile, err := os.CreateTemp("", e.tempFilePrefix)
	if err != nil {
		return func() tea.Msg {
			return editorFinishedMsg{content: "", err: err}
		}
	}

	tmpFileName := tmpFile.Name()

	if currentContent != "" {
		if _, err := tmpFile.WriteString(currentContent); err != nil {
			tmpFile.Close()
			os.Remove(tmpFileName)
			return func() tea.Msg {
				return editorFinishedMsg{content: "", err: err}
			}
		}
	}
	tmpFile.Close()

	cmd := exec.Command(editorCmd, tmpFileName)

	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer os.Remove(tmpFileName)

		if err != nil {
			return editorFinishedMsg{content: "", err: err}
		}

		content, readErr := os.ReadFile(tmpFileName)
		if readErr != nil {
			return editorFinishedMsg{content: "", err: readErr}
		}

		return editorFinishedMsg{content: string(content), err: nil}
	})
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

var _ tea.Model = (*StatusModel)(nil)
