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
	w.handleSystemMsg(`{"type":"session","data":{"state":"ready"}}`)

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

	w.handleSystemMsg(`{"type":"session","data":{"state":"initializing"}}`)
	w.handleSystemMsg(`{"type":"session","data":{}}`)

	if w.ConsumeSessionReady() {
		t.Fatal("ConsumeSessionReady should be false for non-ready states")
	}
}

// TestMCPDoneDoesNotCloseOverlay is a regression test: the MCP progress
// "done" frame is display-only (it also arrives for canceled/aborted init
// and never for the no-MCP case) — overlay closure must be driven solely
// by the session-ready frame.
func TestMCPDoneDoesNotCloseOverlay(t *testing.T) {
	w := NewTerminalOutput(DefaultStyles())

	w.handleSystemMsg(`{"type":"mcp","data":{"status":"done"}}`)

	if w.ConsumeSessionReady() {
		t.Fatal("ConsumeSessionReady should NOT be triggered by the mcp done frame")
	}

	// The real ready frame still works afterwards.
	w.handleSystemMsg(`{"type":"session","data":{"state":"ready"}}`)
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
	w.handleSystemMsg(`{"type":"mcp","data":{"status":"auth_required","server":"github","url":"https://example.com/auth"}}`)
	w.handleSystemMsg(`{"type":"session","data":{"state":"ready"}}`)

	if !w.ConsumeSessionReady() {
		t.Fatal("ConsumeSessionReady should report ready")
	}
	if _, _, ok := w.GetPendingMCPAuth(); ok {
		t.Fatal("stale MCP auth confirmation should be cleared on session ready")
	}
}
