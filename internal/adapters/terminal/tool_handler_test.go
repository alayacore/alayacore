package terminal

import (
	"fmt"
	"strings"
	"testing"
)

func TestGenericHandler_FormatCall_WithArgs(t *testing.T) {
	h := &GenericHandler{name: "my_tool"}

	// Normal input with actual arguments should show the args
	result := h.FormatCall([]byte(`{"key":"value"}`))
	expected := "my_tool: {\"key\":\"value\"}\n"
	if result != expected {
		t.Errorf("FormatCall with args = %q, want %q", result, expected)
	}
}

// TestGenericHandlerCompactsPrettyJSON verifies pretty-printed JSON
// arguments (hard newlines inside the payload) are compacted to a single
// line — otherwise the parameter shows hard line breaks mid-parameter and
// copying yields newlines in the middle of the tool call.
func TestGenericHandlerCompactsPrettyJSON(t *testing.T) {
	h := &GenericHandler{name: "custom_tool"}

	pretty := []byte("{\n  \"command\": \"ls\",\n  \"path\": \"/tmp\"\n}")
	got := h.FormatCall(pretty)
	want := "custom_tool: {\"command\":\"ls\",\"path\":\"/tmp\"}\n"
	if got != want {
		t.Errorf("FormatCall(pretty) = %q, want %q", got, want)
	}
	if strings.Contains(got, "\n") && got != want {
		t.Errorf("FormatCall output contains unexpected newlines: %q", got)
	}

	// Non-JSON input (defensive fallback) passes through unchanged.
	if got := h.FormatCall([]byte("plain text")); got != "custom_tool: plain text\n" {
		t.Errorf("FormatCall(non-JSON) = %q", got)
	}
}

// TestEditFileHandlerBlankLineChangeKeepsItsMarker pins the shape of a diff
// whose whole change is a blank line. The call is one that actually happened,
// as recorded: old_string ends with a newline and new_string does not, so the
// two lines above it are identical and the entire edit is the removal of one
// empty line. That row carries "- " like any other removal — the text after the
// marker is empty, which is why the marker, not the text, has to say what
// happened to the line.
func TestEditFileHandlerBlankLineChangeKeepsItsMarker(t *testing.T) {
	h := &EditFileHandler{}
	input := `{"path":"internal/mcp/client.go",` +
		`"old_string":"// loadTransport returns the current transport, or nil.\nfunc (c *Client) loadTransport() Transport {\n",` +
		`"new_string":"// loadTransport returns the current transport, or nil.\nfunc (c *Client) loadTransport() Transport {"}`

	got := h.FormatCall([]byte(input))
	want := "edit_file: internal/mcp/client.go\n" +
		"  // loadTransport returns the current transport, or nil.\n" +
		"  func (c *Client) loadTransport() Transport {\n" +
		"- "
	if got != want {
		t.Errorf("FormatCall:\n  got:  %q\n  want: %q", got, want)
	}
}

// TestEditFileHandlerFormatCallHugeInput pins the size cap. A pair of huge
// arguments — the shape that once allocated an 800MB table and took the session
// down with it — is not diffed at all, and the block says so in one row. It
// must not draw every line as removed and added instead: that is a claim about
// the change nobody made, and the longest way to say nothing.
func TestEditFileHandlerFormatCallHugeInput(t *testing.T) {
	h := &EditFileHandler{}
	// JSON-escaped already: the block is one line per entry, joined.
	makeBlock := func(prefix string, n int) string {
		lines := make([]string, n)
		for i := range lines {
			lines[i] = fmt.Sprintf("%s-%d", prefix, i)
		}
		return strings.Join(lines, `\n`)
	}
	// Both sides exceed the cap (2*maxDiffLines+1 lines each) and share no end
	// at all, so the whole pair is the undiffable middle.
	n := 2*maxDiffLines + 1
	input := `{"path":"f","old_string":"` + makeBlock("old", n) + `","new_string":"` + makeBlock("new", n) + `"}`

	result := h.FormatCall([]byte(input))
	want := fmt.Sprintf("edit_file: f\n… %d lines hidden, too large to diff", 2*n)
	if result != want {
		t.Errorf("FormatCall:\n  got:  %q\n  want: %q", result, want)
	}
}

// execute_command names a directory now, and where a command ran is part of
// what it was: "./scripts/fetch.sh" in the skill's folder and in the project are
// different calls that would otherwise print identically.
func TestExecuteCommandHandlerFormatCallShowsWorkDir(t *testing.T) {
	h := &ExecuteCommandHandler{}

	plain := h.FormatCall([]byte(`{"command":"ls -la"}`))
	if want := "execute_command: ls -la\n"; plain != want {
		t.Errorf("without workdir = %q, want %q", plain, want)
	}

	// The workdir goes on a line of its own, after the command, so it cannot be
	// read as one of the command's own arguments (see dirMarker).
	withDir := h.FormatCall([]byte(`{"command":"./scripts/fetch.sh","workdir":"/home/me/skills/weather"}`))
	want := "execute_command: ./scripts/fetch.sh\n[dir=/home/me/skills/weather]\n"
	if withDir != want {
		t.Errorf("with workdir = %q, want %q", withDir, want)
	}

	// An empty workdir is the same call as no workdir; it must not print "[]".
	if got := h.FormatCall([]byte(`{"command":"ls","workdir":""}`)); got != "execute_command: ls\n" {
		t.Errorf("empty workdir = %q", got)
	}

	if got := h.FormatCall([]byte(`{broken`)); got != "execute_command: <parse error>" {
		t.Errorf("unparsable input = %q", got)
	}
}
