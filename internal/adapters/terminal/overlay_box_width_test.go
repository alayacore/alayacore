package terminal

// Regression tests for the overlay row-diff wipe: overlay boxes span the
// FULL terminal width by design (all overlay input boxes are as wide as
// the terminal), so their CUP rows share row coordinates with the base
// rows at column 1. The row diff must distinguish base rows from overlay
// rows and never rewrite a base row that an overlay row covers — on Tab
// (or any steady-frame change) the overlay would otherwise be wiped,
// showing the windows behind.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/tlv"
)

// TestOverlayBoxSpansTerminalWidth locks the design: overlay boxes span
// the FULL terminal width (all overlay input boxes are as wide as the
// terminal), so WithSize sizes the box width — and the filter input — to
// the terminal width, and the box starts at column 1.
func TestOverlayBoxSpansTerminalWidth(t *testing.T) {
	styles := DefaultStyles()

	ms := NewModelSelector(styles).WithSize(137, 50)
	if ms.Width != 137 {
		t.Errorf("model selector width = %d, want terminal width 137", ms.Width)
	}
	ts := NewThemeSelector(styles).WithSize(137, 50)
	if ts.Width != 137 {
		t.Errorf("theme selector width = %d, want terminal width 137", ts.Width)
	}
	hw := NewHelpWindow(styles).WithSize(137, 50)
	if hw.Width != 137 {
		t.Errorf("help window width = %d, want terminal width 137", hw.Width)
	}
	aw := NewAttachmentWindow(styles).WithSize(137, 50)
	if aw.Width != 137 {
		t.Errorf("attachment window width = %d, want terminal width 137", aw.Width)
	}
	// The full-width box starts at column 1 (origin x = 0).
	m := newTestTerminal()
	m.modelSelector = m.modelSelector.Open().WithSize(137, 50)
	box := m.modelSelector.View().Content
	if x, _ := overlayOrigin(box, 137, 50); x != 0 {
		t.Errorf("overlay origin x = %d, want 0 (full-width box)", x)
	}
}

// TestTabKeepsOverlayAtWideTerminal drives the real flow at width 137 with
// real window content through Screen and a terminal grid: after Tab, the
// overlay box must still be on screen (its rows must not be wiped by the
// row diff).
func TestTabKeepsOverlayAtWideTerminal(t *testing.T) {
	const W = 137
	m := newTestTerminal()
	m.windowWidth = W
	m.windowHeight = 50
	wb := m.out.WindowBuffer()
	wb.AppendOrUpdate(tlv.TagAssistantT, "a1", "assistant answer line one\nline two more content")
	wb.AppendOrUpdate(tlv.TagUserT, "u1", "user prompt here")
	m.display = m.display.updateContent()
	m = m.openModelSelector()

	tab := KeyPressMsg(Key{Code: KeyTab})
	buf := &bytes.Buffer{}
	s := &Screen{out: buf}
	grid := make([][]rune, 50)
	for i := range grid {
		grid[i] = make([]rune, W)
		for j := range grid[i] {
			grid[i][j] = ' '
		}
	}

	v1 := m.View()
	if err := s.Render(v1.Content, v1.Cursor, true); err != nil {
		t.Fatal(err)
	}
	applyFrame(grid, buf.String(), W)
	buf.Reset()

	mm, _ := m.Update(tab)
	m = mm.(Terminal)
	v2 := m.View()
	if err := s.Render(v2.Content, v2.Cursor, true); err != nil {
		t.Fatal(err)
	}
	applyFrame(grid, buf.String(), W)

	found := false
	for r := 0; r < 50; r++ {
		if strings.Contains(string(grid[r]), "Model Selector") {
			found = true
			break
		}
	}
	if !found {
		t.Error("overlay box vanished after Tab at width 137 — base rows rewrote over it")
	}
}

// TestTabKeepsOverlayNarrowTerminal covers the overlap edge case: a
// terminal narrower than the box puts the overlay at column 1 (sharing
// row coordinates with the base). The row diff must not rewrite the base
// rows underneath the overlay.
func TestTabKeepsOverlayNarrowTerminal(t *testing.T) {
	const W = 50 // narrower than the 60-col box
	m := newTestTerminal()
	m.windowWidth = W
	m.windowHeight = 20
	wb := m.out.WindowBuffer()
	wb.AppendOrUpdate(tlv.TagAssistantT, "a1", "assistant answer line one")
	m.display = m.display.updateContent()
	m = m.openModelSelector()

	tab := KeyPressMsg(Key{Code: KeyTab})
	buf := &bytes.Buffer{}
	s := &Screen{out: buf}
	grid := make([][]rune, 20)
	for i := range grid {
		grid[i] = make([]rune, W)
		for j := range grid[i] {
			grid[i][j] = ' '
		}
	}

	v1 := m.View()
	if err := s.Render(v1.Content, v1.Cursor, true); err != nil {
		t.Fatal(err)
	}
	applyFrame(grid, buf.String(), W)
	buf.Reset()

	mm, _ := m.Update(tab)
	m = mm.(Terminal)
	v2 := m.View()
	if err := s.Render(v2.Content, v2.Cursor, true); err != nil {
		t.Fatal(err)
	}
	applyFrame(grid, buf.String(), W)

	found := false
	for r := 0; r < 20; r++ {
		if strings.Contains(string(grid[r]), "Model Selector") {
			found = true
			break
		}
	}
	if !found {
		t.Error("overlay box vanished after Tab on a narrow terminal")
	}
}
