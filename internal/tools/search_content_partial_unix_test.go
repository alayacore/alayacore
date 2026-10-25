//go:build !windows

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ripgrep exits 2 for a run that hit an error and kept going: an unreadable
// subdirectory beside readable ones leaves a permission complaint on stderr and
// every match it could read on stdout. Reporting the complaint as the whole
// result discards those matches and answers "not found" about a pattern that was
// found — the one answer the model cannot check, since the failure looks like a
// property of the tree rather than of the search. The matches are delivered and
// the warning says the search was partial.
func TestSearchContentKeepsMatchesWhenRipgrepReportsErrors(t *testing.T) {
	requireRipgrep(t)
	if os.Geteuid() == 0 {
		t.Skip("running as root: an unreadable directory is still readable")
	}

	dir := t.TempDir()
	open := filepath.Join(dir, "open")
	locked := filepath.Join(dir, "locked")
	for _, d := range []string{open, locked} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(open, "hit.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "unreadable.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	parts, err := executeSearchContent(context.Background(), SearchContentInput{
		Pattern: "needle",
		Path:    dir,
	})
	if err != nil {
		t.Fatalf("a found match must not be reported as an error: %v", err)
	}

	text := extractFirstText(parts)
	if !strings.Contains(text, "hit.txt:1:needle") {
		t.Errorf("the readable match is missing from the result: %q", text)
	}
	if !strings.Contains(text, "incomplete") {
		t.Errorf("result does not say the search was partial: %q", text)
	}
	if !strings.Contains(text, "Permission denied") {
		t.Errorf("result does not carry ripgrep's reason: %q", text)
	}
}

// The warning has to reach the file, not just the summary. The file is what a
// model reads back through read_file — now, or after the summary has scrolled out
// of its context — and a results list that omits why it is short is the same
// false negative one level down. execute_command writes its stop reason into the
// saved file for the same reason.
func TestSearchContentIncompleteWarningReachesTheSavedFile(t *testing.T) {
	requireRipgrep(t)
	if os.Geteuid() == 0 {
		t.Skip("running as root: an unreadable directory is still readable")
	}

	dir := t.TempDir()
	open := filepath.Join(dir, "open")
	locked := filepath.Join(dir, "locked")
	for _, d := range []string{open, locked} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(open, "hit.txt"),
		[]byte(strings.Repeat("needle\n", 20)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "unreadable.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	// max_lines below the match count forces the save-to-file path.
	parts, err := executeSearchContent(context.Background(), SearchContentInput{
		Pattern:  "needle",
		Path:     dir,
		MaxLines: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := extractFirstText(parts)
	_, after, ok := strings.Cut(text, "Results saved to: ")
	if !ok {
		t.Fatalf("expected the large-result summary, got %q", text)
	}
	saved, _, _ := strings.Cut(after, "\n")
	t.Cleanup(func() { _ = os.Remove(saved) })

	data, err := os.ReadFile(saved)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.Contains(body, "needle") {
		t.Errorf("saved file holds no results: %q", body)
	}
	if !strings.Contains(body, "incomplete") || !strings.Contains(body, "Permission denied") {
		t.Errorf("saved file does not say the search was partial: %q", body)
	}
}
