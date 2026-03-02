package adaptors

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	agentpkg "github.com/wallacegibbon/coreclaw/internal/agent"
	"github.com/wallacegibbon/coreclaw/internal/stream"
	"github.com/wallacegibbon/coreclaw/internal/todo"
)

const (
	tempFilePrefix = "coreclaw-input-*.txt"
)

// DisplayBuffer holds text to display in Terminal
type DisplayBuffer struct {
	mu       sync.Mutex
	Messages []string
}

// NewDisplayBuffer creates a new display buffer
func NewDisplayBuffer() *DisplayBuffer {
	return &DisplayBuffer{
		Messages: []string{},
	}
}

// Append adds text to the display buffer
func (d *DisplayBuffer) Append(text string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Messages = append(d.Messages, text)
}

// GetAll returns all messages joined together
func (d *DisplayBuffer) GetAll() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return strings.Join(d.Messages, "")
}

// TerminalAdaptor is a terminal adaptor with a Terminal interface
type TerminalAdaptor struct {
	AgentFactory AgentFactory
	BaseURL      string
	ModelName    string
	processor    *agentpkg.Processor
	session      *agentpkg.Session
	sessionFile  string
	TodoMgr      *todo.Manager
}

// NewTerminalAdaptor creates a new Terminal adaptor
func NewTerminalAdaptor(agentFactory AgentFactory, baseURL, modelName string) *TerminalAdaptor {
	return &TerminalAdaptor{
		AgentFactory: agentFactory,
		BaseURL:      baseURL,
		ModelName:    modelName,
		sessionFile:  "",
	}
}

// SetTodoMgr sets the todo manager
func (a *TerminalAdaptor) SetTodoMgr(todoMgr *todo.Manager) {
	a.TodoMgr = todoMgr
}

// SetSessionFile sets the session file path
func (a *TerminalAdaptor) SetSessionFile(sessionFile string) {
	a.sessionFile = sessionFile
}

// Start runs the Terminal
func (a *TerminalAdaptor) Start() {
	agent := a.AgentFactory()

	// Create input and output streams
	inputStream := stream.NewChanInput(10)
	terminalOutput := newTerminalOutput()
	processor := agentpkg.NewProcessorWithIO(agent, inputStream, terminalOutput)
	a.processor = processor

	// Load or create session
	a.session, a.sessionFile = agentpkg.LoadOrNewSession(agent, a.BaseURL, a.ModelName, processor, a.sessionFile)

	// Set TodoMgr on session
	if a.TodoMgr != nil {
		a.session.SetTodoMgr(a.TodoMgr)
	}

	// Display loaded messages if session has any
	if len(a.session.Messages) > 0 {
		a.session.DisplayMessages()
		// Force flush to ensure all messages are written to display buffer
		processor.Output.Flush()
	}

	t := NewTerminal(a.session, terminalOutput, inputStream, a.sessionFile)
	t.SetTodoMgr(a.TodoMgr)

	p := tea.NewProgram(t, tea.WithAltScreen(), tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout))
	p.Run()
}

// terminalOutput writes to the Terminal display with TLV support
type terminalOutput struct {
	display    *DisplayBuffer
	buffer     []byte
	mu         sync.Mutex
	updateChan chan struct{}
	status     string // Status bar content from TagSystem

	textStyle        lipgloss.Style
	userInputStyle   lipgloss.Style
	toolStyle        lipgloss.Style
	toolContentStyle lipgloss.Style
	reasoningStyle   lipgloss.Style
	errorStyle       lipgloss.Style
	systemStyle      lipgloss.Style
	promptStyle      lipgloss.Style
}

func newTerminalOutput() *terminalOutput {
	return &terminalOutput{
		display:          NewDisplayBuffer(),
		updateChan:       make(chan struct{}, 1),
		textStyle:        lipgloss.NewStyle().Foreground(lipgloss.Color("#cdd6f4")).Bold(true),
		userInputStyle:   lipgloss.NewStyle().Foreground(lipgloss.Color("#89d4fa")).Bold(true),
		toolStyle:        lipgloss.NewStyle().Foreground(lipgloss.Color("#f9e2af")),
		toolContentStyle: lipgloss.NewStyle().Foreground(lipgloss.Color("#89d4fa")),
		reasoningStyle:   lipgloss.NewStyle().Foreground(lipgloss.Color("#6c7086")).Italic(true),
		errorStyle:       lipgloss.NewStyle().Foreground(lipgloss.Color("#f38ba8")),
		systemStyle:      lipgloss.NewStyle().Foreground(lipgloss.Color("#6c7086")),
		promptStyle:      lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1")).Bold(true),
	}
}

func (w *terminalOutput) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	w.buffer = append(w.buffer, p...)
	w.processBuffer()
	w.mu.Unlock()
	return len(p), nil
}

func (w *terminalOutput) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *terminalOutput) Flush() error {
	return nil
}

func (w *terminalOutput) processBuffer() {
	for len(w.buffer) >= 5 {
		tag := w.buffer[0]

		length := int32(binary.BigEndian.Uint32(w.buffer[1:5]))

		if len(w.buffer) < 5+int(length) {
			break
		}

		value := string(w.buffer[5 : 5+length])
		w.writeColored(tag, value)

		w.buffer = w.buffer[5+length:]
	}
}

func (w *terminalOutput) renderMultiline(style lipgloss.Style, value string, trimRight bool) string {
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		rendered := style.Render(line)
		if trimRight {
			rendered = strings.TrimRight(rendered, " ")
		}
		lines[i] = rendered
	}
	return strings.Join(lines, "\n")
}

func (w *terminalOutput) writeColored(tag byte, value string) {
	triggerUpdate := func() {
		select {
		case w.updateChan <- struct{}{}:
		default:
		}
	}

	switch tag {
	case stream.TagAssistantText, stream.TagTool, stream.TagReasoning, stream.TagError, stream.TagNotify, stream.TagSystem, stream.TagPromptStart, stream.TagStreamGap:
		triggerUpdate()
	}

	output := func(style lipgloss.Style, text string) string {
		return strings.TrimRight(w.renderMultiline(style, text, true), " ")
	}

	switch tag {
	case stream.TagAssistantText:
		w.display.Append(output(w.textStyle, value))
	case stream.TagTool:
		w.display.Append(strings.TrimRight(w.colorizeTool(value), " "))
	case stream.TagReasoning:
		w.display.Append(output(w.reasoningStyle, value))
	case stream.TagError:
		w.display.Append(output(w.errorStyle, value))
	case stream.TagNotify:
		w.display.Append(output(w.systemStyle, value))
	case stream.TagSystem:
		var info agentpkg.SystemInfo
		if err := json.Unmarshal([]byte(value), &info); err == nil {
			baseStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#45475a"))
			queueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#f38ba8")).Bold(true)
			if info.QueueCount > 0 {
				queueNum := queueStyle.Render(fmt.Sprintf("%d", info.QueueCount))
				w.status = baseStyle.Render("Queue: ") + queueNum + baseStyle.Render(fmt.Sprintf(" | Context: %d | Total: %d", info.ContextTokens, info.TotalTokens))
			} else {
				w.status = baseStyle.Render(fmt.Sprintf("Context: %d | Total: %d", info.ContextTokens, info.TotalTokens))
			}
		}
		return
	case stream.TagPromptStart:
		w.display.Append(strings.TrimRight(w.promptStyle.Render("> ")+w.userInputStyle.Render(value), " "))
	case stream.TagStreamGap:
		w.display.Append("\n")
	default:
		w.display.Append(value)
	}
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

func (w *terminalOutput) colorizeTool(value string) string {
	lines := strings.Split(value, "\n")
	if len(lines) == 1 {
		// Single line: original logic
		colonIdx := strings.Index(value, ":")
		if colonIdx > 0 {
			toolName := value[:colonIdx]
			rest := value[colonIdx:]
			return strings.TrimRight(w.toolStyle.Render(toolName), " ") + strings.TrimRight(w.toolContentStyle.Render(rest), " ")
		}
		return strings.TrimRight(w.toolStyle.Render(value), " ")
	}

	// Multiline: first line may contain colon
	firstLine := lines[0]
	colonIdx := strings.Index(firstLine, ":")
	var result strings.Builder
	if colonIdx > 0 {
		toolName := firstLine[:colonIdx]
		restFirst := firstLine[colonIdx:]
		result.WriteString(strings.TrimRight(w.toolStyle.Render(toolName), " "))
		result.WriteString(strings.TrimRight(w.toolContentStyle.Render(restFirst), " "))
	} else {
		// No colon in first line, treat entire first line as tool name
		result.WriteString(strings.TrimRight(w.toolStyle.Render(firstLine), " "))
	}
	// Remaining lines: apply toolContentStyle (continuation of tool output)
	for _, line := range lines[1:] {
		result.WriteString("\n")
		result.WriteString(strings.TrimRight(w.toolContentStyle.Render(line), " "))
	}
	return result.String()
}

// Terminal is the main Terminal model
type Terminal struct {
	session          *agentpkg.Session
	terminalOutput   *terminalOutput
	streamInput      *stream.ChanInput
	display          viewport.Model
	input            textinput.Model
	quitting         bool
	confirmDialog    bool
	focusedWindow    string        // "display" or "input"
	userScrolledAway bool          // true after user manually scrolls up
	windowWidth      int           // actual window width
	windowHeight     int           // actual window height
	editorContent    string        // content from external editor with newlines preserved
	showingWelcome   bool          // true while welcome text is still displayed
	welcomeText      string        // colored welcome text for comparison
	sessionFile      string        // session file path for saving on quit
	todoMgr          *todo.Manager // todo manager
	todos            todo.TodoList // cached todos for display

	inputStyle  lipgloss.Style
	statusStyle lipgloss.Style
}

// NewTerminal creates a new Terminal model
func NewTerminal(session *agentpkg.Session, terminalOutput *terminalOutput, inputStream *stream.ChanInput, sessionFile string) *Terminal {
	input := textinput.New()
	input.Placeholder = "Enter your prompt..."
	input.Focus()
	input.Prompt = "> "

	inputStyle := lipgloss.NewStyle()
	statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#45475a"))

	coloredWelcome := colorizeWelcomeText(WelcomeText)
	display := viewport.New(80, 20)

	m := &Terminal{
		session:        session,
		terminalOutput: terminalOutput,
		streamInput:    inputStream,
		display:        display,
		input:          input,
		windowWidth:    80,
		inputStyle:     inputStyle,
		statusStyle:    statusStyle,
		focusedWindow:  "input",
		welcomeText:    coloredWelcome,
		sessionFile:    sessionFile,
	}

	hasExistingContent := len(terminalOutput.display.Messages) > 0
	if hasExistingContent {
		existingContent := terminalOutput.display.GetAll()
		wrapped := wordwrap(existingContent, display.Width)
		newlineCount := strings.Count(wrapped, "\n")
		display.SetContent(wrapped)
		display.SetYOffset(max(0, newlineCount-display.Height))
		m.showingWelcome = false
	} else {
		display.SetContent(coloredWelcome)
		m.showingWelcome = true
	}

	return m
}

// SetTodoMgr sets the todo manager
func (m *Terminal) SetTodoMgr(todoMgr *todo.Manager) {
	m.todoMgr = todoMgr
	m.updateTodos()
}

// updateTodos updates the cached todos from the todo manager
func (m *Terminal) updateTodos() {
	if m.todoMgr != nil {
		m.todos = m.todoMgr.GetTodos()
	}
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
	oldHeight := m.display.Height

	// Only adjust YOffset if height actually changes
	if oldHeight != newHeight {
		// Get raw content and word-wrap to count lines
		rawContent := m.terminalOutput.display.GetAll()
		wrapped := wordwrap(rawContent, m.display.Width)
		totalLines := strings.Count(wrapped, "\n") + 1 // content may have trailing newline
		// Ensure totalLines is at least 1
		if totalLines < 1 {
			totalLines = 1
		}

		topLine := m.display.YOffset
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

		m.display.Height = newHeight
		m.display.SetYOffset(newTopLine)
	} else {
		m.display.Height = newHeight
	}

	m.updateDisplayContent()
}

// renderTodos formats the todo list for display
func (m *Terminal) renderTodos() string {
	if len(m.todos) == 0 {
		return ""
	}

	todoStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#f9e2af"))                  // Yellow
	pendingStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff"))               // White
	inProgressStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1")).Bold(true) // Green bold
	completedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1"))             // Green

	var sb strings.Builder
	sb.WriteString(todoStyle.Render("TODO LIST"))
	sb.WriteString("\n")

	for i, item := range m.todos {
		var statusStyle lipgloss.Style
		var todoText string

		switch item.Status {
		case "pending":
			statusStyle = pendingStyle
			todoText = fmt.Sprintf("%d. %s", i+1, item.Content)
		case "in_progress":
			statusStyle = inProgressStyle
			todoText = fmt.Sprintf("%d. %s", i+1, item.ActiveForm)
		case "completed":
			statusStyle = completedStyle
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

type editorFinishedMsg struct {
	content string
	err     error
}

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
		m.display.Width = max(0, msg.Width-8) // Leave room for padding (4 on each side)
		m.updateDisplayHeight()
		m.centerWelcomeText()
		m.updateTodos()
		return m, nil
	case tickMsg:
		m.updateDisplayContent()
		m.updateStatus()
		if m.session != nil && m.session.IsInProgress() {
			return m, tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
				return tickMsg{}
			})
		}
		return m, nil
	case editorFinishedMsg:
		if msg.err != nil {
			m.terminalOutput.display.Append(m.terminalOutput.errorStyle.Render(fmt.Sprintf("Editor error: %v", msg.err)))
		} else if msg.content != "" {
			m.editorContent = msg.content
			lineCount := strings.Count(msg.content, "\n") + 1
			preview := strings.Fields(msg.content)
			var previewText string
			if len(preview) > 0 && len(preview[0]) > 20 {
				previewText = preview[0][:20] + "..."
			} else if len(preview) > 0 {
				previewText = preview[0]
			} else {
				previewText = "(empty)"
			}
			m.input.SetValue(fmt.Sprintf("[%d lines] %s (press Enter to send)", lineCount, previewText))
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
	// Handle confirm dialog
	if m.confirmDialog {
		switch msg.String() {
		case "y", "Y":
			m.quitting = true
			// Close input channel to stop session's readFromInput
			close(m.streamInput.Ch)
			return m, tea.Quit
		case "n", "N", "esc", "ctrl+c":
			m.confirmDialog = false
			m.input.SetValue("")
			return m, nil
		}
		return m, nil
	}

	// Handle Tab to switch focus
	if msg.Type == tea.KeyTab {
		if m.focusedWindow == "display" {
			m.focusedWindow = "input"
			m.input.Focus()
		} else {
			m.focusedWindow = "display"
			m.input.Blur()
		}
		return m, nil
	}

	// Handle j/k for display window scrolling
	if m.focusedWindow == "display" {
		switch msg.String() {
		case "j":
			m.display.ScrollDown(1)
			// Check if now at bottom
			if m.display.AtBottom() {
				m.userScrolledAway = false
			} else {
				m.userScrolledAway = true
			}
			return m, nil
		case "k":
			m.display.ScrollUp(1)
			m.userScrolledAway = true
			return m, nil
		case "G":
			m.display.GotoBottom()
			m.userScrolledAway = false
			return m, nil
		case "g":
			m.display.GotoTop()
			m.userScrolledAway = true
			return m, nil
		case "ctrl+d":
			m.display.ScrollDown(m.display.Height / 2)
			if m.display.AtBottom() {
				m.userScrolledAway = false
			} else {
				m.userScrolledAway = true
			}
			return m, nil
		case "ctrl+u":
			m.display.ScrollUp(m.display.Height / 2)
			m.userScrolledAway = true
			return m, nil
		case "/":
			m.focusedWindow = "input"
			m.input.Focus()
			m.input.SetValue("/")
			m.input.CursorEnd()
			return m, nil
		}
	}

	switch msg.Type {
	case tea.KeyCtrlC:
		// Cancel the current request
		return m, m.submitCommand("cancel", false)
	case tea.KeyCtrlS:
		// Save session
		return m, m.submitCommand("save", false)
	case tea.KeyCtrlO:
		// Open external editor for multi-line input
		return m, m.openEditor()
	case tea.KeyEnter:
		var prompt string

		// Check if we have editor content to submit
		if m.editorContent != "" {
			prompt = m.editorContent
			m.editorContent = ""
		} else {
			prompt = m.input.Value()
		}

		if prompt == "" {
			return m, nil
		}

		// Handle commands
		if command, found := strings.CutPrefix(prompt, "/"); found {
			if command == "quit" || command == "exit" {
				m.confirmDialog = true
				return m, nil
			}
			return m, m.submitCommand(command, true)
		}

		// Submit prompt as TLV to input stream - session handles queuing
		m.streamInput.EmitTLVData(stream.TagUserText, prompt)

		m.input.SetValue("")
		m.updateStatus()

		// Start ticking to check for updates during processing
		return m, tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
			return tickMsg{}
		})
	}

	oldValue := m.input.Value()
	m.input, _ = m.input.Update(msg)
	newValue := m.input.Value()

	// If user modified input and we have editorContent, clear it
	if m.editorContent != "" && oldValue != newValue && !strings.HasPrefix(oldValue, "[") {
		m.editorContent = ""
	}

	return m, nil
}

func (m *Terminal) updateStatus() {}

func (m *Terminal) submitCommand(command string, clearInput bool) tea.Cmd {
	// Send command as TLV to session
	m.streamInput.EmitTLVData(stream.TagUserText, "/"+command)
	if clearInput {
		m.input.SetValue("")
	}
	// Start ticking to check for updates during command processing
	return tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg{}
	})
}

func (m *Terminal) centerWelcomeText() {
	width := m.display.Width
	height := m.display.Height
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

func (m *Terminal) openEditor() tea.Cmd {
	editorCmd := getEditorCommand(os.Getenv("EDITOR"))

	if editorCmd == "" {
		return func() tea.Msg {
			return editorFinishedMsg{content: "", err: fmt.Errorf("no editor found (tried: vim, vi, nano)")}
		}
	}

	tmpFile, err := os.CreateTemp("", tempFilePrefix)
	if err != nil {
		return func() tea.Msg {
			return editorFinishedMsg{content: "", err: err}
		}
	}

	tmpFileName := tmpFile.Name()

	existingContent := m.getInputForEditor()

	if existingContent != "" {
		if _, err := tmpFile.WriteString(existingContent); err != nil {
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
	newContent := m.terminalOutput.display.GetAll()

	// If showing welcome, only switch to real content when it actually exists
	if m.showingWelcome {
		if newContent != "" && newContent != m.welcomeText {
			m.showingWelcome = false
		} else {
			return
		}
	}

	// Wordwrap to viewport width for proper word boundary wrapping
	width := m.display.Width

	if width > 0 {
		newContent = wordwrap(newContent, width)
	}
	m.display.SetContent(newContent)
	// Auto-scroll by default, unless user has manually scrolled away
	if !m.userScrolledAway {
		m.display.GotoBottom()
	}
}

// View renders the Terminal
func (m *Terminal) View() string {
	windowWidth := m.windowWidth
	focused := m.focusedWindow == "input"
	borderColor := map[bool]string{true: "#89d4fa", false: "#45475a"}[focused]

	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(borderColor)).
		Padding(0, 1)

	m.input.PromptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(borderColor)).Bold(true)
	if focused {
		m.input.TextStyle = lipgloss.NewStyle()
	} else {
		m.input.TextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#45475a"))
	}

	var sb strings.Builder
	sb.WriteString(lipgloss.NewStyle().Padding(0, 4).Render(m.display.View()))
	sb.WriteString("\n")

	// Add todo list between display and input
	todos := m.renderTodos()
	if todos != "" {
		todoInnerStyle := lipgloss.NewStyle().
			Width(max(0, windowWidth-4))
		todoBorderStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#6c7086")).
			Padding(0, 1)
		sb.WriteString(todoBorderStyle.Render(todoInnerStyle.Render(todos)))
		sb.WriteString("\n")
	}

	if m.confirmDialog {
		confirmText := lipgloss.NewStyle().
			Width(max(0, windowWidth-4)).
			Foreground(lipgloss.Color("#f38ba8")).
			Bold(true).Render("Confirm exit? Press y/n")
		sb.WriteString(borderStyle.Render(confirmText))
	} else {
		sb.WriteString(borderStyle.Render(m.inputStyle.Width(max(0, windowWidth-4)).Render(m.input.View())))
	}

	sb.WriteString("\n")
	sb.WriteString(lipgloss.NewStyle().Width(max(0, windowWidth-8)).Padding(0, 4).Render(m.terminalOutput.status))

	return sb.String()
}

var (
	_ tea.Model = (*Terminal)(nil)
)

// getEditorCommand returns the editor command to use
// First checks EDITOR env var, then tries vim, vi, nano in order
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
