package terminal

import (
	"regexp"
	"strings"
	"testing"

	ansi "github.com/charmbracelet/x/ansi"

	"github.com/alayacore/alayacore/internal/tlv"
)

// TestRawViewSoftWrapEndToEnd verifies the fixed end-to-end layout with the
// forked raw passthrough renderer: the Terminal's View content is written
// verbatim (no fake newlines inside windows), and when a terminal
// soft-wraps it at the screen width the result is exactly the screen
// height — expanded windows fully visible, prompt input at its position.
func TestRawViewSoftWrapEndToEnd(t *testing.T) {
	m := newTestTerminal()
	wb := m.out.WindowBuffer()

	// Expanded AT (long content, wraps to several visual rows).
	wb.AppendOrUpdate(tlv.TagAssistantT, "1", strings.Repeat("word ", 40))
	// Folded reasoning window.
	wb.AppendOrUpdate(tlv.TagAssistantR, "2", "short reasoning")

	m = m.updateDisplayHeight()
	m = m.updateDisplayHeight()

	v := m.View()
	if !v.AltScreen {
		t.Error("main view must use the alt screen")
	}
	content := v.Content
	t.Logf("VIEW (string rows=%d):\n%s", len(strings.Split(stripANSI(content), "\n")), stripANSI(content))

	// Simulate the terminal: soft-wrap the raw content at the screen width.
	// The renderer emits \r\n (raw mode has no ONLCR): '\r' returns to
	// column 0 before the line feed, so a full-width row followed by a
	// window separator is ONE row — Hardwrap on bare '\n' would insert a
	// spurious blank row at that boundary. Hardwrap is still used below
	// for the content assertions (row count comes from terminalRows).
	wrapped := ansi.Hardwrap(stripANSI(content), 80, true)
	rows := terminalRows(stripANSI(content), 80)
	t.Logf("TERMINAL ROWS=%d", rows)
	if rows != 24 {
		t.Errorf("terminal soft-wrap of View() = %d rows, want 24 (screen height)", rows)
	}

	plain := stripANSI(wrapped)
	if !strings.Contains(plain, "ASSISTANT") {
		t.Error("expanded AT header not shown")
	}
	if !strings.Contains(plain, "word word word") {
		t.Error("expanded AT content not shown")
	}
	if !strings.Contains(plain, "REASONING") {
		t.Error("folded reasoning window not shown")
	}
	if !strings.Contains(plain, "Enter your prompt") {
		t.Error("prompt input not shown")
	}
}

// TestRawViewNoFakeNewlines verifies the copy-fidelity property at the
// end-to-end View level: the display content contains no hard newlines
// inside a window's content (the fragment is continuous text), so a
// terminal selection of a window copies the original text.
func TestRawViewNoFakeNewlines(t *testing.T) {
	m := newTestTerminal()
	wb := m.out.WindowBuffer()
	original := strings.Repeat("word ", 40)
	wb.AppendOrUpdate(tlv.TagAssistantT, "1", original)

	m = m.updateDisplayHeight()
	m = m.updateDisplayHeight()

	content := stripANSI(m.View().Content)
	// The AT fragment runs from the header until the next hard newline
	// (the window separator). Within it there must be no '\n'.
	idx := strings.Index(content, "ASSISTANT")
	if idx < 0 {
		t.Fatal("AT header not found")
	}
	frag := content[idx:]
	if i := strings.Index(frag, "\n"); i >= 0 {
		frag = frag[:i]
	}
	if strings.Contains(frag, "\n") {
		t.Fatal("fragment must be continuous")
	}
	// Strip the UI chrome (header + rules) and layout padding; the
	// remainder is the original text, no fake newlines.
	frag = strings.ReplaceAll(frag, "─", "")
	if i := strings.Index(frag, "ASSISTANT"); i >= 0 {
		frag = frag[i+len("ASSISTANT"):]
	}
	frag = strings.TrimSpace(frag)
	if frag != strings.TrimSpace(original) {
		t.Errorf("copy fidelity broken:\n  got:  %q\n  want: %q", frag, strings.TrimSpace(original))
	}
}

// TestRawOverlayCUPPositioning verifies overlays render for the raw
// passthrough mode: the box rows are written after the base content at
// absolute CUP positions, padded to the box width (the lipgloss
// compositor cannot layer over soft-wrapped fragments).
func TestRawOverlayCUPPositioning(t *testing.T) {
	base := "BASE"
	box := "TOP\nitem\nBOT"
	out := renderOverlay(base, box, 80, 24, 0)
	if !strings.HasPrefix(out, base) {
		t.Errorf("raw overlay must keep the base content first: %q", out)
	}
	// The box rows must appear with absolute CUP sequences (row;col).
	if !regexp.MustCompile(`\x1b\[\d+;\d+H`).MatchString(out) {
		t.Errorf("raw overlay missing CUP sequences: %q", out)
	}
	// The box rows must be padded to the box width (3) so they fully
	// cover the base content beneath.
	if !strings.Contains(out, "TOP ") || !strings.Contains(out, "BOT ") {
		t.Errorf("raw overlay rows must be padded to the box width: %q", out)
	}
	if strings.Contains(out, "item") == false {
		t.Errorf("raw overlay must contain the box rows: %q", out)
	}
}

// terminalRows simulates a terminal soft-wrapping content at the given
// width: '\r' returns to column 0, '\n' moves to the next line, and runs
// longer than the width wrap. This matches the real terminal behavior for
// the renderer's \r\n output — a full-width row followed by \r\n is one
// row, not two.
func terminalRows(s string, width int) int {
	if width <= 0 {
		return 1
	}
	rows := 0
	for _, line := range strings.Split(s, "\n") {
		// '\r' has no display width; remaining runes occupy 1 column each
		// (the test content is ASCII + box-drawing rules).
		w := ansi.StringWidth(line)
		rows += max(1, (w+width-1)/width)
	}
	return rows
}
