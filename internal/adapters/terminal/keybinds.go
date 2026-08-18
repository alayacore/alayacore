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

// handleKeyMsg routes keyboard input to the appropriate handler.
func (m Terminal) handleKeyMsg(msg KeyMsg) (Terminal, Cmd) {
	// During async session loading, ignore all keyboard input.
	if m.loading {
		return m, nil
	}

	// Ctrl+Z works from any context, including overlays
	if msg.String() == keyCtrlZ {
		return m, Suspend
	}

	// Priority overlays (confirm, MCP init)
	if tm, cmd, handled := m.handlePriorityOverlayKeys(msg); handled {
		return tm, cmd
	}

	// Selector overlays (theme, model, attachment, help)
	if tm, cmd, handled := m.handleSelectorOverlayKeys(msg); handled {
		return tm, cmd
	}

	// Tab toggles focus between display and input
	if msg.String() == keyTab {
		m = m.toggleFocus()
		return m, nil
	}

	// Global shortcuts (work from any context)
	if tm, cmd, handled := m.handleGlobalKeys(msg); handled {
		return tm, cmd
	}

	// Focus-specific key handling
	switch m.focusedWindow {
	case focusDisplay:
		return m.handleDisplayKeys(msg)

	case focusInput:
		return m.handleInputKeys(msg)

	default:
		return m, nil
	}
}

// handleThemeSelectorKeys handles input when theme selector is open.
func (m Terminal) handleThemeSelectorKeys(msg KeyMsg) (Terminal, Cmd) {
	wasOpen := m.themeSelector.IsOpen()

	ts, cmd := m.themeSelector.Update(msg)
	m.themeSelector = ts

	// If closed — restore original theme on cancel, or apply selected theme.
	if wasOpen && !ts.IsOpen() {
		// Invalidate any pending preview debounce: a tick scheduled by the
		// last navigation may not have fired yet, and applying it after the
		// overlay closed would leave the wrong theme on screen (cancel
		// restores the original, then the stale tick re-applies the
		// preview). handleThemePreview only applies when the tick's ID
		// still matches, so bump the counter.
		m.themePreviewID++
		key := msg.String()
		if key == keyQ || key == keyEsc {
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
		m.quitting = true
		return m, Sequence(
			func() Msg {
				m.streamInput.Close()
				m.out.Close()
				return nil
			},
			Quit,
		)
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
	ms, cmd := m.modelSelector.Update(msg)
	m.modelSelector = ms
	if wasOpen && !ms.IsOpen() {
		m = m.restoreFocus()
	}
	return m, cmd
}

// handleMCPInitKeys handles keyboard input when the MCP init overlay is open.
func (m Terminal) handleMCPInitKeys(msg KeyMsg) (Terminal, Cmd) {
	if msg.String() == keyCtrlG {
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
		aw, cmd := aw.Update(msg)
		m.attachmentWindow = aw
		if t.JustClosed(aw) {
			// Check if a file/URL was selected (via AttachmentSelectedMsg)
			if cmd != nil {
				if resultMsg := cmd(); resultMsg != nil {
					if ac, ok := resultMsg.(AttachmentSelectedMsg); ok {
						if strings.HasPrefix(ac.Path, "http://") || strings.HasPrefix(ac.Path, "https://") {
							m = m.addURLAttachment(ac.Path)
						} else {
							m = m.addAttachment(ac.Path)
						}
					}
				}
			}
			m = m.restoreFocus()
		}
		return m, cmd, true
	}
	if m.helpWindow.IsOpen() {
		hw := m.helpWindow
		t := trackOverlay(hw)
		hw, cmd := hw.Update(msg)
		m.helpWindow = hw
		if t.JustClosed(hw) {
			// Check if a command was selected (via HelpCmdMsg)
			if cmd != nil {
				if resultMsg := cmd(); resultMsg != nil {
					if hc, ok := resultMsg.(HelpCmdMsg); ok {
						m = m.focusInput()
						m.input = m.input.WithValue(hc.Command + " ")
						m.input = m.input.CursorEnd()
						m.display = m.display.updateContent()
						return m, nil, true
					}
				}
			}
			m = m.restoreFocus()
		}
		return m, nil, true
	}
	return m, nil, false
}

// handleOverlayConfirm handles keyboard input when the confirm dialog is open.
func (m Terminal) handleOverlayConfirm(msg KeyMsg) (Terminal, Cmd) {
	cd, cmd := m.confirmOverlay.Update(msg)
	m.confirmOverlay = cd

	if cmd == nil {
		return m, nil
	}

	resultMsg := cmd()
	if resultMsg == nil {
		return m, nil
	}

	if r, ok := resultMsg.(ConfirmResultMsg); ok {
		// ConfirmResultMsg must be processed synchronously
		// (modifies Terminal state inline)
		return m.handleConfirmResult(r.Result)
	}

	// Other messages (e.g. openEditorForDisplayMsg):
	// re-wrap and let Terminal.Update handle them normally
	return m, func() Msg { return resultMsg }
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
	var cmd Cmd
	m.display, cmd = m.display.Update(msg)
	return m, cmd
}

// handleGlobalKeys handles global keyboard shortcuts.
func (m Terminal) handleGlobalKeys(msg KeyMsg) (Terminal, Cmd, bool) {
	switch msg.String() {
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
// Layer 1 (synchronous, always works): toggle forceRedraw so View()
// appends/removes an invisible SGR reset, making the view content differ
// from the last rendered frame.  This guarantees the next flush won't
// early-return.
//
// Layer 2 (best-effort, arm full repaint): the synthetic WindowSizeMsg
// lands in Screen.Resize, which clears the frame caches so the next
// render is a full clear+repaint instead of a diff.  If it is dropped
// (rare), the view change from layer 1 still ensures a diff-based redraw
// that covers every content cell.
func (m Terminal) handleRedraw() (Terminal, Cmd) {
	m.forceRedraw++
	m.display = m.display.ForceContentDirty()
	m.display = m.display.updateContent()

	m.pendingForceRedraw = true
	return m, func() Msg {
		return WindowSizeMsg{Width: m.windowWidth, Height: m.windowHeight}
	}
}

// handleInputKeys handles keys when the input field is focused.
// It processes submit, editor open, attachment, and clear commands,
// then delegates unrecognized keys to PromptInput.
func (m Terminal) handleInputKeys(msg KeyMsg) (Terminal, Cmd) {
	switch msg.String() {
	case keyEnter:
		return m.handleSubmit()
	case keyCtrlA:
		m = m.openAttachmentWindow()
		return m, nil
	case keyCtrlC:
		m.input = m.input.WithValue("")
		m = m.clearAttachments()
		return m, nil
	}

	var cmd Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// ============================================================================
// Command Handling
// ============================================================================

// handleSubmit processes the input when Shift+Enter is pressed.
func (m Terminal) handleSubmit() (Terminal, Cmd) {
	prompt := strings.TrimSpace(m.input.Value())

	// Check if it's a command (starts with ":") — ignore attachments for commands.
	if command, found := strings.CutPrefix(prompt, ":"); found {
		return m.handleCommand(strings.TrimSpace(command))
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
	if prompt == "" && len(m.pendingAttachments) == 0 {
		return m, nil
	}

	// Capture resources, clear state, return Cmd for I/O
	attachments := m.pendingAttachments
	writer := m.streamInput
	m.input = m.input.WithValue("")
	m = m.clearAttachments()

	return m, Batch(
		submitCmd(writer, attachments, prompt),
		scheduleTick(),
	)
}

// handleCommand processes a command string (without the ":" prefix).
func (m Terminal) handleCommand(command string) (Terminal, Cmd) {
	// Quit command
	if command == cmdQuit || command == cmdQShort {
		m = m.openConfirmQuit()
		m.confirmFromCommand = true
		return m, nil
	}

	// Cancel command
	if command == cmdCancel {
		m = m.openConfirmCancel()
		m.confirmFromCommand = true
		return m, nil
	}

	// Suspend command - suspends the process (like Ctrl+Z)
	if command == cmdSuspend {
		m.input = m.input.WithValue("")
		return m, Suspend
	}

	// Help command - opens help window locally, not sent to session
	if command == cmdHelp {
		m.input = m.input.WithValue("")
		m = m.openHelpWindow()
		return m, nil
	}

	// All other commands - pass through to session
	return m.submitCommand(command, true)
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
