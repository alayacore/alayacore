package terminal

// T5: a component reports facts as Result values, and the owner folds them in
// the same Update. These pin the two halves that the old sync-cmd() hack
// blurred — a component hands back a value, and folding one changes parent state
// before the frame is drawn.

import "testing"

// TestPromptInputCtrlOReportsEditorRequest pins that Ctrl+O leaves the prompt as
// a Result value, not an opaque func() the owner would have to call to learn
// what happened.
func TestPromptInputCtrlOReportsEditorRequest(t *testing.T) {
	pi := NewPromptInput(DefaultStyles()).WithValue("hello")
	_, results := pi.Update(KeyPressMsg(Key{Code: 'o', Mod: ModCtrl}))
	if len(results) != 1 {
		t.Fatalf("Ctrl+O produced %d results, want 1: %v", len(results), results)
	}
	req, ok := results[0].(openEditorForPromptMsg)
	if !ok {
		t.Fatalf("result = %T, want openEditorForPromptMsg", results[0])
	}
	if req.content != "hello" {
		t.Errorf("editor request carries %q, want %q", req.content, "hello")
	}
}

// TestFoldConfirmChangesStateInSameUpdate pins that folding a component result
// mutates the parent now, not on a later message round trip: confirming the quit
// dialog sets the quitting state within the same call that folded it.
func TestFoldConfirmChangesStateInSameUpdate(t *testing.T) {
	m := newTestTerminal().openConfirmQuit()
	after, cmd := m.handleOverlayConfirm(KeyPressMsg{Code: 'y'})
	if !after.quitting {
		t.Fatal("confirming quit did not set the quitting state in the same fold")
	}
	if cmd == nil {
		t.Fatal("confirming quit must still yield the I/O it implies (close + quit)")
	}
}

// TestFoldResultsAppliesEachOnceInOrder pins that a result list is folded
// element by element: each applies, and later ones see the state earlier ones
// left. This is the invariant the old synchronous cmd() path risked — a result
// that was both folded and re-dispatched as a message would apply twice.
func TestFoldResultsAppliesEachOnceInOrder(t *testing.T) {
	m := newTestTerminal()
	after, _ := m.foldResults([]Result{
		AttachmentSelectedMsg{Path: "/tmp/a.txt"},
		AttachmentSelectedMsg{Path: "/tmp/b.txt"},
	})
	if got := len(after.pendingAttachments); got != 2 {
		t.Fatalf("pendingAttachments = %d, want 2: each result must apply exactly once", got)
	}

	// Order matters: the last focus request is the value that stands.
	m2 := newTestTerminal()
	after2, _ := m2.foldResults([]Result{
		focusInputWithValueMsg{value: ":first "},
		focusInputWithValueMsg{value: ":second "},
	})
	if got := after2.input.Value(); got != ":second " {
		t.Fatalf("input = %q, want %q: results must fold in order", got, ":second ")
	}
}

// TestApplyResultIsTheSingleInterpretation pins that a selection's meaning lives
// in applyResult: folding a model or reload result emits the command the session
// expects. Every dispatcher folds through here, so the meaning cannot drift.
func TestApplyResultIsTheSingleInterpretation(t *testing.T) {
	m := newTestTerminal()
	if _, cmd := m.applyResult(ModelSelectedMsg{ID: 7}); cmd == nil {
		t.Fatal("a model selection must emit the model_set command")
	}
	if _, cmd := m.applyResult(ReloadModelsMsg{}); cmd == nil {
		t.Fatal("a reload must emit the model_load command")
	}
}
