package terminal

// Glyph policy enforcement. constants.go states the rule; this file is what
// keeps it true.
//
// The scan is done with go/parser over this package's own non-test sources,
// so it cannot drift from what the adapter actually draws: a new symbol
// anywhere in the terminal adapter fails here until it is classified, and a
// symbol that stops being drawn fails until its entry goes with it.
//
// What "classified" buys us is the one thing runtime cannot detect. A
// terminal configured for double-width ambiguous characters (xterm
// -cjkwidth, mlterm in a CJK locale, some font configurations) draws an
// East-Asian-Ambiguous glyph two cells wide, and every width library we
// build with answers 1 for it. So the property is measured offline, against
// the tables in those libraries — uniseg's switchable
// EastAsianAmbiguousWidth is the oracle for "is this glyph Ambiguous?" —
// and pinned here. (The terminal consequence follows from the property and
// has not been measured on a host here; see arrows_test.go for the same
// disclaimer.)
//
// This test mutates the package-level uniseg.EastAsianAmbiguousWidth and
// restores it. No test in this package uses t.Parallel(), which is what
// makes that safe; if that changes, the probe needs its own process.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

type glyphClass struct {
	// cells is what the layout reserves for the glyph, under the width
	// model the renderer measures with (ansi.StringWidth).
	cells int
	// ambiguous records "East_Asian_Width = A": a double-width-ambiguous
	// terminal draws it two cells, which is policy waiver 2 (constants.go).
	// Every Ambiguous glyph drawn here must be listed with the reason it is
	// tolerated; anything not on the list may not be Ambiguous at all.
	ambiguous bool
	why       string
}

// drawnGlyphs is the complete set of non-ASCII characters the terminal
// adapter draws, and the verdict on each. Keep the reasons honest: an entry
// that is not Ambiguous must not claim the waiver, and vice versa — the
// test measures both.
var drawnGlyphs = map[rune]glyphClass{
	// --- Fixed-width, Neutral: policy rule 1 satisfied. -----------------
	'▸': {1, false, "collapsed fold arrow (constants.go)"},
	'▾': {1, false, "expanded fold arrow (constants.go)"},
	'∙': {1, false, "status dot — the only state marker in the status bar"},
	'✓': {1, false, "tool success (tool_render.go)"},
	'✗': {1, false, "tool failure (tool_render.go)"},
	'⠋': {1, false, "tool spinner frame"},
	'⠙': {1, false, "tool spinner frame"},
	'⠹': {1, false, "tool spinner frame"},
	'⠸': {1, false, "tool spinner frame"},
	'⠼': {1, false, "tool spinner frame"},
	'⠴': {1, false, "tool spinner frame"},
	'⠦': {1, false, "tool spinner frame"},
	'⠧': {1, false, "tool spinner frame"},
	'⠇': {1, false, "tool spinner frame"},
	'⠏': {1, false, "tool spinner frame"},

	// --- Waiver 2a: box drawing, no Neutral alternative. ----------------
	// The whole range U+2500-U+257F is Ambiguous (measured — the frame and
	// the grid below are drawn from it), so a double-width-ambiguous
	// terminal breaks the frame regardless of which glyph is picked. The
	// alternative is an ASCII glyph set, which is a product decision
	// (constants.go, waiver 2).
	'─': {1, true, "box rule: box drawing is Ambiguous through and through"},
	'│': {1, true, "markdown table vertical rule: same waiver; the help bars dropped this glyph for an ASCII pipe"},
	'┌': {1, true, "markdown table corner"},
	'┐': {1, true, "markdown table corner"},
	'└': {1, true, "markdown table corner"},
	'┘': {1, true, "markdown table corner"},
	'├': {1, true, "markdown table junction"},
	'┤': {1, true, "markdown table junction"},
	'┬': {1, true, "markdown table junction"},
	'┴': {1, true, "markdown table junction"},
	'┼': {1, true, "markdown table junction"},

	// --- Waiver 2b: typographic marks with no Neutral same-meaning pair. --
	'…': {1, true, "truncation marker; the Neutral option ⋯ (U+22EF) is mid-line, thin, and badly covered"},
	'—': {1, true, "prose dash inside a dialog message — not a column, and ASCII '-' reads as a hyphen here"},
	'↓': {1, true, "auto-follow marker in 'F↓'; every Neutral arrow measured (⬇, ⏷) is either pictographic or badly covered. Candidate for an ASCII replacement"},
	'∞': {1, true, "unlimited context in the model column. Candidate for the word 'none'"},

	// --- Wide by design: the media icons. --------------------------------
	// Two cells under BOTH width models (they are Extended_Pictographic
	// with Emoji_Presentation=Yes), so the layout reserves two. What is
	// pinned here is that each stays a single codepoint — no U+FE0F, no ZWJ
	// (policy rule 3) — and that nothing else in the adapter is wide.
	'📷': {2, false, "image attachment icon"},
	'🎬': {2, false, "video attachment icon"},
	'🎵': {2, false, "audio attachment icon"},
	'📄': {2, false, "document attachment icon"},
}

// TestDrawnGlyphsMatchPolicy scans this package's sources for the symbols it
// draws and holds each one to its entry in drawnGlyphs.
func TestDrawnGlyphsMatchPolicy(t *testing.T) {
	drawn, err := drawnGlyphsInPackage(".")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	var unclassified, stale []string
	for r, files := range drawn {
		if _, ok := drawnGlyphs[r]; !ok {
			unclassified = append(unclassified, fmt.Sprintf("%q U+%04X (EAW=%s, ansi=%d, uniseg=%d) in %s",
				r, r, eastAsianClass(r), ansi.StringWidth(string(r)), uniseg.StringWidth(string(r)),
				strings.Join(files, ", ")))
		}
	}
	for r := range drawnGlyphs {
		if _, ok := drawn[r]; !ok {
			stale = append(stale, fmt.Sprintf("%q U+%04X — classified but no longer drawn", r, r))
		}
	}
	if len(unclassified) > 0 {
		sort.Strings(unclassified)
		t.Errorf("glyphs drawn without a policy entry (classify them in glyphs_test.go, or drop them):\n  %s",
			strings.Join(unclassified, "\n  "))
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Errorf("stale glyph policy entries:\n  %s", strings.Join(stale, "\n  "))
	}

	for r, want := range drawnGlyphs {
		if _, ok := drawn[r]; !ok {
			continue // reported as stale above
		}
		g := string(r)

		// The two width models in this package must agree on the glyph.
		// This is the check that catches the class policy rule 3 is about:
		// displaywidth reports U+2630 as two cells and uniseg as one, and
		// reports a text glyph followed by U+FE0F as two where uniseg says
		// one — a string measured by one library and sliced by the other is
		// then one cell off, invisibly.
		if a, u := ansi.StringWidth(g), uniseg.StringWidth(g); a != u {
			t.Errorf("%q U+%04X: the two width models disagree — ansi=%d uniseg=%d", g, r, a, u)
		}
		if got := ansi.StringWidth(g); got != want.cells {
			t.Errorf("%q U+%04X: measures %d cells, policy reserves %d (%s)", g, r, got, want.cells, want.why)
		}
		if n := utf8.RuneCountInString(g); n != 1 {
			t.Errorf("%q U+%04X: %d codepoints, policy allows single-codepoint glyphs only", g, r, n)
		}

		// Ambiguity, measured rather than asserted from prose.
		if got := isEastAsianAmbiguous(r); got != want.ambiguous {
			if got {
				t.Errorf("%q U+%04X is East-Asian Ambiguous but is not declared as such (%s) — it needs a waiver reason in drawnGlyphs", g, r, want.why)
			} else {
				t.Errorf("%q U+%04X is claimed Ambiguous (%s) but measures one cell even in a double-width-ambiguous terminal — the waiver is stale", g, r, want.why)
			}
		}
	}
}

// TestStatusDotIsNeutralWidth pins the specific change that motivated the
// policy: the status bar draws its indicator into the FIRST cell of a row
// truncated to exactly the terminal width, so an Ambiguous glyph there
// shifts the whole bar and wraps its last segment away.
func TestStatusDotIsNeutralWidth(t *testing.T) {
	if statusDot != "∙" {
		t.Errorf("statusDot = %q, want U+2219 (BULLET OPERATOR)", statusDot)
	}
	if isEastAsianAmbiguous([]rune(statusDot)[0]) {
		t.Errorf("statusDot %q is East-Asian Ambiguous — the old pair (· U+00B7, • U+2022) was, and that is what this replaced", statusDot)
	}
	if w := ansi.StringWidth(statusDot); w != 1 {
		t.Errorf("statusDot measures %d cells, the status row reserves 1", w)
	}
}

// isEastAsianAmbiguous asks uniseg — the library whose tables this build
// links — whether the rune's East_Asian_Width is "A": it charges an
// Ambiguous rune EastAsianAmbiguousWidth cells and every other rune the
// same in both settings, so a rune whose width changes is exactly the set
// of Ambiguous runes.
func isEastAsianAmbiguous(r rune) bool {
	orig := uniseg.EastAsianAmbiguousWidth
	defer func() { uniseg.EastAsianAmbiguousWidth = orig }()

	uniseg.EastAsianAmbiguousWidth = 1
	narrow := uniseg.StringWidth(string(r))
	uniseg.EastAsianAmbiguousWidth = 2
	wide := uniseg.StringWidth(string(r))
	return narrow != wide
}

// drawnGlyphsInPackage returns the non-ASCII runes inside the string
// literals of dir's non-test Go files — the characters the adapter can put
// on screen. Comments and rune literals are out of scope by construction:
// go/parser hands us literal tokens, and the only non-ASCII rune literals
// in this package are in tests.
func drawnGlyphsInPackage(dir string) (map[rune][]string, error) {
	found := map[rune][]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	fileSet := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		f, err := parser.ParseFile(fileSet, path, nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		runes := nonASCIIInStrings(f)
		sort.Slice(runes, func(i, j int) bool { return runes[i] < runes[j] })
		for i, r := range runes {
			if i > 0 && r == runes[i-1] {
				continue // already recorded for this file
			}
			found[r] = append(found[r], name)
		}
	}
	return found, nil
}

// nonASCIIInStrings collects the non-ASCII runes inside the string literals
// of a parsed file.
func nonASCIIInStrings(f *ast.File) []rune {
	var runes []rune
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		for _, r := range s {
			if r > utf8.RuneSelf {
				runes = append(runes, r)
			}
		}
		return true
	})
	return runes
}

// eastAsianClass labels a rune's East_Asian_Width class for error messages.
func eastAsianClass(r rune) string {
	if isEastAsianAmbiguous(r) {
		return "A"
	}
	if uniseg.StringWidth(string(r)) == 2 {
		return "W"
	}
	return "N"
}
