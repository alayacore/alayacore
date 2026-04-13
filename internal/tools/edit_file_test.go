package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

func TestEditFileTool(t *testing.T) {
	// Create a temp directory for test files
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")

	tests := []struct {
		name        string
		initial     string
		oldString   string
		newString   string
		expected    string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "empty path",
			initial:     "",
			oldString:   "",
			newString:   "",
			expected:    "",
			expectError: true,
			errorMsg:    "path is required",
		},
		{
			name:        "empty old_string",
			initial:     "line 1\nline 2\nline 3",
			oldString:   "",
			newString:   "new",
			expected:    "",
			expectError: true,
			errorMsg:    "old_string is required",
		},
		{
			name:      "single line replacement",
			initial:   "line 1\nline 2\nline 3",
			oldString: "line 2",
			newString: "line 2 modified",
			expected:  "line 1\nline 2 modified\nline 3",
		},
		{
			name:      "multiline replacement",
			initial:   "line 1\nline 2\nline 3\nline 4",
			oldString: "line 2\nline 3",
			newString: "new line 2\nnew line 3",
			expected:  "line 1\nnew line 2\nnew line 3\nline 4",
		},
		{
			name:      "add content",
			initial:   "line 1\nline 3",
			oldString: "line 1\nline 3",
			newString: "line 1\nline 2\nline 3",
			expected:  "line 1\nline 2\nline 3",
		},
		{
			name:      "remove content",
			initial:   "line 1\nline 2\nline 3",
			oldString: "\nline 2",
			newString: "",
			expected:  "line 1\nline 3",
		},
		{
			name:      "replace with empty",
			initial:   "line 1\nline 2\nline 3",
			oldString: "\nline 2",
			newString: "",
			expected:  "line 1\nline 3",
		},
		{
			name:      "replace entire file",
			initial:   "old content",
			oldString: "old content",
			newString: "new content",
			expected:  "new content",
		},
		{
			name:        "identical old_string and new_string",
			initial:     "line 1\nline 2\nline 3",
			oldString:   "line 2",
			newString:   "line 2",
			expected:    "",
			expectError: true,
			errorMsg:    "old_string and new_string are identical",
		},
		{
			name:      "replace with indentation",
			initial:   "func main() {\n    fmt.Println(\"hello\")\n}",
			oldString: "    fmt.Println(\"hello\")",
			newString: "    fmt.Println(\"goodbye\")",
			expected:  "func main() {\n    fmt.Println(\"goodbye\")\n}",
		},
		{
			name:        "old_string not found",
			initial:     "line 1\nline 2\nline 3",
			oldString:   "nonexistent",
			newString:   "new",
			expected:    "",
			expectError: true,
			errorMsg:    "old_string not found in file",
		},
		{
			name:        "old_string appears multiple times",
			initial:     "line\nline\nline",
			oldString:   "line",
			newString:   "new",
			expected:    "",
			expectError: true,
			errorMsg:    "old_string found multiple times",
		},
		{
			name:        "file not found",
			initial:     "",
			oldString:   "something",
			newString:   "new",
			expected:    "",
			expectError: true,
			errorMsg:    "file not found",
		},
		{
			name:      "unique context for multiple occurrences",
			initial:   "first\nline\nmiddle\nline\nlast",
			oldString: "middle\nline",
			newString: "middle\nnew line",
			expected:  "first\nline\nmiddle\nnew line\nlast",
		},
		{
			name:      "preserve tabs",
			initial:   "\tif true {\n\t\treturn\n\t}",
			oldString: "\t\treturn",
			newString: "\t\treturn nil",
			expected:  "\tif true {\n\t\treturn nil\n\t}",
		},
		{
			name:        "empty file to content",
			initial:     "",
			oldString:   "",
			newString:   "new content",
			expected:    "",
			expectError: true,
			errorMsg:    "old_string is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup initial file
			if tt.initial != "" {
				if err := os.WriteFile(testFile, []byte(tt.initial), 0644); err != nil {
					t.Fatalf("failed to write initial file: %v", err)
				}
			} else {
				// Remove file if it exists from previous test
				os.Remove(testFile)
			}

			// Create tool input
			input := EditFileInput{
				Path:      testFile,
				OldString: tt.oldString,
				NewString: tt.newString,
			}
			if tt.name == "empty path" {
				input.Path = ""
			}

			// Run tool
			tool := NewEditFileTool()
			inputJSON, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := tool.Execute(context.Background(), inputJSON)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.expectError {
				// Check for error response
				var errMsg string
				switch r := resp.(type) {
				case llm.ToolResultOutputError:
					errMsg = r.Error
				default:
					t.Errorf("expected error response, got %T", resp)
					return
				}
				if !strings.Contains(errMsg, tt.errorMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errorMsg, errMsg)
				}
				return
			}

			// Check result
			textResp, ok := resp.(llm.ToolResultOutputText)
			if !ok {
				t.Errorf("expected text response, got %T", resp)
			}
			_ = textResp // We don't check the success message content

			// Verify file content
			content, err := os.ReadFile(testFile)
			if err != nil {
				t.Fatalf("failed to read result file: %v", err)
			}

			result := string(content)
			if result != tt.expected {
				t.Errorf("expected:\n%q\n\ngot:\n%q", tt.expected, result)
			}
		})
	}
}
