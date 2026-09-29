package terminal

// The expectations in this file are not derived from a specification or from the
// code under test. They are what tmux 3.7c drew, read back out of it: a pane 40
// columns wide starting at column 0, printf'ing the input, then #{cursor_x} for
// the cell count and capture-pane for the bytes it stored. The harness is
// /tmp/perf/invprobe.sh in the revision that took them; TestScreenMatchesATerminal
// UnderTmux is the same idea, applied to a whole frame instead of one string.
//
// They are recorded here because the alternative is a width table whose agreement
// with a terminal is a claim in a comment. Every row below is a case where a
// component in this package, or a library it uses, gives a different answer than
// the terminal does.

import (
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// terminalCases are the measured inputs. cells is tmux's cursor column after
// drawing the input; boxes is the number of U+FFFD it stored; want is what
// sanitizeUTF8 must produce so that the bytes emitted are the bytes measured.
var terminalCases = []struct {
	name  string
	in    string
	cells int
	boxes int
	want  string
}{
	{"well-formed ASCII", "ab", 2, 0, "ab"},
	{"a lone continuation byte", "a\x80b", 3, 1, "a\uFFFDb"},
	{"three lone continuation bytes", "a\x80\x81\x82b", 5, 3, "a\uFFFD\uFFFD\uFFFDb"},
	{"a 2-byte encoding cut short", "a\xc3b", 3, 1, "a\uFFFDb"},
	{"a 3-byte encoding cut short", "a\xe4\xb8b", 3, 1, "a\uFFFDb"},
	{"a 4-byte encoding cut short", "a\xf0\x9f\x98b", 3, 1, "a\uFFFDb"},
	{"an overlong encoding", "a\xc0\x80b", 4, 2, "a\uFFFD\uFFFDb"},
	{"a surrogate", "a\xed\xa0\x80b", 3, 1, "a\uFFFDb"},
	// A lead that has consumed what it is owed must not keep eating the
	// continuations after it: these are the shapes that tell "consume the run the
	// encoding calls for" from "consume every continuation in sight".
	{"a surrogate, then a continuation", "a\xed\xa0\x80\x80b", 4, 2, "a\uFFFD\uFFFDb"},
	{"a surrogate, then two continuations", "a\xed\xa0\x80\x80\x80b", 5, 3, "a\uFFFD\uFFFD\uFFFDb"},
	{"a whole 3-byte character, then a continuation", "a\xe4\xb8\x80\x80b", 5, 1, "a一\uFFFDb"},
	{"a whole 2-byte character, then a continuation", "a\xc3\x80\x80b", 4, 1, "aÀ\uFFFDb"},
	{"a byte past U+10FFFF", "a\xf5b", 3, 1, "a\uFFFDb"},
	{"two bytes that are never UTF-8", "a\xfe\xffb", 4, 2, "a\uFFFD\uFFFDb"},
	{"a lone C1 control", "a\x9bb", 3, 1, "a\uFFFDb"},
	{"a lone C1 control before text", "a\x9b31mb", 6, 1, "a\uFFFD31mb"},
	{"a C1 control in its UTF-8 form", "a\u009bb", 2, 0, "a\u009bb"},
}

// TestSanitizeUTF8MatchesWhatATerminalDraws is the invariant the repair exists
// for: after it runs, the width this package computes is the width the terminal
// drew. That is the whole contract — a row's padding, its wrap points and the
// caret's column are all arithmetic on that width, and every one of them is wrong
// by the same amount the width is.
func TestSanitizeUTF8MatchesWhatATerminalDraws(t *testing.T) {
	for _, tc := range terminalCases {
		got := sanitizeUTF8(tc.in)
		if got != tc.want {
			t.Errorf("%s: sanitizeUTF8(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
			continue
		}
		if !utf8.ValidString(got) {
			t.Errorf("%s: sanitizeUTF8(%q) = %q, which is not well-formed UTF-8", tc.name, tc.in, got)
			continue
		}
		if n := strings.Count(got, replacementChar); n != tc.boxes {
			t.Errorf("%s: %d replacements, the terminal drew %d", tc.name, n, tc.boxes)
		}
		if w := cellWidth(got); w != tc.cells {
			t.Errorf("%s: cellWidth of the repaired %q is %d, the terminal drew %d cells", tc.name, got, w, tc.cells)
		}
	}
}

// TestSanitizeUTF8IsWhatTheComponentsDisagreeAbout records the three answers one
// input used to get, so that the repair is understood as removing a disagreement
// rather than as a preference. It asserts the disagreements still exist in the
// components: if a library upgrade ever makes them agree, this test says so and
// the repair can be reconsidered rather than left because nobody checked.
func TestSanitizeUTF8IsWhatTheComponentsDisagreeAbout(t *testing.T) {
	// "a" + 0xF5 + "b": the terminal draws three cells. The width table, handed
	// the raw bytes, bills one — it takes 0xF5 for the lead of a 4-byte encoding
	// and consumes what follows it. That is a layout bug, not a rounding
	// difference: the row is padded and wrapped to a width two cells short of
	// what the terminal draws.
	const raw = "a\xf5b"
	if got := cellWidth(raw); got == 3 {
		t.Errorf("cellWidth(%q) = 3, so the width table now agrees with the terminal and the repair may be unnecessary; re-check whether it still is", raw)
	}
	if got := cellWidth(sanitizeUTF8(raw)); got != 3 {
		t.Errorf("cellWidth of the repaired %q = %d, want the 3 the terminal draws", raw, got)
	}

	// "a" + 0x9B + "[m b": the terminal draws six cells and stores a U+FFFD,
	// because it does not read a lone 0x9B as a C1 introducer. ansi.Strip does,
	// and removes the byte and the "[m" after it. hasEscape is what keeps Strip
	// away from that input, and it is the reason the width is right today.
	const c1 = "a\x9b[m b"
	if hasEscape(c1) {
		t.Errorf("hasEscape(%q) is true, so cellWidth would reach ansi.Strip, which does not agree with the terminal on a lone C1", c1)
	}
	if got := widthModel.String(ansi.Strip(c1)); got == 6 {
		t.Errorf("ansi.Strip now agrees with the terminal on %q (%d cells); the gate in hasEscape may no longer be load-bearing", c1, got)
	}
	if got := cellWidth(c1); got != 6 {
		t.Errorf("cellWidth(%q) = %d, want the 6 the terminal draws", c1, got)
	}
}

// TestSanitizeUTF8LeavesWellFormedContentAlone is the fast path, and the reason
// it can sit on every append: content that needs nothing is returned as the same
// string, so the common case is one scan and no allocation.
func TestSanitizeUTF8LeavesWellFormedContentAlone(t *testing.T) {
	for _, s := range []string{
		"",
		"plain ascii",
		"中文与 English mixed",
		"emoji 👨‍👩‍👧‍👦 and a keycap 1️⃣",
		"\x1b[31mstyled\x1b[0m and a break\n",
		"a\u009bb", // a C1 control, well-formed, zero cells, and not an introducer
		strings.Repeat("a long line of ordinary content ", 40),
	} {
		if got := sanitizeUTF8(s); got != s {
			t.Errorf("sanitizeUTF8 changed well-formed content:\n got %q\nwant %q", got, s)
		}
	}
}

// TestSanitizeUTF8NeverReturnsIllFormedOutput is the property the invariant rests
// on, over generated bytes rather than chosen ones: whatever comes in, what goes
// out is well-formed, and every byte of the input is either preserved inside a
// well-formed sequence or accounted for by a replacement.
func TestSanitizeUTF8NeverReturnsIllFormedOutput(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
	}{
		// Built from the shapes above plus text, so runs of bad bytes land beside
		// good ones and at both ends.
		{"empty", ""},
		{"one bad byte", "\x80"},
		{"bad at the end", "abc\xf5"},
		{"bad at the start", "\xfe abc"},
		{"only bad bytes", "\x80\x81\xc0\xf5\xfe"},
		{"truncated at the end", "abc\xe4\xb8"},
		{"c1 run", "\x9b\x9c\x9d"},
	} {
		if got := sanitizeUTF8(tc.in); !utf8.ValidString(got) {
			t.Errorf("%s: sanitizeUTF8(%q) = %q, which is ill-formed", tc.name, tc.in, got)
		}
	}
	// And the same over bytes with no shape chosen at all.
	for _, seed := range []int64{1, 7, 42, 99, 1234, 2024, 31337} {
		rng := rand.New(rand.NewSource(seed))
		for iter := 0; iter < 500; iter++ {
			raw := make([]byte, 1+rng.Intn(48))
			for i := range raw {
				raw[i] = byte(rng.Intn(256))
			}
			in := string(raw)
			got := sanitizeUTF8(in)
			if !utf8.ValidString(got) {
				t.Fatalf("seed=%d iter=%d: sanitizeUTF8(%q) = %q, which is ill-formed", seed, iter, in, got)
			}
			if utf8.ValidString(in) && got != in {
				t.Fatalf("seed=%d iter=%d: sanitizeUTF8 changed well-formed %q into %q", seed, iter, in, got)
			}
		}
	}
}
