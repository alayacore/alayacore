//go:build !windows

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

// An atomic replace needs write permission on the *directory*, while an in-place
// write only needs the file itself to be writable. A writable file inside a
// read-only directory is a real layout (vendored trees, service configs owned by
// root with a group-writable file), so the tool keeps the capability — and says
// that the write it made was not atomic, so neither the model nor the user
// mistakes it for one.
func TestWriteFileWritableFileInReadOnlyDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "app.conf")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil { // file stays writable
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	parts, err := executeWriteFile(context.Background(), WriteFileInput{Path: path, Content: "updated"})
	if err != nil {
		t.Fatalf("write failed in a read-only directory: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("got %d content parts, want 1", len(parts))
	}
	text, ok := parts[0].(*llm.TextPart)
	if !ok {
		t.Fatalf("result is %T, want *llm.TextPart", parts[0])
	}
	if !strings.Contains(text.Text, "not atomic") {
		t.Errorf("result = %q, want it to say the write was not atomic", text.Text)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "updated" {
		t.Errorf("content = %q, want %q", data, "updated")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Errorf("mode = %o, want 0644 preserved through the fallback", got)
	}
}

// The fallback must not swallow real errors: with the directory read-only AND
// the file read-only, the write still fails, and no temp file is stranded.
func TestWriteFileReadOnlyDirAndReadOnlyFileStillFails(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ro2")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "locked.txt")
	if err := os.WriteFile(path, []byte("original"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if _, err := executeWriteFile(context.Background(), WriteFileInput{Path: path, Content: "nope"}); err == nil {
		t.Fatal("expected failure writing a read-only file in a read-only dir")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "locked.txt" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("stray files left behind: %v", names)
	}
}
