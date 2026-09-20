package terminal

import (
	"sync"
	"testing"
)

// newTestSessionState returns a sessionState with a fresh mutex.
func newTestSessionState() *sessionState {
	return &sessionState{mu: &sync.Mutex{}}
}

// TestUpdateMCPProgressTracksServersAcrossCycle verifies the connecting/
// connected lifecycle accumulates and drains the server list correctly
// within a single init cycle.
func TestUpdateMCPProgressTracksServersAcrossCycle(t *testing.T) {
	st := newTestSessionState()

	st.updateMCPProgress("connecting", "alpha")
	st.updateMCPProgress("connecting", "beta") // concurrent init — both listed
	st.updateMCPProgress("connected", "alpha")

	snap := st.snapshotStatus()
	if len(snap.MCPServers) != 1 || snap.MCPServers[0] != "beta" {
		t.Fatalf("MCPServers after alpha connected = %v, want [beta]", snap.MCPServers)
	}

	st.updateMCPProgress("connected", "beta")
	st.updateMCPProgress("done", "")

	snap = st.snapshotStatus()
	if len(snap.MCPServers) != 0 {
		t.Fatalf("MCPServers after done = %v, want empty", snap.MCPServers)
	}

	// The init overlay is closed by the authoritative session-ready
	// signal, not by the MCP "done" progress status.
	st.markSessionReady()
	if !st.takeSessionReady() {
		t.Fatal("takeSessionReady should report ready after markSessionReady")
	}
}

// TestSessionReadyOneShot verifies the session-ready flag is consumed
// exactly once — the Terminal closes the init overlay a single time even
// if the tick handler runs again before any new frame arrives.
func TestSessionReadyOneShot(t *testing.T) {
	st := newTestSessionState()

	if st.takeSessionReady() {
		t.Fatal("takeSessionReady should be false before markSessionReady")
	}

	st.markSessionReady()
	if !st.takeSessionReady() {
		t.Fatal("takeSessionReady should report ready once")
	}
	if st.takeSessionReady() {
		t.Fatal("takeSessionReady should be one-shot (false after consumption)")
	}
}

// TestSessionClosedOneShot verifies the terminal frame is consumed exactly
// once — the Terminal leaves the event loop a single time.
func TestSessionClosedOneShot(t *testing.T) {
	st := newTestSessionState()

	if st.takeSessionClosed() {
		t.Fatal("takeSessionClosed should be false before markSessionClosed")
	}

	st.markSessionClosed()
	if !st.takeSessionClosed() {
		t.Fatal("takeSessionClosed should report the terminal frame once")
	}
	if st.takeSessionClosed() {
		t.Fatal("takeSessionClosed should be one-shot (false after consumption)")
	}
}

// TestSessionClosedDoesNotMarkReady pins that the terminal frame is not a
// readiness signal: a session that ends before initialization completed has
// closed without having been ready, and the init overlay must not be closed
// by it.
func TestSessionClosedDoesNotMarkReady(t *testing.T) {
	st := newTestSessionState()

	st.markSessionClosed()

	if st.takeSessionReady() {
		t.Fatal("the closed frame must not report initialization complete")
	}
}

// TestUpdateMCPProgressResetsListOnNewCycle verifies that a new init cycle
// (after done/idle) clears stale server entries. Regression test: the old
// code checked s.mcpStatus AFTER assigning the incoming status, so the
// reset branch was dead code and stale entries survived into the new cycle.
func TestUpdateMCPProgressResetsListOnNewCycle(t *testing.T) {
	st := newTestSessionState()

	// First cycle: alpha starts but the cycle is interrupted (never connects).
	st.updateMCPProgress("connecting", "alpha")
	st.updateMCPProgress("done", "")
	// mcpStatus stays "done" (display-only terminal state); the new-cycle
	// reset below checks prevStatus == "done", so no stale entries survive.

	// Second cycle: beta starts. The stale "alpha" entry must be cleared.
	st.updateMCPProgress("connecting", "beta")

	snap := st.snapshotStatus()
	if len(snap.MCPServers) != 1 || snap.MCPServers[0] != "beta" {
		t.Fatalf("MCPServers at start of new cycle = %v, want [beta] (stale entries must be reset)", snap.MCPServers)
	}
}
