package terminal

// InputField is a text input component. Its display is always one line — the
// line the cursor is on — and its *capacity* is one line or many: a multi-line
// field's value may contain line breaks (Up/Down navigate them, a pasted block
// keeps them) while a single-line field's value may not, and every door that
// could introduce one removes it. Capacity is set once at construction
// (NewInputField vs NewMultilineInputField) and is the field's own invariant,
// not a routing decision: which box receives text is the owner's question
// (Terminal.keyboardTarget), but a single-line box cannot hold a line break any
// more than a fixed-width one can hold more cells than it has.

import (
	"slices"
	"strings"
	"unicode"
)

// InputField is the Elm-style model for a text input with multi-line support
// but single-line display. Cursor up/down navigates between lines.
//
// Horizontal scrolling is modeled with a rune-index visible start
// (visStart), never a raw cell offset: a rune index is inherently a rune
// boundary, so a wide (CJK) character can never be split by the left edge of
// the viewport — no alignment/rounding helpers are needed. visLine tracks
// which line visStart belongs to so that a line change resets the view.
type InputField struct {
	value       []rune
	pos         int // cursor position in value
	goalCol     int // remembered column position for up/down navigation (-1 = none)
	visLine     int // line start index of the current visible start
	visStart    int // visible start: rune index within the current line
	width       int // visible width (cells)
	Prompt      string
	Placeholder string
	// active is which register the box paints in: the live one pads its value
	// to the full width and renders with the focused styles. It is a rendering
	// fact and nothing else — whether text arrives here is decided by whoever
	// owns the keyboard (Terminal.keyboardTarget), never by the box. A box used
	// to answer that question for itself, which is how a window losing OS focus
	// (a state that decides only painting) could delete a paste.
	active bool

	// multiline reports whether the value may contain line breaks; see the
	// type doc. It is fixed at construction and is what decides whether a
	// break from a paste, an editor buffer, or a WithValue call is kept or
	// stripped — the one place the distinction lives.
	multiline bool

	styleFocused inputFieldStyle
	styleBlurred inputFieldStyle
}

type inputFieldStyle struct {
	Prompt      Style
	Text        Style
	Placeholder Style
}

// NewInputField creates a single-line field: its value may hold no line break,
// and one that a pasted or edited block carries is stripped rather than stored
// (see stripLineBreaks). Single line is the default because a box that cannot
// represent a break must not silently keep one — a filter holding a stranded
// newline matches nothing, and the attachment box uses its value as a path, so
// the corruption would be invisible, whereas a field that wanted breaks and did
// not get them fails visibly.
func NewInputField() InputField {
	return InputField{
		width:   20,
		Prompt:  "> ",
		active:  true,
		goalCol: -1,
	}
}

// NewMultilineInputField creates a field whose value may hold line breaks:
// Up/Down move between the lines and a pasted block keeps its breaks. The
// prompt is the one such field — the one box whose Enter submits a whole draft
// and whose Ctrl+J (PromptInput.InsertNewline) asks for a break.
func NewMultilineInputField() InputField {
	m := NewInputField()
	m.multiline = true
	return m
}

// Init implements Model.
func (m InputField) Init() Cmd { return nil }

// Update implements Model. It accepts what it is handed: an InputField is a
// buffer, not a gate, and deciding whether this box is the user's target is the
// owner's job (see Terminal.keyboardTarget). The owners reach it only for their
// own target — PromptInput.Update from the input's dispatch, FilteredListCore
// from a filter that the same dispatch already chose — so a message that should
// not land here is never sent here in the first place, rather than dropped here.
//
// It returns results, not a Cmd: a text buffer has no I/O to start, and a
// component that reports what happened as a value is what keeps the parent from
// having to unwrap an opaque func() to learn it.
func (m InputField) Update(msg Msg) (InputField, []Result) {
	switch msg := msg.(type) {
	case KeyMsg:
		return m.handleKeyMsg(msg)
	case PasteMsg:
		return m.handlePaste(msg), nil
	}
	return m, nil
}

func (m InputField) handleKeyMsg(msg KeyMsg) (InputField, []Result) {
	key := msg.Chord()

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
func (m InputField) handleMovement(key Chord) (InputField, bool) {
	switch key {
	case keyLeft:
		m = m.moveLeft()
	case keyRight:
		m = m.moveRight()
	case keyUp:
		var ok bool
		m, ok = m.moveLineUp()
		if !ok {
			return m, true
		}
	case keyDown:
		var ok bool
		m, ok = m.moveLineDown()
		if !ok {
			return m, true
		}
	case keyHome:
		m.pos = m.lineStart(m.pos)
		m.visStart = 0
		m.goalCol = -1
		return m.ensureCursorVisible(), true
	case keyEnd:
		m.pos = m.lineEnd(m.pos)
		m.goalCol = -1
		return m.ensureCursorVisible(), true
	default:
		return m, false
	}
	return m.ensureCursorVisible(), true
}

// handleDeletion returns true if the key was a deletion action.
func (m InputField) handleDeletion(key Chord) (InputField, bool) {
	switch key {
	case keyBackspace:
		m = m.deleteBackward()
	case keyDelete:
		m = m.deleteForward()
	default:
		return m, false
	}
	return m.ensureCursorVisible(), true
}

// handleInsertion returns true if the key inserted a character. A printable
// character is inserted as itself; space is nothing special — it is the rune
// 0x20, and isPrintableRune accepts it like any other. The filtering policy is
// the same as handlePaste: isPrintableRune is the one rule for what text may
// enter the buffer.
//
// The character comes from the chord's Code, not from re-parsing the rendered
// string: a chord already carries the rune, and going back through String() to
// recover it was a round trip that could only lose information.
func (m InputField) handleInsertion(key Chord) (InputField, bool) {
	if key.Mod != 0 || key.Code >= KeyExtended || !isPrintableRune(key.Code) {
		return m, false
	}
	m.value = slices.Insert(m.value, m.pos, key.Code)
	m.pos++
	m.goalCol = -1
	return m.ensureCursorVisible(), true
}

// insertNewline inserts a single line break at the cursor. It is the prompt's
// Ctrl+J action (see keybinds.go → handleInputKeys).
//
// This is deliberately a separate primitive rather than
// handlePaste(PasteMsg{Content: "\n"}): handlePaste trims trailing newlines
// (terminals append one to pasted text, so a paste must not leave the caret on
// an empty line), and a lone "\n" is therefore trimmed down to the empty
// string, which its len(filtered) == 0 guard returns unchanged. Inserting a
// line break on request wants the opposite policy — keep the newline — so it
// cannot reuse that path.
//
// goalCol is reset for the same reason the character-insertion path resets it:
// the remembered goal column of up/down navigation belongs to the line the
// cursor was on, and a new line starts at column 0.
//
// On a single-line field there is no line to break to, so it is a no-op — the
// same disposition a pasted break gets (stripLineBreaks). Only the prompt, the
// one multi-line field, is bound to this action, but the field enforces its own
// capacity rather than trusting the caller to have checked.
func (m InputField) insertNewline() InputField {
	if !m.multiline {
		return m
	}
	m.value = slices.Insert(m.value, m.pos, '\n')
	m.pos++
	m.goalCol = -1
	return m.ensureCursorVisible()
}

// blockText is the one rule for text that arrives as a *block* rather than as
// keystrokes: the content between bracketed-paste markers, and the finished
// buffer of an external editor. Line endings are normalized to LF, control
// characters other than the newlines are dropped, and trailing newlines are
// trimmed.
//
// Each part of that is load-bearing on a Windows host, where the two sources
// disagree with every other terminal about line endings: the clipboard ends lines
// with CRLF, and an editor writing a fresh temp file (notepad always, vim with
// fileformat=dos) hands back CRLF too. A bare CR inside the value is not merely
// untidy — it reaches the frame verbatim, and on screen it moves the cursor to
// column 0, painting the rest of the line over the beginning of another.
//
// Trailing newlines go because both sources add one: a terminal appends it to a
// paste, and an editor appends it to the file. A buffer that held only a newline
// therefore filters to nothing, which is the empty prompt the user left behind.
//
// The precedent for treating this as a boundary concern is already in the tree:
// config.ParseKeyValueBlocks normalizes CRLF for the same reason, because the
// model file is edited in the same editors.
func blockText(content string) []rune {
	runes := []rune(content)
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

	filtered := make([]rune, 0, len(normalized))
	for _, r := range normalized {
		if r == '\n' || isPrintableRune(r) {
			filtered = append(filtered, r)
		}
	}
	for len(filtered) > 0 && filtered[len(filtered)-1] == '\n' {
		filtered = filtered[:len(filtered)-1]
	}
	return filtered
}

// handlePaste inserts pasted text at the cursor position, filtered by the block
// rule (blockText) that an editor's buffer goes through as well, and then
// stripped of line breaks when the field is single-line. Both rules apply here
// and in WithBlockValue — the two doors a block enters by — so the field's
// capacity holds no matter which one a paste comes through.
func (m InputField) handlePaste(msg PasteMsg) InputField {
	filtered := blockText(msg.Content)
	if !m.multiline {
		filtered = stripLineBreaks(filtered)
	}
	if len(filtered) == 0 {
		return m
	}
	m.value = slices.Insert(m.value, m.pos, filtered...)
	m.pos += len(filtered)
	m.goalCol = -1
	return m.ensureCursorVisible()
}

// stripLineBreaks returns runes with every line break removed. It is the whole
// of single-line behavior: applied wherever text can enter a single-line
// field, it makes "the value holds no break" true by construction instead of by
// each caller remembering. The break is removed rather than replaced with a
// space — that keeps every other character the block carried, matches the rule
// blockText already applies to characters a buffer cannot represent (tabs and
// NULs are dropped, not substituted), matches the keystroke path where LF
// arrives as Ctrl+J and is not inserted, and matches the HTML rule that a
// single-line <input> strips newlines from its value.
func stripLineBreaks(runes []rune) []rune {
	if !slices.Contains(runes, '\n') {
		return runes
	}
	out := make([]rune, 0, len(runes))
	for _, r := range runes {
		if r != '\n' {
			out = append(out, r)
		}
	}
	return out
}

func (m InputField) ensureCursorVisible() InputField {
	if m.width <= 0 {
		return m
	}
	lineStart, lineEnd := m.currentLine(m.pos)
	line := m.value[lineStart:lineEnd]

	// A new line resets the visible start: display from the beginning.
	// Predictable, and no stale state can be carried across lines.
	if lineStart != m.visLine {
		m.visLine = lineStart
		m.visStart = 0
	}
	// The value is mutable: inserts/deletes can shrink the current line, so
	// a previously valid visStart may now point past its end. Clamp it; the
	// scroll logic below then pulls the view back to the cursor.
	if m.visStart > len(line) {
		m.visStart = len(line)
	}
	// Inserts/deletes can also shift grapheme cluster boundaries within the
	// line (e.g. inserting a character before "a" + combining acute), so a
	// previously valid visStart may no longer be a cluster boundary.
	// Re-anchor it to the containing cluster to keep the invariant
	// "visStart is always a cluster boundary".
	m.visStart = clusterStartAt(line, m.visStart)
	if len(line) == 0 || runesWidth(line) <= m.width {
		m.visStart = 0
		return m
	}

	relPos := m.pos - lineStart // cursor position within the line
	cursorCell := runesWidth(line[:relPos])
	// The rune under the cursor must stay fully visible: a wide (CJK) rune
	// straddling the right edge of the viewport would be clipped in half.
	// At end-of-line there is no rune, but the cursor block itself still
	// needs one cell, so the threshold degrades to the plain cursor check.
	need := 1
	if relPos < len(line) {
		// Width of the cluster at the cursor: the whole cluster must stay
		// visible so it is never split at the right edge of the viewport.
		for _, c := range graphemeClusters(line) {
			if relPos >= c.start && relPos < c.end {
				need = c.width
				break
			}
		}
	}
	startCell := runesWidth(line[:m.visStart])

	switch {
	case cursorCell < startCell:
		// Cursor left of the window: show the cluster containing the cursor
		// at the left edge. Anchor at the cluster start so visStart stays a
		// cluster boundary even when the cursor sits inside a cluster (e.g.
		// between an emoji and its variation selector).
		m.visStart = clusterStartAt(line, relPos)
	case cursorCell+need > startCell+m.width:
		// Scroll so the cluster at the cursor is fully visible: the visible
		// start must be a cluster boundary with
		// startCell >= cursorCell + need - width. Rounding that target up
		// to a cluster boundary (firstRuneStartAtLeast) yields rel <=
		// width-need, so the cluster is never clipped by the right edge —
		// and for 1-cell clusters the cursor can sit at rel = width-1
		// instead of being pushed further left than necessary.
		m.visStart = firstRuneStartAtLeast(line, cursorCell+need-m.width)
		// Physical limit: when the viewport cannot fit the cluster at the
		// cursor (need > width), keep at least the cursor block visible.
		if runesWidth(line[:m.visStart]) > cursorCell {
			m.visStart = clusterStartAt(line, relPos)
		}
	}
	return m
}

// clusterStartAt returns the start index of the grapheme cluster containing
// pos within line (or pos itself when it lies on a cluster boundary or past
// the end of the line).
func clusterStartAt(line []rune, pos int) int {
	if pos <= 0 || pos >= len(line) {
		return pos
	}
	for _, c := range graphemeClusters(line) {
		if pos >= c.start && pos < c.end {
			return c.start
		}
	}
	return pos
}

// firstRuneStartAtLeast returns the index of the first grapheme cluster whose
// start cell is >= target, rounding up to a cluster boundary so a wide
// character (e.g. an emoji) is never split at the left edge of the viewport.
// Returns 0 for target <= 0 and len(line) when target is past the end of the
// line.
func firstRuneStartAtLeast(line []rune, target int) int {
	if target <= 0 {
		return 0
	}
	cells := 0
	for _, c := range graphemeClusters(line) {
		if cells >= target {
			return c.start
		}
		cells += c.width
	}
	return len(line)
}

// View implements Model.
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

	if !m.active {
		return m.promptRender() + v
	}

	// The live box pads to fill the input width.
	// Width comes from the same source as the truncation/cursor math
	// (runesWidth), so padding never disagrees with the rendered text.
	valWidth := runesWidth(visible)
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
// starting at the visible start (visStart) and extending up to the field
// width. Grapheme clusters are never split: a cluster that would exceed the
// width is excluded entirely.
func (m InputField) buildVisibleText() []rune {
	if len(m.value) == 0 {
		return nil
	}
	lineStart, lineEnd := m.currentLine(m.pos)
	line := m.value[lineStart:lineEnd]

	start := min(m.visStart, len(line))
	var vis []rune
	cells := 0
	for _, c := range graphemeClusters(line) {
		if c.end <= start {
			continue // cluster fully before the visible start
		}
		if cells+c.width > m.width {
			break // do not split a cluster at the right edge
		}
		vis = append(vis, line[c.start:c.end]...)
		cells += c.width
	}
	return vis
}

func (m InputField) placeholderView() string {
	styles := m.activeStyle()
	v := " "
	placeholder := m.Placeholder
	// Width from the same source as truncatePlaceholder (runesWidth).
	if m.width > 0 && runesWidth([]rune(placeholder)) > m.width-1 {
		placeholder = truncatePlaceholder(placeholder, m.width-1)
	}
	v += styles.Placeholder.Inline(true).Render(placeholder)
	if !m.active {
		return m.promptRender() + v
	}
	// v contains styled (ANSI) text; measure the plain pieces instead:
	// the leading space plus the (already truncated) placeholder.
	valWidth := 1 + runesWidth([]rune(placeholder))
	if m.width <= 0 || valWidth >= m.width {
		return m.promptRender() + v
	}
	v += strings.Repeat(" ", m.width-valWidth)
	return m.promptRender() + v
}

func (m InputField) activeStyle() inputFieldStyle {
	if m.active {
		return m.styleFocused
	}
	return m.styleBlurred
}

// WithActive says whether this box is the live one, for painting. See the field.
func (m InputField) WithActive(active bool) InputField {
	m.active = active
	return m
}

// IsActive reports the painting state WithActive set.
func (m InputField) IsActive() bool { return m.active }

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
	lineStart, lineEnd := m.currentLine(m.pos)
	line := m.value[lineStart:lineEnd]
	relPos := m.pos - lineStart // cursor position within the line
	start := min(m.visStart, len(line))
	cell := runesWidth(line[:relPos]) - runesWidth(line[:start])
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

// WithValue replaces the whole value and puts the cursor at the end. A
// single-line field strips any line break the value carries, the same rule
// handlePaste applies, so a caller cannot leave such a field holding a
// character it cannot represent.
func (m InputField) WithValue(s string) InputField {
	m.value = []rune(s)
	if !m.multiline {
		m.value = stripLineBreaks(m.value)
	}
	m.pos = len(m.value)
	m.goalCol = -1
	// Explicitly invalidate the visible start: the new value's lineStart can
	// coincidentally equal the old visLine (e.g. both single-line values
	// start at 0), which the line-change detection alone cannot distinguish.
	m.visLine = -1
	m.visStart = 0
	return m.ensureCursorVisible()
}

// WithBlockValue replaces the value with a block of text that arrived whole —
// an external editor's buffer — cursor at the end. Routing it through the field
// rather than letting the caller run blockText itself keeps the block rule in
// one place and makes the field's line capacity hold for the editor exactly as
// it does for a paste: this and handlePaste are the two doors a block enters by.
func (m InputField) WithBlockValue(s string) InputField {
	return m.WithValue(string(blockText(s)))
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

// clusterInfo describes one grapheme cluster: its rune range within a line
// and its terminal display width in cells.
type clusterInfo struct {
	start, end int // rune indices, end exclusive
	width      int // display width in cells
}

// graphemeClusters splits line into grapheme clusters and returns each
// cluster's rune range and terminal display width. This is the single width
// source for the whole input chain: clusters() performs the Unicode text
// segmentation (UAX #29) and measures each cluster from the one table the
// adapter cuts with (width.go), so a cluster renders as one unit — a ZWJ
// family emoji is one cluster, "e" + combining acute is one cluster of one
// cell — and truncation, cursor placement, scrolling, and padding always
// agree and never split a cluster.
func graphemeClusters(line []rune) []clusterInfo {
	if len(line) == 0 {
		return nil
	}
	cs := clusters(string(line))
	out := make([]clusterInfo, 0, len(cs))
	for _, c := range cs {
		out = append(out, clusterInfo{start: c.runeStart, end: c.runeEnd, width: c.cells})
	}
	return out
}

func runesWidth(runes []rune) int {
	total := 0
	for _, c := range graphemeClusters(runes) {
		total += c.width
	}
	return total
}

// runeIndexAtWidth returns the rune index into runes where the accumulated
// cluster width first meets or exceeds targetWidth, stopping at a grapheme
// cluster boundary (a cluster is never split). If targetWidth exceeds the
// total width, returns len(runes).
func runeIndexAtWidth(runes []rune, targetWidth int) int {
	cells := 0
	for _, c := range graphemeClusters(runes) {
		if cells+c.width > targetWidth {
			return c.start
		}
		cells += c.width
	}
	return len(runes)
}

func isPrintableRune(r rune) bool {
	return !unicode.IsControl(r) && r != 0x7f
}

func truncatePlaceholder(s string, maxWidth int) string {
	runes := []rune(s)
	var result strings.Builder
	cells := 0
	for _, c := range graphemeClusters(runes) {
		if cells+c.width > maxWidth {
			break
		}
		result.WriteString(string(runes[c.start:c.end]))
		cells += c.width
	}
	return result.String()
}
