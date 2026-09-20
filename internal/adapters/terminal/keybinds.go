package terminal

// Key handling for the terminal UI.
// This file provides key bindings and the key handler.
// Key strings are as reported by bubbletea's KeyMsg.String().

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/alayacore/alayacore/internal/commands"
	"github.com/alayacore/alayacore/internal/platform"
	"github.com/alayacore/alayacore/internal/theme"
)

// mcpAuthTimeout bounds the Phase 3 wait for the OAuth callback, mirroring
// the plainio adapter. On expiry the flow stops the callback server and
// tells the user to continue manually with :mcp_confirm.
const mcpAuthTimeout = 5 * time.Minute

// ============================================================================
// Key Bindings
// ============================================================================

// ============================================================================
// Key Handler
// ============================================================================

// handleKeyMsg routes keyboard input down the input layer stack (see
// input_layers.go). The first layer that consumes the key wins; a layer that
// does not apply passes to the next. The order is the one inputLayers defines,
// so this function no longer carries a second copy of the priority.
func (m Terminal) handleKeyMsg(msg KeyMsg) (Terminal, Cmd) {
	for _, layer := range m.inputLayers() {
		switch layer {
		case layerLoading:
			// The loading screen has no boxes; every key is dropped.
			return m, nil

		case layerUniversal:
			if msg.Chord() == keyCtrlZ {
				return m, Suspend
			}

		case layerModal:
			tm, cmd, _ := m.handlePriorityOverlayKeys(msg)
			return tm, cmd

		case layerOverlay:
			tm, cmd, _ := m.handleSelectorOverlayKeys(msg)
			return tm, cmd

		case layerGlobal:
			if msg.Chord() == keyTab {
				return m.toggleFocus(), nil
			}
			if tm, cmd, handled := m.handleGlobalKeys(msg); handled {
				return tm, cmd
			}

		case layerPane:
			return m.handlePaneKeys(msg)
		}
	}
	return m, nil
}

// handlePaneKeys handles keys when a pane (display or prompt) owns the keyboard.
func (m Terminal) handlePaneKeys(msg KeyMsg) (Terminal, Cmd) {
	switch m.focusedWindow {
	case focusDisplay:
		return m.handleDisplayKeys(msg)
	case focusInput:
		return m.handleInputKeys(msg)
	}
	return m, nil
}

// handleThemeSelectorKeys handles input when theme selector is open.
func (m Terminal) handleThemeSelectorKeys(msg KeyMsg) (Terminal, Cmd) {
	wasOpen := m.themeSelector.IsOpen()

	ts, results := m.themeSelector.Update(msg)
	m.themeSelector = ts
	m, cmd := m.foldResults(results)

	// If closed — restore original theme on cancel, or apply selected theme.
	if wasOpen && !ts.IsOpen() {
		// Invalidate any pending preview debounce: a tick scheduled by the
		// last navigation may not have fired yet, and applying it after the
		// overlay closed would leave the wrong theme on screen (cancel
		// restores the original, then the stale tick re-applies the
		// preview). handleThemePreview only applies when the tick's ID
		// still matches, so bump the counter.
		m.themePreviewID++
		key := msg.Chord()
		if key == keyEsc {
			// Cancel: restore original theme if a different theme was previewed.
			lastApplied := m.previewAppliedTheme
			m.previewAppliedTheme = nil
			if lastApplied != nil && lastApplied != m.selectorOriginalTheme {
				originalThemeName := ts.GetOriginalThemeName()
				snap := m.out.SnapshotStatus()
				for _, t := range snap.CachedThemes {
					if t.Name == originalThemeName && t.Theme != nil {
						m = m.applyTheme(t.Theme)
						break
					}
				}
			}
			m.selectorOriginalTheme = nil
		}
		m = m.restoreFocus()
		return m, cmd
	}

	// Apply preview theme with debounce on any navigation or filter change.
	previewTheme := ts.GetPreviewTheme()
	if previewTheme != nil {
		m.themePreviewID++
		id := m.themePreviewID
		p := previewTheme
		return m, Batch(cmd, Tick(ThemePreviewDebounce, func(_ time.Time) Msg {
			return themePreviewMsg{theme: p, id: id}
		}))
	}

	return m, cmd
}

// themePreviewMsg is sent when a theme preview should be applied
type themePreviewMsg struct {
	theme *theme.Theme
	id    int // ID to check if this preview is still current
}

func (m Terminal) handleThemePreview(msg themePreviewMsg) Terminal {
	// Only apply if this preview is still the current one (debouncing)
	if msg.id == m.themePreviewID {
		m = m.applyTheme(msg.theme)
		m.previewAppliedTheme = msg.theme
	}
	return m
}

func (m Terminal) handleConfirmQuit(r *ConfirmResult, fromCmd bool) (Terminal, Cmd) {
	if r.Confirmed {
		// Ask the session to end rather than closing the pipes and returning
		// Quit: the session is what decides whether it is done, and it may
		// still be finishing a task. Exiting here would orphan the tool
		// processes that task spawned (they are in their own session and
		// receive no terminal signal) and skip the auto-save at its end. The
		// exit itself happens when the session's terminal frame arrives — see
		// handleTick.
		m.quitting = true
		return m, m.emitCommand(":" + commands.CommandNameQuit)
	}
	if fromCmd {
		m.input = m.input.WithValue("")
	}
	m = m.restoreFocusAfterConfirm()
	return m, nil
}

func (m Terminal) handleConfirmCancel(r *ConfirmResult, fromCmd bool) (Terminal, Cmd) {
	if fromCmd {
		m.input = m.input.WithValue("")
	}
	m = m.restoreFocusAfterConfirm()
	if r.Confirmed {
		return m.submitCommand(commands.CommandNameCancel, fromCmd)
	}
	return m, nil
}

func (m Terminal) handleConfirmTool(r *ConfirmResult, fromCmd bool) (Terminal, Cmd) {
	if fromCmd {
		m.input = m.input.WithValue("")
	}

	var cmd Cmd
	if r.Confirmed {
		cmd = m.emitCommand(":" + commands.CommandNameToolConfirm + " " + r.ToolID)
	} else {
		cmd = m.emitCommand(":" + commands.CommandNameToolDecline + " " + r.ToolID)
	}

	m = m.restoreFocusAfterConfirm()
	if nextID, nextName, nextInput, ok := m.out.GetPendingToolConfirm(); ok {
		m = m.openConfirmTool(nextID, nextName, nextInput)
	}
	return m, Batch(cmd, scheduleTick())
}

func (m Terminal) handleConfirmMCPAuth(r *ConfirmResult, fromCmd bool) (Terminal, Cmd) {
	if fromCmd {
		m.input = m.input.WithValue("")
	}

	var cmd Cmd
	switch {
	case r.Confirmed:
		cmd = m.startMCPAuthFlow(r.ToolID, r.ToolInput)
	case r.CtrlGCanceled:
		m.out.ClearMCPAuths()
		cmd = m.emitCommand(":" + commands.CommandNameMCPSkip)
	default:
		cmd = m.emitCommand(":" + commands.CommandNameMCPDecline + " " + r.ToolID)
	}

	m = m.restoreFocusAfterConfirm()
	if nextServer, nextURL, ok := m.out.GetPendingMCPAuth(); ok {
		m = m.openConfirmMCPAuth(nextServer, nextURL)
	}
	return m, Batch(cmd, scheduleTick())
}

// startMCPAuthFlow starts the OAuth callback server, opens the browser,
// and returns a Cmd that waits for the authorization code.
// The callback server is started synchronously (needed before the Cmd);
// all user-facing I/O (notification, browser, TLV writes) runs in the Cmd.
//
// Uses Sequence to split into phases so all display output (notify/error)
// flows through messages handled by Terminal.Update rather than direct calls
// to m.out from inside a goroutine.
//
// Phase 3 blocks on resultCh for up to mcpAuthTimeout. Because the
// sequence runs in its own goroutine (Program.execSequence is dispatched
// with `go ...`), the wait only stalls that goroutine — the main loop
// continues to drain p.msgs, so the input loop is unaffected.
func (m Terminal) startMCPAuthFlow(serverName, authURL string) Cmd {
	state := platform.RandomState()

	resultCh, redirectURI, cleanup := platform.StartCallbackServer("127.0.0.1:0", state, serverName)

	encodedRedirect := url.QueryEscape(redirectURI)
	filledURL := authURL
	filledURL = strings.ReplaceAll(filledURL, "{{redirect_uri}}", encodedRedirect)
	filledURL = strings.ReplaceAll(filledURL, "{{state}}", state)

	// Capture streamInput for TLV writes in phase 2 (it's a pointer, safe to capture)
	streamInput := m.streamInput

	return Sequence(
		// Phase 1: Notify user and try to open browser
		func() Msg {
			return displayNotifyMsg{
				message: fmt.Sprintf("Authorizing %s. If your browser doesn't open, open this URL:\n%s",
					serverName, filledURL),
			}
		},
		// Phase 2: Open browser, report error if any
		func() Msg {
			if err := platform.OpenURL(filledURL); err != nil {
				return displayErrorMsg{
					message: fmt.Sprintf("Failed to open browser: %v", err),
				}
			}
			return nil
		},
		// Phase 3: Wait for OAuth callback
		func() Msg {
			select {
			case res := <-resultCh:
				cleanup()
				if res.Err != nil {
					// Decline only this server — keeps the rest of MCP
					// init (and any other servers' authorizations) intact,
					// mirroring the plainio adapter.
					writeCommand(streamInput, fmt.Sprintf(":%s %s", commands.CommandNameMCPDecline, serverName))
					return displayErrorMsg{
						message: fmt.Sprintf("MCP auth callback error: %v", res.Err),
					}
				}
				cmd := fmt.Sprintf(":%s %s %s %s", commands.CommandNameMCPConfirm, serverName, res.Code, redirectURI)
				if res.Iss != "" {
					cmd += " " + res.Iss
				}
				writeCommand(streamInput, cmd)
				return nil
			case <-time.After(mcpAuthTimeout):
				cleanup()
				return displayErrorMsg{
					message: fmt.Sprintf("MCP authorization for %q timed out — continue manually with :mcp_confirm %s <code> <redirect_uri>",
						serverName, serverName),
				}
			}
		},
	)
}

// restoreFocusAfterConfirm restores input/display focus only if no overlay
// is still open. If another overlay (e.g. model selector) was active before
// the confirm appeared, it remains active — the overlay naturally catches
// keys in handleKeyMsg.
func (m Terminal) restoreFocusAfterConfirm() Terminal {
	if m.modelSelector.IsOpen() || m.themeSelector.IsOpen() ||
		m.helpWindow.IsOpen() || m.mcpInitOverlay.IsOpen() {
		m.display = m.display.WithBlocked(m.isBlocked())
		m.display = m.display.updateContent()
		return m
	}
	m = m.restoreFocus()
	return m
}

// handleOverlayModelSelector handles keyboard input when the model selector is open.
func (m Terminal) handleOverlayModelSelector(msg KeyMsg) (Terminal, Cmd) {
	wasOpen := m.modelSelector.IsOpen()
	ms, results := m.modelSelector.Update(msg)
	m.modelSelector = ms
	m, cmd := m.foldResults(results)
	if wasOpen && !ms.IsOpen() {
		m = m.restoreFocus()
	}
	return m, cmd
}

// handleMCPInitKeys handles keyboard input when the MCP init overlay is open.
func (m Terminal) handleMCPInitKeys(msg KeyMsg) (Terminal, Cmd) {
	if msg.Chord() == keyCtrlG {
		return m, Batch(
			m.emitCommand(":"+commands.CommandNameMCPSkip),
			scheduleTick(),
		)
	}
	return m, nil
}

// handlePriorityOverlayKeys handles the highest-priority overlays that
// block all other interaction (confirm dialog, MCP init overlay).
func (m Terminal) handlePriorityOverlayKeys(msg KeyMsg) (Terminal, Cmd, bool) {
	if m.confirmOverlay.IsOpen() {
		tm, cmd := m.handleOverlayConfirm(msg)
		return tm, cmd, true
	}
	if m.mcpInitOverlay.IsOpen() {
		tm, cmd := m.handleMCPInitKeys(msg)
		return tm, cmd, true
	}
	return m, nil, false
}

// handleSelectorOverlayKeys handles selector-style overlays (theme, model,
// attachment, help) that are mutually exclusive.
func (m Terminal) handleSelectorOverlayKeys(msg KeyMsg) (Terminal, Cmd, bool) {
	if m.themeSelector.IsOpen() {
		tm, cmd := m.handleThemeSelectorKeys(msg)
		return tm, cmd, true
	}
	if m.modelSelector.IsOpen() {
		tm, cmd := m.handleOverlayModelSelector(msg)
		return tm, cmd, true
	}
	if m.attachmentWindow.IsOpen() {
		aw := m.attachmentWindow
		t := trackOverlay(aw)
		var results []Result
		aw, results = aw.Update(msg)
		m.attachmentWindow = aw
		// Folding applies an AttachmentSelectedMsg — adding the file — before
		// the focus is restored, which is the order the result's meaning wants:
		// the selection lands, then the pane the user returns to is decided.
		var cmd Cmd
		m, cmd = m.foldResults(results)
		if t.JustClosed(aw) {
			m = m.restoreFocus()
		}
		return m, cmd, true
	}
	if m.helpWindow.IsOpen() {
		hw := m.helpWindow
		t := trackOverlay(hw)
		var results []Result
		hw, results = hw.Update(msg)
		m.helpWindow = hw
		var cmd Cmd
		m, cmd = m.foldResults(results)
		if t.JustClosed(hw) {
			m = m.restoreFocus()
		}
		return m, cmd, true
	}
	return m, nil, false
}

// handleOverlayConfirm handles keyboard input when the confirm dialog is open.
func (m Terminal) handleOverlayConfirm(msg KeyMsg) (Terminal, Cmd) {
	cd, results := m.confirmOverlay.Update(msg)
	m.confirmOverlay = cd
	return m.foldResults(results)
}

// handleConfirmResult processes a ConfirmResult (triggered by ConfirmResultMsg).
func (m Terminal) handleConfirmResult(r *ConfirmResult) (Terminal, Cmd) {
	if r == nil {
		return m, nil
	}

	fromCmd := m.confirmFromCommand
	m.confirmFromCommand = false

	switch r.Kind {
	case ConfirmQuit:
		return m.handleConfirmQuit(r, fromCmd)
	case ConfirmCancel:
		return m.handleConfirmCancel(r, fromCmd)
	case ConfirmTool:
		return m.handleConfirmTool(r, fromCmd)
	case ConfirmMCPAuth:
		return m.handleConfirmMCPAuth(r, fromCmd)
	}
	return m, nil
}

func (m Terminal) handleDisplayKeys(msg KeyMsg) (Terminal, Cmd) {
	var results []Result
	m.display, results = m.display.Update(msg)
	return m.foldResults(results)
}

// handleGlobalKeys handles global keyboard shortcuts.
func (m Terminal) handleGlobalKeys(msg KeyMsg) (Terminal, Cmd, bool) {
	switch msg.Chord() {
	case keyCtrlG:
		m = m.openConfirmCancel()
		m.confirmFromCommand = false
		return m, nil, true

	case keyCtrlS:
		tm, cmd := m.handleSaveKey()
		return tm, cmd, true

	case keyCtrlL:
		m = m.openModelSelector()
		return m, nil, true

	case keyCtrlP:
		m = m.openThemeSelector()
		return m, nil, true

	case keyCtrlR:
		tm, cmd := m.handleRedraw()
		return tm, cmd, true

	case keyCtrlH, keyF1:
		m = m.openHelpWindow()
		return m, nil, true
	}

	return m, nil, false
}

// handleSaveKey handles the Ctrl+S save shortcut.
// If no session file is bound, it focuses the input and inserts ":save "
// so the user can type a filename (same pattern as Ctrl+F for :fork).
// If a session file is bound, it submits the save command directly.
func (m Terminal) handleSaveKey() (Terminal, Cmd) {
	if m.appConfig.Cfg.Session == "" {
		m = m.focusInput()
		m.input = m.input.WithValue(":" + commands.CommandNameSave + " ")
		m.input = m.input.CursorEnd()
		m.display = m.display.updateContent()
		return m, nil
	}
	return m.submitCommand(commands.CommandNameSave, false)
}

// handleRedraw handles the Ctrl+R force-redraw shortcut.
//
// Returns a Cmd that delivers a forceRepaintMsg: when the event loop
// processes it, the Program clears both Program.lastView and the Screen
// frame caches, then re-renders the current view synchronously. This
// guarantees a full clear+repaint on the next terminal write — even
// when the view content is byte-identical to the previous frame (e.g.
// garbage on screen, terminal out of sync, emulator quirk).
//
// Replaces the previous two-layer approach:
//   - the old `\x1b[0m` content-suffix trick made the view bytes differ
//     to defeat the same-content check, which forced two diff renders
//     per Ctrl-R (one for adding the suffix, one for removing it on
//     the next toggle); and
//   - the synthetic WindowSizeMsg trick relied on Screen.Resize clearing
//     lastContent, which worked but coupled redraw to resize handling.
//
// The single-message approach clears caches directly and produces
// exactly one full repaint per Ctrl-R.
func (m Terminal) handleRedraw() (Terminal, Cmd) {
	m.display = m.display.ForceContentDirty()
	m.display = m.display.updateContent()
	return m, ForceRepaintCmd()
}

// handleInputKeys handles keys when the input field is focused.
// It processes submit, editor open, attachment, newline, and clear commands,
// then delegates unrecognized keys to PromptInput.
func (m Terminal) handleInputKeys(msg KeyMsg) (Terminal, Cmd) {
	switch msg.Chord() {
	case keyEnter:
		return m.handleSubmit()
	case keyCtrlJ:
		// Ctrl+J inserts a line break instead of submitting. It is the
		// terminal-independent way to compose multi-line prompt text: on a
		// host without bracketed paste, the newlines of pasted text arrive as
		// this same byte (LF is Ctrl+J), so they land here as content rather
		// than as submissions. Enter keeps submitting — it is the byte the
		// Enter key produces, and nothing a user pastes can be confused with
		// it. See docs/tui.md → "Paste and terminal capability".
		m.input = m.input.InsertNewline()
		return m, nil
	case keyCtrlA:
		m = m.openAttachmentWindow()
		return m, nil
	case keyCtrlC:
		m.input = m.input.WithValue("")
		m = m.clearAttachments()
		return m, nil
	}

	var results []Result
	m.input, results = m.input.Update(msg)
	return m.foldResults(results)
}

// ============================================================================
// Command Handling
// ============================================================================

// handleSubmit sends the current input as the prompt. It is what Enter does,
// and Shift+Enter does not reach here as a different key on most hosts: the
// line-break binding is Ctrl+J (see handleInputKeys and docs/tui.md → "Paste and
// terminal capability"), which is the only byte that distinguishes the two
// intentions without asking the terminal for a capability it may not have.
func (m Terminal) handleSubmit() (Terminal, Cmd) {
	// The line's grammar decides what it is: a command never travels as prompt
	// text and prompt text never reaches the command path.
	sub := parseSubmission(m.input.Value())
	if command, ok := sub.(CommandSubmission); ok {
		return m.handleCommand(command)
	}
	prompt, ok := sub.(PromptSubmission)
	if !ok {
		// parseSubmission returns exactly the two types above; defensive.
		return m, nil
	}

	// If a task is running, reject without clearing input.
	if m.inProgress {
		return m, func() Msg {
			return displayErrorMsg{
				message: "A task is already running. Wait for it to complete or cancel it.",
			}
		}
	}

	// Nothing to send
	if prompt.Text == "" && len(m.pendingAttachments) == 0 {
		return m, nil
	}

	// Capture resources, clear state, return Cmd for I/O
	attachments := m.pendingAttachments
	writer := m.streamInput
	m.input = m.input.WithValue("")
	m = m.clearAttachments()

	return m, Batch(
		submitCmd(writer, attachments, prompt.Text),
		scheduleTick(),
	)
}

// handleCommand processes a command line (the ":"-form, already parsed).
func (m Terminal) handleCommand(sub CommandSubmission) (Terminal, Cmd) {
	// An adapter-local command is matched on its name with no arguments: a
	// command that carries arguments is the session's, which is what the old
	// whole-string comparison did (":quit foo" is not the local quit).
	if sub.Args == "" {
		switch sub.Name {
		case cmdQuit, cmdQShort:
			m = m.openConfirmQuit()
			m.confirmFromCommand = true
			return m, nil

		case cmdCancel:
			m = m.openConfirmCancel()
			m.confirmFromCommand = true
			return m, nil

		case cmdSuspend:
			m.input = m.input.WithValue("")
			return m, Suspend

		case cmdHelp:
			m.input = m.input.WithValue("")
			m = m.openHelpWindow()
			return m, nil
		}
	}

	// Everything else is the session's command.
	return m.submitCommand(sub.Raw, true)
}

// submitCommand sends a command to the session and optionally clears input.
func (m Terminal) submitCommand(command string, clearInput bool) (Terminal, Cmd) {
	cmd := m.emitCommand(":" + command)
	if clearInput {
		m.input = m.input.WithValue("")
	}
	return m, Batch(cmd, scheduleTick())
}

// scheduleTick schedules a tick message for UI updates.
func scheduleTick() Cmd {
	return Tick(SubmitTickDelay, func(_ time.Time) Msg {
		return tickMsg{}
	})
}
