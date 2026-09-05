package terminal

import "time"

// Separator is the visual divider between blocks inside a window: tool
// arguments and tool result, media block and user text, one user text part
// and the next.
//
// It is three cells of the same box-drawing rule the window frames are made
// of (see Styles.RenderOpenBoxLines), deliberately not the ASCII "---".
// "---" is a meaningful token everywhere else in this product — the session
// frontmatter, the model.conf and mcp.conf block delimiters, the SKILL.md
// frontmatter, a markdown horizontal rule, and the file header of a unified
// diff (`--- a/file`), which the edit_file window shows right above this
// very divider. A chrome line must not be spellable as content.
//
// The width cost is nil in the policy's terms: every window already spends
// two full-width Ambiguous rules on its frame (the box-drawing waiver,
// constants.go), so three more cells of the same waived class change nothing
// that the frame has not already committed to.
const Separator = "───"

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
// ▸/▾ (U+25B8/U+25BE) and not the heavier ▶/▼ (U+25B6/U+25BC), because of
// East Asian Width: U+25B6/U+25BC are "A" (ambiguous), so a terminal
// configured for double-width ambiguous characters draws them two cells;
// U+25B8/U+25BE are "N" (narrow) — one cell, always. ▶ carries a second
// hazard on top: it is in Extended_Pictographic (Emoji=Yes), so a terminal
// whose font resolution reaches an emoji font can hand back a two-cell
// color glyph even though its default presentation is text
// (Emoji_Presentation=No — it needs U+FE0F to be emoji by default). ▼ is
// not pictographic at all. An earlier revision of this comment claimed
// Emoji_Presentation=Yes for both, which is why the pair is now justified
// on width alone with the emoji hazard noted only where it exists.
//
// Either hazard out-renders the single cell the layout reserves, and
// neither is visible to us at runtime — with the default options both
// width libraries report one cell for all four codepoints — so only the
// choice of glyph can prevent it. Tests pin the codepoints and the cell
// width (arrows_test.go, glyphs_test.go).
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

// statusDot is the one state marker the status bar draws, at the very
// first cell of a row that is truncated to exactly the terminal width.
//
// It is a single East-Asian Neutral glyph for BOTH states: the old pair
// was "·" U+00B7 (idle) and "•" U+2022 (running), both Ambiguous, so in a
// double-width-ambiguous terminal the row started one cell too late and
// its last segment wrapped onto a second row — and the shift appeared
// only while a task ran, which is the worst time to discover it.
//
// The state now reads from color (dim idle / accent running, as before)
// and weight (bold while running, so a bar dimmed by an overlay still
// distinguishes running from idle). See renderStatusBar. U+2219 BULLET
// OPERATOR keeps the dot language of the design and, of every
// Neutral-width dot we measured, the best font coverage.
const statusDot = "∙"

// ============================================================================
// Glyph policy
// ============================================================================
//
// Everything the adapter draws is measured by one of TWO width models:
// ansi.StringWidth (via displaywidth) sizes rows, and uniseg slices
// grapheme clusters while truncating. A terminal is free to disagree with
// both, in one specific way: a character whose East_Asian_Width is "A"
// (Ambiguous) is drawn one cell wide by default and TWO cells wide by a
// terminal configured for CJK (xterm -cjkwidth, mlterm in a CJK locale,
// some font configurations). Nothing at runtime reveals it — both
// libraries answer 1 for an ambiguous glyph — so the only defense is the
// choice of codepoint.
//
//  1. A glyph the layout gives exactly one cell must be East-Asian Neutral
//     and outside Extended_Pictographic. This is what pins ▸/▾ above, "∙"
//     in statusDot, the "⠋…⠏"/"✓"/"✗" tool indicators, and the ASCII "|"
//     the help bars use between key hints — a help bar is truncated and
//     padded to exactly the box width (renderHelpBar), so one doubled cell
//     there overflows the row.
//  2. Where a whole class is Ambiguous and no Neutral member carries the
//     meaning, rule 1 is waived for the class and recorded as a limitation
//     instead of being worked around glyph by glyph. Two classes need the
//     waiver, and glyphs_test.go keeps the list honest:
//      - Box drawing (U+2500-U+257F) is Ambiguous through and through —
//        measured, no exception. The rules ("─") and the markdown table
//        grid take the waiver: one rule spans the whole window width, so a
//        doubling breaks the frame whichever glyph is picked, and a
//        per-glyph hunt buys nothing. The alternative is an ASCII
//        ("+---") glyph set, which is a product decision, not a fix. The
//        in-content divider (Separator, "───") joins this family on
//        purpose — one job and one glyph for every line the app draws, and
//        a chrome line that must never be spellable as content ("---" is a
//        markdown rule, a unified-diff file header, and the frontmatter and
//        config-block delimiter of this product's own file formats).
//      - Typographic marks with no same-meaning Neutral equivalent: the
//        ellipsis "…" (U+2026 — "⋯" U+22EF is Neutral but mid-line, thin,
//        and poorly covered), "—" U+2014, "∞" U+221E, "↓" U+2193. Each one
//        is a row that can shift by a cell; none is a frame that can
//        shatter. (The speed segment's "·" used to be on this list; the
//        segment now reads "12.5 tok/s (ttft 1.2s)" instead, so the row is
//        Ambiguous-free apart from the markers above.)
//     Anything else Ambiguous is a bug — that is the test, not the prose,
//     that says so.
//  3. Program-owned symbols are single codepoints. Multi-codepoint
//     sequences are not banned because a library mis-measures them — both
//     report 2 cells for a camera emoji followed by U+FE0F, and for a ZWJ
//     family — but because the two models disagree on ~3100 codepoints
//     (U+2713 followed by U+FE0F measures 2 to displaywidth and 1 to
//     uniseg) and a single string passes through both. Never put a glyph
//     whose width depends on a variation selector or ZWJ where a one-cell
//     mismatch is a layout shift.
//  4. Color and weight carry state before a glyph does. A marker that never
//     changes is decoration: the "✦" that used to sit after the reasoning
//     level (shown as "R0✦" even at level 0, pinned to never be
//     highlighted) carried no information and looked like an indicator.
//
// glyphs_test.go scans this package's own source, extracts every
// non-ASCII character it draws, and fails on an unclassified glyph, on a
// stale entry, and on a glyph whose measured width contradicts its class.

// CollapsedLabelWidth is the width of the label column in collapsed window
// header lines ("▸ LABEL content…"), so content starts at the same column
// for every window type (USER PROMPT, REASONING, ASSISTANT, SYSTEM NOTIFY,
// SYSTEM ERROR, TOOL). The widest label is "SYSTEM NOTIFY" (13 columns); the column is 16
// to keep a separating space before content and leave headroom for longer
// labels (e.g. "SYSTEM ERROR", "TOOL CALL") without re-tuning.
const CollapsedLabelWidth = 16
