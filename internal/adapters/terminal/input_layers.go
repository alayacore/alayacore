package terminal

// The input layer stack: the single ordered answer to "who gets this key", for
// both dispatch and routing.
//
// Two questions used to be answered by two hand-written chains in two files —
// handleKeyMsg's sequence of ifs, and keyboardTarget's separate switch — and the
// orders could (and did) drift. Now there is one list per keypress: dispatch
// walks it and stops at the first layer that consumes, and keyboardTarget walks
// it and stops at the first layer that owns text. Same list, so the two answers
// cannot disagree.
//
// A layer is a *kind*, not an object: the handlers it names (handleGlobalKeys,
// handleSelectorOverlayKeys, …) already exist, and the loop just calls them in
// order. That keeps the ordering in one place without inventing a per-layer
// interface whose contract each handler would have to be re-shaped to satisfy.

// inputLayer names one level of input ownership, highest priority first.
type inputLayer int

const (
	// layerLoading is the loading screen: no box exists yet, so it consumes
	// every key (and is the whole stack while loading).
	layerLoading inputLayer = iota
	// layerUniversal is the always-present floor above modals: Ctrl+Z. Suspend
	// works from anywhere, including inside a dialog.
	layerUniversal
	// layerModal is a yes/no dialog (tool confirm, MCP), which consumes every
	// key and answers only its own.
	layerModal
	// layerOverlay is a filter/list overlay (theme, model, attachment, help).
	layerOverlay
	// layerGlobal is Tab and the global shortcuts (Ctrl+S/L/P/R/H, F1).
	layerGlobal
	// layerPane is the display pane or the prompt, whichever has focus.
	layerPane
)

// inputLayers returns the layers present, top (highest priority) first. The
// order is the dispatch order and the routing priority at once.
func (m Terminal) inputLayers() []inputLayer {
	if m.loading {
		return []inputLayer{layerLoading}
	}

	layers := make([]inputLayer, 0, 5)
	layers = append(layers, layerUniversal)

	if m.confirmOverlay.IsOpen() || m.mcpInitOverlay.IsOpen() {
		// A modal takes the keyboard entirely; nothing below it sees a key.
		return append(layers, layerModal)
	}
	if m.anyOverlayOpen() {
		layers = append(layers, layerOverlay)
	}
	return append(layers, layerGlobal, layerPane)
}

// anyOverlayOpen reports whether a selector overlay owns the screen.
func (m Terminal) anyOverlayOpen() bool {
	return m.attachmentWindow.IsOpen() ||
		m.modelSelector.IsOpen() ||
		m.themeSelector.IsOpen() ||
		m.helpWindow.IsOpen()
}
