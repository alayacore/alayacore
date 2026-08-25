package terminal

import (
	"fmt"
	"strings"
	"testing"
)

// TestStatusBarReasoningAlwaysShownWithoutHighlight verifies the status
// bar always renders the reasoning indicator ("R0✦".."R2✦") with the
// ✦ glyph retained but without the accent (highlight) color or bold
// weight. The status dot is the only element in the status bar that
// uses the accent.
func TestStatusBarReasoningAlwaysShownWithoutHighlight(t *testing.T) {
	for _, level := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("level=%d", level), func(t *testing.T) {
			out := NewTerminalOutput(DefaultStyles())
			out.handleSystemMsg(fmt.Sprintf(`{"type":"reasoning","data":{"level":%d}}`, level))

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

			// 1. Reasoning level is always shown with the ✦ glyph, even
			//    when 0 ("R0✦").
			want := fmt.Sprintf("R%d✦", level)
			if !containsSubstring(plain, want) {
				t.Errorf("expected status to contain %q, got %q", want, plain)
			}
			// 2. No bold SGR — bold was tied to the accent color and is
			//    no longer applied to the reasoning indicator.
			if strings.Contains(terminal.statusLeft, "\x1b[1m") {
				t.Errorf("reasoning indicator should not be bold, got %q", terminal.statusLeft)
			}
			// 3. The accent color must not be applied to the reasoning
			//    indicator. Render a reference string with the accent
			//    style, capture its exact ANSI signature, and confirm
			//    that signature never appears adjacent to the "R{n}✦".
			accentSignature := styles.Status.Foreground(styles.ColorAccent).Render("X")
			// Style.Render wraps text in SGR escapes; pull out the
			// opening escape prefix so we can scan for it.
			accentOpen := accentSignature
			if end := strings.Index(accentSignature, "X"); end > 0 {
				accentOpen = accentSignature[:end]
			}
			if accentOpen != "" && strings.Contains(accentOpen, "\x1b[") {
				// Locate "R{n}" in the raw (ANSI-bearing) status and
				// confirm the accent signature is not adjacent to it.
				rawIdx := strings.Index(terminal.statusLeft, want)
				if rawIdx >= 0 {
					window := terminal.statusLeft
					if rawIdx-len(accentOpen) >= 0 {
						window = terminal.statusLeft[rawIdx-len(accentOpen) : rawIdx+len(want)]
					}
					if strings.Contains(window, accentOpen) {
						t.Errorf("accent SGR %q wraps R{n} text: %q", accentOpen, terminal.statusLeft)
					}
				}
			}
		})
	}
}

func TestStatusBarShowsCurrentStepsDuringProgress(t *testing.T) {
	// Create output writer and simulate task in progress
	out := NewTerminalOutput(DefaultStyles())

	// Simulate task in progress
	out.handleSystemMsg(`{"type":"task","data":{"in_progress":true,"current_step":7,"max_steps":20,"context":0,"context_limit":0}}`)

	// Create terminal with the output writer
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

	// Update status
	*terminal = terminal.updateStatus()

	// Check that status shows current step progress
	expectedSubstring := "7/20"
	plain := stripANSI(terminal.statusLeft)
	if !containsSubstring(plain, expectedSubstring) {
		t.Errorf("Expected status to contain %q, got %q", expectedSubstring, plain)
	}
}

func TestStatusBarShowsLastStepsAfterCompletion(t *testing.T) {
	// Create output writer and simulate a task that ran 5 of 10 steps
	out := NewTerminalOutput(DefaultStyles())

	out.handleSystemMsg(`{"type":"task","data":{"in_progress":true,"current_step":5,"max_steps":10,"context":0,"context_limit":0}}`)
	// Completion broadcast carries current_step zeroed.
	out.handleSystemMsg(`{"type":"task","data":{"in_progress":false,"current_step":0,"max_steps":10,"context":0,"context_limit":0}}`)

	// Create terminal with the output writer
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

	// Update status
	*terminal = terminal.updateStatus()

	// Check that the status shows the last run's summary
	expectedSubstring := "5/10"
	plain := stripANSI(terminal.statusLeft)
	if !containsSubstring(plain, expectedSubstring) {
		t.Errorf("Expected status to contain %q, got %q", expectedSubstring, plain)
	}
}

func TestStatusBarShowsLastStepsUnlimited(t *testing.T) {
	// Simulate a task with unlimited max steps (default --max-steps=0)
	out := NewTerminalOutput(DefaultStyles())

	out.handleSystemMsg(`{"type":"task","data":{"in_progress":true,"current_step":3,"max_steps":0,"context":0,"context_limit":0}}`)
	out.handleSystemMsg(`{"type":"task","data":{"in_progress":false,"current_step":0,"max_steps":0,"context":0,"context_limit":0}}`)

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

	expectedSubstring := "3/INF"
	plain := stripANSI(terminal.statusLeft)
	if !containsSubstring(plain, expectedSubstring) {
		t.Errorf("Expected status to contain %q, got %q", expectedSubstring, plain)
	}
}

// Helper function to check if a string contains a substring
func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestStatusBarShowsActiveModelRightAligned verifies the active model
// name is displayed in the status bar, right-aligned in the remaining
// flexible space: the line ends with the model name and the padding
// sits between the left status segments and the model.
func TestStatusBarShowsActiveModelRightAligned(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	out.handleSystemMsg(`{"type":"model","data":{"active_id":1,"active_name":"gpt-4o","context_limit":128000}}`)

	m := newTerminalForUpdateStatusTest(out)
	m = m.updateStatus()

	rendered := m.renderStatusBar()
	plain := stripANSI(rendered)

	// Model name is the last thing on the line (right-aligned).
	if !strings.HasSuffix(plain, "gpt-4o") {
		t.Errorf("status bar should end with the model name, got %q", plain)
	}
	// The flexible padding goes between the left segments and the model.
	if !strings.Contains(plain, " gpt-4o") {
		t.Errorf("expected padding before the right-aligned model, got %q", plain)
	}
	// Flush against the right edge: no trailing cells after the model.
	// (Regression guard: the model used to be right-aligned to
	// windowWidth-2, leaving 2 blank cells after its name.)
	if w := Width(plain); w != m.windowWidth {
		t.Errorf("status bar width %d should fill the window width %d exactly: %q", w, m.windowWidth, plain)
	}
}

// TestStatusBarModelTruncatedWithEllipsis verifies the right-aligned
// model is truncated with "…" when the remaining space cannot fit it,
// and dropped entirely when there is no room at all.
func TestStatusBarModelTruncatedWithEllipsis(t *testing.T) {
	const modelName = "a-very-long-model-name-that-does-not-fit"

	out := NewTerminalOutput(DefaultStyles())
	out.handleSystemMsg(fmt.Sprintf(`{"type":"model","data":{"active_id":1,"active_name":%q,"context_limit":0}}`, modelName))

	m := newTerminalForUpdateStatusTest(out)
	m.windowWidth = 24 // lineBudget = 24: left "· R0✦ F↓" (8) leaves 15 cells for the model
	m = m.updateStatus()

	rendered := m.renderStatusBar()
	plain := stripANSI(rendered)

	// Truncated: the line ends with "…", the model's full name is gone.
	if !strings.HasSuffix(plain, "…") {
		t.Errorf("status bar should end with truncation ellipsis, got %q", plain)
	}
	if strings.Contains(plain, modelName) {
		t.Errorf("status bar should not contain the full model name, got %q", plain)
	}
	if w := Width(plain); w != m.windowWidth {
		t.Errorf("status bar width %d should fill the window width %d exactly: %q", w, m.windowWidth, plain)
	}

	// Extremely narrow window: no room for the model at all — it is
	// dropped (only the left segments remain, themselves truncated).
	m.windowWidth = 6 // lineBudget = 6 < left "· R0✦ F↓" (8) + separator (1) + 1-cell model
	m = m.updateStatus()
	plain = stripANSI(m.renderStatusBar())
	if strings.Contains(plain, modelName) {
		t.Errorf("model should be dropped with no room, got %q", plain)
	}
}

// TestStatusBarNoModelOmitsPadding verifies that without an active
// model the status bar renders exactly the left segments with no
// trailing padding (the model-empty path must not inject spaces).
func TestStatusBarNoModelOmitsPadding(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())

	m := newTerminalForUpdateStatusTest(out)
	m = m.updateStatus()

	plain := stripANSI(m.renderStatusBar())
	if plain != "· R0✦ F↓" {
		t.Errorf("expected bare left segments without model, got %q", plain)
	}
}

// TestStatusBarNoModelMayFillWidth verifies the no-model path shares
// the same full-width cap as the model path: an overlong bare status
// line is truncated to the window width (flush-to-edge design), not to
// windowWidth-2.
func TestStatusBarNoModelMayFillWidth(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	// Long reasoning + context segments so the bare line overflows a
	// narrow window.
	out.handleSystemMsg(`{"type":"reasoning","data":{"level":2}}`)
	out.handleSystemMsg(`{"type":"task","data":{"in_progress":false,"current_step":0,"max_steps":0,"context":999999999,"context_limit":1000000000}}`)

	m := newTerminalForUpdateStatusTest(out)
	m.windowWidth = 12 // content "· R2✦ F↓ | 1000.0M" (18) overflows → truncated to 12
	m = m.updateStatus()

	plain := stripANSI(m.renderStatusBar())
	if w := Width(plain); w != m.windowWidth {
		t.Errorf("status bar width %d should fill the window width %d exactly: %q", w, m.windowWidth, plain)
	}
	if strings.HasSuffix(plain, " ") {
		t.Errorf("status bar should not end with padding, got %q", plain)
	}
}

// TestStatusBarModelInvalidatesRenderCache verifies the render cache
// key includes the model segment — a model change must produce a new
// rendered line even when every other input is unchanged.
func TestStatusBarModelInvalidatesRenderCache(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	out.handleSystemMsg(`{"type":"model","data":{"active_id":1,"active_name":"gpt-4o","context_limit":0}}`)

	m := newTerminalForUpdateStatusTest(out)
	m = m.updateStatus()
	first := m.renderStatusBar()

	// Same inputs → cached.
	if got := m.renderStatusBar(); got != first {
		t.Fatalf("status bar should be cached on unchanged inputs: %q != %q", got, first)
	}

	// Model change → new render.
	out.handleSystemMsg(`{"type":"model","data":{"active_id":2,"active_name":"claude-sonnet-4-5","context_limit":0}}`)
	m = m.updateStatus()
	second := m.renderStatusBar()
	if second == first {
		t.Errorf("status bar should change when the active model changes: %q == %q", second, first)
	}
	if !strings.HasSuffix(stripANSI(second), "claude-sonnet-4-5") {
		t.Errorf("status bar should show the new model, got %q", stripANSI(second))
	}
}

func TestFormatTokenCount(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "0"},
		{42, "42"},
		{999, "999"},
		{1000, "1K"},
		{1500, "1.5K"},
		{9999, "10.0K"},
		{10000, "10K"},
		{15500, "15.5K"},
		{100000, "100K"},
		{999499, "999.5K"},
		{1000000, "1M"},
		{1500000, "1.5M"},
		{10000000, "10M"},
		{128000, "128K"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d", tt.input), func(t *testing.T) {
			got := formatTokenCount(tt.input)
			if got != tt.expected {
				t.Errorf("formatTokenCount(%d) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
