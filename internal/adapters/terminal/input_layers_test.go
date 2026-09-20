package terminal

// The input layer stack, pinned as one table: for every state the user can put
// the program in, which layers are present and in what order. This is the other
// half of input_routing_test.go — that file asks where text goes, this one asks
// who gets the key — and both must read the same stack, so a table here is what
// keeps the two from drifting.

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/alayacore/alayacore/internal/commands"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// TestInputLayersForEveryState cross-checks the stack each fixture produces
// against the order the handlers run in.
func TestInputLayersForEveryState(t *testing.T) {
	pane := []inputLayer{layerUniversal, layerGlobal, layerPane}
	overlay := []inputLayer{layerUniversal, layerOverlay, layerGlobal, layerPane}
	want := map[string][]inputLayer{
		"prompt":                    pane,
		"display pane":              pane,
		"loading":                   {layerLoading},
		"attachment filter (local)": overlay,
		"attachment filter (url)":   overlay,
		"attachment list":           overlay,
		"model selector filter":     overlay,
		"theme filter":              overlay,
		"help filter":               overlay,
		"tool-confirm modal":        {layerUniversal, layerModal},
		"MCP-init modal":            {layerUniversal, layerModal},
	}
	for _, st := range routingStates() {
		t.Run(st.name, func(t *testing.T) {
			got := st.build(t).inputLayers()
			if !slices.Equal(got, want[st.name]) {
				t.Errorf("inputLayers() = %v, want %v for the state %q", got, want[st.name], st.name)
			}
		})
	}
}

// TestModalSwallowsGlobalKeys is the dispatch order made observable: a modal
// sits above the global layer, so a global shortcut inside it does nothing to
// the terminal. Ctrl+G would open the cancel-confirm dialog if the global layer
// saw it; with a tool-confirm modal open it must not.
func TestModalSwallowsGlobalKeys(t *testing.T) {
	m := openModal(t)
	if m.confirmOverlay.Kind() != ConfirmTool {
		t.Fatalf("fixture is not on the tool-confirm modal: %v", m.confirmOverlay.Kind())
	}
	after := feedMsg(t, m, KeyPressMsg{Code: 'g', Mod: ModCtrl})
	if after.confirmOverlay.Kind() != ConfirmTool {
		t.Fatalf("Ctrl+G leaked past the modal to the global layer: kind = %v", after.confirmOverlay.Kind())
	}
}

// TestUniversalSuspendOutranksModal pins that Ctrl+Z is checked before any modal:
// it works from inside a dialog, and the dialog is left as it was.
func TestUniversalSuspendOutranksModal(t *testing.T) {
	m := openModal(t)
	after, cmd := m.handleKeyMsg(KeyPressMsg{Code: 'z', Mod: ModCtrl})
	if cmd == nil {
		t.Fatal("Ctrl+Z must suspend even with a modal open")
	}
	if !after.confirmOverlay.IsOpen() {
		t.Error("Ctrl+Z must not disturb the modal it was pressed in")
	}
}

// TestOverlayOutranksGlobalKeys: with an overlay open, a global shortcut does not
// reach the terminal. Ctrl+G would open the cancel-confirm dialog from the global
// layer; the overlay consumes it first.
func TestOverlayOutranksGlobalKeys(t *testing.T) {
	m := openModelSelector(t)
	if !m.modelSelector.IsOpen() {
		t.Fatal("fixture is not on the model selector")
	}
	after := feedMsg(t, m, KeyPressMsg{Code: 'g', Mod: ModCtrl})
	if after.confirmOverlay.IsOpen() {
		t.Fatal("Ctrl+G leaked past the overlay to the global layer")
	}
}

// The MCP-init overlay's one key, and where it goes.
//
// Ctrl+G cancels initialization by emitting :mcp_cancel — it must not reach the
// global layer, which would open the cancel-confirm dialog instead. The overlay
// itself stays up: nothing on it closes it, because it is closed by the
// session's ready frame. That is also why ConfirmDialog has no key branch for
// this kind: the dialog is display-only, and this handler is its whole keyboard.
func TestMCPInitModalOwnsTheKeyboard(t *testing.T) {
	cap := &captureWriteCloser{}
	m := openMCPInit(t)
	m.streamInput = cap

	after, cmd := m.Update(KeyPressMsg{Code: 'g', Mod: ModCtrl})
	m = after.(Terminal)
	if m.confirmOverlay.IsOpen() {
		t.Fatalf("Ctrl+G reached the global layer: the cancel dialog opened (kind %v)", m.confirmOverlay.Kind())
	}
	if !m.mcpInitOverlay.IsOpen() || m.mcpInitOverlay.Kind() != ConfirmMCPInit {
		t.Fatalf("the MCP-init overlay should still be up: open=%v kind=%v",
			m.mcpInitOverlay.IsOpen(), m.mcpInitOverlay.Kind())
	}

	// The key's I/O: one :mcp_cancel CI frame. The handler batches it with the
	// next tick, so the batch is run the way the event loop runs it.
	if cmd == nil {
		t.Fatal("Ctrl+G should emit :mcp_cancel")
	}
	switch msg := cmd().(type) {
	case BatchMsg:
		for _, c := range msg {
			if c != nil {
				_ = c()
			}
		}
	default:
		t.Fatalf("Ctrl+G produced %T, want a BatchMsg", msg)
	}
	tag, value, err := tlv.ReadTLV(cap)
	if err != nil {
		t.Fatalf("read the emitted command: %v", err)
	}
	if tag != tlv.TagCommandIn {
		t.Fatalf("tag = %s, want CI", tag)
	}
	var sent protocol.CmdMsg
	if err := json.Unmarshal([]byte(value), &sent); err != nil {
		t.Fatalf("CI payload is not CmdMsg JSON: %v", err)
	}
	if sent.Name != commands.CommandNameMCPSkip {
		t.Errorf("sent %q, want %q", sent.Name, commands.CommandNameMCPSkip)
	}

	// Any other global chord is swallowed too: the modal is the keyboard's owner
	// while it is up.
	if leaked := feedMsg(t, m, KeyPressMsg{Code: 'l', Mod: ModCtrl}); leaked.modelSelector.IsOpen() {
		t.Error("Ctrl+L leaked past the MCP-init modal and opened the model selector")
	}
}
