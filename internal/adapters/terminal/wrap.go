package terminal

// Line wrapping and truncation utilities for window content rendering.
// These functions handle wrapping styled content at display width
// boundaries while preserving ANSI styles across line breaks, and
// display-width-aware truncation of PLAIN text with a "…" suffix
// (callers apply their own Style after truncating — see
// truncateWithSuffix).
//
// Used by Window.renderer.BuildInner, tool_render.go
// (RenderDiffContent), tui_status.go, model_selector.go,
// theme_selector.go, help_window.go, confirm_dialog.go,
// attachment_window.go, overlay.go, prompt_input.go, and tests.

import (
	"bytes"
	"image/color"
	"io"
	"strings"
	"unicode/utf8"

	ansi "github.com/charmbracelet/x/ansi"
)

// wrapContent wraps styled content at the given display width, preserving
// ANSI styles across line breaks. Updates the wrapping strategy here to
// change how all window content is wrapped.
func wrapContent(s string, width int) string {
	if width < 1 {
		return s
	}
	// Step 1: hard-wrap at cluster boundaries (like a terminal), measured
	// with the same table cellWidth uses — see width.go's header for why
	// this is not ansi.Hardwrap.
	// Step 2: re-apply ANSI styles after each inserted newline
	return restyleBreaks(hardwrapCells(s, width))
}

// restyleBreaks re-applies the style in force across each '\n' in s. A terminal
// drops the pen at a line boundary, so a row a wrap produced would draw
// unstyled from its second row on without this.
//
// It is the half of wrapContent that a caller wanting the rows separately still
// needs, and it is width-preserving by construction: it inserts escape sequences
// and nothing else, and escapes charge no cells. It also adds no '\n', so the
// rows it returns are the rows it was given.
//
// A string the writer would hand back unchanged is handed back here instead —
// canRestyleNothing says why that is a proof and not a guess. It is the common
// case: transcript prose, tool output, most rows of most messages. A per-line
// call used to pay 38 allocations for it, one of them per byte written.
func restyleBreaks(s string) string {
	if canRestyleNothing(s) {
		return s
	}
	var buf bytes.Buffer
	w := NewWrapWriter(&buf)
	defer w.Close()
	_, _ = io.WriteString(w, s) // bytes.Buffer.Write never fails
	return buf.String()
}

// canRestyleNothing reports whether the WrapWriter would emit exactly the bytes
// it is given for s, so that restyleBreaks can skip building one.
//
// Every byte the writer adds is guarded by the pen or the hyperlink being set,
// and only a CSI 'm' or an OSC 8 sets either, so the question is whether s
// carries an introducer. hasEscape answers it for the two forms a well-formed
// string can hold: a 7-bit ESC, and a C1 control in its UTF-8 encoding, C2
// followed by 80..9F.
//
// The well-formedness is a condition and not a formality. The parser is
// byte-oriented and reads a LONE byte in 0x80..9F as a C1 introducer, which
// hasEscape deliberately does not: in valid UTF-8 such a byte is a continuation,
// and reading it as an introducer would put most CJK text on the escape route —
// 文 ends in 0x87. A string that is not valid UTF-8 can therefore carry an
// introducer hasEscape cannot see, and has to go through the writer. That is not
// hypothetical: "\x9b31m red\nacross a break" comes back from the writer
// restyled, and hasEscape finds nothing in it.
//
// TestNothingTheWriterChangesEvadesCanRestyleNothing is the check on this, over
// random bytes rather than a chosen corpus, asserting the implication in the
// direction that has to hold: a string the writer changes is never one this
// reports safe to skip.
func canRestyleNothing(s string) bool {
	return !hasEscape(s) && utf8.ValidString(s)
}

// wrapRows is wrapContent returning the rows it produced and the width of each.
// The widths come from the hard-wrap walk, which charged cells to find the breaks
// in the first place, and restyleBreaks preserves them — so this is what a caller
// that has to know a row's width calls, instead of wrapping and then measuring.
//
// cells is the scratch the widths are appended to, for a caller wrapping many
// lines in a loop; pass cells[:0].
//
// It is not wrapContent with an extra return value, and the two are not one
// function, for the measured reason given on hardwrapCellsWidths: a caller that
// wants one string should not pay for a rows slice and a join to get back to it.
// Expressing wrapContent as strings.Join over wrapRows costs BenchmarkWrapContent
// 5,624 B and 10 allocations per call where it now has none, and 4.02μs where it
// now takes 1.37μs.
func wrapRows(s string, width int, cells []int) (rows []string, widths []int) {
	joined, widths := hardwrapCellsWidths(s, width, cells)
	rows = strings.Split(restyleBreaks(joined), "\n")
	if len(widths) != len(rows) {
		// Cannot happen: hardwrapCellsWidths emits one width per row and
		// restyleBreaks adds no break of its own. Recomputing rather than
		// indexing out of range keeps a rendering path from panicking should
		// that ever stop holding, and TestWrapRowsMatchesWrapContent is what
		// says it stopped.
		widths = make([]int, len(rows))
		for i, r := range rows {
			widths[i] = cellWidth(r)
		}
	}
	return rows, widths
}

// WrapWriter is a writer that writes to a buffer and keeps track of the
// current pen style for the purpose of wrapping with newlines.
//
// When it encounters a newline, it resets the style, writes the newline,
// and then reapplies the style to the next line — so every visual line is
// self-contained (SGR prefix + reset), which the soft-wrap fragment
// pipeline relies on. It is a faithful port of lipgloss v2's WrapWriter
// (which used ultraviolet's Style); the pen parsing and canonical SGR
// re-emission are byte-compatible.
//
// The ansi.Parser comes from a pool (ansi.GetParser in NewWrapWriter, returned
// to it in Close), so building a WrapWriter reuses a parser rather than
// allocating one. The writer itself, its two handler closures and the buffer
// they write into are not pooled, and building one per line is what the wrap
// does: wrapRows calls restyleBreaks once per ORIGINAL line of a message. A
// plain single-row line so paid 38 allocations to move a pen that never left
// zero — one of them per byte it wrote, which is what the scratch below is for.
// restyleBreaks now returns its input untouched when there is no escape in it,
// so that line skips the construction entirely; styled content still builds one
// per line.
type WrapWriter struct {
	w     io.Writer
	p     *ansi.Parser
	style penStyle
	link  link
	// one is the scratch a single byte is written from. Write has to hand the
	// parser every byte in turn, and []byte{b} made that one allocation per
	// byte of every line the wrap produced. An io.Writer must not retain what
	// it is given, so reusing one array for the whole stream is safe.
	one [1]byte
}

// NewWrapWriter returns a new WrapWriter.
func NewWrapWriter(w io.Writer) *WrapWriter {
	pw := &WrapWriter{w: w}
	pw.p = ansi.GetParser()
	handleCsi := func(cmd ansi.Cmd, params ansi.Params) {
		if cmd == 'm' {
			readStyle(params, &pw.style)
		}
	}
	handleOsc := func(cmd int, data []byte) {
		if cmd == 8 {
			readLink(data, &pw.link)
		}
	}
	pw.p.SetHandler(ansi.Handler{
		HandleCsi: handleCsi,
		HandleOsc: handleOsc,
	})
	return pw
}

// Write writes to the buffer.
func (w *WrapWriter) Write(p []byte) (int, error) {
	if w.p == nil {
		// The writer has been closed and its parser returned to the pool.
		// Writing after close can happen during out-of-order teardown of
		// nested writer chains; treat it as a no-op rather than panicking.
		return len(p), nil
	}
	for i := range p {
		b := p[i]
		w.p.Advance(b)
		if b == '\n' {
			if !w.style.IsZero() {
				_, _ = io.WriteString(w.w, ansi.ResetStyle)
			}
			if !w.link.IsZero() {
				_, _ = io.WriteString(w.w, ansi.ResetHyperlink())
			}
		}

		w.one[0] = b
		_, _ = w.w.Write(w.one[:])
		if b == '\n' {
			if !w.link.IsZero() {
				_, _ = io.WriteString(w.w, ansi.SetHyperlink(w.link.URL, w.link.Params))
			}
			if !w.style.IsZero() {
				_, _ = io.WriteString(w.w, w.style.String())
			}
		}
	}

	return len(p), nil
}

// Close closes the writer, resets the style and link if necessary, and
// releases its parser. Calling it is performance critical, but forgetting
// it does not cause safety issues or leaks.
func (w *WrapWriter) Close() error {
	if !w.style.IsZero() {
		_, _ = io.WriteString(w.w, ansi.ResetStyle)
	}
	if !w.link.IsZero() {
		_, _ = io.WriteString(w.w, ansi.ResetHyperlink())
	}
	if w.p != nil {
		ansi.PutParser(w.p)
		w.p = nil
	}
	return nil
}

// penStyle is the current SGR pen state (port of ultraviolet's Style).
type penStyle struct {
	Fg             color.Color
	Bg             color.Color
	UnderlineColor color.Color
	Underline      ansi.Underline
	Attrs          uint8
}

// Pen attributes (bit flags, mirroring ultraviolet's Attr constants).
const (
	penBold uint8 = 1 << iota
	penFaint
	penItalic
	penBlink
	penRapidBlink
	penReverse
	penConceal
	penStrikethrough
)

// IsZero reports whether the style is empty.
func (s penStyle) IsZero() bool {
	return s.Fg == nil && s.Bg == nil && s.UnderlineColor == nil &&
		s.Underline == ansi.UnderlineNone && s.Attrs == 0
}

// String returns the ANSI SGR sequence for the style in the canonical
// attribute order (identical to ultraviolet's Style.String()).
//
//nolint:gocyclo // canonical attribute dispatch
func (s penStyle) String() string {
	if s.IsZero() {
		return ansi.ResetStyle
	}

	var b ansi.Style
	if s.Attrs != 0 {
		if s.Attrs&penBold != 0 {
			b = b.Bold()
		}
		if s.Attrs&penFaint != 0 {
			b = b.Faint()
		}
		if s.Attrs&penItalic != 0 {
			b = b.Italic(true)
		}
		if s.Attrs&penBlink != 0 {
			b = b.Blink(true)
		}
		if s.Attrs&penRapidBlink != 0 {
			b = b.RapidBlink(true)
		}
		if s.Attrs&penReverse != 0 {
			b = b.Reverse(true)
		}
		if s.Attrs&penConceal != 0 {
			b = b.Conceal(true)
		}
		if s.Attrs&penStrikethrough != 0 {
			b = b.Strikethrough(true)
		}
	}
	switch s.Underline {
	case ansi.UnderlineSingle:
		b = b.Underline(true)
	case ansi.UnderlineDouble:
		b = b.UnderlineStyle(ansi.UnderlineDouble)
	case ansi.UnderlineCurly:
		b = b.UnderlineStyle(ansi.UnderlineCurly)
	case ansi.UnderlineDotted:
		b = b.UnderlineStyle(ansi.UnderlineDotted)
	case ansi.UnderlineDashed:
		b = b.UnderlineStyle(ansi.UnderlineDashed)
	}
	if s.Fg != nil {
		b = b.ForegroundColor(s.Fg)
	}
	if s.Bg != nil {
		b = b.BackgroundColor(s.Bg)
	}
	if s.UnderlineColor != nil {
		b = b.UnderlineColor(s.UnderlineColor)
	}

	return b.String()
}

// readStyle reads a Select Graphic Rendition (SGR) escape sequence from a
// list of parameters into pen (port of ultraviolet's ReadStyle).
//
//nolint:gocyclo // SGR parameter dispatch
func readStyle(params ansi.Params, pen *penStyle) {
	if len(params) == 0 {
		*pen = penStyle{}
		return
	}

	for i := 0; i < len(params); i++ {
		param, hasMore, _ := params.Param(i, 0)
		switch param {
		case 0: // Reset
			*pen = penStyle{}
		case 1: // Bold
			pen.Attrs |= penBold
		case 2: // Dim/Faint
			pen.Attrs |= penFaint
		case 3: // Italic
			pen.Attrs |= penItalic
		case 4: // Underline
			nextParam, _, ok := params.Param(i+1, 0)
			if hasMore && ok { // Only accept subparameters i.e. separated by ":"
				switch nextParam {
				case 0, 1, 2, 3, 4, 5:
					i++
					pen.Underline = ansi.Underline(nextParam)
				}
			} else {
				// Single Underline
				pen.Underline = ansi.UnderlineSingle
			}
		case 5: // Slow Blink
			pen.Attrs |= penBlink
		case 6: // Rapid Blink
			pen.Attrs |= penRapidBlink
		case 7: // Reverse
			pen.Attrs |= penReverse
		case 8: // Conceal
			pen.Attrs |= penConceal
		case 9: // Crossed-out/Strikethrough
			pen.Attrs |= penStrikethrough
		case 22: // Normal Intensity (not bold or faint)
			pen.Attrs &^= penBold | penFaint
		case 23: // Not italic, not Fraktur
			pen.Attrs &^= penItalic
		case 24: // Not underlined
			pen.Underline = ansi.UnderlineNone
		case 25: // Blink off
			pen.Attrs &^= penBlink | penRapidBlink
		case 27: // Positive (not reverse)
			pen.Attrs &^= penReverse
		case 28: // Reveal
			pen.Attrs &^= penConceal
		case 29: // Not crossed out
			pen.Attrs &^= penStrikethrough
		case 30, 31, 32, 33, 34, 35, 36, 37: // Set foreground
			pen.Fg = ansi.Black + ansi.BasicColor(param-30) //nolint:gosec // G115: bounded
		case 38: // Set foreground 256 or truecolor
			var c color.Color
			n := ansi.ReadStyleColor(params[i:], &c)
			if n > 0 {
				pen.Fg = c
				i += n - 1
			}
		case 39: // Default foreground
			pen.Fg = nil
		case 40, 41, 42, 43, 44, 45, 46, 47: // Set background
			pen.Bg = ansi.Black + ansi.BasicColor(param-40) //nolint:gosec // G115: bounded
		case 48: // Set background 256 or truecolor
			var c color.Color
			n := ansi.ReadStyleColor(params[i:], &c)
			if n > 0 {
				pen.Bg = c
				i += n - 1
			}
		case 49: // Default Background
			pen.Bg = nil
		case 58: // Set underline color
			var c color.Color
			n := ansi.ReadStyleColor(params[i:], &c)
			if n > 0 {
				pen.UnderlineColor = c
				i += n - 1
			}
		case 59: // Default underline color
			pen.UnderlineColor = nil
		case 90, 91, 92, 93, 94, 95, 96, 97: // Set bright foreground
			pen.Fg = ansi.BrightBlack + ansi.BasicColor(param-90) //nolint:gosec // G115: bounded
		case 100, 101, 102, 103, 104, 105, 106, 107: // Set bright background
			pen.Bg = ansi.BrightBlack + ansi.BasicColor(param-100) //nolint:gosec // G115: bounded
		}
	}
}

// link is an OSC 8 hyperlink (port of ultraviolet's Link).
type link struct {
	URL    string
	Params string
}

// IsZero reports whether the link is unset.
func (l link) IsZero() bool { return l.URL == "" && l.Params == "" }

// readLink reads an OSC 8 hyperlink sequence from a data buffer into link.
func readLink(p []byte, l *link) {
	parts := bytes.Split(p, []byte{';'})
	if len(parts) != 3 {
		return
	}
	l.Params = string(parts[1])
	l.URL = string(parts[2])
}

// visualLine is one rendered visual row of a window.
//
// Text is the row content (one terminal row's worth, no '\n' inside).
// Cont marks rows that are a soft-wrap CONTINUATION of the previous row's
// ORIGINAL line — e.g. a very long single line broken at the window
// width. Cont rows join their predecessor without a newline (the terminal
// soft-wraps them); rows where Cont is false are the start of a new
// original line and are separated from the previous row by a hard '\n'.
// This is what keeps copy-fidelity: only genuinely-long single lines
// become soft-wrap runs — ordinary multi-line content stays multi-line.
//
// Pad is the spaces a frame must append to this row so that the terminal's own
// soft wrap lands on the row boundary instead of somewhere inside the next one.
// It is non-zero only for a row the wrap broke, which is exactly the set of rows a
// frame pads: a row followed by a continuation. Every other row — a window's own
// line, a folded summary, a box rule, a row that ends its original line — leaves it
// at zero, which is the right answer rather than an absent one, and is why no
// construction site has to work out a width it does not need.
//
// The wrap fills it in for free: hardwrapCells charges cells per cluster to find
// the breaks, so the cells a row took are known at the moment it is made and
// expensive at every point after (measuring a styled row copies it, since
// cellWidth strips the escapes first). It is int32 because that fits in the padding
// Cont's bool already leaves in this struct, so carrying it costs no memory —
// 24 bytes a row either way, against 32 for an int.
//
// Pad is relative to the width the row was wrapped at, and a frame pads to the
// width it renders at. Those are the same number: buildLines is keyed on the
// buffer's width and rebuilds when it changes, and every renderer wraps at the
// width it is given (innerWidth := max(0, width)). TestRowsPadToTheWidthTheyDraw
// pins the equality rather than assuming it.
type visualLine struct {
	Text string
	Pad  int32
	Cont bool
}

// joinVisualLines joins visual rows the way they render: continuation
// rows (Cont) follow their predecessor without a newline; rows starting
// a new original line are separated by '\n'.
func joinVisualLines(lines []visualLine) string {
	// One row joins to itself: returning it as-is keeps a folded window's
	// Render from allocating a copy of the single row it already has.
	if len(lines) == 1 {
		return lines[0].Text
	}
	var sb strings.Builder
	for i, l := range lines {
		if i > 0 && !l.Cont {
			sb.WriteByte('\n')
		}
		sb.WriteString(l.Text)
	}
	return sb.String()
}

// wrapVisualLines wraps content into visual rows carrying continuation
// marks: each ORIGINAL line's first row has Cont=false, and rows produced
// by hard-wrapping an over-long single line have Cont=true. Cont=false
// rows must be separated by hard '\n'; Cont=true rows must join their
// predecessor (soft wrap).
func wrapVisualLines(s string, width int) []visualLine {
	var out []visualLine
	// One scratch for the widths of every row of every line, rather than one per
	// line: a message is many lines and most of them make one row. wrapRows fills
	// the slice it is handed and hands it back, so it survives the loop — and
	// reusing it is safe because the widths are read inside the iteration that
	// produced them, when the rows they belong to are being appended to out.
	var widths []int
	for _, part := range strings.Split(s, "\n") {
		// Expand tabs per ORIGINAL line (column resets at '\n'): this is
		// done here — not on the whole content — so the incremental
		// streaming path and the full re-wrap path agree on tab columns
		// (a delta starting with '\t' must expand from the line's actual
		// column, not from 0).
		part = expandTabs(part)
		var rows []string
		switch {
		case width >= 1:
			rows, widths = wrapRows(part, width, widths[:0])
		case part != "":
			rows = []string{part}
			widths = append(widths[:0], cellWidth(part))
		default:
			rows = []string{""}
			widths = append(widths[:0], 0)
		}
		for i, r := range rows {
			// A row the wrap broke is followed by one of its own continuations,
			// and is the row a frame has to pad out to the width it was broken at
			// so the terminal wraps where the row ends. The part's last row ends
			// the original line and is padded by nobody: a selection must not
			// carry trailing spaces. A row wider than the width — the unbreakable
			// cluster that gets a line to itself — pads to nothing, which is what
			// measuring it in the frame used to decide too.
			var pad int32
			if i < len(rows)-1 {
				pad = int32(max(0, width-widths[i])) //nolint:gosec // G115: bounded by width, and non-negative by the max
			}
			out = append(out, visualLine{Text: r, Pad: pad, Cont: i > 0})
		}
	}
	return out
}

// appendDeltaToVisualLines incrementally wraps a delta onto existing
// visual lines, preserving continuation marks.
func appendDeltaToVisualLines(lines []visualLine, delta string, width int) []visualLine {
	if len(lines) == 0 {
		return wrapVisualLines(delta, width)
	}
	if width <= 0 {
		last := lines[len(lines)-1]
		last.Text += delta
		// last.Pad carries over unchanged and was zero: this is its part's last
		// row, which no frame pads, and at this width none could.
		lines[len(lines)-1] = last
		return lines
	}

	if strings.Contains(delta, "\n") {
		return appendDeltaWithNewlinesVisual(lines, delta, width)
	}

	// Append to last line and rewrap: the combined row keeps the last
	// row's continuation state (it is the tail of the same original line).
	last := lines[len(lines)-1]
	combined := last.Text + delta
	newRows := wrapVisualLines(combined, width)
	// The first rewrapped row inherits the original row's Cont state
	// (it may itself be a continuation of an earlier row); wrapVisualLines
	// marks the combined single line's first row Cont=false, so fix it.
	if len(newRows) > 0 {
		newRows[0].Cont = last.Cont
	}
	return append(lines[:len(lines)-1], newRows...)
}

// appendDeltaWithNewlinesVisual handles a delta that contains newlines.
func appendDeltaWithNewlinesVisual(lines []visualLine, delta string, width int) []visualLine {
	parts := strings.Split(delta, "\n")
	for i, part := range parts {
		if i == 0 {
			if len(lines) == 0 {
				lines = wrapVisualLines(part, width)
			} else {
				// Merge the first part onto the last row, keeping its
				// continuation state.
				lastIdx := len(lines) - 1
				combined := lines[lastIdx].Text + part
				newRows := wrapVisualLines(combined, width)
				if len(newRows) > 0 {
					newRows[0].Cont = lines[lastIdx].Cont
				}
				lines = append(lines[:lastIdx], newRows...)
			}
		} else {
			lines = append(lines, wrapVisualLines(part, width)...)
		}
	}
	return lines
}

// wrapLabels wraps a list of labels at word boundaries (separator "  "),
// keeping each label intact unless it is wider than the given width —
// such labels are hard-wrapped into multiple lines so the returned
// string's visual line count matches what the terminal will actually
// render after soft-wrap. Each resulting line is styled with the given
// style.
//
// Layout invariant: every line in the output has display width ≤ width,
// so callers computing line counts (PromptInput.Height,
// PromptInput.AttachmentsOffset) match the terminal's actual row usage.
func wrapLabels(labels []string, width int, style Style) string {
	if len(labels) == 0 {
		return ""
	}
	if width < 1 {
		// No usable width — cannot wrap. Join labels with separator and
		// render so we still produce some output (callers that measure
		// Height() are unaffected because none of them pass width < 1).
		var parts []string
		for _, l := range labels {
			if l != "" {
				parts = append(parts, style.Render(l))
			}
		}
		return strings.Join(parts, "  ")
	}

	var lines []string
	var currentLine strings.Builder

	flushCurrent := func() {
		if currentLine.Len() > 0 {
			lines = append(lines, style.Render(currentLine.String()))
			currentLine.Reset()
		}
	}

	for _, label := range labels {
		if label == "" {
			continue
		}
		labelWidth := cellWidth(label)

		// Single label wider than width: hard-wrap it into multiple lines
		// first. Without this, the label would land on one line wider
		// than width and the terminal's soft-wrap would push every
		// subsequent row down — but Height() / AttachmentsOffset() would
		// still count just one line, so the input box cursor position
		// would land on the wrong row (raw passthrough mode relies on
		// the displayed rows matching the computed row count exactly).
		if labelWidth > width {
			flushCurrent()
			for _, part := range strings.Split(hardwrapCells(label, width), "\n") {
				if part != "" {
					lines = append(lines, style.Render(part))
				}
			}
			continue
		}

		if currentLine.Len() > 0 {
			currentWidth := cellWidth(currentLine.String())
			sepWidth := 2 // "  "
			if currentWidth+sepWidth+labelWidth > width {
				flushCurrent()
				currentLine.WriteString(label)
			} else {
				currentLine.WriteString("  ")
				currentLine.WriteString(label)
			}
		} else {
			currentLine.WriteString(label)
		}
	}
	// Flush the last line. The flush must run after the loop, not inside
	// it: a trailing empty label would otherwise skip the per-item flush
	// and drop the last non-empty line.
	flushCurrent()

	return strings.Join(lines, "\n")
}

// truncateWithSuffix truncates a PLAIN (ANSI-free) string to fit within
// maxWidth display columns, appending "…" to mark the cut. The result is
// guaranteed to be at most maxWidth display columns wide, provided the
// input contains no unexpanded tabs — the width table counts a tab as 0
// width while terminals render it as TabWidth columns. Callers must
// expandTabs (see tool_render.go) before truncating content that may
// contain tabs.
//
// Styling is the caller's job: truncate the plain text first, then render
// with the caller's Style — the ellipsis then inherits the style
// naturally (e.g. the status bar truncates its plain segments and styles
// each segment afterwards). Do NOT pass already-rendered ANSI strings
// here; strip and re-render instead.
func truncateWithSuffix(content string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if cellWidth(content) <= maxWidth {
		return content
	}
	// The marker's own cells come off the budget through the same table
	// that cuts the content — an env-tuned answer for one and a pinned one
	// for the other is exactly the off-by-one this file used to have.
	marker := "…"
	return takeCells(content, maxWidth-cellWidth(marker)) + marker
}
