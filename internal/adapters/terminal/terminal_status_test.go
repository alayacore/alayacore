package terminal

import (
	"fmt"
	"strings"
	"testing"
)

// TestStatusBarReasoningRidesWithModelWithoutHighlight verifies the status
// bar always renders the reasoning level ("R0".."R2") plain — no accent
// (highlight) color and no bold weight. The status dot is the only
// element in the status bar that uses the accent. It also pins where the
// level sits: the right group, fused to the model name when there is one
// ("gpt-4o | R2") and alone when there is not (a bare "R2") — the level
// never moves to the left segments. There is no marker glyph after the
// level: ✦ used to be drawn there unconditionally (even at R0, where it
// signals nothing), which read like a state indicator while never
// changing state.
func TestStatusBarReasoningRidesWithModelWithoutHighlight(t *testing.T) {
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

			want := fmt.Sprintf("R%d", level)

			// 1. No active model: the level still sits on the right — the
			//    bar is the status dot and the bare level, and the left
			//    segments stay empty. Always shown, even when 0 ("R0").
			rendered := terminal.renderStatusBar()
			if got := stripANSI(rendered); !strings.HasSuffix(got, want) {
				t.Errorf("status bar should end with %q, got %q", want, got)
			}
			if got := stripANSI(terminal.statusLeft); got != "" {
				t.Errorf("level must not be a left segment, got %q", got)
			}

			// 2. Active model: the level rides after the model name in the
			//    right group.
			out.handleSystemMsg(`{"type":"model","data":{"active_id":1,"active_name":"gpt-4o","context_limit":0}}`)
			*terminal = terminal.updateStatus()

			rendered = terminal.renderStatusBar()
			if got := stripANSI(rendered); !strings.HasSuffix(got, "gpt-4o | "+want) {
				t.Errorf("status bar should end with the model group %q, got %q", "gpt-4o | "+want, got)
			}
			// 3. No bold SGR — bold was tied to the accent color and is
			//    no longer applied to the reasoning indicator.
			if strings.Contains(rendered, "\x1b[1m") {
				t.Errorf("reasoning indicator should not be bold, got %q", rendered)
			}
			// 4. The accent color must not be applied to the reasoning
			//    indicator. Render a reference string with the accent
			//    style, capture its exact ANSI signature, and confirm
			//    that signature never appears adjacent to the "R{n}" text.
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
				rawIdx := strings.Index(rendered, want)
				if rawIdx >= 0 {
					window := rendered
					if rawIdx-len(accentOpen) >= 0 {
						window = rendered[rawIdx-len(accentOpen) : rawIdx+len(want)]
					}
					if strings.Contains(window, accentOpen) {
						t.Errorf("accent SGR %q wraps R{n} text: %q", accentOpen, rendered)
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
// flexible space with the reasoning level fused to it: the line ends
// with the model group and the padding sits between the left status
// segments and the group.
func TestStatusBarShowsActiveModelRightAligned(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	out.handleSystemMsg(`{"type":"model","data":{"active_id":1,"active_name":"gpt-4o","context_limit":128000}}`)

	m := newTerminalForUpdateStatusTest(out)
	m = m.updateStatus()

	rendered := m.renderStatusBar()
	plain := stripANSI(rendered)

	// The model group — model name plus the reasoning level riding after
	// it — is the last thing on the line (right-aligned).
	if !strings.HasSuffix(plain, "gpt-4o | R0") {
		t.Errorf("status bar should end with the model group, got %q", plain)
	}
	// The flexible padding goes between the left segments and the group.
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
// model group is truncated with "…" when the remaining space cannot fit
// it, and dropped entirely when there is no room at all.
func TestStatusBarModelTruncatedWithEllipsis(t *testing.T) {
	const modelName = "a-very-long-model-name-that-does-not-fit"

	out := NewTerminalOutput(DefaultStyles())
	out.handleSystemMsg(fmt.Sprintf(`{"type":"model","data":{"active_id":1,"active_name":%q,"context_limit":0}}`, modelName))

	m := newTerminalForUpdateStatusTest(out)
	m.windowWidth = 24 // the 45-cell group ("<40-cell name> | R0") cannot share 24 with the dot → merged, truncated
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

	// Extremely narrow window: the group joins the (empty) left segments
	// and the truncation cuts into the model name itself.
	m.windowWidth = 6 // budget 4: only the first cells of the model name survive
	m = m.updateStatus()
	plain = stripANSI(m.renderStatusBar())
	if strings.Contains(plain, modelName) {
		t.Errorf("model should be dropped with no room, got %q", plain)
	}
}

// TestStatusBarSqueezedLineDropsReasoningFirst pins the tail position of
// the reasoning level in the model group: truncation runs from the end,
// so a line with no room loses "R0" before it costs the reader any of
// the model name — a squeeze must not take the session's identity while
// a setting the session file already records is still on screen.
func TestStatusBarSqueezedLineDropsReasoningFirst(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	out.handleSystemMsg(`{"type":"model","data":{"active_id":1,"active_name":"gpt-4o","context_limit":0}}`)

	m := newTerminalForUpdateStatusTest(out)
	m = m.updateStatus()
	m.windowWidth = 9 // "∙ gpt-4o | R0" (13 cells) merges → "∙ gpt-4o…" (9)

	plain := stripANSI(m.renderStatusBar())
	if !strings.Contains(plain, "gpt-4o") {
		t.Errorf("the model name must survive the squeeze, got %q", plain)
	}
	if strings.Contains(plain, "R0") {
		t.Errorf("the level rides at the tail and must be truncated first, got %q", plain)
	}
}

// TestStatusBarNoModelKeepsLevelRightAligned verifies that with no active
// model the bar is still the status dot and the level flush right — the
// level's column does not depend on whether a model happens to be set —
// and that the model-empty left side carries no segments at all (no stray
// separator, no padding injected before the group).
func TestStatusBarNoModelKeepsLevelRightAligned(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())

	m := newTerminalForUpdateStatusTest(out)
	m = m.updateStatus()

	if got := stripANSI(m.statusLeft); got != "" {
		t.Errorf("left segments without a model = %q, want none (the level is a right-group field)", got)
	}

	plain := stripANSI(m.renderStatusBar())
	if !strings.HasPrefix(plain, statusDotGlyph+" ") {
		t.Errorf("status bar should open with the status dot, got %q", plain)
	}
	if !strings.HasSuffix(plain, "R0") {
		t.Errorf("status bar should end with the level, got %q", plain)
	}
	if w := Width(plain); w != m.windowWidth {
		t.Errorf("status bar width %d should fill the window width %d exactly: %q", w, m.windowWidth, plain)
	}
}

// TestStatusBarNoModelMayFillWidth verifies the no-model path shares
// the same full-width cap as the model path: an overlong status line is
// truncated to the window width (flush-to-edge design), not to
// windowWidth-2 — the level stays in the right group throughout.
func TestStatusBarNoModelMayFillWidth(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	// Long reasoning + context segments so the row overflows a narrow
	// window.
	out.handleSystemMsg(`{"type":"reasoning","data":{"level":2}}`)
	out.handleSystemMsg(`{"type":"task","data":{"in_progress":false,"current_step":0,"max_steps":0,"context":999999999,"context_limit":1000000000}}`)

	m := newTerminalForUpdateStatusTest(out)
	m.windowWidth = 12 // the 22-cell context segment overflows the budget on its own → truncated
	m = m.updateStatus()

	plain := stripANSI(m.renderStatusBar())
	if w := Width(plain); w != m.windowWidth {
		t.Errorf("status bar width %d should fill the window width %d exactly: %q", w, m.windowWidth, plain)
	}
	if strings.HasSuffix(plain, " ") {
		t.Errorf("status bar should not end with padding, got %q", plain)
	}
}

// TestStatusBarModelGroupInvalidatesRenderCache verifies the render cache
// key includes the whole right group — a model change and a reasoning
// level change must each produce a new rendered line even when every
// other input is unchanged.
func TestStatusBarModelGroupInvalidatesRenderCache(t *testing.T) {
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
	if !strings.HasSuffix(stripANSI(second), "claude-sonnet-4-5 | R0") {
		t.Errorf("status bar should show the new model, got %q", stripANSI(second))
	}

	// Reasoning level change → new render: the level is part of the group,
	// so a level change has to move the cache key too.
	out.handleSystemMsg(`{"type":"reasoning","data":{"level":2}}`)
	m = m.updateStatus()
	third := m.renderStatusBar()
	if third == second {
		t.Errorf("status bar should change when the reasoning level changes: %q == %q", third, second)
	}
	if !strings.HasSuffix(stripANSI(third), "claude-sonnet-4-5 | R2") {
		t.Errorf("status bar should show the new level, got %q", stripANSI(third))
	}
}

// TestStatusRightSegment pins the group the status bar puts on the
// right: the model name with the reasoning level fused to it, or the bare
// level when no model is active — the level never moves to the left.
func TestStatusRightSegment(t *testing.T) {
	tests := []struct {
		model string
		level int
		want  string
	}{
		{"gpt-4o", 0, "gpt-4o | R0"},
		{"gpt-4o", 2, "gpt-4o | R2"},
		{"DeepSeek / DeepSeek Flash", 2, "DeepSeek / DeepSeek Flash | R2"},
		{"", 0, "R0"},
		{"", 2, "R2"},
	}
	for _, tt := range tests {
		if got := statusRightSegment(tt.model, tt.level); got != tt.want {
			t.Errorf("statusRightSegment(%q, %d) = %q, want %q", tt.model, tt.level, got, tt.want)
		}
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
