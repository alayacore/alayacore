package terminal

// DisplayModel provides a viewport over WindowBuffer content.
// It manages scrolling, cursor navigation, and auto-follow behavior.
//
// Field groups:
//   Elm UI state  — all value types / primitives (copied on WithXxx).
//   Dependencies  — pointers to shared data (WindowBuffer, Styles).

import (
	"fmt"
	"math"

	"github.com/alayacore/alayacore/internal/commands"
)

type DisplayModel struct {
	// ── Elm UI state (value types, copied on every WithXxx) ─
	scrollView     ScrollView // viewport into the window buffer content
	height         int        // display area height (viewport height)
	count          int        // count prefix typed but not yet spent (0 = none); see pushCount
	windowCursor   int        // currently selected window index (-1 = none)
	autoFollow     bool       // true on init and after FollowLatest (`G`, submit); disabled by navigation
	displayFocused bool       // whether the display pane has input focus
	blocked        bool       // true when an overlay is active (dim rendering)
	lastContent    string     // cached last rendered output for change detection

	// contentDirty is true when the next updateContent call must rebuild
	// the rendered output. It is cleared after a successful render. The
	// tick handler invokes updateContent 4×/sec regardless of input; without
	// this flag, every tick would lock the window buffer, walk every
	// visible window's cache, allocate a strings.Builder, and join lines
	// even when nothing rendering-relevant has changed — producing the
	// exact same content the screen renderer's same-content check then
	// discards. With the flag, an idle tick returns immediately.
	contentDirty bool

	// ── Dependencies (pointers to shared data, not copied semantically) ─
	windowBuffer *WindowBuffer // windowed content storage (shared with OutputWriter)
	styles       *Styles       // derived styles (replaced on theme switch)
}

// NewDisplayModel creates a new display model
func NewDisplayModel(windowBuffer *WindowBuffer, styles *Styles) DisplayModel {
	return DisplayModel{
		scrollView:     NewScrollView(DefaultHeight),
		windowBuffer:   windowBuffer,
		styles:         styles,
		height:         DefaultHeight,
		windowCursor:   -1,
		autoFollow:     true,
		displayFocused: false,
		blocked:        false,
		// The first render must run so lastContent is populated.
		contentDirty: true,
	}
}

// Init initializes the display
func (m DisplayModel) Init() Cmd { return nil }

// maxCount is where a count prefix saturates. It is not a ceiling on what can
// be addressed — every motion clamps against the buffer it walks — but the one
// guard against the parse overflowing into a negative number, which would walk
// backwards. Six digits is far past any transcript (one step adds at most four
// windows), and it keeps the echo at most seven cells wide.
const maxCount = 999_999

// Update handles key messages for display navigation and cursor movement.
// Only pure display operations are handled here; cross-component actions
// (e.g. switching focus, opening editor) are handled by Terminal.
//
//nolint:gocyclo // key dispatch over many shortcuts; each case is a simple navigation call
func (m DisplayModel) Update(msg Msg) (DisplayModel, []Result) {
	keyMsg, ok := msg.(KeyMsg)
	if !ok {
		return m, nil
	}

	// The count prefix. A digit arms the next motion and nothing else, so it is
	// read before the key means anything; every other key reaches takeCount
	// below, which spends the count whether or not its case reads it. That is
	// what makes "this key ignores the count" the default instead of a rule
	// each case has to remember, and it is why ESC needs no code of its own to
	// be the cancel: its case runs after the count is gone.
	if d, ok := countDigit(keyMsg.Chord()); ok {
		return m.pushCount(d), nil
	}
	n, hasCount := m.takeCount()
	if !hasCount {
		n = 1 // one press, for every motion that has no use for hasCount
	}

	switch keyMsg.Chord() {
	// The pointer, on the home row only. `j`/`k` and the letters below own
	// it; the arrows belong to the viewport, for the reason written on those
	// cases.
	//
	// A count is spent as N presses, not as one jump: the placement rule runs
	// after every step (`EnsureWindowCursorVisible` is what a single `j` does),
	// and a step that cannot move ends the run — which is also the bound on
	// the loop. `100j` therefore lands exactly where 100 presses land, at the
	// last window if there are fewer, ever leaving the viewport where the last
	// press put it.
	case keyJ:
		for i := 0; i < n; i++ {
			var moved bool
			m, moved = m.MoveWindowCursorDown()
			m = m.EnsureWindowCursorVisible()
			if !moved {
				break
			}
		}
		return m.updateContent(), nil

	case keyK:
		for i := 0; i < n; i++ {
			var moved bool
			m, moved = m.MoveWindowCursorUp()
			m = m.EnsureWindowCursorVisible()
			if !moved {
				break
			}
		}
		return m.updateContent(), nil

	// The coarse motions, counted as N of themselves. A scroll cannot fail —
	// it clamps — so there is no per-step effect to preserve and the count is
	// multiplied instead of run: `999999<C-d>` is one clamped scroll, not a
	// million calls.
	case keyCtrlD, keyPgDown:
		if !m.AtBottom() {
			m = m.MarkUserScrolled()
			m = m.ScrollDown(scrollAmount(n, max(1, m.GetHeight()/2)))
			m = m.updateContent()
		}
		return m, nil

	case keyCtrlU, keyPgUp:
		m = m.MarkUserScrolled()
		m = m.ScrollUp(scrollAmount(n, max(1, m.GetHeight()/2)))
		return m.updateContent(), nil

	// The viewport, on the arrows and on vi's own one-line scroll keys.
	//
	// The arrows belong here rather than beside `j`/`k` because of the mouse
	// wheel. While this program owns the alternate screen it asks for no mouse
	// reports (screen.go → mouseReportingOff, which is also what leaves
	// click-and-drag text selection with the terminal), so a host that lets the
	// wheel reach the application at all does it by writing the arrow keys' own
	// bytes — `ESC [ A`, `ESC [ B` — into the input stream. Gesture and
	// keystroke are then literally the same message and there is no second thing
	// to bind: whatever the arrows do *is* the wheel's behavior, at one line per
	// arrow a host produces for a notch. A host that synthesizes nothing keeps
	// sending no wheel input at all, and loses nothing by it — its users still move
	// half a screen at a time with `Ctrl+U`/`Ctrl+D` and `PgUp`/`PgDn`.
	//
	// Bound to the pointer instead, that same stream is a motion auto-follow
	// refuses at the live edge (MoveWindowCursorDown returns straight away, so
	// rolling down looked dead) and that steps whole windows at a time (rolling
	// up jumped): the feel this replaced.
	//
	// `Ctrl+E`/`Ctrl+Y` are the same motion and are here for the one thing the
	// arrows cannot give it: a spelling that is not also a wheel notch. vi binds
	// them to exactly this (scroll the window a line, leave the cursor alone),
	// so they are the one keyboard-only form of this family.
	//
	// No shifted letter is bound here, and the two that were are the reason. `J`
	// and `K` used to spell this motion on the home row, so that shift carried
	// one meaning in this table; the argument that wins is the reader's hands.
	// In vi `J` joins lines; `K` is the keyword lookup a vim user's hands expect
	// and nvi does not have at all (it has no `keywordprg` option). Either way
	// neither key is a scroll, and this UI is used beside those editors rather
	// than instead of them — a scroll on the same keys trains the wrong reflex
	// in both. Shift plus an arrow stays unbound
	// for a second reason: it was the only binding that needed a terminal to
	// report a modified arrow, and a modified arrow is not what a wheel
	// synthesizes. The parser still decodes them.
	case keyDown, keyCtrlE:
		if !m.AtBottom() {
			m = m.MarkUserScrolled()
			m = m.ScrollDown(n)
			m = m.updateContent()
		}
		return m, nil

	case keyUp, keyCtrlY:
		m = m.MarkUserScrolled()
		m = m.ScrollUp(n)
		return m.updateContent(), nil

	// `G` is the transcript's other end, and the only absolute motion here:
	// bare, it is the last window and a request to follow it; counted, it is
	// window N of the visible windows, in document order.
	//
	// A counted G is a jump, and a jump is never a request to watch the live
	// edge — following goes off before the move, or updateContent would pull
	// the viewport straight back to the bottom whenever the cursor already sat
	// where the count points (`WithWindowCursor` preserves autoFollow when the
	// index does not change, so on a single window taller than the screen the
	// key would do nothing at all).
	//
	// There is no `GotoTop` here, and that is the decision rather than an
	// omission: `1G` places the cursor on the first window exactly as `j` would
	// after enough presses, and where the viewport ends up is that rule's
	// answer. It is the top of the document whenever the first message fits the
	// screen; when that message is taller than the viewport the pin names it and
	// counts the rows that are hidden, and the scroll keys reach the rest.
	case keyG:
		if !hasCount {
			return m.FollowLatest(), nil
		}
		// A counted G counts down from the top of the transcript and stops where
		// the count runs out: `10G` is the tenth window, and `999G` in a
		// hundred-window session is the hundredth. That reading is what makes the
		// count and the rest of the table one rule — the target is resolved
		// against what exists and taken as far as it goes.
		//
		// Refusing instead ("there is no window 999") would need a way to say so.
		// vi has a bell; this UI has no key-level error channel, and the transcript
		// never shows its own length, so a refusal here would be a silent no-op
		// indistinguishable from a broken key.
		//
		// The empty transcript is the one case with no target at all: nothing moves
		// and auto-follow is untouched, because nothing happened.
		target, ok := m.windowCursorAtOrdinal(n)
		if !ok {
			return m, nil
		}
		m = m.MarkUserScrolled()
		m = m.WithWindowCursor(target)
		return m.EnsureWindowCursorVisible().updateContent(), nil

	// `H`/`L` count through the windows the viewport shows, from its top edge
	// down and from its bottom edge up. "On screen" is the viewport's line
	// range, so a window straddling an edge is in the sequence — half a message
	// is on screen. A count past the far edge clamps to that edge's window,
	// which is the other key's first: `999H` lands where `L` does.
	case keyH:
		m, _ = m.MoveWindowCursorToTop(n)
		return m.EnsureWindowCursorVisible().updateContent(), nil

	case keyL:
		m, _ = m.MoveWindowCursorToBottom(n)
		return m.EnsureWindowCursorVisible().updateContent(), nil

	// The middle of what is on screen has no ordinal space, so `M` takes no
	// count: the digit is spent and dropped like any other non-motion (vi's M
	// has nothing to count either).
	case keyM:
		m, _ = m.MoveWindowCursorToCenter()
		return m.EnsureWindowCursorVisible().updateContent(), nil

	case keySpace:
		m, _ = m.ToggleWindowFold()
		return m.anchorWindowCursor().EnsureWindowCursorVisible().updateContent(), nil

	case keyR:
		// Toggle markdown rendering — only effective on an
		// UNFOLDED plain-text window (assistant text / reasoning).
		//
		// The toggle leaves the buffer dirty, but the anchor and
		// EnsureWindowCursorVisible below both call GetWindowLineRange →
		// ensureLineHeights, which clears wb.dirty as a side effect.
		// contentDirty is therefore set explicitly, so updateContent (last)
		// still rebuilds the viewport with the new rendering — without it,
		// updateContent would early-return and the toggle would not show
		// until Ctrl+R.
		w := m.windowBuffer.WindowAt(m.windowCursor)
		if w == nil || w.Folded {
			return m, nil
		}
		if m.windowBuffer.ToggleMarkdownMode(m.windowCursor) {
			m.contentDirty = true
			return m.anchorWindowCursor().EnsureWindowCursorVisible().updateContent(), nil
		}
		return m, nil

	// `f`/`b` count prompts, and they move as far as the prompts exist: a
	// count whose last step finds nothing stays on the last prompt it reached,
	// which is what pressing the key that many times does (a press at the last
	// prompt is already a no-op). Re-seating happens once, after the walk: it
	// depends only on where the cursor ended, and every press would have put
	// the same window at the top.
	//
	// Re-seat only when the jump happened. Unlike the EnsureWindowCursorVisible
	// used by the other jump keys, ScrollWindowCursorToTop always moves: it puts
	// the cursor window's first line at the top of the viewport. With the cursor
	// parked below the last user prompt (where a finished task leaves it) there
	// is no next prompt to find, and re-seating anyway would yank the viewport up
	// to the head of the window already under the cursor.
	case keyF:
		moved := false
		for i := 0; i < n; i++ {
			var step bool
			m, step = m.MoveWindowCursorToNextUserPrompt()
			if !step {
				break
			}
			moved = true
		}
		if moved {
			return m.ScrollWindowCursorToTop().updateContent(), nil
		}
		return m, nil

	case keyB:
		// Same guard as keyF, mirrored: no prompt above the cursor means
		// nothing to jump to and nothing to re-seat.
		moved := false
		for i := 0; i < n; i++ {
			var step bool
			m, step = m.MoveWindowCursorToPrevUserPrompt()
			if !step {
				break
			}
			moved = true
		}
		if moved {
			return m.ScrollWindowCursorToTop().updateContent(), nil
		}
		return m, nil

	case keyE:
		content := m.GetWindowCursorContent()
		if content != "" {
			m = m.MarkUserScrolled()
			return m, []Result{openEditorForDisplayMsg{content: content}}
		}
		return m, nil

	case keyColon:
		return m, []Result{focusPromptWithValueMsg{value: ":"}}

	case keyCtrlF:
		if historyID := m.GetWindowCursorHistoryID(); historyID > 0 {
			return m, []Result{focusPromptWithValueMsg{value: fmt.Sprintf(":%s %d ", commands.CommandNameFork, historyID)}}
		}
		return m, nil

	// The one key that spends a count without doing anything. Without it,
	// abandoning a mistyped count means pressing a key whose own action you did
	// not want — `e` opens $EDITOR, `r` re-renders, Space folds — which is a poor
	// way to cancel. The count is already gone (takeCount, above), so the case
	// body is the whole of the behavior; with no count armed it is a no-op like
	// any other key the display does not bind.
	case keyEsc:
		return m, nil

	default:
		return m, nil
	}
}

// View renders the display
func (m DisplayModel) View() View {
	return NewView(m.scrollView.View())
}

func (m DisplayModel) WithHeight(height int) DisplayModel {
	if m.height == height {
		return m
	}
	m.height = height
	m.scrollView = m.scrollView.WithHeight(max(0, height))
	m.contentDirty = true
	return m
}

func (m DisplayModel) GetHeight() int {
	return m.scrollView.Height()
}

func (m DisplayModel) WithDisplayFocused(focused bool) DisplayModel {
	if m.displayFocused == focused {
		return m
	}
	m.displayFocused = focused
	m.contentDirty = true
	return m
}

// WithBlocked marks the display as blocked (covered by an overlay).
// When blocked, subsequent updateContent calls render with dimmed colors.
func (m DisplayModel) WithBlocked(blocked bool) DisplayModel {
	if m.blocked == blocked {
		return m
	}
	m.blocked = blocked
	m.contentDirty = true
	return m
}

func (m DisplayModel) WithStyles(styles *Styles) DisplayModel {
	m.styles = styles
	m.contentDirty = true
	return m
}

// ForceContentDirty clears the cached content so the next updateContent
// regenerates and sets the scroll content even if nothing changed.
func (m DisplayModel) ForceContentDirty() DisplayModel {
	m.lastContent = ""
	m.contentDirty = true
	return m
}

func (m DisplayModel) YOffset() int {
	return m.scrollView.YOffset()
}

// updateContent updates the viewport content from the window buffer.
// Uses m.blocked to determine whether to render with dimmed colors.
//
// Early-exits when contentDirty is false: navigation/scroll/style
// mutations set the flag (see With* / Move* / Scroll* methods), and the
// tick handler that runs 4×/sec is the primary caller. Without the
// early-exit, an idle tick would lock the window buffer, walk every
// visible window's cache, allocate a strings.Builder, and join lines
// even when nothing rendering-relevant has changed — producing the
// exact same content the screen renderer's same-content check then
// discards.
//
// contentDirty is also forced on when the underlying window buffer is
// dirty (content appended, window invalidated by resize/theme): the
// Tick handler may not have called the DisplayModel mutators that set
// the flag, but the buffer itself reports new content.
func (m DisplayModel) updateContent() DisplayModel {
	if m.windowBuffer != nil && m.windowBuffer.IsDirty() {
		m.contentDirty = true
	}
	if !m.contentDirty {
		return m
	}
	m.contentDirty = false

	windowCursorIndex := -1
	if m.displayFocused {
		windowCursorIndex = m.windowCursor
	}

	totalLines := m.windowBuffer.GetTotalLines()
	viewportHeight := m.scrollView.Height()

	// ScrollView's content is the pre-clipped visible region, so it can
	// no longer clamp yOffset against its own content length — the full
	// document visual line count is the clamping basis.
	m.scrollView = m.scrollView.WithTotalLines(totalLines)

	targetYOffset := m.scrollView.YOffset()
	if m.autoFollow && totalLines > viewportHeight {
		targetYOffset = max(0, totalLines-viewportHeight)
	}

	m.windowBuffer.SetViewportPosition(targetYOffset, viewportHeight)

	newContent := m.windowBuffer.GetAll(windowCursorIndex, m.blocked)
	if newContent == m.lastContent {
		// Content unchanged — but auto-follow may still need to move the
		// viewport to the new bottom. With soft-wrap fragments the clip
		// can be byte-identical after appended content (e.g. all rows are
		// the same text), so the position update must not be skipped.
		if m.autoFollow {
			m.scrollView = m.scrollView.GotoBottom()
		}
		return m
	}
	m.lastContent = newContent

	m.scrollView = m.scrollView.WithContent(newContent)

	if m.autoFollow {
		m.scrollView = m.scrollView.GotoBottom()
	}
	return m
}

// ScrollDown scrolls down by lines
func (m DisplayModel) ScrollDown(lines int) DisplayModel {
	m.scrollView = m.scrollView.ScrollDown(lines)
	m.contentDirty = true
	return m
}

// AtBottom returns whether viewport is at bottom
func (m DisplayModel) AtBottom() bool {
	return m.scrollView.AtBottom()
}

// ScrollUp scrolls up by lines
func (m DisplayModel) ScrollUp(lines int) DisplayModel {
	m.scrollView = m.scrollView.ScrollUp(lines)
	m.contentDirty = true
	return m
}

// GotoBottom goes to bottom
func (m DisplayModel) GotoBottom() DisplayModel {
	m.scrollView = m.scrollView.GotoBottom()
	m.contentDirty = true
	return m
}

// GotoTop goes to top
func (m DisplayModel) GotoTop() DisplayModel {
	m.scrollView = m.scrollView.GotoTop()
	m.contentDirty = true
	return m
}

func (m DisplayModel) shouldFollow() bool {
	return m.autoFollow
}

// LinesBelow returns how many transcript lines the viewport hides below
// its bottom edge (0 when it shows the document's last line). Read by the
// live-edge marker (live_edge.go) to tell a scrolled-back user how much
// they are missing.
func (m DisplayModel) LinesBelow() int {
	return m.scrollView.LinesBelow()
}

func (m DisplayModel) GetWindowCursor() int {
	return m.windowCursor
}

// ── Count prefix ─────────────────────────────────────────────────────────
//
// The count lives here rather than in Terminal because this is the only pane
// that reads it and the only one that can spend it: every key DisplayModel
// reads goes through takeCount, so a count cannot outlive the key it was typed
// for. Terminal clears it when a key lands anywhere else (handleKeyMsg), which
// is the one place the two panes could otherwise leak into each other.

// PendingCount returns the count typed but not yet spent, or 0. Read at render
// time by the status bar, which is why it is an accessor and not a cached
// string: a count flips on keypresses, and the status segments are rebuilt only
// when the session's version moves.
func (m DisplayModel) PendingCount() int {
	return m.count
}

// pushCount appends a digit to the pending count, saturating at maxCount. A
// leading `0` starts nothing — vi reads it as a column motion, this UI has no
// columns — and is dropped rather than kept as a zero, so `0G` is `G`.
func (m DisplayModel) pushCount(d int) DisplayModel {
	if m.count == 0 && d == 0 {
		return m
	}
	if m.count > (maxCount-d)/10 {
		m.count = maxCount
		return m
	}
	m.count = m.count*10 + d
	return m
}

// takeCount spends the count: it returns it and clears it. Every key the display
// reads calls it, so "this key ignores the count" is the default and ESC's
// cancel is the case that then does nothing.
func (m *DisplayModel) takeCount() (int, bool) {
	n := m.count
	m.count = 0
	return n, n > 0
}

// ClearCount drops a count without spending it: the answer for a key that lands
// in another pane, which is not the motion the digits were typed for.
func (m DisplayModel) ClearCount() DisplayModel {
	m.count = 0
	return m
}

// scrollAmount is N presses of a step motion as one scroll. The steps here
// clamp rather than fail, so there is no per-step state to preserve and the
// count is multiplied; the multiplication saturates, because n is a count the
// user typed and `int` is not required to hold the product.
func scrollAmount(n, step int) int {
	if step <= 0 {
		step = 1
	}
	if n > math.MaxInt32/step {
		return math.MaxInt32
	}
	return n * step
}

// windowCursorAtOrdinal counts down to the window a counted `G` names: the n-th
// visible window (1-based, in document order), or the last one when the
// transcript has fewer than n. It counts what navigation can reach, so folded
// windows count as one each and invisible ones are skipped, exactly as `j` steps
// over them.
//
// ok is false only when there is no visible window at all — a target that cannot
// be counted down to versus nothing to count. That is the degenerate case every
// motion has, not a rule of its own.
func (m DisplayModel) windowCursorAtOrdinal(n int) (int, bool) {
	target := -1
	ordinal := 0
	m.windowBuffer.ForEachVisible(0, func(i int, _ *Window) bool {
		ordinal++
		target = i
		return ordinal < n
	})
	return target, target >= 0
}

// Returns empty string if no window is selected.
func (m DisplayModel) GetWindowCursorContent() string {
	if m.windowCursor < 0 {
		return ""
	}
	return m.windowBuffer.GetWindowContent(m.windowCursor)
}

// Returns 0 if no window is selected.
func (m DisplayModel) GetWindowCursorHistoryID() uint64 {
	if m.windowCursor < 0 {
		return 0
	}
	w := m.windowBuffer.WindowAt(m.windowCursor)
	if w == nil {
		return 0
	}
	return w.HistoryID
}

// setWindowCursor sets the window cursor and disables auto-follow.
// Only WithWindowCursorToLastWindow re-enables auto-follow afterwards.
func (m DisplayModel) setWindowCursor(i int) DisplayModel {
	if m.windowCursor == i && !m.autoFollow {
		return m
	}
	m.windowCursor = i
	m.autoFollow = false
	m.contentDirty = true
	return m
}

// clearWindowCursor unsets the window cursor and disables auto-follow.
func (m DisplayModel) clearWindowCursor() DisplayModel {
	if m.windowCursor == -1 && !m.autoFollow {
		return m
	}
	m.windowCursor = -1
	m.autoFollow = false
	m.contentDirty = true
	return m
}

// If the index points to an invisible window, the nearest visible window is chosen instead.
// Disables auto-follow only if the cursor actually moves; only
// WithWindowCursorToLastWindow re-enables it.
func (m DisplayModel) WithWindowCursor(index int) DisplayModel {
	if index < 0 || m.windowBuffer.WindowCount() == 0 {
		return m.clearWindowCursor()
	}
	index = m.windowBuffer.NearestVisibleIndex(index)
	if index < 0 {
		return m.clearWindowCursor()
	}
	if m.windowCursor == index {
		return m // cursor unchanged, preserve autoFollow
	}
	return m.setWindowCursor(index)
}

// MoveWindowCursorDown moves the window cursor down, skipping invisible windows.
// When auto-follow is active this is a no-op: new windows may have been
// appended to the buffer between ticks, making the cursor appear to be
// "not at the last window" even though the user never moved.  Allowing j
// to move in that case would silently jump to a window the user can't see
// and disable auto-follow.  While auto-following, only the tick handler
// (SetCursorToLastWindow) advances the cursor — FollowLatest is what put it
// there, and this key must not move it.
func (m DisplayModel) MoveWindowCursorDown() (DisplayModel, bool) {
	if m.autoFollow {
		return m, false
	}
	if m.windowBuffer.WindowCount() == 0 {
		return m, false
	}

	// If cursor is unset, start at the first visible window
	if m.windowCursor < 0 {
		i := m.windowBuffer.FirstVisibleIndex()
		if i >= 0 {
			return m.setWindowCursor(i), true
		}
		return m, false
	}

	// Step forward to the next visible window
	found := false
	m.windowBuffer.ForEachVisible(m.windowCursor+1, func(i int, _ *Window) bool {
		m = m.setWindowCursor(i)
		found = true
		return false
	})
	return m, found
}

// MoveWindowCursorUp moves the window cursor up, skipping invisible windows.
func (m DisplayModel) MoveWindowCursorUp() (DisplayModel, bool) {
	if m.windowBuffer.WindowCount() == 0 {
		return m, false
	}

	// If cursor is unset or below the last window, start at the last visible window
	if m.windowCursor < 0 || m.windowCursor >= m.windowBuffer.WindowCount() {
		i := m.windowBuffer.LastVisibleIndex()
		if i >= 0 {
			if m.windowCursor == i {
				return m, false
			}
			return m.setWindowCursor(i), true
		}
		return m, false
	}

	// Step backward to the previous visible window
	found := false
	m.windowBuffer.ForEachVisibleBackward(m.windowCursor-1, func(i int, _ *Window) bool {
		m = m.setWindowCursor(i)
		found = true
		return false
	})
	return m, found
}

// MarkUserScrolled disables auto-follow. Called by scroll keys (arrows/Ctrl-D/Ctrl-U).
func (m DisplayModel) MarkUserScrolled() DisplayModel {
	return m.DisableAutoFollow()
}

// DisableAutoFollow explicitly disables auto-follow.
// This is the single method that scroll key handlers should call to
// disable auto-follow, ensuring the state transition is clear.
func (m DisplayModel) DisableAutoFollow() DisplayModel {
	if !m.autoFollow {
		return m
	}
	m.autoFollow = false
	m.contentDirty = true
	return m
}

// EnsureWindowCursorVisible scrolls the viewport only if the cursor window is
// completely off-screen. If any part of the window is already visible, the
// viewport position is left unchanged. The cursor highlight tells the user
// where they are; explicit scroll keys (Ctrl-D, J, etc.) can reveal more.
//
// This avoids viewport jumping on repeated navigation and prevents
// oscillation when a window is taller than the viewport.
func (m DisplayModel) EnsureWindowCursorVisible() DisplayModel {
	if m.windowCursor < 0 {
		return m
	}

	startLine, endLine := m.windowBuffer.GetWindowLineRange(m.windowCursor)
	viewportTop := m.scrollView.YOffset()
	viewportBottom := viewportTop + m.scrollView.Height()

	if endLine <= viewportTop {
		// Entirely above — show the bottom edge
		m.scrollView = m.scrollView.WithYOffset(max(0, endLine-m.scrollView.Height()))
		m.contentDirty = true
	} else if startLine >= viewportBottom {
		// Entirely below — show the top edge
		m.scrollView = m.scrollView.WithYOffset(startLine)
		m.contentDirty = true
	}
	return m
}

// ScrollWindowCursorToTop scrolls the viewport so the cursor window's start line
// is at the top of the viewport.  This is used when jumping to a specific
// prompt (e.g. via b/f) so the user can see both the prompt and the
// response that follows it.
func (m DisplayModel) ScrollWindowCursorToTop() DisplayModel {
	if m.windowCursor < 0 {
		return m
	}
	startLine, _ := m.windowBuffer.GetWindowLineRange(m.windowCursor)
	m.scrollView = m.scrollView.WithYOffset(startLine)
	m.contentDirty = true
	return m
}

// anchorWindowCursor re-seats the viewport after a toggle that changes only the
// window's body — a fold/unfold, or a markdown re-render. The own line keeps its
// document position either way, so a line that is on screen keeps its row. A
// line above the viewport top does not: when the shrunken window no longer
// reaches below that line, the row is left entirely above the viewport and
// EnsureWindowCursorVisible would drag the viewport to the row's bottom edge — the
// jump a pinned line took when its window folded to a single row. Pulling
// the offset up to the own line brings it back to row 0, so the toggle reads as
// a change in place: the rule is simply that a cursor window scrolled above the
// top returns to the top, the place ScrollWindowCursorToTop puts it.
//
// Only a line above the top moves the offset; at or below the top it is left
// alone, and EnsureWindowCursorVisible still covers a window scrolled off the bottom.
// The offset is clamped, so a window near the transcript's end settles as high
// as the shrunken document allows.
func (m DisplayModel) anchorWindowCursor() DisplayModel {
	if m.windowCursor < 0 {
		return m
	}
	if start, _ := m.windowBuffer.GetWindowLineRange(m.windowCursor); start < m.scrollView.YOffset() {
		m.scrollView = m.scrollView.WithYOffset(start)
		m.contentDirty = true
	}
	return m
}

// ValidateWindowCursor ensures the window cursor is valid and visible.
// Uses partial visibility check to avoid jarring scroll jumps on resize.
func (m DisplayModel) ValidateWindowCursor() DisplayModel {
	m = m.ClampWindowCursor()
	if m.windowCursor >= 0 && m.windowBuffer.WindowCount() > 0 {
		m = m.EnsureWindowCursorVisible()
	}
	return m
}

// ClampWindowCursor clamps the window cursor to a visible window without scrolling.
// Unlike ValidateWindowCursor, this does not call EnsureWindowCursorVisible, preserving
// the user's scroll position. Use this on resize events where only bounds
// correction is needed.
func (m DisplayModel) ClampWindowCursor() DisplayModel {
	if m.windowBuffer.WindowCount() == 0 {
		m.windowCursor = -1
		return m
	}
	// ScreenCursor is intentionally unset or negative — normalize to -1
	if m.windowCursor < 0 {
		m.windowCursor = -1
		return m
	}
	// Use NearestVisibleIndex to handle out-of-bounds or invisible cursor.
	// Note: we assign directly instead of using setWindowCursor because ClampWindowCursor
	// intentionally does NOT modify autoFollow — it's a silent correction that
	// preserves the user's scroll/follow state.
	m.windowCursor = m.windowBuffer.NearestVisibleIndex(m.windowCursor)
	return m
}

// WithWindowCursorToLastWindow parks the cursor on the last visible window and
// re-enables auto-follow. It is the one place auto-follow comes back on —
// setWindowCursor and clearWindowCursor only ever turn it off — so every caller is a
// deliberate "take me to what is happening now": FollowLatest (`G`,
// submitting, a task starting) and focusDisplay's cursorless case.
func (m DisplayModel) WithWindowCursorToLastWindow() DisplayModel {
	i := m.windowBuffer.LastVisibleIndex()
	if i >= 0 {
		m.windowCursor = i
	} else {
		m.windowCursor = -1
	}
	m.autoFollow = true
	m.contentDirty = true
	return m
}

// FollowLatest is the "take me to what is happening now" gesture: park the
// cursor on the last window, re-enable auto-follow, and paint the live edge.
// Two things mean it — `G`, and submitting a prompt. A send is a
// request to watch the answer, so the prompt the session echoes back and
// everything streamed after it must land in view instead of below a viewport
// left parked in the scrollback (where a silent Enter reads as a prompt that
// never went).
// The third caller is not a gesture but the session's own report that a task
// began (see Terminal.handleDisplayRefresh), which is the same request made by
// a command: :continue and :summarize.
//
// At submit time the echo has not arrived yet, so the cursor lands on the last
// window there is and the tick handler's WithWindowCursorToLastWindow advances it as
// the session's frames append windows — the same state `G` leaves the display in.
//
// The render belongs to the gesture: View paints scrollView's cached clip, and
// a tick returns early when no output delta arrived, so a jump that skipped
// updateContent would not be drawn until content happened to follow.
func (m DisplayModel) FollowLatest() DisplayModel {
	return m.WithWindowCursorToLastWindow().GotoBottom().updateContent()
}

func (m DisplayModel) ToggleWindowFold() (DisplayModel, bool) {
	if m.windowCursor < 0 {
		return m, false
	}
	ok := m.windowBuffer.ToggleFold(m.windowCursor)
	if ok {
		m.contentDirty = true
	}
	return m, ok
}

// windowsOnScreen returns the visible windows the viewport shows, in document
// order — the sequence `H` and `L` count through. "On screen" is the viewport's
// line range, so a window straddling an edge is in it: half a message is on
// screen, and a window taller than the viewport is one entry however little of
// it fits. Nothing else is: a window entirely above or below is not a place the
// cursor can be sent to "within the view".
func (m DisplayModel) windowsOnScreen() []int {
	top := m.scrollView.YOffset()
	bottom := top + m.scrollView.Height()
	var onScreen []int
	m.windowBuffer.ForEachVisibleRanged(func(i int, startLine, endLine int) bool {
		if startLine >= bottom {
			return false // document order: everything after this is below too
		}
		if endLine > top {
			onScreen = append(onScreen, i)
		}
		return true
	})
	return onScreen
}

// MoveWindowCursorToTop moves the cursor to the n-th window counted from the
// viewport's top edge: n=1 is the first window on screen — the one containing
// the top row, or the first one that starts below it — and n counts down the
// screen from there. A count past the bottom edge clamps to the last window on
// screen, which is where L begins.
func (m DisplayModel) MoveWindowCursorToTop(n int) (DisplayModel, bool) {
	onScreen := m.windowsOnScreen()
	if len(onScreen) == 0 {
		return m, false
	}
	target := onScreen[min(max(n, 1), len(onScreen))-1]
	if target == m.windowCursor {
		return m, false
	}
	return m.setWindowCursor(target), true
}

// MoveWindowCursorToBottom moves the cursor to the n-th window counted from the
// viewport's bottom edge, the mirror of MoveWindowCursorToTop: n=1 is the
// bottom-most window on screen, and a count past the top edge clamps to the
// window H begins at.
//
// No-op when auto-follow is active (same race as MoveWindowCursorDown).
func (m DisplayModel) MoveWindowCursorToBottom(n int) (DisplayModel, bool) {
	if m.autoFollow {
		return m, false
	}
	onScreen := m.windowsOnScreen()
	if len(onScreen) == 0 {
		return m, false
	}
	target := onScreen[max(0, len(onScreen)-max(n, 1))]
	if target == m.windowCursor {
		return m, false
	}
	return m.setWindowCursor(target), true
}

// MoveWindowCursorToCenter moves cursor to the window at the visual center of the screen.
// It finds the window that contains the center line of the visible viewport.
// If no window contains the center line, it finds the window closest to the center.
func (m DisplayModel) MoveWindowCursorToCenter() (DisplayModel, bool) {
	if m.windowBuffer.WindowCount() == 0 {
		return m, false
	}

	// Calculate the center line of the visible viewport
	viewportHeight := m.scrollView.Height()
	viewportTop := m.scrollView.YOffset()
	viewportCenter := viewportTop + viewportHeight/2

	// First, try to find the window that contains the viewport center line
	if idx := m.findWindowContainingLine(viewportCenter); idx >= 0 {
		if idx == m.windowCursor {
			return m, false // cursor unchanged, preserve autoFollow
		}
		return m.setWindowCursor(idx), true
	}

	// If center line falls in a gap (or all windows are above/below center),
	// find the visible window whose center is closest to the viewport center
	if idx := m.findClosestVisibleWindow(viewportTop, viewportTop+viewportHeight, viewportCenter); idx >= 0 {
		if idx == m.windowCursor {
			return m, false // cursor unchanged, preserve autoFollow
		}
		return m.setWindowCursor(idx), true
	}

	return m, false
}

// findWindowContainingLine returns the index of the first visible window that
// contains the given line, or -1 if no such window exists.
func (m DisplayModel) findWindowContainingLine(line int) int {
	idx := -1
	m.windowBuffer.ForEachVisibleRanged(func(i int, startLine, endLine int) bool {
		if line >= startLine && line < endLine {
			idx = i
			return false
		}
		return true
	})
	return idx
}

// findClosestVisibleWindow returns the index of the visible window whose
// center is closest to viewportCenter, considering only windows that are
// at least partially within [viewportTop, viewportBottom). Returns -1 if
// no visible window is found in that range.
func (m DisplayModel) findClosestVisibleWindow(viewportTop, viewportBottom, viewportCenter int) int {
	bestWindow := -1
	bestDistance := -1

	m.windowBuffer.ForEachVisibleRanged(func(i int, startLine, endLine int) bool {
		// Skip windows entirely outside the viewport
		if startLine >= viewportBottom || endLine <= viewportTop {
			return true
		}

		windowCenter := (startLine + endLine) / 2
		distance := windowCenter - viewportCenter
		if distance < 0 {
			distance = -distance
		}

		if bestDistance < 0 || distance < bestDistance {
			bestWindow = i
			bestDistance = distance
		}
		return true
	})

	return bestWindow
}

// MoveWindowCursorToNextUserPrompt moves the window cursor forward (down) to
// the next visible window whose tag is tagUserText (a user message). Returns false if
// no such window exists below the current cursor, in which case the cursor
// and the viewport are left untouched. Callers must test that flag before
// re-seating the viewport, which always moves (see ScrollWindowCursorToTop).
func (m DisplayModel) MoveWindowCursorToNextUserPrompt() (DisplayModel, bool) {
	if m.windowBuffer.WindowCount() == 0 {
		return m, false
	}

	found := false
	m.windowBuffer.ForEachVisible(m.windowCursor+1, func(i int, w *Window) bool {
		if w.Tag() == tagUserText {
			m = m.setWindowCursor(i)
			found = true
			return false
		}
		return true
	})

	return m, found
}

// MoveWindowCursorToPrevUserPrompt moves the window cursor backward (up) to
// the previous visible window whose tag is tagUserText (a user message). Returns false
// if no such window exists above the current cursor, in which case the
// cursor and the viewport are left untouched. Callers must test that flag
// before re-seating the viewport, which always moves (see ScrollWindowCursorToTop).
func (m DisplayModel) MoveWindowCursorToPrevUserPrompt() (DisplayModel, bool) {
	if m.windowBuffer.WindowCount() == 0 {
		return m, false
	}

	found := false
	m.windowBuffer.ForEachVisibleBackward(m.windowCursor-1, func(i int, w *Window) bool {
		if w.Tag() == tagUserText {
			m = m.setWindowCursor(i)
			found = true
			return false
		}
		return true
	})

	return m, found
}
