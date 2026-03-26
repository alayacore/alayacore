package terminal

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// InputModel handles text input and editor integration.
type InputModel struct {
	input         textinput.Model
	focused       bool
	editorContent string
	editor        *Editor
	styles        *Styles
	width         int
}

// NewInputModel creates a new input model
func NewInputModel(styles *Styles) InputModel {
	input := textinput.New()
	input.Placeholder = "Enter your prompt..."
	input.Focus()
	input.Prompt = "> "
	input.SetWidth(76)

	return InputModel{
		input:   input,
		focused: true,
		editor:  NewEditor(),
		styles:  styles,
		width:   DefaultWidth,
	}
}

// Init initializes the input model
func (m InputModel) Init() tea.Cmd {
	return nil
}

// Update handles messages for the input model
func (m InputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.input.SetWidth(max(0, msg.Width-8))
	case editorFinishedMsg:
		if msg.err != nil {
			return m, nil
		}
		if msg.content != "" {
			m.editorContent = msg.content
			m.input.SetValue(FormatEditorContent(msg.content))
			m.input.CursorEnd()
			m.focused = true
			m.input.Focus()
		}
	}

	oldValue := m.input.Value()
	m.input, _ = m.input.Update(msg)
	newValue := m.input.Value()

	if m.editorContent != "" && oldValue != newValue && !strings.HasPrefix(oldValue, "[") {
		m.editorContent = ""
	}

	return m, nil
}

// View renders the input field
func (m InputModel) View() tea.View {
	m.updateInputStyles()
	return tea.NewView(m.input.View())
}

// updateInputStyles updates the text input styles based on current theme
func (m InputModel) updateInputStyles() {
	styles := textinput.DefaultStyles(true)
	styles.Focused.Prompt = lipgloss.NewStyle().Foreground(m.styles.ColorAccent).Bold(true)
	styles.Blurred.Prompt = lipgloss.NewStyle().Foreground(m.styles.ColorDim).Bold(true)
	styles.Focused.Text = lipgloss.NewStyle()
	styles.Blurred.Text = lipgloss.NewStyle().Foreground(m.styles.ColorDim)
	styles.Cursor.Color = m.styles.CursorColor
	m.input.SetStyles(styles)
}

// Focus sets focus on the input
func (m *InputModel) Focus() {
	m.focused = true
	m.input.Focus()
}

// Blur removes focus from the input
func (m *InputModel) Blur() {
	m.focused = false
	m.input.Blur()
}

// IsFocused returns whether the input is focused
func (m InputModel) IsFocused() bool {
	return m.focused
}

// Value returns the current input value
func (m InputModel) Value() string {
	return m.input.Value()
}

// SetValue sets the input value
func (m *InputModel) SetValue(value string) {
	m.input.SetValue(value)
}

// Clear clears the input and editor content
func (m *InputModel) Clear() {
	m.input.SetValue("")
	m.editorContent = ""
}

// GetPrompt returns the actual prompt text (editor content or input value)
func (m InputModel) GetPrompt() string {
	if m.editorContent != "" {
		return m.editorContent
	}
	return m.input.Value()
}

// GetEditorContent returns the editor content
func (m InputModel) GetEditorContent() string {
	return m.editorContent
}

// ClearEditorContent clears the editor content
func (m *InputModel) ClearEditorContent() {
	m.editorContent = ""
}

// OpenEditor opens the external editor
func (m InputModel) OpenEditor() tea.Cmd {
	content := m.editorContent
	if content == "" {
		content = m.input.Value()
	}
	return m.editor.Open(content)
}

// RenderWithBorder renders the input with a border
func (m InputModel) RenderWithBorder(confirmDialog bool, confirmText string) string {
	borderColor := m.styles.BorderFocused
	if !m.focused {
		borderColor = m.styles.BorderBlurred
	}

	// Set input styles based on focus state
	styles := textinput.DefaultStyles(true)
	styles.Focused.Prompt = lipgloss.NewStyle().Foreground(m.styles.ColorAccent).Bold(true)
	styles.Blurred.Prompt = lipgloss.NewStyle().Foreground(m.styles.ColorDim).Bold(true)
	styles.Focused.Text = lipgloss.NewStyle()
	styles.Blurred.Text = lipgloss.NewStyle().Foreground(m.styles.ColorDim)
	styles.Cursor.Color = m.styles.CursorColor
	m.input.SetStyles(styles)

	if confirmDialog {
		return m.styles.RenderBorderedBox(m.styles.Confirm.Render(confirmText), m.width, borderColor)
	}

	return m.styles.RenderBorderedBox(m.input.View(), m.width, borderColor)
}

// SetWidth sets the input width
func (m *InputModel) SetWidth(width int) {
	m.width = width
	m.input.SetWidth(max(0, width-InputPaddingH))
}

// SetStyles updates the styles for the input
func (m *InputModel) SetStyles(styles *Styles) {
	m.styles = styles
	m.updateInputStyles()
}

// CursorEnd moves cursor to end
func (m *InputModel) CursorEnd() {
	m.input.CursorEnd()
}

// updateFromMsg handles a message and updates internal state (non-tea.Model interface)
func (m *InputModel) updateFromMsg(msg tea.Msg) {
	oldValue := m.input.Value()
	m.input, _ = m.input.Update(msg)
	newValue := m.input.Value()

	if m.editorContent != "" && oldValue != newValue && !strings.HasPrefix(oldValue, "[") {
		m.editorContent = ""
	}
}

var _ tea.Model = (*InputModel)(nil)
