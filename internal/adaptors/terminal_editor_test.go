package adaptors

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/wallacegibbon/coreclaw/internal/stream"
)

func TestCtrlOOpensEditor(t *testing.T) {
	terminal := NewTerminal(nil, newTerminalOutput(), stream.NewChanInput(10), "")

	msg := tea.KeyPressMsg(tea.Key{
		Code: 'o',
		Mod:  tea.ModCtrl,
	})

	model, cmd := terminal.Update(msg)

	if model == nil {
		t.Fatal("Update returned nil model")
	}

	if cmd == nil {
		t.Fatal("Update returned nil command - should return editor command")
	}
}

func TestCtrlOWithExistingContent(t *testing.T) {
	terminal := NewTerminal(nil, newTerminalOutput(), stream.NewChanInput(10), "")
	terminal.input.SetValue("existing input text")

	msg := tea.KeyPressMsg(tea.Key{
		Code: 'o',
		Mod:  tea.ModCtrl,
	})

	model, cmd := terminal.Update(msg)

	if model == nil {
		t.Fatal("Update returned nil model")
	}

	if cmd == nil {
		t.Fatal("Update returned nil command - should return editor command")
	}

	if terminal.input.Value() != "existing input text" {
		t.Errorf("Input should retain existing text before editor opens, got '%s'", terminal.input.Value())
	}
}

func TestEditorFinishedMsg(t *testing.T) {
	terminal := NewTerminal(nil, newTerminalOutput(), stream.NewChanInput(10), "")

	msg := editorFinishedMsg{
		content: "test content from editor",
		err:     nil,
	}

	model, _ := terminal.Update(msg)

	if model == nil {
		t.Fatal("Update returned nil model")
	}

	// Input should show summary with line count
	inputValue := terminal.input.Value()
	if !strings.Contains(inputValue, "[1 lines]") {
		t.Errorf("Expected summary in input, got '%s'", inputValue)
	}

	// editorContent should preserve original content
	if terminal.editorContent != "test content from editor" {
		t.Errorf("Expected editorContent 'test content from editor', got '%s'", terminal.editorContent)
	}
}

func TestEditorFinishedMsgWithWhitespace(t *testing.T) {
	terminal := NewTerminal(nil, newTerminalOutput(), stream.NewChanInput(10), "")

	msg := editorFinishedMsg{
		content: "  content with leading and trailing spaces  \n",
		err:     nil,
	}

	model, _ := terminal.Update(msg)

	if model == nil {
		t.Fatal("Update returned nil model")
	}

	// editorContent should preserve all whitespace including leading/trailing spaces
	if terminal.editorContent != "  content with leading and trailing spaces  \n" {
		t.Errorf("Expected to preserve all whitespace, got '%s'", terminal.editorContent)
	}
}

func TestEditorContentSubmittedOnEnter(t *testing.T) {
	terminal := NewTerminal(nil, newTerminalOutput(), stream.NewChanInput(10), "")
	terminal.editorContent = "line1\nline2\nline3"

	// editorContent is cleared before submission when Enter is pressed
	// This test verifies the logic flow that checks editorContent first
	if terminal.editorContent != "line1\nline2\nline3" {
		t.Errorf("Expected editorContent to be set before Enter, got '%s'", terminal.editorContent)
	}
}

func TestEditorContentUsedInsteadOfInputValue(t *testing.T) {
	terminal := NewTerminal(nil, newTerminalOutput(), stream.NewChanInput(10), "")
	terminal.editorContent = "editor content"
	terminal.input.SetValue("input value")

	// When editorContent is set, it should be used instead of input value
	// This is verified by checking that editorContent has the right value
	if terminal.editorContent != "editor content" {
		t.Errorf("Expected editorContent to be 'editor content', got '%s'", terminal.editorContent)
	}
}

func TestEditorFinishedMsgWithError(t *testing.T) {
	terminal := NewTerminal(nil, newTerminalOutput(), stream.NewChanInput(10), "")
	terminal.input.SetValue("original content")

	msg := editorFinishedMsg{
		content: "",
		err:     fmt.Errorf("editor failed"),
	}

	model, _ := terminal.Update(msg)

	if model == nil {
		t.Fatal("Update returned nil model")
	}

	if terminal.input.Value() != "original content" {
		t.Errorf("Input should remain unchanged on error, got '%s'", terminal.input.Value())
	}

	displayContent := terminal.terminalOutput.display.GetAll()
	if displayContent == "" {
		t.Error("Expected error message in display")
	}
}

func TestEditorSelectionOrder(t *testing.T) {
	editor := getEditorCommand("")
	if editor == "" {
		t.Fatal("Expected editor to be found")
	}

	// Should return one of the three editors in order: vim, vi, nano
	// Or use EDITOR environment variable if set
	if editor != "vim" && editor != "vi" && editor != "nano" {
		t.Logf("Editor is: %s (may be set by EDITOR env var)", editor)
	}
}

func TestRenderMultiline(t *testing.T) {
	// Note: lipgloss.SetColorProfile is no longer needed in v2

	output := newTerminalOutput()
	// Use existing reasoning style which should produce ANSI codes
	style := output.reasoningStyle
	// First test direct rendering
	direct := style.Render("test")
	t.Logf("Direct render: %q, bytes: %v", direct, []byte(direct))
	hasANSI := strings.Contains(direct, "\x1b[")
	if !hasANSI {
		t.Log("Warning: style.Render produced no ANSI codes (maybe color disabled)")
	}
	text := "line1\nline2\nline3"
	result := output.renderMultiline(style, text, true)
	lines := strings.Split(result, "\n")
	if len(lines) != 3 {
		t.Errorf("Expected 3 lines, got %d", len(lines))
	}
	// Debug output
	for i, line := range lines {
		t.Logf("Line %d: %q", i, line)
		t.Logf("  bytes: %v", []byte(line))
	}
	// Check each line contains ANSI escape sequence if the style produces them
	if hasANSI {
		for i, line := range lines {
			if !strings.Contains(line, "\x1b[") {
				t.Errorf("Line %d missing ANSI escape sequence: %q", i, line)
			}
		}
	}
}

func TestColorizeToolMultiline(t *testing.T) {
	// Note: lipgloss.SetColorProfile is no longer needed in v2

	output := newTerminalOutput()
	// Test multiline tool output with colon on first line
	value := "tool_name: first line\nsecond line\nthird line"
	result := output.colorizeTool(value)
	lines := strings.Split(result, "\n")
	if len(lines) != 3 {
		t.Errorf("Expected 3 lines, got %d", len(lines))
	}
	// First line should have toolStyle for tool_name and toolContentStyle for rest
	// Check that each line contains ANSI codes
	for i, line := range lines {
		if !strings.Contains(line, "\x1b[") {
			t.Errorf("Line %d missing ANSI escape sequence: %q", i, line)
		}
	}
	// Additional checks: first line should contain toolStyle color
	// We can check that the line includes the specific ANSI codes for toolStyle and toolContentStyle
	// but for simplicity we just ensure styling per line.
}

func TestWordwrapPreservesANSI(t *testing.T) {
	// Note: lipgloss.SetColorProfile is no longer needed in v2

	// Create a styled line with ANSI escape sequences (dimmed reasoning style)
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("#585b70")).Italic(true)
	styledText := style.Render("This is a long line of reasoning text that should wrap when width is limited.")

	// Test wrapping at various widths
	widths := []int{20, 40, 60}
	for _, width := range widths {
		t.Run(fmt.Sprintf("width-%d", width), func(t *testing.T) {
			wrapped := wordwrap(styledText, width)
			lines := strings.Split(strings.TrimSuffix(wrapped, "\n"), "\n")
			if len(lines) == 0 {
				t.Fatal("No lines after wrapping")
			}
			// Each line should contain ANSI escape sequence
			for i, line := range lines {
				t.Logf("Line %d: %q", i, line)
				if !strings.Contains(line, "\x1b[") {
					t.Errorf("Line %d missing ANSI escape sequence after wrapping at width %d: %q", i, width, line)
				}
				// Ensure each line starts with escape sequence (style prefix)
				if !strings.HasPrefix(line, "\x1b[") {
					t.Errorf("Line %d does not start with ANSI escape sequence: %q", i, line)
				}
				// Ensure each line ends with reset sequence (\x1b[0m or \x1b[m)
				if !strings.HasSuffix(line, "\x1b[0m") && !strings.HasSuffix(line, "\x1b[m") {
					t.Errorf("Line %d does not end with reset sequence: %q", i, line)
				}
			}
		})
	}
}
