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

	// A directory is rejected with a clear message instead of a rename error.
	if _, err := executeWriteFile(context.Background(), WriteFileInput{Path: t.TempDir(), Content: "x"}); err == nil ||
		!strings.Contains(err.Error(), "it is a directory") {
		t.Fatalf("expected 'it is a directory' error, got: %v", err)
	}
}

// Empty content used to be rejected with "content is required", which left the
// model no way to truncate a file to zero length — a legitimate request it
// could only satisfy by running a shell command.
func TestWriteFileEmptyContentTruncates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.txt")
	if _, err := executeWriteFile(context.Background(), WriteFileInput{Path: path, Content: "prior content"}); err != nil {
		t.Fatal(err)
	}

	if _, err := executeWriteFile(context.Background(), WriteFileInput{Path: path, Content: ""}); err != nil {
		t.Fatalf("empty content should truncate the file, got error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Errorf("file still holds %q, want empty", data)
	}
}

// Creating a file with empty content must work too, not just truncating.
func TestWriteFileCreatesEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blank.txt")

	if _, err := executeWriteFile(context.Background(), WriteFileInput{Path: path, Content: ""}); err != nil {
		t.Fatalf("creating an empty file failed: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("file was not created: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("size = %d, want 0", info.Size())
	}
}

// The write is atomic: the target is replaced by a rename, so an interrupted
// write can never leave a truncated file where a complete one was.
func TestWriteFileLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if _, err := executeWriteFile(context.Background(), WriteFileInput{Path: path, Content: "a"}); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "out.txt" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("temp file left behind: %v", names)
	}
}

// A symlinked target (the dotfiles-in-a-repo layout) must keep working as a
// link: renaming a temp file over the link would silently destroy it.
func TestWriteFileFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.txt")
	link := filepath.Join(dir, "link.txt")

	if err := os.WriteFile(real, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := executeWriteFile(context.Background(), WriteFileInput{Path: link, Content: "updated"}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced by a regular file")
	}
	data, err := os.ReadFile(real)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "updated" {
		t.Errorf("real file holds %q, want %q", data, "updated")
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
