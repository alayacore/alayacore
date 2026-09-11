package terminal

// The input layer stack, pinned as one table: for every state the user can put
// the program in, which layers are present and in what order. This is the other
// half of input_routing_test.go — that file asks where text goes, this one asks
// who gets the key — and both must read the same stack, so a table here is what
// keeps the two from drifting.

import (
	"slices"
	"testing"
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
