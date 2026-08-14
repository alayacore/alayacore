package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteFileValidation(t *testing.T) {
	// Missing path.
	_, err := executeWriteFile(context.Background(), WriteFileInput{Path: "", Content: "x"})
	if err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("expected 'path is required' error, got: %v", err)
	}

	// Missing content.
	_, err = executeWriteFile(context.Background(), WriteFileInput{Path: "somefile", Content: ""})
	if err == nil || !strings.Contains(err.Error(), "content is required") {
		t.Fatalf("expected 'content is required' error, got: %v", err)
	}
}

// TestWriteFileCreatesNewFile verifies that a brand-new file is created
// and its content is readable back. The exact permission bits are
// asserted in write_file_mode_unix_test.go (0644 narrowed by umask) —
// they differ on Windows.
func TestWriteFileCreatesNewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.txt")

	parts, err := executeWriteFile(context.Background(), WriteFileInput{Path: path, Content: "hello"})
	if err != nil {
		t.Fatalf("executeWriteFile: %v", err)
	}
	if len(parts) == 0 {
		t.Fatal("expected a result part")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("content = %q, want %q", string(data), "hello")
	}
}

// TestWriteFileOverwritePreservesMode locks in the overwrite semantics:
// replacing an existing file must NOT reset its permissions (a 0755
// script stays executable). This matches edit_file's mode preservation.
func TestWriteFileOverwritePreservesMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "script.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nold\n"), 0755); err != nil {
		t.Fatal(err)
	}

	if _, err := executeWriteFile(context.Background(), WriteFileInput{Path: path, Content: "#!/bin/sh\nnew\n"}); err != nil {
		t.Fatalf("executeWriteFile: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != "#!/bin/sh\nnew\n" {
		t.Fatalf("content = %q, want replacement content", string(data))
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0755 {
			t.Fatalf("existing file mode after overwrite = %o, want 755 (mode must be preserved)", perm)
		}
	}
}
