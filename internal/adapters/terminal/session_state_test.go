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
	if !st.takeMCPDone() {
		t.Fatal("takeMCPDone should report done")
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
	st.takeMCPDone() // Terminal consumes "done" → status back to ""

	// Second cycle: beta starts. The stale "alpha" entry must be cleared.
	st.updateMCPProgress("connecting", "beta")

	snap := st.snapshotStatus()
	if len(snap.MCPServers) != 1 || snap.MCPServers[0] != "beta" {
		t.Fatalf("MCPServers at start of new cycle = %v, want [beta] (stale entries must be reset)", snap.MCPServers)
	}
}
