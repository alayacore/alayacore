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
// a full-width Ambiguous rule on opening itself (the box-drawing waiver,
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

// The fold markers. A folded window is one line starting with "+" — there
// is more to it; an expanded window's own line starts with "-" — its
// content follows. Both are ASCII, and that is the point: a marker that
// sits at column 0 of every window row must measure one cell in EVERY
// terminal and must not depend on the reader's font covering a symbol
// block. Non-ASCII can promise neither — the width tables say what the
// terminal *should* do, and nothing can tell us what the font *has*.
//
// The pair replaced the triangles ▸/▾ (U+25B8/U+25BE). Those were picked
// for width (East-Asian Neutral, outside Extended_Pictographic — the
// waiver discussion in the glyph policy below) and the reasoning held; but
// "a glyph that is probably one cell and probably in the font" is a worse
// foundation for a marker that appears on every row than "a byte that is
// defined to be one cell in every terminal that has ever existed".
//
// The markers belong to the terminal layout, not to the theme: the
// collapsed line is laid out as "marker + space + label column", so the
// glyph owns a cell of that geometry, and a palette switch must never
// change it.
const (
	foldArrow   = "+"
	unfoldArrow = "-"

	// arrowCellWidth is the display width of both markers. It is a
	// constant because the glyphs are (ASCII — the one class whose width
	// no table can disagree about); arrows_test.go asserts it.
	arrowCellWidth = 1

	// collapsedPrefixWidth is what a collapsed line spends before the
	// label column: the marker plus one separating space. Content is
	// measured against the remaining width, and contentColumn tests pin
	// the label column to start at exactly this offset.
	collapsedPrefixWidth = arrowCellWidth + 1
)

// timeStampLayout is the wall clock a window's own line carries on the
// right, and timeStampWidth is its exact cell width.
//
// The column is right-aligned to the window edge, so a format whose width
// drifted would move every timestamp on screen and misalign the whole
// transcript: the layout is pinned, and a test asserts that a formatted
// timestamp measures exactly timeStampWidth cells — every zone, every month,
// every day. The shape is the one this UI already used, plus seconds and the
// UTC offset: "2026/09/14 18:30:07 +08:00".
//
// Local time with its offset, not UTC: this is the reader's own receipt clock
// (when the window appeared in this view), not a record field. Session
// records carry no per-message time — the frontmatter's created_at and
// updated_at are session-level, written once, in whatever zone the session
// was created in, and are nothing a per-window clock can borrow.
//
// Seconds, where the layout used to stop at the minute: a minute is shorter
// than the gaps this transcript is read for (a command that ran 40 seconds
// and the answer that follows it land in the same minute), while everything
// one delta flush delivers still shares one second — 400 windows are created,
// stamped and rendered in about a millisecond — so the finer resolution
// separates what a reader is looking at without fragmenting what arrived
// together. It costs 3 cells of every row.
//
// The offset is the other half of saying "local": six cells that name the
// zone the clock is in. They are the same six cells on every row of a
// session, because the zone is the process's — constant chrome, spent on
// making a column that appears nowhere else in the frame self-describing. (A
// zone name would be three cells, "CST", and mean three different zones.)
// This is not RFC 3339, and a strict parser will reject it: a
// machine-readable column, if one is ever wanted, is a separate decision.
//
// The width is not negotiable in the other direction either: a timestamp is
// drawn only when the label leaves room for all of it beside itself (see
// buildExpandHeader), never truncated and never shortened to fit.
const (
	timeStampLayout = "2006/01/02 15:04:05 -07:00"
	timeStampWidth  = 26
)

// statusDotGlyph is the one state marker the status bar draws, at the very
// first cell of a row that is truncated to exactly the terminal width. The
// tool header has its own marker — the (ToolStatus) statusDot method in
// tool_render.go — and the two names are deliberately separate: similar
// marks, one per row, never in the same line.
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
const statusDotGlyph = "∙"

// ============================================================================
// Glyph policy
// ============================================================================
//
// Cell arithmetic has one home: width.go measures a string and cuts it from
// the same table (displaywidth's, its options constructed there — see the
// reasons in that file's header). What one table cannot know is what the
// terminal does with a character whose East_Asian_Width is "A" (Ambiguous):
// it is drawn one cell wide by default and TWO cells wide by a terminal
// configured for CJK (xterm -cjkwidth, mlterm's setting, some font
// configurations). That configuration is deliberate and rare — every
// mainstream terminal defaults to one cell — and nothing reveals it at
// runtime, so the only defense is the choice of codepoint.
//
//  1. A glyph the layout gives exactly one cell must be East-Asian Neutral
//     and outside Extended_Pictographic, or ASCII. This is what pins "∙"
//     in statusDotGlyph, the "⠋…⠏"/"✓"/"✗" tool indicators, and the ASCII "|"
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
//        and poorly covered), "—" U+2014, "∞" U+221E. Each one
//        is a row that can shift by a cell; none is a frame that can
//        shatter. (The speed segment's "·" used to be on this list; the
//        segment now reads "12.5 tok/s (ttft 1.2s)" instead, so the row is
//        Ambiguous-free apart from the markers above. "↓" U+2193 has left
//        the list: the auto-follow marker it stood in is now the word
//        "following" on the live-edge row (live_edge.go), and the only
//        non-ASCII glyph that row draws is the box-drawing rule every
//        frame in this UI already pays for.)
//     Anything else Ambiguous is a bug — that is the test, not the prose,
//     that says so.
//  3. Program-owned symbols are single codepoints. The reason used to be
//     internal — the adapter measured a row with one library and cut it
//     with another, and no glyph survived that pair untested — and it is
//     now the terminal's: a glyph followed by U+FE0F asks for emoji
//     presentation, which some terminals honor and some draw one cell wide
//     while our table answers two, and a ZWJ family is one cluster on one
//     host and several on the next. A single codepoint outside
//     Extended_Pictographic has no second opinion to disagree with, so it
//     cannot move a layout by a cell.
//  4. Color and weight carry state before a glyph does. A marker that never
//     changes is decoration: the "✦" that used to sit after the reasoning
//     level (shown as "R0✦" even at level 0, pinned to never be
//     highlighted) carried no information and looked like an indicator.
//
// glyphs_test.go scans this package's own source, extracts every
// non-ASCII character it draws, and fails on an unclassified glyph, on a
// stale entry, and on a glyph whose measured width contradicts its class.

// CollapsedLabelWidth is the width of the label column in a window's own
// line ("+ LABEL content…" folded, "- LABEL … timestamp" expanded), so
// content starts at the same column for every window type (USER PROMPT,
// REASONING, ASSISTANT, SYSTEM NOTIFY, SYSTEM ERROR, TOOL). The widest
// label is "SYSTEM NOTIFY" (13 columns); the column is 16 to keep a
// separating space before content and leave headroom for longer labels
// (e.g. "SYSTEM ERROR", "TOOL CALL") without re-tuning.
const CollapsedLabelWidth = 16
