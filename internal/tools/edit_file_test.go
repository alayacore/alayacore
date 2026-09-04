package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

func TestEditFile(t *testing.T) {
	// Create a temporary directory for test files
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")

	tests := []struct {
		name        string
		setup       func() error
		input       EditFileInput
		expectError bool
		errorMsg    string
		expected    string // expected file content after edit
	}{
		{
			name: "simple replacement",
			setup: func() error {
				return os.WriteFile(testFile, []byte("hello world"), 0644)
			},
			input: EditFileInput{
				Path:      testFile,
				OldString: "hello",
				NewString: "goodbye",
			},
			expected: "goodbye world",
		},
		{
			name: "no changes when old and new are same",
			setup: func() error {
				return os.WriteFile(testFile, []byte("hello world"), 0644)
			},
			input: EditFileInput{
				Path:      testFile,
				OldString: "hello",
				NewString: "hello",
			},
			expectError: true,
			errorMsg:    "identical",
		},
		{
			name: "file not found",
			input: EditFileInput{
				Path:      "/nonexistent/file.txt",
				OldString: "hello",
				NewString: "world",
			},
			expectError: true,
			errorMsg:    "not found",
		},
		{
			name: "old_string not found in file",
			setup: func() error {
				return os.WriteFile(testFile, []byte("hello world"), 0644)
			},
			input: EditFileInput{
				Path:      testFile,
				OldString: "goodbye",
				NewString: "world",
			},
			expectError: true,
			errorMsg:    "not found",
		},
		{
			name: "path required",
			input: EditFileInput{
				Path:      "",
				OldString: "hello",
				NewString: "world",
			},
			expectError: true,
			errorMsg:    "required",
		},
		{
			name: "old_string required",
			input: EditFileInput{
				Path:      testFile,
				OldString: "",
				NewString: "world",
			},
			expectError: true,
			errorMsg:    "required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean up and setup
			os.Remove(testFile)
			if tt.setup != nil {
				if err := tt.setup(); err != nil {
					t.Fatalf("test fixture: %v", err)
				}
			}

			content, err := executeEditFile(context.Background(), tt.input)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error, got success: %v", content)
				} else if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errorMsg, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Verify file content
			fileContent, err := os.ReadFile(testFile)
			if err != nil {
				t.Fatalf("failed to read file: %v", err)
			}
			if string(fileContent) != tt.expected {
				t.Errorf("expected file content %q, got %q", tt.expected, string(fileContent))
			}
		})
	}
}

// TestStreamEditorBufferCapacity verifies the initial buffer pre-allocation
// is capped: normal old_strings keep the 2× + chunk hint, but a huge
// LLM-supplied old_string cannot trigger a proportional allocation.
func TestStreamEditorBufferCapacity(t *testing.T) {
	// Normal old_string — the 2× + chunk hint applies (no cap hit).
	small := newStreamEditor("abc", "x")
	want := 2*len("abc") + 4096
	if cap(small.buffer) != want {
		t.Errorf("small buffer capacity = %d, want %d", cap(small.buffer), want)
	}

	// Huge old_string — the pre-allocation is capped, not proportional.
	huge := strings.Repeat("a", 2*maxEditBufferCapacity)
	big := newStreamEditor(huge, "x")
	if cap(big.buffer) > maxEditBufferCapacity {
		t.Errorf("huge buffer capacity = %d, want <= %d", cap(big.buffer), maxEditBufferCapacity)
	}
}

func TestEditFileEdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		oldString   string
		newString   string
		shouldError bool
		errorMsg    string
	}{
		{
			name:        "old_string not found",
			content:     "hello world",
			oldString:   "not found",
			newString:   "replacement",
			shouldError: true,
			errorMsg:    "not found",
		},
		{
			name:        "old_string appears multiple times",
			content:     "foo bar foo",
			oldString:   "foo",
			newString:   "baz",
			shouldError: true,
			errorMsg:    "found multiple times",
		},
		{
			name:        "successful replacement",
			content:     "hello world",
			oldString:   "world",
			newString:   "universe",
			shouldError: false,
		},
		{
			name:        "empty new_string (deletion)",
			content:     "hello world",
			oldString:   " world",
			newString:   "",
			shouldError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			testFile := filepath.Join(tmpDir, "test.txt")
			if err := os.WriteFile(testFile, []byte(tt.content), 0644); err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}

			_, err := executeEditFile(context.Background(), EditFileInput{
				Path:      testFile,
				OldString: tt.oldString,
				NewString: tt.newString,
			})

			if tt.shouldError {
				if err == nil {
					t.Errorf("Expected error, got success")
				} else if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Error message should contain %q, got: %q", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Expected success, got error: %v", err)
				}

				content, err := os.ReadFile(testFile)
				if err != nil {
					t.Fatalf("Failed to read file: %v", err)
				}

				expectedContent := tt.content[:len(tt.content)-len(tt.oldString)]
				expectedContent = expectedContent[:strings.LastIndex(tt.content, tt.oldString)]
				expectedContent += tt.newString
				expectedContent += tt.content[strings.LastIndex(tt.content, tt.oldString)+len(tt.oldString):]

				if string(content) != expectedContent {
					t.Errorf("Expected content %q, got %q", expectedContent, string(content))
				}
			}
		})
	}
}

// ============================================================================
// Whitespace-tolerant fallback matching
// ============================================================================

func TestNormalizeWhitespace(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"no whitespace", "abc", "abc"},
		{"single spaces preserved", "a b c", "a b c"},
		{"mixed run collapsed", "a  \t b", "a b"},
		{"crlf collapsed", "a\r\nb", "a b"},
		{"leading and trailing runs", "  abc  ", " abc "},
		{"all whitespace", "  \t\r\n ", " "},
		{"empty", "", ""},
		{"single space", " ", " "},
		{"utf8 preserved", "hé  llo", "hé llo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(normalizeWhitespace([]byte(tt.in)))
			if got != tt.want {
				t.Errorf("normalizeWhitespace(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestEditFileWhitespaceTolerant exercises the fallback path: exact matching
// fails, whitespace-tolerant matching succeeds (or fails with the expected
// error). Every success case is constructed so the old_string does NOT exist
// byte-for-byte in the content (otherwise the exact path handles it and the
// fallback never runs). Expected contents pin down the exact replaced span.
func TestEditFileWhitespaceTolerant(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		oldString   string
		newString   string
		wantErr     string // substring of expected error; empty = success
		wantContent string // expected file content on success
	}{
		{
			name:        "tab vs spaces",
			content:     "foo\tbar",
			oldString:   "foo bar",
			newString:   "baz",
			wantContent: "baz",
		},
		{
			name:        "indented block tab vs spaces",
			content:     "if x {\n    foo()\n}",
			oldString:   "if x {\n\tfoo()\n}",
			newString:   "if x {\n\tbar()\n}",
			wantContent: "if x {\n\tbar()\n}",
		},
		{
			name:        "missing indentation in old_string",
			content:     "if x {\n    foo()\n}",
			oldString:   "if x {\nfoo()\n}",
			newString:   "if x {\nbar()\n}",
			wantContent: "if x {\nbar()\n}",
		},
		{
			name:        "extra spaces",
			content:     "foo  bar",
			oldString:   "foo bar",
			newString:   "baz",
			wantContent: "baz",
		},
		{
			name:        "crlf vs lf",
			content:     "foo\r\nbar",
			oldString:   "foo\nbar",
			newString:   "baz",
			wantContent: "baz",
		},
		{
			name:        "crlf partial replacement",
			content:     "foo\r\nbar\r\nbaz",
			oldString:   "bar\nbaz",
			newString:   "BAR",
			wantContent: "foo\r\nBAR",
		},
		{
			name:      "ambiguity after normalization fails",
			content:   "a  b a  b",
			oldString: "a b",
			newString: "x",
			wantErr:   "multiple",
		},
		{
			name:      "content typo not tolerated",
			content:   "hello world",
			oldString: "hellx world",
			newString: "x",
			wantErr:   "not found",
		},
		{
			name:      "old_string with extra whitespace not tolerated",
			content:   "foo",
			oldString: "  foo",
			newString: "x",
			wantErr:   "not found",
		},
		{
			name:      "leading newline not tolerated",
			content:   "foo",
			oldString: "\nfoo",
			newString: "x",
			wantErr:   "not found",
		},
		{
			name:        "utf8 content preserved",
			content:     "hé  llo",
			oldString:   "hé llo",
			newString:   "hi",
			wantContent: "hi",
		},
		{
			name:        "match at file start",
			content:     "foo bar baz",
			oldString:   "foo  bar",
			newString:   "X",
			wantContent: "X baz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			testFile := filepath.Join(tmpDir, "test.txt")
			if err := os.WriteFile(testFile, []byte(tt.content), 0644); err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}

			parts, err := executeEditFile(context.Background(), EditFileInput{
				Path:      testFile,
				OldString: tt.oldString,
				NewString: tt.newString,
			})

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got success: %v", tt.wantErr, parts)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			content, rerr := os.ReadFile(testFile)
			if rerr != nil {
				t.Fatalf("failed to read file: %v", rerr)
			}
			if string(content) != tt.wantContent {
				t.Errorf("expected content %q, got %q", tt.wantContent, string(content))
			}

			// Fallback edits must be marked as whitespace-insensitive so the
			// model knows its old_string was not exact.
			if len(parts) != 1 {
				t.Fatalf("expected 1 content part, got %d", len(parts))
			}
			text, ok := parts[0].(*llm.TextPart)
			if !ok {
				t.Fatalf("expected TextPart, got %T", parts[0])
			}
			if !strings.Contains(text.Text, "whitespace-insensitive") {
				t.Errorf("fallback success message %q should mark the match as whitespace-insensitive", text.Text)
			}
		})
	}
}

// TestEditFileExactMatchStillPreferred verifies the exact path keeps its
// message and that whitespace-tolerant fallback is not invoked when an exact
// match exists.
func TestEditFileExactMatchStillPreferred(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("foo bar"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	parts, err := executeEditFile(context.Background(), EditFileInput{
		Path:      testFile,
		OldString: "foo",
		NewString: "baz",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, rerr := os.ReadFile(testFile)
	if rerr != nil {
		t.Fatalf("failed to read file: %v", rerr)
	}
	if string(content) != "baz bar" {
		t.Errorf("expected content %q, got %q", "baz bar", string(content))
	}

	text, ok := parts[0].(*llm.TextPart)
	if !ok {
		t.Fatalf("expected TextPart, got %T", parts[0])
	}
	if strings.Contains(text.Text, "whitespace-insensitive") {
		t.Errorf("exact-match success message %q must not claim a whitespace-insensitive match", text.Text)
	}
}

// TestEditFileTolerantChunkBoundaries exercises matches and whitespace runs
// that straddle the 4096-byte streaming chunk boundary.
func TestEditFileTolerantChunkBoundaries(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		oldString   string
		newString   string
		wantContent string
	}{
		{
			name: "old_string spanning chunks",
			content: strings.Repeat("a", 5000) + "  " +
				strings.Repeat("b", 100),
			oldString: strings.Repeat("a", 5000) + " " +
				strings.Repeat("b", 50),
			newString:   "X",
			wantContent: "X" + strings.Repeat("b", 50),
		},
		{
			name:        "whitespace run spanning chunks",
			content:     strings.Repeat(" ", 4096) + "abc",
			oldString:   "\tabc",
			newString:   "X",
			wantContent: "X",
		},
		{
			name:        "match starting at chunk boundary",
			content:     strings.Repeat("a", 4096) + "\nfoo",
			oldString:   "\rfoo",
			newString:   "done",
			wantContent: strings.Repeat("a", 4096) + "done",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			testFile := filepath.Join(tmpDir, "test.txt")
			if err := os.WriteFile(testFile, []byte(tt.content), 0644); err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}

			_, err := executeEditFile(context.Background(), EditFileInput{
				Path:      testFile,
				OldString: tt.oldString,
				NewString: tt.newString,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			content, rerr := os.ReadFile(testFile)
			if rerr != nil {
				t.Fatalf("failed to read file: %v", rerr)
			}
			if string(content) != tt.wantContent {
				t.Errorf("expected content %q, got %q", tt.wantContent, string(content))
			}
		})
	}
}

// TestEditFileTolerantEndToEndFile verifies file metadata survives the
// fallback path (atomic rename + mode preservation), matching the exact
// path's behavior.
func TestEditFileTolerantEndToEndFile(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.sh")
	if err := os.WriteFile(testFile, []byte("echo  hello\n"), 0755); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	_, err := executeEditFile(context.Background(), EditFileInput{
		Path:      testFile,
		OldString: "echo hello",
		NewString: "echo world",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, serr := os.Stat(testFile)
	if serr != nil {
		t.Fatalf("failed to stat file: %v", serr)
	}
	if info.Mode().Perm() != 0755 {
		t.Errorf("expected mode 0755, got %v", info.Mode().Perm())
	}

	content, rerr := os.ReadFile(testFile)
	if rerr != nil {
		t.Fatalf("failed to read file: %v", rerr)
	}
	if string(content) != "echo world\n" {
		t.Errorf("expected content %q, got %q", "echo world\n", string(content))
	}
}
