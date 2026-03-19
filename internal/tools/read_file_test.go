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

func TestReadFileFull(t *testing.T) {
	// Create a temp file
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	content := "line1\nline2\nline3\n"
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool()
	input := ReadFileInput{Path: tmpFile}
	inputJSON, _ := json.Marshal(input)
	result, err := tool.Execute(context.Background(), inputJSON)
	if err != nil {
		t.Fatal(err)
	}

	textResp, ok := result.(llm.ToolResultOutputText)
	if !ok {
		t.Errorf("expected text response, got %T", result)
	}
	if textResp.Text != content {
		t.Errorf("expected %q, got %q", content, textResp.Text)
	}
}

func TestReadFileWithLineRange(t *testing.T) {
	// Create a temp file with many lines
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	var content string
	for i := 1; i <= 100; i++ {
		content += "line" + itoa(i) + "\n"
	}
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool()

	tests := []struct {
		name      string
		input     ReadFileInput
		wantLines []string
		wantError bool
		errorMsg  string
	}{
		{
			name:      "read lines 5-10",
			input:     ReadFileInput{Path: tmpFile, StartLine: "5", EndLine: "10"},
			wantLines: []string{"line5", "line6", "line7", "line8", "line9", "line10"},
		},
		{
			name:      "read first line",
			input:     ReadFileInput{Path: tmpFile, StartLine: "1", EndLine: "1"},
			wantLines: []string{"line1"},
		},
		{
			name:      "read last line",
			input:     ReadFileInput{Path: tmpFile, StartLine: "100", EndLine: "100"},
			wantLines: []string{"line100"},
		},
		{
			name:      "read from line to end",
			input:     ReadFileInput{Path: tmpFile, StartLine: "98"},
			wantLines: []string{"line98", "line99", "line100"},
		},
		{
			name:      "read from start to line",
			input:     ReadFileInput{Path: tmpFile, EndLine: "3"},
			wantLines: []string{"line1", "line2", "line3"},
		},
		{
			name:      "invalid start_line",
			input:     ReadFileInput{Path: tmpFile, StartLine: "0"},
			wantError: true,
			errorMsg:  "start_line must be >= 1",
		},
		{
			name:      "invalid end_line",
			input:     ReadFileInput{Path: tmpFile, EndLine: "-1"},
			wantError: true,
			errorMsg:  "end_line must be >= 1",
		},
		{
			name:      "start > end",
			input:     ReadFileInput{Path: tmpFile, StartLine: "10", EndLine: "5"},
			wantError: true,
			errorMsg:  "start_line must be <= end_line",
		},
		{
			name:      "non-numeric start_line",
			input:     ReadFileInput{Path: tmpFile, StartLine: "abc"},
			wantError: true,
			errorMsg:  "invalid start_line",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputJSON, _ := json.Marshal(tt.input)
			result, err := tool.Execute(context.Background(), inputJSON)
			if err != nil {
				t.Fatal(err)
			}

			if tt.wantError {
				errResp, ok := result.(llm.ToolResultOutputError)
				if !ok {
					t.Errorf("expected error response, got %T", result)
					return
				}
				if !strings.Contains(errResp.Error, tt.errorMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errorMsg, errResp.Error)
				}
				return
			}

			textResp, ok := result.(llm.ToolResultOutputText)
			if !ok {
				t.Errorf("expected text response, got %T", result)
				return
			}

			expected := strings.Join(tt.wantLines, "\n")
			if textResp.Text != expected {
				t.Errorf("expected %q, got %q", expected, textResp.Text)
			}
		})
	}
}

func TestReadFileTooLarge(t *testing.T) {
	// Create a temp file larger than maxFullReadSize
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "large.txt")
	largeContent := make([]byte, maxFullReadSize+1)
	for i := range largeContent {
		largeContent[i] = 'x'
	}
	if err := os.WriteFile(tmpFile, largeContent, 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool()
	input := ReadFileInput{Path: tmpFile}
	inputJSON, _ := json.Marshal(input)
	result, err := tool.Execute(context.Background(), inputJSON)
	if err != nil {
		t.Fatal(err)
	}

	errResp, ok := result.(llm.ToolResultOutputError)
	if !ok {
		t.Errorf("expected error response for large file, got %T", result)
	}
	if !strings.Contains(errResp.Error, "too large") {
		t.Errorf("expected 'too large' error, got %q", errResp.Error)
	}
}

func TestReadFileLargeWithLineRange(t *testing.T) {
	// Create a temp file larger than maxFullReadSize
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "large.txt")

	// Create lines: first line small, then many lines to make file large, then more
	f, err := os.Create(tmpFile)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("first line\n")
	// Write enough data to exceed maxFullReadSize, but with reasonable line lengths
	for i := 0; i < 100000; i++ {
		f.WriteString("x")
	}
	f.WriteString("\nthird line\n")
	f.Close()

	tool := NewReadFileTool()

	// Should be able to read first and last lines without loading entire file
	input := ReadFileInput{Path: tmpFile, StartLine: "1", EndLine: "1"}
	inputJSON, _ := json.Marshal(input)
	result, err := tool.Execute(context.Background(), inputJSON)
	if err != nil {
		t.Fatal(err)
	}

	textResp, ok := result.(llm.ToolResultOutputText)
	if !ok {
		errResp, _ := result.(llm.ToolResultOutputError)
		t.Errorf("expected text response, got error: %q", errResp.Error)
		return
	}
	if textResp.Text != "first line" {
		t.Errorf("expected 'first line', got %q", textResp.Text)
	}

	// Also test reading the third line
	input = ReadFileInput{Path: tmpFile, StartLine: "3", EndLine: "3"}
	inputJSON, _ = json.Marshal(input)
	result, err = tool.Execute(context.Background(), inputJSON)
	if err != nil {
		t.Fatal(err)
	}

	textResp, ok = result.(llm.ToolResultOutputText)
	if !ok {
		t.Errorf("expected text response, got %T", result)
	}
	if textResp.Text != "third line" {
		t.Errorf("expected 'third line', got %q", textResp.Text)
	}
}

func TestReadFileNotFound(t *testing.T) {
	tool := NewReadFileTool()
	input := ReadFileInput{Path: "/nonexistent/file.txt"}
	inputJSON, _ := json.Marshal(input)
	result, err := tool.Execute(context.Background(), inputJSON)
	if err != nil {
		t.Fatal(err)
	}

	_, ok := result.(llm.ToolResultOutputError)
	if !ok {
		t.Errorf("expected error response, got %T", result)
	}
}

func TestReadFileBinary(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name        string
		content     []byte
		expectError bool
	}{
		{
			name:        "text file",
			content:     []byte("Hello, world!\nThis is text.\n"),
			expectError: false,
		},
		{
			name:        "binary with null bytes",
			content:     []byte{0x00, 0x01, 0x02, 0x03, 'H', 'e', 'l', 'l', 'o'},
			expectError: true,
		},
		{
			name:        "PNG header",
			content:     []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00},
			expectError: true,
		},
		{
			name:        "UTF-8 text with special chars",
			content:     []byte("Hello 世界\nПривет мир\n🎉\n"),
			expectError: false,
		},
		{
			name:        "empty file",
			content:     []byte{},
			expectError: false,
		},
		{
			name:        "code file",
			content:     []byte("package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"),
			expectError: false,
		},
	}

	tool := NewReadFileTool()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpFile := filepath.Join(tmpDir, tt.name+".bin")
			if err := os.WriteFile(tmpFile, tt.content, 0644); err != nil {
				t.Fatal(err)
			}

			input := ReadFileInput{Path: tmpFile}
			inputJSON, _ := json.Marshal(input)
			result, err := tool.Execute(context.Background(), inputJSON)
			if err != nil {
				t.Fatal(err)
			}

			if tt.expectError {
				errResp, ok := result.(llm.ToolResultOutputError)
				if !ok {
					t.Errorf("expected error response for binary file, got %T", result)
					return
				}
				if !strings.Contains(errResp.Error, "binary") {
					t.Errorf("expected binary error message, got %q", errResp.Error)
				}
			} else {
				textResp, ok := result.(llm.ToolResultOutputText)
				if !ok {
					errResp, _ := result.(llm.ToolResultOutputError)
					t.Errorf("expected text response for text file, got error: %q", errResp.Error)
					return
				}
				// For empty file, just check we got empty content
				if len(tt.content) == 0 && textResp.Text != "" {
					t.Errorf("expected empty content for empty file, got %q", textResp.Text)
				}
			}
		})
	}
}

// Helper function to convert int to string without strconv
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var neg bool
	if i < 0 {
		neg = true
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
