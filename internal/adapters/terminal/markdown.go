package terminal

// Markdown table rendering: transforms GFM-style markdown table blocks
// into aligned tables with padded columns. Only tables are handled —
// all other markdown (bold, inline code, …) passes through unchanged,
// matching the "streaming content carries no styling" convention (the
// dim Body color applied under overlays is layered on by the renderer's
// bodyStyled/styleBodyLines, never here).
//
// The transform is line-based and fence-aware: tables inside fenced code
// blocks are never transformed. Rows wider than the terminal are fitted
// by shrinking the widest columns first (cells truncated with "…"),
// mirroring the column-fitting used by model_selector and help_window.

import (
	"strings"

	ansi "github.com/charmbracelet/x/ansi"
)

// tableMinColWidth is the floor for a column during fit-to-width
// shrinking. Below it a table is left wider than the terminal and the
// normal hard-wrap path (wrapVisualLines) takes over — line heights stay
// correct, only the alignment is lost in that degenerate case.
const tableMinColWidth = 3

// hasPipePrefix reports whether a line starts with '|' after trimming
// surrounding whitespace — i.e. it could be a table row.
func hasPipePrefix(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "|")
}

// deltaHasPipeLine reports whether any line of a streaming delta starts
// with '|'. If so, the delta could begin, extend, or re-pad a table, and
// the incremental wrap path is unsafe. Allocation-free (hot path: runs
// for every delta in every markdown-mode window).
func deltaHasPipeLine(delta string) bool {
	lineStart := 0
	for i := 0; i <= len(delta); i++ {
		if i == len(delta) || delta[i] == '\n' {
			// Skip leading spaces/tabs, then check for '|'.
			j := lineStart
			for j < i && (delta[j] == ' ' || delta[j] == '\t') {
				j++
			}
			if j < i && delta[j] == '|' {
				return true
			}
			lineStart = i + 1
		}
	}
	return false
}

// lastLine returns the substring after the last '\n' (empty when the
// string ends with a newline — a trailing blank line closes a table).
func lastLine(s string) string {
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// mdAlign is a column's alignment, derived from the delimiter row.
// mdAlignPlain is the default: the delimiter had no ':' marker, rendered
// as plain dashes (matches the common "|---|---|" form).
type mdAlign int

const (
	mdAlignPlain mdAlign = iota
	mdAlignLeft
	mdAlignRight
	mdAlignCenter
)

// mdTable is a parsed markdown table block: header row first, then the
// delimiter-derived alignment, then body rows. Column count is the max
// cell count across all rows; shorter rows are padded with empty cells.
type mdTable struct {
	align []mdAlign
	rows  [][]string
}

// renderMarkdownTables transforms markdown table blocks in content into
// aligned, padded tables. Each table row is fitted to maxWidth display
// columns when possible. All other content passes through unchanged.
func renderMarkdownTables(content string, maxWidth int) string {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	inFence := false
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSuffix(lines[i], "\r")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			out = append(out, line)
			continue
		}
		if inFence {
			out = append(out, line)
			continue
		}
		if t, n := parseTable(lines[i:]); t != nil {
			out = append(out, formatTable(t, maxWidth)...)
			i += n - 1
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// parseTable parses a table block starting at lines[0]. Returns nil when
// lines[0] is not a table header (a '|' row followed by a delimiter row
// whose cells are all dashes/colons). On success returns the table and
// the number of consumed lines (header + delimiter + body rows).
func parseTable(lines []string) (*mdTable, int) {
	if len(lines) < 2 {
		return nil, 0
	}
	header := expandTabs(strings.TrimSuffix(lines[0], "\r"))
	if !isTableRow(header) {
		return nil, 0
	}
	headerCells := splitCells(header)
	if len(headerCells) == 0 {
		return nil, 0
	}
	delim := expandTabs(strings.TrimSuffix(lines[1], "\r"))
	align, ok := parseDelimiter(delim)
	if !ok {
		return nil, 0
	}

	t := &mdTable{align: align}
	t.rows = append(t.rows, headerCells)

	n := 2
	for n < len(lines) {
		body := expandTabs(strings.TrimSuffix(lines[n], "\r"))
		if !isTableRow(body) {
			break
		}
		t.rows = append(t.rows, splitCells(body))
		n++
	}
	return t, n
}

// isTableRow reports whether the line looks like a markdown table row
// (starts with '|' after trimming surrounding whitespace).
func isTableRow(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "|")
}

// parseDelimiter validates a delimiter row and returns per-column
// alignment. Every cell must be dashes with optional ':' alignment
// markers (e.g. "---", ":---", "---:", ":---:").
func parseDelimiter(line string) ([]mdAlign, bool) {
	if !isTableRow(line) {
		return nil, false
	}
	cells := splitCells(line)
	if len(cells) == 0 {
		return nil, false
	}
	align := make([]mdAlign, len(cells))
	for i, c := range cells {
		if !isDelimiterCell(c) {
			return nil, false
		}
		align[i] = delimiterAlign(c)
	}
	return align, true
}

// isDelimiterCell reports whether a trimmed cell is a valid delimiter
// segment: only dashes and colons, with at least one dash.
func isDelimiterCell(c string) bool {
	if c == "" {
		return false
	}
	hasDash := false
	for _, r := range c {
		switch r {
		case '-':
			hasDash = true
		case ':':
		default:
			return false
		}
	}
	return hasDash
}

// delimiterAlign derives the column alignment from a delimiter cell:
// ":---:" center, "---:" right, ":---" left, "---" plain.
func delimiterAlign(c string) mdAlign {
	left := strings.HasPrefix(c, ":")
	right := strings.HasSuffix(c, ":")
	switch {
	case left && right:
		return mdAlignCenter
	case right:
		return mdAlignRight
	case left:
		return mdAlignLeft
	default:
		return mdAlignPlain
	}
}

// splitCells splits a table row into cells on unescaped '|', trimming
// each cell. The leading '|' is consumed; a trailing '|' produces a
// trailing empty cell that is dropped. "\|" is an escaped literal pipe.
func splitCells(line string) []string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "|") {
		return nil
	}
	var cells []string
	var cur strings.Builder
	escaped := false
	flush := func() {
		cells = append(cells, strings.TrimSpace(cur.String()))
		cur.Reset()
	}
	for _, r := range line[1:] {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == '|':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	// Drop the trailing empty cell left by a trailing '|'.
	if len(cells) > 0 && cells[len(cells)-1] == "" && strings.HasSuffix(line, "|") {
		cells = cells[:len(cells)-1]
	}
	return cells
}

// formatTable renders a parsed table: header row, delimiter row, body
// rows — every row fitted to maxWidth when it would overflow.
func formatTable(t *mdTable, maxWidth int) []string {
	n := 0
	for _, row := range t.rows {
		if len(row) > n {
			n = len(row)
		}
	}
	if n == 0 {
		return nil
	}
	// The delimiter may declare fewer columns than the widest row.
	align := make([]mdAlign, n)
	copy(align, t.align)

	// Natural column widths from the widest cell in each column.
	widths := make([]int, n)
	for _, row := range t.rows {
		for i := 0; i < n; i++ {
			var cell string
			if i < len(row) {
				cell = row[i]
			}
			if w := ansi.StringWidth(cell); w > widths[i] {
				widths[i] = w
			}
		}
	}
	shrinkToFit(widths, maxWidth)

	lines := make([]string, 0, len(t.rows)+1)
	lines = append(lines, formatRow(t.rows[0], widths, align))
	lines = append(lines, formatDelimiter(widths, align))
	for _, row := range t.rows[1:] {
		lines = append(lines, formatRow(row, widths, align))
	}
	return lines
}

// shrinkToFit reduces column widths until the widest table row fits
// maxWidth display columns. The widest column is shrunk first, so short
// cells stay intact as long as possible. Columns stop at
// tableMinColWidth; if the table still overflows, it is left as-is and
// the hard-wrap path handles it.
func shrinkToFit(widths []int, maxWidth int) {
	if maxWidth <= 0 {
		return
	}
	// Row width = "| " + c1 + " | " + c2 + … + " |": 3 chars of framing
	// per column (2 for "| " + 1 shared pad) plus one final "|".
	rowWidth := sumWidths(widths) + 3*len(widths) + 1
	overflow := rowWidth - maxWidth
	for overflow > 0 {
		widest := -1
		for i, w := range widths {
			if w > tableMinColWidth && (widest == -1 || w > widths[widest]) {
				widest = i
			}
		}
		if widest == -1 {
			return // all at floor; leave the rest to hard-wrap
		}
		widths[widest]--
		overflow--
	}
}

func sumWidths(widths []int) int {
	sum := 0
	for _, w := range widths {
		sum += w
	}
	return sum
}

// formatRow renders one data row (header or body): "| c1 | c2 | … |",
// each cell padded to its column width with the column's alignment.
func formatRow(row []string, widths []int, align []mdAlign) string {
	parts := make([]string, len(widths))
	for i, w := range widths {
		var cell string
		if i < len(row) {
			cell = row[i]
		}
		cell = fitCell(cell, w)
		switch align[i] {
		case mdAlignRight:
			parts[i] = padLeft(cell, w)
		case mdAlignCenter:
			parts[i] = padCenter(cell, w)
		default: // plain and left
			parts[i] = padRight(cell, w)
		}
	}
	return "| " + strings.Join(parts, " | ") + " |"
}

// formatDelimiter renders the delimiter row. Each segment spans the same
// width as the padded cell above it (w+2), with ':' markers where the
// source delimiter declared an alignment.
func formatDelimiter(widths []int, align []mdAlign) string {
	segs := make([]string, len(widths))
	for i, w := range widths {
		switch align[i] {
		case mdAlignRight:
			segs[i] = strings.Repeat("-", w+1) + ":"
		case mdAlignCenter:
			segs[i] = ":" + strings.Repeat("-", w) + ":"
		case mdAlignLeft:
			segs[i] = ":" + strings.Repeat("-", w+1)
		default: // plain
			segs[i] = strings.Repeat("-", w+2)
		}
	}
	return "|" + strings.Join(segs, "|") + "|"
}

// fitCell truncates a cell to fit width display columns, keeping the
// head and marking the cut with "…" (display-width aware).
func fitCell(cell string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(cell) <= width {
		return cell
	}
	return truncateWithSuffix(cell, width)
}

// padRight pads a cell to width with trailing spaces (left aligned).
func padRight(cell string, width int) string {
	return cell + strings.Repeat(" ", max(0, width-ansi.StringWidth(cell)))
}

// padLeft pads a cell to width with leading spaces (right aligned).
func padLeft(cell string, width int) string {
	return strings.Repeat(" ", max(0, width-ansi.StringWidth(cell))) + cell
}

// padCenter pads a cell to width with spaces on both sides (centered).
func padCenter(cell string, width int) string {
	pad := max(0, width-ansi.StringWidth(cell))
	left := pad / 2
	return strings.Repeat(" ", left) + cell + strings.Repeat(" ", pad-left)
}
