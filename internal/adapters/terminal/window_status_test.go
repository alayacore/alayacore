package terminal

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/tlv"
)

func TestHandleToolInputEvent(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Send a "call" type event (creates the window with Name set = start frame)
	wb.HandleToolInputEvent(protocol.ToolInputData{
		ID:    "tool123",
		Name:  "execute_command",
		Input: json.RawMessage("execute_command: git status"),
	}, 0)

	// Verify window was created
	if wb.WindowCount() != 1 {
		t.Fatalf("Expected 1 window, got %d", wb.WindowCount())
	}

	// Status defaults to pending (inferred from window creation)
	if wb.WindowAt(0).RawStatus() != ToolStatusPending {
		t.Errorf("Expected ToolStatusPending, got %v", wb.WindowAt(0).RawStatus())
	}

	// Send a result
	wb.HandleToolOutput("tool123", "output text", false, 0)

	// Check status was updated
	if wb.WindowAt(0).RawStatus() != ToolStatusSuccess {
		t.Errorf("Expected ToolStatusSuccess, got %v", wb.WindowAt(0).RawStatus())
	}

	// Send a result with error
	wb.HandleToolOutput("tool123", "error output", true, 0)

	// Check status was updated
	if wb.WindowAt(0).RawStatus() != ToolStatusError {
		t.Errorf("Expected ToolStatusError, got %v", wb.WindowAt(0).RawStatus())
	}

	// Try to update non-existent window (should not crash)
	wb.HandleToolOutput("nonexistent", "output", false, 0)
}

func TestRenderWindowContentWithStatus(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Create a tool window (Name set = start frame)
	wb.HandleToolInputEvent(protocol.ToolInputData{
		ID:    "tool123",
		Name:  "execute_command",
		Input: json.RawMessage("execute_command: git status"),
	}, 0)

	// Test rendering with pending status (default on creation)
	w := wb.WindowAt(0)
	content := wb.RenderWindowContent(w, 76)
	if content == "" {
		t.Error("Expected non-empty content")
	}
	// The status dot lives in the header line (TOOL CALL ⠋), not the content —
	// content shows the bare argument without the tool-name prefix.
	if contains(content, statusDot) {
		t.Errorf("Content should not contain a status dot, got: %s", content)
	}
	if !contains(stripANSI(content), "git status") {
		t.Errorf("Expected bare argument in content, got: %s", stripANSI(content))
	}

	// Send result with success
	wb.HandleToolOutput("tool123", "output", false, 0)

	// Test rendering with success status: input + "───" + output
	content = wb.RenderWindowContent(w, 76)
	if !contains(stripANSI(content), "git status\n───\noutput") {
		t.Errorf("Expected input --- output, got: %s", stripANSI(content))
	}

	// Send result with error
	wb.HandleToolOutput("tool123", "error output", true, 0)

	// Test rendering with error status
	content = wb.RenderWindowContent(w, 76)
	if !contains(stripANSI(content), "git status\n───\nerror output") {
		t.Errorf("Expected input --- error output, got: %s", stripANSI(content))
	}
}

// contains checks if a string contains a substring
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestOutputWriterToolCallStartThenFull(t *testing.T) {
	// End-to-end test: write TagAssistantF TLVs through the actual
	// outputWriter pipeline (Write → processBuffer → writeColored → HandleToolInputEvent).
	out := NewTerminalOutput(NewStyles(theme.DefaultTheme()))
	out.SetWindowWidth(80)

	makeStartFD := func(id, name string) []byte {
		fd, _ := json.Marshal(protocol.ToolInputData{
			ID:   id,
			Name: name,
		})
		return encodeTestTLV(tlv.TagAssistantF, tlv.WrapID("1", string(fd)))
	}

	makeInputFD := func(id, input string) []byte {
		fd, _ := json.Marshal(protocol.ToolInputData{
			ID:    id,
			Input: json.RawMessage(input),
		})
		return encodeTestTLV(tlv.TagAssistantF, tlv.WrapID("1", string(fd)))
	}

	// 1. Simulate ToolCallStart: Name set, no input yet
	_, err := out.Write(makeStartFD("call-abc", "write_file"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	wb := out.WindowBuffer()
	if wb.WindowCount() != 1 {
		t.Fatalf("Expected 1 window after start event, got %d", wb.WindowCount())
	}
	w := wb.WindowAt(0)
	if w.RawToolName() != "write_file" {
		t.Errorf("Expected tool name 'write_file', got %q", w.RawToolName())
	}

	// 2. Simulate ToolCallComplete: Name nil with full JSON input
	_, err = out.Write(makeInputFD("call-abc", `{"path":"/tmp/f.txt","content":"hello world"}`))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if wb.WindowCount() != 1 {
		t.Fatalf("Expected still 1 window, got %d", wb.WindowCount())
	}
	w = wb.WindowAt(0)
	if ti := w.ToolInfo(); ti == nil || ti.Input != "write_file: /tmp/f.txt\nhello world" {
		t.Errorf("Expected full input, got %v", w.ToolInfo())
	}
}

func TestToolRendererDeltaTruncation(t *testing.T) {
	styles := DefaultStyles()

	tests := []struct {
		name      string
		innerW    int
		toolName  string
		delta     string
		wantLines int
		wantTrim  bool // expect "…" truncation
	}{
		{
			name:      "short delta fits",
			innerW:    80,
			toolName:  "write_file",
			delta:     `{"path":"/tmp/foo"}`,
			wantLines: 3, // top border + content + bottom border
			wantTrim:  false,
		},
		{
			name:      "long delta truncated",
			innerW:    30,
			toolName:  "write_file",
			delta:     `{"path":"/home/user/very/long/path/that/exceeds/width/for/preview"}`,
			wantLines: 3, // still single content line
			wantTrim:  true,
		},
		{
			name:      "narrow window",
			innerW:    10,
			toolName:  "cat",
			delta:     `{"path":"/tmp/foo"}`,
			wantLines: 3,
			// Bare delta preview gets the full width (10) — still truncated
			// because the delta is longer than 10 columns.
			wantTrim: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Status is Pending here on purpose: while arguments are still
			// streaming in, the tool has not started executing. The delta
			// preview is bare JSON — no dot, no tool-name prefix (both live
			// in the header line).
			tr := &toolRenderer{
				name:        tt.toolName,
				deltaBuffer: tt.delta,
				status:      ToolStatusPending,
			}
			lines, lineCount := tr.BuildInner(tt.innerW, false, styles)
			result := joinVisualLines(lines)

			if lineCount != tt.wantLines {
				t.Errorf("Expected %d lines, got %d", tt.wantLines, lineCount)
			}

			// Streaming preview: bare delta, no status dot at all.
			if strings.Contains(result, statusDot) {
				t.Errorf("Streaming preview should not contain status dots, got: %q", result)
			}

			hasTrim := strings.Contains(result, "…")
			if hasTrim != tt.wantTrim {
				t.Errorf("Truncation mismatch: wantTrim=%v, got contains-ellipsis=%v\nResult: %q", tt.wantTrim, hasTrim, result)
			}
		})
	}
}

// ============================================================================
// Uf preview truncation (like Af)
// ============================================================================

func TestToolRendererUfPreviewTruncated(t *testing.T) {
	// Pending + over-long output (Uf preview) → truncated to a single line.
	styles := NewStyles(theme.DefaultTheme())
	tr := &toolRenderer{
		name:   "execute_command",
		input:  "command: ls -la",
		output: strings.Repeat("x", 200),
		status: ToolStatusPending,
	}

	lines, lineCount := tr.BuildInner(80, false, styles)
	result := joinVisualLines(lines)
	if lineCount != 5 {
		t.Errorf("lineCount = %d, want 5 (input + --- + preview + box rules)", lineCount)
	}
	if strings.Contains(result, strings.Repeat("x", 200)) {
		t.Error("preview should be truncated, full 200-char output leaked")
	}
	if !strings.Contains(result, "…") {
		t.Errorf("expected truncation ellipsis, got %q", result)
	}
}

func TestToolRendererUfPreviewShort(t *testing.T) {
	// Pending + short output (Uf preview) → single line, no truncation.
	styles := NewStyles(theme.DefaultTheme())
	tr := &toolRenderer{
		name:   "execute_command",
		input:  "command: ls -la",
		output: " 42%",
		status: ToolStatusPending,
	}

	lines, lineCount := tr.BuildInner(80, false, styles)
	result := joinVisualLines(lines)
	if lineCount != 5 {
		t.Errorf("lineCount = %d, want 5 (input + --- + preview + box rules)", lineCount)
	}
	if !strings.Contains(result, " 42%") {
		t.Errorf("preview should contain %q, got %q", " 42%", result)
	}
	if strings.Contains(result, "…") {
		t.Errorf("short preview should not be truncated, got %q", result)
	}
}

func TestToolRendererUfPreviewAuthoritative(t *testing.T) {
	// Success (UF arrived) + long output → full multiline rendering,
	// no flatten/truncate applied.
	styles := NewStyles(theme.DefaultTheme())
	long := strings.Repeat("line content that wraps\n", 3)
	tr := &toolRenderer{
		name:   "execute_command",
		input:  "command: ls -la",
		output: long,
		status: ToolStatusSuccess,
	}

	_, lineCount := tr.BuildInner(80, false, styles)
	if lineCount <= 3 {
		t.Errorf("lineCount = %d, want > 3 (multiline authoritative output)", lineCount)
	}
	if !strings.Contains(tr.output, "line content that wraps\n") {
		t.Error("authoritative output should be untouched")
	}
}

func TestToolRendererUfPreviewTabs(t *testing.T) {
	// Uf preview containing tabs: tabs must be expanded before truncation
	// so width accounting matches the final render (expandTabs = 8 cols).
	// ansi.Hardwrap counts a tab as 0 width, so truncating raw tabs lets
	// the expanded preview overflow the window and soft-wrap at the
	// terminal (invisible to lineCount, which only counts '\n').
	styles := NewStyles(theme.DefaultTheme())
	tr := &toolRenderer{
		name:   "execute_command",
		input:  "command: ls -la",
		output: "\t" + strings.Repeat("x", 100),
		status: ToolStatusPending,
	}

	lines, lineCount := tr.BuildInner(80, false, styles)
	result := joinVisualLines(lines)
	if lineCount != 5 {
		t.Errorf("lineCount = %d, want 5 (input + --- + preview + box rules)", lineCount)
	}
	if strings.Contains(result, "\t") {
		t.Error("preview should not contain raw tabs")
	}
	// The rendered first line must fit the full window width (80) —
	// open boxes have no side borders or padding; otherwise the terminal
	// soft-wraps it to a second line.
	plain := stripANSI(result)
	firstLine := plain
	if i := strings.IndexByte(firstLine, '\n'); i >= 0 {
		firstLine = firstLine[:i]
	}
	if w := cellWidth(firstLine); w > 80 {
		t.Errorf("first line width = %d, want <= 80 (inner width)", w)
	}
}

func TestToolRendererUfPreviewFlattensNewlines(t *testing.T) {
	// Defensive: Uf text with newlines (should not happen with the
	// current single-line producer) is still flattened to one line.
	styles := NewStyles(theme.DefaultTheme())
	tr := &toolRenderer{
		name:   "execute_command",
		input:  "command: ls -la",
		output: "line one\nline two",
		status: ToolStatusPending,
	}

	lines, lineCount := tr.BuildInner(80, false, styles)
	result := joinVisualLines(lines)
	if lineCount != 5 {
		t.Errorf("lineCount = %d, want 3 (flattened to single line)", lineCount)
	}
	if !strings.Contains(result, "line one line two") {
		t.Errorf("expected flattened content, got %q", result)
	}
}

func TestToolRendererUfPreviewFillsRemainingWidth(t *testing.T) {
	// Short input + longer Uf preview: the preview fills the room left
	// on the input line (not a fixed name-based budget) and stays single
	// line — like Af's "fill the window" behavior.
	styles := NewStyles(theme.DefaultTheme())
	tr := &toolRenderer{
		name:   "execute_command",
		input:  "ls",
		output: strings.Repeat("y", 60),
		status: ToolStatusPending,
	}

	lines, lineCount := tr.BuildInner(80, false, styles)
	result := joinVisualLines(lines)
	if lineCount != 5 {
		t.Errorf("lineCount = %d, want 5 (input + --- + preview + box rules)", lineCount)
	}
	if !strings.Contains(result, strings.Repeat("y", 60)) {
		t.Errorf("preview should fill remaining width without truncation, got %q", result)
	}
	if strings.Contains(result, "…") {
		t.Errorf("preview should not be truncated when it fits, got %q", result)
	}
}

func TestToolRendererUfPreviewBlockGlyphs(t *testing.T) {
	// Progress-bar block glyphs (█) render intact, single line, and are
	// counted as 1 display column each (no broken characters or wraps).
	styles := NewStyles(theme.DefaultTheme())
	bar := " 42% [████████░░░░░░░░] 3.2MB/s"
	tr := &toolRenderer{
		name:   "execute_command",
		input:  "wget",
		output: bar,
		status: ToolStatusPending,
	}

	lines, lineCount := tr.BuildInner(80, false, styles)
	result := joinVisualLines(lines)
	if lineCount != 5 {
		t.Errorf("lineCount = %d, want 3 (single line + border)", lineCount)
	}
	if !strings.Contains(result, "████████░░░░░░░░") {
		t.Errorf("block glyphs should render intact, got %q", result)
	}
	if strings.Contains(result, "\uFFFD") {
		t.Error("replacement character found — broken UTF-8")
	}
}
