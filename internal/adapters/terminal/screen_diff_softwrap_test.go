package terminal

import (
	"encoding/binary"
	"fmt"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/tlv"
)

func tlvFrame(tag, id, payload string) []byte {
	value := "\x00" + id + "\x00" + payload
	b := make([]byte, 6+len(value))
	copy(b[0:2], tag)
	binary.BigEndian.PutUint32(b[2:6], uint32(len(value)))
	copy(b[6:], value)
	return b
}

// TestRealAppWrappedLineContinuity drives the real Terminal with a tool
// AF frame whose parameter is long enough to wrap a word across two
// terminal rows, then a streaming update that changes the line. It
// verifies (1) the frame content keeps the wrapped line as ONE continuous
// base row, and (2) the Screen diff repaints it whole (one continuous
// write at the line's start row, no per-row CUP to the continuation rows)
// so the terminal's logical line — and the copy — stay intact.
func TestRealAppWrappedLineContinuity(t *testing.T) {
	const width = 60

	out := NewTerminalOutput(DefaultStyles())
	terminal := NewTerminalWithTheme(out, nopWriteCloser{}, nil, width, 24, theme.DefaultTheme(), nil, "theme-dark")
	terminal.out.SetWindowWidth(width)

	writeTool := func(command string, expand bool) string {
		payload := fmt.Sprintf(`{"id":"t1","name":"execute_command","input":{"command":%q}}`, command)
		terminal.out.Write(append(tlvFrame(tlv.TagAssistantF, "t1", payload), tlvFrame(tlv.TagUserF, "t1", `{"id":"t1","content":[],"is_error":false}`)...))
		terminal.out.FlushPendingDeltas()
		if expand {
			if wb := terminal.out.WindowBuffer(); wb.WindowCount() > 0 {
				wb.ToggleFold(0) // expand: window starts folded
			}
		}
		var cmd Cmd
		terminal, cmd = terminal.handleDisplayRefresh()
		_ = cmd
		return terminal.View().Content
	}

	// A command long enough that the wrap splits a word across 2 rows.
	cmd := "cd /home/wallace/playground/alayacore && go test ./internal/adapters/terminal/ -run TestGenericHandler -v && gofmt -l internal/adapters/terminal/"
	frame1 := writeTool(cmd+" && echo short", true)
	frame2 := writeTool(cmd+" && echo done && staticcheck ./internal/adapters/terminal/...", false)

	// (1) In both frames the wrapped parameter must be ONE base row whose
	// terminal span > 1 — the fragment joins the wrapped rows without '\n'.
	for name, f := range map[string]string{"frame1": frame1, "frame2": frame2} {
		found := false
		for _, r := range positionedRows(f, width) {
			if r.base && strings.Contains(r.frameRow.text, "internal/adapters/terminal/") {
				found = true
				if r.terminalRows <= 1 {
					t.Errorf("%s: wrapped line is a single-terminal-row base row (len %d), want >1", name, len(r.frameRow.text))
				}
			}
		}
		if !found {
			t.Errorf("%s: wrapped line not found as a base row", name)
		}
	}

	// (2) The diff must repaint the wrapped line whole: one CUP to its
	// start row followed by the full line text — and NO CUP to any of its
	// continuation rows (that would split the terminal's logical line).
	diff := string(diffFrameRows(frame1, frame2, width))
	var line positionedRow
	for _, r := range positionedRows(frame2, width) {
		if r.base && strings.Contains(r.frameRow.text, "internal/adapters/terminal/") {
			line = r
			break
		}
	}
	if line.terminalRows <= 1 {
		t.Fatalf("test setup: wrapped line must span >1 terminal rows")
	}
	start := ansiCursorPosition(1, line.frameRow.row+1)
	if !strings.HasPrefix(diff, start) {
		t.Fatalf("diff should start with CUP to the wrapped line's start row %d, got %q", line.frameRow.row+1, diff[:min(len(diff), 40)])
	}
	if !strings.HasPrefix(diff, start+line.frameRow.text) {
		t.Errorf("diff must write the full wrapped line immediately after the start CUP (no per-row CUP between segments): %q", diff)
	}
	for r := line.frameRow.row + 1; r < line.frameRow.row+line.terminalRows; r++ {
		if strings.Contains(diff, ansiCursorPosition(1, r+1)) {
			t.Errorf("diff must not CUP-write wrapped continuation row %d separately (splits the logical line): %q", r+1, diff)
		}
	}
}

// ansiCursorPosition builds the escape sequence for a 1-based (col,row).
func ansiCursorPosition(col, row int) string {
	return fmt.Sprintf("\x1b[%d;%dH", row, col)
}
