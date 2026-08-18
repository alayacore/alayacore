package terminal

// Regression tests for the flicker-free (overlay) render path: they lock
// the premise that a normal Terminal.View soft-wraps to EXACTLY the screen
// height — full-width padded rows cover any previous frame, so the renderer
// can skip ED2 (see Screen.Render).

import (
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/tlv"
	"github.com/charmbracelet/x/ansi"
)

// TestViewAlwaysFillsScreen verifies the normal Terminal.View content
// soft-wraps to exactly the screen height at various sizes, with and
// without window content, with and without a typed input.
func TestViewAlwaysFillsScreen(t *testing.T) {
	cases := []struct {
		name   string
		width  int
		height int
		withUT bool
		type_  string
	}{
		{"small empty", 40, 10, false, ""},
		{"default empty", 80, 24, false, ""},
		{"large with windows", 100, 40, true, ""},
		{"with typed input", 100, 24, true, "the screen looks weird"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, &app.Config{}, tc.width, tc.height, theme.DefaultTheme(), nil, "theme-dark")
			mm, _ := m.Update(WindowSizeMsg{Width: tc.width, Height: tc.height})
			m = mm.(Terminal)
			if tc.withUT {
				wb := m.out.WindowBuffer()
				wb.AppendOrUpdate(tlv.TagAssistantT, "w1", strings.Repeat("line of text here\n", 30))
			}
			for _, r := range tc.type_ {
				mm, _ := m.Update(KeyPressMsg(Key{Code: r}))
				m = mm.(Terminal)
			}
			v := m.View()
			if !v.FullScreen {
				t.Error("normal view must be marked FullScreen")
			}
			rows := strings.Count(ansi.Hardwrap(stripANSI(v.Content), tc.width, true), "\n") + 1
			if rows != tc.height {
				t.Errorf("soft-wrap rows = %d, want screen height %d", rows, tc.height)
			}
		})
	}
}

// TestViewLoadingNotFullScreen verifies the loading view is NOT full-screen,
// so the renderer keeps the clearing (ED2) path for it.
func TestViewLoadingNotFullScreen(t *testing.T) {
	m := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, &app.Config{}, 80, 24, theme.DefaultTheme(), nil, "theme-dark")
	m.loading = true
	v := m.View()
	if v.FullScreen {
		t.Error("loading view must not be marked FullScreen (it does not fill the screen)")
	}
}
