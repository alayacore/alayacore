package terminal

// restyleBreaks hands back untouched any string whose line breaks have no style to
// carry across them, and canRestyleNothing is the predicate that decides which
// those are. This file checks the direction that has to hold: a string containing
// an escape is never skipped, so no row loses the style it was supposed to keep.
// The other direction needs no check beyond the predicate itself — a string that is
// skipped goes through nothing and comes back byte for byte.
//
// The oracle below is the previous implementation, verbatim: always through the
// WrapWriter, with no fast path to agree with. It is what a skipped string is
// compared against when the string carries an escape, and it is deliberately NOT
// the authority on what a string without one should produce — see
// TestALoneC1ControlIsTextNotAnIntroducer for the case where the two disagree and
// the terminal settles it.

import (
	"bytes"
	"io"
	"math/rand"
	"strings"
	"testing"
)

// oldRestyleBreaks is restyleBreaks before the fast path.
func oldRestyleBreaks(s string) string {
	var buf bytes.Buffer
	w := NewWrapWriter(&buf)
	defer w.Close() //nolint:errcheck // the only error a bytes.Buffer returns is nil
	_, _ = io.WriteString(w, s)
	return buf.String()
}

// TestALoneC1ControlIsTextNotAnIntroducer is the case where the WrapWriter and a
// terminal disagree, and the reason canRestyleNothing asks about an ESC rather than
// about well-formedness.
//
// The writer's escape parser is byte-oriented and reads a lone 0x9B as a C1
// introducer, so it takes "\x9b31m" for "set the foreground red" and re-applies
// that red after the next line break. tmux 3.7c does not: measured, it stores a
// U+FFFD for the byte and draws "31m" as text, which is 6 cells and no color
// (sanitize_test.go has the cursor columns and the pane bytes). The bytes the
// writer added are real 7-bit sequences, so a terminal obeys them — the invented
// red is drawn even though nothing asked for it.
//
// Content reaching here is well-formed anyway, so this is a case the invariant in
// sanitize.go already excludes. It is pinned because the wrong version of it was
// written once: a predicate that sent ill-formed content to the writer "to be
// safe", which is the one input where the writer is the thing that is unsafe.
func TestALoneC1ControlIsTextNotAnIntroducer(t *testing.T) {
	for _, s := range []string{
		"\x9b31m raw C1 CSI across\na break",
		"\x9b31m raw C1 CSI, no break",
		"\x9b]8;;https://example.com\x07raw C1 OSC\x9b]8;;\x07\nafter",
		"a\x80b lone continuation\nand a break",
	} {
		if !canRestyleNothing(s) {
			t.Errorf("canRestyleNothing(%q) is false, so the writer runs over it and invents SGR the terminal never saw an introducer for", s)
			continue
		}
		if got := restyleBreaks(s); got != s {
			t.Errorf("restyleBreaks(%q) = %q, want it handed back untouched", s, got)
		}
		// Recorded rather than asserted as correct: this is what the writer would
		// have done, and it is the behavior being declined.
		if changed := oldRestyleBreaks(s); changed != s {
			t.Logf("%q: the writer would have produced %q", s, changed)
		}
	}
}

// TestNothingWithAnEscapeIsSkipped is the safety direction, over generated strings
// rather than chosen ones: whatever carries an ESC goes through the writer and
// comes out as the writer's answer, and whatever is skipped carries no ESC and
// comes back untouched. Both branches must be exercised, or the test has proved
// nothing about one of them.
func TestNothingWithAnEscapeIsSkipped(t *testing.T) {
	var skipped, written, restyled int
	for _, seed := range []int64{1, 7, 42, 99, 1234, 2024, 31337, 55555, 77777, 99999} {
		rng := rand.New(rand.NewSource(seed))
		for iter := 0; iter < 400; iter++ {
			var sb strings.Builder
			if rng.Intn(6) == 0 {
				// Raw bytes, so ill-formed sequences are common rather than rare:
				// the fast path has to be right about them too, and "right" is
				// decided by the absence of an ESC and not by well-formedness.
				for n := rng.Intn(24); n >= 0; n-- {
					sb.WriteByte(byte(rng.Intn(256)))
				}
			} else {
				for n := rng.Intn(8) + 1; n > 0; n-- {
					sb.WriteString(restyleTokens[rng.Intn(len(restyleTokens))])
				}
			}
			s := sb.String()
			hasESC := strings.ContainsRune(s, '\x1b')

			got := restyleBreaks(s)
			switch {
			case canRestyleNothing(s):
				skipped++
				if hasESC {
					t.Fatalf("seed=%d iter=%d: canRestyleNothing(%q) is true but the string carries an ESC, so a style in force across a break would be dropped", seed, iter, s)
				}
				if got != s {
					t.Fatalf("seed=%d iter=%d: restyleBreaks(%q) = %q, want it handed back untouched", seed, iter, s, got)
				}
			default:
				written++
				if !hasESC {
					// Not a failure — hasEscape also answers true for a DEL and for
					// a C1 control in its two-byte UTF-8 form, neither of which is
					// an introducer. Both are counted so the tally below says the
					// branch was reached for the reason that matters as well.
					continue
				}
				if want := oldRestyleBreaks(s); got != want {
					t.Fatalf("seed=%d iter=%d: restyleBreaks(%q) = %q, want the writer's %q", seed, iter, s, got, want)
				}
				if got != s {
					restyled++
				}
			}
		}
	}
	if skipped == 0 {
		t.Fatal("nothing was skipped, so the fast path was never exercised")
	}
	if restyled == 0 {
		t.Fatal("no string with an ESC was actually restyled, so the branch that keeps a style across a break was never compared with the writer")
	}
	t.Logf("%d strings skipped, %d written through the writer, %d of those restyled", skipped, written, restyled)
}

// restyleTokens are the pieces generated strings are built from: text, breaks,
// 7-bit sequences, C1 controls in both forms, and bytes that are not UTF-8 at all.
var restyleTokens = []string{
	"a", "bb", "ccc", " ", "\t", "\r", "\n", "\n\n", "word ", "the quick brown fox",
	"中", "文", "中文", "…", "é", "αβγ", "👨‍👩‍👧‍👦", "日本語のテキスト",
	"\x1b[31m", "\x1b[0m", "\x1b[1;32m", "\x1b[m", "\x1b",
	"\x1b]8;;https://x.example\x07", "\x1b]8;;\x07",
	"\xc2\x9b31m", "\xc2\x9b0m", "\xc2\x9b",
	"\x9b31m", "\x9b0m", "\x9b", "\x80", "\x9f", "\x8a", "\x7f",
	"\xff", "\xfe\x80", "\xc2", "\xe4\xb8",
}

// TestRestyleBreaksDoesNotAllocatePerByte guards the scratch a single byte is
// written from. The fast path means plain content never reaches the writer at all,
// so the writer's own cost is visible only on styled content — and there it used to
// be one allocation per byte, []byte{b} for each of them. Lengthening a styled
// string without adding a break must therefore add almost nothing: what the writer
// emits per byte is the byte itself, and what it emits per break is bounded by the
// breaks. The little it does add is the buffer growing.
func TestRestyleBreaksDoesNotAllocatePerByte(t *testing.T) {
	pad := func(n int) string {
		return "\x1b[31m" + strings.Repeat("a", n) + "\n" + strings.Repeat("b", n) + "\x1b[0m"
	}
	short := testing.AllocsPerRun(10, func() { restyleBreaks(pad(200)) })
	long := testing.AllocsPerRun(10, func() { restyleBreaks(pad(2000)) })
	if grew := long - short; grew > 8 {
		t.Errorf("ten times the bytes cost %.0f more allocations (%.0f to %.0f); the writer is allocating per byte again",
			grew, short, long)
	}
	t.Logf("200 bytes a line: %.0f allocations, 2000: %.0f", short, long)
}

// TestWrapVisualLinesKeepsOneWidthScratch guards the two things that make the
// per-line cost of a full re-wrap what it is: wrapVisualLines' width scratch lives
// outside its loop, and restyleBreaks does not build a WrapWriter for a line it can
// skip. Both were once comments with nothing behind them — the scratch was
// described as reused while it was declared inside the loop, and a plain single-row
// line paid 38 allocations for a pen that never left zero.
//
// What is asserted is the slope rather than a count, because counts move: doubling
// the lines must not add anything like one allocation per line. With both in place
// the remaining per-line allocation is the rows slice wrapRows splits the wrapped
// line into; a scratch declared inside the loop doubles that, and losing the fast
// path multiplies it by roughly 39.
func TestWrapVisualLinesKeepsOneWidthScratch(t *testing.T) {
	const n = 200
	line := "a short line that makes one row\n"
	oneN := strings.Repeat(line, n)
	twoN := strings.Repeat(line, 2*n)

	small := testing.AllocsPerRun(10, func() { wrapVisualLines(oneN, 80) })
	large := testing.AllocsPerRun(10, func() { wrapVisualLines(twoN, 80) })
	perLine := (large - small) / float64(n)

	if perLine > 1.5 {
		t.Errorf("wrapping %d more lines cost %.0f more allocations, %.2f per line; the width scratch and the skippable fast path together leave one",
			n, large-small, perLine)
	}
	t.Logf("%d lines: %.0f allocations, %d lines: %.0f, slope %.3f per line", n, small, 2*n, large, perLine)
}
