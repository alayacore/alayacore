package terminal

// This package implements the terminal UI adapter for AlayaCore.
// It runs on the self-built TUI runtime (program.go) and handles:
//   - Display of assistant output with virtual scrolling
//   - User input with external editor support
//   - Model selection and theme switching
//   - TLV protocol communication with the session

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/commands"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/tlv"
	ansi "github.com/charmbracelet/x/ansi"
)

// ============================================================================
// Command transmission — CI frames
// ============================================================================

// commandSeq generates unique command call IDs for CI frames.
var commandSeq atomic.Uint64

// commandNames maps command call IDs to command names, mirroring how tool
// names are tracked from AF frames: the request carries the name, the
// result (CO) carries only the ID, and the adapter correlates them.
var commandNames sync.Map // id → command name

// writeCommand sends a colon-command (e.g. ":save /tmp/x") to the session as
// a CI (Command Input) frame. The adapter translates the human-facing text
// into {id, name, input}; the session never sees colon-text anymore.
func writeCommand(w io.Writer, cmd string) {
	name, args := commands.SplitCommand(strings.TrimPrefix(cmd, ":"))
	id := fmt.Sprintf("tui-%d", commandSeq.Add(1))
	payload, err := json.Marshal(protocol.CmdMsg{
		ID:    id,
		Name:  name,
		Input: args,
	})
	if err != nil {
		return
	}
	commandNames.Store(id, name)
	_ = tlv.WriteTLV(w, tlv.TagCommandIn, string(payload))
}

// ============================================================================
// Async Session Loading Messages
// ============================================================================

// sessionLoadedMsg is sent when the async session loading completes.
type sessionLoadedMsg struct{}

// sessionLoadingErrorMsg is sent when the async session loading fails.
type sessionLoadingErrorMsg struct {
	err error
}

// ============================================================================
// Overlay Result Messages
// ============================================================================

// ThemeSelectedMsg is sent when the user selects a theme.
type ThemeSelectedMsg struct{ Name string }

// ModelSelectedMsg is sent when the user selects a model.
type ModelSelectedMsg struct{ ID int }

// ReloadModelsMsg is sent when the user requests a model reload.
type ReloadModelsMsg struct{}

// HelpCmdMsg is sent when the user selects a command from the help window.
type HelpCmdMsg struct{ Command string }

// AttachmentSelectedMsg is sent when the user selects a file or URL to attach.
type AttachmentSelectedMsg struct{ Path string }

// ConfirmResultMsg is sent when a confirm dialog produces a result.
type ConfirmResultMsg struct{ Result *ConfirmResult }

// displayErrorMsg carries an error message to be written to the display output
// via Terminal.Update, ensuring all OutputWriter mutations happen on the event loop.
type displayErrorMsg struct {
	message string
}

// displayNotifyMsg carries a notification message to be written to the display
// output via Terminal.Update.
type displayNotifyMsg struct {
	message string
}

// openEditorForDisplayMsg is sent by DisplayModel when the user presses 'e'
// to edit the currently selected window's content in an external editor.
type openEditorForDisplayMsg struct {
	content string
}

// focusInputWithValueMsg is sent by DisplayModel when a display key press
// requires switching focus to the input field and inserting a value.
// Used by ':' (command prefix) and Ctrl-F (fork command).
type focusInputWithValueMsg struct {
	value string
}

// openEditorForPromptMsg is sent by PromptInput when the user presses
// Ctrl+O to edit the current input content in an external editor.
type openEditorForPromptMsg struct {
	content string
}

// emitCommand returns a Cmd that sends a user-level command to the
// session as a CI frame when executed by the event loop.
// Errors are silently ignored — commands are best-effort and the
// session may close the input stream at any time.
func (m Terminal) emitCommand(cmd string) Cmd {
	return func() Msg {
		writeCommand(m.streamInput, cmd)
		return nil
	}
}

// submitCmd returns a Cmd that sends staged content (attachments + text)
// as a complete user message via TLV. Runs outside Update when executed by
// the event loop. Errors reading attachments are returned as
// displayErrorMsg so the event loop handles the display write.
func submitCmd(w io.WriteCloser, attachments []attachment, prompt string) Cmd {
	return func() Msg {
		var errs []string
		for _, a := range attachments {
			var value string
			if a.isURL {
				value = a.path
			} else {
				data, err := os.ReadFile(a.path)
				if err != nil {
					errs = append(errs, fmt.Sprintf("Failed to read attachment: %s", err))
					continue
				}
				mime := tlv.MimeTypeForPath(a.path)
				b64 := base64.StdEncoding.EncodeToString(data)
				value = fmt.Sprintf("data:%s;base64,%s", mime, b64)
			}
			_ = tlv.WriteTLV(w, a.tag, value)
		}
		if prompt != "" {
			_ = tlv.WriteTLV(w, tlv.TagUserT, prompt)
		}
		_ = tlv.WriteTLV(w, tlv.TagUserEnd, "")

		if len(errs) > 0 {
			return displayErrorMsg{
				message: strings.Join(errs, "\n"),
			}
		}
		return nil
	}
}

// addAttachment adds a local file path to pending attachments.
func (m Terminal) addAttachment(path string) Terminal {
	tag := tlv.TagForPath(path)
	m.pendingAttachments = append(m.pendingAttachments, attachment{path: path, tag: tag})
	m.input = m.input.WithAttachments(m.pendingAttachmentLabels())
	m = m.updateDisplayHeight()
	return m
}

// addURLAttachment adds a URL to pending attachments.
func (m Terminal) addURLAttachment(url string) Terminal {
	tag := tlv.TagForPath(url)
	m.pendingAttachments = append(m.pendingAttachments, attachment{path: url, tag: tag, isURL: true})
	m.input = m.input.WithAttachments(m.pendingAttachmentLabels())
	m = m.updateDisplayHeight()
	return m
}

// clearAttachments clears all pending attachments.
func (m Terminal) clearAttachments() Terminal {
	m.pendingAttachments = nil
	m.input = m.input.WithAttachments(nil)
	m = m.updateDisplayHeight()
	return m
}

// pendingAttachmentLabels returns display labels for all pending attachments.
func (m Terminal) pendingAttachmentLabels() []string {
	labels := make([]string, len(m.pendingAttachments))
	for i, a := range m.pendingAttachments {
		labels[i] = tlv.MediaLabel(a.tag)
	}
	return labels
}

// ============================================================================
// Constants
// ============================================================================

const (
	DefaultWidth  = 80
	DefaultHeight = 20

	// Component sizing
	InputPaddingH     = 8  // horizontal padding for input fields (border + padding both sides)
	SelectorMaxHeight = 30 // maximum height for model selector and similar overlays
	SelectorListRows  = 8  // content rows inside selector borders
	LayoutGap         = 4  // non-content rows subtracted for selector/list sizing
)

// Timing constants
const (
	TickInterval    = 250 * time.Millisecond // polling during streaming
	SubmitTickDelay = 50 * time.Millisecond  // delay before first tick after submit
)

// Focus constants
const (
	focusInput   = "input"
	focusDisplay = "display"
)

// ============================================================================
// Terminal Model
// ============================================================================

// Terminal is the main model that composes display, input, and status components.
// It serves as the central coordinator for the terminal UI, managing:
//   - User input and keyboard shortcuts (delegated to keybinds.go)
//   - Display updates from the agent session
//   - Model selection and switching
//   - Theme selection and switching
//   - Window focus management
//
// Terminal is the root model.
//
// Field groups are separated by blank lines:
//
//	Elm UI state  — value types, copied on every Update.
//	Transient     — one-shot or infrequently-set flags.
//	Dependencies  — pointer types for external services / shared singletons.
//	Loading       — async session startup state (transient, set once).
//
// All Elm UI state fields use value receivers; all dependency fields use
// pointers.  See docs/tui-architecture.md for the rationale.
type Terminal struct {
	// ── Elm UI state (value types, copied on every Update) ──────────────
	display            DisplayModel     // conversation display with virtual scrolling
	input              PromptInput      // text input, attachments, focus
	modelSelector      ModelSelector    // model switching overlay
	themeSelector      ThemeSelector    // theme switching overlay
	helpWindow         HelpWindow       // keybinding help overlay
	confirmOverlay     ConfirmDialog    // quit/cancel/tool confirm dialogs
	mcpInitOverlay     ConfirmDialog    // MCP initialization progress overlay
	attachmentWindow   AttachmentWindow // file/URL attachment picker
	focusedWindow      string           // which pane has focus: "input" or "display"
	statusText         string           // status bar text (active)
	statusTextDim      string           // status bar text (dimmed, out of focus)
	inProgress         bool             // whether a task is currently running
	windowWidth        int              // terminal width in cells
	windowHeight       int              // terminal height in cells
	activeTheme        string           // last theme name from system info updates
	appliedTheme       string           // last theme name that was visually applied
	forceRedraw        uint64           // odd → append invisible SGR reset so renderer repaints
	pendingAttachments []attachment     // pending file attachments for multi-modal input

	// ── Transient state (set once or infrequently, not Elm-copied semantically) ─
	quitting              bool         // terminal is shutting down
	confirmFromCommand    bool         // cancel came from :cancel command (vs Ctrl+G)
	hasFocus              bool         // terminal has OS-level application focus
	themePreviewID        int          // debounce ID for pending theme preview
	previewAppliedTheme   *theme.Theme // last theme data applied by a preview (nil = none)
	selectorOriginalTheme *theme.Theme // theme data active when selector opened
	pendingForceRedraw    bool         // Ctrl-R sets this; handleWindowSize consumes it

	// ── Dependencies (pointer types, shared, not copied semantically) ──
	out          OutputWriter   // TLV output writer (shared, thread-safe)
	streamInput  io.WriteCloser // TLV input writer (shared, thread-safe)
	pipeReader   *io.PipeReader // read end of input pipe; set before async loading
	appConfig    *app.Config    // application configuration (read-only)
	editor       *Editor        // external editor process helper (stateless service)
	themeManager *ThemeManager  // theme loader (reads from disk, read-only after init)
	styles       *Styles        // derived styles (computed once, replaced on theme switch)

	// ── Async session loading (transient, set once in Init / first tick) ─
	loading      bool
	loadingError error
	postLoading  bool
}

// attachment represents a pending file attachment for multi-modal input.
type attachment struct {
	path  string // file path or URL
	tag   string // TLV tag: TagUserI, TagUserV, TagUserA, or TagUserD
	isURL bool   // true if it's a URL (sent as-is, no file reading)
}

// NewTerminalWithTheme creates a new Terminal model with a custom theme.
// themeName is the name of the initial theme (used for tracking session-driven theme changes).
func NewTerminalWithTheme(
	out OutputWriter,
	inputWriter io.WriteCloser,
	appCfg *app.Config,
	initialWidth, initialHeight int,
	theme *theme.Theme,
	themeManager *ThemeManager,
	themeName string,
) Terminal {
	styles := NewStyles(theme)

	editor := NewEditor()

	modelSelector := NewModelSelector(styles)
	themeSelector := NewThemeSelector(styles)
	helpWindow := NewHelpWindow(styles)
	confirmOverlay := NewConfirmDialog(styles)
	mcpInitOverlay := NewConfirmDialog(styles)
	attachmentWindow := NewAttachmentWindow(styles)

	m := Terminal{
		out:              out,
		streamInput:      inputWriter,
		appConfig:        appCfg,
		editor:           editor,
		display:          NewDisplayModel(out.WindowBuffer(), styles),
		input:            NewPromptInput(styles),
		themeManager:     themeManager,
		modelSelector:    modelSelector,
		themeSelector:    themeSelector,
		helpWindow:       helpWindow,
		confirmOverlay:   confirmOverlay,
		mcpInitOverlay:   mcpInitOverlay,
		attachmentWindow: attachmentWindow,
		focusedWindow:    focusInput,
		windowWidth:      initialWidth,
		windowHeight:     initialHeight,
		styles:           styles,
		hasFocus:         true,
		activeTheme:      themeName,
		appliedTheme:     themeName,
	}

	// Initialize component sizes
	m.input = m.input.WithWidth(initialWidth)
	m = m.updateComponentSizes(initialWidth, initialHeight)
	m = m.updateDisplayHeight()

	return m
}

// Init starts the periodic tick loop for processing session updates.
// When loading is true, it also kicks off async session loading.
func (m Terminal) Init() Cmd {
	var cmds []Cmd

	// Display any buffered init errors via messages so OutputWriter
	// mutations go through Terminal.Update like all other display writes.
	if m.themeManager != nil {
		if errs := m.themeManager.GetInitErrors(); len(errs) > 0 {
			for _, e := range errs {
				err := e // capture
				cmds = append(cmds, func() Msg {
					return displayErrorMsg{
						message: err.Message,
					}
				})
			}
		}
	}

	cmds = append(cmds,
		Tick(TickInterval, func(_ time.Time) Msg {
			return tickMsg{}
		}),
	)

	// If in loading mode, kick off async session loading.
	if m.loading {
		cmds = append(cmds, m.loadSessionCmd())
	}

	return Batch(cmds...)
}

// loadSessionCmd returns a Cmd that runs app.StartSession in a goroutine.
// It is only used when the TUI starts in loading mode (m.loading == true).
func (m Terminal) loadSessionCmd() Cmd {
	return func() Msg {
		_, _, err := app.StartSession(m.appConfig, m.out, m.pipeReader)
		if err != nil {
			return sessionLoadingErrorMsg{err: err}
		}
		return sessionLoadedMsg{}
	}
}

// Update handles all incoming messages and routes them to appropriate handlers.
// Messages are processed in order of priority:
//  1. KeyMsg - keyboard input (highest priority for responsiveness)
//  2. WindowSizeMsg - terminal resize
//  3. tickMsg - periodic updates for display and model switching
//  4. Editor messages - external editor completion
//  5. Focus/Blur - application focus changes
//  6. Paste - clipboard paste
//
//nolint:gocyclo // message dispatch over many Msg types; each case is a simple handler call
func (m Terminal) Update(msg Msg) (Model, Cmd) {
	// Sync display dim state at the start of every update cycle.
	m.display = m.display.WithBlocked(m.isBlocked())

	// Loading message handling — these take priority during startup.
	switch msg := msg.(type) {
	case sessionLoadedMsg:
		return m.handleSessionLoadedMsg()

	case sessionLoadingErrorMsg:
		return m.handleSessionLoadingError(msg.err)
	}

	switch msg := msg.(type) {
	case KeyMsg:
		return m.handleKeyMsg(msg)

	case WindowSizeMsg:
		return m.handleWindowSize(msg), nil

	case tickMsg:
		return m.handleTick()

	case ThemeSelectedMsg:
		return m, m.emitCommand(":" + commands.CommandNameThemeSet + " " + msg.Name)

	case ModelSelectedMsg:
		return m, m.emitCommand(fmt.Sprintf(":%s %d", commands.CommandNameModelSet, msg.ID))

	case ReloadModelsMsg:
		return m, m.emitCommand(":" + commands.CommandNameModelLoad)

	case ConfirmResultMsg:
		return m.handleConfirmResult(msg.Result)

	case themePreviewMsg:
		return m.handleThemePreview(msg), nil

	case editorStartMsg:
		return m.handleEditorStart(msg)

	case openEditorForDisplayMsg:
		return m, m.editor.OpenForDisplay(msg.content)

	case focusInputWithValueMsg:
		m = m.focusInput()
		m.input = m.input.WithValue(msg.value).CursorEnd()
		m.display = m.display.updateContent()
		return m, nil

	case openEditorForPromptMsg:
		return m, m.editor.Open(msg.content)

	case displayErrorMsg:
		m.out.WriteError(msg.message)
		return m, nil

	case displayNotifyMsg:
		m.out.WriteNotify(msg.message)
		return m, nil

	case EditorFinishedMsg:
		return m.handleEditorFinished(msg)

	case BlurMsg:
		return m.handleBlur(), nil

	case FocusMsg:
		return m.handleFocus(), nil

	case PasteMsg:
		return m.handlePaste(msg)

	default:
		return m, nil
	}
}

// tickMsg is sent periodically to update the display.
type tickMsg struct{}

// handleWindowSize handles terminal resize events.
func (m Terminal) handleWindowSize(msg WindowSizeMsg) Terminal {
	m.windowWidth = msg.Width
	m.windowHeight = msg.Height

	// Update all components
	m.out.SetWindowWidth(max(0, msg.Width))
	m.input = m.input.WithWidth(max(0, msg.Width))
	m = m.updateComponentSizes(msg.Width, msg.Height)
	m = m.updateDisplayHeight()

	// Clamp cursor to valid bounds (windows may have been removed) but
	// don't scroll to make it visible — the user's scroll position is
	// preserved across resizes and suspend/resume cycles.
	m.display = m.display.ClampCursor()

	// If this is a synthetic resize triggered by Ctrl-R, consume the flag.
	// The view toggle already happened in handleRedraw, and resize() just
	// armed the renderer's clear flag — the next flush will do a full
	// clear+repaint.
	if m.pendingForceRedraw {
		m.pendingForceRedraw = false
	}

	// Re-render display content with new width (windowBuffer was marked dirty by SetWindowWidth)
	m.display = m.display.updateContent()

	return m
}

// handleTick processes periodic updates for display and model switching.
func (m Terminal) handleTick() (Terminal, Cmd) {
	// During async loading, the only periodic task is to re-render the
	// loading screen (spinner animation). Skip all session-driven updates.
	if m.loading {
		return m, Tick(TickInterval, func(_ time.Time) Msg {
			return tickMsg{}
		})
	}

	m = m.handleMCPOverlays()

	// First tick after loading: initialization is done — restore input
	// focus if no overlay is blocking it.
	if m.postLoading {
		m.postLoading = false
		if !m.isBlocked() {
			m = m.restoreFocus()
		}
	}

	var cmd Cmd
	m, cmd = m.handleDisplayRefresh()
	return m, Batch(
		Tick(TickInterval, func(_ time.Time) Msg {
			return tickMsg{}
		}),
		cmd,
	)
}

// handleMCPOverlays manages all MCP overlay state in one place.
// Called on every tick.
//
// The init overlay (mcpInitOverlay) persists throughout MCP init.
// The confirm overlay (confirmOverlay) handles auth confirm and tool
// confirm as temporary dialogs on top of the init overlay.
func (m Terminal) handleMCPOverlays() Terminal {
	wasOpen := m.mcpInitOverlay.IsOpen()
	var action OverlayAction
	m, action = m.handleMCPProgress()
	if action.CloseInitOverlay {
		m = m.restoreFocusAfterConfirm()
	}
	if action.InitOverlayActive || action.OpenedConfirm {
		// MCP init overlay just opened — blur input so its border renders
		// as blurred (empty box) rather than focused but unreachable.
		if action.InitOverlayActive && !wasOpen {
			m.input = m.input.Blur()
		}
		m.display = m.display.WithBlocked(true)
		m.display = m.display.updateContent()
	}
	return m
}

// handleSessionLoadedMsg is called when the async session loading completes.
// It transitions the UI from the loading spinner to the normal TUI view,
// applying the loaded theme, populating the model selector, and preparing
// for MCP initialization if needed.
func (m Terminal) handleSessionLoadedMsg() (Terminal, Cmd) {
	m.loading = false
	m.postLoading = true

	// Drain any buffered output written during loading.
	m.out.FlushPendingDeltas()
	m.out.DrainDirty()

	// Reset appliedTheme so the session's theme data (from sendThemeListMsg)
	// is always applied on initial load, even if the theme name matches the
	// loading screen theme. This ensures that if the user modified a theme
	// file (e.g. theme-dark.conf), the changes take effect immediately.
	m.appliedTheme = ""

	if m.out.WindowBuffer().WindowCount() > 0 {
		m = m.updateStatus()
		m = m.updateDisplayHeight()
		if m.display.shouldFollow() {
			m.display = m.display.WithCursorToLastWindow()
		}
		m.display = m.display.updateContent()
	}

	// Sync theme from the now-loaded session state.
	// (Fallback when there are no windows yet — updateStatus above already
	// handles the normal case via syncThemeFromSession.)
	snap := m.out.SnapshotStatus()
	m = m.syncThemeFromSession(snap.ActiveTheme, snap.ActiveThemeData)

	// Populate model selector from the now-loaded model list.
	modelSnap := m.out.SnapshotModels()

	// Blur the input and try to open MCP init overlay immediately.
	// The input stays blurred (rendered as empty box) until the first
	// tick determines whether MCP init is needed.
	m.input = m.input.Blur()
	m = m.handleMCPOverlays()

	ms, cmd := m.modelSelector.LoadModels(modelSnap.Models, modelSnap.ActiveID)
	m.modelSelector = ms
	return m, cmd
}

// handleSessionLoadingError is called when the async session loading fails.
// It transitions the UI to a quitting state with the error recorded.
func (m Terminal) handleSessionLoadingError(err error) (Terminal, Cmd) {
	m.loading = false
	m.loadingError = err
	m.quitting = true
	return m, Quit
}

// handleDisplayRefresh checks if the display needs updating and returns
// a Cmd for model selector updates if models changed.
func (m Terminal) handleDisplayRefresh() (Terminal, Cmd) {
	// Flush pending deltas first so the WindowBuffer has the latest content
	// before we check the dirty flag.
	m.out.FlushPendingDeltas()

	if !m.out.DrainDirty() {
		m = m.updateStatus()
		return m, nil
	}

	if m.out.WindowBuffer().WindowCount() > 0 {
		m = m.updateStatus()
		m = m.updateDisplayHeight()
		if m.display.shouldFollow() {
			m.display = m.display.WithCursorToLastWindow()
		}
		m.display = m.display.updateContent()
	}

	modelSnap := m.out.SnapshotModels()
	ms, cmd := m.modelSelector.LoadModels(modelSnap.Models, modelSnap.ActiveID)
	m.modelSelector = ms
	return m, cmd
}

// handleEditorFinished handles completion of the external editor.
// Dispatches based on the EditorAction to handle different editor scenarios:
//
//   - EditorActionNone:          view-only (display), no side effects
//   - EditorActionUpdateInput:  update input field with editor content
func (m Terminal) handleEditorFinished(msg EditorFinishedMsg) (Terminal, Cmd) {
	if msg.Err != nil {
		return m, func() Msg {
			return displayErrorMsg{
				message: fmt.Sprintf("Editor error: %v", msg.Err),
			}
		}
	}

	switch msg.Action {
	case EditorActionNone:
		// View-only (display window viewing) — nothing to do
		return m, nil

	case EditorActionUpdateInput:
		if msg.Content != "" {
			// Strip trailing newlines that text editors add by default.
			content := strings.TrimRight(msg.Content, "\n")
			m.input = m.input.WithValue(content)
			m.input = m.input.CursorEnd()
			m = m.focusInput()
		}
		return m, nil

	default:
		return m, nil
	}
}

// updateDisplayHeight updates the display viewport height based on window size
// and current input box height (which varies when attachments are present).
func (m Terminal) updateDisplayHeight() Terminal {
	// Layout from bottom up:
	//   line H:          status bar (fixed, 1 line)
	//   separator:       1 newline between input and status
	//   lines above:     input box (dynamic, based on attachments)
	//   separator:       1 newline between display and input
	//   remaining lines: display (elastic)
	//
	// Total = display + inputBox + statusBar = H
	inputBoxHeight := m.input.Height()
	m.display = m.display.WithHeight(max(0, m.windowHeight-inputBoxHeight-1))
	m.display = m.display.updateContent()
	return m
}

// syncThemeFromSession updates the applied theme when the session reports a change.
func (m Terminal) syncThemeFromSession(sessionTheme string, themeData *theme.Theme) Terminal {
	if m.appliedTheme == sessionTheme || sessionTheme == "" {
		return m
	}
	if themeData != nil {
		m = m.applyTheme(themeData)
	}
	m.appliedTheme = sessionTheme
	return m
}

// View renders the complete terminal UI.
func (m Terminal) View() View {
	// Loading screen: shown while the session is being loaded asynchronously.
	if m.loading {
		return m.renderLoadingView()
	}
	if m.loadingError != nil {
		// Should not normally be reached since we quit on error,
		// but provide a fallback view just in case.
		v := NewView(fmt.Sprintf("Session loading failed: %v\n", m.loadingError))
		v.AltScreen = true
		return v
	}

	var sb strings.Builder

	// Display area — base row sequence. We pad to viewportHeight rows
	// inside m.display so its content fills the display region exactly.
	sb.WriteString(m.display.View().Content)

	// Input area — drawn at an ABSOLUTE screen position with a CUP so
	// its location does not depend on the display area's actual row
	// count (which can drift from viewportHeight when soft-wrap or
	// scroll-induced fragment count changes happen). Top rule sits at
	// (m.windowHeight - inputHeight - 1) (0-indexed); the box content
	// (attachments, separator, input field) follows, then the bottom rule.
	//
	// The status bar sits at the last row (m.windowHeight - 1, 0-indexed)
	// and is also positioned with a CUP — guarantees the status bar is
	// always visible, even if the input box's rendered content overshoots
	// its computed height (e.g. a misbehaving renderer or an attachment
	// line that the terminal soft-wraps despite wrapLabels pre-wrapping it).
	//
	// '\n' separators before each CUP:
	//   - input box strings have no trailing '\n', so without a separator
	//     before its CUP, the status bar would land on the same row as
	//     the input box's bottom rule (and look glued to it);
	//   - m.display.View().Content also has no trailing '\n', so without
	//     a separator before the input-box CUP, the display's last row
	//     and the input top rule would merge into one segment under
	//     soft-wrap, losing a row and breaking the FullScreen invariant
	//     (View content must soft-wrap to exactly screen height).
	// The CUPs are absolute, so the '\n' does not change where the
	// subsequent content lands — only what the soft-wrap row counter
	// sees, which is what we need.
	m.input = m.input.WithBlocked(m.isBlocked() || m.postLoading)
	inputBoxHeight := m.input.Height()
	inputBoxY := max(0, m.windowHeight-inputBoxHeight-1) // 0-indexed row of the top rule
	if m.windowHeight > 0 {
		sb.WriteString("\n")
		sb.WriteString(ansi.CursorPosition(1, inputBoxY+1))
		sb.WriteString(m.input.View().Content)
		sb.WriteString("\n")
		// Status bar: hard anchor to the last row.
		sb.WriteString(ansi.CursorPosition(1, m.windowHeight))
		sb.WriteString(m.renderStatusBar())
	}

	baseContent := sb.String()

	// Render all overlay layers through the overlay manager.
	overlayContent, _ := m.renderOverlays(baseContent, m.windowWidth, m.windowHeight, m.forceRedraw&1 == 1)

	v := NewView(overlayContent)
	v.AltScreen = true
	// The frame soft-wraps to exactly the screen height (viewport padded,
	// every row padded to the terminal width, input box + status fill the
	// rest), so it can be rendered by overwriting without clearing first
	// — no ED2 flicker during streaming (see Screen.Render).
	v.FullScreen = true
	// Raw passthrough mode (forked bubbletea): the content is written
	// verbatim to the terminal so it soft-wraps natively — window
	// fragments (continuous text padded to the terminal width) wrap
	// exactly at the visual lines, preserving copy fidelity (no fake
	// newlines) and terminal continuation features. The stock
	// cell-buffer renderer truncates wide lines and cannot do this.
	v.Raw = true
	v.ReportFocus = true

	// Position the real terminal cursor at the active text input: the focused
	// overlay filter box when one is open, otherwise the prompt input. A
	// visible real cursor is required for IME on-the-spot preedit rendering
	// and stable candidate-window anchoring. The real cursor stays hidden
	// when neither input has focus (display pane focused, app blurred, or
	// overlays without a text input).
	if x, y, ok := m.overlayCursorPosition(); ok {
		v.Cursor = m.newCursor(x, y)
	} else if m.input.IsFocused() && !m.isBlocked() {
		// y: display area occupies [0, displayH); the input box's top rule
		// sits at displayH; content starts after the rule; then attachment
		// lines (+ separator). The input renders a single content line, so
		// the cursor's line index within the value does not affect y.
		y := max(0, m.windowHeight-m.input.Height()-1) // display height
		y++                                            // input box top rule
		y += m.input.AttachmentsOffset()
		// x: open boxes have no side border or padding, so the cursor cell
		// is the horizontal position directly (prompt is empty for the
		// main input).
		x := m.input.CursorCell()
		v.Cursor = m.newCursor(x, y)
	}

	return v
}

// newCursor returns a steady block cursor in the theme's cursor color at the
// given screen position (cells relative to the frame's top-left corner).
func (m Terminal) newCursor(x, y int) *Cursor {
	cur := NewCursor(x, y)
	cur.Shape = CursorBlock
	cur.Blink = false
	cur.Color = m.styles.CursorColor
	return cur
}

// overlayCursorPosition returns the screen position of the active overlay's
// filter input cursor. ok is true only when an overlay with a text input is
// open and its filter box has focus (see FilteredListCore.CursorPosition).
func (m Terminal) overlayCursorPosition() (x, y int, ok bool) {
	switch {
	case m.modelSelector.IsOpen():
		return m.modelSelector.CursorPosition(m.windowWidth, m.windowHeight)
	case m.themeSelector.IsOpen():
		return m.themeSelector.CursorPosition(m.windowWidth, m.windowHeight)
	case m.helpWindow.IsOpen():
		return m.helpWindow.CursorPosition(m.windowWidth, m.windowHeight)
	case m.attachmentWindow.IsOpen():
		return m.attachmentWindow.CursorPosition(m.windowWidth, m.windowHeight)
	}
	return 0, 0, false
}

// renderLoadingView renders the loading screen shown while the session is
// being loaded asynchronously. It displays a centered message with a simple
// spinner animation (updated by tickMsg) so the user gets instant feedback
// even on slow machines.
func (m Terminal) renderLoadingView() View {
	// Simple spinner frames.
	spinner := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	frame := int(time.Now().UnixMilli()/150) % len(spinner)
	spinnerChar := spinner[frame]

	msg := fmt.Sprintf(" %s Loading session...", spinnerChar)
	// Center the message vertically and horizontally (ASCII width ~= len).
	contentWidth := len(msg)
	padX := max(0, (m.windowWidth-contentWidth)/2)
	padY := max(0, m.windowHeight/2-1)

	var sb strings.Builder
	sb.WriteString(strings.Repeat("\n", padY))
	sb.WriteString(strings.Repeat(" ", padX))
	sb.WriteString(msg)
	sb.WriteString("\n")

	v := NewView(sb.String())
	v.AltScreen = true
	return v
}

// ensure Terminal implements Model
var _ Model = Terminal{}

func (m Terminal) applyTheme(theme *theme.Theme) Terminal {
	m.styles = NewStyles(theme)
	m.out.WithStyles(m.styles)
	m.display = m.display.WithStyles(m.styles)
	m.input = m.input.WithStyles(m.styles)
	m.modelSelector = m.modelSelector.WithStyles(m.styles)
	m.themeSelector = m.themeSelector.WithStyles(m.styles)
	m.helpWindow = m.helpWindow.WithStyles(m.styles)
	m.confirmOverlay = m.confirmOverlay.WithStyles(m.styles)
	m.mcpInitOverlay = m.mcpInitOverlay.WithStyles(m.styles)
	m.attachmentWindow = m.attachmentWindow.WithStyles(m.styles)
	m.display = m.display.updateContent()
	return m
}

// updateComponentSizes updates the size of all overlay components.
func (m Terminal) updateComponentSizes(width, height int) Terminal {
	m.modelSelector = m.modelSelector.WithSize(width, height)
	m.themeSelector = m.themeSelector.WithSize(width, height)
	m.helpWindow = m.helpWindow.WithSize(width, height)
	m.confirmOverlay = m.confirmOverlay.WithSize(width, height)
	m.mcpInitOverlay = m.mcpInitOverlay.WithSize(width, height)
	m.attachmentWindow = m.attachmentWindow.WithSize(width, height)
	return m
}

// isBlocked returns true when the user's view is covered by any overlay
// that prevents interaction with the prompt input.
func (m Terminal) isBlocked() bool {
	return m.modelSelector.IsOpen() || m.themeSelector.IsOpen() ||
		m.helpWindow.IsOpen() || m.attachmentWindow.IsOpen() ||
		m.confirmOverlay.IsOpen() || m.mcpInitOverlay.IsOpen()
}

// handleMCPProgress manages all MCP overlay state.
// Extracted from the former OverlayManager.
func (m Terminal) handleMCPProgress() (Terminal, OverlayAction) {
	out := m.out
	if out.ConsumeSessionReady() {
		if m.mcpInitOverlay.IsOpen() {
			m.mcpInitOverlay = m.mcpInitOverlay.Close()
			return m, OverlayAction{CloseInitOverlay: true}
		}
		return m, OverlayAction{}
	}

	if !m.confirmOverlay.IsOpen() {
		if id, name, input, ok := out.GetPendingToolConfirm(); ok {
			m.confirmOverlay = m.confirmOverlay.OpenTool(id, name, input)
			return m, OverlayAction{OpenedConfirm: true}
		}
	}

	if !m.confirmOverlay.IsOpen() {
		if server, url, ok := out.GetPendingMCPAuth(); ok {
			m.confirmOverlay = m.confirmOverlay.OpenMCPAuth(server, url)
			return m, OverlayAction{OpenedConfirm: true}
		}
	}

	st := out.SnapshotStatus()
	if st.MCPStatus != "" && st.MCPStatus != "done" {
		if m.mcpInitOverlay.IsOpen() {
			m.mcpInitOverlay = m.mcpInitOverlay.UpdateMCPInitProgress(st.MCPServers)
		} else {
			m.mcpInitOverlay = m.mcpInitOverlay.OpenMCPInit()
			m.mcpInitOverlay = m.mcpInitOverlay.UpdateMCPInitProgress(st.MCPServers)
		}
		return m, OverlayAction{InitOverlayActive: true}
	}

	return m, OverlayAction{}
}

// renderOverlays applies all overlay layers to the base content and returns
// the final view string.
func (m Terminal) renderOverlays(baseContent string, width, height int, forceRedraw bool) (string, bool) {
	overlayContent := baseContent

	switch {
	case m.modelSelector.IsOpen():
		overlayContent = m.modelSelector.RenderOverlay(baseContent, width, height)
	case m.themeSelector.IsOpen():
		overlayContent = m.themeSelector.RenderOverlay(baseContent, width, height)
	case m.helpWindow.IsOpen():
		overlayContent = m.helpWindow.RenderOverlay(baseContent, width, height)
	case m.attachmentWindow.IsOpen():
		overlayContent = m.attachmentWindow.RenderOverlay(baseContent, width, height)
	}

	if m.mcpInitOverlay.IsOpen() {
		overlayContent = m.mcpInitOverlay.RenderOverlay(overlayContent, width, height)
	}

	if m.confirmOverlay.IsOpen() {
		fullContent := m.confirmOverlay.RenderOverlay(overlayContent, width, height)
		if forceRedraw {
			fullContent += "\x1b[0m"
		}
		return fullContent, true
	}

	if forceRedraw {
		overlayContent += "\x1b[0m"
	}
	return overlayContent, false
}

// OverlayAction describes what happened during handleMCPProgress.
type OverlayAction struct {
	CloseInitOverlay  bool
	OpenedConfirm     bool
	InitOverlayActive bool
}
