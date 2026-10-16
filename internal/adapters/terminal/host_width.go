package terminal

// The cells a HOST may charge a string, as opposed to the cells this package's
// one table charges it (width.go).
//
// Two callers need the same answer, which is why the classes live in the
// adapter rather than in a test:
//
//   - the status bar's layout (tui_status.go). Its right group carries text the
//     app does not own — the model name comes from the session — and the row is
//     the frame's BOTTOM one, the one place a cell written past the last column
//     wraps AND the wrap scrolls the terminal (measured in
//     screen_tmux_join_test.go). The row therefore reserves what a host may
//     charge for its own text, instead of leaving a cell blank in every session
//     whether or not that session's name can be billed differently.
//   - the containment tests (host_width_test.go), which judge the frame against
//     a screen model that bills like a host: the classes it exercises are these,
//     so the list the tests lean on and the list the layout reserves for cannot
//     drift apart.
//
// The classes and their numbers are MEASURED rather than derived.
// misc/probe-tmux-widths.sh re-takes them against a real tmux (one run per row,
// #{cursor_x}, printed with the build) and host_width_test.go holds the recorded
// pairs, so a change here that the probe does not agree with fails a test.
//
// What is deliberately not here: a model of what a host DRAWS. This is an upper
// bound for reserving space, not a prediction. A host outside these classes can
// charge anything, and double-width-ambiguous hosts are outside the supported
// configurations at all (constants.go → glyph policy, waiver 2).

// The code points the class checks below name as NUMBERS rather than as literals,
// because glyphs_test.go reads every non-ASCII literal in this package's sources
// as a glyph the adapter DRAWS and demands a policy entry for it. These four are
// a lookup set — joiners and selectors, listed so a cluster can be tested for
// them — and the adapter never writes one to a terminal on its own.
const (
	cpKeycap = 0x20E3 // combining enclosing keycap
	cpZWJ    = 0x200D // zero-width joiner
	cpVS15   = 0xFE0E // variation selector-15, text presentation
	cpVS16   = 0xFE0F // variation selector-16, emoji presentation
)

// hasCodePoint reports whether s contains any of the given code points.
func hasCodePoint(s string, cps ...rune) bool {
	for _, r := range s {
		for _, cp := range cps {
			if r == cp {
				return true
			}
		}
	}
	return false
}

// countCodePoint returns how many times cp appears in s.
func countCodePoint(s string, cp rune) int {
	n := 0
	for _, r := range s {
		if r == cp {
			n++
		}
	}
	return n
}

// hostWiderByOne are the code points a host was measured to bill one cell wider
// than this package's table, as part of a cluster. Each is a spacing mark the
// host does not combine onto the previous cell (this table's UAX #29 clustering
// does), or a format character this table charges nothing for.
var hostWiderByOne = map[rune]bool{
	0x093E: true, // devanagari aa-matra
	0x093F: true, // devanagari i-matra
	0x0940: true, // devanagari ii-matra
	0x09BE: true, // bengali aa-matra
	0x09BF: true, // bengali i-matra
	0x0A3F: true, // gurmukhi sihari
	0x0ABF: true, // gujarati i-matra
	0x0BBE: true, // tamil aa-matra
	0x0BBF: true, // tamil i-matra
	0x0D3E: true, // malayalam aa-matra
	0x0D3F: true, // malayalam i-matra
	0x0DDA: true, // sinhala aeda-pilla
	0x0E33: true, // thai sara am
	0x17B6: true, // khmer aa
	0x00AD: true, // soft hyphen
	0x0600: true, // arabic number sign
}

// hostNarrowerBy is the same, for the classes the host billed LESS than this
// table does: U+3164 and U+1160, the Hangul fillers the host ignores entirely (0
// cells where the table bills two and one). A lone regional indicator is the
// other narrower class and is handled by hostDelta, because a PAIR of them
// agrees, and the two other fillers (U+115F, U+FFA0) agree as they stand.
var hostNarrowerBy = map[rune]int{
	0x3164: 2,
	0x1160: 1,
}

// hostDelta returns the cells to add to the cells this package's table charges a
// cluster, to get the cells the measured host charges it. A negative result
// means the host charges fewer. Every rule below is a property of the cluster's
// CODE POINTS, so the cluster's width is not a parameter — it was, for the
// emoji-presentation rule this table turned out to cover itself (see above).
//
// Emoji presentation is deliberately absent: a cluster ending in a variation
// selector is TWO cells to this table already (displaywidth applies the
// presentation rule), and tmux forces the joined cell to two as well — measured
// on "a"+U+FE0F, "1"+U+FE0F, "!"+U+FE0F and a keycap, which are 2 and 2 every
// time. A rule here for it would be inert and would read as if the table needed
// help with the class.
func hostDelta(cluster string) int {
	delta := 0
	for _, r := range cluster {
		if hostWiderByOne[r] {
			delta++
		}
		delta -= hostNarrowerBy[r]
	}
	// A zero-width joiner: a sequence this table draws as ONE cluster (two
	// cells, the first emoji's) is several to a host that does not cluster at
	// all — the class docs/tui.md records as "a ZWJ family is one cluster here
	// and several there", where such a host bills every emoji it can see. tmux
	// itself clusters them (the probe shows the family at two cells, agreeing),
	// so this models the pessimistic side of a recorded class rather than a
	// measurement; it is what makes a multi-row spill reachable in the
	// containment tests. Each joiner is one more emoji the host draws, at two
	// cells.
	delta += 2 * countCodePoint(cluster, cpZWJ)
	if delta == 0 && isLoneRegionalIndicator(cluster) {
		// The host's default width cache bills a regional indicator one cell;
		// this table bills a lone one two (a pair is two on both).
		delta--
	}
	return delta
}

// isLoneRegionalIndicator reports whether the cluster is exactly one regional
// indicator code point.
func isLoneRegionalIndicator(cluster string) bool {
	runes := []rune(cluster)
	return len(runes) == 1 && runes[0] >= 0x1F1E6 && runes[0] <= 0x1F1FF
}

// hostMaxCells returns the cells a host may charge s: this package's width plus
// every cell the measured classes can add to it, and never less than this
// table's own answer. The clusters a host charges less for (a lone regional
// indicator, a Hangul filler) are not subtracted: the point is an upper bound,
// so the space reserved for a string can never be less than what a host draws.
func hostMaxCells(s string) int {
	cells := 0
	walkCells(s, func(text string, w int) bool {
		if d := hostDelta(text); d > 0 {
			w += d
		}
		cells += w
		return true
	})
	return cells
}

// hostCells returns the cells the measured host charges s — this table's width
// plus every delta, positive or negative. It is what the probe's recorded
// numbers are compared against (host_width_test.go); a LAYOUT reserves space
// with hostMaxCells, because a reservation must never be short.
func hostCells(s string) int {
	cells := 0
	walkCells(s, func(text string, w int) bool {
		cells += w + hostDelta(text)
		return true
	})
	return cells
}

// hostMayBillDifferently reports whether s carries a cluster whose width this
// package's table and a host's are measured — or recorded as likely — to
// disagree about, so a screen comparison that reads the cluster back cannot be
// judged on it:
//
//   - a spacing mark the host bills on its own (hostWiderByOne),
//   - the cell a variation selector joined, which the host forces to two cells,
//   - a zero-width joiner, one cluster here and several to a host that does not
//     cluster,
//   - U+3164 HANGUL FILLER, which the host ignores, and a lone regional
//     indicator, which its width cache bills one cell.
//
// The list is the measured classes plus the two the glyph policy already records
// as host-dependent (a variation selector and a ZWJ sequence). Everything else —
// ASCII, CJK, the box-drawing glyphs, braille, the tool indicators — is asserted
// where it is compared.
func hostMayBillDifferently(s string) bool {
	if hasCodePoint(s, cpVS15, cpVS16, cpKeycap, cpZWJ) {
		return true
	}
	for _, r := range s {
		if hostWiderByOne[r] || hostNarrowerBy[r] != 0 {
			return true
		}
		if r >= 0x1F1E6 && r <= 0x1F1FF {
			return true
		}
	}
	return false
}
