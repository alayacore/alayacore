package terminal

import (
	"testing"
)

// TestLastStepsCapturedOnCompletion verifies the completion edge: the last
// known step values are snapshotted when a task transitions to done, before
// the completion broadcast overwrites current_step with 0.
func TestLastStepsCapturedOnCompletion(t *testing.T) {
	w := NewTerminalOutput(DefaultStyles())

	// Task in progress — step broadcasts carry the live step count.
	w.handleSystemMsg(`{"type":"task","data":{"in_progress":true,"current_step":5,"max_steps":10,"context":0,"context_limit":0}}`)
	// Completion broadcast arrives with current_step zeroed.
	w.handleSystemMsg(`{"type":"task","data":{"in_progress":false,"current_step":0,"max_steps":10,"context":0,"context_limit":0}}`)

	snap := w.SnapshotStatus()
	if snap.LastCurrentStep != 5 || snap.LastMaxSteps != 10 {
		t.Errorf("Expected last step info (5, 10), got (%d, %d)", snap.LastCurrentStep, snap.LastMaxSteps)
	}
}

// TestLastStepsResetOnNewTask verifies the start edge: a new task clears the
// previous run's summary so the status bar shows live progress again.
func TestLastStepsResetOnNewTask(t *testing.T) {
	w := NewTerminalOutput(DefaultStyles())

	w.handleSystemMsg(`{"type":"task","data":{"in_progress":true,"current_step":5,"max_steps":10,"context":0,"context_limit":0}}`)
	w.handleSystemMsg(`{"type":"task","data":{"in_progress":false,"current_step":0,"max_steps":10,"context":0,"context_limit":0}}`)

	// New task starts — last step info must be reset.
	w.handleSystemMsg(`{"type":"task","data":{"in_progress":true,"current_step":1,"max_steps":20,"context":0,"context_limit":0}}`)

	snap := w.SnapshotStatus()
	if snap.LastCurrentStep != 0 || snap.LastMaxSteps != 0 {
		t.Errorf("Expected last step info (0, 0) after new task start, got (%d, %d)",
			snap.LastCurrentStep, snap.LastMaxSteps)
	}
}

// TestLastStepsNotCapturedWithoutRun verifies a startup broadcast (no task
// ever ran) does not trigger the completion edge.
func TestLastStepsNotCapturedWithoutRun(t *testing.T) {
	w := NewTerminalOutput(DefaultStyles())

	// Startup broadcast: not in progress, current_step zero.
	w.handleSystemMsg(`{"type":"task","data":{"in_progress":false,"current_step":0,"max_steps":10,"context":0,"context_limit":0}}`)

	snap := w.SnapshotStatus()
	if snap.LastCurrentStep != 0 || snap.LastMaxSteps != 0 {
		t.Errorf("Expected last step info (0, 0) without a run, got (%d, %d)",
			snap.LastCurrentStep, snap.LastMaxSteps)
	}
}

// TestLastStepsZeroWhenTaskFailsBeforeFirstStep verifies an instant failure
// (no step broadcasts before completion) leaves the last-step summary empty
// so the status bar does not show a meaningless "0/N".
func TestLastStepsZeroWhenTaskFailsBeforeFirstStep(t *testing.T) {
	w := NewTerminalOutput(DefaultStyles())

	w.handleSystemMsg(`{"type":"task","data":{"in_progress":true,"current_step":0,"max_steps":10,"context":0,"context_limit":0}}`)
	w.handleSystemMsg(`{"type":"task","data":{"in_progress":false,"current_step":0,"max_steps":10,"context":0,"context_limit":0}}`)

	snap := w.SnapshotStatus()
	if snap.LastCurrentStep != 0 {
		t.Errorf("Expected LastCurrentStep 0 for a task that never reached step 1, got %d", snap.LastCurrentStep)
	}
}
