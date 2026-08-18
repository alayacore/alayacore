package terminal

// Regression tests for the two overlay box bugs found after the row-diff
// renderer landed:
//
//  1. FilteredListCore.WithSize overrode the component's FIXED box width
//     (60/72) with the full terminal width — the overlay box spanned the
//     whole screen from column 1, and its rows shared row coordinates
//     with the base rows.
//  2. The row-diff renderer then treated those colliding base rows as
//     "changed" and rewrote them over the overlay on Tab — the overlay
//     content vanished, showing the windows behind.
//
// The fixes: WithSize never touches the box width (only the list height
// adapts), and diffFrameRows distinguishes base rows from CUP overlay
// rows and never rewrites a base row that an overlay row covers.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/tlv"
)

// TestOverlayBoxWidthNotOverriddenByWithSize locks the fixed box width:
// a terminal resize must not change the overlay box width (60 for the
// selectors/attachment, 72 for help).
func TestOverlayBoxWidthNotOverriddenByWithSize(t *testing.T) {
	styles := DefaultStyles()

	ms := NewModelSelector(styles).WithSize(137, 50)
	if ms.Width != 60 {
		t.Errorf("model selector width = %d, want fixed 60", ms.Width)
	}
	ts := NewThemeSelector(styles).WithSize(137, 50)
	if ts.Width != 60 {
		t.Errorf("theme selector width = %d, want fixed 60", ts.Width)
	}
	hw := NewHelpWindow(styles).WithSize(137, 50)
	if hw.Width != 72 {
		t.Errorf("help window width = %d, want fixed 72", hw.Width)
	}
	aw := NewAttachmentWindow(styles).WithSize(137, 50)
	if aw.Width != 60 {
		t.Errorf("attachment window width = %d, want fixed 60", aw.Width)
	}
	// The box must stay centered: at width 137 a 60-col box starts at col 38.
	m := newTestTerminal()
	m.modelSelector = m.modelSelector.Open().WithSize(137, 50)
	box := m.modelSelector.View().Content
	if x, _ := overlayOrigin(box, 137, 50); x != 38 {
		t.Errorf("overlay origin x = %d, want 38 (centered 60-col box)", x)
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
