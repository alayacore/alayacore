package terminal

import (
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/tlv"
)

// TestStatusBarAutoFollowIndicatorUpdatesOnNavigation reproduces the
// bug where the F↓ (auto-follow) indicator in the status bar was
// driven solely by the OutputWriter's status snapshot version. The
// auto-follow state lives on the DisplayModel and changes when the
// user navigates with j/k/h/l/G/space — none of which bump the
// session version. As a result, updateStatus() early-exited on the
// version match and the cached m.statusLeft (with or without "F↓")
// was left stale until the next status-affecting session event.
//
// Fix: updateStatus() must also rebuild when autoFollow has flipped
// since the last build. Track the last-rendered autoFollow value on
// the Terminal and force-rebuild when it changes (version match).
func TestStatusBarAutoFollowIndicatorUpdatesOnNavigation(t *testing.T) {
	styles := DefaultStyles()
	out := NewTerminalOutput(styles)

	// Seed two assistant text windows so navigation can target them.
	out.WindowBuffer().AppendOrUpdate(tlv.TagAssistantT, "win-1", "first message")
	out.WindowBuffer().AppendOrUpdate(tlv.TagAssistantT, "win-2", "second message")

	// Bump the status snapshot version at least once. Without this the
	// updateStatus() early-exit (`lastStatusVersion != 0 && matches`) is
	// short-circuited by the "0 != 0" guard and the bug doesn't surface.
	out.handleSystemMsg(`{"type":"task","data":{"in_progress":true,"current_step":1,"max_steps":1,"context":0,"context_limit":0}}`)
	out.handleSystemMsg(`{"type":"task","data":{"in_progress":false,"current_step":0,"max_steps":1,"context":0,"context_limit":0}}`)

	tm := &Terminal{
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
		focusedWindow:    focusDisplay,
		windowWidth:      80,
		windowHeight:     24,
		styles:           styles,
		hasFocus:         true,
	}

	// Initial setup: cursor at last window (auto-follow is ON by default
	// and stays ON while cursor sits at last window after one build).
	tm.display = tm.display.WithCursorToLastWindow()
	*tm = tm.updateStatus()
	tm.display = tm.display.WithCursorToLastWindow()

	// Move up one window — auto-follow should flip OFF, F↓ should disappear.
	tm.display, _ = tm.display.MoveWindowCursorUp()
	if tm.display.shouldFollow() {
		t.Fatalf("setup: MoveWindowCursorUp must disable autoFollow")
	}
	*tm = tm.updateStatus()

	if strings.Contains(stripANSI(tm.statusLeft), "F↓") {
		t.Errorf("after moving cursor up, F↓ must be hidden (autoFollow=false), got statusLeft=%q",
			stripANSI(tm.statusLeft))
	}

	// Move back to the bottom — auto-follow should flip ON, F↓ should reappear.
	// This call must bump lastAutoFollowSeen / force a rebuild even though
	// the snapshot version does not change.
	tm.display = tm.display.WithCursorToLastWindow()
	if !tm.display.shouldFollow() {
		t.Fatalf("setup: WithCursorToLastWindow must enable autoFollow")
	}
	*tm = tm.updateStatus()

	if !strings.Contains(stripANSI(tm.statusLeft), "F↓") {
		t.Errorf("after moving to bottom, F↓ must be shown (autoFollow=true), got statusLeft=%q",
			stripANSI(tm.statusLeft))
	}
}
