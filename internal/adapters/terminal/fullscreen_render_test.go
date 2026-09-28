package terminal

// Regression tests for the flicker-free (overlay) render path: they lock
// the premise that a normal Terminal.View soft-wraps to EXACTLY the screen
// height — full-width padded rows cover any previous frame, so the renderer
// can skip ED2 (see Screen.Render).

import (
	"fmt"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/tlv"
)

// TestViewAlwaysFillsScreen verifies the normal Terminal.View content
// soft-wraps to exactly the screen height at various sizes, with and
// without window content, with and without a typed input.
//
// The row count is measured with hardwrapCells, so it is not the same call the
// layout makes — a check that measured with whatever the renderer used could
// not fail (see width.go's header, point 3).
func TestViewAlwaysFillsScreen(t *testing.T) {
	cases := []struct {
		name    string
		width   int
		height  int
		windows [][2]string // tag, content — fed through the output writer
		type_   string
	}{
		{"small empty", 40, 10, nil, ""},
		{"default empty", 80, 24, nil, ""},
		{"large with windows", 100, 40, [][2]string{
			{tlv.TagAssistantT, strings.Repeat("line of text here\n", 30)},
		}, ""},
		{"with typed input", 100, 24, [][2]string{
			{tlv.TagAssistantT, strings.Repeat("line of text here\n", 30)},
		}, "the screen looks weird"},
		// Keycaps: the cluster whose width the library wrapper got wrong
		// (width.go's header, point 3).
		{"keycap list", 40, 20, [][2]string{
			{tlv.TagAssistantT, "步骤如下：\n1️⃣ 第一步：安装依赖并确认环境变量\n2️⃣ 第二步：运行测试\n3️⃣ 第三步：部署上线\n"},
			{tlv.TagAssistantT, strings.Repeat("9️⃣", 30)},
		}, ""},
		{"keycap run on a narrow pane", 24, 16, [][2]string{
			{tlv.TagAssistantT, strings.Repeat("9️⃣", 30)},
			{tlv.TagUserT, strings.Repeat("1️⃣2️⃣3️⃣", 20)},
		}, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The content goes in through the output writer, the way a
			// session's frames arrive, and before the size does: writing
			// straight to the window buffer leaves the display's cached
			// content untouched, which is how this check came to be run
			// against an empty frame while claiming to cover "with windows".
			out := NewTerminalOutput(DefaultStyles())
			for i, w := range tc.windows {
				out.Write(tlvFrame(w[0], fmt.Sprintf("w%d", i), w[1]))
			}
			out.FlushPendingDeltas()

			m := NewTerminalWithTheme(out, nopWriteCloser{}, &app.Config{}, tc.width, tc.height, theme.DefaultTheme(), nil, "theme-dark")
			mm, _ := m.Update(WindowSizeMsg{Width: tc.width, Height: tc.height})
			m = mm.(Terminal)
			for _, r := range tc.type_ {
				mm, _ := m.Update(KeyPressMsg(Key{Code: r}))
				m = mm.(Terminal)
			}
			v := m.View()
			if !v.FullScreen {
				t.Error("normal view must be marked FullScreen")
			}
			if len(tc.windows) > 0 && !strings.Contains(stripANSI(v.Content), "ASSISTANT") {
				t.Fatal("the window content never reached the frame, so the row count proves nothing")
			}
			rows := strings.Count(hardwrapCells(stripANSI(v.Content), tc.width), "\n") + 1
			if rows != tc.height {
				t.Errorf("soft-wrap rows = %d, want screen height %d", rows, tc.height)
			}
			// The same fact from the row map the diff renderer uses: the
			// base region ends exactly where the first CUP-anchored row
			// (the live edge) begins.
			baseEnd, firstCUP := frameBaseRegion(v.Content, tc.width)
			if firstCUP >= 0 && baseEnd != firstCUP {
				t.Errorf("the base region occupies %d terminal rows for the %d the layout reserved (it ends on row %d, the first CUP row is %d): %d row(s) of transcript spill onto the live edge",
					baseEnd, firstCUP, baseEnd-1, firstCUP, baseEnd-firstCUP)
			}
		})
	}
}

// frameBaseRegion returns the terminal row the frame's base rows run to
// (exclusive) and the row its first CUP-anchored row sits on, from the same
// row map the diff renderer builds.
func frameBaseRegion(content string, width int) (baseEnd, firstCUP int) {
	firstCUP = -1
	for _, r := range positionedRows(content, width) {
		if r.base {
			baseEnd = max(baseEnd, r.frameRow.row+r.terminalRows)
			continue
		}
		if firstCUP < 0 {
			firstCUP = r.frameRow.row
		}
	}
	return baseEnd, firstCUP
}

// TestViewLoadingFullScreen verifies the loading view IS marked full-screen
// (padded with erased blank rows), so Screen.Render takes the row-diff path
// instead of ED2-clear — only the spinner row is repainted when the spinner
// advances, eliminating the flicker that ED2 would otherwise produce on every
// tick. The diff parser relies on the view soft-wrapping to exactly the
// screen height, so the test also locks the row count.
func TestViewLoadingFullScreen(t *testing.T) {
	m := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, &app.Config{}, 80, 24, theme.DefaultTheme(), nil, "theme-dark")
	m.loading = true
	v := m.View()
	if !v.FullScreen {
		t.Error("loading view must be marked FullScreen so Screen.Render uses the row-diff path")
	}
	// Must soft-wrap to exactly the screen height (no trailing empty
	// rows that the diff parser would not cover). Measured with the same
	// primitive the layout uses — see TestViewAlwaysFillsScreen for why a
	// second width table here would make the check unable to fail.
	rows := strings.Count(hardwrapCells(stripANSI(v.Content), 80), "\n") + 1
	if rows != 24 {
		t.Errorf("soft-wrap rows = %d, want screen height 24", rows)
	}
}
