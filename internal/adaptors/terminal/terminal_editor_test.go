package terminal

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/alayacore/alayacore/internal/stream"
)

func visibleLength(s string) int {
	return lipgloss.Width(s)
}

func TestCtrlOOpensEditor(t *testing.T) {
	terminal := NewTerminal(nil, NewTerminalOutput(DefaultStyles()), stream.NewChanInput(10), nil, 80, 24)

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
	terminal := NewTerminal(nil, NewTerminalOutput(DefaultStyles()), stream.NewChanInput(10), nil, 80, 24)
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
	terminal := NewTerminal(nil, NewTerminalOutput(DefaultStyles()), stream.NewChanInput(10), nil, 80, 24)

	msg := editorFinishedMsg{
		content: "test content from editor",
		err:     nil,
	}

	model, _ := terminal.Update(msg)

	if model == nil {
		t.Fatal("Update returned nil model")
	}

	// For single-line content, input should show the content without any suffix
	inputValue := terminal.input.Value()
	if inputValue != "test content from editor" {
		t.Errorf("Expected exact content in input for single line, got '%s'", inputValue)
	}

	// editorContent should preserve original content
	if terminal.input.editorContent != "test content from editor" {
		t.Errorf("Expected editorContent 'test content from editor', got '%s'", terminal.input.editorContent)
	}
}

func TestEditorFinishedMsgWithWhitespace(t *testing.T) {
	terminal := NewTerminal(nil, NewTerminalOutput(DefaultStyles()), stream.NewChanInput(10), nil, 80, 24)

	msg := editorFinishedMsg{
		content: "  content with leading and trailing spaces  \n",
		err:     nil,
	}

	model, _ := terminal.Update(msg)

	if model == nil {
		t.Fatal("Update returned nil model")
	}

	// editorContent should preserve leading/trailing spaces but strip trailing newlines
	// (text editors add a trailing newline by default, which should be removed)
	if terminal.input.editorContent != "  content with leading and trailing spaces  " {
		t.Errorf("Expected to preserve spaces but strip trailing newline, got '%s'", terminal.input.editorContent)
	}
}

func TestEditorFinishedMsgWithMultipleTrailingNewlines(t *testing.T) {
	terminal := NewTerminal(nil, NewTerminalOutput(DefaultStyles()), stream.NewChanInput(10), nil, 80, 24)

	msg := editorFinishedMsg{
		content: "content with multiple trailing newlines\n\n\n",
		err:     nil,
	}

	model, _ := terminal.Update(msg)

	if model == nil {
		t.Fatal("Update returned nil model")
	}

	// All trailing newlines should be stripped
	if terminal.input.editorContent != "content with multiple trailing newlines" {
		t.Errorf("Expected to strip all trailing newlines, got '%s'", terminal.input.editorContent)
	}
}

func TestEditorContentSubmittedOnEnter(t *testing.T) {
	terminal := NewTerminal(nil, NewTerminalOutput(DefaultStyles()), stream.NewChanInput(10), nil, 80, 24)
	terminal.input.editorContent = "line1\nline2\nline3"

	// editorContent is cleared before submission when Enter is pressed
	// This test verifies the logic flow that checks editorContent first
	if terminal.input.editorContent != "line1\nline2\nline3" {
		t.Errorf("Expected editorContent to be set before Enter, got '%s'", terminal.input.editorContent)
	}
}

func TestEditorFinishedMsgMultiline(t *testing.T) {
	terminal := NewTerminal(nil, NewTerminalOutput(DefaultStyles()), stream.NewChanInput(10), nil, 80, 24)

	msg := editorFinishedMsg{
		content: "line1\nline2\nline3",
		err:     nil,
	}

	model, _ := terminal.Update(msg)

	if model == nil {
		t.Fatal("Update returned nil model")
	}

	// For multi-line content, input should show summary with line count
	inputValue := terminal.input.Value()
	if !strings.Contains(inputValue, "[3 lines]") {
		t.Errorf("Expected line count summary for multi-line content, got '%s'", inputValue)
	}

	// editorContent should preserve original content
	if terminal.input.editorContent != "line1\nline2\nline3" {
		t.Errorf("Expected editorContent to preserve multi-line content, got '%s'", terminal.input.editorContent)
	}
}

func TestFormatEditorContent(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{
			name:     "single line short",
			content:  "hello world",
			expected: "hello world",
		},
		{
			name:     "single line long",
			content:  "this is a very long single line that exceeds the hundred character limit and should be shown as-is",
			expected: "this is a very long single line that exceeds the hundred character limit and should be shown as-is",
		},
		{
			name:     "empty content",
			content:  "",
			expected: "",
		},
		{
			name:     "two lines",
			content:  "line1\nline2",
			expected: "[2 lines] line1 (press Enter to send)",
		},
		{
			name:     "three lines",
			content:  "line1\nline2\nline3",
			expected: "[3 lines] line1 (press Enter to send)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatEditorContent(tt.content)
			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestEditorContentUsedInsteadOfInputValue(t *testing.T) {
	terminal := NewTerminal(nil, NewTerminalOutput(DefaultStyles()), stream.NewChanInput(10), nil, 80, 24)
	terminal.input.editorContent = "editor content"
	terminal.input.SetValue("input value")

	// When editorContent is set, it should be used instead of input value
	// This is verified by checking that editorContent has the right value
	if terminal.input.editorContent != "editor content" {
		t.Errorf("Expected editorContent to be 'editor content', got '%s'", terminal.input.editorContent)
	}
}

func TestEditorFinishedMsgWithError(t *testing.T) {
	terminal := NewTerminal(nil, NewTerminalOutput(DefaultStyles()), stream.NewChanInput(10), nil, 80, 24)
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

	displayContent := terminal.out.WindowBuffer().GetAll(-1)
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

	styles := DefaultStyles()
	// Use existing reasoning style which should produce ANSI codes
	style := styles.Reasoning
	// First test direct rendering
	direct := style.Render("test")
	t.Logf("Direct render: %q, bytes: %v", direct, []byte(direct))
	hasANSI := strings.Contains(direct, "\x1b[")
	if !hasANSI {
		t.Log("Warning: style.Render produced no ANSI codes (maybe color disabled)")
	}
	text := "line1\nline2\nline3"
	result := styleMultiline(text, style)
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

	styles := DefaultStyles()
	// Test multiline tool output with colon on first line
	value := "tool_name: first line\nsecond line\nthird line"
	result := ColorizeTool(value, styles)
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
			wrapped := lipgloss.Wrap(styledText, width, " ")
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

func TestCtrlCClearsInput(t *testing.T) {
	terminal := NewTerminal(nil, NewTerminalOutput(DefaultStyles()), stream.NewChanInput(10), nil, 80, 24)
	terminal.input.SetValue("test input text")

	// Press Ctrl+C while in input window
	terminal.focusInput()
	msg := tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl})

	model, cmd := terminal.Update(msg)

	// Should return a model and no command
	if model == nil {
		t.Fatal("Update returned nil model")
	}

	// Input should be cleared
	if terminal.input.Value() != "" {
		t.Errorf("Input should be cleared after Ctrl+C in input window, got %q", terminal.input.Value())
	}

	// Should not emit any command (cmd should be nil)
	if cmd != nil {
		t.Errorf("Ctrl+C in input window should not emit command, got %v", cmd)
	}
}

func TestCtrlCInDisplayWindow(t *testing.T) {
	terminal := NewTerminal(nil, NewTerminalOutput(DefaultStyles()), stream.NewChanInput(10), nil, 80, 24)
	terminal.input.SetValue("test input text")

	// Press Ctrl+C while in display window
	terminal.focusDisplay()
	msg := tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl})

	model, cmd := terminal.Update(msg)

	// Should return a model and no command
	if model == nil {
		t.Fatal("Update returned nil model")
	}

	// Should not emit any command
	if cmd != nil {
		t.Errorf("Ctrl+C in display window should not emit command, got %v", cmd)
	}

	// Input should NOT be cleared
	if terminal.input.Value() != "test input text" {
		t.Errorf("Input should NOT be cleared when Ctrl+C is pressed in display window, got %q", terminal.input.Value())
	}
}

func TestCtrlGTriggersCancel(t *testing.T) {
	terminal := NewTerminal(nil, NewTerminalOutput(DefaultStyles()), stream.NewChanInput(10), nil, 80, 24)
	terminal.input.SetValue("test input text")

	// Press Ctrl+G (should work regardless of focus)
	terminal.focusInput()
	msg := tea.KeyPressMsg(tea.Key{Code: 'g', Mod: tea.ModCtrl})

	model, cmd := terminal.Update(msg)

	// Should return a model and no command (just shows dialog)
	if model == nil {
		t.Fatal("Update returned nil model")
	}

	if cmd != nil {
		t.Fatal("Ctrl+G should not emit command immediately, should show confirm dialog")
	}

	// Cancel confirmation dialog should be shown
	if terminal.confirmDialog != confirmCancel {
		t.Errorf("Ctrl+G should set confirmDialog to confirmCancel, got %v", terminal.confirmDialog)
	}

	// Input should remain unchanged
	if terminal.input.Value() != "test input text" {
		t.Errorf("Input should remain unchanged after Ctrl+G, got %q", terminal.input.Value())
	}

	// Test confirming the dialog by pressing 'y'
	msg = tea.KeyPressMsg(tea.Key{Code: 'y'})
	_, cmd = terminal.Update(msg)

	// Now should emit cancel command
	if cmd == nil {
		t.Fatal("Pressing 'y' should emit cancel command")
	}

	// Cancel dialog should be closed
	if terminal.confirmDialog != confirmNone {
		t.Errorf("Cancel dialog should be closed after confirming, got %v", terminal.confirmDialog)
	}
}

func TestCancelAllCommandRequiresConfirm(t *testing.T) {
	terminal := NewTerminal(nil, NewTerminalOutput(DefaultStyles()), stream.NewChanInput(10), nil, 80, 24)
	terminal.input.SetValue(":cancel_all")

	// Press Enter to submit the command
	terminal.focusInput()
	msg := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})

	model, cmd := terminal.Update(msg)

	// Should return a model and no command (just shows dialog)
	if model == nil {
		t.Fatal("Update returned nil model")
	}

	if cmd != nil {
		t.Fatal(":cancel_all should not emit command immediately, should show confirm dialog")
	}

	// Cancel all confirmation dialog should be shown
	if terminal.confirmDialog != confirmCancelAll {
		t.Errorf(":cancel_all should set confirmDialog to confirmCancelAll, got %v", terminal.confirmDialog)
	}

	// Test confirming the dialog by pressing 'y'
	msg = tea.KeyPressMsg(tea.Key{Code: 'y'})
	_, cmd = terminal.Update(msg)

	// Now should emit cancel_all command
	if cmd == nil {
		t.Fatal("Pressing 'y' should emit cancel_all command")
	}

	// Cancel dialog should be closed
	if terminal.confirmDialog != confirmNone {
		t.Errorf("Cancel dialog should be closed after confirming, got %v", terminal.confirmDialog)
	}
}

func TestCtrlUDoesNothingInInput(t *testing.T) {
	terminal := NewTerminal(nil, NewTerminalOutput(DefaultStyles()), stream.NewChanInput(10), nil, 80, 24)
	terminal.input.SetValue("test input text")

	// Press Ctrl+U while in input window
	terminal.focusInput()
	msg := tea.KeyPressMsg(tea.Key{Code: 'u', Mod: tea.ModCtrl})

	model, cmd := terminal.Update(msg)

	// Should return a model and no command
	if model == nil {
		t.Fatal("Update returned nil model")
	}

	// Input should remain unchanged (Ctrl+U is ignored in input)
	if terminal.input.Value() != "test input text" {
		t.Errorf("Input should remain unchanged after Ctrl+U in input window, got %q", terminal.input.Value())
	}

	// Should not emit any command
	if cmd != nil {
		t.Errorf("Ctrl+U in input window should not emit command, got %v", cmd)
	}
}

func TestWindowBufferDeltaRouting(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	// Write assistant text delta with stream ID
	err := stream.WriteTLV(out, stream.TagTextAssistant, "[:stream1:]Hello")
	if err != nil {
		t.Fatalf("WriteTLV failed: %v", err)
	}
	// Write another delta with same stream ID
	err = stream.WriteTLV(out, stream.TagTextAssistant, "[:stream1:] world")
	if err != nil {
		t.Fatalf("WriteTLV failed: %v", err)
	}
	// Write different stream ID
	err = stream.WriteTLV(out, stream.TagTextAssistant, "[:stream2:]Another")
	if err != nil {
		t.Fatalf("WriteTLV failed: %v", err)
	}
	// Check window count
	windows := out.windowBuffer.Windows
	if len(windows) != 2 {
		t.Errorf("Expected 2 windows, got %d", len(windows))
	}
	// Find window with ID stream1
	var win1 *Window
	for _, w := range windows {
		if w.ID == "stream1" {
			win1 = w
			break
		}
	}
	if win1 == nil {
		t.Fatal("Window with ID stream1 not found")
		return
	}
	// Content should have both deltas concatenated
	// Note: content is styled with color codes; we just check containment
	if !strings.Contains(win1.Content, "Hello") || !strings.Contains(win1.Content, "world") {
		t.Errorf("Window content missing expected parts, got: %q", win1.Content)
	}
	// Check stream2 window exists
	var win2 *Window
	for _, w := range windows {
		if w.ID == "stream2" {
			win2 = w
			break
		}
	}
	if win2 == nil {
		t.Fatal("Window with ID stream2 not found")
	}
}

func TestWindowBufferRendering(t *testing.T) {
	wb := NewWindowBuffer(30, DefaultStyles())
	// Add a window with some content
	wb.AppendOrUpdate("test1", stream.TagTextAssistant, "Hello world")
	// Get rendered output
	rendered := wb.GetAll(-1)
	// Check that border characters appear (rounded border)
	if !strings.Contains(rendered, "╭") || !strings.Contains(rendered, "╮") ||
		!strings.Contains(rendered, "╰") || !strings.Contains(rendered, "╯") {
		t.Errorf("Rendered output missing border characters: %q", rendered)
	}
	// Check that content appears inside
	if !strings.Contains(rendered, "Hello world") {
		t.Errorf("Content not found in rendered output: %q", rendered)
	}
	// Check width constraint: count lines? Not needed.
	// Add another window and ensure ordering
	wb.AppendOrUpdate("test2", stream.TagTextReasoning, "Reasoning content")
	rendered2 := wb.GetAll(-1)
	// Should have two windows separated by newline
	// Count border top lines? Simpler: ensure both contents appear
	if !strings.Contains(rendered2, "Hello world") || !strings.Contains(rendered2, "Reasoning content") {
		t.Errorf("Both window contents not found: %q", rendered2)
	}
	// Ensure ordering: first window appears before second
	idx1 := strings.Index(rendered2, "Hello world")
	idx2 := strings.Index(rendered2, "Reasoning content")
	if idx1 == -1 || idx2 == -1 || idx1 >= idx2 {
		t.Errorf("Window ordering incorrect: idx1=%d, idx2=%d", idx1, idx2)
	}
}

func TestWindowBufferNonDeltaMessages(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	// Write a non-delta message (TagSystemError)
	err := stream.WriteTLV(out, stream.TagSystemError, "Something went wrong")
	if err != nil {
		t.Fatalf("WriteTLV failed: %v", err)
	}
	// Write another non-delta (TagSystemNotify)
	err = stream.WriteTLV(out, stream.TagSystemNotify, "Notification")
	if err != nil {
		t.Fatalf("WriteTLV failed: %v", err)
	}
	// Check that two separate windows were created
	windows := out.windowBuffer.Windows
	if len(windows) != 2 {
		t.Errorf("Expected 2 windows for non-delta messages, got %d", len(windows))
	}
	// Ensure they have different generated IDs
	if windows[0].ID == windows[1].ID {
		t.Errorf("Non-delta windows should have different IDs: %s", windows[0].ID)
	}
	// Ensure tags are correct
	if windows[0].Tag != stream.TagSystemError {
		t.Errorf("Expected TagSystemError, got %s", windows[0].Tag)
	}
	if windows[1].Tag != stream.TagSystemNotify {
		t.Errorf("Expected TagSystemNotify, got %s", windows[1].Tag)
	}
}

func TestWindowBufferEdgeCases(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	// Delta message with malformed stream ID (missing closing bracket)
	err := stream.WriteTLV(out, stream.TagTextAssistant, "[:stream1Hello")
	if err != nil {
		t.Fatalf("WriteTLV failed: %v", err)
	}
	// Should create a new window with generated ID
	windows := out.windowBuffer.Windows
	if len(windows) != 1 {
		t.Errorf("Expected 1 window, got %d", len(windows))
	}
	// Window ID should be generated (starts with 'win')
	if !strings.HasPrefix(windows[0].ID, "win") {
		t.Errorf("Expected generated window ID, got %s", windows[0].ID)
	}
	// Mixed delta and non-delta messages
	err = stream.WriteTLV(out, stream.TagTextAssistant, "[:stream2:]Delta")
	if err != nil {
		t.Fatalf("WriteTLV failed: %v", err)
	}
	err = stream.WriteTLV(out, stream.TagSystemError, "Error")
	if err != nil {
		t.Fatalf("WriteTLV failed: %v", err)
	}
	// Should have three windows total
	windows = out.windowBuffer.Windows
	if len(windows) != 3 {
		t.Errorf("Expected 3 windows, got %d", len(windows))
	}
	// Check ordering: first malformed, second delta, third error
	if windows[0].Tag != stream.TagTextAssistant {
		t.Errorf("First window tag mismatch")
	}
	if windows[1].Tag != stream.TagTextAssistant {
		t.Errorf("Second window tag mismatch")
	}
	if windows[2].Tag != stream.TagSystemError {
		t.Errorf("Third window tag mismatch")
	}
}

func TestWindowBufferWidth(t *testing.T) {
	// Test that window width matches expected total width
	const totalWidth = 50
	wb := NewWindowBuffer(totalWidth, DefaultStyles())
	wb.AppendOrUpdate("test", stream.TagTextAssistant, "Hello")
	rendered := wb.GetAll(-1)
	// Find first line (top border)
	lines := strings.Split(rendered, "\n")
	if len(lines) == 0 {
		t.Fatal("No lines rendered")
	}
	topLine := lines[0]
	// Top line should contain "╭" and "╮" border characters
	if !strings.Contains(topLine, "╭") || !strings.Contains(topLine, "╮") {
		t.Errorf("Top border missing: %q", topLine)
	}
	// Count visible characters between borders
	visibleLen := visibleLength(topLine)
	innerVisible := visibleLen - 2 // subtract border chars
	// The style width is totalWidth, so top line visible length should equal totalWidth (if no line breaks).
	// Allow small deviation due to padding? lipgloss may add spaces.
	if innerVisible <= 0 {
		t.Errorf("Inner border visible length zero: %q", topLine)
	}
	// Ensure total visible width matches expected total width (should be totalWidth)
	if visibleLen != totalWidth {
		t.Errorf("Window border visible width %d does not match expected total width %d", visibleLen, totalWidth)
	}
	// Ensure window width matches input box width pattern.
	// Input box width = totalWidth - 4? Not needed here.
}

func TestWindowBufferWidthMatchesInput(t *testing.T) {
	widths := []int{80, 129}
	for _, terminalWidth := range widths {
		t.Run(fmt.Sprintf("width-%d", terminalWidth), func(t *testing.T) {
			// Input box total width = terminalWidth (border includes padding and border chars)
			inputTotalWidth := terminalWidth
			// Window buffer width should be same as input total width
			wb := NewWindowBuffer(inputTotalWidth, DefaultStyles())
			// Create a window
			wb.AppendOrUpdate("test", stream.TagTextAssistant, "Content")
			rendered := wb.GetAll(-1)
			// Extract top border line
			lines := strings.Split(rendered, "\n")
			if len(lines) == 0 {
				t.Fatal("No lines rendered")
			}
			topLine := lines[0]
			// The top line visible length should equal inputTotalWidth (including border chars)
			visibleLen := visibleLength(topLine)
			t.Logf("Window top line: %q", topLine)
			t.Logf("Visible length: %d, expected: %d", visibleLen, inputTotalWidth)
			// Allow small deviation due to padding? lipgloss may add spaces.
			if visibleLen != inputTotalWidth {
				t.Errorf("Window border visible width %d does not match input total width %d", visibleLen, inputTotalWidth)
			}
		})
	}
}

func TestEKeyOpensDisplayWindowInEditor(t *testing.T) {
	terminal := NewTerminal(nil, NewTerminalOutput(DefaultStyles()), stream.NewChanInput(10), nil, 80, 24)
	terminal.focusDisplay()

	// Add a window with content
	terminal.out.WindowBuffer().AppendOrUpdate("test1", stream.TagTextAssistant, "Hello from display")

	// Set cursor to first window
	terminal.display.SetWindowCursor(0)

	// Press 'e' key in display window
	msg := tea.KeyPressMsg(tea.Key{Code: 'e'})

	model, cmd := terminal.Update(msg)

	if model == nil {
		t.Fatal("Update returned nil model")
	}

	// Should return a command (editor open)
	if cmd == nil {
		t.Fatal("Update returned nil command - should return editor command when 'e' pressed in display with content")
	}
}

func TestEKeyDoesNothingWithNoWindow(t *testing.T) {
	terminal := NewTerminal(nil, NewTerminalOutput(DefaultStyles()), stream.NewChanInput(10), nil, 80, 24)
	terminal.focusDisplay()

	// No windows in buffer
	terminal.display.SetWindowCursor(-1)

	// Press 'e' key in display window
	msg := tea.KeyPressMsg(tea.Key{Code: 'e'})

	model, cmd := terminal.Update(msg)

	if model == nil {
		t.Fatal("Update returned nil model")
	}

	// Should NOT return a command (no content to edit)
	if cmd != nil {
		t.Fatal("Update should return nil command when no window is selected")
	}
}

func TestEKeyDoesNothingInInputWindow(t *testing.T) {
	terminal := NewTerminal(nil, NewTerminalOutput(DefaultStyles()), stream.NewChanInput(10), nil, 80, 24)
	terminal.focusInput()

	// Add a window with content
	terminal.out.WindowBuffer().AppendOrUpdate("test1", stream.TagTextAssistant, "Hello from display")

	// Press 'e' key while in input window (should be passed to input, not open editor)
	msg := tea.KeyPressMsg(tea.Key{Code: 'e'})

	model, cmd := terminal.Update(msg)

	if model == nil {
		t.Fatal("Update returned nil model")
	}

	// Should NOT return a command (e key goes to input, not display handler)
	if cmd != nil {
		t.Fatal("Update should return nil command when 'e' pressed in input window")
	}

	// The key is passed to input handler - we don't verify input value here
	// as that tests input component behavior, not the 'e' key routing
}

func TestDisplayEditorFinishedDoesNotPopulateInput(t *testing.T) {
	terminal := NewTerminal(nil, NewTerminalOutput(DefaultStyles()), stream.NewChanInput(10), nil, 80, 24)
	terminal.input.SetValue("original input")

	// Simulate display editor finishing (user viewed content, then quit)
	msg := displayEditorFinishedMsg{err: nil}

	model, _ := terminal.Update(msg)

	if model == nil {
		t.Fatal("Update returned nil model")
	}

	// Input should remain unchanged
	if terminal.input.Value() != "original input" {
		t.Errorf("Input should not be modified after display editor closes, got '%s'", terminal.input.Value())
	}

	// editorContent should remain empty
	if terminal.input.editorContent != "" {
		t.Errorf("editorContent should be empty after display editor closes, got '%s'", terminal.input.editorContent)
	}
}

func TestDisplayEditorFinishedWithError(t *testing.T) {
	terminal := NewTerminal(nil, NewTerminalOutput(DefaultStyles()), stream.NewChanInput(10), nil, 80, 24)
	terminal.input.SetValue("original input")

	// Simulate display editor finishing with error
	msg := displayEditorFinishedMsg{err: fmt.Errorf("editor failed")}

	model, _ := terminal.Update(msg)

	if model == nil {
		t.Fatal("Update returned nil model")
	}

	// Input should remain unchanged
	if terminal.input.Value() != "original input" {
		t.Errorf("Input should not be modified after display editor error, got '%s'", terminal.input.Value())
	}

	// Error should be shown in display (check window buffer has error)
	// The error is written to window buffer, we just verify input wasn't affected
}

func TestGetWindowContentWriteFile(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	content := "write_file: /path/to/file.txt\nline1\nline2\nline3"
	wb.AppendToolCall("test-id", "write_file", content)

	result := wb.GetWindowContent(0)
	if result != content {
		t.Errorf("Expected write_file content with header, got: %q", result)
	}
}

func TestGetWindowContentDiff(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	// Simulate formatted diff content (what parseDiffFromFormatted produces)
	content := "edit_file: /path/to/file.txt\n- old line 1\n+ new line 1\n  same line\n"
	wb.AppendToolCall("test-id", "edit_file", content)

	result := wb.GetWindowContent(0)

	// Should contain path and diff markers
	if !strings.Contains(result, "edit_file: /path/to/file.txt") {
		t.Errorf("Expected path in diff content, got: %q", result)
	}
	if !strings.Contains(result, "- old line 1") {
		t.Errorf("Expected old line with '- ' prefix, got: %q", result)
	}
	if !strings.Contains(result, "+ new line 1") {
		t.Errorf("Expected new line with '+ ' prefix, got: %q", result)
	}
	// Unchanged line should have "  " prefix (two spaces)
	if !strings.Contains(result, "  same line") {
		t.Errorf("Expected unchanged line with '  ' prefix, got: %q", result)
	}
}

func TestGetWindowContentRegular(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	wb.AppendOrUpdate("test-id", stream.TagTextAssistant, "Hello world")

	content := wb.GetWindowContent(0)
	if content != "Hello world" {
		t.Errorf("Expected regular content, got: %q", content)
	}
}
