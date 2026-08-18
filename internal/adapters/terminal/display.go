package terminal

// DisplayModel provides a viewport over WindowBuffer content.
// It manages scrolling, cursor navigation, and auto-follow behavior.
//
// Field groups:
//   Elm UI state  — all value types / primitives (copied on WithXxx).
//   Dependencies  — pointers to shared data (WindowBuffer, Styles).

import (
	"fmt"

	"github.com/alayacore/alayacore/internal/commands"
	"github.com/alayacore/alayacore/internal/tlv"
)

type DisplayModel struct {
	// ── Elm UI state (value types, copied on every WithXxx) ─
	scrollView     ScrollView // viewport into the window buffer content
	height         int        // display area height (viewport height)
	windowCursor   int        // currently selected window index (-1 = none)
	autoFollow     bool       // true on init and after G; disabled by navigation
	displayFocused bool       // whether the display pane has input focus
	blocked        bool       // true when an overlay is active (dim rendering)
	lastContent    string     // cached last rendered output for change detection

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
	}
}

// Init initializes the display
func (m DisplayModel) Init() Cmd { return nil }

// Update handles key messages for display navigation and cursor movement.
// Only pure display operations are handled here; cross-component actions
// (e.g. switching focus, opening editor) are handled by Terminal.
//
//nolint:gocyclo
func (m DisplayModel) Update(msg Msg) (DisplayModel, Cmd) {
	keyMsg, ok := msg.(KeyMsg)
	if !ok {
		return m, nil
	}

	switch keyMsg.String() {
	case keyJ, keyDown:
		m, _ = m.MoveWindowCursorDown()
		return m.EnsureCursorVisible().updateContent(), nil

	case keyK, keyUp:
		m, _ = m.MoveWindowCursorUp()
		return m.EnsureCursorVisible().updateContent(), nil

	case keyCtrlD, keyPgDown:
		if !m.AtBottom() {
			m = m.MarkUserScrolled()
			m = m.ScrollDown(max(1, m.GetHeight()/2))
			m = m.updateContent()
		}
		return m, nil

	case keyCtrlU, keyPgUp:
		m = m.MarkUserScrolled()
		m = m.ScrollUp(max(1, m.GetHeight()/2))
		return m.updateContent(), nil

	case keyJCapital, keyShiftDown:
		if !m.AtBottom() {
			m = m.MarkUserScrolled()
			m = m.ScrollDown(1)
			m = m.updateContent()
		}
		return m, nil

	case keyKCapital, keyShiftUp:
		m = m.MarkUserScrolled()
		m = m.ScrollUp(1)
		return m.updateContent(), nil

	case keyG, keyEnd:
		m = m.WithCursorToLastWindow()
		m = m.GotoBottom()
		return m.updateContent(), nil

	case keyGSmall, keyHome:
		m = m.WithWindowCursor(0)
		m = m.GotoTop()
		return m.updateContent(), nil

	case keyH:
		m, _ = m.MoveWindowCursorToTop()
		return m.EnsureCursorVisible().updateContent(), nil

	case keyL:
		m, _ = m.MoveWindowCursorToBottom()
		return m.EnsureCursorVisible().updateContent(), nil

	case keyM:
		m, _ = m.MoveWindowCursorToCenter()
		return m.EnsureCursorVisible().updateContent(), nil

	case keySpace:
		m, _ = m.ToggleWindowFold()
		return m.EnsureCursorVisible().updateContent(), nil

	case keyF:
		m, _ = m.MoveWindowCursorToNextUserPrompt()
		return m.ScrollCursorToTop().updateContent(), nil

	case keyB:
		m, _ = m.MoveWindowCursorToPrevUserPrompt()
		return m.ScrollCursorToTop().updateContent(), nil

	case keyE:
		content := m.GetCursorWindowContent()
		if content != "" {
			m = m.MarkUserScrolled()
			return m, func() Msg {
				return openEditorForDisplayMsg{content: content}
			}
		}
		return m, nil

	case keyColon:
		return m, func() Msg {
			return focusInputWithValueMsg{value: ":"}
		}

	case keyCtrlF:
		if historyID := m.GetCursorWindowHistoryID(); historyID > 0 {
			return m, func() Msg {
				return focusInputWithValueMsg{value: fmt.Sprintf(":%s %d ", commands.CommandNameFork, historyID)}
			}
		}
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
	m.height = height
	m.scrollView = m.scrollView.WithHeight(max(0, height))
	return m
}

func (m DisplayModel) GetHeight() int {
	return m.scrollView.Height()
}

func (m DisplayModel) WithDisplayFocused(focused bool) DisplayModel {
	m.displayFocused = focused
	return m
}

// WithBlocked marks the display as blocked (covered by an overlay).
// When blocked, subsequent updateContent calls render with dimmed colors.
func (m DisplayModel) WithBlocked(blocked bool) DisplayModel {
	m.blocked = blocked
	return m
}

func (m DisplayModel) WithStyles(styles *Styles) DisplayModel {
	m.styles = styles
	return m
}

// ForceContentDirty clears the cached content so the next updateContent
// regenerates and sets the scroll content even if nothing changed.
func (m DisplayModel) ForceContentDirty() DisplayModel {
	m.lastContent = ""
	return m
}

func (m DisplayModel) YOffset() int {
	return m.scrollView.YOffset()
}

// updateContent updates the viewport content from the window buffer.
// Uses m.blocked to determine whether to render with dimmed colors.
func (m DisplayModel) updateContent() DisplayModel {
	cursorIndex := -1
	if m.displayFocused {
		cursorIndex = m.windowCursor
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

	newContent := m.windowBuffer.GetAll(cursorIndex, m.blocked)
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
	return m
}

// AtBottom returns whether viewport is at bottom
func (m DisplayModel) AtBottom() bool {
	return m.scrollView.AtBottom()
}

// ScrollUp scrolls up by lines
func (m DisplayModel) ScrollUp(lines int) DisplayModel {
	m.scrollView = m.scrollView.ScrollUp(lines)
	return m
}

// GotoBottom goes to bottom
func (m DisplayModel) GotoBottom() DisplayModel {
	m.scrollView = m.scrollView.GotoBottom()
	return m
}

// GotoTop goes to top
func (m DisplayModel) GotoTop() DisplayModel {
	m.scrollView = m.scrollView.GotoTop()
	return m
}

func (m DisplayModel) shouldFollow() bool {
	return m.autoFollow
}

func (m DisplayModel) GetWindowCursor() int {
	return m.windowCursor
}

// Returns empty string if no window is selected.
func (m DisplayModel) GetCursorWindowContent() string {
	if m.windowCursor < 0 {
		return ""
	}
	return m.windowBuffer.GetWindowContent(m.windowCursor)
}

// Returns 0 if no window is selected.
func (m DisplayModel) GetCursorWindowHistoryID() uint64 {
	if m.windowCursor < 0 {
		return 0
	}
	w := m.windowBuffer.WindowAt(m.windowCursor)
	if w == nil {
		return 0
	}
	return w.HistoryID
}

// setCursor sets the window cursor and disables auto-follow.
// Only SetCursorToLastWindow re-enables auto-follow afterwards.
func (m DisplayModel) setCursor(i int) DisplayModel {
	m.windowCursor = i
	m.autoFollow = false
	return m
}

// clearCursor unsets the window cursor and disables auto-follow.
func (m DisplayModel) clearCursor() DisplayModel {
	m.windowCursor = -1
	m.autoFollow = false
	return m
}

// If the index points to an invisible window, the nearest visible window is chosen instead.
// Disables auto-follow only if the cursor actually moves; only G re-enables it.
func (m DisplayModel) WithWindowCursor(index int) DisplayModel {
	if index < 0 || m.windowBuffer.WindowCount() == 0 {
		return m.clearCursor()
	}
	index = m.windowBuffer.NearestVisibleIndex(index)
	if index < 0 {
		return m.clearCursor()
	}
	if m.windowCursor == index {
		return m // cursor unchanged, preserve autoFollow
	}
	return m.setCursor(index)
}

// MoveWindowCursorDown moves the window cursor down, skipping invisible windows.
// When auto-follow is active this is a no-op: new windows may have been
// appended to the buffer between ticks, making the cursor appear to be
// "not at the last window" even though the user never moved.  Allowing j
// to move in that case would silently jump to a window the user can't see
// and disable auto-follow.  Only the tick handler (SetCursorToLastWindow)
// should advance the cursor while auto-following.
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
			return m.setCursor(i), true
		}
		return m, false
	}

	// Step forward to the next visible window
	found := false
	m.windowBuffer.ForEachVisible(m.windowCursor+1, func(i int, _ *Window) bool {
		m = m.setCursor(i)
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
			return m.setCursor(i), true
		}
		return m, false
	}

	// Step backward to the previous visible window
	found := false
	m.windowBuffer.ForEachVisibleBackward(m.windowCursor-1, func(i int, _ *Window) bool {
		m = m.setCursor(i)
		found = true
		return false
	})
	return m, found
}

// MarkUserScrolled disables auto-follow. Called by scroll keys (J/K/Ctrl-D/Ctrl-U).
func (m DisplayModel) MarkUserScrolled() DisplayModel {
	return m.DisableAutoFollow()
}

// DisableAutoFollow explicitly disables auto-follow.
// This is the single method that scroll key handlers should call to
// disable auto-follow, ensuring the state transition is clear.
func (m DisplayModel) DisableAutoFollow() DisplayModel {
	m.autoFollow = false
	return m
}

// EnsureCursorVisible scrolls the viewport only if the cursor window is
// completely off-screen. If any part of the window is already visible, the
// viewport position is left unchanged. The cursor highlight tells the user
// where they are; explicit scroll keys (Ctrl-D, J, etc.) can reveal more.
//
// This avoids viewport jumping on repeated navigation and prevents
// oscillation when a window is taller than the viewport.
func (m DisplayModel) EnsureCursorVisible() DisplayModel {
	if m.windowCursor < 0 {
		return m
	}

	startLine, endLine := m.windowBuffer.GetWindowLineRange(m.windowCursor)
	viewportTop := m.scrollView.YOffset()
	viewportBottom := viewportTop + m.scrollView.Height()

	if endLine <= viewportTop {
		// Entirely above — show the bottom edge
		m.scrollView = m.scrollView.WithYOffset(max(0, endLine-m.scrollView.Height()))
	} else if startLine >= viewportBottom {
		// Entirely below — show the top edge
		m.scrollView = m.scrollView.WithYOffset(startLine)
	}
	return m
}

// ScrollCursorToTop scrolls the viewport so the cursor window's start line
// is at the top of the viewport.  This is used when jumping to a specific
// prompt (e.g. via b/f) so the user can see both the prompt and the
// response that follows it.
func (m DisplayModel) ScrollCursorToTop() DisplayModel {
	if m.windowCursor < 0 {
		return m
	}
	startLine, _ := m.windowBuffer.GetWindowLineRange(m.windowCursor)
	m.scrollView = m.scrollView.WithYOffset(startLine)
	return m
}

// ValidateCursor ensures the window cursor is valid and visible.
// Uses partial visibility check to avoid jarring scroll jumps on resize.
func (m DisplayModel) ValidateCursor() DisplayModel {
	m = m.ClampCursor()
	if m.windowCursor >= 0 && m.windowBuffer.WindowCount() > 0 {
		m = m.EnsureCursorVisible()
	}
	return m
}

// ClampCursor clamps the window cursor to a visible window without scrolling.
// Unlike ValidateCursor, this does not call EnsureCursorVisible, preserving
// the user's scroll position. Use this on resize events where only bounds
// correction is needed.
func (m DisplayModel) ClampCursor() DisplayModel {
	if m.windowBuffer.WindowCount() == 0 {
		m.windowCursor = -1
		return m
	}
	// Cursor is intentionally unset or negative — normalize to -1
	if m.windowCursor < 0 {
		m.windowCursor = -1
		return m
	}
	// Use NearestVisibleIndex to handle out-of-bounds or invisible cursor.
	// Note: we assign directly instead of using setCursor because ClampCursor
	// intentionally does NOT modify autoFollow — it's a silent correction that
	// preserves the user's scroll/follow state.
	m.windowCursor = m.windowBuffer.NearestVisibleIndex(m.windowCursor)
	return m
}

func (m DisplayModel) WithCursorToLastWindow() DisplayModel {
	i := m.windowBuffer.LastVisibleIndex()
	if i >= 0 {
		m.windowCursor = i
	} else {
		m.windowCursor = -1
	}
	m.autoFollow = true
	return m
}

func (m DisplayModel) ToggleWindowFold() (DisplayModel, bool) {
	if m.windowCursor < 0 {
		return m, false
	}
	ok := m.windowBuffer.ToggleFold(m.windowCursor)
	return m, ok
}

func (m DisplayModel) MoveWindowCursorToTop() (DisplayModel, bool) {
	if m.windowBuffer.WindowCount() == 0 {
		return m, false
	}

	viewportTop := m.scrollView.YOffset()
	viewportBottom := viewportTop + m.scrollView.Height()
	found := false

	m.windowBuffer.ForEachVisibleRanged(func(i int, startLine, endLine int) bool {
		// Skip windows entirely below the viewport
		if startLine >= viewportBottom {
			return true
		}
		// Check if this is the first visible window at or near the viewport top
		if (startLine <= viewportTop && endLine > viewportTop) || startLine >= viewportTop {
			if i != m.windowCursor {
				m = m.setCursor(i)
				found = true
			}
			return false
		}
		return true
	})

	return m, found
}

// MoveWindowCursorToBottom moves cursor to bottom visible window.
// No-op when auto-follow is active (same race as MoveWindowCursorDown).
func (m DisplayModel) MoveWindowCursorToBottom() (DisplayModel, bool) {
	if m.autoFollow {
		return m, false
	}
	if m.windowBuffer.WindowCount() == 0 {
		return m, false
	}

	viewportTop := m.scrollView.YOffset()
	viewportBottom := viewportTop + m.scrollView.Height()
	found := false

	m.windowBuffer.ForEachVisibleBackwardRanged(func(i int, startLine, endLine int) bool {
		// Skip windows entirely above the viewport
		if endLine <= viewportTop {
			return true
		}
		// Skip windows entirely below the viewport
		if startLine >= viewportBottom {
			return true
		}
		// This window is at least partially visible and is the bottommost one
		if i != m.windowCursor {
			m = m.setCursor(i)
			found = true
		}
		return false
	})

	return m, found
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
		return m.setCursor(idx), true
	}

	// If center line falls in a gap (or all windows are above/below center),
	// find the visible window whose center is closest to the viewport center
	if idx := m.findClosestVisibleWindow(viewportTop, viewportTop+viewportHeight, viewportCenter); idx >= 0 {
		if idx == m.windowCursor {
			return m, false // cursor unchanged, preserve autoFollow
		}
		return m.setCursor(idx), true
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
// the next visible window whose tag is TagUserT ("UT"). Returns false if
// no such window exists below the current cursor.
func (m DisplayModel) MoveWindowCursorToNextUserPrompt() (DisplayModel, bool) {
	if m.windowBuffer.WindowCount() == 0 {
		return m, false
	}

	found := false
	m.windowBuffer.ForEachVisible(m.windowCursor+1, func(i int, w *Window) bool {
		if w.Tag() == tlv.TagUserT {
			m = m.setCursor(i)
			found = true
			return false
		}
		return true
	})

	return m, found
}

// MoveWindowCursorToPrevUserPrompt moves the window cursor backward (up) to
// the previous visible window whose tag is TagUserT ("UT"). Returns false
// if no such window exists above the current cursor.
func (m DisplayModel) MoveWindowCursorToPrevUserPrompt() (DisplayModel, bool) {
	if m.windowBuffer.WindowCount() == 0 {
		return m, false
	}

	found := false
	m.windowBuffer.ForEachVisibleBackward(m.windowCursor-1, func(i int, w *Window) bool {
		if w.Tag() == tlv.TagUserT {
			m = m.setCursor(i)
			found = true
			return false
		}
		return true
	})

	return m, found
}
