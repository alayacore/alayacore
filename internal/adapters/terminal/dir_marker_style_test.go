package terminal

// ExecuteCommandHandler shows a call's workdir as a "[dir=…]" line of its own,
// under the command, drawn bold — the layout docs/tui.md → "Tool Result
// Separator" describes. These tests pin that layout and its weight, and
// the lines a rule that matched "[dir=…]" in the text would get wrong.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
)

const (
	testWorkDir = "/home/me/skills/weather"
	testCommand = "./scripts/fetch.sh"
)

// testCallText is what ExecuteCommandHandler formats for a call naming
// testWorkDir: the command line, then the annotation on its own line.
func testCallText() string {
	return "execute_command: " + testCommand + "\n" + dirMarker(testWorkDir) + "\n"
}

// newDirWindow builds an execute_command window from formatted call text,
// through the same entry point output.go uses.
func newDirWindow(t *testing.T, styles *Styles, text string) (*WindowBuffer, int) {
	t.Helper()
	wb := NewWindowBuffer(120, styles)
	wb.HandleToolInputEvent(protocol.ToolInputData{
		ID:    "t1",
		Name:  "execute_command",
		Input: json.RawMessage(text),
	}, 0)
	wb.HandleToolOutput("t1", "ok", false, 0)
	idx, ok := wb.LookupID("t1")
	if !ok {
		t.Fatal("tool window not found")
	}
	return wb, idx
}

// rowFor returns the rendered row whose visible text is exactly want.
func rowFor(rendered, want string) (string, bool) {
	for _, line := range strings.Split(rendered, "\n") {
		if stripANSI(line) == want {
			return line, true
		}
	}
	return "", false
}

// TestExecuteCommandDirMarkerOnItsOwnLine: the annotation is a row of its own,
// bold, and the command's row is plain and does not carry it.
func TestExecuteCommandDirMarkerOnItsOwnLine(t *testing.T) {
	styles := DefaultStyles()
	wb, idx := newDirWindow(t, styles, testCallText())
	wb.ToggleFold(idx)
	rendered := wb.GetAll(-1, false)

	commandRow, ok := rowFor(rendered, testCommand)
	if !ok {
		t.Fatalf("no row holding the command alone; the annotation must not trail it:\n%q", stripANSI(rendered))
	}
	if strings.Contains(commandRow, "\x1b[") {
		t.Errorf("the command's row must be plain body text, got %q", commandRow)
	}

	annotationRow, ok := rowFor(rendered, dirMarker(testWorkDir))
	if !ok {
		t.Fatalf("no row holding the annotation alone:\n%q", stripANSI(rendered))
	}
	if want := styles.Body.Bold(true).Render(dirMarker(testWorkDir)); annotationRow != want {
		t.Errorf("the annotation's row should be bold body text:\n  got:  %q\n  want: %q", annotationRow, want)
	}
}

// TestExecuteCommandDirMarkerDimsUnderOverlay: with Dimmed styles both rows
// carry the dim color (the annotation's row dim + bold) rather than the
// annotation's escapes leaving its row bright.
func TestExecuteCommandDirMarkerDimsUnderOverlay(t *testing.T) {
	styles := DefaultStyles().Dimmed()
	wb, idx := newDirWindow(t, styles, testCallText())
	wb.ToggleFold(idx)
	rendered := wb.GetAll(-1, false)

	commandRow, _ := rowFor(rendered, testCommand)
	if want := styles.Body.Render(testCommand); commandRow != want {
		t.Errorf("the command's row should carry the dim body color:\n  got:  %q\n  want: %q", commandRow, want)
	}
	annotationRow, _ := rowFor(rendered, dirMarker(testWorkDir))
	if want := styles.Body.Bold(true).Render(dirMarker(testWorkDir)); annotationRow != want {
		t.Errorf("the annotation's row should be dim + bold:\n  got:  %q\n  want: %q", annotationRow, want)
	}
}

// TestExecuteCommandNoWorkdirStaysPlain: the common case is untouched — a call
// with no workdir has one argument line and it carries no SGR at all.
func TestExecuteCommandNoWorkdirStaysPlain(t *testing.T) {
	wb, idx := newDirWindow(t, DefaultStyles(), "execute_command: ls -la\n")
	wb.ToggleFold(idx)

	for _, line := range strings.Split(wb.GetAll(-1, false), "\n") {
		if strings.Contains(stripANSI(line), "ls -la") && strings.Contains(line, "\x1b[") {
			t.Fatalf("a command with no workdir must render plain, got %q", line)
		}
	}
}

// TestExecuteCommandDirMarkerNotInFoldedRow: the folded row summarizes the
// call's first line and nothing below it, so the annotation appears when the
// window is expanded — the placement docs/tui.md → "Tool Result Separator"
// records, pinned so it is not taken
// for a bug and "fixed".
func TestExecuteCommandDirMarkerNotInFoldedRow(t *testing.T) {
	wb, _ := newDirWindow(t, DefaultStyles(), testCallText())

	folded := wb.GetAll(-1, false)
	if !strings.Contains(stripANSI(folded), testCommand) {
		t.Fatalf("the folded row should summarize the command, got %q", stripANSI(folded))
	}
	if strings.Contains(folded, "dir=") {
		t.Errorf("the folded row summarizes the first line only, got %q", stripANSI(folded))
	}
}

// TestDirMarkerOnlyBoldedForExecuteCommand: another tool's content that
// contains a "[dir=…]" line of its own (write_file writing a document, say) is
// not execute_command, so the renderer does not bold it. This is the same guard
// that keeps write_file's "- "/"+ " lines from being colored as a diff
// (TestWindow_WriteFileContentStaysPlain).
func TestDirMarkerOnlyBoldedForExecuteCommand(t *testing.T) {
	styles := DefaultStyles()
	tr := &toolRenderer{name: "write_file", input: "write_file: /tmp/x.md\nsee the note\n" + dirMarker("/tmp")}
	for _, l := range mustLines(t, tr, styles) {
		if strings.Contains(l.Text, "\x1b[") {
			t.Fatalf("write_file content must stay plain, got %q", l.Text)
		}
	}
}

// TestDirMarkerLayoutIsExact pins the cases a rule that matched "[dir=…]" in the
// text would get wrong: a command whose own text contains bracketed "[dir=" is
// never the annotation, and with no workdir there is no annotation at all.
func TestDirMarkerLayoutIsExact(t *testing.T) {
	styles := DefaultStyles()
	handler := &ExecuteCommandHandler{}

	// A command that both contains " [dir=" and ends in "]": a bracket-matching
	// rule cannot tell it from the annotation, and would bold part of the
	// command. Here there is no second line, so nothing is bold.
	text := handler.FormatCall([]byte(`{"command":"echo [dir=/x]"}`))
	wb, idx := newDirWindow(t, styles, text)
	wb.ToggleFold(idx)
	if rendered := wb.GetAll(-1, false); strings.Contains(rendered, "\x1b[1m") {
		t.Errorf("a command that merely looks like a marker must not be bolded: %q", rendered)
	}

	// The same command WITH a workdir: the command's own bracketed text stays
	// plain, and only the annotation's line is bold.
	text = handler.FormatCall([]byte(`{"command":"echo [dir=/x]","workdir":"/tmp"}`))
	wb, idx = newDirWindow(t, styles, text)
	wb.ToggleFold(idx)
	rendered := wb.GetAll(-1, false)
	commandRow, ok := rowFor(rendered, "echo [dir=/x]")
	if !ok {
		t.Fatalf("no row holding the command alone:\n%q", stripANSI(rendered))
	}
	if strings.Contains(commandRow, "\x1b[") {
		t.Errorf("the command's own bracketed text must stay plain, got %q", commandRow)
	}
	annotationRow, _ := rowFor(rendered, dirMarker("/tmp"))
	if want := styles.Body.Bold(true).Render(dirMarker("/tmp")); annotationRow != want {
		t.Errorf("the annotation's row should be bold:\n  got:  %q\n  want: %q", annotationRow, want)
	}
}

// TestDirMarkerBoldWhenCommandIsEmpty: a degenerate call with no command still
// puts the annotation on the second line, where it is drawn bold — the layout
// does not depend on the command being there.
func TestDirMarkerBoldWhenCommandIsEmpty(t *testing.T) {
	styles := DefaultStyles()
	for _, command := range []string{"", "   "} {
		text := (&ExecuteCommandHandler{}).FormatCall([]byte(`{"command":"` + command + `","workdir":"` + testWorkDir + `"}`))
		wb, idx := newDirWindow(t, styles, text)
		wb.ToggleFold(idx)
		if rendered := wb.GetAll(-1, false); !strings.Contains(rendered, styles.Body.Bold(true).Render(dirMarker(testWorkDir))) {
			t.Errorf("command %q: the annotation should still be bold on its own line:\n  got: %q", command, rendered)
		}
	}
}
