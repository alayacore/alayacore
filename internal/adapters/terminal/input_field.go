package terminal

// InputField is a text input component supporting multi-line content with a
// single-line display. Users navigate lines with up/down arrows, and the
// visible area shows only the line containing the cursor.

import (
	"image/color"
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

	// fakeCursor renders the painted block cursor (background-colored cell).
	// When false, no cursor cell is painted and the caller is expected to
	// position the real terminal cursor via tea.View.Cursor (used by the
	// main prompt input; overlays keep the painted cursor for now).
	fakeCursor bool

	styleFocused inputFieldStyle
	styleBlurred inputFieldStyle
	cursorColor  color.Color
}

type inputFieldStyle struct {
	Prompt      lipgloss.Style
	Text        lipgloss.Style
	Placeholder lipgloss.Style
}

// NewInputField creates a new InputField with default settings.
func NewInputField() InputField {
	return InputField{
		width:      20,
		Prompt:     "> ",
		focused:    true,
		goalCol:    -1,
		fakeCursor: true,
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
	lineStart, lineEnd := m.currentLine(m.pos)
	lineWidth := runesWidth(m.value[lineStart:lineEnd])

	// If the entire line fits within the viewport, no scrolling needed.
	if lineWidth <= m.width {
		m.offset = 0
		return m
	}

	relPos := m.pos - lineStart // cursor position within current line
	cursorCell := runesWidth(m.value[lineStart : lineStart+relPos])
	visibleEnd := m.offset + m.width
	switch {
	case cursorCell < m.offset:
		m.offset = cursorCell
	case cursorCell >= visibleEnd:
		m.offset = cursorCell - m.width + 2
		if m.offset < 0 {
			m.offset = 0
		}
	}
	return m
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

	visible, cursorIdx := m.buildVisibleText()

	var v string
	v += m.renderText(styleText, visible, cursorIdx)

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
	if cursorIdx >= len(visible) && m.fakeCursor {
		padding-- // cursor(" ") already occupies 1 cell
	}
	if padding < 0 {
		padding = 0
	}
	v += styleText(strings.Repeat(" ", padding))

	return m.promptRender() + v
}

// renderText renders the visible text, optionally painting the fake block
// cursor at cursorIdx (when fakeCursor is enabled). With fakeCursor off the
// text renders plain and the caller positions the real terminal cursor.
func (m InputField) renderText(styleText func(strs ...string) string, visible []rune, cursorIdx int) string {
	var v string
	switch {
	case m.focused && cursorIdx < len(visible):
		pre := string(visible[:cursorIdx])
		at := string(visible[cursorIdx])
		post := string(visible[cursorIdx+1:])
		v += styleText(pre)
		if m.fakeCursor {
			v += m.cursorRender(at)
		} else {
			v += styleText(at)
		}
		v += styleText(post)
	case m.focused:
		v += styleText(string(visible))
		if m.fakeCursor {
			v += m.cursorRender(" ")
		}
	default:
		// Blurred: render text without cursor
		v += styleText(string(visible))
	}
	return v
}

// cursorRender renders a character with the cursor background color.
func (m InputField) cursorRender(s string) string {
	return lipgloss.NewStyle().Background(m.cursorColor).Render(s)
}

// promptRender renders the prompt string.
func (m InputField) promptRender() string {
	styles := m.activeStyle()
	return styles.Prompt.Inline(true).Render(m.Prompt)
}

// buildVisibleText returns the visible portion of the current line as runes
// and the cursor's character index within them.
func (m InputField) buildVisibleText() (visible []rune, cursorIdx int) {
	if len(m.value) == 0 {
		return nil, 0
	}
	lineStart, lineEnd := m.currentLine(m.pos)
	line := m.value[lineStart:lineEnd]
	relPos := m.pos - lineStart // cursor position within the line

	if len(line) == 0 {
		return nil, 0
	}
	// Find start index by cell offset within the line.
	startIdx := 0
	for cells, i := 0, 0; i < len(line); i++ {
		w := rw.RuneWidth(line[i])
		if cells >= m.offset {
			startIdx = i
			break
		}
		if cells+w > m.offset {
			startIdx = i
			break
		}
		cells += w
	}
	// Build visible runes up to width.
	var vis []rune
	visibleStart := startIdx
	cells := 0
	for i := visibleStart; i < len(line); i++ {
		w := rw.RuneWidth(line[i])
		if cells+w > m.width {
			break
		}
		vis = append(vis, line[i])
		cells += w
	}
	// Compute cursor character index within visible.
	cursorChars := 0
	for i := visibleStart; i < len(line) && i < relPos; i++ {
		cursorChars++
	}
	return vis, cursorChars
}

func (m InputField) placeholderView() string {
	styles := m.activeStyle()
	var v string
	if m.focused && m.fakeCursor {
		v = m.cursorRender(" ")
	} else {
		v = " "
	}
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

// WithFakeCursor toggles the painted block cursor. When false, the component
// renders no cursor cell; the caller positions the real terminal cursor via
// tea.View.Cursor (used by the main prompt input).
func (m InputField) WithFakeCursor(fake bool) InputField {
	m.fakeCursor = fake
	return m
}

// CursorCell returns the cursor's line index within the value (0-based) and
// its cell offset within that line after horizontal scrolling (0 = leftmost
// visible cell of the line). The cell offset is the position where the
// cursor block should be rendered — i.e. the first cell of the character
// under the cursor (or the cell after the last character at end-of-line).
func (m InputField) CursorCell() (line, cell int) {
	if len(m.value) == 0 {
		return 0, 0
	}
	lineStart, _ := m.currentLine(m.pos)
	line = 0
	for _, r := range m.value[:lineStart] {
		if r == '\n' {
			line++
		}
	}
	relPos := m.pos - lineStart // cursor position within the line
	cell = runesWidth(m.value[lineStart:lineStart+relPos]) - m.offset
	if cell < 0 {
		cell = 0
	}
	return line, cell
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
		m.pos = ls                                       // go to start of current line
		m.value = slices.Delete(m.value, m.pos-1, m.pos) // delete the \n
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

func (m InputField) WithStyles(focused, blurred inputFieldStyle, cursorColor color.Color) InputField {
	m.styleFocused = focused
	m.styleBlurred = blurred
	m.cursorColor = cursorColor
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
