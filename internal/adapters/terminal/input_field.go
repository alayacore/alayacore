package terminal

// InputField is a text input component supporting multi-line content with a
// single-line display. Users navigate lines with up/down arrows, and the
// visible area shows only the line containing the cursor.

import (
	"slices"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	rw "github.com/mattn/go-runewidth"
)

// InputField is the Bubble Tea model for a text input with multi-line support
// but single-line display. Cursor up/down navigates between lines.
type InputField struct {
	value       []rune
	pos         int // cursor position in value
	goalCol     int // remembered column position for up/down navigation (-1 = none)
	offset      int // horizontal scroll offset (cells)
	width       int // visible width (cells)
	Prompt      string
	Placeholder string
	focused     bool

	styleFocused inputFieldStyle
	styleBlurred inputFieldStyle
}

type inputFieldStyle struct {
	Prompt      lipgloss.Style
	Text        lipgloss.Style
	Placeholder lipgloss.Style
}

// NewInputField creates a new InputField with default settings.
func NewInputField() InputField {
	return InputField{
		width:   20,
		Prompt:  "> ",
		focused: true,
		goalCol: -1,
	}
}

// Init implements tea.Model.
func (m InputField) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m InputField) Update(msg tea.Msg) (InputField, tea.Cmd) {
	if !m.focused {
		return m, nil
	}
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	case tea.PasteMsg:
		return m.handlePaste(msg), nil
	}
	return m, nil
}

func (m InputField) handleKeyMsg(msg tea.KeyMsg) (InputField, tea.Cmd) {
	key := msg.String()

	var handled bool
	m, handled = m.handleMovement(key)
	if handled {
		return m, nil
	}
	m, handled = m.handleDeletion(key)
	if handled {
		return m, nil
	}
	m, handled = m.handleInsertion(key)
	if handled {
		return m, nil
	}

	return m, nil
}

// handleMovement returns true if the key was a cursor movement.
func (m InputField) handleMovement(key string) (InputField, bool) {
	switch {
	case key == "left":
		m = m.moveLeft()
	case key == "right":
		m = m.moveRight()
	case key == "up":
		var ok bool
		m, ok = m.moveLineUp()
		if !ok {
			return m, true
		}
	case key == "down":
		var ok bool
		m, ok = m.moveLineDown()
		if !ok {
			return m, true
		}
	case key == "home":
		m.pos = m.lineStart(m.pos)
		m.offset = 0
		m.goalCol = -1
		return m.ensureCursorVisible(), true
	case key == "end":
		m.pos = m.lineEnd(m.pos)
		m.goalCol = -1
		return m.ensureCursorVisible(), true
	default:
		return m, false
	}
	return m.ensureCursorVisible(), true
}

// handleDeletion returns true if the key was a deletion action.
func (m InputField) handleDeletion(key string) (InputField, bool) {
	switch {
	case key == "backspace":
		m = m.deleteBackward()
	case key == "delete":
		m = m.deleteForward()
	default:
		return m, false
	}
	return m.ensureCursorVisible(), true
}

// handleInsertion returns true if the key was a character or space insertion.
// Note: "space" is handled separately because KeyMsg.String() reports the space
// key as "space" (not " "), so it can't go through printableRune's single-rune
// check. The filtering policy is the same as handlePaste: both ultimately use
// isPrintableRune to accept/reject control characters.
func (m InputField) handleInsertion(key string) (InputField, bool) {
	if key == "space" {
		m.value = slices.Insert(m.value, m.pos, ' ')
		m.pos++
		m.goalCol = -1
		return m.ensureCursorVisible(), true
	}
	if r, ok := printableRune(key); ok {
		m.value = slices.Insert(m.value, m.pos, r)
		m.pos++
		m.goalCol = -1
		return m.ensureCursorVisible(), true
	}
	return m, false
}

// handlePaste inserts pasted text at the cursor position.
// Control characters are filtered out, except for newlines which are
// allowed to support multi-line paste.
func (m InputField) handlePaste(msg tea.PasteMsg) InputField {
	runes := []rune(msg.Content)
	if len(runes) == 0 {
		return m
	}
	// Normalize line endings: handle \r\n and \r.
	normalized := make([]rune, 0, len(runes))
	for i := 0; i < len(runes); i++ {
		switch runes[i] {
		case '\r':
			if i+1 < len(runes) && runes[i+1] == '\n' {
				i++ // skip \r in \r\n
			}
			normalized = append(normalized, '\n')
		case '\n':
			normalized = append(normalized, '\n')
		default:
			normalized = append(normalized, runes[i])
		}
	}
	// Filter out non-printable control characters, but keep newlines.
	filtered := make([]rune, 0, len(normalized))
	for _, r := range normalized {
		if r == '\n' || isPrintableRune(r) {
			filtered = append(filtered, r)
		}
	}
	if len(filtered) == 0 {
		return m
	}
	// Trim trailing newlines (matches editor behavior — terminals often
	// add a trailing newline on paste).
	for len(filtered) > 0 && filtered[len(filtered)-1] == '\n' {
		filtered = filtered[:len(filtered)-1]
	}
	if len(filtered) == 0 {
		return m
	}
	m.value = slices.Insert(m.value, m.pos, filtered...)
	m.pos += len(filtered)
	m.goalCol = -1
	return m.ensureCursorVisible()
}

func (m InputField) ensureCursorVisible() InputField {
	if m.width <= 0 {
		return m
	}
	// Clean up any stale mid-rune scroll offset inherited from a previous
	// line/width state (a resize or value change that did not trigger a
	// scroll would otherwise keep it). After this, the offset is always a
	// rune boundary, so startCell == offset and the window math below is
	// unambiguous.
	m.offset = m.visibleStartCell()
	lineStart, lineEnd := m.currentLine(m.pos)
	lineWidth := runesWidth(m.value[lineStart:lineEnd])

	// If the entire line fits within the viewport, no scrolling needed.
	if lineWidth <= m.width {
		m.offset = 0
		return m
	}

	relPos := m.pos - lineStart // cursor position within current line
	cursorCell := runesWidth(m.value[lineStart : lineStart+relPos])
	// The rune under the cursor must stay fully visible: a wide (CJK) rune
	// straddling the right edge of the viewport would be clipped in half.
	// At end-of-line there is no rune, but the cursor block itself still
	// needs one cell, so the threshold degrades to the plain cursor check.
	need := 1
	if m.pos < lineEnd {
		need = rw.RuneWidth(m.value[m.pos])
	}
	// The text is rendered from the character boundary that contains the
	// scroll offset (see visibleStartCell), so the *actual* viewport window
	// is [startCell, startCell+width) — never [offset, offset+width) when
	// the offset falls inside a wide rune. Scroll decisions must use the
	// actual window, otherwise a rune at the cursor can still end up
	// clipped even though it appears inside the nominal window.
	startCell := m.visibleStartCell()
	visibleEnd := startCell + m.width
	switch {
	case cursorCell < startCell:
		m.offset = cursorCell
	case cursorCell+need > visibleEnd:
		m.offset = cursorCell - m.width + 2
		if m.offset < 0 {
			m.offset = 0
		}
		if need > 1 {
			// The rune under the cursor is wide (CJK): round the offset UP
			// to the next character boundary so the cursor sits at
			// rel <= width-2 and the wide rune is never clipped by the right
			// edge of the viewport (rounding down would push it to rel =
			// width-1, cutting the rune in half).
			m.offset = m.ceilToRuneStart(m.offset)
		} else {
			// Round down to the character boundary so the visible window
			// never splits a wide character; otherwise buildVisibleText
			// (which starts at the character boundary) and CursorCell (which
			// is measured from the raw offset) drift apart, placing the real
			// terminal cursor on top of the last character.
			m.offset = m.visibleStartCell()
		}
		// Extremely narrow viewports (width <= 2) can push the computed
		// offset past the cursor itself (e.g. width=1 gives offset =
		// cursorCell+1). Fall back to the cursor cell so the cursor block —
		// and any rune that can fit in the viewport — stays visible.
		if cursorCell < m.visibleStartCell() {
			m.offset = cursorCell
		}
	}
	return m
}

// ceilToRuneStart returns the smallest rune start cell >= offset within the
// current line (or the line end when offset is past the last rune). Used when
// the wide rune under the cursor must stay fully visible: rounding the scroll
// offset up to the next character boundary guarantees the cursor sits at
// rel <= width-2, so the rune is never clipped by the right edge.
func (m InputField) ceilToRuneStart(offset int) int {
	if offset <= 0 {
		return 0
	}
	lineStart, lineEnd := m.currentLine(m.pos)
	line := m.value[lineStart:lineEnd]
	cells := 0
	for _, r := range line {
		w := rw.RuneWidth(r)
		if cells >= offset {
			return cells
		}
		cells += w
	}
	return cells // past the end of the line
}

// View implements tea.Model.
func (m InputField) View() string {
	// Clamp pos to valid range as a safety measure.
	if m.pos < 0 {
		m.pos = 0
	} else if m.pos > len(m.value) {
		m.pos = len(m.value)
	}

	if len(m.value) == 0 && m.Placeholder != "" {
		return m.placeholderView()
	}

	styles := m.activeStyle()
	styleText := styles.Text.Inline(true).Render

	visible := m.buildVisibleText()

	var v string
	v += styleText(string(visible))

	if !m.focused {
		return m.promptRender() + v
	}

	// When focused, pad with spaces to fill the input width.
	visibleStr := string(visible)
	valWidth := ansi.StringWidth(visibleStr)
	if m.width <= 0 || valWidth >= m.width {
		return m.promptRender() + v
	}
	padding := m.width - valWidth
	if padding < 0 {
		padding = 0
	}
	v += styleText(strings.Repeat(" ", padding))

	return m.promptRender() + v
}

// promptRender renders the prompt string.
func (m InputField) promptRender() string {
	styles := m.activeStyle()
	return styles.Prompt.Inline(true).Render(m.Prompt)
}

// buildVisibleText returns the visible portion of the current line as runes,
// respecting the horizontal scroll offset and the field width.
func (m InputField) buildVisibleText() []rune {
	if len(m.value) == 0 {
		return nil
	}
	lineStart, lineEnd := m.currentLine(m.pos)
	line := m.value[lineStart:lineEnd]

	if len(line) == 0 {
		return nil
	}
	// Find the first rune whose start cell is the visible start. The visible
	// start is the scroll offset rounded down to a character boundary (see
	// visibleStartCell), so a wide character is never split at the left edge.
	startIdx := 0
	startCell := m.visibleStartCell()
	found := false
	for cells, i := 0, 0; i < len(line); i++ {
		w := rw.RuneWidth(line[i])
		if cells >= startCell {
			startIdx = i
			found = true
			break
		}
		if cells+w > startCell {
			startIdx = i // startCell falls inside this rune: keep it whole
			found = true
			break
		}
		cells += w
	}
	if !found {
		// startCell is at or past the end of the line: nothing is visible.
		startIdx = len(line)
	}
	// Build visible runes up to width.
	var vis []rune
	cells := 0
	for i := startIdx; i < len(line); i++ {
		w := rw.RuneWidth(line[i])
		if cells+w > m.width {
			break
		}
		vis = append(vis, line[i])
		cells += w
	}
	return vis
}

// visibleStartCell returns the cell offset (within the current line) where
// the visible text actually begins rendering. The requested scroll offset is
// rounded down to the start of the rune that contains it, so a wide (CJK)
// character is never split at the left edge of the viewport. CursorCell and
// buildVisibleText both derive from this value, keeping the real terminal
// cursor aligned with the rendered text.
func (m InputField) visibleStartCell() int {
	if m.offset <= 0 {
		return 0
	}
	lineStart, lineEnd := m.currentLine(m.pos)
	line := m.value[lineStart:lineEnd]
	cells := 0
	for _, r := range line {
		w := rw.RuneWidth(r)
		if cells == m.offset {
			return m.offset
		}
		if cells+w > m.offset {
			return cells // offset falls inside this rune: align to its start
		}
		cells += w
	}
	// Offset beyond the end of the line: keep it as-is (empty visible text).
	return m.offset
}

func (m InputField) placeholderView() string {
	styles := m.activeStyle()
	v := " "
	placeholder := m.Placeholder
	if m.width > 0 && ansi.StringWidth(placeholder) > m.width-1 {
		placeholder = truncatePlaceholder(placeholder, m.width-1)
	}
	v += styles.Placeholder.Inline(true).Render(placeholder)
	if !m.focused {
		return m.promptRender() + v
	}
	valWidth := ansi.StringWidth(v)
	if m.width <= 0 || valWidth >= m.width {
		return m.promptRender() + v
	}
	v += strings.Repeat(" ", m.width-valWidth)
	return m.promptRender() + v
}

func (m InputField) activeStyle() inputFieldStyle {
	if m.focused {
		return m.styleFocused
	}
	return m.styleBlurred
}

func (m InputField) Focus() InputField {
	m.focused = true
	return m
}

func (m InputField) Blur() InputField {
	m.focused = false
	return m
}

func (m InputField) IsFocused() bool { return m.focused }

func (m InputField) Value() string { return string(m.value) }

// CursorPos returns the cursor position (in runes) within the current value.
func (m InputField) CursorPos() int { return m.pos }

// CursorCell returns the cursor's cell offset within the currently displayed
// line after horizontal scrolling (0 = leftmost visible cell of the line).
// The input field renders only the current line, so the screen y of the
// cursor never depends on the value's line index. The cell offset is where
// the cursor block should be rendered — i.e. the first cell of the character
// under the cursor (or the cell after the last character at end-of-line).
func (m InputField) CursorCell() int {
	if len(m.value) == 0 {
		return 0
	}
	lineStart, _ := m.currentLine(m.pos)
	relPos := m.pos - lineStart // cursor position within the line
	cell := runesWidth(m.value[lineStart:lineStart+relPos]) - m.visibleStartCell()
	if cell < 0 {
		cell = 0
	}
	return cell
}

// currentLine returns the start and end indices (exclusive) of the line
// containing the given position. An empty line (just \n) has start == end.
func (m InputField) currentLine(pos int) (start, end int) {
	if len(m.value) == 0 {
		return 0, 0
	}
	// Clamp pos to valid range.
	if pos < 0 {
		pos = 0
	} else if pos > len(m.value) {
		pos = len(m.value)
	}
	// Scan backwards to find line start.
	start = pos
	for start > 0 && m.value[start-1] != '\n' {
		start--
	}
	// Scan forwards to find line end.
	end = pos
	for end < len(m.value) && m.value[end] != '\n' {
		end++
	}
	return start, end
}

// LineCount returns the number of lines in the value.
func (m InputField) LineCount() int {
	if len(m.value) == 0 {
		return 1 // one empty line
	}
	count := 1
	for _, r := range m.value {
		if r == '\n' {
			count++
		}
	}
	return count
}

// lineStart returns the start index of the line containing pos.
func (m InputField) lineStart(pos int) int {
	s, _ := m.currentLine(pos)
	return s
}

// lineEnd returns the end index (exclusive) of the line containing pos.
func (m InputField) lineEnd(pos int) int {
	_, e := m.currentLine(pos)
	return e
}

// ensureGoalColumn sets goalCol from the current cursor column if not set.
func (m InputField) ensureGoalColumn() InputField {
	if m.goalCol >= 0 {
		return m
	}
	ls := m.lineStart(m.pos)
	m.goalCol = runesWidth(m.value[ls:m.pos])
	return m
}

// moveLeft moves the cursor one position left.
// Stops at the start of a line (no wrapping to previous line).
func (m InputField) moveLeft() InputField {
	if m.pos <= 0 {
		return m
	}
	if m.pos <= m.lineStart(m.pos) {
		return m // at start of line, don't wrap
	}
	m.pos--
	m.goalCol = -1
	return m
}

// moveRight moves the cursor one position right.
// Stops at the end of a line (no wrapping to next line).
func (m InputField) moveRight() InputField {
	if m.pos >= len(m.value) {
		return m
	}
	if m.pos >= m.lineEnd(m.pos) {
		return m // at end of line, don't wrap
	}
	m.pos++
	m.goalCol = -1
	return m
}

// moveLineUp moves the cursor up one line, maintaining the column position.
// Returns false if already on the first line.
func (m InputField) moveLineUp() (InputField, bool) {
	ls := m.lineStart(m.pos)
	if ls == 0 {
		return m, false
	}
	m = m.ensureGoalColumn()
	prevEnd := ls - 1 // position of the \n at end of previous line
	prevStart := m.lineStart(prevEnd)
	prevLen := runesWidth(m.value[prevStart:prevEnd])
	target := min(m.goalCol, prevLen)
	m.pos = prevStart + runeIndexAtWidth(m.value[prevStart:prevEnd], target)
	return m, true
}

// moveLineDown moves the cursor down one line, maintaining the column position.
// Returns false if already on the last line.
func (m InputField) moveLineDown() (InputField, bool) {
	le := m.lineEnd(m.pos)
	if le >= len(m.value) {
		return m, false
	}
	m = m.ensureGoalColumn()
	nextStart := le + 1
	nextEnd := m.lineEnd(nextStart)
	nextLen := runesWidth(m.value[nextStart:nextEnd])
	target := min(m.goalCol, nextLen)
	m.pos = nextStart + runeIndexAtWidth(m.value[nextStart:nextEnd], target)
	return m, true
}

// deleteBackward deletes the character before the cursor (backspace).
// At the start of a line, it joins with the previous line by removing the \n.
func (m InputField) deleteBackward() InputField {
	if m.pos <= 0 {
		return m
	}
	if m.value[m.pos-1] == '\n' {
		ls := m.lineStart(m.pos)
		m.value = slices.Delete(m.value, m.pos-1, m.pos) // delete the \n
		// The following line shifts left by one rune, so the cursor (which
		// stays at the start of the joined line) moves back one position.
		// Without this, pos could end up past the end of the value when the
		// joined line is empty.
		m.pos = ls - 1
	} else {
		m.value = slices.Delete(m.value, m.pos-1, m.pos)
		m.pos--
	}
	m.goalCol = -1
	return m
}

// deleteForward deletes the character at the cursor (delete key).
// At the end of a line, it joins with the next line by removing the \n.
func (m InputField) deleteForward() InputField {
	if m.pos >= len(m.value) {
		return m
	}
	m.value = slices.Delete(m.value, m.pos, m.pos+1)
	m.goalCol = -1
	return m
}

func (m InputField) CursorEnd() InputField {
	m.pos = len(m.value)
	m.goalCol = -1
	return m.ensureCursorVisible()
}

func (m InputField) WithValue(s string) InputField {
	m.value = []rune(s)
	m.pos = len(m.value)
	m.goalCol = -1
	return m.ensureCursorVisible()
}

// WithCursorPos sets the cursor position to pos (in runes) within the value.
// Clamps to valid range [0, len(value)].
func (m InputField) WithCursorPos(pos int) InputField {
	if pos < 0 {
		pos = 0
	}
	if pos > len(m.value) {
		pos = len(m.value)
	}
	m.pos = pos
	m.goalCol = -1
	return m.ensureCursorVisible()
}

func (m InputField) WithWidth(w int) InputField {
	m.width = max(0, w)
	return m.ensureCursorVisible()
}

func (m InputField) WithStyles(focused, blurred inputFieldStyle) InputField {
	m.styleFocused = focused
	m.styleBlurred = blurred
	return m
}

// ============================================================================
// Helpers
// ============================================================================

func runesWidth(runes []rune) int {
	w := 0
	for _, r := range runes {
		w += rw.RuneWidth(r)
	}
	return w
}

// runeIndexAtWidth returns the rune index into runes where the accumulated
// cell width first meets or exceeds targetWidth. If targetWidth exceeds the
// total width, returns len(runes).
func runeIndexAtWidth(runes []rune, targetWidth int) int {
	cells := 0
	for i, r := range runes {
		w := rw.RuneWidth(r)
		if cells+w > targetWidth {
			return i
		}
		cells += w
	}
	return len(runes)
}

func isPrintableRune(r rune) bool {
	return !unicode.IsControl(r) && r != 0x7f
}

func printableRune(key string) (rune, bool) {
	if len([]rune(key)) != 1 {
		return 0, false
	}
	r := []rune(key)[0]
	if !isPrintableRune(r) {
		return 0, false
	}
	return r, true
}

func truncatePlaceholder(s string, maxWidth int) string {
	var result strings.Builder
	cells := 0
	for _, r := range s {
		w := rw.RuneWidth(r)
		if cells+w > maxWidth {
			break
		}
		result.WriteRune(r)
		cells += w
	}
	return result.String()
}
