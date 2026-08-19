package terminal

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// TestWindow_WithANSIContent verifies that windows properly handle content
// with ANSI escape sequences from any source (read_file, write_file, execute_command, etc.)
func TestWindow_WithANSIContent(t *testing.T) {
	styles := DefaultStyles()

	tests := []struct {
		name     string
		tag      string
		content  string
		expected string // Expected text after ANSI stripping (lipgloss ANSI is OK)
	}{
		{
			name:     "read_file result with ANSI",
			tag:      tlv.TagUserF,
			content:  "File content with \x1b[31mred text\x1b[0m",
			expected: "File content with red text",
		},
		{
			name:     "execute_command result with colors",
			tag:      tlv.TagUserF,
			content:  "Command output:\n\x1b[32mSuccess\x1b[0m\nDone",
			expected: "Command output:\nSuccess\nDone",
		},
		{
			name:     "write_file result with cursor codes",
			tag:      tlv.TagUserF,
			content:  "Writing\x1b[2K\rComplete",
			expected: "Writing\nComplete",
		},
		{
			name:     "tool call with ANSI in command",
			tag:      tlv.TagAssistantF,
			content:  "execute_command: echo \x1b[31mtest\x1b[0m",
			expected: "echo test", // status dot and tool name live in the header line
		},
		{
			name:     "text with embedded ANSI",
			tag:      tlv.TagAssistantT,
			content:  "Here is \x1b[1mbold\x1b[0m text",
			expected: "Here is bold text",
		},
		{
			name:     "reasoning with OSC sequence",
			tag:      tlv.TagAssistantR,
			content:  "Thinking\x1b]0;Title\x07...",
			expected: "Thinking...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := NewWindow("test-window", tt.tag, styles)
			if tt.tag == tlv.TagAssistantF {
				// Real tool windows carry a name — the renderer needs it to
				// strip the "name: " prefix from the input.
				w.SetRendererForTool("execute_command", "")
			}
			w.AppendContent(tt.content)

			// Render the window inner content (without border)
			lines, _ := w.renderer.BuildInner(80, false, styles)
			result := joinVisualLines(lines)

			// Strip lipgloss ANSI to check the actual text content
			resultStripped := stripANSI(result)

			if resultStripped != tt.expected {
				t.Errorf("Window render for tag %s:\n  got:  %q\n  want: %q",
					tt.tag, resultStripped, tt.expected)
			}
		})
	}
}

// TestWindow_PreservesLipglossColors verifies that lipgloss styling is preserved
func TestWindow_PreservesLipglossColors(t *testing.T) {
	styles := DefaultStyles()

	tests := []struct {
		name            string
		tag             string
		content         string
		shouldHaveColor bool // Should the rendered output contain ANSI codes?
	}{
		{
			name:            "tool call is plain text",
			tag:             tlv.TagAssistantF,
			content:         "execute_command: echo test",
			shouldHaveColor: false, // tool args render as plain text
		},
		{
			name:            "tool result is plain text",
			tag:             tlv.TagUserF,
			content:         "output text",
			shouldHaveColor: false, // tool output renders as plain text
		},
		{
			name:            "text assistant is plain text",
			tag:             tlv.TagAssistantT,
			content:         "Hello world",
			shouldHaveColor: false, // streaming content carries no styling (markdown tables are plain text too)
		},
		{
			name:            "reasoning is plain text",
			tag:             tlv.TagAssistantR,
			content:         "Thinking...",
			shouldHaveColor: false, // streaming content carries no styling
		},
		{
			name:            "system error is plain text",
			tag:             TagWindowSE,
			content:         "Error occurred",
			shouldHaveColor: false, // unfolded system windows render as plain text
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := NewWindow("test-window", tt.tag, styles)
			w.AppendContent(tt.content)

			// Render the window inner content (without border)
			lines, _ := w.renderer.BuildInner(80, false, styles)
			result := joinVisualLines(lines)

			// Check if result contains ANSI codes
			hasColor := containsANSI(result)

			if tt.shouldHaveColor && !hasColor {
				t.Errorf("Expected styled output with ANSI codes, but got none: %q", result)
			}
			if !tt.shouldHaveColor && hasColor {
				t.Errorf("Expected no ANSI codes, but got: %q", result)
			}
		})
	}
}

// containsANSI checks if a string contains any ANSI escape sequences
func containsANSI(s string) bool {
	return strings.Contains(s, "\x1b[")
}

// TestWindow_DiffContentWithANSI verifies that edit_file windows handle ANSI
func TestWindow_DiffContentWithANSI(t *testing.T) {
	// Use actual escape characters (not literal \x1b)
	oldLine := "\x1b[31mold line\x1b[0m"
	newLine := "\x1b[32mnew line\x1b[0m"
	content := "edit_file: /tmp/test.txt\n- " + oldLine + "\n+ " + newLine + "\n  unchanged"

	styles := DefaultStyles()
	result := RenderDiffContent(content, "edit_file", styles)

	// Strip the output's ANSI (input ANSI is stripped by prepareContent;
	// the rendered rows carry their own diff colors) to check the text.
	resultStripped := stripANSI(result)

	// Should contain the text without the embedded ANSI from input; the
	// first line shows the bare argument (no status dot, no tool-name
	// prefix). Context rows stay plain.
	expected := "/tmp/test.txt\n- old line\n+ new line\n  unchanged"

	if resultStripped != expected {
		t.Errorf("DiffContent:\n  got:  %q\n  want: %q", resultStripped, expected)
	}
	// Removed rows are red, added rows are green — the diff keeps colors.
	if !strings.Contains(result, "\x1b[") {
		t.Errorf("diff rows should carry colors, got %q", result)
	}
	if !strings.Contains(result, styles.DiffRemove.Render("- old line")) {
		t.Errorf("removed row should carry the DiffRemove color, got %q", result)
	}
	if !strings.Contains(result, styles.DiffAdd.Render("+ new line")) {
		t.Errorf("added row should carry the DiffAdd color, got %q", result)
	}
	// Context rows stay plain (no SGR wrapping around them).
	if !strings.Contains(result, "\n  unchanged") {
		t.Errorf("context row should be plain text, got %q", result)
	}
}

// TestWindow_WriteFileContentStaysPlain: write_file's input is the RAW
// file content being written — not a diff. Lines starting with "- " or
// "+ " (markdown lists, script args, …) are literal content and must NOT
// be colored as diff rows. Only edit_file's model-generated diff carries
// the -/+ colors.
func TestWindow_WriteFileContentStaysPlain(t *testing.T) {
	styles := DefaultStyles()
	wb := NewWindowBuffer(80, styles)
	content := "write_file: /tmp/notes.md\n- item one\n+ item two\n  plain line"
	wb.HandleToolInputEvent(protocol.ToolInputData{ID: "t1", Name: "write_file", Input: json.RawMessage(content)}, 0)
	wb.ToggleFold(0)

	rendered := wb.GetAll(-1, false)
	if strings.Contains(rendered, styles.DiffRemove.Render("- item one")) {
		t.Errorf("write_file '- ' row must not carry the diff color: %q", rendered)
	}
	if strings.Contains(rendered, styles.DiffAdd.Render("+ item two")) {
		t.Errorf("write_file '+ ' row must not carry the diff color: %q", rendered)
	}
	plain := stripANSI(rendered)
	if !strings.Contains(plain, "- item one") || !strings.Contains(plain, "+ item two") {
		t.Errorf("write_file content should be visible: %q", plain)
	}
}
