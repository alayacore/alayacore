package terminal

// restyleBreaks hands back untouched any string the WrapWriter would not have
// changed, and canRestyleNothing is the predicate that decides which those are.
// This file is the evidence that the predicate errs only one way: a string it
// reports safe to skip must be one the writer leaves alone. The other direction
// needs no evidence, because a string it does not report safe goes through the
// writer and so is the writer's own answer.
//
// The oracle below is the previous implementation, verbatim — always through the
// WrapWriter, with no fast path to agree with. Keeping it is what makes the
// property test a comparison rather than a restatement.

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

// restyleCorpus names the shapes that sit near the line canRestyleNothing draws.
// The property test below covers far more of them; this one is readable, and its
// failure messages say which shape broke.
func restyleCorpus() []string {
	return []string{
		// Skipped: no introducer, well-formed. The writer must be an identity.
		"",
		"a",
		"plain text with no escapes at all",
		"multi\nline\nplain\ntext",
		"\n",
		"\n\n\n",
		"a trailing newline\n",
		"\x07 a bell is not an introducer\n",
		"\x7f a delete byte is not one either\n",
		"tab\there\nand there",
		"carriage\rreturn\nand a break",
		"中文\nacross a break",
		"emoji 👨‍👩‍👧‍👦 and a break\nafter",
		strings.Repeat("a long plain line that wraps at some point ", 20) + "\nand a second",
		// Through the writer: a 7-bit ESC.
		"\x1b[38;2;88;91;112mabc\ndef\x1b[0m",
		"\x1b[1mbold across\na break\x1b[0m",
		"\x1b[1mstyled with no break at all\x1b[0m",
		"\x1b[1mstyled and never terminated\n",
		"\x1b]8;;https://example.com\x07a link\x1b]8;;\x07\nafter it",
		"\x1b[31m中文\n红字\x1b[0m",
		strings.Repeat("\x1b[31mstyled\x1b[0m\n", 20),
		// Through the writer: a C1 control in the two-byte UTF-8 form hasEscape
		// recognizes, C2 9B being CSI.
		"\xc2\x9b31m C1 CSI, no break",
		"\xc2\x9b31m C1 CSI across\na break",
		// The shapes that make utf8.ValidString part of the predicate rather
		// than decoration: a LONE C1 byte, which the parser reads as an
		// introducer and hasEscape deliberately does not.
		"\x9b31m raw C1 CSI, no break",
		"\x9b31m raw C1 CSI across\na break",
		"\x9b]8;;https://example.com\x07raw C1 OSC\x9b]8;;\x07\nafter",
		"\x80 a lone continuation byte\n",
		"\xff not UTF-8 at all\n",
	}
}

// TestRestyleBreaksIsTheIdentityOnPlainContent is the fast path's own claim, on
// named shapes: for every string canRestyleNothing accepts, the writer produces
// that string unchanged, so handing it back is not an approximation.
func TestRestyleBreaksIsTheIdentityOnPlainContent(t *testing.T) {
	skipped := 0
	for _, s := range restyleCorpus() {
		if !canRestyleNothing(s) {
			continue
		}
		skipped++
		if got := oldRestyleBreaks(s); got != s {
			t.Errorf("the writer changed a string the fast path skips:\n got %q\nwant %q", got, s)
			continue
		}
		if got := restyleBreaks(s); got != s {
			t.Errorf("restyleBreaks(%q) = %q, want it handed back untouched", s, got)
		}
	}
	if skipped == 0 {
		t.Fatal("no corpus entry is skippable, so the fast path was never taken")
	}
	t.Logf("%d of %d entries are skipped, and the writer is an identity on all of them",
		skipped, len(restyleCorpus()))
}

// restyleTokens are the pieces generated strings are built from. They are chosen
// so that both halves of canRestyleNothing are load-bearing: 7-bit and two-byte
// introducers that hasEscape finds, lone bytes in 0x80..9F that only
// utf8.ValidString catches, malformed sequences, and enough plain and multi-byte
// text that the skipped side is populated too.
var restyleTokens = []string{
	"a", "bb", "ccc", " ", "\t", "\r", "\n", "\n\n", "word ", "the quick brown fox",
	"中", "文", "中文", "…", "é", "αβγ", "👨‍👩‍👧‍👦", "日本語のテキスト",
	"\x1b[31m", "\x1b[0m", "\x1b[1;32m", "\x1b[m", "\x1b",
	"\x1b]8;;https://x.example\x07", "\x1b]8;;\x07",
	"\xc2\x9b31m", "\xc2\x9b0m", "\xc2\x9b",
	"\x9b31m", "\x9b0m", "\x9b", "\x80", "\x9f", "\x8a", "\x7f",
	"\xff", "\xfe\x80", "\xc2", "\xe4\xb8",
}

// TestNothingTheWriterChangesEvadesCanRestyleNothing is the soundness condition,
// over generated strings rather than chosen ones: whatever the writer changes,
// canRestyleNothing must decline to skip. It also requires both halves of the
// predicate to have been needed by something in the corpus, so that neither can
// be deleted with the test still passing.
func TestNothingTheWriterChangesEvadesCanRestyleNothing(t *testing.T) {
	var skipped, written, neededValidUTF8, neededHasEscape int
	for _, seed := range []int64{1, 7, 42, 99, 1234, 2024, 31337, 55555, 77777, 99999} {
		rng := rand.New(rand.NewSource(seed))
		for iter := 0; iter < 400; iter++ {
			var sb strings.Builder
			if rng.Intn(6) == 0 {
				// Raw bytes, so malformed sequences are common rather than rare.
				for n := rng.Intn(24); n >= 0; n-- {
					sb.WriteByte(byte(rng.Intn(256)))
				}
			} else {
				for n := rng.Intn(8) + 1; n > 0; n-- {
					sb.WriteString(restyleTokens[rng.Intn(len(restyleTokens))])
				}
			}
			s := sb.String()

			want := oldRestyleBreaks(s)
			got := restyleBreaks(s)
			if got != want {
				t.Fatalf("seed=%d iter=%d: restyleBreaks(%q) = %q, want %q", seed, iter, s, got, want)
			}
			if canRestyleNothing(s) {
				skipped++
				if want != s {
					t.Fatalf("seed=%d iter=%d: canRestyleNothing(%q) is true but the writer changed it to %q — the fast path would drop the restyle",
						seed, iter, s, want)
				}
				continue
			}
			written++
			if want == s {
				continue
			}
			// The writer changed it. Whichever half of the predicate caught it
			// is the half that has to be there.
			switch {
			case !hasEscape(s):
				neededValidUTF8++
				t.Logf("seed=%d iter=%d: %q is changed by the writer and hasEscape sees nothing — only utf8.ValidString declines it", seed, iter, s)
			default:
				neededHasEscape++
			}
		}
	}
	if skipped == 0 {
		t.Fatal("nothing was skipped, so the fast path was never exercised")
	}
	if written == 0 {
		t.Fatal("nothing went through the writer, so the comparison proved nothing")
	}
	if neededHasEscape == 0 {
		t.Fatal("no string needed hasEscape to decline it, so that half of the predicate was never exercised")
	}
	if neededValidUTF8 == 0 {
		t.Fatal("no string needed utf8.ValidString to decline it, so that half of the predicate is decoration and the corpus missed the case it exists for")
	}
	t.Logf("%d strings skipped, %d written through; of those the writer changed, %d were declined by hasEscape and %d only by utf8.ValidString",
		skipped, written, neededHasEscape, neededValidUTF8)
}

// TestRestyleBreaksDoesNotAllocatePerByte guards the scratch a single byte is
// written from. The fast path means plain content never reaches the writer at
// all, so the writer's own cost is visible only on styled content — and there it
// used to be one allocation per byte, []byte{b} for each of them. Lengthening a
// styled string without adding a break must therefore add almost nothing: what
// the writer emits per byte is the byte itself, and what it emits per break is
// bounded by the breaks. The little it does add is the buffer growing.
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
// per-line cost of a full re-wrap what it is: wrapVisualLines' width scratch
// lives outside its loop, and restyleBreaks does not build a WrapWriter for a
// line it can skip. Both were once comments with nothing behind them — the
// scratch was described as reused while it was declared inside the loop, and a
// plain single-row line paid 38 allocations for a pen that never left zero.
//
// What is asserted is the slope rather than a count, because counts move: doubling
// the lines must not add anything like one allocation per line. With both in
// place the remaining per-line allocation is the rows slice wrapRows splits the
// wrapped line into; a scratch declared inside the loop doubles that, and losing
// the fast path multiplies it by roughly 39.
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
