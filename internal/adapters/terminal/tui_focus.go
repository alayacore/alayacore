package terminal

// Who owns the keyboard, and what the terminal window's focus is worth.
//
// Two questions live here, and keeping them apart is the point of the file.
//
// **Which box is the user writing into?** has exactly one answer,
// `keyboardTarget`, derived from state the user changed: the pane they toggled
// to, the overlay they opened, whether that overlay's filter or list holds the
// caret, whether a modal has the screen. It is the only thing that decides where
// a key or a paste goes, and no box gets to answer it for itself — a box that
// does is a box that can drop input for a reason nobody chose. Per-box text
// editing lives in `input_field.go` (`InputField.Update`, `handlePaste`).
//
// **"is this program's window the focused one?"** is `hasFocus`, reported by the
// terminal (DEC mode 1004). It is worth the frame and nothing else: it paints
// borders and text in the blurred register and takes the real caret away (IME
// anchors on it). It was previously wired into the routing flag as well, and that
// is the bug this split exists to have prevented — a terminal's own context menu
// loses the window's focus to hand the clipboard back, so every paste pasted from
// that menu was deleted in silence.
//
// Extracted from tui.go.

import "fmt"

// toggleFocus switches between display and input windows.
func (m Terminal) toggleFocus() Terminal {
	if m.focusedWindow == focusDisplay {
		m = m.focusInput()
	} else {
		m = m.focusDisplay()
	}
	m.display = m.display.updateContent()
	return m
}

// focusInput switches the user's target to the prompt. It does not touch how the
// box paints: View() derives that from the keyboard target and hasFocus every
// frame, so a routing change and a painting change cannot drift apart.
func (m Terminal) focusInput() Terminal {
	m.focusedWindow = focusInput
	m.display = m.display.WithDisplayFocused(false)
	return m
}

// focusDisplay switches focus to the display window.
func (m Terminal) focusDisplay() Terminal {
	m.focusedWindow = focusDisplay
	m.display = m.display.WithDisplayFocused(true)
	if m.display.GetWindowCursor() < 0 {
		m.display = m.display.WithCursorToLastWindow()
	}
	return m
}

// openModelSelector opens the model selector UI.
func (m Terminal) openModelSelector() Terminal {
	m.modelSelector = m.modelSelector.Open()
	m.display = m.display.WithBlocked(true)
	m.display = m.display.WithDisplayFocused(false)
	m.display = m.display.updateContent()
	return m
}

// restoreFocus restores focus to the previously focused window after an overlay closes.
func (m Terminal) restoreFocus() Terminal {
	// Sync blocked state — overlay just closed, so isBlocked() is likely false now.
	m.display = m.display.WithBlocked(m.isBlocked())
	if m.focusedWindow == focusDisplay {
		m = m.focusDisplay()
	} else {
		m = m.focusInput()
	}
	m.display = m.display.updateContent()
	return m
}

// openThemeSelector opens the theme selector UI.
func (m Terminal) openThemeSelector() Terminal {
	if m.themeManager == nil {
		return m
	}
	snap := m.out.SnapshotStatus()
	m.themeSelector = m.themeSelector.Open(snap.CachedThemes, m.activeTheme)
	m.selectorOriginalTheme = nil
	m.previewAppliedTheme = nil
	for _, t := range snap.CachedThemes {
		if t.Name == m.activeTheme && t.Theme != nil {
			m.selectorOriginalTheme = t.Theme
			m.previewAppliedTheme = t.Theme
			break
		}
	}
	m.display = m.display.WithBlocked(true)
	m.display = m.display.WithDisplayFocused(false)
	m.display = m.display.updateContent()
	return m
}

// openHelpWindow opens the help window UI.
func (m Terminal) openHelpWindow() Terminal {
	m.helpWindow = m.helpWindow.Open()
	m.display = m.display.WithBlocked(true)
	m.display = m.display.WithDisplayFocused(false)
	m.display = m.display.updateContent()
	return m
}

// openAttachmentWindow opens the attachment picker overlay.
func (m Terminal) openAttachmentWindow() Terminal {
	m.attachmentWindow = m.attachmentWindow.Open()
	m.display = m.display.WithBlocked(true)
	m.display = m.display.WithDisplayFocused(false)
	m.display = m.display.updateContent()
	return m
}

// openConfirmQuit opens the quit confirmation dialog.
func (m Terminal) openConfirmQuit() Terminal {
	m.confirmOverlay = m.confirmOverlay.OpenQuit()
	m.display = m.display.WithBlocked(true)
	m.display = m.display.WithDisplayFocused(false)
	m.display = m.display.updateContent()
	return m
}

// openConfirmCancel opens the cancel-task confirmation dialog.
func (m Terminal) openConfirmCancel() Terminal {
	m.confirmOverlay = m.confirmOverlay.OpenCancel()
	m.display = m.display.WithBlocked(true)
	m.display = m.display.WithDisplayFocused(false)
	m.display = m.display.updateContent()
	return m
}

// openConfirmTool opens the tool-execution confirmation dialog.
func (m Terminal) openConfirmTool(id, toolName, toolInput string) Terminal {
	m.confirmOverlay = m.confirmOverlay.OpenTool(id, toolName, toolInput)
	m.display = m.display.WithBlocked(true)
	m.display = m.display.WithDisplayFocused(false)
	m.display = m.display.updateContent()
	return m
}

func (m Terminal) openConfirmMCPAuth(server, url string) Terminal {
	m.confirmOverlay = m.confirmOverlay.OpenMCPAuth(server, url)
	m.display = m.display.WithBlocked(true)
	m.display = m.display.WithDisplayFocused(false)
	m.display = m.display.updateContent()
	return m
}

// handleBlur handles loss of application focus. It paints and nothing more: each
// selector below is asked for its blurred register and loses the real caret, the
// display dims, and the keyboard target is left exactly as the user set it. That
// split is the whole lesson of the reported bug — the only thing that can deliver
// input while the window is unfocused is something addressed to this program (the
// terminal's own menu handing back a paste is the everyday case), so a focus
// change may not be a reason to discard what arrives. The two dialogs need no
// line here: their frame state comes from the target, which a blur never moves.
func (m Terminal) handleBlur() Terminal {
	m.hasFocus = false
	m.display = m.display.WithBlocked(m.isBlocked())
	m.display = m.display.WithDisplayFocused(false)
	m.modelSelector = m.modelSelector.WithFocus(false)
	m.themeSelector = m.themeSelector.WithFocus(false)
	m.helpWindow = m.helpWindow.WithFocus(false)
	m.attachmentWindow = m.attachmentWindow.WithFocus(false)
	m.display = m.display.updateContent()
	return m
}

// handleFocus handles gain of application focus.
func (m Terminal) handleFocus() Terminal {
	m.hasFocus = true
	m.display = m.display.WithBlocked(m.isBlocked())
	m.modelSelector = m.modelSelector.WithFocus(true)
	m.themeSelector = m.themeSelector.WithFocus(true)
	m.helpWindow = m.helpWindow.WithFocus(true)
	m.attachmentWindow = m.attachmentWindow.WithFocus(true)

	if m.modelSelector.IsOpen() ||
		m.themeSelector.IsOpen() ||
		m.helpWindow.IsOpen() ||
		m.attachmentWindow.IsOpen() ||
		m.confirmOverlay.IsOpen() ||
		m.mcpInitOverlay.IsOpen() {
		m.display = m.display.updateContent()
		return m
	}

	if m.focusedWindow == focusDisplay {
		m = m.focusDisplay()
	} else {
		m = m.focusInput()
	}
	m.display = m.display.updateContent()
	return m
}

// handlePaste routes a block of text the terminal delivered as one unit
// (bracketed paste; the editor handoff has its own path) to the box that owns the
// keyboard. The three answers are the three targets that can take text, and the
// silence of the rest is deliberate:
//
//   - the prompt, or an open overlay's filter box, takes it;
//   - a list with the caret (Tab moved there) has no text box, so a paste is
//     discarded rather than moved out from under the user's target;
//   - the display pane and a modal discard it for the same reason — the keys that
//     belong there are commands, not text.
//
// Discarding is a decision, not a fallback: it is made in one place, from state
// the user set, and not by whichever box happens to have been told to look
// inactive. `input_routing_test.go` pins the whole table.
func (m Terminal) handlePaste(msg PasteMsg) (Terminal, Cmd) {
	switch m.keyboardTarget() {
	case targetPrompt:
		var results []Result
		m.input, results = m.input.Update(msg)
		return m.foldResults(results)

	case targetOverlayFilter:
		// (b) the open overlay's own box — whichever overlay that is, so the
		// answer is the same in all four.
		return m.pasteIntoOverlay(msg)

	default:
		return m, nil
	}
}

// pasteIntoOverlay hands the block to the overlay that owns the keyboard, and
// re-runs whatever filtering that overlay does on a value change. Each overlay
// keeps its own item logic; what it no longer keeps is a decision about whether
// it is the target.
func (m Terminal) pasteIntoOverlay(msg PasteMsg) (Terminal, Cmd) {
	switch {
	case m.attachmentWindow.IsOpen():
		var results []Result
		m.attachmentWindow, results = m.attachmentWindow.Update(msg)
		return m.foldResults(results)
	case m.modelSelector.IsOpen():
		var results []Result
		m.modelSelector, results = m.modelSelector.Update(msg)
		return m.foldResults(results)
	case m.themeSelector.IsOpen():
		var results []Result
		m.themeSelector, results = m.themeSelector.Update(msg)
		return m.foldResults(results)
	case m.helpWindow.IsOpen():
		var results []Result
		m.helpWindow, results = m.helpWindow.Update(msg)
		return m.foldResults(results)
	}
	return m, nil
}

// inputTarget is the single answer to "which box owns the keyboard".
type inputTarget int

const (
	// targetNothing is the loading screen: no box exists yet to own anything,
	// so keys and pastes are discarded rather than queued for a box that is not
	// drawn.
	targetNothing inputTarget = iota
	// targetPrompt is the prompt box, the only text box outside an overlay.
	targetPrompt
	// targetDisplay is the scrollback pane: commands, no text.
	targetDisplay
	// targetOverlayFilter is an open overlay's filter box.
	targetOverlayFilter
	// targetOverlayList is an open overlay's list: arrows and Enter, no text.
	targetOverlayList
	// targetModal is a yes/no dialog, which answers its own keys only.
	targetModal
)

// String names the target for a failure line. A switch rather than an array
// indexed by the value, because a target added later would turn a diagnostic into
// a panic at the moment a test was trying to explain itself.
func (t inputTarget) String() string {
	switch t {
	case targetNothing:
		return "nothing"
	case targetPrompt:
		return "prompt"
	case targetDisplay:
		return "display"
	case targetOverlayFilter:
		return "overlay-filter"
	case targetOverlayList:
		return "overlay-list"
	case targetModal:
		return "modal"
	}
	return fmt.Sprintf("inputTarget(%d)", int(t))
}

// keyboardTarget resolves the owner of the keyboard from state the user changed
// and nothing else. It walks the same input layer stack dispatch walks
// (inputLayers), stopping at the first layer that owns text — so routing and
// dispatch cannot disagree about who is on top, which two hand-written chains
// could. Loading is first because the screen has no boxes on it; a modal, then
// an overlay, then the pane the user toggled to.
//
// It is a function rather than a field so that there is no second copy of the
// answer to disagree with the first: every component that needs it asks, and the
// painting of a box asks too (View derives PromptInput's register from here), so
// routing and rendering cannot drift apart the way they did when a blur wrote
// both.
func (m Terminal) keyboardTarget() inputTarget {
	for _, layer := range m.inputLayers() {
		switch layer {
		case layerLoading:
			return targetNothing
		case layerModal:
			return targetModal
		case layerOverlay:
			return m.overlayTextTarget()
		case layerPane:
			if m.focusedWindow == focusDisplay {
				return targetDisplay
			}
			return targetPrompt
		}
	}
	return targetNothing
}

// overlayTextTarget is the text target an open overlay holds: its filter box when
// the filter is focused, its list otherwise. Callers reach it only when an
// overlay is open (inputLayers put layerOverlay in the stack).
func (m Terminal) overlayTextTarget() inputTarget {
	switch {
	case m.attachmentWindow.IsOpen():
		return m.overlayFilterOrList(m.attachmentWindow.FilterInputFocused)
	case m.modelSelector.IsOpen():
		return m.overlayFilterOrList(m.modelSelector.FilterInputFocused)
	case m.themeSelector.IsOpen():
		return m.overlayFilterOrList(m.themeSelector.FilterInputFocused)
	case m.helpWindow.IsOpen():
		return m.overlayFilterOrList(m.helpWindow.FilterInputFocused)
	}
	return targetNothing
}

func (m Terminal) overlayFilterOrList(filter bool) inputTarget {
	if filter {
		return targetOverlayFilter
	}
	return targetOverlayList
}
