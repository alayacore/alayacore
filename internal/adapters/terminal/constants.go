package terminal

import "time"

// Separator is the visual separator between sections in a window.
const Separator = "---"

// Timing constants for UI responsiveness.
const (
	// ThemePreviewDebounce is the delay before applying a theme preview
	// after a navigation key press. This keeps cursor movement responsive
	// while preventing flicker from rapid navigation.
	ThemePreviewDebounce = 150 * time.Millisecond
)

// Tab width expansion (standard terminal convention).
const (
	TabWidth = 8
)

// Window tag constants for internal window types in the terminal adapter.
// These are NOT TLV protocol tags (those are defined in internal/tlv/tlv.go).
const (
	TagWindowSE = "SE"
	TagWindowSN = "SN"
)

// CollapsedLabelWidth is the width of the label column in collapsed window
// header lines ("▶ LABEL content…"), so content starts at the same column
// for every window type (USER PROMPT, REASONING, ASSISTANT, NOTIFY, ERROR,
// TOOL). The widest label is "USER PROMPT" (11 columns) — the column is 12
// so it keeps a separating space before the content, like every other label.
const CollapsedLabelWidth = 12
