package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// workdir says where a command runs. Before it existed, every command ran in the
// process's own directory, so a skill that ships scripts/ and tells the agent to
// run ./scripts/fetch.sh could only be obeyed by the model thinking to prepend a
// cd — and the failure when it did not was a shell error about a missing file,
// not a missing capability.

func TestExecuteCommandWorkDirRunsThere(t *testing.T) {
	dir := t.TempDir()

	content, err := executeCommand(context.Background(), ExecuteCommandInput{
		Command: "pwd",
		WorkDir: dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(extractText(content)); got != dir {
		t.Errorf("command ran in %q, want %q", got, dir)
	}
}

// A relative workdir means the same thing as a relative path in any other tool:
// relative to the current working directory.
func TestExecuteCommandWorkDirIsRelativeToTheProcessDir(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	content, err := executeCommand(context.Background(), ExecuteCommandInput{
		Command: "pwd",
		WorkDir: "sub",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(extractText(content)); got != sub {
		t.Errorf("command ran in %q, want %q", got, sub)
	}
}

// No workdir keeps the old behavior exactly: the process's own directory. The
// process directory is changed rather than the argument, so this is the same
// command with the argument absent.
func TestExecuteCommandWithoutWorkDirUsesProcessDir(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	content, err := executeCommand(context.Background(), ExecuteCommandInput{Command: "pwd"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(extractText(content)); got != dir {
		t.Errorf("command ran in %q, want the process directory %q", got, dir)
	}
}

// The process directory is what it was before and after: a workdir on one call
// does not move the next one. This is the assumption the parameter description
// sells, and the one a model is most likely to get wrong the other way.
func TestExecuteCommandWorkDirDoesNotPersist(t *testing.T) {
	root := t.TempDir()
	other := filepath.Join(root, "other")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	if _, err := executeCommand(context.Background(), ExecuteCommandInput{Command: "pwd", WorkDir: other}); err != nil {
		t.Fatal(err)
	}

	content, err := executeCommand(context.Background(), ExecuteCommandInput{Command: "pwd"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(extractText(content)); got != root {
		t.Errorf("the next call ran in %q, want the unchanged process directory %q", got, root)
	}
}

// A directory that cannot be used is said so, and the command never starts: a
// chdir failure carried in a shell's own error text reads as a broken command,
// and the model answers that by rewriting a command that was fine.
func TestExecuteCommandWorkDirMistakes(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		workDir string
		want    string
	}{
		{"missing", filepath.Join(root, "nope"), "does not exist"},
		{"not a directory", file, "is not a directory"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(root)

			_, err := executeCommand(context.Background(), ExecuteCommandInput{
				// The command has a side effect: if it ran at all, the marker
				// proves it.
				Command: "touch marker-" + strings.ReplaceAll(tc.name, " ", ""),
				WorkDir: tc.workDir,
			})
			if err == nil {
				t.Fatalf("workdir %q accepted, want an error naming %q", tc.workDir, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to say %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), tc.workDir) {
				t.Errorf("err = %v, want it to name the directory", err)
			}
			if _, statErr := os.Stat(filepath.Join(root, "marker-"+strings.ReplaceAll(tc.name, " ", ""))); statErr == nil {
				t.Error("the command ran despite the unusable workdir")
			}
		})
	}
}

// A linked directory is a directory: the usual way a skill folder is arranged is
// a symlink, and refusing to run inside one would send the agent back to cd.
func TestExecuteCommandWorkDirThroughSymlink(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	content, err := executeCommand(context.Background(), ExecuteCommandInput{Command: "pwd", WorkDir: link})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The child reports the directory it was given, so the answer is the link —
	// what matters is that the run happened inside it and did not fail.
	if got := strings.TrimSpace(extractText(content)); got != link && got != real {
		t.Errorf("command ran in %q, want %q or %q", got, link, real)
	}
}

// The streaming variant is the path a real call takes (the TUI shows live
// output); it has to honor the argument as well, or the same call would run in
// two different places depending on whether the preview was wanted.
func TestExecuteCommandStreamingHonoursWorkDir(t *testing.T) {
	dir := t.TempDir()

	var previews []string
	content, err := executeCommandStreaming(context.Background(), ExecuteCommandInput{
		Command: "pwd",
		WorkDir: dir,
	}, func(text string) { previews = append(previews, text) })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(extractText(content)); got != dir {
		t.Errorf("streamed call ran in %q, want %q", got, dir)
	}
}

// Through the tool surface the model actually calls: workdir is optional, so a
// call carrying only a command stays valid, and the bad-directory answer has to
// reach the model as a tool error.
func TestExecuteCommandWorkDirThroughToolSurface(t *testing.T) {
	tool := NewExecuteCommandTool()

	var schema struct {
		Required   []string       `json:"required"`
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(tool.Definition.Schema, &schema); err != nil {
		t.Fatal(err)
	}
	if _, ok := schema.Properties["workdir"]; !ok {
		t.Errorf("schema has no workdir property: %s", tool.Definition.Schema)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "command" {
		t.Errorf("required = %v, want only command — workdir must stay optional", schema.Required)
	}

	dir := t.TempDir()
	content, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"pwd","workdir":"`+filepath.ToSlash(dir)+`"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(extractText(content)); got != dir {
		t.Errorf("command ran in %q, want %q", got, dir)
	}

	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"pwd","workdir":"/definitely/not/here"}`)); err == nil {
		t.Error("an unusable workdir must fail the call, not run elsewhere")
	}
}
