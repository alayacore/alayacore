//go:build !windows

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A path the tool chose is reported absolutely — and the temp file beside the
// target is one of those. It reaches the model inside the IO errors the
// create-write-rename cycle reports, where a relative form would be the one path
// in these tools that cannot say where it is: the caller wrote "ro/exists.txt",
// but the name in the message belongs to the tool.
//
// An unwritable directory is how a caller gets that error in practice (a
// root-owned tree, a vendored one), which is why it is the fixture: no root, no
// mocking.
func TestFileToolsReportTheirTempFileAbsolutely(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: an unwritable directory is still writable")
	}

	dir := t.TempDir()
	ro := filepath.Join(dir, "ro")
	if err := os.Mkdir(ro, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ro, "exists.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })
	t.Chdir(dir)

	cases := []struct {
		tool string
		run  func() error
	}{
		{"write_file", func() error {
			_, err := executeWriteFile(context.Background(), WriteFileInput{Path: "ro/new.txt", Content: "y"})
			return err
		}},
		{"edit_file", func() error {
			_, err := executeEditFile(context.Background(), EditFileInput{Path: "ro/exists.txt", OldString: "x", NewString: "y"})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			err := tc.run()
			if err == nil {
				t.Fatal("expected the write to fail in an unwritable directory")
			}
			msg := err.Error()
			if !strings.Contains(msg, ro) {
				t.Errorf("temp path is not absolute, so it does not say where it is: %q", msg)
			}
			if strings.Contains(msg, "open ro/") {
				t.Errorf("the caller's relative form leaked into a tool-chosen path: %q", msg)
			}
		})
	}
}
