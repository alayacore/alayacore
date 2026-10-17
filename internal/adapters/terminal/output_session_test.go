package terminal

import (
	"testing"
)

// TestSessionReadyFrameClosesInitOverlay verifies the SM "session" ready
// frame drives overlay closure through ConsumeSessionReady — the
// authoritative signal — and is one-shot.
func TestSessionReadyFrameClosesInitOverlay(t *testing.T) {
	w := NewTerminalOutput(DefaultStyles())

	// No ready frame yet.
	if w.ConsumeSessionReady() {
		t.Fatal("ConsumeSessionReady should be false before the session-ready frame")
	}

	// Session-ready frame arrives.
	w.handleSystemFrame(`{"type":"session","data":{"state":"ready"}}`)

	if !w.ConsumeSessionReady() {
		t.Fatal("ConsumeSessionReady should be true after the session-ready frame")
	}
	// One-shot — a second consume without a new frame reports false.
	if w.ConsumeSessionReady() {
		t.Fatal("ConsumeSessionReady should be one-shot (false after consumption)")
	}
}

// TestSessionReadyIgnoresOtherStates verifies only state "ready" marks
// the session ready; unknown/in-progress states are ignored.
func TestSessionReadyIgnoresOtherStates(t *testing.T) {
	w := NewTerminalOutput(DefaultStyles())

	w.handleSystemFrame(`{"type":"session","data":{"state":"initializing"}}`)
	w.handleSystemFrame(`{"type":"session","data":{}}`)

	if w.ConsumeSessionReady() {
		t.Fatal("ConsumeSessionReady should be false for non-ready states")
	}
}

// TestSessionClosedFrameIsTerminal verifies the SM "session" closed frame is
// picked up by ConsumeSessionClosed — the session's own announcement that it
// is over — and is one-shot. The ready frame is a different state and must
// not set it.
func TestSessionClosedFrameIsTerminal(t *testing.T) {
	w := NewTerminalOutput(DefaultStyles())

	if w.ConsumeSessionClosed() {
		t.Fatal("ConsumeSessionClosed should be false before the terminal frame")
	}

	w.handleSystemFrame(`{"type":"session","data":{"state":"ready"}}`)
	if w.ConsumeSessionClosed() {
		t.Fatal("the ready frame must not report the session as over")
	}

	w.handleSystemFrame(`{"type":"session","data":{"state":"closed"}}`)
	if !w.ConsumeSessionClosed() {
		t.Fatal("ConsumeSessionClosed should be true after the terminal frame")
	}
	if w.ConsumeSessionClosed() {
		t.Fatal("ConsumeSessionClosed should be one-shot (false after consumption)")
	}
}

// TestMCPDoneDoesNotCloseOverlay is a regression test: the MCP progress
// "done" frame is display-only (it also arrives for canceled/aborted init
// and never for the no-MCP case) — overlay closure must be driven solely
// by the session-ready frame.
func TestMCPDoneDoesNotCloseOverlay(t *testing.T) {
	w := NewTerminalOutput(DefaultStyles())

	w.handleSystemFrame(`{"type":"mcp","data":{"status":"done"}}`)

	if w.ConsumeSessionReady() {
		t.Fatal("ConsumeSessionReady should NOT be triggered by the mcp done frame")
	}

	// The real ready frame still works afterwards.
	w.handleSystemFrame(`{"type":"session","data":{"state":"ready"}}`)
	if !w.ConsumeSessionReady() {
		t.Fatal("ConsumeSessionReady should be true after the session-ready frame")
	}
}

// TestConsumeSessionReadyClearsStaleAuths verifies that consuming the
// session-ready frame also discards MCP auth confirmations queued before
// the cancellation took effect (stale dialogs must not outlive init).
func TestConsumeSessionReadyClearsStaleAuths(t *testing.T) {
	w := NewTerminalOutput(DefaultStyles())

	// An auth prompt was pending when init settled.
	w.handleSystemFrame(`{"type":"mcp","data":{"status":"auth_required","server":"github","url":"https://example.com/auth"}}`)
	w.handleSystemFrame(`{"type":"session","data":{"state":"ready"}}`)

	if !w.ConsumeSessionReady() {
		t.Fatal("ConsumeSessionReady should report ready")
	}
	if _, _, ok := w.GetPendingMCPAuth(); ok {
		t.Fatal("stale MCP auth confirmation should be cleared on session ready")
	}
}

// TestTaskStartEdgeIsOneShotPerTurn covers ConsumeTaskStarted — what the tick
// uses to return the view to the live edge. A task beginning is an edge, not a
// state: the frames that follow within the same run must not report it again,
// or a reader who scrolled back mid-task would be dragged to the bottom on
// every step. :continue and :summarize cross the same edge as a prompt, which
// is why no command name appears in the adapter.
func TestTaskStartEdgeIsOneShotPerTurn(t *testing.T) {
	w := NewTerminalOutput(DefaultStyles())

	if w.ConsumeTaskStarted() {
		t.Fatal("ConsumeTaskStarted should be false before any task frame")
	}

	w.handleSystemFrame(`{"type":"task","data":{"in_progress":true,"current_step":1}}`)
	if !w.ConsumeTaskStarted() {
		t.Fatal("the start of a task did not report a start edge")
	}
	if w.ConsumeTaskStarted() {
		t.Fatal("ConsumeTaskStarted should be one-shot (false after consumption)")
	}

	// A later step of the same run is not a new task.
	w.handleSystemFrame(`{"type":"task","data":{"in_progress":true,"current_step":2}}`)
	if w.ConsumeTaskStarted() {
		t.Error("a running task's progress frame reported a new start")
	}

	// The next run is.
	w.handleSystemFrame(`{"type":"task","data":{"in_progress":false}}`)
	w.handleSystemFrame(`{"type":"task","data":{"in_progress":true,"current_step":1}}`)
	if !w.ConsumeTaskStarted() {
		t.Error("the next task did not report a start edge")
	}
}
