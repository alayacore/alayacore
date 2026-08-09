package terminal

// PromptInput handles text input with external editor support.
// It wraps an InputField which supports multi-line content.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// PromptInput handles text input.
// PromptInput manages the text input area, including attachments display.
//
// Field groups:
//
//	Elm UI state  — value types / primitives (copied on every WithXxx).
//	Dependencies  — pointers to shared styles.
type PromptInput struct {
	// ── Elm UI state (value types, copied on every WithXxx) ─
	input       InputField // wrapped input field (cursor, buffer, selection)
	attachments []string   // pending attachment file paths to display
	focused     bool       // whether this input is focused
	width       int        // input field width
	blocked     bool       // when true, content is dimmed (overlay active)

	// ── Dependencies (pointer to shared data) ─
	styles *Styles
}

// NewPromptInput creates a new prompt input.
func NewPromptInput(styles *Styles) PromptInput {
	input := NewInputField()
	input.Placeholder = "Enter your prompt..."
	input = input.Focus()
	input.Prompt = ""
	input = input.WithWidth(max(0, DefaultWidth-BorderInnerPadding))

	return PromptInput{
		input:   input,
		focused: true,
		styles:  styles,
		width:   DefaultWidth,
	}
}
func (m PromptInput) Init() tea.Cmd {
	return nil
}

// Update handles messages for the prompt input.
func (m PromptInput) Update(msg tea.Msg) (PromptInput, tea.Cmd) {
	var cmd tea.Cmd
	if msg, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = msg.Width
		m.input = m.input.WithWidth(max(0, msg.Width-BorderInnerPadding))
	}
	if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.String() == keyCtrlO {
		return m, func() tea.Msg {
			return openEditorForPromptMsg{content: m.input.Value()}
		}
	}
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// View renders the input field with border, attachments above if present.
// When blocked is true, content is dimmed (overlay active).
func (m PromptInput) View() tea.View {
	borderColor := m.styles.BorderFocused
	if !m.focused {
		borderColor = m.styles.BorderBlurred
	} else if m.input.LineCount() > 1 {
		borderColor = m.styles.ColorWarning
	}

	if m.blocked {
		borderColor = m.styles.ColorDim
	}

	input := m.updateInputStyles()
	content := input.View()
	if len(m.attachments) > 0 {
		innerWidth := max(0, m.width-BorderInnerPadding)
		attachmentStyle := m.styles.Attachment
		systemStyle := m.styles.System
		if m.blocked {
			attachmentStyle = attachmentStyle.Foreground(m.styles.ColorDim)
			systemStyle = systemStyle.Foreground(m.styles.ColorDim)
		}
		styledMedia := wrapLabels(m.attachments, innerWidth, attachmentStyle)
		separator := systemStyle.Width(innerWidth).Render(Separator)
		var sb strings.Builder
		sb.WriteString(styledMedia)
		sb.WriteString("\n")
		sb.WriteString(separator)
		sb.WriteString("\n")
		sb.WriteString(content)
		return tea.NewView(m.styles.RenderBorderedBox(sb.String(), m.width, borderColor))
	}
	if m.blocked {
		content = m.styles.Input.Foreground(m.styles.ColorDim).Render(content)
	}
	return tea.NewView(m.styles.RenderBorderedBox(content, m.width, borderColor))
}

// updateInputStyles updates the text input styles based on current theme.
func (m PromptInput) updateInputStyles() InputField {
	// Use warning color when there are multiple lines as a brighter visual cue.
	promptColor := m.styles.ColorAccent
	if m.input.LineCount() > 1 {
		promptColor = m.styles.ColorWarning
	}
	return m.input.WithStyles(
		inputFieldStyle{
			Prompt:      lipgloss.NewStyle().Foreground(promptColor).Bold(true),
			Text:        lipgloss.NewStyle().Bold(true),
			Placeholder: lipgloss.NewStyle().Foreground(m.styles.ColorMuted),
		},
		inputFieldStyle{
			Prompt:      lipgloss.NewStyle().Foreground(m.styles.ColorDim).Bold(true),
			Text:        lipgloss.NewStyle().Foreground(m.styles.ColorDim).Bold(true),
			Placeholder: lipgloss.NewStyle().Foreground(m.styles.ColorDim),
		},
	)
}

// Focus sets focus on the input.
func (m PromptInput) Focus() PromptInput {
	m.focused = true
	m.input = m.input.Focus()
	return m
}

// Blur removes focus from the input.
func (m PromptInput) Blur() PromptInput {
	m.focused = false
	m.input = m.input.Blur()
	return m
}

func (m PromptInput) IsFocused() bool {
	return m.focused
}

func (m PromptInput) Value() string {
	return m.input.Value()
}

func (m PromptInput) WithValue(value string) PromptInput {
	m.input = m.input.WithValue(value)
	return m
}

// SetAttachments sets the pending attachment paths for display.
func (m PromptInput) WithAttachments(paths []string) PromptInput {
	m.attachments = paths
	return m
}

// Clear clears the input and attachments.
func (m PromptInput) Clear() PromptInput {
	m.input = m.input.WithValue("")
	m.attachments = nil
	return m
}

// Attachments returns the current attachment paths.
func (m PromptInput) Attachments() []string {
	return m.attachments
}

// Height returns the total height (in terminal lines) of the rendered input box,
// including border and attachments if present.
func (m PromptInput) Height() int {
	// Base: border (2) + input field (1) = 3
	lines := 3
	if len(m.attachments) > 0 {
		innerWidth := max(0, m.width-BorderInnerPadding)
		styledMedia := wrapLabels(m.attachments, innerWidth, m.styles.Attachment)
		lines += lipgloss.Height(styledMedia) + 1 // attachment lines + separator
	}
	return lines
}

// OpenEditor opens the external editor for multi-line input.
func (m Terminal) OpenEditor() tea.Cmd {
	return m.editor.Open(m.input.Value())
}

// WithBlocked marks the input as blocked (covered by an overlay).
// When blocked, View() dims the content instead of showing it at full brightness.
func (m PromptInput) WithBlocked(blocked bool) PromptInput {
	m.blocked = blocked
	return m
}

func (m PromptInput) WithWidth(width int) PromptInput {
	m.width = width
	m.input = m.input.WithWidth(max(0, width-BorderInnerPadding))
	return m
}

func (m PromptInput) WithStyles(styles *Styles) PromptInput {
	m.styles = styles
	m.input = m.updateInputStyles()
	return m
}

// CursorEnd moves cursor to end.
func (m PromptInput) CursorEnd() PromptInput {
	m.input = m.input.CursorEnd()
	return m
}

// CursorPos returns the cursor position (in runes) within the input field.
func (m PromptInput) CursorPos() int {
	return m.input.CursorPos()
}

// CursorCell returns the cursor's line index within the input text area
// (0-based, input text lines only — excludes attachments and the separator
// line above the text) and its cell offset within that line (0 = leftmost
// visible cell). Used by Terminal.View to position the real terminal cursor.
func (m PromptInput) CursorCell() (line, cell int) {
	return m.input.CursorCell()
}

// AttachmentsOffset returns the number of content lines above the input
// text inside the bordered box (attachment lines + separator line),
// or 0 when there are no pending attachments.
func (m PromptInput) AttachmentsOffset() int {
	if len(m.attachments) == 0 {
		return 0
	}
	innerWidth := max(0, m.width-BorderInnerPadding)
	styledMedia := wrapLabels(m.attachments, innerWidth, m.styles.Attachment)
	return lipgloss.Height(styledMedia) + 1 // + separator line
}
