package terminal

import (
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	agentpkg "github.com/wallacegibbon/coreclaw/internal/agent"
	"github.com/wallacegibbon/coreclaw/internal/app"
	"github.com/wallacegibbon/coreclaw/internal/stream"
)

// TerminalAdaptor is a terminal adaptor with a Terminal interface
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
	)

	t := NewTerminal(session, terminalOutput, inputStream, a.sessionFile)

	p := tea.NewProgram(t, tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout))
	p.Run()
}

// colorizeWelcomeText applies gradient coloring to the ASCII art
func colorizeWelcomeText(text string) string {
	lines := strings.Split(text, "\n")
	colors := []string{
		"#cba6f7",
		"#f38ba8",
		"#f9e2af",
		"#a6e3a1",
		"#89d4fa",
		"#cba6f7",
	}

	var result strings.Builder
	for i, line := range lines {
		if i < len(colors) && line != "" {
			style := lipgloss.NewStyle().Foreground(lipgloss.Color(colors[i]))
			result.WriteString(style.Render(line))
		} else {
			result.WriteString(line)
		}
		if i < len(lines)-1 {
			result.WriteString("\n")
		}
	}
	return result.String()
}

// Terminal is the main Terminal model that composes all components
type Terminal struct {
	session        *agentpkg.Session
	terminalOutput *terminalOutput
	streamInput    *stream.ChanInput

	display DisplayModel
	todo    TodoModel
	input   InputModel
	status  StatusModel

	quitting            bool
	confirmDialog       bool
	cancelConfirmDialog bool
	cancelFromCommand   bool
	focusedWindow       string
	windowWidth         int
	windowHeight        int
	sessionFile         string
	styles              *Styles
}

// NewTerminal creates a new Terminal model
func NewTerminal(session *agentpkg.Session, terminalOutput *terminalOutput, inputStream *stream.ChanInput, sessionFile string) *Terminal {
	styles := DefaultStyles()

	m := &Terminal{
		session:        session,
		terminalOutput: terminalOutput,
		streamInput:    inputStream,
		display:        NewDisplayModel(terminalOutput.windowBuffer, styles),
		todo:           NewTodoModel(styles),
		input:          NewInputModel(styles),
		status:         NewStatusModel(styles),
		windowWidth:    80,
		styles:         styles,
		focusedWindow:  "input",
		sessionFile:    sessionFile,
	}

	return m
}

// Init initializes the Terminal
func (m *Terminal) Init() tea.Cmd {
	return nil
}

type tickMsg struct{}

// Update handles messages
func (m *Terminal) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	select {
	case <-m.terminalOutput.updateChan:
		m.display.updateContent()
		m.status.SetStatus(m.terminalOutput.status)
		m.todo.SetTodos(m.terminalOutput.todos)
		m.updateDisplayHeight()
	default:
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	case tea.WindowSizeMsg:
		return m.handleWindowSize(msg)
	case tickMsg:
		m.display.updateContent()
		m.status.SetStatus(m.terminalOutput.status)
		m.todo.SetTodos(m.terminalOutput.todos)
		if m.terminalOutput.inProgress {
			return m, tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
				return tickMsg{}
			})
		}
		return m, nil
	case editorFinishedMsg:
		if msg.err != nil {
			m.terminalOutput.AppendError("Editor error: %v", msg.err)
		} else if msg.content != "" {
			m.input.editorContent = msg.content
			m.input.SetValue(FormatEditorContent(msg.content))
			m.input.CursorEnd()
			m.input.Focus()
		}
		return m, nil
	}

	m.input.updateFromMsg(msg)
	return m, nil
}

// handleWindowSize handles window resize events
func (m *Terminal) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.windowWidth = msg.Width
	m.windowHeight = msg.Height
	m.terminalOutput.SetWindowWidth(max(0, msg.Width))
	m.display.SetWidth(max(0, msg.Width))
	m.input.SetWidth(max(0, msg.Width))
	m.todo.SetWidth(max(0, msg.Width))
	m.status.SetWidth(max(0, msg.Width))
	m.display.centerWelcomeText()
	m.updateDisplayHeight()
	return m, nil
}

// handleKeyMsg handles keyboard input
func (m *Terminal) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
		m.input.Focus()
	} else {
		m.focusedWindow = "display"
		m.input.Blur()
	}
}

// handleDisplayKeys handles key events when display window is focused
func (m *Terminal) handleDisplayKeys(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "j":
		m.display.scrollDown(1)
		return nil, true
	case "k":
		m.display.ScrollUp(1)
		return nil, true
	case "G":
		m.display.GotoBottom()
		return nil, true
	case "g":
		m.display.GotoTop()
		return nil, true
	case "ctrl+d":
		m.display.scrollDown(m.display.GetHeight() / 2)
		return nil, true
	case "ctrl+u":
		m.display.ScrollUp(m.display.GetHeight() / 2)
		return nil, true
	case ":":
		m.focusedWindow = "input"
		m.input.Focus()
		m.input.SetValue(":")
		m.input.CursorEnd()
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
		if m.focusedWindow == "input" {
			return nil, true
		}
	case "ctrl+s":
		return m.submitCommand("save", false), true
	case "ctrl+o":
		return m.input.OpenEditor(), true
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
		if command == "quit" || command == "exit" {
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

	return tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg{}
	})
}

func (m *Terminal) submitCommand(command string, clearInput bool) tea.Cmd {
	m.streamInput.EmitTLV(stream.TagUserText, ":"+command)
	if clearInput {
		m.input.SetValue("")
	}
	return tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg{}
	})
}

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

	v := tea.NewView(sb.String())
	v.AltScreen = true
	return v
}

var (
	_ tea.Model = (*Terminal)(nil)
)
