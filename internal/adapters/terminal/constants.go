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
// for every window type (USER PROMPT, REASONING, ASSISTANT, SYSTEM NOTIFY,
// SYSTEM ERROR, TOOL). The widest label is "SYSTEM NOTIFY" (13 columns); the column is 16
// to keep a separating space before content and leave headroom for longer
// labels (e.g. "SYSTEM ERROR", "TOOL CALL") without re-tuning.
const CollapsedLabelWidth = 16
