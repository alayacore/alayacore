package tools

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

func TestRGAvailable(t *testing.T) {
	// This just tests that RGAvailable doesn't panic
	_ = RGAvailable()
}

func TestSearchContentBasicSearch(t *testing.T) {
	if !RGAvailable() {
		t.Skip("rg not available on system")
	}

	// Create a temp directory with some test files
	tmpDir, err := os.MkdirTemp("", "alayacore-rg-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Write a test file
	testFile := filepath.Join(tmpDir, "test.txt")
	content := "hello world\nfoo bar\nhello again\n"
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Search for "hello"
	result, err := executeSearchContent(context.Background(), SearchContentInput{
		Pattern: "hello",
		Path:    tmpDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text, ok := result.(llm.ToolResultOutputText)
	if !ok {
		t.Fatalf("expected text output, got %T", result)
	}
	if text.Text == "" {
		t.Error("expected non-empty output")
	}
}

func TestSearchContentNoMatches(t *testing.T) {
	if !RGAvailable() {
		t.Skip("rg not available on system")
	}

	tmpDir, err := os.MkdirTemp("", "alayacore-rg-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("hello world\n"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := executeSearchContent(context.Background(), SearchContentInput{
		Pattern: "nonexistent_pattern_xyz",
		Path:    tmpDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text, ok := result.(llm.ToolResultOutputText)
	if !ok {
		t.Fatalf("expected text output, got %T", result)
	}
	if text.Text != "No matches found" {
		t.Errorf("expected 'No matches found', got %q", text.Text)
	}
}

func TestSearchContentEmptyPattern(t *testing.T) {
	result, err := executeSearchContent(context.Background(), SearchContentInput{
		Pattern: "",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	errOut, ok := result.(llm.ToolResultOutputError)
	if !ok {
		t.Fatalf("expected error output, got %T", result)
	}
	if errOut.Error != "pattern is required" {
		t.Errorf("expected 'pattern is required', got %q", errOut.Error)
	}
}

func TestSearchContentFileTypeFilter(t *testing.T) {
	if !RGAvailable() {
		t.Skip("rg not available on system")
	}

	tmpDir, err := os.MkdirTemp("", "alayacore-rg-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Write a Go file
	goFile := filepath.Join(tmpDir, "test.go")
	if err := os.WriteFile(goFile, []byte("package main\nfunc test() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Write a text file that also contains "func"
	txtFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(txtFile, []byte("func should not match\n"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := executeSearchContent(context.Background(), SearchContentInput{
		Pattern:  "func",
		Path:     tmpDir,
		FileType: "go",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text, ok := result.(llm.ToolResultOutputText)
	if !ok {
		t.Fatalf("expected text output, got %T", result)
	}

	// Should only contain the Go file match
	if text.Text == "" {
		t.Error("expected non-empty output")
	}
}

func TestSearchContentIgnoreCase(t *testing.T) {
	if !RGAvailable() {
		t.Skip("rg not available on system")
	}

	tmpDir, err := os.MkdirTemp("", "alayacore-rg-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("Hello World\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Without ignore_case, lowercase "hello" should not match "Hello"
	result, err := executeSearchContent(context.Background(), SearchContentInput{
		Pattern: "hello",
		Path:    tmpDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text, ok := result.(llm.ToolResultOutputText)
	if !ok {
		t.Fatalf("expected text output, got %T", result)
	}
	if text.Text != "No matches found" {
		t.Errorf("expected 'No matches found' for case-sensitive search, got %q", text.Text)
	}

	// With ignore_case, lowercase "hello" should match "Hello"
	result, err = executeSearchContent(context.Background(), SearchContentInput{
		Pattern:    "hello",
		Path:       tmpDir,
		IgnoreCase: "true",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text, ok = result.(llm.ToolResultOutputText)
	if !ok {
		t.Fatalf("expected text output, got %T", result)
	}
	if text.Text == "" || text.Text == "No matches found" {
		t.Errorf("expected match with ignore_case=true, got %q", text.Text)
	}
}

func TestTruncateLines(t *testing.T) {
	input := "line1\nline2\nline3\nline4\nline5\n"
	truncated, output := truncateLines(input, 3)
	if !truncated {
		t.Error("expected truncation")
	}
	if output != "line1\nline2\nline3" {
		t.Errorf("unexpected output: %q", output)
	}

	// No truncation needed
	truncated, output = truncateLines(input, 10)
	if truncated {
		t.Error("expected no truncation")
	}
	if output != "line1\nline2\nline3\nline4\nline5" {
		t.Errorf("unexpected output: %q", output)
	}
}

func TestSearchContentMaxLinesGlobal(t *testing.T) {
	if !RGAvailable() {
		t.Skip("rg not available on system")
	}

	tmpDir, err := os.MkdirTemp("", "alayacore-rg-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create 5 files, each with 20 matching lines
	for f := 0; f < 5; f++ {
		var content string
		for i := 0; i < 20; i++ {
			content += "match line\n"
		}
		if err := os.WriteFile(filepath.Join(tmpDir, "file"+strconv.Itoa(f)+".txt"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// With MaxLines=5, output should be capped at 5 lines globally
	result, err := executeSearchContent(context.Background(), SearchContentInput{
		Pattern:  "match",
		Path:     tmpDir,
		MaxLines: "5",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text, ok := result.(llm.ToolResultOutputText)
	if !ok {
		t.Fatalf("expected text output, got %T", result)
	}

	lineCount := strings.Count(text.Text, "\n") + 1
	if strings.HasSuffix(text.Text, "... (output truncated)") {
		lineCount-- // don't count the truncation indicator
	}
	if lineCount > 5 {
		t.Errorf("expected at most 5 lines, got %d\nOutput:\n%s", lineCount, text.Text)
	}
	if !strings.Contains(text.Text, "... (output truncated)") {
		t.Errorf("expected truncation indicator in output:\n%s", text.Text)
	}
}
