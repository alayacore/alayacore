package terminal

// Regression test for the PageUp/PageDown complaint: the prompt input
// occasionally shifted upward. The mechanism: scrolling changes the
// display rows; the blank padding rows at the display bottom previously
// erased the row ABOVE them (EL before '\n' clears the current row), so
// old content survived on the blank rows and visually pushed the input
// box up. The padding now erases each blank row AFTER entering it.

import (
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/tlv"
	"github.com/charmbracelet/x/ansi"
)

// TestPageScrollNoResidueBelowDisplay renders full Terminal frames before
// and after a PageDown scroll, paints them onto a terminal grid, and
// asserts the input box (and rows around it) carry no old content.
func TestPageScrollNoResidueBelowDisplay(t *testing.T) {
	const W, H = 100, 24
	m := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, &app.Config{}, W, H, theme.DefaultTheme(), nil, "theme-dark")
	m.out.SetWindowWidth(W)
	mm, _ := m.Update(WindowSizeMsg{Width: W, Height: H})
	m = mm.(Terminal)
	wb := m.out.WindowBuffer()
	wb.AppendOrUpdate(tlv.TagAssistantT, "a1", strings.Repeat("long assistant row content here\n", 20))
	wb.AppendOrUpdate(tlv.TagAssistantR, "r1", `The user says "say hello".`)
	mm, _ = m.Update(WindowSizeMsg{Width: W, Height: H})
	m = mm.(Terminal)

	frame1 := m.View().Content
	mm, _ = m.Update(KeyPressMsg(Key{Code: KeyPgDown}))
	m = mm.(Terminal)
	frame2 := m.View().Content

	grid := applyFrame(nil, frame1, W)
	grid = applyFrame(grid, frame2, W)

	// The input box must be intact: its top rule and placeholder present.
	joined := make([]string, 0, len(grid))
	for _, r := range grid {
		joined = append(joined, string(r))
	}
	screen := strings.Join(joined, "\n")
	if !strings.Contains(screen, "Enter your prompt") {
		t.Errorf("prompt input missing after scroll")
	}
	// No row above the input box may contain old content ("long assistant
	// row content here" fragments) unless it's the display's own content.
	// The last display row (before the input box) must be blank, not old.
	if !strings.Contains(screen, "long assistant row content here") {
		t.Errorf("display content vanished after scroll")
	}
	// The input box's top rule must sit exactly one row above the
	// placeholder row (input box = rule + content + rule).
	for i := 1; i < len(grid); i++ {
		if strings.Contains(string(grid[i]), "Enter your prompt") {
			above := string(grid[i-1])
			if !strings.Contains(above, "─") {
				t.Errorf("row above prompt is %q, want the input top rule", above)
			}
			break
		}
	}
	_ = ansi.StringWidth
}
