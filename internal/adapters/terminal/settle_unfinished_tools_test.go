package terminal

// Tests for the task-completion safety net: when the task completion
// frame arrives (in_progress true→false), tool windows that never
// received a UF result frame — left behind by abnormal paths (canceled
// confirmation, malformed/dropped frames) — are settled to the error
// state (✗) instead of spinning forever.

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// TestUpdateTaskCompletionEdge locks the edge detection that drives the
// settlement: only the in_progress true→false transition reports the
// completion edge; start edges and no-op updates do not.
func TestUpdateTaskCompletionEdge(t *testing.T) {
	st := &sessionState{mu: &sync.Mutex{}}

	// Start edge: false→true — not a completion.
	if st.updateTask(true, 1, 5, 0) {
		t.Error("start edge must not report completion")
	}
	// In-progress update: true→true — not a completion.
	if st.updateTask(true, 2, 5, 10) {
		t.Error("in-progress update must not report completion")
	}
	// Completion edge: true→false.
	if !st.updateTask(false, 0, 5, 10) {
		t.Error("completion edge must report completion")
	}
	// Idle update: false→false — not a completion.
	if st.updateTask(false, 0, 5, 0) {
		t.Error("idle update must not report completion")
	}
}

// TestSettleUnfinishedTools: only tool windows still in the running
// states (args streaming / executing) are settled; finished tools, text
// windows, and windows with partial output keep their state/content.
func TestSettleUnfinishedTools(t *testing.T) {
	wb := NewWindowBuffer(60, DefaultStyles())
	// Executing tool with no output yet — the straggler case.
	wb.HandleToolInputEvent(protocol.ToolInputData{
		ID:    "pending",
		Name:  "execute_command",
		Input: json.RawMessage("sleep 60"),
	}, 0)
	// Executing tool with partial output — output must be preserved.
	wb.HandleToolInputEvent(protocol.ToolInputData{
		ID:    "partial",
		Name:  "search_content",
		Input: json.RawMessage("x"),
	}, 0)
	wb.HandleToolOutputDelta("partial", "50%", 0)
	// Args-streaming window (never completed its AF input).
	wb.HandleToolInputDelta("streaming", "write_file", `{"path":"/tmp/`, 0)
	// Finished tools and a text window — untouched.
	wb.HandleToolInputEvent(protocol.ToolInputData{
		ID:    "done",
		Name:  "read_file",
		Input: json.RawMessage("x"),
	}, 0)
	wb.HandleToolOutput("done", "content", false, 0)
	wb.HandleToolInputEvent(protocol.ToolInputData{
		ID:    "failed",
		Name:  "execute_command",
		Input: json.RawMessage("boom"),
	}, 0)
	wb.HandleToolOutput("failed", "err", true, 0)
	wb.AppendOrUpdate(tlv.TagAssistantT, "w1", "hello")
	wb.GetAll(-1, false) // populate borders so invalidate is observable

	settled := wb.SettleUnfinishedTools()
	if settled != 3 {
		t.Fatalf("settled = %d, want 3 (pending + partial + streaming)", settled)
	}
	if !wb.IsDirty() {
		t.Error("settling must mark the buffer dirty")
	}

	tr := func(id string) *toolRenderer {
		t.Helper()
		idx, ok := wb.LookupID(id)
		if !ok {
			t.Fatalf("window %q missing", id)
		}
		r, ok := wb.WindowAt(idx).renderer.(*toolRenderer)
		if !ok {
			t.Fatalf("window %q is not a tool window", id)
		}
		return r
	}

	if got := tr("pending").status; got != ToolStatusError {
		t.Errorf("pending status = %v, want ToolStatusError", got)
	}
	if got := tr("pending").output; got != "tool did not complete before the task ended" {
		t.Errorf("pending output = %q, want the settlement explanation", got)
	}
	if got := tr("partial").status; got != ToolStatusError {
		t.Errorf("partial status = %v, want ToolStatusError", got)
	}
	if got := tr("partial").output; got != "50%" {
		t.Errorf("partial output must be preserved, got %q", got)
	}
	if got := tr("streaming").status; got != ToolStatusError {
		t.Errorf("streaming status = %v, want ToolStatusError", got)
	}
	if got := tr("done").status; got != ToolStatusSuccess {
		t.Errorf("done status = %v, want ToolStatusSuccess (untouched)", got)
	}
	if got := tr("failed").status; got != ToolStatusError {
		t.Errorf("failed status = %v, want ToolStatusError (already settled)", got)
	}

	// Idle after settling: nothing left to settle.
	if again := wb.SettleUnfinishedTools(); again != 0 {
		t.Errorf("second settle returned %d, want 0", again)
	}
}

// TestTaskCompletionSettlesPendingTools is the end-to-end integration:
// a task frame with in_progress:false settles a tool window left pending,
// and the rendered header shows ✗ instead of a spinner.
func TestTaskCompletionSettlesPendingTools(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	out.WindowBuffer().HandleToolInputEvent(protocol.ToolInputData{
		ID:    "t1",
		Name:  "execute_command",
		Input: json.RawMessage("sleep 60"),
	}, 0)

	// Task runs, then completes — no UF frame ever arrives for t1.
	out.handleSystemMsg(`{"type":"task","data":{"in_progress":true,"current_step":1,"max_steps":1,"context":0}}`)
	out.handleSystemMsg(`{"type":"task","data":{"in_progress":false,"current_step":0,"max_steps":1,"context":0}}`)

	rendered := stripANSI(out.WindowBuffer().GetAll(-1, false))
	if !containsToolLabel(rendered, "TOOL CALL ✗") {
		t.Errorf("pending tool must settle to TOOL CALL ✗, got %q", rendered)
	}
	if containsToolLabel(rendered, "⠋") {
		t.Errorf("pending tool must no longer show a spinner frame, got %q", rendered)
	}
}

// TestTaskCompletionDoesNotDisturbSettledTools: the safety net must only
// touch genuine stragglers — tools that already settled via their UF
// frames (✓ or ✗) are untouched when the task completion frame arrives.
func TestTaskCompletionDoesNotDisturbSettledTools(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	wb := out.WindowBuffer()
	wb.HandleToolInputEvent(protocol.ToolInputData{
		ID:    "ok",
		Name:  "read_file",
		Input: json.RawMessage("x"),
	}, 0)
	wb.HandleToolOutput("ok", "content", false, 0)
	wb.HandleToolInputEvent(protocol.ToolInputData{
		ID:    "bad",
		Name:  "execute_command",
		Input: json.RawMessage("boom"),
	}, 0)
	wb.HandleToolOutput("bad", "err", true, 0)

	out.handleSystemMsg(`{"type":"task","data":{"in_progress":true,"current_step":1,"max_steps":1,"context":0}}`)
	out.handleSystemMsg(`{"type":"task","data":{"in_progress":false,"current_step":0,"max_steps":1,"context":0}}`)

	rendered := stripANSI(wb.GetAll(-1, false))
	if !strings.Contains(rendered, "TOOL CALL ✓") {
		t.Errorf("successful tool must stay ✓ after task completion, got %q", rendered)
	}
	if !strings.Contains(rendered, "TOOL CALL ✗") {
		t.Errorf("failed tool must stay ✗ after task completion, got %q", rendered)
	}
	if strings.Contains(rendered, "did not complete") {
		t.Errorf("settled tools must not receive the settlement explanation, got %q", rendered)
	}
}

// containsToolLabel reports whether the plain content contains the given
// label sequence (e.g. "TOOL CALL ✗").
func containsToolLabel(content, label string) bool {
	return strings.Contains(content, label)
}
