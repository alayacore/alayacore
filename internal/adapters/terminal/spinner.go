package terminal

import "time"

// The spinner: the one animated glyph in the frame, drawn by every surface
// that reports a running task — the tool header (ToolStatus.statusDot), the
// session-loading screen (renderLoadingView), and the status bar's indicator
// column (renderStatusBar). One table and one wall-clock frame function, so
// the three cannot drift apart.
//
// It is the braille dot-segment rotation. The frames are East-Asian Neutral
// and one cell wide (glyphs_test.go pins them), so the cell they occupy is
// one cell in every terminal — the same guarantee the status row's truncation
// arithmetic and the tool label column rely on.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinnerFrameMS is how long each frame is held. The frame advances with the
// wall clock rather than a per-surface timer: any re-render — the 250ms tick,
// a content arrival, an overlay open — picks up the current frame, so a
// surface only has to be repainted to spin.
const spinnerFrameMS = 150

// spinnerFrameAt returns the spinner frame for the given moment. Extracted
// from spinnerFrame so tests can inject a fixed moment.
func spinnerFrameAt(t time.Time) string {
	return spinnerFrames[int(t.UnixMilli()/spinnerFrameMS)%len(spinnerFrames)]
}

// spinnerFrame returns the spinner frame for the current moment.
func spinnerFrame() string {
	return spinnerFrameAt(time.Now())
}
