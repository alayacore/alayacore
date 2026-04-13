package terminal

import (
	"strings"
	"testing"
)

func TestStripANSI_ColorCodes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "red text",
			input:    "\x1b[31mRed Text\x1b[0m",
			expected: "Red Text",
		},
		{
			name:     "green text",
			input:    "\x1b[32mGreen\x1b[0m",
			expected: "Green",
		},
		{
			name:     "bold and colored",
			input:    "\x1b[1;34mBold Blue\x1b[0m",
			expected: "Bold Blue",
		},
		{
			name:     "no escape codes",
			input:    "Plain text",
			expected: "Plain text",
		},
		{
			name:     "mixed content",
			input:    "Start \x1b[31mred\x1b[0m middle \x1b[32mgreen\x1b[0m end",
			expected: "Start red middle green end",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripANSI(tt.input)
			if result != tt.expected {
				t.Errorf("stripANSI(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestStripANSI_CursorMovement(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "cursor up",
			input:    "Text\x1b[1A",
			expected: "Text",
		},
		{
			name:     "clear line",
			input:    "Text\x1b[2K",
			expected: "Text",
		},
		{
			name:     "cursor position",
			input:    "\x1b[10;20HPosition",
			expected: "Position",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripANSI(tt.input)
			if result != tt.expected {
				t.Errorf("stripANSI(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestStripANSI_OSCSequences(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "OSC with BEL",
			input:    "Text\x1b]0;Title\x07More",
			expected: "TextMore",
		},
		{
			name:     "OSC with ST",
			input:    "Text\x1b]0;Title\x1b\\More",
			expected: "TextMore",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripANSI(tt.input)
			if result != tt.expected {
				t.Errorf("stripANSI(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestStripANSI_CarriageReturns(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "single CR",
			input:    "Progress: 50%\rProgress: 100%",
			expected: "Progress: 50%\nProgress: 100%",
		},
		{
			name:     "multiple CRs",
			input:    "A\rB\rC",
			expected: "A\nB\nC",
		},
		{
			name:     "CR with ANSI",
			input:    "\x1b[31mRed\x1b[0m\r\x1b[32mGreen\x1b[0m",
			expected: "Red\nGreen",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripANSI(tt.input)
			if result != tt.expected {
				t.Errorf("stripANSI(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestPrepareContent_WithANSI(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "tabs and ANSI",
			input:    "\x1b[31m\tRed\x1b[0m",
			expected: "        Red",
		},
		{
			name:     "ANSI stripped first",
			input:    "\x1b[1;32m\tText\x1b[0m",
			expected: "        Text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := prepareContent(tt.input)
			if result != tt.expected {
				t.Errorf("prepareContent(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestColorizeTool_WithANSI(t *testing.T) {
	styles := DefaultStyles()

	tests := []struct {
		name  string
		input string
		// Just verify no crash and contains expected text
		contains string
	}{
		{
			name:     "tool output with ANSI colors",
			input:    "shell: \x1b[31merror\x1b[0m",
			contains: "error",
		},
		{
			name:     "multiline with ANSI",
			input:    "read_file: test.txt\n\x1b[32mcontent\x1b[0m",
			contains: "content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Prepare content first (as renderGenericContent does)
			prepared := prepareContent(tt.input)
			result := ColorizeTool(prepared, styles)
			// Check that the result contains the expected text
			// Result will have lipgloss ANSI styling, which is intentional
			// Strip all ANSI from result to check plain text
			stripped := stripANSI(result)
			if !strings.Contains(stripped, tt.contains) {
				t.Errorf("ColorizeTool(%q) = %q, should contain %q", tt.input, result, tt.contains)
			}
		})
	}
}
