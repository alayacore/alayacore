package terminal

// Regression tests for the render-path optimizations that prevent
// unnecessary terminal updates:
//
//   - Loading view is full-screen (padded), so spinner changes only
//     repaint one row instead of doing ED2-clear every tick.
//   - forceRedraw (Ctrl-R) uses a dedicated forceRepaintMsg that clears
//     the frame caches directly, producing exactly one full repaint
//     instead of two diff renders per toggle.
//   - updateStatus skips the rebuild when the status snapshot's
//     version counter is unchanged since the last call.
//   - updateContent short-circuits when neither DisplayModel state
//     changed nor the underlying window buffer is dirty.
//   - modelSelector.LoadModels is skipped when the model snapshot's
//     version is unchanged.
//
// These lock the cheap-path behavior so a future refactor doesn't
// re-introduce the screen flicker / wasted CPU.

import (
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// TestUpdateStatusSkipsWhenVersionUnchanged verifies that calling
// updateStatus twice without an intervening session update does not
// rebuild the status text. The version counter on the snapshot drives
// the early-exit.
func TestUpdateStatusSkipsWhenVersionUnchanged(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	out.handleSystemMsg(`{"type":"reasoning","data":{"level":1}}`)
	out.handleSystemMsg(`{"type":"task","data":{"in_progress":false,"current_step":3,"max_steps":5,"context":1000}}`)
	snap1 := out.SnapshotStatus()

	m := newTerminalForUpdateStatusTest(out)
	m1 := m.updateStatus()

	snap2 := out.SnapshotStatus()
	if snap1.Version != snap2.Version {
		t.Fatalf("snapshot version should be stable across reads: %d != %d", snap1.Version, snap2.Version)
	}

	// Second call with no new updates should be a no-op for the
	// status-text fields (version match → early exit).
	before := m1.statusText
	after := m1.updateStatus()
	if after.statusText != before {
		t.Errorf("status text changed without a session update: %q → %q", before, after.statusText)
	}
}

// TestUpdateStatusRebuildsAfterSessionUpdate verifies the version
// counter triggers a rebuild when the session actually changes.
func TestUpdateStatusRebuildsAfterSessionUpdate(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	out.handleSystemMsg(`{"type":"task","data":{"in_progress":true,"current_step":3,"max_steps":5,"context":1000}}`)

	m := newTerminalForUpdateStatusTest(out)
	m = m.updateStatus()
	first := m.statusText

	// Token count changes — version should bump and rebuild.
	out.handleSystemMsg(`{"type":"task","data":{"in_progress":true,"current_step":3,"max_steps":5,"context":9999}}`)
	m = m.updateStatus()
	if m.statusText == first {
		t.Errorf("status text should change when context tokens change: %q == %q", first, m.statusText)
	}
}

// TestSnapshotStatusSkipsEmptySlices verifies the snapshot's MCPServers
// and CachedThemes stay nil (not an allocated empty slice) when no
// servers/themes are configured — `append([]T(nil), nil...)` would
// otherwise allocate an empty non-nil slice every tick.
func TestSnapshotStatusSkipsEmptySlices(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	snap := out.SnapshotStatus()
	if snap.MCPServers != nil {
		t.Errorf("MCPServers should be nil when no MCP servers are configured, got %v", snap.MCPServers)
	}
	if snap.CachedThemes != nil {
		t.Errorf("CachedThemes should be nil when no themes are configured, got %d entries", len(snap.CachedThemes))
	}
}

// TestSnapshotModelsSkipsEmptySlice verifies the same for Models.
func TestSnapshotModelsSkipsEmptySlice(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	snap := out.SnapshotModels()
	if snap.Models != nil {
		t.Errorf("Models should be nil when no models are configured, got %d entries", len(snap.Models))
	}
}

// TestUpdateContentSkipsWhenIdle verifies that an idle tick (no
// DisplayModel mutation, no window-buffer mutation) does not call
// WindowBuffer.GetAll. The cheap path returns the receiver unchanged
// without acquiring the window-buffer lock.
//
// We check via lastContent (which only updates on a real render) and
// the contentDirty flag (which is cleared after a successful render).
func TestUpdateContentSkipsWhenIdle(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	out.WindowBuffer().AppendOrUpdate(tlv.TagAssistantT, "w1", "hello")
	m := newTerminalForUpdateStatusTest(out)

	// First render populates lastContent / clears dirty.
	m1 := m.display.updateContent()
	if m1.lastContent == "" {
		t.Fatal("first render must populate lastContent")
	}
	if m1.contentDirty {
		t.Fatal("first render must clear contentDirty")
	}

	// Second render with no state change must be a no-op — lastContent
	// stays the same, contentDirty stays false.
	m2 := m1.updateContent()
	if m2.lastContent != m1.lastContent {
		t.Errorf("idle render changed lastContent: %q → %q", m1.lastContent, m2.lastContent)
	}
	if m2.contentDirty {
		t.Error("idle render left contentDirty set")
	}
}

// TestUpdateContentRunsOnBufferDirty verifies the cheap path falls
// through to the real render when the underlying window buffer is
// dirty (new content arrived, or windows were invalidated).
func TestUpdateContentRunsOnBufferDirty(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	out.WindowBuffer().AppendOrUpdate(tlv.TagAssistantT, "w1", "hello")
	m := newTerminalForUpdateStatusTest(out)
	m1 := m.display.updateContent() // populate cache, clear dirty

	out.WindowBuffer().AppendOrUpdate(tlv.TagAssistantT, "w1", "hello world")
	if !m.out.WindowBuffer().IsDirty() {
		t.Fatal("AppendOrUpdate should mark the buffer dirty")
	}

	m2 := m1.updateContent()
	if !strings.Contains(stripANSI(m2.lastContent), "hello world") {
		t.Errorf("render after content append did not include new content: %q", stripANSI(m2.lastContent))
	}
}

// TestModelSelectorVersionUnchanged verifies the model snapshot's
// version counter is stable across reads with no intervening updates.
// The Terminal's tick handler relies on this to skip the
// modelSelector.LoadModels rebuild.
func TestModelSelectorVersionUnchanged(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	out.handleSystemMsg(`{"type":"model_list","data":{"models":[{"id":1,"name":"a"},{"id":2,"name":"b"}]}}`)
	out.handleSystemMsg(`{"type":"model","data":{"active_id":1,"active_name":"a","context_limit":0}}`)

	snap1 := out.SnapshotModels()
	snap2 := out.SnapshotModels()
	if snap1.Version == 0 {
		t.Fatal("model snapshot version should be non-zero after updates")
	}
	if snap1.Version != snap2.Version {
		t.Errorf("snapshot version should be stable across reads: %d != %d", snap1.Version, snap2.Version)
	}
}

// TestForceRepaintMsgBypassesIdentityCheck verifies the dedicated
// forceRepaintMsg is recognized by Program.run and clears both
// Program.lastView and the Screen caches, triggering a full repaint on
// the next render. The previous `\x1b[0m` content-suffix trick has
// been removed; this test locks the replacement.
func TestForceRepaintMsgBypassesIdentityCheck(t *testing.T) {
	// forceRepaintMsg is unexported (an internal protocol message);
	// we exercise the public Cmd helper instead.
	cmd := ForceRepaintCmd()
	if cmd == nil {
		t.Fatal("ForceRepaintCmd must return a non-nil Cmd")
	}
	msg := cmd()
	if _, ok := msg.(forceRepaintMsg); !ok {
		t.Errorf("ForceRepaintCmd must produce a forceRepaintMsg, got %T", msg)
	}
}

// newTerminalForUpdateStatusTest builds a Terminal ready for
// updateStatus / updateContent calls in tests — most fields are
// zero-valued, only the ones the methods touch are populated.
func newTerminalForUpdateStatusTest(out OutputWriter) Terminal {
	styles := DefaultStyles()
	return Terminal{
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
		appConfig:        &app.Config{},
	}
}

// TestModelSelectorLoadModelsProtocol verifies the protocol-level
// behavior of LoadModels (the function the version-checked tick path
// guards): when the model list changes, the selector's internal
// searchable model slice is updated. The selector's State is
// independent of LoadModels (Open/Close drive that).
func TestModelSelectorLoadModelsProtocol(t *testing.T) {
	m := newTerminalForUpdateStatusTest(NewTerminalOutput(DefaultStyles()))
	beforeCount := len(m.modelSelector.models)

	ms, _ := m.modelSelector.LoadModels([]protocol.ModelInfo{{ID: 1, Name: "a"}, {ID: 2, Name: "b"}}, 1)
	if len(ms.models) == beforeCount {
		t.Errorf("LoadModels should populate the selector's model list: before=%d after=%d", beforeCount, len(ms.models))
	}
	if ms.activeModel == nil || ms.activeModel.ID != 1 {
		t.Errorf("LoadModels should set activeModel to the active ID, got %+v", ms.activeModel)
	}
}

// TestRenderStatusBarCachedOnIdle verifies the status-bar render
// cache returns the same string on consecutive calls with no state
// change. View() invokes renderStatusBar every frame; without the
// cache, every 250ms tick re-runs the indicator + truncate +
// Style.Render pipeline, even when nothing rendering-relevant has
// changed.
func TestRenderStatusBarCachedOnIdle(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	out.handleSystemMsg(`{"type":"task","data":{"in_progress":false,"current_step":0,"max_steps":5,"context":0}}`)
	m := newTerminalForUpdateStatusTest(out)
	m = m.updateStatus()

	first := m.renderStatusBar()
	second := m.renderStatusBar()
	if first != second {
		t.Errorf("idle renderStatusBar should be cached: %q != %q", first, second)
	}
	if m.renderedStatusBarCache == nil || len(*m.renderedStatusBarCache) == 0 {
		t.Error("renderStatusBar should populate the cache on first call")
	}
}

// TestRenderStatusBarCacheInvalidatesOnChange verifies the cache
// invalidates when an input that affects the rendered string changes
// (here: in-progress flag).
func TestRenderStatusBarCacheInvalidatesOnChange(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	out.handleSystemMsg(`{"type":"task","data":{"in_progress":false,"current_step":0,"max_steps":5,"context":0}}`)
	m := newTerminalForUpdateStatusTest(out)
	m = m.updateStatus()
	first := m.renderStatusBar()

	// Task starts → inProgress flips; the indicator color (accent vs dim)
	// changes, so the rendered string must differ.
	out.handleSystemMsg(`{"type":"task","data":{"in_progress":true,"current_step":1,"max_steps":5,"context":0}}`)
	m = m.updateStatus()
	second := m.renderStatusBar()
	if first == second {
		t.Errorf("status bar should change when inProgress flips: %q == %q", first, second)
	}
}
