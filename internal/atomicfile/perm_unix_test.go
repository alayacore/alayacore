//go:build !windows

package atomicfile

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// The atomic write sets the mode with chmod, and chmod is NOT filtered by the
// umask the way open(2) is. Applying a literal perm would therefore silently
// WIDEN permissions for anyone running under a restrictive umask — backwards for
// a config file that may hold an API key.

// Wiring test: force the cached umask and confirm Write actually applies it.
// Deterministic because currentUmask caches after the first read, so the only
// honest way to exercise the restrictive-umask path without racing the
// process-global syscall is to set the cached value and assert behavior.
func TestWriteNewFileNarrowsPermByUmask(t *testing.T) {
	saved := currentUmask() // trigger the real read, then override the cache
	defer func() { umaskValue = saved }()

	tests := []struct {
		umask os.FileMode
		want  os.FileMode
	}{
		{0o022, 0o600}, // the mode a config file asks for is already private
		{0o077, 0o600},
		{0o777, 0o000},
	}
	for _, tt := range tests {
		umaskValue = tt.umask
		path := filepath.Join(t.TempDir(), "runtime.conf")
		if err := Write(path, []byte("x"), 0o600); err != nil {
			t.Fatalf("umask %o: %v", tt.umask, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != tt.want {
			t.Errorf("umask %o: new file mode = %o, want %o", tt.umask, got, tt.want)
		}
	}
}

func TestWriteNewFileMatchesNarrowedPerm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theme.conf")
	if err := Write(path, []byte("primary: #ff0000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o644)&^currentUmask(); got != want {
		t.Errorf("new file mode = %o, want %o (0644 narrowed by cached umask %o)", got, want, currentUmask())
	}
}

// An existing file keeps its exact mode regardless of the umask: re-narrowing
// would strip bits the user deliberately set (a 0755 script going to 0700 under
// a 0077 umask would stop running).
func TestWriteExistingFileKeepsExactMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "script.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Pin the cached umask BEFORE changing the process-global one: currentUmask
	// reads the real value exactly once (sync.Once), and a read that happened
	// inside this window would latch 0077 for the rest of this test binary.
	_ = currentUmask()

	old := syscall.Umask(0o077)
	defer syscall.Umask(old) // defer, not a bare call: a Fatal must not leave 0077 behind

	if err := Write(path, []byte("#!/bin/sh\necho hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Errorf("existing file mode = %o, want 0755 (exact preservation, no umask narrowing)", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "#!/bin/sh\necho hi\n"; got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
}

// A rename needs write permission on the *directory*, while an in-place write
// only needs the file itself to be writable. WriteBestEffort exists because a
// writable file inside a read-only directory is a real layout (vendored trees,
// service configs owned by root with a group-writable file) and refusing it would
// remove a capability the caller had before.
func TestWriteBestEffortWritableFileInReadOnlyDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "runtime.conf")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil { // file stays writable
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	inPlace, err := WriteBestEffort(path, []byte("updated"), 0o600)
	if err != nil {
		t.Fatalf("write failed in a read-only directory: %v", err)
	}
	if !inPlace {
		t.Error("the in-place fallback was not reported; the caller cannot tell the write was not atomic")
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
func TestWriteBestEffortReadOnlyDirAndFileStillFails(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ro2")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "locked.conf")
	if err := os.WriteFile(path, []byte("original"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if _, err := WriteBestEffort(path, []byte("nope"), 0o600); err == nil {
		t.Fatal("expected failure writing a read-only file in a read-only dir")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "locked.conf" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("stray files left behind: %v", names)
	}
}

// The atomic path is not reported as degraded: a caller that always says "this
// write was not atomic" would be as misleading as one that never does.
func TestWriteBestEffortReportsAtomicWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.conf")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	inPlace, err := WriteBestEffort(path, []byte("updated"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if inPlace {
		t.Error("a rename commit was reported as an in-place write")
	}
}

// Write does not degrade: when the directory cannot hold a temp file, the
// non-atomic in-place write is precisely what it must not do, so the write fails
// and the previous content stands. AlayaCore's own files use this — a config file
// another process can read half-written is worse than a persistence error the
// session reports.
func TestWriteRefusesInPlaceFallback(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ro3")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "runtime.conf")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if err := Write(path, []byte("updated"), 0o600); err == nil {
		t.Fatal("Write fell back to an in-place write in a read-only directory")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Errorf("content = %q, want the previous content untouched", data)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("files left in %s: %v, want only runtime.conf", dir, names)
	}
}

// A config file symlinked out of a dotfiles repository must stay a symlink: the
// rename commits to the link's target, not over the link.
func TestWriteFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.conf")
	link := filepath.Join(dir, "runtime.conf")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := Write(link, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced by a regular file")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Errorf("target content = %q, want %q", data, "new")
	}
}
