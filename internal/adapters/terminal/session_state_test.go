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

// TestUpdateMCPProgressResetsListOnNewCycle verifies that a new init cycle
// (after done/idle) clears stale server entries. Regression test: the old
// code checked s.mcpStatus AFTER assigning the incoming status, so the
// reset branch was dead code and stale entries survived into the new cycle.
func TestUpdateMCPProgressResetsListOnNewCycle(t *testing.T) {
	st := newTestSessionState()

	// First cycle: alpha starts but the cycle is interrupted (never connects).
	st.updateMCPProgress("connecting", "alpha")
	st.updateMCPProgress("done", "")
	// No takeMCPDone — mcpStatus stays "done" (display-only); the new-cycle
	// reset below checks prevStatus == "done", so no stale entries survive.

	// Second cycle: beta starts. The stale "alpha" entry must be cleared.
	st.updateMCPProgress("connecting", "beta")

	snap := st.snapshotStatus()
	if len(snap.MCPServers) != 1 || snap.MCPServers[0] != "beta" {
		t.Fatalf("MCPServers at start of new cycle = %v, want [beta] (stale entries must be reset)", snap.MCPServers)
	}
}
