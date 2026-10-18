package terminal

import "github.com/alayacore/alayacore/internal/theme"

// nopWriteCloser is a WriteCloser that discards all data.
// Used in tests that need an io.WriteCloser but don't read from it.
type nopWriteCloser struct{}

func (nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopWriteCloser) Close() error                { return nil }

// testTUI is the Terminal most tests draw with: an 80×24 screen, a fresh output
// writer, an input stream that goes nowhere, no session config, and the built-in
// theme. A test that needs another stream, size or theme builds Options itself —
// naming the fields it means is what the struct is for; these two exist because
// the rest would write the same six fields to say the same thing.
func testTUI() Terminal { return testTUISized(80) }

// testTUIWide is testTUI on a 120-column screen: markdown tables are the one
// reason a test needs the room.
func testTUIWide() Terminal { return testTUISized(120) }

func testTUISized(width int) Terminal {
	return NewTerminal(Options{
		Out:       NewTerminalOutput(DefaultStyles()),
		Input:     nopWriteCloser{},
		Width:     width,
		Height:    24,
		Theme:     theme.DefaultTheme(),
		ThemeName: defaultThemeName,
	})
}
