package terminal

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/stream"
)

// Terminal is the main Bubble Tea model that composes display, input, and status components.
// It serves as the central coordinator for the terminal UI, managing:
//   - User input and keyboard shortcuts (delegated to keys.go)
//   - Command processing (delegated to commands.go)
//   - Display updates from the agent session
//   - Model selection and switching
//   - Window focus management
type Terminal struct {
	// Core components
	session     *agentpkg.Session
	out         *outputWriter
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
	out *outputWriter,
	inputStream *stream.ChanInput,
	appCfg *app.Config,
	initialWidth, initialHeight int,
) *Terminal {
	styles := DefaultStyles()

	m := &Terminal{
		session:       session,
		out:           out,
		streamInput:   inputStream,
		appConfig:     appCfg,
		display:       NewDisplayModel(out.windowBuffer, styles),
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
	return tea.Tick(TickInterval, func(t time.Time) tea.Msg {
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
	case <-m.out.updateChan:
		if m.out.windowBuffer.GetWindowCount() > 0 {
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
		tea.Tick(TickInterval, func(t time.Time) tea.Msg {
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
		m.streamInput.EmitTLV(stream.TagTextUser, ":model_load")
	}

	return m, nil
}

// handleBlur handles loss of application focus.
func (m *Terminal) handleBlur() (tea.Model, tea.Cmd) {
	m.hasFocus = false
	m.display.SetDisplayFocused(false)
	m.input.Blur()
	m.display.updateContent()
	return m, nil
}

// handleFocus handles gain of application focus.
func (m *Terminal) handleFocus() (tea.Model, tea.Cmd) {
	m.hasFocus = true

	// If model selector is open, don't restore focus to main input
	// The model selector maintains its own focus state
	if m.modelSelector.IsOpen() {
		m.display.updateContent()
		return m, nil
	}

	// Restore focus to the previously focused window
	if m.focusedWindow == "display" {
		m.display.SetDisplayFocused(true)
	} else {
		m.input.Focus()
	}
	m.display.updateContent()

	return m, nil
}

// updateDisplayHeight updates the display viewport height based on window size.
func (m *Terminal) updateDisplayHeight() {
	m.display.UpdateHeight(m.windowHeight)
}

// updateStatusWithQueue updates the status bar with queue count
func (m *Terminal) updateStatusWithQueue() {
	status := m.out.GetStatus()
	queueCount := m.out.GetQueueCount()
	if queueCount > 0 {
		if status != "" {
			status = fmt.Sprintf("Queued: %d | %s", queueCount, status)
		} else {
			status = fmt.Sprintf("Queued: %d", queueCount)
		}
	}
	m.status.SetStatus(status)
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
