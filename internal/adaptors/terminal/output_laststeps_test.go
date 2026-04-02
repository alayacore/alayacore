package terminal

import (
	"encoding/json"
	"testing"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
)

func TestLastMaxStepsPreservation(t *testing.T) {
	w := NewTerminalOutput(DefaultStyles())

	// Simulate a task in progress with max steps = 10, current step = 5
	systemInfoInProgress := agentpkg.SystemInfo{
		InProgress:  true,
		MaxSteps:    10,
		CurrentStep: 5,
	}
	data := marshalSystemInfo(t, systemInfoInProgress)
	w.handleSystemTag(string(data))

	// Verify in-progress state
	snap := w.SnapshotStatus()
	if !snap.InProgress {
		t.Error("Expected in-progress to be true")
	}
	if snap.MaxSteps != 10 {
		t.Errorf("Expected max steps 10, got %d", snap.MaxSteps)
	}
	if snap.LastCurrentStep != 0 || snap.LastMaxSteps != 0 {
		t.Errorf("Expected last step info (0, 0) (not set yet), got (%d, %d)", snap.LastCurrentStep, snap.LastMaxSteps)
	}

	// Simulate task completion (transition from in-progress to done)
	systemInfoCompleted := agentpkg.SystemInfo{
		InProgress:  false,
		MaxSteps:    10,
		CurrentStep: 0,
	}
	data = marshalSystemInfo(t, systemInfoCompleted)
	w.handleSystemTag(string(data))

	// Verify completed state
	snap = w.SnapshotStatus()
	if snap.InProgress {
		t.Error("Expected in-progress to be false")
	}
	if snap.MaxSteps != 10 {
		t.Errorf("Expected max steps 10, got %d", snap.MaxSteps)
	}
	if snap.LastCurrentStep != 5 || snap.LastMaxSteps != 10 {
		t.Errorf("Expected last step info (5, 10) (preserved), got (%d, %d)", snap.LastCurrentStep, snap.LastMaxSteps)
	}

	// Simulate a new task starting with different max steps
	systemInfoNewTask := agentpkg.SystemInfo{
		InProgress:  true,
		MaxSteps:    20,
		CurrentStep: 1,
	}
	data = marshalSystemInfo(t, systemInfoNewTask)
	w.handleSystemTag(string(data))

	// Verify new task state - last step info should be reset when new task starts
	snap = w.SnapshotStatus()
	if !snap.InProgress {
		t.Error("Expected in-progress to be true")
	}
	if snap.MaxSteps != 20 {
		t.Errorf("Expected max steps 20, got %d", snap.MaxSteps)
	}
	if snap.LastCurrentStep != 0 || snap.LastMaxSteps != 0 {
		t.Errorf("Expected last step info (0, 0) (reset for new task), got (%d, %d)", snap.LastCurrentStep, snap.LastMaxSteps)
	}
}

func TestLastMaxStepsZeroOnStart(t *testing.T) {
	w := NewTerminalOutput(DefaultStyles())

	// Initial state - no last step info
	snap := w.SnapshotStatus()
	if snap.LastCurrentStep != 0 || snap.LastMaxSteps != 0 {
		t.Errorf("Expected last step info (0, 0) initially, got (%d, %d)", snap.LastCurrentStep, snap.LastMaxSteps)
	}

	// First task starts - last step info should still be (0, 0)
	systemInfoFirstTask := agentpkg.SystemInfo{
		InProgress:  true,
		MaxSteps:    5,
		CurrentStep: 1,
	}
	data := marshalSystemInfo(t, systemInfoFirstTask)
	w.handleSystemTag(string(data))

	snap = w.SnapshotStatus()
	if snap.LastCurrentStep != 0 || snap.LastMaxSteps != 0 {
		t.Errorf("Expected last step info (0, 0) (task not completed yet), got (%d, %d)", snap.LastCurrentStep, snap.LastMaxSteps)
	}
}

func TestLastMaxStepsNotUpdatedWithoutTransition(t *testing.T) {
	w := NewTerminalOutput(DefaultStyles())

	// Send multiple in-progress updates
	for i := 1; i <= 3; i++ {
		systemInfo := agentpkg.SystemInfo{
			InProgress:  true,
			MaxSteps:    15,
			CurrentStep: i,
		}
		data := marshalSystemInfo(t, systemInfo)
		w.handleSystemTag(string(data))
	}

	// Last step info should still be (0, 0) (no completion transition yet)
	snap := w.SnapshotStatus()
	if snap.LastCurrentStep != 0 || snap.LastMaxSteps != 0 {
		t.Errorf("Expected last step info (0, 0) (no completion), got (%d, %d)", snap.LastCurrentStep, snap.LastMaxSteps)
	}

	// Now complete the task
	systemInfo := agentpkg.SystemInfo{
		InProgress:  false,
		MaxSteps:    15,
		CurrentStep: 0,
	}
	data := marshalSystemInfo(t, systemInfo)
	w.handleSystemTag(string(data))

	// Now last step info should be set to the last current step before completion
	snap = w.SnapshotStatus()
	if snap.LastCurrentStep != 3 || snap.LastMaxSteps != 15 {
		t.Errorf("Expected last step info (3, 15), got (%d, %d)", snap.LastCurrentStep, snap.LastMaxSteps)
	}
}

// Helper function to marshal SystemInfo to JSON
func marshalSystemInfo(t *testing.T, info agentpkg.SystemInfo) []byte {
	t.Helper()
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("Failed to marshal SystemInfo: %v", err)
	}
	return data
}
