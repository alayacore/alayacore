package terminal

import (
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wallacegibbon/coreclaw/internal/adaptors/common"
	agentpkg "github.com/wallacegibbon/coreclaw/internal/agent"
	"github.com/wallacegibbon/coreclaw/internal/app"
	"github.com/wallacegibbon/coreclaw/internal/stream"
	"github.com/wallacegibbon/coreclaw/internal/todo"
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
	// Create input and output streams
	inputStream := stream.NewChanInput(10)
	terminalOutput := NewTerminalOutput()
	// Load or create session
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
		"#cba6f7", // Purple
		"#f38ba8", // Red/pink
		"#f9e2af", // Yellow
		"#a6e3a1", // Green
		"#89d4fa", // Cyan
		"#cba6f7", // Purple
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

// Terminal is the main Terminal model
type Terminal struct {
	session             *agentpkg.Session
	terminalOutput      *terminalOutput
	streamInput         *stream.ChanInput
	display             viewport.Model
	input               textinput.Model
	quitting            bool
	confirmDialog       bool
	cancelConfirmDialog bool
	cancelFromCommand   bool          // true if cancel triggered by /cancel command, false if by Ctrl+G
	focusedWindow       string        // "display" or "input"
	userScrolledAway    bool          // true after user manually scrolls up
	windowWidth         int           // actual window width
	windowHeight        int           // actual window height
	editorContent       string        // content from external editor with newlines preserved
	showingWelcome      bool          // true while welcome text is still displayed
	welcomeText         string        // colored welcome text for comparison
	sessionFile         string        // session file path for saving on quit
	todos               todo.TodoList // cached todos for display
	styles              *Styles       // UI styles
	editor              *Editor       // external editor handler
}

// NewTerminal creates a new Terminal model
func NewTerminal(session *agentpkg.Session, terminalOutput *terminalOutput, inputStream *stream.ChanInput, sessionFile string) *Terminal {
	input := textinput.New()
	input.Placeholder = "Enter your prompt..."
	input.Focus()
	input.Prompt = "> "
	input.SetWidth(76)

	styles := DefaultStyles()
	coloredWelcome := colorizeWelcomeText(common.WelcomeText)
	display := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))

	m := &Terminal{
		session:        session,
		terminalOutput: terminalOutput,
		streamInput:    inputStream,
		display:        display,
		input:          input,
		windowWidth:    80,
		styles:         styles,
		focusedWindow:  "input",
		welcomeText:    coloredWelcome,
		sessionFile:    sessionFile,
		editor:         NewEditor(),
	}

	hasExistingContent := false // window buffer starts empty
	if hasExistingContent {
		existingContent := terminalOutput.windowBuffer.GetAll()
		wrapped := lipgloss.Wrap(existingContent, display.Width(), " ")
		newlineCount := strings.Count(wrapped, "\n")
		display.SetContent(wrapped)
		display.SetYOffset(max(0, newlineCount-display.Height()))
		m.showingWelcome = false
	} else {
		display.SetContent(coloredWelcome)
		m.showingWelcome = true
	}

	return m
}

// updateTodos updates the cached todos from terminalOutput
func (m *Terminal) updateTodos() {
	m.todos = m.terminalOutput.todos
	m.updateDisplayHeight()
}

// updateDisplayHeight updates the display viewport height based on window size and todo visibility
func (m *Terminal) updateDisplayHeight() {
	// Base height: total height minus input box (3 lines) and status bar (1 line)
	height := m.windowHeight - 4

	// Subtract todo box height if visible (header + todos + border)
	if len(m.todos) > 0 {
		// Header line + each todo item + border lines (2 for top/bottom)
		height -= (1 + len(m.todos) + 2)
	}

	newHeight := max(0, height)
	oldHeight := m.display.Height()

	// Only adjust YOffset if height actually changes
	if oldHeight != newHeight {
		// Get raw content and word-wrap to count lines
		rawContent := m.terminalOutput.windowBuffer.GetAll()
		totalLines := max(1, strings.Count(rawContent, "\n")+1)

		topLine := m.display.YOffset()
		var newTopLine int

		if m.userScrolledAway {
			// User manually scrolled up: keep top line constant
			newTopLine = topLine
		} else {
			// Auto-scroll mode: keep bottom line constant
			bottomLine := topLine + oldHeight - 1
			newTopLine = bottomLine - newHeight + 1
		}

		// Clamp to ensure visible region stays within content
		maxTopLine := max(0, totalLines-newHeight)
		if newTopLine > maxTopLine {
			newTopLine = maxTopLine
		}
		if newTopLine < 0 {
			newTopLine = 0
		}

		m.display.SetHeight(newHeight)
		m.display.SetYOffset(newTopLine)
	} else {
		m.display.SetHeight(newHeight)
	}

	m.updateDisplayContent()
}

// renderTodos formats the todo list for display
func (m *Terminal) renderTodos() string {
	if len(m.todos) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(m.styles.TodoHeader.Render("TODO LIST"))
	sb.WriteString("\n")

	for i, item := range m.todos {
		var statusStyle lipgloss.Style
		var todoText string

		switch item.Status {
		case "pending":
			statusStyle = m.styles.Pending
			todoText = fmt.Sprintf("%d. %s", i+1, item.Content)
		case "in_progress":
			statusStyle = m.styles.InProgress
			todoText = fmt.Sprintf("%d. %s", i+1, item.ActiveForm)
		case "completed":
			statusStyle = m.styles.Completed
			todoText = fmt.Sprintf("%d. %s", i+1, item.Content)
		}

		sb.WriteString(statusStyle.Render(todoText))
		if i < len(m.todos)-1 {
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

// Init initializes the Terminal
func (m *Terminal) Init() tea.Cmd {
	return nil
}

type tickMsg struct{}

// Update handles messages
func (m *Terminal) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Non-blocking check for display updates
	select {
	case <-m.terminalOutput.updateChan:
		m.updateDisplayContent()
		m.updateStatus()
		m.updateTodos()
	default:
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	case tea.WindowSizeMsg:
		m.windowWidth = msg.Width
		m.windowHeight = msg.Height
		m.display.SetWidth(max(0, msg.Width)) // Full terminal width
		m.input.SetWidth(max(0, msg.Width-8)) // Leave room for border padding (2 on each side)
		m.terminalOutput.SetWindowWidth(max(0, msg.Width))
		m.updateDisplayHeight()
		m.centerWelcomeText()
		m.updateTodos()
		return m, nil
	case tickMsg:
		m.updateDisplayContent()
		m.updateStatus()
		m.updateTodos()
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
			m.editorContent = msg.content
			m.input.SetValue(FormatEditorContent(msg.content))
			m.input.CursorEnd()
			m.input.Focus()
		}
		return m, nil
	}

	// Update input
	m.input, _ = m.input.Update(msg)
	return m, nil
}

func (m *Terminal) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Handle confirmation dialogs first
	if cmd, handled := m.handleConfirmDialog(msg); handled {
		return m, cmd
	}

	// Handle Tab to switch focus
	if msg.String() == "tab" {
		m.toggleFocus()
		return m, nil
	}

	// Handle display window scrolling
	if m.focusedWindow == "display" {
		if cmd, handled := m.handleDisplayKeys(msg); handled {
			return m, cmd
		}
	}

	// Handle global shortcuts
	if cmd, handled := m.handleGlobalKeys(msg); handled {
		return m, cmd
	}

	// Update input
	oldValue := m.input.Value()
	m.input, _ = m.input.Update(msg)
	newValue := m.input.Value()

	// If user modified input and we have editorContent, clear it
	if m.editorContent != "" && oldValue != newValue && !strings.HasPrefix(oldValue, "[") {
		m.editorContent = ""
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
		m.scrollDown(1)
		return nil, true
	case "k":
		m.display.ScrollUp(1)
		m.userScrolledAway = true
		return nil, true
	case "G":
		m.display.GotoBottom()
		m.userScrolledAway = false
		return nil, true
	case "g":
		m.display.GotoTop()
		m.userScrolledAway = true
		return nil, true
	case "ctrl+d":
		m.scrollDown(m.display.Height() / 2)
		return nil, true
	case "ctrl+u":
		m.display.ScrollUp(m.display.Height() / 2)
		m.userScrolledAway = true
		return nil, true
	case "/":
		m.focusedWindow = "input"
		m.input.Focus()
		m.input.SetValue("/")
		m.input.CursorEnd()
		return nil, true
	}
	return nil, false
}

// scrollDown scrolls the display and updates userScrolledAway
func (m *Terminal) scrollDown(lines int) {
	m.display.ScrollDown(lines)
	m.userScrolledAway = !m.display.AtBottom()
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
			m.editorContent = ""
		}
		return nil, true
	case "ctrl+u":
		// Disable Ctrl+U in input window
		if m.focusedWindow == "input" {
			return nil, true
		}
	case "ctrl+s":
		return m.submitCommand("save", false), true
	case "ctrl+o":
		return m.editor.Open(m.getInputForEditor()), true
	case "enter":
		return m.handleSubmit(), true
	}
	return nil, false
}

// handleSubmit processes the input when Enter is pressed
func (m *Terminal) handleSubmit() tea.Cmd {
	var prompt string

	if m.editorContent != "" {
		prompt = m.editorContent
		m.editorContent = ""
	} else {
		prompt = m.input.Value()
	}

	if prompt == "" {
		return nil
	}

	// Handle commands
	if command, found := strings.CutPrefix(prompt, "/"); found {
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

	// Submit prompt
	m.streamInput.EmitTLV(stream.TagUserText, prompt)
	m.input.SetValue("")
	m.updateStatus()

	return tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg{}
	})
}

func (m *Terminal) updateStatus() {}

func (m *Terminal) submitCommand(command string, clearInput bool) tea.Cmd {
	// Send command as TLV to session
	m.streamInput.EmitTLV(stream.TagUserText, "/"+command)
	if clearInput {
		m.input.SetValue("")
	}
	// Start ticking to check for updates during command processing
	return tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg{}
	})
}

func (m *Terminal) centerWelcomeText() {
	width := m.display.Width()
	height := m.display.Height()
	if width == 0 || height == 0 {
		return
	}

	// Only center if welcome text is still being shown
	if !m.showingWelcome {
		return
	}

	// Find the widest line in welcome text
	lines := strings.Split(m.welcomeText, "\n")
	maxWidth := 0
	for _, line := range lines {
		lineWidth := lipgloss.Width(line)
		if lineWidth > maxWidth {
			maxWidth = lineWidth
		}
	}

	// Calculate vertical centering
	lineCount := len(lines)
	topPadding := max(0, (height-lineCount)/2)

	// Calculate horizontal centering
	centeredLines := make([]string, 0, len(lines)+topPadding)
	if maxWidth < width {
		padding := (width - maxWidth) / 2
		for _, line := range lines {
			centeredLines = append(centeredLines, strings.Repeat(" ", padding)+line)
		}
	} else {
		centeredLines = append(centeredLines, lines...)
	}

	// Add vertical padding at the top
	for range topPadding {
		centeredLines = append([]string{""}, centeredLines...)
	}

	m.display.SetContent(strings.Join(centeredLines, "\n"))
}

// getInputForEditor returns the content to pre-populate in the editor
// If editorContent is set (from a previous Ctrl+O), use that.
// Otherwise, use the current input value.
func (m *Terminal) getInputForEditor() string {
	if m.editorContent != "" {
		return m.editorContent
	}
	return m.input.Value()
}

func (m *Terminal) updateDisplayContent() {
	newContent := m.terminalOutput.windowBuffer.GetAll()

	// If showing welcome, only switch to real content when it actually exists
	if m.showingWelcome {
		if newContent != "" && newContent != m.welcomeText {
			m.showingWelcome = false
		} else {
			return
		}
	}
	// Debug: log first line of newContent
	if lines := strings.Split(newContent, "\n"); len(lines) > 0 {
	}
	m.display.SetContent(newContent)
	// Auto-scroll by default, unless user has manually scrolled away
	if !m.userScrolledAway {
		m.display.GotoBottom()
	}
}

// View renders the Terminal
func (m *Terminal) View() tea.View {
	windowWidth := m.windowWidth
	focused := m.focusedWindow == "input"
	borderColor := map[bool]string{true: "#89d4fa", false: "#45475a"}[focused]

	borderStyle := m.styles.InputBorder.BorderForeground(lipgloss.Color(borderColor)).Padding(0, 1)

	styles := textinput.DefaultStyles(true)
	styles.Focused.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color(borderColor)).Bold(true)
	styles.Blurred.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color(borderColor)).Bold(true)
	if focused {
		styles.Focused.Text = lipgloss.NewStyle()
		styles.Blurred.Text = lipgloss.NewStyle()
	} else {
		styles.Focused.Text = lipgloss.NewStyle().Foreground(lipgloss.Color("#45475a"))
		styles.Blurred.Text = lipgloss.NewStyle().Foreground(lipgloss.Color("#45475a"))
	}
	m.input.SetStyles(styles)

	var sb strings.Builder
	displayView := m.display.View()
	sb.WriteString(displayView)
	sb.WriteString("\n")

	// Add todo list between display and input
	todos := m.renderTodos()
	if todos != "" {
		todoInnerStyle := lipgloss.NewStyle().Width(max(0, windowWidth-4))
		todoBorderStyle := m.styles.TodoBorder.Padding(0, 1)
		sb.WriteString(todoBorderStyle.Render(todoInnerStyle.Render(todos)))
		sb.WriteString("\n")
	}

	if m.confirmDialog {
		confirmText := m.styles.Confirm.Width(max(0, windowWidth-4)).Render("Confirm exit? Press y/n")
		sb.WriteString(borderStyle.Render(confirmText))
	} else if m.cancelConfirmDialog {
		confirmText := m.styles.Confirm.Width(max(0, windowWidth-4)).Render("Confirm cancel? Press y/n")
		sb.WriteString(borderStyle.Render(confirmText))
	} else {
		inputBorder := borderStyle.Render(m.styles.Input.Width(max(0, windowWidth-4)).Render(m.input.View()))
		sb.WriteString(inputBorder)
	}

	sb.WriteString("\n")
	sb.WriteString(m.styles.Status.Width(max(0, windowWidth-4)).Padding(0, 1).Render(m.terminalOutput.status))

	v := tea.NewView(sb.String())
	v.AltScreen = true
	return v
}

var (
	_ tea.Model = (*Terminal)(nil)
)
