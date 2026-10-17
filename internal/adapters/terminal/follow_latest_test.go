package terminal

// What returns the view to the live edge. Two things do, and they are
// different kinds of fact:
//
//   - Sending — a gesture. Enter submits a prompt, and that prompt plus the
//     answer it asks for are exactly what the sender wants to watch. With
//     follow off they would be appended below a viewport left parked in the
//     scrollback, where a silent Enter reads as a prompt that never went.
//   - A turn beginning — a session fact, consumed from the idle → in-progress
//     edge. :continue and :summarize start a turn without submitting anything,
//     and the adapter must not name them to know it: the edge is what the
//     status bar already reports.
//
// Everything else must leave the view alone: an empty prompt box, a `:` command
// that produces no transcript (cancel, save, tool_confirm, …). The state under
// test is the one ↑/Ctrl+U leave behind.
//
// Where a turn starts without a prompt, the session state is the writer's, so
// the frame-level half of that rule — the one-shot edge — is pinned in
// output_session_test.go.

import (
	"bytes"
	"strings"
	"testing"
)

// scrolledBackTranscript is the terminal in that state: a transcript taller
// than the viewport, at home in the middle of it, auto-follow off, and lines
// still below the viewport's bottom edge.
func scrolledBackTranscript(t *testing.T) (Terminal, *outputWriter) {
	t.Helper()
	m := newTestTerminal()
	w, ok := m.out.(*outputWriter)
	if !ok {
		t.Fatalf("fixture writer is %T, want *outputWriter", m.out)
	}
	w.WindowBuffer().AppendOrUpdate(tagAssistantText, "w1", strings.Repeat("answer line\n", 40))
	m.display = m.display.updateContent()

	m.display = m.display.MarkUserScrolled().ScrollUp(5).updateContent()
	if m.display.autoFollow {
		t.Fatal("fixture kept auto-follow on; the scrolled-back view is the state under test")
	}
	if m.display.LinesBelow() == 0 {
		t.Fatal("fixture is already at the live edge; a jump would go undetected")
	}
	m.sessionPipe = bufferWriteCloser{&bytes.Buffer{}}
	return m, w
}

func TestSubmitReturnsTheViewToTheLiveEdge(t *testing.T) {
	m, _ := scrolledBackTranscript(t)
	m.promptBox = m.promptBox.WithValue("what next?")

	m, _ = m.handleSubmit()

	if !m.display.autoFollow {
		t.Error("submitting a prompt did not re-enable auto-follow")
	}
	if got := m.display.LinesBelow(); got != 0 {
		t.Errorf("%d lines still below the viewport after submitting a prompt", got)
	}
}

// TestEmptySubmitLeavesTheViewportPut guards where the jump sits in
// handleSubmit: after the empty-prompt return. Enter on an empty box sends
// nothing, so it must not drag the reader to the bottom either.
func TestEmptySubmitLeavesTheViewportPut(t *testing.T) {
	m, _ := scrolledBackTranscript(t)
	offset := m.display.YOffset()

	m, _ = m.handleSubmit()

	if m.display.autoFollow {
		t.Error("an empty submit re-enabled auto-follow")
	}
	if got := m.display.YOffset(); got != offset {
		t.Errorf("an empty submit moved the viewport: %d -> %d", offset, got)
	}
}

// TestTaskStartReturnsTheViewToTheLiveEdge covers :continue and :summarize:
// they start a turn on the session side alone, so the jump rides the task
// frame's start edge through the tick — no command name reaches the adapter.
func TestTaskStartReturnsTheViewToTheLiveEdge(t *testing.T) {
	m, w := scrolledBackTranscript(t)

	w.handleSystemFrame(`{"type":"task","data":{"in_progress":true,"current_step":1,"max_steps":0}}`)
	m, _ = m.handleTick()

	if !m.display.autoFollow {
		t.Error("a turn starting did not re-enable auto-follow")
	}
	if got := m.display.LinesBelow(); got != 0 {
		t.Errorf("%d lines still below the viewport after a turn started", got)
	}
}
