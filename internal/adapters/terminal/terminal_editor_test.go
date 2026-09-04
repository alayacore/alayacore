package terminal

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/tlv"
)

func visibleLength(s string) int {
	return Width(s)
}

func TestCtrlOOpensEditor(t *testing.T) {
	terminal := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, nil, 80, 24, theme.DefaultTheme(), nil, "theme-dark")

	msg := KeyPressMsg(Key{
		Code: 'o',
		Mod:  ModCtrl,
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
	terminal := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, nil, 80, 24, theme.DefaultTheme(), nil, "theme-dark")
	terminal.input = terminal.input.WithValue("existing input text")

	msg := KeyPressMsg(Key{
		Code: 'o',
		Mod:  ModCtrl,
	})

	model, cmd := terminal.Update(msg)
	terminal = model.(Terminal)

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
	terminal := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, nil, 80, 24, theme.DefaultTheme(), nil, "theme-dark")

	msg := EditorFinishedMsg{
		Action:  EditorActionUpdateInput,
		Content: "test content from editor",
		Err:     nil,
	}

	model, _ := terminal.Update(msg)
	terminal = model.(Terminal)

	if model == nil {
		t.Fatal("Update returned nil model")
	}

	inputValue := terminal.input.Value()
	if inputValue != "test content from editor" {
		t.Errorf("Expected exact content in input, got '%s'", inputValue)
	}
}

func TestEditorFinishedMsgWithWhitespace(t *testing.T) {
	terminal := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, nil, 80, 24, theme.DefaultTheme(), nil, "theme-dark")

	msg := EditorFinishedMsg{
		Action:  EditorActionUpdateInput,
		Content: "  content with leading and trailing spaces  \n",
		Err:     nil,
	}

	model, _ := terminal.Update(msg)
	terminal = model.(Terminal)

	if model == nil {
		t.Fatal("Update returned nil model")
	}

	// Should preserve leading/trailing spaces but strip trailing newlines.
	if terminal.input.Value() != "  content with leading and trailing spaces  " {
		t.Errorf("Expected to preserve spaces but strip trailing newline, got '%s'", terminal.input.Value())
	}
}

func TestEditorFinishedMsgWithMultipleTrailingNewlines(t *testing.T) {
	terminal := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, nil, 80, 24, theme.DefaultTheme(), nil, "theme-dark")

	msg := EditorFinishedMsg{
		Action:  EditorActionUpdateInput,
		Content: "content with multiple trailing newlines\n\n\n",
		Err:     nil,
	}

	model, _ := terminal.Update(msg)
	terminal = model.(Terminal)

	if model == nil {
		t.Fatal("Update returned nil model")
	}

	// All trailing newlines should be stripped.
	if terminal.input.Value() != "content with multiple trailing newlines" {
		t.Errorf("Expected to strip all trailing newlines, got '%s'", terminal.input.Value())
	}
}

func TestEditorFinishedMsgMultiline(t *testing.T) {
	terminal := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, nil, 80, 24, theme.DefaultTheme(), nil, "theme-dark")

	msg := EditorFinishedMsg{
		Action:  EditorActionUpdateInput,
		Content: "line1\nline2\nline3",
		Err:     nil,
	}

	model, _ := terminal.Update(msg)
	terminal = model.(Terminal)

	if model == nil {
		t.Fatal("Update returned nil model")
	}

	// Multi-line content should be stored directly in the input value.
	if terminal.input.Value() != "line1\nline2\nline3" {
		t.Errorf("Expected multi-line content preserved in input, got '%s'", terminal.input.Value())
	}
}

func TestEditorFinishedMsgWithError(t *testing.T) {
	terminal := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, nil, 80, 24, theme.DefaultTheme(), nil, "theme-dark")
	terminal.input = terminal.input.WithValue("original content")

	msg := EditorFinishedMsg{
		Action:  EditorActionUpdateInput,
		Content: "",
		Err:     fmt.Errorf("editor failed"),
	}

	model, cmd := terminal.Update(msg)

	// Execute the cmd to get the displayErrorMsg
	if cmd != nil {
		// cmd() returns a displayErrorMsg; feed it back through Update
		// so the error write happens on the event loop (consistent with
		// real runtime behavior).
		result := cmd()
		if result != nil {
			model, _ = model.Update(result)
			terminal = model.(Terminal)
		}
	}

	if model == nil {
		t.Fatal("Update returned nil model")
	}

	if terminal.input.Value() != "original content" {
		t.Errorf("Input should remain unchanged on error, got '%s'", terminal.input.Value())
	}

	displayContent := terminal.out.WindowBuffer().GetAll(-1, false)
	if displayContent == "" {
		t.Error("Expected error message in display")
	}
}

func TestEditorSelectionOrder(t *testing.T) {
	editor := getEditorCommand("")
	if editor == "" {
		t.Fatal("Expected editor to be found")
	}

	// Should return one of the editors in order: vim, vi
	// Or use EDITOR environment variable if set
	if editor != "vim" && editor != "vi" {
		t.Logf("Editor is: %s (may be set by EDITOR env var)", editor)
	}
}

// TestEditorContentCarriedInMessage verifies the editor content travels in
// the editorStartMsg (not on the shared *Editor): two opens in a row must
// each carry their own content — a regression guard for the shared-mutable
// e.content field, which could be clobbered by a second open before the
// first temp file was created.
func TestEditorContentCarriedInMessage(t *testing.T) {
	t.Setenv("EDITOR", "vim")
	e := NewEditor()

	msgA := e.Open("content A")()
	startA, ok := msgA.(editorStartMsg)
	if !ok {
		t.Fatalf("open A produced %T, want editorStartMsg", msgA)
	}
	if startA.content != "content A" {
		t.Errorf("open A content = %q, want %q", startA.content, "content A")
	}

	msgB := e.Open("content B")()
	startB, ok := msgB.(editorStartMsg)
	if !ok {
		t.Fatalf("open B produced %T, want editorStartMsg", msgB)
	}
	if startB.content != "content B" {
		t.Errorf("open B content = %q, want %q", startB.content, "content B")
	}
	// The first message must not have been clobbered by the second open.
	if startA.content != "content A" {
		t.Errorf("first message content clobbered: %q", startA.content)
	}
}

// TestEditorCreateTempFileContent verifies the temp file is written with
// exactly the content passed at open time.
func TestEditorCreateTempFileContent(t *testing.T) {
	e := NewEditor()
	name, err := e.createTempFile("hello editor")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(name)

	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello editor" {
		t.Errorf("temp file content = %q, want %q", data, "hello editor")
	}
}

func TestRenderMultiline(t *testing.T) {
	// Note: SetColorProfile is no longer needed in v2

	styles := DefaultStyles()
	// Use an existing style which should produce ANSI codes
	style := styles.System
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
	// Tool args render as plain text (no ANSI styling).
	value := "tool_name: first line\nsecond line\nthird line"
	result := defaultToolRender(value, "tool_name")
	lines := strings.Split(result, "\n")
	if len(lines) != 3 {
		t.Errorf("Expected 3 lines, got %d", len(lines))
	}
	// Plain output: no ANSI styling on any line.
	for i, line := range lines {
		if strings.Contains(line, "\x1b[") {
			t.Errorf("Line %d should be plain text, got %q", i, line)
		}
	}
	// The tool-name prefix must be stripped from the first line.
	if !strings.Contains(stripANSI(lines[0]), "first line") || strings.Contains(stripANSI(lines[0]), "tool_name:") {
		t.Errorf("First line should show bare args, got %q", stripANSI(lines[0]))
	}
}

func TestWrapContentPreservesANSI(t *testing.T) {
	// Create a styled line with ANSI escape sequences (dimmed reasoning style)
	style := NewStyle().Foreground(Color("#585b70")).Italic(true)
	styledText := style.Render("This is a long line of reasoning text that should wrap when width is limited.")

	// Test wrapping at various widths
	widths := []int{20, 40, 60}
	for _, width := range widths {
		t.Run(fmt.Sprintf("width-%d", width), func(t *testing.T) {
			wrapped := wrapContent(styledText, width)
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
	terminal := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, nil, 80, 24, theme.DefaultTheme(), nil, "theme-dark")
	terminal.input = terminal.input.WithValue("test input text")

	// Press Ctrl+C while in input window
	terminal = terminal.focusInput()
	msg := KeyPressMsg(Key{Code: 'c', Mod: ModCtrl})

	model, cmd := terminal.Update(msg)
	terminal = model.(Terminal)

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
	terminal := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, nil, 80, 24, theme.DefaultTheme(), nil, "theme-dark")
	terminal.input = terminal.input.WithValue("test input text")

	// Press Ctrl+C while in display window
	terminal = terminal.focusDisplay()
	msg := KeyPressMsg(Key{Code: 'c', Mod: ModCtrl})

	model, cmd := terminal.Update(msg)
	terminal = model.(Terminal)

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
	terminal := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, nil, 80, 24, theme.DefaultTheme(), nil, "theme-dark")
	terminal.input = terminal.input.WithValue("test input text")

	// Press Ctrl+G (should work regardless of focus)
	terminal = terminal.focusInput()
	msg := KeyPressMsg(Key{Code: 'g', Mod: ModCtrl})

	model, cmd := terminal.Update(msg)
	terminal = model.(Terminal)

	// Should return a model and no command (just shows dialog)
	if model == nil {
		t.Fatal("Update returned nil model")
	}

	if cmd != nil {
		t.Fatal("Ctrl+G should not emit command immediately, should show confirm dialog")
	}

	// Cancel confirmation dialog should be shown
	if !terminal.confirmOverlay.IsOpen() {
		t.Fatal("Ctrl+G should open confirm overlay")
	}
	if terminal.confirmOverlay.Kind() != ConfirmCancel {
		t.Errorf("Ctrl+G should set confirm overlay kind to ConfirmCancel, got %v", terminal.confirmOverlay.Kind())
	}

	// Input should remain unchanged
	if terminal.input.Value() != "test input text" {
		t.Errorf("Input should remain unchanged after Ctrl+G, got %q", terminal.input.Value())
	}

	// Test confirming the dialog by pressing 'y'
	msg = KeyPressMsg(Key{Code: 'y'})
	model, cmd = terminal.Update(msg)
	terminal = model.(Terminal)

	// Now should emit cancel command
	if cmd == nil {
		t.Fatal("Pressing 'y' should emit cancel command")
	}

	// Cancel dialog should be closed
	if terminal.confirmOverlay.IsOpen() {
		t.Errorf("Cancel dialog should be closed after confirming")
	}
}

func TestCtrlUClearsInput(t *testing.T) {
	terminal := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, nil, 80, 24, theme.DefaultTheme(), nil, "theme-dark")
	terminal.input = terminal.input.WithValue("test input text")

	// Press Ctrl+U while in input window
	terminal = terminal.focusInput()
	msg := KeyPressMsg(Key{Code: 'u', Mod: ModCtrl})

	model, cmd := terminal.Update(msg)
	terminal = model.(Terminal)

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
	// Write assistant text delta with history ID
	err := tlv.WriteTLV(out, tlv.TagAssistantTDelta, tlv.WrapID("1", "Hello"))
	if err != nil {
		t.Fatalf("WriteTLV failed: %v", err)
	}
	// Write another delta with same history ID
	err = tlv.WriteTLV(out, tlv.TagAssistantTDelta, tlv.WrapID("1", " world"))
	if err != nil {
		t.Fatalf("WriteTLV failed: %v", err)
	}
	// Write different history ID
	err = tlv.WriteTLV(out, tlv.TagAssistantTDelta, tlv.WrapID("2", "Another"))
	if err != nil {
		t.Fatalf("WriteTLV failed: %v", err)
	}
	// Flush pending deltas so windows are created in the buffer.
	out.FlushPendingDeltas()
	out.DrainDirty()
	// Check window count
	windows := out.windowBuffer.AllWindows()
	if len(windows) != 2 {
		t.Errorf("Expected 2 windows, got %d", len(windows))
	}
	// Find window with latest first delta (ID "1")
	var win1 *Window
	for _, w := range windows {
		if w.ID == "1" {
			win1 = w
			break
		}
	}
	if win1 == nil {
		t.Fatal("Window with ID \"1\" not found")
		return
	}
	// Content should have both deltas concatenated
	// Note: content is styled with color codes; we just check containment
	if !strings.Contains(win1.RawContent(), "Hello") || !strings.Contains(win1.RawContent(), "world") {
		t.Errorf("Window content missing expected parts, got: %q", win1.RawContent())
	}
	// Check window with ID "2" exists
	var win2 *Window
	for _, w := range windows {
		if w.ID == "2" {
			win2 = w
			break
		}
	}
	if win2 == nil {
		t.Fatal("Window with ID \"2\" not found")
	}
}

func TestWindowBufferRendering(t *testing.T) {
	wb := NewWindowBuffer(30, DefaultStyles())
	// Add a window with some content
	wb.AppendOrUpdate(tlv.TagAssistantT, "test1", "Hello world")
	// Get rendered output
	rendered := wb.GetAll(-1, false)
	// Check that top/bottom rule lines appear (plain horizontal rules,
	// full window width — open box with no side borders)
	if !strings.Contains(rendered, strings.Repeat("─", 30)) {
		t.Errorf("Rendered output missing rule line: %q", rendered)
	}
	// Check that content appears inside
	if !strings.Contains(rendered, "Hello world") {
		t.Errorf("Content not found in rendered output: %q", rendered)
	}
	// Check width constraint: count lines? Not needed.
	// Add another window and ensure ordering
	wb.AppendOrUpdate(tlv.TagAssistantR, "test2", "Reasoning")
	rendered2 := wb.GetAll(-1, false)
	// Should have two windows separated by newline
	// Count border top lines? Simpler: ensure both contents appear
	if !strings.Contains(rendered2, "Hello world") || !strings.Contains(rendered2, "Reasoning") {
		t.Errorf("Both window contents not found: %q", rendered2)
	}
	// Ensure ordering: first window appears before second
	idx1 := strings.Index(rendered2, "Hello world")
	idx2 := strings.Index(rendered2, "Reasoning")
	if idx1 == -1 || idx2 == -1 || idx1 >= idx2 {
		t.Errorf("Window ordering incorrect: idx1=%d, idx2=%d", idx1, idx2)
	}
}

func TestWindowBufferNonDeltaMessages(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	// Write a non-delta message (error)
	err := protocol.WriteSystemMsg(out, protocol.ErrorMsg{Text: "Something went wrong"})
	if err != nil {
		t.Fatalf("WriteSystemMsg failed: %v", err)
	}
	// Write another non-delta (notify)
	err = protocol.WriteSystemMsg(out, protocol.NotifyMsg{Text: "Notification"})
	if err != nil {
		t.Fatalf("WriteSystemMsg failed: %v", err)
	}
	// Check that two separate windows were created
	windows := out.windowBuffer.AllWindows()
	if len(windows) != 2 {
		t.Errorf("Expected 2 windows for non-delta messages, got %d", len(windows))
	}
	// Ensure they have different generated IDs
	if windows[0].ID == windows[1].ID {
		t.Errorf("Non-delta windows should have different IDs: %s", windows[0].ID)
	}
	// Ensure tags are correct (error → TagWindowSE, notify → TagWindowSN)
	if windows[0].RawTag() != TagWindowSE {
		t.Errorf("Expected SE tag, got %s", windows[0].RawTag())
	}
	if windows[1].RawTag() != TagWindowSN {
		t.Errorf("Expected SN tag, got %s", windows[1].RawTag())
	}
}

func TestWindowBufferEdgeCases(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	// Delta message without valid NUL-delimited history ID (plain text)
	// is malformed — should be silently ignored.
	err := tlv.WriteTLV(out, tlv.TagAssistantTDelta, "plain text without history ID")
	if err != nil {
		t.Fatalf("WriteTLV failed: %v", err)
	}
	// Should NOT create a window (frame rejected)
	windows := out.windowBuffer.AllWindows()
	if len(windows) != 0 {
		t.Errorf("Expected 0 windows (malformed frame), got %d", len(windows))
	}
	// Mixed delta and non-delta messages
	err = tlv.WriteTLV(out, tlv.TagAssistantTDelta, tlv.WrapID("3", "Delta"))
	if err != nil {
		t.Fatalf("WriteTLV failed: %v", err)
	}
	err = protocol.WriteSystemMsg(out, protocol.ErrorMsg{Text: "Error"})
	if err != nil {
		t.Fatalf("WriteSystemMsg failed: %v", err)
	}
	// Should have two windows total (malformed frame rejected)
	windows = out.windowBuffer.AllWindows()
	if len(windows) != 2 {
		t.Errorf("Expected 2 windows, got %d", len(windows))
	}
	// Check ordering: first delta, second error
	if windows[0].RawTag() != tlv.TagAssistantT {
		t.Errorf("First window tag mismatch, got %s", windows[0].RawTag())
	}
	if windows[1].RawTag() != TagWindowSE {
		t.Errorf("Second window tag mismatch, got %s", windows[1].RawTag())
	}
}

func TestWindowBufferWidth(t *testing.T) {
	// Test that window width matches expected total width
	const totalWidth = 50
	wb := NewWindowBuffer(totalWidth, DefaultStyles())
	wb.AppendOrUpdate(tlv.TagAssistantT, "test", "Hello")
	rendered := wb.GetAll(-1, false)
	lines := strings.Split(rendered, "\n")
	if len(lines) < 3 {
		t.Fatal("No lines rendered")
	}
	// Line 0 is the header (expand arrow + "ASSISTANT"); line 1 is the box
	// top rule.
	headerLine := lines[0]
	if !strings.Contains(headerLine, "▼") || !strings.Contains(headerLine, "ASSISTANT") {
		t.Errorf("Header line missing: %q", headerLine)
	}
	topLine := lines[1]
	// Top line should be a full-width horizontal rule (no side borders)
	if !strings.Contains(topLine, strings.Repeat("─", totalWidth)) {
		t.Errorf("Top rule missing: %q", topLine)
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
			wb.AppendOrUpdate(tlv.TagAssistantT, "test", "Content")
			rendered := wb.GetAll(-1, false)
			// Extract top border line (line 1 — line 0 is the header)
			lines := strings.Split(rendered, "\n")
			if len(lines) < 3 {
				t.Fatal("No lines rendered")
			}
			topLine := lines[1]
			// The top line visible length should equal inputTotalWidth (including border chars)
			visibleLen := visibleLength(topLine)
			t.Logf("Window header line: %q", lines[0])
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
	terminal := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, nil, 80, 24, theme.DefaultTheme(), nil, "theme-dark")
	terminal = terminal.focusDisplay()

	// Add a window with content
	terminal.out.WindowBuffer().AppendOrUpdate(tlv.TagAssistantT, "test1", "Hello from display")

	// Set cursor to first window
	terminal.display = terminal.display.WithWindowCursor(0)

	// Press 'e' key in display window
	msg := KeyPressMsg(Key{Code: 'e'})

	model, cmd := terminal.Update(msg)
	terminal = model.(Terminal)

	if model == nil {
		t.Fatal("Update returned nil model")
	}

	// Should return a command (editor open)
	if cmd == nil {
		t.Fatal("Update returned nil command - should return editor command when 'e' pressed in display with content")
	}
}

func TestEKeyDoesNothingWithNoWindow(t *testing.T) {
	terminal := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, nil, 80, 24, theme.DefaultTheme(), nil, "theme-dark")
	terminal = terminal.focusDisplay()

	// No windows in buffer
	terminal.display = terminal.display.WithWindowCursor(-1)

	// Press 'e' key in display window
	msg := KeyPressMsg(Key{Code: 'e'})

	model, cmd := terminal.Update(msg)
	terminal = model.(Terminal)

	if model == nil {
		t.Fatal("Update returned nil model")
	}

	// Should NOT return a command (no content to edit)
	if cmd != nil {
		t.Fatal("Update should return nil command when no window is selected")
	}
}

func TestEKeyDoesNothingInInputWindow(t *testing.T) {
	terminal := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, nil, 80, 24, theme.DefaultTheme(), nil, "theme-dark")
	terminal = terminal.focusInput()

	// Add a window with content
	terminal.out.WindowBuffer().AppendOrUpdate(tlv.TagAssistantT, "test1", "Hello from display")

	// Press 'e' key while in input window (should be passed to input, not open editor)
	msg := KeyPressMsg(Key{Code: 'e'})

	model, cmd := terminal.Update(msg)
	terminal = model.(Terminal)

	if model == nil {
		t.Fatal("Update returned nil model")
	}

	// Should NOT return a command (e key goes to input, not display handler)
	if cmd != nil {
		t.Fatal("Update should return nil command when 'e' pressed in input window")
	}

	// The key is passed to input handler - we don't verify input value here
	// as that tests prompt input behavior, not the 'e' key routing
}

func TestDisplayEditorFinishedDoesNotPopulateInput(t *testing.T) {
	terminal := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, nil, 80, 24, theme.DefaultTheme(), nil, "theme-dark")
	terminal.input = terminal.input.WithValue("original input")

	// Simulate display editor finishing (user viewed content, then quit)
	msg := EditorFinishedMsg{Action: EditorActionNone, Err: nil}

	model, _ := terminal.Update(msg)
	terminal = model.(Terminal)

	if model == nil {
		t.Fatal("Update returned nil model")
	}

	// Input should remain unchanged
	if terminal.input.Value() != "original input" {
		t.Errorf("Input should not be modified after display editor closes, got '%s'", terminal.input.Value())
	}
}

func TestDisplayEditorFinishedWithError(t *testing.T) {
	terminal := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, nil, 80, 24, theme.DefaultTheme(), nil, "theme-dark")
	terminal.input = terminal.input.WithValue("original input")

	// Simulate display editor finishing with error
	msg := EditorFinishedMsg{Action: EditorActionNone, Err: fmt.Errorf("editor failed")}

	model, _ := terminal.Update(msg)
	terminal = model.(Terminal)

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
	wb.HandleToolInputEvent(protocol.ToolInputData{ID: "test-id", Name: "write_file", Input: json.RawMessage(content)}, 0)

	result := wb.GetWindowContent(0)
	if result != content {
		t.Errorf("Expected write_file content with header, got: %q", result)
	}
}

func TestGetWindowContentDiff(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	// Simulate formatted diff content (what parseDiffFromFormatted produces)
	content := "edit_file: /path/to/file.txt\n- old line 1\n+ new line 1\n  same line\n"
	wb.HandleToolInputEvent(protocol.ToolInputData{ID: "test-id", Name: "edit_file", Input: json.RawMessage(content)}, 0)

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
	wb.AppendOrUpdate(tlv.TagAssistantT, "test-id", "Hello world")

	content := wb.GetWindowContent(0)
	if content != "Hello world" {
		t.Errorf("Expected regular content, got: %q", content)
	}
}
