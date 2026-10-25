package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// edit_file replaces its target with a renamed temp file. Renaming onto a
// symlink would destroy the link, so the write must resolve through it — the
// dotfiles-in-a-repo layout (~/.bashrc -> repo/bashrc) is extremely common and
// silently losing the link is data loss the user cannot undo by reading the
// file back.
func TestEditFileFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.txt")
	link := filepath.Join(dir, "link.txt")

	if err := os.WriteFile(real, []byte("alpha\nbeta\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := executeEditFile(context.Background(), EditFileInput{
		Path: link, OldString: "beta", NewString: "gamma",
	}); err != nil {
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
	if string(data) != "alpha\ngamma\n" {
		t.Errorf("real file = %q, want %q", data, "alpha\ngamma\n")
	}
}

// Editing through a symlink must preserve the target's mode, not the link's
// (links report 0777 on Linux, which would make every dotfile executable).
func TestEditFileSymlinkPreservesTargetMode(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "script.sh")
	link := filepath.Join(dir, "script-link.sh")

	if err := os.WriteFile(real, []byte("echo hi\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := executeEditFile(context.Background(), EditFileInput{
		Path: link, OldString: "hi", NewString: "there",
	}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(real)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0755 {
		t.Errorf("target mode = %o, want 0755", perm)
	}
}

// A dangling symlink (target not yet created) is not a missing path: the link
// exists, and edit_file cannot create the target it points at. The message has
// to say that, because "file not found" about a path the model can see sends it
// back to the same path — write_file, which does create through the link, would
// have succeeded, so the difference is the whole diagnosis.
func TestEditFileDanglingSymlinkNamesTheTarget(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "dangling.txt")
	target := filepath.Join(dir, "never-created.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, err := executeEditFile(context.Background(), EditFileInput{
		Path: link, OldString: "x", NewString: "y",
	})
	if err == nil {
		t.Fatal("expected an error for a symlink whose target does not exist")
	}
	msg := err.Error()
	if strings.Contains(msg, "file not found") {
		t.Errorf("error claims the path does not exist, but the symlink does: %q", msg)
	}
	if !strings.Contains(msg, "symlink") || !strings.Contains(msg, target) {
		t.Errorf("error should name the symlink condition and its target %q, got %q", target, msg)
	}
}
