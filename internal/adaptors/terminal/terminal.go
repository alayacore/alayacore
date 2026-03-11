package terminal

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	agentpkg "github.com/wallacegibbon/alayacore/internal/agent"
	"github.com/wallacegibbon/alayacore/internal/app"
	"github.com/wallacegibbon/alayacore/internal/stream"
)

// --- Adaptor (entry point) ---

// TerminalAdaptor starts the TUI; use from main/app.
type TerminalAdaptor struct {
	Config      *app.Config
	sessionFile string
}

// NewTerminalAdaptor creates a new Terminal adaptor
func NewTerminalAdaptor(cfg *app.Config) *TerminalAdaptor {
	return &TerminalAdaptor{
		Config:      cfg,
		sessionFile: "",
	}
}

// SetSessionFile sets the session file path
func (a *TerminalAdaptor) SetSessionFile(sessionFile string) {
	a.sessionFile = sessionFile
}

// Start runs the Terminal
func (a *TerminalAdaptor) Start() {
	inputStream := stream.NewChanInput(10)
	terminalOutput := NewTerminalOutput()
	var session *agentpkg.Session
	session, a.sessionFile = agentpkg.LoadOrNewSession(
		a.Config.Model,
		a.Config.AgentTools,
		a.Config.SystemPrompt,
		a.Config.Cfg.BaseURL,
		a.Config.Cfg.ModelName,
		inputStream,
		terminalOutput,
		a.sessionFile,
		a.Config.Cfg.ContextLimit,
	)

	t := NewTerminal(session, terminalOutput, inputStream, a.sessionFile, a.Config)

	// Initialize model selector from session's model manager
	t.modelSelector.LoadFromManager(session.ModelManager)
	// Set initial model from CLI if no active model
	session.ModelManager.SetInitialModel(
		a.Config.Cfg.ProviderType,
		a.Config.Cfg.BaseURL,
		a.Config.Cfg.APIKey,
		a.Config.Cfg.ModelName,
	)

	p := tea.NewProgram(t, tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout))
	p.Run()
}

// --- Terminal model ---

// Terminal is the main Bubble Tea model; composes display, input, todo, status.
type Terminal struct {
	session     *agentpkg.Session
	out         *outputWriter
	streamInput *stream.ChanInput
	appConfig   *app.Config

	display       DisplayModel
	todo          TodoModel
	input         InputModel
	status        StatusModel
	modelSelector *ModelSelector

	quitting            bool
	confirmDialog       bool
	cancelConfirmDialog bool
	cancelFromCommand   bool
	focusedWindow       string
	windowWidth         int
	windowHeight        int
	sessionFile         string
	styles              *Styles
	hasFocus            bool // tracks whether the terminal has application focus
}

// NewTerminal creates a new Terminal model
func NewTerminal(session *agentpkg.Session, out *outputWriter, inputStream *stream.ChanInput, sessionFile string, appCfg *app.Config) *Terminal {
	styles := DefaultStyles()

	m := &Terminal{
		session:       session,
		out:           out,
		streamInput:   inputStream,
		appConfig:     appCfg,
		display:       NewDisplayModel(out.windowBuffer, styles),
		todo:          NewTodoModel(styles),
		input:         NewInputModel(styles),
		status:        NewStatusModel(styles),
		modelSelector: NewModelSelector(styles),
		windowWidth:   DefaultWidth,
		styles:        styles,
		focusedWindow: "input",
		sessionFile:   sessionFile,
		hasFocus:      true, // program starts with focus
	}

	return m
}

// Init initializes the Terminal
func (m *Terminal) Init() tea.Cmd {
	return nil
}

// --- Message handling ---

type tickMsg struct{}

// Update routes messages; KeyMsg first for responsive input (see PERFORMANCE_ANALYSIS.md).
func (m *Terminal) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Process user-facing messages FIRST to avoid blocking keyboard input.
	// Display updates run after so keypress returns immediately.
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	case tea.WindowSizeMsg:
		return m.handleWindowSize(msg)
	case tickMsg:
		// Drain pending display updates (non-blocking) so streaming content appears
		select {
		case <-m.out.updateChan:
			if m.out.windowBuffer.GetWindowCount() > 0 {
				m.status.SetStatus(m.out.status)
				m.todo.SetTodos(m.out.todos)
				m.updateDisplayHeight()
				if m.display.shouldFollow() {
					m.display.SetCursorToLastWindow()
				}
				m.display.updateContent()
			}
			// Check for model switch from model_set command
			if activeModel := m.out.GetActiveModel(); activeModel != nil {
				m.applyModelSwitch(activeModel)
			}
			// Update model selector if models changed
			if models := m.out.GetModels(); len(models) > 0 {
				m.modelSelector.LoadFromManager(m.session.ModelManager)
			}
		default:
			m.status.SetStatus(m.out.status)
			m.todo.SetTodos(m.out.todos)
		}
		if m.out.inProgress {
			return m, tea.Tick(TickInterval, func(t time.Time) tea.Msg {
				return tickMsg{}
			})
		}
		return m, nil
	case editorFinishedMsg:
		if msg.err != nil {
			m.out.AppendError("Editor error: %v", msg.err)
		} else if msg.content != "" {
			m.input.editorContent = msg.content
			m.input.SetValue(FormatEditorContent(msg.content))
			m.input.CursorEnd()
			m.input.Focus()
		}
		return m, nil
	case FileEditorFinishedMsg:
		// External editor finished for a specific file (e.g., model config)
		if msg.Err != nil {
			m.out.WriteNotify(fmt.Sprintf("Error editing file %s: %v", msg.Path, msg.Err))
		}
		// If it was the model config file, reload models
		if strings.Contains(msg.Path, "models.json") {
			m.streamInput.EmitTLV(stream.TagUserText, ":model_load")
		}
		return m, nil
	case tea.BlurMsg:
		// User switched away from this program (e.g., to another tmux window)
		m.hasFocus = false
		m.display.SetDisplayFocused(false)
		m.input.Blur()
		m.display.updateContent() // re-render without cursor highlight
		return m, nil
	case tea.FocusMsg:
		// User switched back to this program
		m.hasFocus = true
		// Restore focus to the previously focused window
		if m.focusedWindow == "display" {
			m.display.SetDisplayFocused(true)
		} else {
			m.input.Focus()
		}
		m.display.updateContent() // re-render with cursor if display focused
		return m, nil
	case tea.PasteMsg:
		// Handle paste - route to input
		m.input.updateFromMsg(msg)
		return m, nil
	}

	m.input.updateFromMsg(msg)
	return m, nil
}

// --- Key bindings ---

// handleWindowSize handles window resize events
func (m *Terminal) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.windowWidth = msg.Width
	m.windowHeight = msg.Height
	m.out.SetWindowWidth(max(0, msg.Width))
	m.display.SetWidth(max(0, msg.Width))
	m.input.SetWidth(max(0, msg.Width))
	m.todo.SetWidth(max(0, msg.Width))
	m.status.SetWidth(max(0, msg.Width))
	m.updateDisplayHeight()
	m.display.centerWelcomeText()
	return m, nil
}

// handleKeyMsg handles keyboard input
func (m *Terminal) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Handle model selector input when open
	if m.modelSelector.IsOpen() {
		key := msg.String()
		// Handle all keys in list mode
		m.modelSelector.HandleKey(key)
		// Check if a model was selected
		if m.modelSelector.ConsumeModelSelected() {
			m.switchToSelectedModel()
		}
		// Check if user wants to open model file
		if m.modelSelector.ConsumeOpenModelFile() {
			return m, m.openModelConfigFile()
		}
		// Check if user wants to reload models
		if m.modelSelector.ConsumeReloadModels() {
			m.streamInput.EmitTLV(stream.TagUserText, ":model_load")
		}
		return m, nil
	}

	if cmd, handled := m.handleConfirmDialog(msg); handled {
		return m, cmd
	}

	if msg.String() == "tab" {
		m.toggleFocus()
		return m, nil
	}

	if m.focusedWindow == "display" {
		if cmd, handled := m.handleDisplayKeys(msg); handled {
			return m, cmd
		}
	}

	if cmd, handled := m.handleGlobalKeys(msg); handled {
		return m, cmd
	}

	oldValue := m.input.Value()
	m.input.updateFromMsg(msg)
	newValue := m.input.Value()

	if m.input.editorContent != "" && oldValue != newValue && !strings.HasPrefix(oldValue, "[") {
		m.input.editorContent = ""
	}

	return m, nil
}

// handleConfirmDialog handles quit and cancel confirmation dialogs
func (m *Terminal) handleConfirmDialog(msg tea.KeyMsg) (tea.Cmd, bool) {
	if m.confirmDialog {
		switch msg.String() {
		case "y", "Y":
			m.quitting = true
			m.streamInput.Close()
			m.out.Close()
			return tea.Quit, true
		case "n", "N", "esc", "ctrl+c":
			m.confirmDialog = false
			m.input.SetValue("")
			return nil, true
		}
		return nil, true
	}

	if m.cancelConfirmDialog {
		switch msg.String() {
		case "y", "Y":
			m.cancelConfirmDialog = false
			if m.cancelFromCommand {
				m.input.SetValue("")
			}
			return m.submitCommand("cancel", m.cancelFromCommand), true
		case "n", "N", "esc", "ctrl+c":
			m.cancelConfirmDialog = false
			if m.cancelFromCommand {
				m.input.SetValue("")
			}
			return nil, true
		}
		return nil, true
	}

	return nil, false
}

// toggleFocus switches between display and input windows
func (m *Terminal) toggleFocus() {
	if m.focusedWindow == "display" {
		m.focusedWindow = "input"
		m.display.SetDisplayFocused(false)
		m.input.Focus()
	} else {
		m.focusedWindow = "display"
		m.display.SetDisplayFocused(true)
		m.input.Blur()
	}
	m.display.updateContent()
}

// handleDisplayKeys handles key events when display window is focused
func (m *Terminal) handleDisplayKeys(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "j":
		if m.display.MoveWindowCursorDown() {
			m.display.updateContent()
			m.display.EnsureCursorVisible()
		}
		return nil, true
	case "k":
		if m.display.MoveWindowCursorUp() {
			m.display.updateContent()
			m.display.EnsureCursorVisible()
		}
		return nil, true
	case "J":
		m.display.ScrollDown(1)
		return nil, true
	case "K":
		m.display.ScrollUp(1)
		return nil, true
	case "H":
		if m.display.MoveWindowCursorToTop() {
			m.display.updateContent()
		}
		return nil, true
	case "L":
		if m.display.MoveWindowCursorToBottom() {
			m.display.updateContent()
		}
		return nil, true
	case "M":
		if m.display.MoveWindowCursorToCenter() {
			m.display.updateContent()
		}
		return nil, true
	case "G":
		m.display.GotoBottom()
		m.display.SetCursorToLastWindow()
		m.display.updateContent()
		return nil, true
	case "g":
		m.display.GotoTop()
		m.display.SetWindowCursor(0)
		m.display.updateContent()
		return nil, true
	case ":":
		m.focusedWindow = "input"
		m.input.Focus()
		m.input.SetValue(":")
		m.input.CursorEnd()
		return nil, true
	case "space":
		if m.display.ToggleWindowWrap() {
			m.display.updateContent()
		}
		return nil, true
	}
	return nil, false
}

// handleGlobalKeys handles global keyboard shortcuts
func (m *Terminal) handleGlobalKeys(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+g":
		m.cancelConfirmDialog = true
		m.cancelFromCommand = false
		return nil, true
	case "ctrl+c":
		if m.focusedWindow == "input" {
			m.input.SetValue("")
			m.input.editorContent = ""
		}
		return nil, true
	case "ctrl+u":
		return nil, true
	case "ctrl+s":
		return m.submitCommand("save", false), true
	case "ctrl+o":
		return m.input.OpenEditor(), true
	case "ctrl+l":
		m.modelSelector.Open()
		return nil, true
	case "enter":
		return m.handleSubmit(), true
	}
	return nil, false
}

// handleSubmit processes the input when Enter is pressed
func (m *Terminal) handleSubmit() tea.Cmd {
	prompt := m.input.GetPrompt()
	m.input.editorContent = ""

	if prompt == "" {
		return nil
	}

	if command, found := strings.CutPrefix(prompt, ":"); found {
		if command == "quit" || command == "q" {
			m.confirmDialog = true
			return nil
		}
		if command == "cancel" {
			m.cancelConfirmDialog = true
			m.cancelFromCommand = true
			return nil
		}
		return m.submitCommand(command, true)
	}

	m.streamInput.EmitTLV(stream.TagUserText, prompt)
	m.input.SetValue("")

	return tea.Tick(SubmitTickDelay, func(t time.Time) tea.Msg {
		return tickMsg{}
	})
}

func (m *Terminal) submitCommand(command string, clearInput bool) tea.Cmd {
	m.streamInput.EmitTLV(stream.TagUserText, ":"+command)
	if clearInput {
		m.input.SetValue("")
	}
	return tea.Tick(SubmitTickDelay, func(t time.Time) tea.Msg {
		return tickMsg{}
	})
}

// switchToSelectedModel sends a model_set command to switch to the selected model
func (m *Terminal) switchToSelectedModel() {
	selectedModel := m.modelSelector.GetActiveModel()
	if selectedModel == nil {
		return
	}

	// Send model_set command to session instead of switching directly
	if selectedModel.ID != "" {
		m.streamInput.EmitTLV(stream.TagUserText, ":model_set "+selectedModel.ID)
	}
}

// openModelConfigFile opens the model config file with $EDITOR using shared Editor
func (m *Terminal) openModelConfigFile() tea.Cmd {
	path, err := ModelsConfigFile()
	if err != nil {
		return func() tea.Msg {
			return FileEditorFinishedMsg{Path: "", Err: err}
		}
	}

	return m.input.editor.OpenFile(path)
}

// applyModelSwitch applies a model switch from a model_set response
func (m *Terminal) applyModelSwitch(model *agentpkg.ModelConfig) {
	if model == nil || m.appConfig == nil {
		return
	}

	// Create new provider and model
	provider, err := app.CreateProvider(model.ProtocolType, model.APIKey, model.BaseURL, m.appConfig.Cfg.DebugAPI, m.appConfig.Cfg.Proxy)
	if err != nil {
		m.out.WriteNotify("Failed to create provider: " + err.Error())
		return
	}

	newModel, err := provider.LanguageModel(context.Background(), model.ModelName)
	if err != nil {
		m.out.WriteNotify("Failed to create language model: " + err.Error())
		return
	}

	// Switch the session to the new model
	m.session.SwitchModel(newModel, model.BaseURL, model.ModelName, m.appConfig.AgentTools, m.appConfig.SystemPrompt)

	// Show notification
	m.out.WriteNotify("Switched to model: " + model.Name + " (" + model.ModelName + ")")
}

// --- Layout ---

// updateDisplayHeight updates the display viewport height based on window size and todo visibility
func (m *Terminal) updateDisplayHeight() {
	m.display.UpdateHeightForTodos(m.windowHeight, m.todo.Count())
}

// View renders the Terminal
func (m *Terminal) View() tea.View {
	var sb strings.Builder

	sb.WriteString(m.display.View().Content)
	sb.WriteString("\n")

	todos := m.todo.RenderString()
	if todos != "" {
		todoInnerStyle := lipgloss.NewStyle().Width(max(0, m.windowWidth-4))
		todoBorderStyle := m.styles.TodoBorder.Padding(0, 1)
		sb.WriteString(todoBorderStyle.Render(todoInnerStyle.Render(todos)))
		sb.WriteString("\n")
	}

	confirmText := ""
	if m.confirmDialog {
		confirmText = "Confirm exit? Press y/n"
	} else if m.cancelConfirmDialog {
		confirmText = "Confirm cancel? Press y/n"
	}

	inputView := m.input.RenderWithBorder(m.confirmDialog || m.cancelConfirmDialog, confirmText)
	sb.WriteString(inputView)

	sb.WriteString("\n")
	sb.WriteString(m.status.RenderString())

	// Build the base content
	baseContent := sb.String()

	// Render model selector overlay if open (centered on screen, on top of base content)
	if m.modelSelector.IsOpen() {
		fullContent := m.modelSelector.RenderOverlay(baseContent, m.windowWidth, m.windowHeight)
		v := tea.NewView(fullContent)
		v.AltScreen = true
		v.ReportFocus = true
		return v
	}

	v := tea.NewView(baseContent)
	v.AltScreen = true
	v.ReportFocus = true // Enable focus/blur events when user switches applications
	return v
}

var (
	_ tea.Model = (*Terminal)(nil)
)
