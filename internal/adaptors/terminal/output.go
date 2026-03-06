package terminal

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"

	agentpkg "github.com/wallacegibbon/coreclaw/internal/agent"
	"github.com/wallacegibbon/coreclaw/internal/stream"
	"github.com/wallacegibbon/coreclaw/internal/todo"
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

// terminalOutput writes to the Terminal display with TLV support
type terminalOutput struct {
	windowBuffer *WindowBuffer
	buffer       []byte
	mu           sync.Mutex
	updateChan   chan struct{}
	status       string        // Status bar content from TagSystem
	todos        todo.TodoList // Current todo list
	inProgress   bool          // Whether session has task in progress
	styles       *Styles       // UI styles
	nextWindowID int           // Monotonic counter for generating window IDs
}

func NewTerminalOutput() *terminalOutput {
	return &terminalOutput{
		windowBuffer: NewWindowBuffer(80), // default width, will be updated later
		updateChan:   make(chan struct{}, 1),
		styles:       DefaultStyles(),
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

// AppendError adds an error message to the display buffer with error styling
func (w *terminalOutput) AppendError(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	id := w.generateWindowID()
	w.windowBuffer.AppendOrUpdate(id, stream.TagError, w.styles.Error.Render(msg))
}

// processBuffer parses TLV-encoded data from the buffer
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

// writeColored writes styled content based on the TLV tag
func (w *terminalOutput) writeColored(tag byte, value string) {
	w.triggerUpdateForTag(tag)

	output := func(style lipgloss.Style, text string) string {
		return strings.TrimRight(w.renderMultiline(style, text, true), " ")
	}

	switch tag {
	case stream.TagAssistantText, stream.TagReasoning, stream.TagTool:
		// Delta messages with stream ID prefix
		id, content, ok := w.parseStreamID(value)
		if !ok {
			// Should not happen, but fallback
			id = w.generateWindowID()
			content = value
		}
		var styled string
		switch tag {
		case stream.TagAssistantText:
			styled = output(w.styles.Text, content)
		case stream.TagReasoning:
			styled = output(w.styles.Reasoning, content)
		case stream.TagTool:
			styled = strings.TrimRight(w.colorizeTool(content), " ")
		}
		w.windowBuffer.AppendOrUpdate(id, tag, styled)

	case stream.TagError:
		id := w.generateWindowID()
		styled := output(w.styles.Error, value)
		w.windowBuffer.AppendOrUpdate(id, tag, styled)

	case stream.TagNotify:
		id := w.generateWindowID()
		styled := output(w.styles.System, value)
		w.windowBuffer.AppendOrUpdate(id, tag, styled)

	case stream.TagSystem:
		w.handleSystemTag(value)
		return

	case stream.TagTodo:
		json.Unmarshal([]byte(value), &w.todos)
		return

	case stream.TagUserText:
		id := w.generateWindowID()
		styled := strings.TrimRight(w.styles.Prompt.Render("> ")+w.styles.UserInput.Render(value), " ")
		w.windowBuffer.AppendOrUpdate(id, tag, styled)

	default:
		id := w.generateWindowID()
		w.windowBuffer.AppendOrUpdate(id, tag, value)
	}
}

// triggerUpdateForTag sends an update signal for tags that modify the display
func (w *terminalOutput) triggerUpdateForTag(tag byte) {
	switch tag {
	case stream.TagAssistantText, stream.TagTool, stream.TagReasoning, stream.TagError,
		stream.TagNotify, stream.TagSystem, stream.TagUserText, stream.TagTodo:
		select {
		case w.updateChan <- struct{}{}:
		default:
		}
	}
}

// handleSystemTag processes system information tags
func (w *terminalOutput) handleSystemTag(value string) {
	var info agentpkg.SystemInfo
	if err := json.Unmarshal([]byte(value), &info); err == nil {
		w.inProgress = info.InProgress
		w.status = fmt.Sprintf("Context: %d | Total: %d", info.ContextTokens, info.TotalTokens)
	}
}

// renderMultiline applies a style to each line of text
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

// colorizeTool applies tool-specific styling to tool output
func (w *terminalOutput) colorizeTool(value string) string {
	lines := strings.Split(value, "\n")
	if len(lines) == 1 {
		return w.colorizeSingleLineTool(value)
	}
	return w.colorizeMultiLineTool(lines)
}

func (w *terminalOutput) colorizeSingleLineTool(value string) string {
	colonIdx := strings.Index(value, ":")
	if colonIdx > 0 {
		toolName := value[:colonIdx]
		rest := value[colonIdx:]
		return strings.TrimRight(w.styles.Tool.Render(toolName), " ") + strings.TrimRight(w.styles.ToolContent.Render(rest), " ")
	}
	return strings.TrimRight(w.styles.Tool.Render(value), " ")
}

func (w *terminalOutput) colorizeMultiLineTool(lines []string) string {
	var result strings.Builder
	firstLine := lines[0]
	colonIdx := strings.Index(firstLine, ":")

	if colonIdx > 0 {
		toolName := firstLine[:colonIdx]
		restFirst := firstLine[colonIdx:]
		result.WriteString(strings.TrimRight(w.styles.Tool.Render(toolName), " "))
		result.WriteString(strings.TrimRight(w.styles.ToolContent.Render(restFirst), " "))
	} else {
		result.WriteString(strings.TrimRight(w.styles.Tool.Render(firstLine), " "))
	}

	for _, line := range lines[1:] {
		result.WriteString("\n")
		result.WriteString(strings.TrimRight(w.styles.ToolContent.Render(line), " "))
	}
	return result.String()
}

// parseStreamID extracts stream ID prefix from value.
// Format: "[:id:]content". Returns id, content, true if prefix found.
func (w *terminalOutput) parseStreamID(value string) (string, string, bool) {
	const prefixStart = "[:"
	const prefixEnd = ":]"
	if !strings.HasPrefix(value, prefixStart) {
		return "", value, false
	}
	endIdx := strings.Index(value, prefixEnd)
	if endIdx == -1 {
		return "", value, false
	}
	id := value[len(prefixStart):endIdx]
	content := value[endIdx+len(prefixEnd):]
	return id, content, true
}

// generateWindowID returns a unique window ID for non-delta messages.
func (w *terminalOutput) generateWindowID() string {
	w.nextWindowID++
	return fmt.Sprintf("win%d", w.nextWindowID)
}

// SetWindowWidth updates the window buffer width.
func (w *terminalOutput) SetWindowWidth(width int) {
	w.windowBuffer.SetWidth(width)
}
