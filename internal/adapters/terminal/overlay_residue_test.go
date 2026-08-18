package terminal

// Regression test for the overlay-renderer residue bug: when a new frame
// places a SHORT row (e.g. a folded line) on a screen row where the
// previous frame had a LONGER row (e.g. an assistant content line), the
// old characters would remain on the row tail — "my os?" became
// "my os?el's Core Ultra...", and folded lines trailed rules. The EL
// erase after every fragment's unpadded last row and on every blank pad
// row now clears that residue.

import (
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/tlv"
	"github.com/charmbracelet/x/ansi"
)

// TestOverlayResidueFoldedOverContent simulates the exact reported
// scenario at width 137: an assistant line is on screen; a later frame
// puts a short folded USER line on the same row. The residue (the tail of
// the old assistant line) must not survive.
func TestOverlayResidueFoldedOverContent(t *testing.T) {
	const W = 137
	m := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, &app.Config{}, W, 24, theme.DefaultTheme(), nil, "theme-dark")
	m.out.SetWindowWidth(W)
	mm, _ := m.Update(WindowSizeMsg{Width: W, Height: 24})
	m = mm.(Terminal)
	wb := m.out.WindowBuffer()

	// Frame 1: assistant expanded (long row on screen row N).
	wb.AppendOrUpdate(tlv.TagAssistantT, "a1", `This is part of Intel's Core Ultra 200 series (Arrow Lake architecture, launched late 2024).`)
	mm, _ = m.Update(WindowSizeMsg{Width: W, Height: 24})
	m = mm.(Terminal)
	frame1 := m.View().Content
	t.Logf("frame1 contains long assistant row: %v", strings.Contains(stripANSI(frame1), "Core Ultra 200 series"))

	// Frame 2: a folded USER window appears — its short row lands on a
	// screen row previously occupied by a long assistant row (or rule).
	wb.AppendOrUpdate(tlv.TagUserT, "u1", "my os?")
	mm, _ = m.Update(WindowSizeMsg{Width: W, Height: 24})
	m = mm.(Terminal)
	frame2 := m.View().Content

	// The terminal applies frame2 over frame1. Without EL erases, the row
	// "▶ USER PROMPT my os?" would trail "el's Core Ultra..."; with them,
	// the row is cleared at its end.
	plain2 := stripANSI(frame2)
	// Check the USER folded row specifically: its content must be exactly
	// the folded line (no pollution from the old assistant row).
	for _, row := range splitTerminalRows(plain2, W) {
		if strings.HasPrefix(row, "▶ USER") {
			trimmed := strings.TrimRight(row, " ")
			if trimmed != "▶ USER PROMPT my os?" {
				t.Errorf("USER folded row polluted by residue: %q (trimmed %q)", row, trimmed)
			}
		}
		// No folded row may trail rule characters (the old rule residue).
		if strings.HasPrefix(row, "▶ ") && strings.Contains(row, "─") {
			t.Errorf("folded row trails rule residue: %q", row)
		}
	}
	// The frame must contain EL erases (the residue-cleanup mechanism).
	if !strings.Contains(frame2, "\x1b[K") {
		t.Error("frame should contain EL erases for residue cleanup")
	}
}

// splitTerminalRows splits plain content into terminal rows of the given
// width (rune-based; test content is ASCII + box drawing).
func splitTerminalRows(s string, width int) []string {
	var rows []string
	cur := ""
	col := 0
	for _, r := range s {
		if r == '\n' {
			rows = append(rows, cur)
			cur = ""
			col = 0
			continue
		}
		w := ansi.StringWidth(string(r))
		cur += string(r)
		col += w
		if col >= width {
			rows = append(rows, cur)
			cur = ""
			col = 0
		}
	}
	if cur != "" {
		rows = append(rows, cur)
	}
	return rows
}
