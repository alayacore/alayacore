package terminal

// PromptInput handles text input with external editor support.
// It wraps an InputField which supports multi-line content.

import (
	"image/color"
	"strings"
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
	// active is the painting answer to "is this the box the user is writing
	// into, in a window the operating system has focused". Terminal derives it
	// each frame from its own keyboard target and hasFocus, and passes it down
	// to the field. It is never consulted by the input path: two flags used to
	// sit here, one for routing and one for the window, and the routing one is
	// what let a context-menu paste vanish (see Terminal.keyboardTarget).
	active  bool // the live box, for painting (see WithActive)
	width   int  // input field width
	blocked bool // when true, content is dimmed (overlay active)

	// ── Dependencies (pointer to shared data) ─
	styles *Styles
}

// NewPromptInput creates a new prompt input.
func NewPromptInput(styles *Styles) PromptInput {
	input := NewMultilineInputField()
	input.Placeholder = "Enter your prompt..."
	input = input.WithActive(true)
	input.Prompt = ""
	input = input.WithWidth(max(0, DefaultWidth))

	return PromptInput{
		input:  input,
		active: true,
		styles: styles,
		width:  DefaultWidth,
	}
}
func (m PromptInput) Init() Cmd {
	return nil
}

// Update handles messages for the prompt input.
func (m PromptInput) Update(msg Msg) (PromptInput, []Result) {
	if msg, ok := msg.(WindowSizeMsg); ok {
		m.width = msg.Width
		m.input = m.input.WithWidth(max(0, msg.Width))
	}
	if keyMsg, ok := msg.(KeyMsg); ok && keyMsg.Chord() == keyCtrlO {
		return m, []Result{openEditorForPromptMsg{content: m.input.Value()}}
	}
	var results []Result
	m.input, results = m.input.Update(msg)
	return m, results
}

// View renders the input field with border, attachments above if present.
// When blocked is true, content is dimmed (overlay active).
func (m PromptInput) View() View {
	borderColor := m.borderColor()

	input := m.updateInputStyles()
	content := input.View()
	if len(m.attachments) > 0 {
		innerWidth := max(0, m.width)
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
		return NewView(m.styles.RenderOpenBox(sb.String(), m.width, borderColor))
	}
	if m.blocked {
		content = m.styles.Input.Foreground(m.styles.ColorDim).Render(content)
	}
	return NewView(m.styles.RenderOpenBox(content, m.width, borderColor))
}

// borderColor returns the prompt box's rule color. Multi-line content is the
// one state of the box that changes it: a whole draft is waiting to be
// submitted, so the rule turns to the warning color. The prompt is the one
// field that can hold a line break (InputField's type doc), which is why this
// affordance is exclusive to it — an overlay filter, being single-line, can
// never reach the branch.
func (m PromptInput) borderColor() color.Color {
	switch {
	case m.blocked:
		return m.styles.ColorDim
	case !m.active:
		return m.styles.BorderBlurred
	case m.input.LineCount() > 1:
		return m.styles.ColorWarning
	default:
		return m.styles.BorderFocused
	}
}

// updateInputStyles updates the text input styles based on current theme.
//
// View() invokes this on every render; the resulting Style allocations
// are not memoized — PromptInput is a value type whose mutations don't
// persist across the value-copy that View()'s caller sees, so a cache
// keyed on this receiver would never hit. The allocations are cheap
// (a handful of NewStyle() calls) and the downstream same-content
// identity check in Program.render skips the terminal write anyway.
//
// The line count still drives the prompt color (warning for multi-line
// inputs as a brighter visual cue).
func (m PromptInput) updateInputStyles() InputField {
	promptColor := m.styles.ColorAccent
	if m.input.LineCount() > 1 {
		promptColor = m.styles.ColorWarning
	}
	focused := inputFieldStyle{
		Prompt:      NewStyle().Foreground(promptColor).Bold(true),
		Text:        NewStyle(),
		Placeholder: NewStyle().Foreground(m.styles.ColorMuted),
	}
	blurred := inputFieldStyle{
		Prompt:      NewStyle().Foreground(m.styles.ColorDim).Bold(true),
		Text:        NewStyle().Foreground(m.styles.ColorDim),
		Placeholder: NewStyle().Foreground(m.styles.ColorDim),
	}
	if !m.active {
		// The register the box paints in. Handing the blurred style to both
		// slots is how the window's focus reaches the text: the field's own
		// answer to "am I live" is set from here, never from the input path.
		focused = blurred
	}
	return m.input.WithStyles(focused, blurred)
}

// WithActive says whether this box is the live one — the user's target, in a
// window the operating system has focused — for painting: the border, the
// register of the text, and whether the real terminal cursor belongs here (IME
// preedit and the candidate window anchor on that cursor, so an unfocused
// window is asked to give it up).
//
// Idempotent like WithBlocked: Terminal.View() syncs it every frame, and the
// value type would otherwise be replaced on each render.
func (m PromptInput) WithActive(active bool) PromptInput {
	if m.active == active && m.input.IsActive() == active {
		return m
	}
	m.active = active
	m.input = m.input.WithActive(active)
	return m
}

// IsActive reports the painting state WithActive set.
func (m PromptInput) IsActive() bool { return m.active }

func (m PromptInput) Value() string {
	return m.input.Value()
}

func (m PromptInput) WithValue(value string) PromptInput {
	m.input = m.input.WithValue(value)
	return m
}

// WithBlockValue replaces the prompt with a block of text that arrived whole —
// an external editor's buffer — cursor at the end, through the wrapped field's
// block rule (InputField.WithBlockValue).
func (m PromptInput) WithBlockValue(value string) PromptInput {
	m.input = m.input.WithBlockValue(value)
	return m
}

// SetAttachments sets the pending attachment paths for display.
func (m PromptInput) WithAttachments(paths []string) PromptInput {
	m.attachments = paths
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
		innerWidth := max(0, m.width)
		styledMedia := wrapLabels(m.attachments, innerWidth, m.styles.Attachment)
		lines += Height(styledMedia) + 1 // attachment lines + separator
	}
	return lines
}

// WithBlocked marks the input as blocked (covered by an overlay).
// When blocked, View() dims the content instead of showing it at full brightness.
//
// Idempotent: returns the receiver unchanged when the blocked state is
// already set to the requested value. View() invokes this on every frame
// (with the live overlay state) — without the early-return, every render
// would replace the PromptInput value type even when nothing changed.
func (m PromptInput) WithBlocked(blocked bool) PromptInput {
	if m.blocked == blocked {
		return m
	}
	m.blocked = blocked
	return m
}

func (m PromptInput) WithWidth(width int) PromptInput {
	m.width = width
	m.input = m.input.WithWidth(max(0, width))
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

// InsertNewline inserts a line break at the cursor (the prompt's Ctrl+J
// action). Bound from handleInputKeys only, so overlay filter boxes — which
// embed their own InputField and read Enter as "accept" — never see it.
func (m PromptInput) InsertNewline() PromptInput {
	m.input = m.input.insertNewline()
	return m
}

// CursorPos returns the cursor position (in runes) within the input field.
func (m PromptInput) CursorPos() int {
	return m.input.CursorPos()
}

// CursorCell returns the cursor's cell offset within the input text area
// (0 = leftmost visible cell of the current line). Used by Terminal.View to
// position the real terminal cursor. The input renders a single line, so the
// cursor's vertical position is independent of the value's line index.
func (m PromptInput) CursorCell() int {
	return m.input.CursorCell()
}

// AttachmentsOffset returns the number of content lines above the input
// text inside the bordered box (attachment lines + separator line),
// or 0 when there are no pending attachments.
func (m PromptInput) AttachmentsOffset() int {
	if len(m.attachments) == 0 {
		return 0
	}
	innerWidth := max(0, m.width)
	styledMedia := wrapLabels(m.attachments, innerWidth, m.styles.Attachment)
	return Height(styledMedia) + 1 // + separator line
}
