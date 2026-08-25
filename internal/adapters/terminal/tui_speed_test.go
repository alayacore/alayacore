package terminal

// Tests for provider speed display: statusSpeedSegment rendering, segment
// ordering (speed right after context), and the end-to-end pipeline
// (SM "task" frame → sessionState → status bar).

import (
	"strings"
	"sync"
	"testing"
)

func TestStatusSpeedSegment(t *testing.T) {
	tests := []struct {
		name    string
		stepTPS float64
		ttftMS  int64
		want    string
	}{
		{"no data", 0, 0, ""},
		{"step speed only", 12.5, 0, "12.5 tok/s"},
		{"step speed with ttft", 12.5, 1200, "12.5 tok/s · ttft 1.2s"},
		{"kept after completion", 12.5, 1200, "12.5 tok/s · ttft 1.2s"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := statusSpeedSegment(tc.stepTPS, tc.ttftMS)
			if got != tc.want {
				t.Errorf("statusSpeedSegment(%v, %v) = %q, want %q",
					tc.stepTPS, tc.ttftMS, got, tc.want)
			}
		})
	}
}

// TestUpdateTaskResetsSpeedOnNewTask verifies that starting a new task
// clears the previous task's speed values so step 1 streaming never shows
// stale numbers (speed persists across task completion, but not across
// task starts).
func TestUpdateTaskResetsSpeedOnNewTask(t *testing.T) {
	st := &sessionState{mu: &sync.Mutex{}}

	// A step completes with speed data, then the task completes — speed
	// must persist (it reflects the final step).
	st.updateTask(true, 1, 5, 10, 12.5, 800)
	st.updateTask(false, 0, 5, 10, 12.5, 800)
	snap := st.snapshotStatus()
	if snap.StepTPS != 12.5 || snap.TTFTMS != 800 {
		t.Fatalf("after completion: step=%v ttft=%v, want 12.5/800 (kept)",
			snap.StepTPS, snap.TTFTMS)
	}

	// New task starts with no speed data yet — previous run's values gone.
	st.updateTask(true, 1, 5, 10, 0, 0)
	snap = st.snapshotStatus()
	if snap.StepTPS != 0 || snap.TTFTMS != 0 {
		t.Errorf("after new task start: step=%v ttft=%v, want all zero",
			snap.StepTPS, snap.TTFTMS)
	}
}

// TestStatusBarShowsSpeed verifies the end-to-end path: an SM "task"
// frame with speed fields is parsed, stored, and rendered into the
// status bar, positioned right after the context segment and before the
// steps segment.
func TestStatusBarShowsSpeed(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	out.handleSystemMsg(`{"type":"task","data":{"in_progress":true,"current_step":1,"max_steps":5,"context":1500,"step_tps":12.5,"ttft_ms":1200}}`)

	styles := DefaultStyles()
	terminal := &Terminal{
		out:              out,
		display:          NewDisplayModel(out.WindowBuffer(), styles),
		input:            NewPromptInput(styles),
		editor:           NewEditor(),
		modelSelector:    NewModelSelector(styles),
		themeSelector:    NewThemeSelector(styles),
		helpWindow:       NewHelpWindow(styles),
		confirmOverlay:   NewConfirmDialog(styles),
		mcpInitOverlay:   NewConfirmDialog(styles),
		attachmentWindow: NewAttachmentWindow(styles),
		focusedWindow:    focusInput,
		windowWidth:      80,
		windowHeight:     24,
		styles:           styles,
		hasFocus:         true,
	}

	*terminal = terminal.updateStatus()

	plain := stripANSI(terminal.statusLeft)
	if !containsSubstring(plain, "12.5 tok/s · ttft 1.2s") {
		t.Errorf("status bar missing speed segment, got %q", plain)
	}
	// Segment order: context ("1.5K") → speed ("tok/s") → steps ("1/5").
	ctxIdx := strings.Index(plain, "1.5K")
	speedIdx := strings.Index(plain, "tok/s")
	stepsIdx := strings.Index(plain, "1/5")
	if ctxIdx < 0 || speedIdx < 0 || stepsIdx < 0 {
		t.Fatalf("status bar missing segments: ctx=%d speed=%d steps=%d, got %q",
			ctxIdx, speedIdx, stepsIdx, plain)
	}
	if !(ctxIdx < speedIdx && speedIdx < stepsIdx) {
		t.Errorf("segment order = ctx(%d) speed(%d) steps(%d), want ctx < speed < steps: %q",
			ctxIdx, speedIdx, stepsIdx, plain)
	}
}

// TestStatusBarKeepsSpeedAfterCompletion verifies that after a task
// completes (in_progress=false) the final step's speed remains visible in
// the status bar.
func TestStatusBarKeepsSpeedAfterCompletion(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	out.handleSystemMsg(`{"type":"task","data":{"in_progress":false,"current_step":0,"max_steps":5,"context":100,"step_tps":12.5,"ttft_ms":1200}}`)

	styles := DefaultStyles()
	terminal := &Terminal{
		out:              out,
		display:          NewDisplayModel(out.WindowBuffer(), styles),
		input:            NewPromptInput(styles),
		editor:           NewEditor(),
		modelSelector:    NewModelSelector(styles),
		themeSelector:    NewThemeSelector(styles),
		helpWindow:       NewHelpWindow(styles),
		confirmOverlay:   NewConfirmDialog(styles),
		mcpInitOverlay:   NewConfirmDialog(styles),
		attachmentWindow: NewAttachmentWindow(styles),
		focusedWindow:    focusInput,
		windowWidth:      80,
		windowHeight:     24,
		styles:           styles,
		hasFocus:         true,
	}

	*terminal = terminal.updateStatus()

	plain := stripANSI(terminal.statusLeft)
	if !containsSubstring(plain, "12.5 tok/s · ttft 1.2s") {
		t.Errorf("status bar lost speed after completion, want it kept, got %q", plain)
	}
}
