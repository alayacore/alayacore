module github.com/alayacore/alayacore

go 1.26.1

require (
	charm.land/bubbletea/v2 v2.0.8
	charm.land/lipgloss/v2 v2.0.5
	github.com/charmbracelet/x/ansi v0.11.7
	github.com/rivo/uniseg v0.4.7
	golang.org/x/net v0.57.0
	golang.org/x/sys v0.47.0
	golang.org/x/term v0.45.0
	gopkg.in/yaml.v3 v3.0.1
)

// Fork of bubbletea v2.0.8 with a raw passthrough renderer mode
// (tea.View.Raw): the view content is written verbatim to the terminal so
// it soft-wraps natively — the stock cell-buffer renderer truncates lines
// wider than the screen and re-materializes wrapped rows as hard rows,
// which breaks soft-wrap display and copy fidelity (REFACTOR.md).
replace charm.land/bubbletea/v2 => ./third_party/bubbletea

require (
	github.com/charmbracelet/colorprofile v0.4.3 // indirect
	github.com/charmbracelet/ultraviolet v0.0.0-20260808192814-d38ea0f8aa5c // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.1 // indirect
	github.com/mattn/go-runewidth v0.0.27 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	golang.org/x/exp v0.0.0-20260218203240-3dfff04db8fa // indirect
	golang.org/x/sync v0.22.0 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
)
