package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// rgAvailable checks whether ripgrep is on the system, for test skipping.
func rgAvailable() bool {
	_, err := exec.LookPath("rg")
	return err == nil
}

func TestSearchContentBasicSearch(t *testing.T) {
	if !rgAvailable() {
		t.Skip("rg not available on system")
	}

	tmpDir := t.TempDir()

	testFile := filepath.Join(tmpDir, "test.txt")
	content := "hello world\nfoo bar\nhello again\n"
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := executeSearchContent(context.Background(), SearchContentInput{
		Pattern: "hello",
		Path:    tmpDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractFirstText(result)
	if text == "" {
		t.Error("expected non-empty output")
	}
}

func TestSearchContentNoMatches(t *testing.T) {
	if !rgAvailable() {
		t.Skip("rg not available on system")
	}

	tmpDir := t.TempDir()

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
	text := extractFirstText(result)
	if text != "No matches found" {
		t.Errorf("expected 'No matches found', got %q", text)
	}
}

func TestSearchContentEmptyPattern(t *testing.T) {
	_, err := executeSearchContent(context.Background(), SearchContentInput{
		Pattern: "",
	})
	if err == nil {
		t.Fatal("expected error for empty pattern")
	}
	if err.Error() != "pattern is required" {
		t.Errorf("expected 'pattern is required', got %q", err.Error())
	}
}

func TestSearchContentFileTypeFilter(t *testing.T) {
	if !rgAvailable() {
		t.Skip("rg not available on system")
	}

	tmpDir := t.TempDir()

	goFile := filepath.Join(tmpDir, "test.go")
	if err := os.WriteFile(goFile, []byte("package main\nfunc test() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

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
	text := extractFirstText(result)
	if text == "" {
		t.Error("expected non-empty output")
	}
}

func TestSearchContentIgnoreCase(t *testing.T) {
	if !rgAvailable() {
		t.Skip("rg not available on system")
	}

	tmpDir := t.TempDir()

	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("Hello World\n"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := executeSearchContent(context.Background(), SearchContentInput{
		Pattern: "hello",
		Path:    tmpDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractFirstText(result)
	if text != "No matches found" {
		t.Errorf("expected 'No matches found' for case-sensitive search, got %q", text)
	}

	result, err = executeSearchContent(context.Background(), SearchContentInput{
		Pattern:    "hello",
		Path:       tmpDir,
		IgnoreCase: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text = extractFirstText(result)
	if text == "" || text == "No matches found" {
		t.Errorf("expected match with ignore_case=true, got %q", text)
	}
}

func TestSearchContentMaxLinesGlobal(t *testing.T) {
	if !rgAvailable() {
		t.Skip("rg not available on system")
	}

	tmpDir := t.TempDir()

	for f := 0; f < 5; f++ {
		var content string
		for i := 0; i < 20; i++ {
			content += "match line\n"
		}
		if err := os.WriteFile(filepath.Join(tmpDir, "file"+strconv.Itoa(f)+".txt"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	result, err := executeSearchContent(context.Background(), SearchContentInput{
		Pattern:  "match",
		Path:     tmpDir,
		MaxLines: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractFirstText(result)
	if !strings.Contains(text, "matching lines") {
		t.Errorf("expected 'matching lines' in output, got:\n%s", text)
	}
	if !strings.Contains(text, "Results saved to:") {
		t.Errorf("expected 'Results saved to:' in output, got:\n%s", text)
	}
}

func TestFormatSearchResultNoLineLimit(t *testing.T) {
	// max_lines of 0 (the default when omitted) disables the line cap:
	// 200 lines (well under the 64KB byte cap) must be returned inline
	// instead of being saved to a temp file.
	output := strings.Repeat("match line\n", 200)
	parts, err := formatSearchResult(searchResult{
		stdout:   output,
		stderr:   "",
		exitCode: 0,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	text := extractFirstText(parts)
	if text != output {
		t.Errorf("expected full %d-line output inline, got %d bytes", 200, len(text))
	}

	// A positive max_lines caps inline results: 200 lines with a limit of
	// 50 must be saved to a temp file.
	parts, err = formatSearchResult(searchResult{
		stdout:   output,
		stderr:   "",
		exitCode: 0,
	}, 50)
	if err != nil {
		t.Fatal(err)
	}
	text = extractFirstText(parts)
	if !strings.Contains(text, "Results saved to:") {
		t.Errorf("expected temp-file reference message with max_lines=50, got:\n%s", text)
	}

	// Even with max_lines = 0 the 64KB byte cap still applies.
	hugeLine := strings.Repeat("x", maxSearchContentSize+1)
	parts, err = formatSearchResult(searchResult{
		stdout:   hugeLine,
		stderr:   "",
		exitCode: 0,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	text = extractFirstText(parts)
	if strings.Contains(text, "xxxxxxxxxx") {
		t.Errorf("oversized single-line output was returned inline: %.60q…", text)
	}
	if !strings.Contains(text, "Results saved to:") {
		t.Errorf("expected temp-file reference message for oversized output, got:\n%s", text)
	}
}

func TestFormatSearchResultByteCap(t *testing.T) {
	// A single matching line can be arbitrarily large (minified JS,
	// base64 blobs) — the line-count cap alone would return it inline and
	// blow the context window. Output above maxSearchContentSize must be
	// saved to a temp file with a reference message instead.
	hugeLine := strings.Repeat("x", maxSearchContentSize+1)
	parts, err := formatSearchResult(searchResult{
		stdout:   hugeLine,
		stderr:   "",
		exitCode: 0,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	text := extractFirstText(parts)
	if strings.Contains(text, "xxxxxxxxxx") {
		t.Errorf("oversized single-line output was returned inline: %.60q…", text)
	}
	if !strings.Contains(text, "Results saved to:") {
		t.Errorf("expected temp-file reference message, got:\n%s", text)
	}

	// At exactly the cap the output is still returned inline.
	atCap := strings.Repeat("y", maxSearchContentSize)
	parts, err = formatSearchResult(searchResult{
		stdout:   atCap,
		stderr:   "",
		exitCode: 0,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractFirstText(parts); len(text) != maxSearchContentSize {
		t.Errorf("output at the cap should be inline, got %d bytes", len(text))
	}
}

func TestSearchContentPatternLooksLikeFlag(t *testing.T) {
	if !rgAvailable() {
		t.Skip("rg not available on system")
	}

	tmpDir := t.TempDir()

	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("--skill\n--help\nnormal text\n"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := executeSearchContent(context.Background(), SearchContentInput{
		Pattern: "--skill",
		Path:    tmpDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractFirstText(result)
	if text == "" || text == "No matches found" {
		t.Errorf("expected match for '--skill' pattern, got %q", text)
	}
	if !strings.Contains(text, "--skill") {
		t.Errorf("expected output to contain '--skill', got %q", text)
	}
}

func TestSearchContentStreaming(t *testing.T) {
	if !rgAvailable() {
		t.Skip("rg not available on system")
	}

	tmpDir := t.TempDir()

	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("hello world\nfoo bar\nhello again\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var previews []string
	result, err := executeSearchContentStreaming(context.Background(), SearchContentInput{
		Pattern: "hello",
		Path:    tmpDir,
	}, func(text string) {
		previews = append(previews, text)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractFirstText(result)
	if text == "" {
		t.Error("expected non-empty output")
	}
	// flushPreview emits the final snapshot after completion, so a search
	// with output must deliver at least one preview frame.
	if len(previews) == 0 {
		t.Error("expected at least one preview snapshot")
	}
}

func TestSearchContentStreamingNoMatches(t *testing.T) {
	if !rgAvailable() {
		t.Skip("rg not available on system")
	}

	tmpDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("hello world\n"), 0644); err != nil {
		t.Fatal(err)
	}

	previewCount := 0
	result, err := executeSearchContentStreaming(context.Background(), SearchContentInput{
		Pattern: "nonexistent_pattern_xyz",
		Path:    tmpDir,
	}, func(string) {
		previewCount++
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractFirstText(result)
	if text != "No matches found" {
		t.Errorf("expected 'No matches found', got %q", text)
	}
	// No output → no dirty state → no preview frames.
	if previewCount != 0 {
		t.Errorf("expected no previews for empty result, got %d", previewCount)
	}
}

func TestSearchContentStreamingEmptyPattern(t *testing.T) {
	_, err := executeSearchContentStreaming(context.Background(), SearchContentInput{
		Pattern: "",
	}, func(string) {})
	if err == nil {
		t.Fatal("expected error for empty pattern")
	}
	if err.Error() != "pattern is required" {
		t.Errorf("expected 'pattern is required', got %q", err.Error())
	}
}

func TestSearchContentStreamingMaxLines(t *testing.T) {
	if !rgAvailable() {
		t.Skip("rg not available on system")
	}

	tmpDir := t.TempDir()

	for f := 0; f < 5; f++ {
		var content string
		for i := 0; i < 20; i++ {
			content += "match line\n"
		}
		if err := os.WriteFile(filepath.Join(tmpDir, "file"+strconv.Itoa(f)+".txt"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	result, err := executeSearchContentStreaming(context.Background(), SearchContentInput{
		Pattern:  "match",
		Path:     tmpDir,
		MaxLines: 5,
	}, func(string) {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractFirstText(result)
	if !strings.Contains(text, "matching lines") {
		t.Errorf("expected 'matching lines' in output, got:\n%s", text)
	}
	if !strings.Contains(text, "Results saved to:") {
		t.Errorf("expected 'Results saved to:' in output, got:\n%s", text)
	}
}
