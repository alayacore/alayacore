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

// TestApplyResultIsTheSingleInterpretation pins that a result means the same
// thing however it arrives: folding a ModelSelectedMsg emits the model_set
// command, which is what Terminal.Update's message case does too (both call
// applyResult). A divergence would be a fact with two meanings.
func TestApplyResultIsTheSingleInterpretation(t *testing.T) {
	m := newTestTerminal()
	if _, cmd := m.applyResult(ModelSelectedMsg{ID: 7}); cmd == nil {
		t.Fatal("a model selection must emit the model_set command")
	}
	if _, cmd := m.applyResult(ReloadModelsMsg{}); cmd == nil {
		t.Fatal("a reload must emit the model_load command")
	}
}
