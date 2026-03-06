package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/fantasy"
)

func TestEditFileTool(t *testing.T) {
	// Create a temp directory for test files
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")

	tests := []struct {
		name        string
		initial     string
		diff        string
		expected    string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "empty path",
			initial:     "",
			diff:        "",
			expected:    "",
			expectError: true,
			errorMsg:    "path is required",
		},
		{
			name:        "empty diff",
			initial:     "line 1\nline 2\nline 3",
			diff:        "",
			expected:    "",
			expectError: true,
			errorMsg:    "diff is required",
		},
		{
			name:     "single line change",
			initial:  "line 1\nline 2\nline 3",
			diff:     "--- a/test.txt\n+++ b/test.txt\n@@ -1,3 +1,3 @@\n line 1\n-line 2\n+line 2 modified\n line 3",
			expected: "line 1\nline 2 modified\nline 3",
		},
		{
			name:     "add line",
			initial:  "line 1\nline 3",
			diff:     "--- a/test.txt\n+++ b/test.txt\n@@ -1,2 +1,3 @@\n line 1\n+line 2\n line 3",
			expected: "line 1\nline 2\nline 3",
		},
		{
			name:     "remove line",
			initial:  "line 1\nline 2\nline 3",
			diff:     "--- a/test.txt\n+++ b/test.txt\n@@ -1,3 +1,2 @@\n line 1\n-line 2\n line 3",
			expected: "line 1\nline 3",
		},
		{
			name:     "multiple hunks",
			initial:  "line 1\nline 2\nline 3\nline 4\nline 5",
			diff:     "--- a/test.txt\n+++ b/test.txt\n@@ -1,3 +1,3 @@\n line 1\n-line 2\n+line 2 modified\n line 3\n@@ -3,3 +3,3 @@\n line 3\n-line 4\n+line 4 modified\n line 5",
			expected: "line 1\nline 2 modified\nline 3\nline 4 modified\nline 5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup initial file
			if tt.initial != "" {
				if err := os.WriteFile(testFile, []byte(tt.initial), 0644); err != nil {
					t.Fatalf("failed to write initial file: %v", err)
				}
			}

			// Create tool input
			var inputPath string
			if tt.initial != "" {
				inputPath = testFile
			}
			input := EditFileInput{
				Path: inputPath,
				Diff: tt.diff,
			}

			// Run tool
			tool := NewEditFileTool()
			resp, err := tool.Run(context.Background(), fantasy.ToolCall{
				ID:    "test",
				Name:  "edit_file",
				Input: toJSON(input),
			})

			if tt.expectError {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
				if !strings.Contains(resp.Content, tt.errorMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errorMsg, resp.Content)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Check result
			if resp.Type != "text" {
				t.Errorf("expected text response, got %s", resp.Type)
			}

			// Verify file content
			content, err := os.ReadFile(testFile)
			if err != nil {
				t.Fatalf("failed to read result file: %v", err)
			}

			result := string(content)
			if result != tt.expected {
				t.Errorf("expected:\n%s\n\ngot:\n%s", tt.expected, result)
			}
		})
	}
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
