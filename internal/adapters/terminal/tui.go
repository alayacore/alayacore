package terminal

// This package implements the terminal UI adapter for AlayaCore.
// It runs on the self-built TUI runtime (program.go) and handles:
//   - Display of assistant output with virtual scrolling
//   - User input with external editor support
//   - Model selection and theme switching
//   - frame protocol communication with the session

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/commands"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/tools"
	"github.com/alayacore/alayacore/internal/wire"
	ansi "github.com/charmbracelet/x/ansi"
)

// ============================================================================
// Command transmission — CI frames
// ============================================================================

// tuiCommands is this adapter's CI writer: the event loop sends a command through
// it, and the output side reads that command's name back when its CO arrives
// (output.go), since a CO carries only the id. See commands.Writer.
var tuiCommands = commands.NewWriter("tui")

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

// focusPromptWithValueMsg is sent by DisplayModel when a display key press
// requires switching focus to the input field and inserting a value.
// Used by ':' (command prefix) and Ctrl-F (fork command).
type focusPromptWithValueMsg struct {
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
		_, _ = tuiCommands.Write(m.sessionPipe, cmd)
		return nil
	}
}

// submitCmd returns a Cmd that sends staged content (attachments + text)
// as a complete user message. Runs outside Update when executed by the event
// loop. Anything that stopped the message from being sent is returned as
// displayErrorMsg so the event loop handles the display write.
func submitCmd(w io.WriteCloser, attachments []attachment, prompt string) Cmd {
	return func() Msg {
		var errs []string

		// send reports a failed write instead of dropping it. The frame stream is
		// the session's input: an error means it is gone, and the rest of this
		// message cannot arrive either, so sending stops there. Silently ignoring
		// it — which is what this did — is how an attachment disappears while the
		// text around it is delivered, leaving the model to answer a question
		// about a file it never saw.
		send := func(tag, body string) bool {
			if err := wire.WriteFrame(w, tag, body); err != nil {
				errs = append(errs, fmt.Sprintf("Failed to send the message: %s", err))
				return false
			}
			return true
		}

		for _, a := range attachments {
			value, problem := attachmentValue(a)
			if problem != "" {
				errs = append(errs, problem)
				continue
			}
			if !send(wire.TagUserX, contentBody(a.kind, value)) {
				return reportErrs(errs)
			}
		}
		if prompt != "" && !send(wire.TagUserX, contentBody(protocol.TypeText, prompt)) {
			return reportErrs(errs)
		}
		if !send(wire.TagUserEnd, "") {
			return reportErrs(errs)
		}
		return reportErrs(errs)
	}
}

// An attachment is bounded by tools.MaxMediaBytes, the media policy itself: the
// question is the same one a tool reading a file asks — how much media may one
// message embed as a data URI — so it has one answer, not two that have to be kept
// in step. The frame that media becomes is a third larger again (base64) plus its
// wrapper, which is why the media bound and the frame bound are one decision; see
// internal/wire's MaxFrameSize.

// attachmentValue returns the frame value for one attachment — the file as a
// base64 data URI, or its URL — or, as the second result, the message to show
// the user instead. The message is a string rather than an error because it is
// shown as written, not wrapped into a diagnostic.
func attachmentValue(a attachment) (value, problem string) {
	if a.isURL {
		return a.path, ""
	}
	info, err := os.Stat(a.path)
	if err != nil {
		return "", fmt.Sprintf("Failed to read attachment: %s", err)
	}
	if info.Size() > tools.MaxMediaBytes {
		return "", fmt.Sprintf("Attachment %s is %.1fMB — over the %dMB media limit. Media is embedded as base64 and cannot be truncated; attach a smaller file, or attach a URL and let the model's provider fetch it.",
			filepath.Base(a.path), float64(info.Size())/(1024*1024), tools.MaxMediaBytes/(1024*1024))
	}
	data, err := os.ReadFile(a.path)
	if err != nil {
		return "", fmt.Sprintf("Failed to read attachment: %s", err)
	}
	return fmt.Sprintf("data:%s;base64,%s",
		wire.MimeTypeForPath(a.path), base64.StdEncoding.EncodeToString(data)), ""
}

// reportErrs tasks accumulated failures into the message the event loop shows,
// or nil when there is nothing to say.
func reportErrs(errs []string) Msg {
	if len(errs) == 0 {
		return nil
	}
	return displayErrorMsg{message: strings.Join(errs, "\n")}
}

// addAttachment adds a local file path to pending attachments.
func (m Terminal) addAttachment(path string) Terminal {
	kind := wire.KindForPath(path)
	m.pendingAttachments = append(m.pendingAttachments, attachment{path: path, kind: kind})
	m.promptBox = m.promptBox.WithAttachments(m.pendingAttachmentLabels())
	m = m.updateDisplayHeight()
	return m
}

// addURLAttachment adds a URL to pending attachments.
func (m Terminal) addURLAttachment(url string) Terminal {
	kind := wire.KindForPath(url)
	m.pendingAttachments = append(m.pendingAttachments, attachment{path: url, kind: kind, isURL: true})
	m.promptBox = m.promptBox.WithAttachments(m.pendingAttachmentLabels())
	m = m.updateDisplayHeight()
	return m
}

// clearAttachments clears all pending attachments.
func (m Terminal) clearAttachments() Terminal {
	m.pendingAttachments = nil
	m.promptBox = m.promptBox.WithAttachments(nil)
	m = m.updateDisplayHeight()
	return m
}

// pendingAttachmentLabels returns display labels for all pending attachments.
func (m Terminal) pendingAttachmentLabels() []string {
	labels := make([]string, len(m.pendingAttachments))
	for i, a := range m.pendingAttachments {
		labels[i] = wire.MediaLabel(a.kind)
	}
	return labels
}

// contentBody marshals a UX frame body: a content item with the given kind.
func contentBody(kind, textOrURI string) string {
	item := protocol.ContentItem{Type: kind}
	if kind == protocol.TypeText {
		item.Text = textOrURI
	} else {
		item.URI = textOrURI
	}
	body, err := json.Marshal(protocol.ContentData{ContentItem: item})
	if err != nil {
		return "{}"
	}
	return string(body)
}

// ============================================================================
// Constants
// ============================================================================

const (
	DefaultWidth  = 80
	DefaultHeight = 20

	// Component sizing
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
	focusPrompt  = "input"
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
	display            DisplayModel      // conversation display with virtual scrolling
	promptBox          PromptInput       // the prompt box: text, attachments, focus
	modelSelector      ModelSelector     // model switching overlay
	themeSelector      ThemeSelector     // theme switching overlay
	helpOverlay        HelpOverlay       // keybinding help overlay
	confirmOverlay     ConfirmDialog     // quit/cancel/tool confirm dialogs
	mcpInitOverlay     ConfirmDialog     // MCP initialization progress overlay
	attachmentOverlay  AttachmentOverlay // file/URL attachment picker
	focusedWindow      string            // which pane has focus: "input" or "display"
	statusLeft         string            // status bar left segments, PLAIN text joined with " | " (no ANSI — styles applied at render time)
	statusRight        string            // status bar right group — the active model name with the reasoning level ("gpt-4o | R2"), or the bare level with no model ("R2"); PLAIN (styled at render time, right-aligned)
	inProgress         bool              // whether a task is currently running
	screenWidth        int               // terminal width in cells
	screenHeight       int               // terminal height in cells
	activeTheme        string            // last theme name from system info updates
	appliedTheme       string            // last theme name that was visually applied
	pendingAttachments []attachment      // pending file attachments for multi-modal input
	lastStatusVersion  uint64            // last StatusSnapshot.Version seen by updateStatus

	lastModelVersion uint64 // last ModelSnapshot.Version seen by the selector rebuild

	// renderedStatusBarCache memoizes the status-bar render output keyed
	// by the inputs that affect it. View() is called on every frame and
	// would otherwise re-run the indicator + truncate + Style.Render
	// pipeline every 250ms tick, only for Program.render to discard
	// the byte-identical result on its same-content identity check.
	//
	// Pointer-typed so the cache survives value copies of Terminal —
	// the Elm-style Update copies the value, but the cache must persist
	// across Updates and Views.
	renderedStatusBarCache *map[renderStatusBarCacheKey]string

	// ── Transient state (set once or infrequently, not Elm-copied semantically) ─
	quitting              bool         // terminal is shutting down
	quitCancelSent        bool         // the wait window's 'c' went out (its next phase takes no keys)
	confirmFromCommand    bool         // cancel came from :cancel command (vs Ctrl+G)
	hasFocus              bool         // terminal has OS-level application focus
	themePreviewID        int          // debounce ID for pending theme preview
	previewAppliedTheme   *theme.Theme // last theme data applied by a preview (nil = none)
	selectorOriginalTheme *theme.Theme // theme data active when selector opened

	// ── Dependencies (pointer types, shared, not copied semantically) ──
	out          OutputWriter   // output writer (shared, thread-safe)
	sessionPipe  io.WriteCloser // input writer (shared, thread-safe)
	pipeReader   *io.PipeReader // read end of input pipe; set before async loading
	appConfig    *app.Config    // application configuration (read-only)
	editor       *Editor        // external editor process helper (stateless service)
	themeManager *theme.Manager // prepares the themes folder; read-only after init
	styles       *Styles        // derived styles (computed once, replaced on theme switch)

	// ── Async session loading (transient, set once in Init / first tick) ─
	loading      bool
	loadingError error
	postLoading  bool
}

// attachment represents a pending file attachment for multi-modal input.
type attachment struct {
	path  string // file path or URL
	kind  string // content kind: wire.KindImage, KindVideo, KindAudio, or KindDocument
	isURL bool   // true if it's a URL (sent as-is, no file reading)
}

// Options is everything the TUI needs before it can draw: where its output and
// its frames go, the session settings it mirrors, the screen it starts on, and the
// theme it draws in.
//
// A struct rather than a parameter list because that list had grown to eight
// values, two of them an adjacent pair of ints — a caller could swap the width and
// the height, and every one of those eight positions had to be remembered.
type Options struct {
	Out       OutputWriter   // session output, parsed into windows as it arrives
	Input     io.WriteCloser // frames to the session
	AppConfig *app.Config    // session settings this UI mirrors; nil in tests

	Width, Height int // initial screen size, in cells

	Theme        *theme.Theme   // the theme to start in
	ThemeManager *theme.Manager // prepares the themes folder; nil when themes are not in play
	ThemeName    string         // the name Theme was loaded as
}

// NewTerminal builds the root model. It does not draw: Run does.
func NewTerminal(opts Options) Terminal {
	styles := NewStyles(opts.Theme)

	editor := NewEditor()

	modelSelector := NewModelSelector(styles)
	themeSelector := NewThemeSelector(styles)
	helpOverlay := NewHelpOverlay(styles)
	confirmOverlay := NewConfirmDialog(styles)
	mcpInitOverlay := NewConfirmDialog(styles)
	attachmentOverlay := NewAttachmentOverlay(styles)

	m := Terminal{
		out:               opts.Out,
		sessionPipe:       opts.Input,
		appConfig:         opts.AppConfig,
		editor:            editor,
		display:           NewDisplayModel(opts.Out.WindowBuffer(), styles),
		promptBox:         NewPromptInput(styles),
		themeManager:      opts.ThemeManager,
		modelSelector:     modelSelector,
		themeSelector:     themeSelector,
		helpOverlay:       helpOverlay,
		confirmOverlay:    confirmOverlay,
		mcpInitOverlay:    mcpInitOverlay,
		attachmentOverlay: attachmentOverlay,
		focusedWindow:     focusPrompt,
		screenWidth:       opts.Width,
		screenHeight:      opts.Height,
		styles:            styles,
		hasFocus:          true,
		activeTheme:       opts.ThemeName,
		appliedTheme:      opts.ThemeName,
	}

	// Initialize component sizes
	m.promptBox = m.promptBox.WithWidth(opts.Width)
	m = m.updateComponentSizes(opts.Width, opts.Height)
	m = m.updateDisplayHeight()

	// Apply the CLI default for markdown rendering to new assistant
	// text windows (--no-markdown turns it off; per-window 'r' toggling
	// is unaffected). Tests pass a nil appCfg — keep the built-in default.
	if opts.AppConfig != nil && opts.AppConfig.Cfg != nil {
		opts.Out.WindowBuffer().SetMarkdownDefault(!opts.AppConfig.Cfg.NoMarkdown)
	}

	return m
}

// Init starts the periodic tick loop for processing session updates.
// When loading is true, it also kicks off async session loading.
func (m Terminal) Init() Cmd {
	var cmds []Cmd

	// Display any buffered init errors via messages so OutputWriter
	// mutations go through Terminal.Update like all other display writes.
	if m.themeManager != nil {
		for _, msg := range m.themeManager.GetLoadErrors() {
			cmds = append(cmds, func() Msg {
				return displayErrorMsg{message: msg}
			})
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
//  2. ScreenSizeMsg - terminal resize
//  3. tickMsg - periodic updates for display and model switching
//  4. Editor messages - external editor completion
//  5. Focus/Blur - application focus changes
//  6. Paste - clipboard paste
//
// Component results — a model or theme selection, an editor request, a confirm
// outcome — are deliberately not messages. A component hands one back as a
// Result value and the dispatcher folds it through applyResult in the same
// Update (see result.go), so there is no round trip and no opaque func() to
// unwrap. What reaches this switch is only input events and asynchronous facts
// from the runtime and the session.
//
// message dispatch over many Msg types; each case is a simple handler call
func (m Terminal) Update(msg Msg) (Model, Cmd) {
	// Sync display dim state at the start of every update cycle.
	m.display = m.display.WithBlocked(m.isBlocked())

	// Loading message handling — these take priority during startup.
	switch msg := msg.(type) {
	case sessionLoadedMsg:
		return m.handleSessionLoadedMsg(), nil

	case sessionLoadingErrorMsg:
		return m.handleSessionLoadingError(msg.err)
	}

	switch msg := msg.(type) {
	case KeyMsg:
		return m.handleKeyMsg(msg)

	case ScreenSizeMsg:
		return m.handleScreenSize(msg), nil

	case tickMsg:
		return m.handleTick()

	case themePreviewMsg:
		return m.handleThemePreview(msg), nil

	case editorStartMsg:
		return m.handleEditorStart(msg)

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
		// Unreachable by construction: Msg is sealed to the set in messages.go,
		// and every message that reaches Update is handled above. Under test it
		// is fatal, so a message type added to the set without a case fails
		// loudly instead of being dropped.
		if failOnUnknownDispatch {
			panic(fmt.Sprintf("terminal: unhandled message %T", msg))
		}
		return m, nil
	}
}

// tickMsg is sent periodically to update the display.
type tickMsg struct{}

// handleScreenSize handles terminal resize events.
func (m Terminal) handleScreenSize(msg ScreenSizeMsg) Terminal {
	m.screenWidth = msg.Width
	m.screenHeight = msg.Height

	// Update all components
	m.out.SetScreenWidth(max(0, msg.Width))
	m.promptBox = m.promptBox.WithWidth(max(0, msg.Width))
	m = m.updateComponentSizes(msg.Width, msg.Height)
	m = m.updateDisplayHeight()

	// Clamp cursor to valid bounds (windows may have been removed) but
	// don't scroll to make it visible — the user's scroll position is
	// preserved across resizes and suspend/resume cycles.
	m.display = m.display.ClampWindowCursor()

	// Re-render display content with new width (windowBuffer was marked dirty by SetScreenWidth)
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

	// The session has announced that it is over (its terminal frame): leave,
	// releasing the input pipe on the way out. The last task's output is
	// already in the WindowBuffer — see handleSessionClosed.
	if m.out.ConsumeSessionClosed() {
		return m.handleSessionClosed(), Quit
	}

	// A quit that has been sent but not honored yet.
	m = m.handleQuitWaitingOverlay()

	m = m.handleMCPOverlays()

	// First tick after loading: initialization is done — restore input
	// focus if no overlay is blocking it.
	if m.postLoading {
		m.postLoading = false
		if !m.isBlocked() {
			m = m.restoreFocus()
		}
	}

	m = m.handleDisplayRefresh()
	return m, Tick(TickInterval, func(_ time.Time) Msg {
		return tickMsg{}
	})
}

// handleSessionClosed finishes the exit sequence after the session's terminal
// frame. It does not flush pending deltas: that frame is an authoritative,
// non-delta frame, so the outputWriter already flushed whatever was pending
// when it arrived — the last task's streaming output is in the WindowBuffer by
// the time we decide to leave, which is the state the final render draws. The
// input pipe is released because the session that read it is gone.
func (m Terminal) handleSessionClosed() Terminal {
	if m.sessionPipe != nil {
		_ = m.sessionPipe.Close()
	}
	return m
}

// handleQuitWaitingOverlay shows the wait window for a quit that the session
// has not honored yet because a task is still running: the session ends when
// that task does, and the user's one remaining choice is to cancel it. Nothing
// is shown when the session is idle — the terminal frame is already on its way
// and the program leaves without a window appearing at all. Called on every
// tick, like the other session-progress overlays.
//
// It takes the confirm overlay while it is up, so a tool confirmation the
// running task asks for in the meantime does not appear behind it: the quit is
// the user's last instruction, and 'c' is the way out of the task that would
// otherwise wait for an answer.
func (m Terminal) handleQuitWaitingOverlay() Terminal {
	if !m.quitting {
		return m
	}
	snap := m.out.SnapshotStatus()
	if !snap.InProgress {
		return m
	}
	if !m.confirmOverlay.IsOpen() {
		if m.quitCancelSent {
			// The cancel went out; the window returns in the phase that only
			// reports progress, since there is nothing left to decide.
			m.confirmOverlay = m.confirmOverlay.OpenQuitCanceling()
		} else {
			m.confirmOverlay = m.confirmOverlay.OpenQuitWaiting()
		}
	}
	m.confirmOverlay = m.confirmOverlay.UpdateQuitWaiting(snap)
	m.display = m.display.WithBlocked(true)
	m.display = m.display.updateContent()
	return m
}

// handleMCPOverlays manages all MCP overlay state in one place.
// Called on every tick.
//
// The init overlay (mcpInitOverlay) persists throughout MCP init.
// The confirm overlay (confirmOverlay) handles auth confirm and tool
// confirm as temporary dialogs on top of the init overlay.
func (m Terminal) handleMCPOverlays() Terminal {
	var action OverlayAction
	m, action = m.handleMCPProgress()
	if action.CloseInitOverlay {
		m = m.restoreFocusAfterConfirm()
	}
	if action.InitOverlayActive || action.OpenedConfirm {
		// A modal owns the screen: the display dims, and the prompt's own
		// blurred register follows from the keyboard target at render time.
		m.display = m.display.WithBlocked(true)
		m.display = m.display.updateContent()
	}
	return m
}

// handleSessionLoadedMsg is called when the async session loading completes.
// It transitions the UI from the loading spinner to the normal TUI view,
// applying the loaded theme, populating the model selector, and preparing
// for MCP initialization if needed.
func (m Terminal) handleSessionLoadedMsg() Terminal {
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
			m.display = m.display.WithWindowCursorToLastWindow()
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

	// Try to open the MCP init overlay immediately. Whether the prompt paints
	// as the live box is derived from the keyboard target each frame (View), so
	// a modal taking the keyboard needs no blur here.
	m = m.handleMCPOverlays()

	m.modelSelector = m.modelSelector.LoadModels(modelSnap.Models, modelSnap.ActiveID)
	return m
}

// handleSessionLoadingError is called when the async session loading fails.
// It transitions the UI to a quitting state with the error recorded.
func (m Terminal) handleSessionLoadingError(err error) (Terminal, Cmd) {
	m.loading = false
	m.loadingError = err
	m.quitting = true
	return m, Quit
}

// handleDisplayRefresh checks if the display needs updating and rebuilds the
// model selector when the model list changed.
func (m Terminal) handleDisplayRefresh() Terminal {
	// Flush pending deltas first so the WindowBuffer has the latest content
	// before we check the dirty flag.
	m.out.FlushPendingDeltas()

	// A task began — a submitted prompt, :continue, :summarize, or any future
	// command that starts one. What follows is what the sender asked to watch,
	// so the view returns to the live edge and follows, as it does on Enter.
	// Above the dirty check on purpose: the refresh below only re-arms follow
	// when it is already on, and this edge must not depend on a frame happening
	// to mark the buffer dirty.
	if m.out.ConsumeTaskStarted() {
		m.display = m.display.FollowLatest()
	}

	// While a tool executes, its spinner must keep rotating even when the
	// tool produces no output (no Uf/Af deltas arrive, so nothing
	// invalidates the window's render cache — the glyph is baked in at the
	// last render). Invalidate executing tool windows on every tick; the
	// call is a no-op when no tool is executing, so idle ticks keep the
	// 100% skip behavior. Checked BEFORE DrainDirty so a stream of
	// unrelated deltas cannot starve the spinner: the invalidation is
	// additive to whatever the drain reports. Note it sets WindowBuffer's
	// own dirty flag (consumed by updateContent via IsDirty), not the
	// outputWriter's drain flag — hence the explicit merge below.
	spinnerRefresh := m.out.WindowBuffer().InvalidateRunningToolSpinners()

	if !m.out.DrainDirty() && !spinnerRefresh {
		m = m.updateStatus()
		return m
	}

	if m.out.WindowBuffer().WindowCount() > 0 {
		m = m.updateStatus()
		m = m.updateDisplayHeight()
		if m.display.shouldFollow() {
			m.display = m.display.WithWindowCursorToLastWindow()
		}
		m.display = m.display.updateContent()
	}

	// Rebuild the model selector only when the model list or active ID has
	// actually changed — LoadModels iterates the list, applies filters,
	// and recomputes scroll, all of which would be wasted work on every
	// tick (4×/sec) when nothing changed.
	modelSnap := m.out.SnapshotModels()
	if m.lastModelVersion != modelSnap.Version || m.lastModelVersion == 0 {
		m.lastModelVersion = modelSnap.Version
		m.modelSelector = m.modelSelector.LoadModels(modelSnap.Models, modelSnap.ActiveID)
	}
	return m
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
			// The buffer comes back through the same rule a paste does, and
			// through the field so its line capacity applies too (input_field.go
			// → WithBlockValue): a Windows editor writes CRLF, and a trailing
			// newline the editor always adds.
			m.promptBox = m.promptBox.WithBlockValue(msg.Content)
			m.promptBox = m.promptBox.CaretEnd()
			m = m.focusPrompt()
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
	//   separator:       1 newline between live edge and input
	//   live edge:       1 line (fixed) — the transcript's closing row
	//   separator:       1 newline between display and live edge
	//   remaining lines: display (elastic)
	//
	// Total = display + liveEdge + inputBox + statusBar = H. The live edge
	// is reserved whether or not it has anything to show — a row that
	// appears and disappears would shift the viewport by a line on every
	// auto-follow flip, and the frame's row count is an invariant (the
	// content must soft-wrap to exactly the screen height).
	inputBoxHeight := m.promptBox.Height()
	m.display = m.display.WithHeight(max(0, m.screenHeight-inputBoxHeight-liveEdgeRows-1))
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
	// (m.screenHeight - inputHeight - 1) (0-indexed); the box content
	// (attachments, input field) follows, then the bottom rule.
	//
	// The status bar sits at the last row (m.screenHeight - 1, 0-indexed)
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
	//     a separator after it the display's last row and whatever is
	//     drawn next (the live edge, or the input top rule when the live
	//     edge has nothing to show) would merge into one segment under
	//     soft-wrap, losing a row and breaking the FullScreen invariant
	//     (View content must soft-wrap to exactly screen height).
	// The CUPs are absolute, so the '\n' does not change where the
	// subsequent content lands — only what the soft-wrap row counter
	// sees, which is what we need.
	m.promptBox = m.promptBox.WithBlocked(m.isBlocked() || m.postLoading)
	// The live box, derived rather than stored: an overlay or a modal that owns
	// the keyboard dims this box and takes the real caret with it, and so does
	// the window losing OS focus — both painting facts, neither a routing one.
	m.promptBox = m.promptBox.WithActive(m.keyboardTarget() == targetPrompt && m.hasFocus)
	inputBoxHeight := m.promptBox.Height()
	inputBoxY := max(0, m.screenHeight-inputBoxHeight-1) // 0-indexed row of the top rule

	// Live edge — the transcript's closing row (live_edge.go), the line
	// between the last message and the input box's top rule. It gets its
	// own '\n' and CUP for the same reasons as the two blocks below: the
	// '\n' terminates the display's last row (without it the marker and the
	// display tail merge into one segment under soft-wrap and the frame
	// comes up a row short), and the CUP keeps the row's position absolute
	// so a drift in the display's actual row count cannot drag it along.
	//
	// The row is written whether or not the marker has anything to say, and it
	// ends with an EL. That is the chrome contract the three blocks in this
	// function keep: a row the frame owns from column 0 is positioned
	// absolutely and cleared to its own end, so nothing the row above flowed
	// onto it can survive beside what the frame drew. Both matter for a host
	// whose width table disagrees with width.go's (docs/tui.md → Line
	// Wrapping): such a host can wrap a transcript row one row early or late,
	// and the cell that does not fit lands on the row below it — which for the
	// last transcript row is this one. The marker is constant while output
	// streams, so the row diff would never rewrite it, and the stray cell
	// would stay until a full repaint.
	//
	// The whole block is skipped when inputBoxY is 0 — a terminal too short for
	// the input box plus the status bar has no row to spare, and adding one
	// here would put more rows in the frame than the screen has.
	if m.screenHeight > 0 && inputBoxY > 0 {
		sb.WriteString("\n")
		sb.WriteString(ansi.CursorPosition(1, inputBoxY)) // row inputBoxY-1
		sb.WriteString(m.renderLiveEdge())
		sb.WriteString(ansi.EraseLine(0))
	}

	if m.screenHeight > 0 {
		sb.WriteString("\n")
		sb.WriteString(ansi.CursorPosition(1, inputBoxY+1))
		sb.WriteString(m.promptBox.View().Content)
		// The box is the frame's from column 0 as well: clear the tail of the
		// block's last row (a box shorter than the previous frame's, or a cell
		// the row above flowed onto it). The box's inner rows are separated by
		// hard newlines, so a host that bills one of them a different width
		// cannot merge two of them — the rows below the first are positioned by
		// the newline and re-anchored by the status bar's CUP.
		sb.WriteString(ansi.EraseLine(0))
		sb.WriteString("\n")
		// Status bar: hard anchor to the last row, cleared to its end for the
		// same reason. renderStatusBar lays the row out against what a host may
		// charge its own text (the model name is the session's), because this
		// is the frame's bottom row and a cell written past the bottom row
		// scrolls the terminal: the whole frame — including rows the diff will
		// not rewrite — off by a row until a full repaint.
		sb.WriteString(ansi.CursorPosition(1, m.screenHeight))
		sb.WriteString(m.renderStatusBar())
		sb.WriteString(ansi.EraseLine(0))
	}

	baseContent := sb.String()

	// Render all overlay layers through the overlay manager.
	overlayContent, _ := m.renderOverlays(baseContent, m.screenWidth, m.screenHeight)

	v := NewView(overlayContent)
	v.AltScreen = true
	// The frame soft-wraps to exactly the screen height (viewport padded,
	// its base rows spanning the terminal width, with the live edge, the
	// input box and the status bar filling the rest), so it can be rendered
	// by overwriting without clearing first
	// — no ED2 flicker during streaming (see Screen.Render).
	v.FullScreen = true
	// Raw passthrough mode (Bubble Tea's Raw branch): the content is written
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
	if x, y, ok := m.overlayCaretPosition(); ok {
		v.ScreenCursor = m.newScreenCursor(x, y)
	} else if m.promptBox.IsActive() && !m.isBlocked() {
		// y: the input box's top rule sits at (screenHeight - inputHeight - 1)
		// — the same expression View uses for inputBoxY. The rows above it
		// belong to the display region and the live edge, which reach down
		// no further than the row before it. Content starts after the rule,
		// then the attachment rows. The input renders a single
		// content line, so the cursor's line index within the value does not
		// affect y.
		y := max(0, m.screenHeight-m.promptBox.Height()-1) // 0-indexed top rule
		y++                                                // first content row
		y += m.promptBox.AttachmentsOffset()
		// x: the prompt box has no side border or padding, so the cursor cell
		// is the horizontal position directly (prompt is empty for the
		// main input).
		x := m.promptBox.CaretCell()
		v.ScreenCursor = m.newScreenCursor(x, y)
	}

	return v
}

// newScreenCursor returns a steady block cursor at the given screen position
// (cells relative to the frame's top-left corner). The cursor color is
// left unset — the terminal emulator renders the OS-level cursor in its
// default color. Set cur.Color to a color.Color to override via OSC 12
// (Change Text ScreenCursor Color) when supported.
func (m Terminal) newScreenCursor(x, y int) *ScreenCursor {
	cur := NewScreenCursor(x, y)
	cur.Shape = ScreenCursorBlock
	cur.Blink = false
	return cur
}

// overlayCaretPosition returns the screen position of the active overlay's
// filter input cursor. ok is true only when an overlay with a text input is
// open and its filter box has focus (see FilteredListCore.CaretPosition).
func (m Terminal) overlayCaretPosition() (x, y int, ok bool) {
	switch {
	case m.modelSelector.IsOpen():
		return m.modelSelector.CaretPosition(m.screenWidth, m.screenHeight)
	case m.themeSelector.IsOpen():
		return m.themeSelector.CaretPosition(m.screenWidth, m.screenHeight)
	case m.helpOverlay.IsOpen():
		return m.helpOverlay.CaretPosition(m.screenWidth, m.screenHeight)
	case m.attachmentOverlay.IsOpen():
		return m.attachmentOverlay.CaretPosition(m.screenWidth, m.screenHeight)
	}
	return 0, 0, false
}

// renderLoadingView renders the loading screen shown while the session is
// being loaded asynchronously. It displays a centered message with a
// simple spinner animation (updated by tickMsg) so the user gets instant
// feedback even on slow machines.
//
// The view pads to the full viewport height with erased blank rows and
// sets v.FullScreen = true so Screen.Render uses the row-diff path
// instead of ED2-clear: only the spinner row is repainted when the
// spinner advances, eliminating the full-screen flicker that ED2 would
// otherwise produce on every tick. The first paint (empty lastContent)
// still clears; later repaints diff only the changed row.
//
// The spinner frames are the shared braille dot-segment rotation (see
// spinner.go) — the same glyph the tool header and the status bar draw. They
// advance based on wall clock, so any re-render — the 250ms tick, a content
// arrival, an overlay open — picks up the current frame without a
// separate timer.
func (m Terminal) renderLoadingView() View {
	spinnerChar := spinnerFrame()

	msg := fmt.Sprintf(" %s Loading session...", spinnerChar)
	// Center the message vertically and horizontally (ASCII width ~= len).
	contentWidth := len(msg)
	padX := max(0, (m.screenWidth-contentWidth)/2)
	padY := max(0, m.screenHeight/2-1)
	bottomRows := max(0, m.screenHeight-padY-1)

	// Build rows top-to-bottom: padY erased blank rows, the centered
	// message row, bottomRows erased blank rows. Total rows = screenHeight
	// so the view fills the screen exactly and Screen.Render can take the
	// diff path. The rows are joined with '\n' (not a trailing '\n') so the
	// string has exactly screenHeight-1 newlines — wrapping it at the screen
	// width reports screenHeight rows, matching the FullScreen invariant.
	blankRow := ansi.EraseLine(0)
	lines := make([]string, 0, m.screenHeight)
	for i := 0; i < padY; i++ {
		lines = append(lines, blankRow)
	}
	lines = append(lines, strings.Repeat(" ", padX)+msg)
	for i := 0; i < bottomRows; i++ {
		lines = append(lines, blankRow)
	}

	v := NewView(strings.Join(lines, "\n"))
	v.AltScreen = true
	v.FullScreen = true
	return v
}

// ensure Terminal implements Model
var _ Model = Terminal{}

func (m Terminal) applyTheme(theme *theme.Theme) Terminal {
	m.styles = NewStyles(theme)
	m.out.WithStyles(m.styles)
	m.display = m.display.WithStyles(m.styles)
	m.promptBox = m.promptBox.WithStyles(m.styles)
	m.modelSelector = m.modelSelector.WithStyles(m.styles)
	m.themeSelector = m.themeSelector.WithStyles(m.styles)
	m.helpOverlay = m.helpOverlay.WithStyles(m.styles)
	m.confirmOverlay = m.confirmOverlay.WithStyles(m.styles)
	m.mcpInitOverlay = m.mcpInitOverlay.WithStyles(m.styles)
	m.attachmentOverlay = m.attachmentOverlay.WithStyles(m.styles)
	m.display = m.display.updateContent()
	return m
}

// updateComponentSizes updates the size of all overlay components.
func (m Terminal) updateComponentSizes(width, height int) Terminal {
	m.modelSelector = m.modelSelector.WithSize(width, height)
	m.themeSelector = m.themeSelector.WithSize(width, height)
	m.helpOverlay = m.helpOverlay.WithSize(width, height)
	m.confirmOverlay = m.confirmOverlay.WithSize(width, height)
	m.mcpInitOverlay = m.mcpInitOverlay.WithSize(width, height)
	m.attachmentOverlay = m.attachmentOverlay.WithSize(width, height)
	return m
}

// isBlocked returns true when the user's view is covered by any overlay
// that prevents interaction with the prompt input.
func (m Terminal) isBlocked() bool {
	return m.modelSelector.IsOpen() || m.themeSelector.IsOpen() ||
		m.helpOverlay.IsOpen() || m.attachmentOverlay.IsOpen() ||
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
	if st.MCPStatus != "" && st.MCPStatus != protocol.MCPStatusDone {
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
func (m Terminal) renderOverlays(baseContent string, width, height int) (string, bool) {
	overlayContent := baseContent

	switch {
	case m.modelSelector.IsOpen():
		overlayContent = m.modelSelector.RenderOverlay(baseContent, width, height)
	case m.themeSelector.IsOpen():
		overlayContent = m.themeSelector.RenderOverlay(baseContent, width, height)
	case m.helpOverlay.IsOpen():
		overlayContent = m.helpOverlay.RenderOverlay(baseContent, width, height)
	case m.attachmentOverlay.IsOpen():
		overlayContent = m.attachmentOverlay.RenderOverlay(baseContent, width, height)
	}

	if m.mcpInitOverlay.IsOpen() {
		overlayContent = m.mcpInitOverlay.RenderOverlay(overlayContent, width, height)
	}

	if m.confirmOverlay.IsOpen() {
		return m.confirmOverlay.RenderOverlay(overlayContent, width, height), true
	}
	return overlayContent, false
}

// OverlayAction describes what happened during handleMCPProgress.
type OverlayAction struct {
	CloseInitOverlay  bool
	OpenedConfirm     bool
	InitOverlayActive bool
}
