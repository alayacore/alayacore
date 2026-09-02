package terminal

// Markdown table rendering: transforms markdown table blocks into an aligned
// unicode grid. Only tables are handled — all other markdown (bold, inline
// code, …) passes through unchanged, matching the "streaming content carries
// no styling" convention: the grid is emitted as PLAIN text (no SGR), so the
// renderer's bodyStyled/styleBodyLines layer still owns all color, and the dim
// Body color under overlays works on it unchanged.
//
// A table row must start with '|' (after trimming). GFM also accepts rows
// without the leading and trailing pipe, and this renderer deliberately does
// not: the streaming path decides whether a delta could touch a table by
// testing for a line that starts with '|' (deltaHasPipeLine), so relaxing the
// parser without relaxing that test the same way would let table rows slip
// through the incremental path and render half-reflowed. The restriction is
// load-bearing, not an oversight.
//
// The transform is line-based and fence-aware: tables inside fenced code
// blocks are never transformed.
//
// Fitting: a table wider than the terminal is re-flowed, never truncated.
// Columns are allocated by marginal gain and cells that no longer fit are
// HARD-WRAPPED with the same primitive as ordinary body text (wrapContent →
// ansi.Hardwrap), so a row may occupy several terminal rows and a horizontal
// rule separates each pair of records. The frame is kept however cramped the
// columns get; only when a column cannot even hold its widest unbreakable
// grapheme cluster does the layout fall back to a vertical record form
// ("Field  value" per line), which needs no column budget at all. The one
// invariant: no content character is ever dropped. (A space that happens to
// sit exactly on a line break is the one exception — see wrapCell.)
//
// (The previous design shrank columns and cut cells with "…" to hold every
// row to a single visual line; that invariant is gone, and with it the
// truncation.)

import (
	"strings"

	ansi "github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

// hasPipePrefix reports whether a line starts with '|' after trimming
// surrounding whitespace — i.e. it could be a table row.
func hasPipePrefix(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "|")
}

// deltaHasPipeLine reports whether any line of a streaming delta starts
// with '|'. If so, the delta could begin, extend, or re-flow a table, and
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
// mdAlignPlain is the default: the delimiter carried no ':' marker.
//
// Alignment is expressed purely by how a cell is padded inside its column;
// the rules themselves are uniform, so a right-aligned column still draws a
// plain "─────" band. mdAlignPlain and mdAlignLeft therefore RENDER
// IDENTICALLY (both pad right) — the distinction is kept only because it is
// present in the source. Only mdAlignCenter and mdAlignRight branch.
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

// renderMarkdownTables transforms markdown table blocks in content into an
// aligned grid with wrapped (never truncated) cells. All other content
// passes through unchanged.
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

// ============================================================================
// Framing
// ============================================================================

// Grid framing glyphs. Box-drawing is charged one display cell per glyph,
// which is the assumption the whole terminal UI already makes — window borders
// are strings.Repeat("─", width) (see Styles.RenderOpenBoxLines) and the status
// dots, spinners and help separators are all East-Asian Ambiguous glyphs too.
// A terminal that measured those as two cells would break every window frame,
// so there is nothing table-specific to defend against here.
//
// A full grid — a rule between every pair of rows — is what makes multi-line
// rows readable: when a cell spans several terminal rows, a horizontal rule is
// the only thing that marks where a record ends.
var mdGrid = struct {
	vert, horiz            string
	topL, topR, botL, botR string
	crossL, crossR         string
	crossT, crossB, crossX string
}{
	vert: "│", horiz: "─",
	topL: "┌", topR: "┐", botL: "└", botR: "┘",
	crossL: "├", crossR: "┤",
	crossT: "┬", crossB: "┴", crossX: "┼",
}

// mdFraming is the display columns a table row spends on framing: two spaces
// per column plus one vertical rule per column boundary (n+1 of them). Every
// glyph is one cell, so this is 3n+1.
func mdFraming(n int) int { return 3*n + 1 }

// ============================================================================
// Layout
// ============================================================================

// formatTable renders a parsed table as a grid of one-or-more-row records.
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

	// Natural (max-content) width per column, and each column's irreducible
	// width. A cell is never narrower than its widest cluster, so
	// maxc >= minw holds by construction — allocateColumns relies on it.
	maxc, minw := measureColumns(t.rows, n)

	avail := maxWidth - mdFraming(n)

	// How crowded is too crowded is arithmetic, not taste: each column must be
	// at least as wide as the widest unbreakable thing it holds — one grapheme
	// cluster, which is 2 cells for CJK and cannot be split. Below that sum the
	// framed table is undrawable inside the width budget (no amount of wrapping
	// helps), so the record layout takes over. Above it the frame is kept
	// however cramped — a table never silently turns into a list because it
	// looked tidy. The bound is 3n+1 framing plus that sum: two ASCII columns
	// need 9 cells, the same two columns in Chinese need 11, three Chinese
	// columns need 16 (all three verified against the renderer).
	if avail < sumWidths(minw) {
		return formatVertical(t, maxWidth)
	}

	widths := allocateColumns(maxc, minw, avail)
	// The delimiter may declare fewer columns than the widest row.
	align := make([]mdAlign, n)
	copy(align, t.align)

	rule := func(l, m, r string) string {
		segs := make([]string, n)
		for i, w := range widths {
			segs[i] = strings.Repeat(mdGrid.horiz, w+2)
		}
		return l + strings.Join(segs, m) + r
	}

	// Header, then a rule that separates it from the body — but only when
	// there IS a body: a header-only table must not get a dangling rule.
	out := renderRecord(t.rows[0], widths, align)
	if len(t.rows) > 1 {
		out = append(out, rule(mdGrid.crossL, mdGrid.crossX, mdGrid.crossR))
	}
	for ri, row := range t.rows[1:] {
		out = append(out, renderRecord(row, widths, align)...)
		// Between records only — the bottom rule closes the last one.
		if ri < len(t.rows)-2 {
			out = append(out, rule(mdGrid.crossL, mdGrid.crossX, mdGrid.crossR))
		}
	}
	// Top and bottom close the grid.
	out = append([]string{rule(mdGrid.topL, mdGrid.crossT, mdGrid.topR)}, out...)
	return append(out, rule(mdGrid.botL, mdGrid.crossB, mdGrid.botR))
}

// allocateColumns spreads avail display columns over n columns, purely by
// marginal benefit: every spare column goes to the column where it buys the
// most, measured by max/(w*(w+1)) — the drop in that column's wrapped line
// count. Columns start empty and stop at their natural (max-content) width,
// so narrow ones (Size, Used) are never padded into uselessness and leftover
// space simply goes unused.
//
// There is deliberately no starting width or per-column minimum: hard-wrap can
// break anywhere, so nothing needs protecting, and the grid gate above already
// guaranteed by the caller's impossibility gate, so the greedy never has to
// protect a minimum (a seed would be dead code: the columns already start at
// their irreducible width).
func allocateColumns(maxc, minw []int, avail int) []int {
	n := len(maxc)
	w := make([]int, n)
	used := 0
	// Every column starts at its irreducible width, so a column can never be
	// handed less room than its widest unbreakable cluster needs — that is what
	// keeps every rendered row inside the window. The caller has already
	// checked the budget covers this total.
	for i, m := range minw {
		w[i] = m
		used += m
	}
	for used < avail {
		best, bestGain := -1, 0.0
		for i, m := range maxc {
			if w[i] >= m {
				continue // at natural width; more is pure waste
			}
			cur := max(1, w[i])
			if gain := float64(m) / float64(cur*(cur+1)); gain > bestGain {
				bestGain, best = gain, i
			}
		}
		if best == -1 {
			break
		}
		w[best]++
		used++
	}
	return w
}

// renderRecord renders one source row as h terminal rows, where h is the
// height of its tallest wrapped cell. Cells that wrap in one column while
// their neighbors do not are space-filled, which is exactly what keeps the
// vertical rules aligned down the whole record.
func renderRecord(row []string, widths []int, align []mdAlign) []string {
	cols := make([][]string, len(widths))
	h := 1
	for i, w := range widths {
		var cell string
		if i < len(row) {
			cell = row[i]
		}
		lines := wrapCell(cell, w)
		for j := range lines {
			switch align[i] {
			case mdAlignRight:
				lines[j] = padLeft(lines[j], w)
			case mdAlignCenter:
				lines[j] = padCenter(lines[j], w)
			default: // plain and left
				lines[j] = padRight(lines[j], w)
			}
		}
		cols[i] = lines
		if len(lines) > h {
			h = len(lines)
		}
	}
	out := make([]string, 0, h)
	for ln := 0; ln < h; ln++ {
		cells := make([]string, len(widths))
		for i, w := range widths {
			if ln < len(cols[i]) {
				cells[i] = cols[i][ln]
			} else {
				cells[i] = strings.Repeat(" ", w)
			}
		}
		out = append(out, mdGrid.vert+" "+strings.Join(cells, " "+mdGrid.vert+" ")+" "+mdGrid.vert)
	}
	return out
}

// wrapCell hard-wraps a cell to width display columns using the same
// primitive as ordinary body text (wrapContent → ansi.Hardwrap), so it breaks
// at the same offsets the terminal would break a line of prose.
//
// Not byte-identical to prose, deliberately: the space a break landed on is
// dropped from the continuation row, where prose keeps it as a stray leading
// space. Interior spaces and every other character are untouched; a cell's
// leading/trailing whitespace is already gone (splitCells trims). Nothing is
// truncated — overflow becomes another row, never a "…".
func wrapCell(s string, width int) []string {
	if s == "" || width <= 0 {
		return []string{s}
	}
	if ansi.StringWidth(s) <= width {
		return []string{s}
	}
	out := make([]string, 0, 4)
	for _, line := range strings.Split(wrapContent(s, width), "\n") {
		// Spaces at either end are dead weight: the caller pads to width
		// (a right-aligned column would otherwise be pushed left by trailing
		// spaces), and ansi.Hardwrap leaves the space it broke on at the head
		// of the continuation line, which would shift that line by a column.
		// Safe to strip: splitCells already trimmed the cell, so any leading
		// space here came from the wrap itself.
		line = strings.Trim(line, " ")
		// Defensive clamp — the layout charges `width` cells to this line, so
		// anything longer would desynchronize the viewport's line-height
		// accounting. wrapContent should already have bounded it; a lone
		// grapheme cluster wider than width is the one case that cannot be
		// (see the file header), and it is emitted rather than dropped.
		for ansi.StringWidth(line) > width {
			head := takeHead(line, width)
			if head == "" {
				break
			}
			out = append(out, head)
			line = strings.TrimPrefix(line, head)
		}
		out = append(out, line)
	}
	// ansi.Hardwrap can emit an empty leading line when no grapheme cluster
	// fits the requested width (a CJK glyph in a 1-cell column). Such a line
	// carries no content, and as a table cell or a field line it is pure blank
	// noise, so it is dropped — unless it is all there is (an empty cell must
	// still render as one padded row).
	if len(out) > 1 {
		kept := out[:0]
		for _, l := range out {
			if l == "" {
				continue
			}
			kept = append(kept, l)
		}
		out = kept
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

// ============================================================================
// Vertical (record) layout — used when the grid gets too narrow
// ============================================================================

// formatVertical renders each record as a block of fields, separated by a
// plain rule in the app's open-box style. A field is one line when it fits and
// two (label, then indented value) when it does not — see fieldLines. Chosen only when the
// framed table is undrawable — some column cannot fit its widest unbreakable
// cluster within the window (see the gate in formatTable).
//
// It has no label column and therefore no column budget, no shrink rule and no
// threshold: each field is one wrapped text run (see verticalRecord), so this
// layout holds at any width — modulo a single grapheme cluster wider than the
// window itself, which is the same exception every wrapper shares.
func formatVertical(t *mdTable, maxWidth int) []string {
	n := 0
	for _, row := range t.rows {
		if len(row) > n {
			n = len(row)
		}
	}
	if n == 0 {
		return nil
	}
	header := t.rows[0]
	if len(t.rows) == 1 {
		// Header-only table: nothing to pair values with.
		var out []string
		for _, h := range header {
			out = append(out, wrapCell(h, maxWidth)...)
		}
		return out
	}

	sep := strings.Repeat(mdGrid.horiz, max(0, maxWidth))
	var out []string
	for ri, row := range t.rows[1:] {
		if ri > 0 {
			out = append(out, sep)
		}
		out = append(out, verticalRecord(row, header, n, maxWidth)...)
	}
	return out
}

// verticalRecord renders one record of the vertical layout: each field as the
// text "label  value", hard-wrapped like any other line with a hanging indent.
//
// There is no label column, so there is nothing to budget, shrink, or fall
// back to. When a window is too narrow for label and value to share a line,
// the value simply wraps onto the next one — the "stacked" arrangement appears
// as a consequence of ordinary wrapping rather than needing a second layout or
// a readability threshold to choose between them.
func verticalRecord(row, header []string, n, maxWidth int) []string {
	var out []string
	for i := 0; i < n; i++ {
		var label, val string
		if i < len(header) {
			label = header[i]
		}
		if i < len(row) {
			val = row[i]
		}
		out = append(out, fieldLines(label, val, maxWidth)...)
	}
	return out
}

// fieldLines renders one field: "label  value" on a single line when the whole
// field fits, otherwise the label on its own line with the value starting on
// the next one, indented.
//
// Sharing a line only when the ENTIRE field fits is the point: splitting the
// text run at an arbitrary character would chop the label itself and glue the
// value onto its tail ("COMMAN" / "D  /us"), which destroys exactly the
// label/value boundary this layout exists to make obvious. It also hands the
// value the full width instead of whatever was left over.
//
// Indentation is chrome, so it is given up entirely rather than letting a line
// overflow: lay it out, and if any line still exceeds maxWidth, re-lay it with
// no indentation. The only surviving overflow is a lone grapheme cluster wider
// than the window itself.
func fieldLines(label, value string, maxWidth int) []string {
	const indent, hang = "  ", "    "
	full := strings.TrimSpace(label + "  " + value)

	// One line if the whole field fits, chrome included.
	if fits := wrappedAt(full, maxWidth, indent, hang); len(fits) == 1 &&
		!anyTooWide(fits, maxWidth) {
		return fits
	}
	out := append(wrappedAt(label, maxWidth, indent, hang),
		wrappedAt(value, maxWidth, hang, hang)...)
	if !anyTooWide(out, maxWidth) {
		return out
	}
	// Chrome yields: retry with no indentation at all.
	out = append(wrappedAt(label, maxWidth, "", ""), wrappedAt(value, maxWidth, "", "")...)
	return out
}

// wrappedAt hard-wraps text across lines whose budgets differ: the first line
// pays `first`, every continuation pays `every`. Budgeting both against the
// wider prefix would steal cells from line 0 — at a 10-cell window "COMMAND"
// (7 cells) fits behind a 2-space indent but not behind a 4-space one, and it
// was being split for no reason.
func wrappedAt(text string, maxWidth int, first, every string) []string {
	firstRoom := max(1, maxWidth-len(first))
	if ansi.StringWidth(text) <= firstRoom {
		return []string{first + text}
	}
	head := takeHead(text, firstRoom)
	if head == "" {
		// Not even one grapheme cluster fits on line 0. Hand the whole text to
		// the ordinary wrapper, which gives one cluster per line; grabbing all
		// of it here would glue several over-wide clusters onto a single line.
		out := make([]string, 0, 4)
		for ln, line := range wrapCell(text, firstRoom) {
			p := every
			if ln == 0 {
				p = first
			}
			out = append(out, p+line)
		}
		return out
	}
	out := []string{first + strings.TrimRight(head, " ")}
	rest := strings.TrimLeft(strings.TrimPrefix(text, head), " ")
	if rest == "" {
		return out
	}
	for _, line := range wrapCell(rest, max(1, maxWidth-len(every))) {
		out = append(out, every+line)
	}
	return out
}

// anyTooWide reports whether any line exceeds maxWidth display columns.
func anyTooWide(lines []string, maxWidth int) bool {
	for _, l := range lines {
		if ansi.StringWidth(l) > maxWidth {
			return true
		}
	}
	return false
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

// sumWidths totals display columns claimed by a set of columns.
func sumWidths(ws []int) int {
	n := 0
	for _, w := range ws {
		n += w
	}
	return n
}

// widestCluster returns the display width of the widest single grapheme
// cluster in s — the narrowest column s can be hard-wrapped into. A ZWJ emoji
// or combining mark is measured whole, matching what a wrapper must place.
func widestCluster(s string) int {
	best, state := 0, -1
	for s != "" {
		cluster, rest, w, next := uniseg.FirstGraphemeClusterInString(s, state)
		if cluster == "" {
			break
		}
		s, state = rest, next
		if w > best {
			best = w
		}
	}
	return best
}

// measureColumns returns, per column, the natural (max-content) width and the
// irreducible width — the widest single grapheme cluster any cell in that
// column holds. Both are zero for a column nothing was ever put in.
func measureColumns(rows [][]string, n int) (maxc, minw []int) {
	maxc, minw = make([]int, n), make([]int, n)
	for _, row := range rows {
		for i := 0; i < n && i < len(row); i++ {
			if w := ansi.StringWidth(row[i]); w > maxc[i] {
				maxc[i] = w
			}
			if w := widestCluster(row[i]); w > minw[i] {
				minw[i] = w
			}
		}
	}
	return maxc, minw
}
