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

// Fold-state arrows. The collapsed arrow points right (the window can be
// opened), the expanded arrow points down (it is showing its content).
//
// These belong to the terminal layout, not to the theme: the header is
// laid out as "arrow + space + label column" with exactly one cell
// reserved for the arrow, so the glyph is a geometry decision, and a
// palette switch must never change it.
//
// ▸/▾ (U+25B8/U+25BE) and not the heavier ▶/▼ (U+25B6/U+25BC), for
// terminal reasons rather than taste: U+25B6/U+25BC are East Asian Width
// "ambiguous" (double-width in terminals configured that way) and
// Emoji_Presentation=Yes (Windows Terminal, GNOME Console and any font
// stack with an emoji fallback paint them as a two-cell color emoji).
// Both cases out-render the one cell the layout reserves and shift every
// window header. U+25B8/U+25BE are narrow, default to text presentation,
// and are present in essentially every monospace font. Tests pin the
// codepoints and the cell width (arrows_test.go).
const (
	foldArrow   = "▸"
	unfoldArrow = "▾"

	// arrowCellWidth is the display width of both arrows. It is a
	// constant because the arrows are; arrows_test.go asserts the glyphs
	// really measure this wide.
	arrowCellWidth = 1

	// collapsedPrefixWidth is what a header line spends before the label
	// column: the arrow plus one separating space. Content is measured
	// against the remaining width, and contentColumn tests pin the label
	// column to start at exactly this offset.
	collapsedPrefixWidth = arrowCellWidth + 1
)

// CollapsedLabelWidth is the width of the label column in collapsed window
// header lines ("▸ LABEL content…"), so content starts at the same column
// for every window type (USER PROMPT, REASONING, ASSISTANT, SYSTEM NOTIFY,
// SYSTEM ERROR, TOOL). The widest label is "SYSTEM NOTIFY" (13 columns); the column is 16
// to keep a separating space before content and leave headroom for longer
// labels (e.g. "SYSTEM ERROR", "TOOL CALL") without re-tuning.
const CollapsedLabelWidth = 16
