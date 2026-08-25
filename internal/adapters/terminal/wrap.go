package terminal

// Line wrapping and truncation utilities for window content rendering.
// These functions handle wrapping styled content at display width
// boundaries while preserving ANSI styles across line breaks, and
// display-width-aware truncation with a "…" suffix.
//
// Used by Window.renderer.BuildInner, tool_render.go
// (RenderDiffContent), model_selector.go, help_window.go,
// theme_selector.go, prompt_input.go, and tests.

import (
	"bytes"
	"image/color"
	"io"
	"strings"

	ansi "github.com/charmbracelet/x/ansi"
)

// wrapContent wraps styled content at the given display width, preserving
// ANSI styles across line breaks. Updates the wrapping strategy here to
// change how all window content is wrapped.
func wrapContent(s string, width int) string {
	if width < 1 {
		return s
	}
	// Step 1: hard-wrap at character boundaries (like a terminal)
	s = ansi.Hardwrap(s, width, true)
	// Step 2: re-apply ANSI styles after each inserted newline
	var buf bytes.Buffer
	w := NewWrapWriter(&buf)
	defer w.Close()
	_, _ = io.WriteString(w, s) // bytes.Buffer.Write never fails
	return buf.String()
}

// Wrap wraps the given string to the given width at word boundaries,
// preserving ANSI styles across lines (mirrors lipgloss.Wrap).
func Wrap(s string, width int, breakpoints string) string {
	var buf bytes.Buffer
	s = ansi.Wrap(s, width, breakpoints)
	w := NewWrapWriter(&buf)
	defer w.Close() //nolint:errcheck // Close only flushes the style reset; errors are impossible for a bytes.Buffer
	_, _ = io.WriteString(w, s)
	return buf.String()
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
// The ansi.Parser is allocated once per WrapWriter (via ansi.GetParser
// in NewWrapWriter, returned to the pool in Close). wrapContent and
// Wrap call NewWrapWriter once per invocation, so the pool does see
// reuse across consecutive renders — it is not allocated per line as
// the audit suggested.
type WrapWriter struct {
	w     io.Writer
	p     *ansi.Parser
	style penStyle
	link  link
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
				_, _ = w.w.Write([]byte(ansi.ResetStyle))
			}
			if !w.link.IsZero() {
				_, _ = w.w.Write([]byte(ansi.ResetHyperlink()))
			}
		}

		_, _ = w.w.Write([]byte{b})
		if b == '\n' {
			if !w.link.IsZero() {
				_, _ = w.w.Write([]byte(ansi.SetHyperlink(w.link.URL, w.link.Params)))
			}
			if !w.style.IsZero() {
				_, _ = w.w.Write([]byte(w.style.String()))
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
		_, _ = w.w.Write([]byte(ansi.ResetStyle))
	}
	if !w.link.IsZero() {
		_, _ = w.w.Write([]byte(ansi.ResetHyperlink()))
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
type visualLine struct {
	Text string
	Cont bool
}

// joinVisualLines joins visual rows the way they render: continuation
// rows (Cont) follow their predecessor without a newline; rows starting
// a new original line are separated by '\n'.
func joinVisualLines(lines []visualLine) string {
	var sb strings.Builder
	for i, l := range lines {
		if i > 0 && !l.Cont {
			sb.WriteByte('\n')
		}
		sb.WriteString(l.Text)
	}
	return sb.String()
}

// wrapLines wraps content into lines at the given width.
func wrapLines(content string, width int) []string {
	if width <= 0 {
		return []string{content}
	}
	wrapped := wrapContent(content, width)
	return strings.Split(wrapped, "\n")
}

// wrapVisualLines wraps content into visual rows carrying continuation
// marks: each ORIGINAL line's first row has Cont=false, and rows produced
// by hard-wrapping an over-long single line have Cont=true. Cont=false
// rows must be separated by hard '\n'; Cont=true rows must join their
// predecessor (soft wrap).
func wrapVisualLines(s string, width int) []visualLine {
	var out []visualLine
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
			rows = strings.Split(wrapContent(part, width), "\n")
		case part != "":
			rows = []string{part}
		default:
			rows = []string{""}
		}
		for i, r := range rows {
			out = append(out, visualLine{Text: r, Cont: i > 0})
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

// appendDeltaToLines incrementally wraps a delta onto existing lines.
func appendDeltaToLines(lines []string, delta string, width int) []string {
	if len(lines) == 0 {
		return wrapLines(delta, width)
	}
	if width <= 0 {
		lines[len(lines)-1] += delta
		return lines
	}

	if strings.Contains(delta, "\n") {
		return appendDeltaWithNewlines(lines, delta, width)
	}

	// Append to last line and rewrap
	lastLine := lines[len(lines)-1]
	combined := lastLine + delta
	newLines := wrapLines(combined, width)
	return append(lines[:len(lines)-1], newLines...)
}

// appendDeltaWithNewlines handles delta that contains newlines.
func appendDeltaWithNewlines(lines []string, delta string, width int) []string {
	parts := strings.Split(delta, "\n")
	for i, part := range parts {
		if i == 0 {
			if len(lines) == 0 {
				lines = wrapLines(part, width)
			} else {
				lastIdx := len(lines) - 1
				combined := lines[lastIdx] + part
				newLines := wrapLines(combined, width)
				lines = append(lines[:lastIdx], newLines...)
			}
		} else {
			lines = append(lines, wrapLines(part, width)...)
		}
	}
	return lines
}

// styleMultiline applies a style to each line of text.
func styleMultiline(content string, style Style) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = style.Render(line)
	}
	return strings.Join(lines, "\n")
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
		labelWidth := ansi.StringWidth(label)

		// Single label wider than width: hard-wrap it into multiple lines
		// first. Without this, the label would land on one line wider
		// than width and the terminal's soft-wrap would push every
		// subsequent row down — but Height() / AttachmentsOffset() would
		// still count just one line, so the input box cursor position
		// would land on the wrong row (raw passthrough mode relies on
		// the displayed rows matching the computed row count exactly).
		if labelWidth > width {
			flushCurrent()
			for _, part := range strings.Split(ansi.Hardwrap(label, width, true), "\n") {
				if part != "" {
					lines = append(lines, style.Render(part))
				}
			}
			continue
		}

		if currentLine.Len() > 0 {
			currentWidth := ansi.StringWidth(currentLine.String())
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

// truncateWithSuffix truncates content to fit within maxWidth, appending "…"
// to indicate content has been cut. The result is guaranteed to be at most
// maxWidth display columns wide, provided the input contains no unexpanded
// tabs — ansi.StringWidth counts a tab as 0 width while terminals render it
// as TabWidth columns. Callers must expandTabs (see tool_render.go) before
// truncating content that may contain tabs.
//
// ANSI styling is preserved: escape sequences are never broken, and the
// ellipsis is inserted at the truncation point while the SGR state active
// there is still open — so a truncated styled segment (including a segment
// reduced to a single column) keeps its color instead of falling back to
// the terminal default. Trailing escapes of the cut-off remainder are
// carried along inertly and closed by their original resets.
func truncateWithSuffix(content string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	return ansi.Truncate(content, maxWidth, "…")
}
