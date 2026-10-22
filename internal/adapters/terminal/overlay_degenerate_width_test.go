package terminal

// Overlays must render at any width a terminal can report — including 0.
//
// A terminal with no usable size is not hypothetical: Screen.Size returns
// (0, 0) when TIOCGWINSZ fails or reports a winsize nobody set, which is how a
// bare serial console comes up, and the adapter's 80×20 default is not what the
// frame is then laid out at (Program.Run takes the size from the tty). The
// overlays are opened by the *session* (a tool confirmation, an MCP server
// starting, a quit that is waiting), not by the user, so View() has to survive
// that size rather than assume a user typed at it.
//
// The dialog title line used to end this: at inner width 0 the trailing
// strings.Repeat went negative and panicked with "strings: negative Repeat
// count", and View panics were outside the loop's recover at the time — so the
// process died with a Go stack. Both halves are fixed (confirm_dialog.go,
// program.go → handleMsg); what this test states is the general property rather
// than the one input that was found: at every width the UI can report,
// rendering is total, and — where there is a width to respect — no
// CUP-anchored row asks for more cells than the row holds.

import (
	"fmt"
	"testing"

	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/theme"
)

// openOverlay opens one overlay by name on a terminal of the given size.
func openOverlay(t *testing.T, name string, w, h int) Terminal {
	t.Helper()
	out := NewTerminalOutput(DefaultStyles())
	m := NewTerminal(Options{
		Out: out, Input: nopWriteCloser{}, AppConfig: &app.Config{},
		Width: w, Height: h,
		Theme: theme.DefaultTheme(), ThemeName: "theme-dark",
	})
	mm, _ := m.Update(ScreenSizeMsg{Width: w, Height: h})
	m = mm.(Terminal)
	m = m.updateComponentSizes(w, h)

	switch name {
	case "help":
		m.helpOverlay = m.helpOverlay.Open()
	case "model":
		m.modelSelector = m.modelSelector.LoadModels(nil, 0).Open()
	case "theme":
		m.themeSelector = m.themeSelector.Open([]ThemeEntry{{Name: "dark"}}, "dark")
	case "attachment":
		m.attachmentOverlay = m.attachmentOverlay.Open()
	case "confirm_tool":
		m.confirmOverlay = m.confirmOverlay.OpenTool("1", "execute_command", `{"command":"ls -la"}`)
	case "confirm_quit":
		m.confirmOverlay = m.confirmOverlay.OpenQuit()
	case "confirm_mcp_auth":
		m.confirmOverlay = m.confirmOverlay.OpenMCPAuth("server", "https://example.test/auth")
	case "confirm_mcp_init":
		m.mcpInitOverlay = m.mcpInitOverlay.OpenMCPInit()
	case "confirm_quit_waiting":
		m.confirmOverlay = m.confirmOverlay.OpenQuitWaiting()
	case "confirm_quit_canceling":
		m.confirmOverlay = m.confirmOverlay.OpenQuitCanceling()
	default:
		t.Fatalf("unknown overlay %q", name)
	}
	return m
}

// TestOverlaysRenderAtAnyWidth drives every overlay at widths a terminal can
// report, from none at all up to a narrow pane.
func TestOverlaysRenderAtAnyWidth(t *testing.T) {
	overlays := []string{
		"help", "model", "theme", "attachment",
		"confirm_tool", "confirm_quit", "confirm_mcp_auth",
		"confirm_mcp_init", "confirm_quit_waiting", "confirm_quit_canceling",
	}

	const h = 24
	for _, name := range overlays {
		for w := 0; w <= 6; w++ {
			t.Run(fmt.Sprintf("%s/%d", name, w), func(t *testing.T) {
				m := openOverlay(t, name, w, h)

				// The whole frame, not just the overlay: the base content sits
				// under it and the status bar is CUP-anchored to the last row.
				v := m.View()
				if w == 0 {
					// Nothing is measurable against a width of zero, and the
					// components below the dialog do not try: the description
					// and hint rows are wrapped to a width that does not
					// exist, so they come out as whole sentences in a
					// zero-width box (verified — this is the state, not an
					// assumption). The claim here is only that rendering is
					// *total*: a session-opened dialog at a size the terminal
					// reported but cannot support must not take the process
					// down. Everything above this line stays unmeasurable
					// until the size arrives.
					return
				}
				// The rows that must fit are the CUP-anchored ones: chrome and
				// overlay layers own their terminal row(s) from a fixed
				// column, so a cell past the last one wraps — and a wrap on
				// the bottom row scrolls the screen. Base rows are excluded:
				// they are soft-wrap fragments, padded to the width, so
				// their measured width is not the screen width by design.
				//
				// A layer may legitimately span more than one terminal row:
				// the dialog's description run is emitted as one continuous
				// write and the host soft-wraps it (terminalRows > 1). What
				// it may not do is need more cells than those rows hold.
				for _, r := range positionedRows(v.Content, w) {
					if r.frameRow.base {
						continue
					}
					rows := max(1, r.terminalRows)
					avail := (w - r.frameRow.col) + (rows-1)*w
					if cw := cellWidth(stripANSI(r.frameRow.text)); cw > avail {
						t.Errorf("row %d anchored at column %d is %d cells, %d available over %d terminal row(s) of %d columns: %q",
							r.frameRow.row+1, r.frameRow.col+1, cw, avail, rows, w,
							truncateWithSuffix(stripANSI(r.frameRow.text), 40))
					}
					if bottom := r.frameRow.row + r.terminalRows; bottom > h {
						t.Errorf("row %d spans to %d, past the last row %d", r.frameRow.row+1, bottom, h)
					}
				}
			})
		}
	}
}

// TestConfirmDialogTitleAtZeroWidth pins the specific input that used to panic,
// so the general sweep above cannot be weakened without this failing too.
func TestConfirmDialogTitleAtZeroWidth(t *testing.T) {
	cd := NewConfirmDialog(DefaultStyles()).WithSize(0, 24)
	cd = cd.OpenTool("1", "execute_command", `{"command":"ls"}`)

	if got := cd.renderTitleLine("Confirm tool call", 0); got != "" {
		t.Errorf("renderTitleLine at inner width 0 = %q, want the empty row", got)
	}
	if got := cd.View().Content; got == "" {
		t.Error("View at width 0 should still produce a frame (an empty box), not nothing")
	}
}
