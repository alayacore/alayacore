package terminal

import (
	"strings"
	"testing"

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
			shouldHaveColor: false, // streaming content carries no styling (markdown is a future concern)
		},
		{
			name:            "reasoning is plain text",
			tag:             tlv.TagAssistantR,
			content:         "Thinking...",
			shouldHaveColor: false, // streaming content carries no styling
		},
		{
			name:            "system error gets styled",
			tag:             TagWindowSE,
			content:         "Error occurred",
			shouldHaveColor: true,
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

	result := RenderDiffContent(content, "edit_file")

	// Strip the output's ANSI (input ANSI is stripped by prepareContent;
	// the rendered content is plain) to check the actual text.
	resultStripped := stripANSI(result)

	// Should contain the text without the embedded ANSI from input; the
	// first line shows the bare argument (no status dot, no tool-name
	// prefix). No diff colors are applied — content is plain.
	expected := "/tmp/test.txt\n- old line\n+ new line\n  unchanged"

	if resultStripped != expected {
		t.Errorf("DiffContent:\n  got:  %q\n  want: %q", resultStripped, expected)
	}
	if strings.Contains(result, "\x1b[") {
		t.Errorf("DiffContent must be plain text (no colors), got %q", result)
	}
}
