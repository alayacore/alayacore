package terminal

// Making content safe to draw.
//
// The adapter's contract with the terminal is that the bytes it emits are the
// bytes it measured: a row's padding, its wrap points and the caret's column all
// come from a width, and a terminal that reads those bytes differently draws the
// row somewhere else. Ill-formed UTF-8 breaks that contract in a way no width
// table can repair, because what an invalid byte draws is each terminal's own
// error recovery. Measured on tmux 3.7c: a lone byte in 0x80..0x9F becomes one
// U+FFFD of one cell, while the escape parser this package also uses reads 0x9B
// as a C1 introducer and swallows the bytes after it, and the width table bills
// "a" + 0xF5 + "b" as one cell where the terminal draws three. Three components,
// three answers, and only one of them is what the user sees.
//
// Emitting well-formed UTF-8 removes the question rather than answering it three
// ways: every terminal then draws what the width table says, the escape parser
// has no raw C1 to mistake for an introducer, and no library's handling of an
// invalid byte is ever consulted. So content is repaired on its way into a Window
// — AppendFromTLV and its neighbors — and everything downstream may assume it.
//
// The input side is the same question arriving with a weaker guarantee. It has to
// hold a run a read cut short, because its remaining bytes are still in the
// kernel's buffer and a replacement for them now would be a box for a character
// that is arriving — and it has to replace a run that is finished but ill-formed
// with one U+FFFD, which is the rule above. Those are two answers to "is this
// unfinished or is it broken", and only the second is a replacement: the hold is
// decodePrintable's, the run is the same illFormedRun the repair here walks, and
// the measurements in sanitize_test.go are asserted of both paths. So a keystroke,
// a paste and a Window's content all draw the same number of boxes for the same
// bytes, and nothing decides what an invalid byte means by accident. A paste is
// repaired where a read becomes text — closePaste, which hands over a string every
// consumer can take as one.

import (
	"strings"
	"unicode/utf8"
)

// replacementChar is what one ill-formed run draws as: U+FFFD, one cell, in every
// terminal, because it is well-formed UTF-8 and so is not an error to recover from.
const replacementChar = "\uFFFD"

// sanitizeUTF8 returns s with every ill-formed run replaced by one U+FFFD, and s
// itself when s is already well-formed — the common case, which costs one scan and
// allocates nothing.
//
// The rule is the one a terminal applies, measured rather than assumed (see
// TestSanitizeUTF8MatchesWhatATerminalDraws, whose expectations are tmux 3.7c's
// own cursor column and pane bytes): a lead byte consumes the continuation bytes
// its encoding calls for whether or not the sequence they form is legal, and the
// whole consumed run becomes one U+FFFD. A truncated 3-byte sequence is therefore
// one replacement and not three, while three lone continuation bytes are three.
//
// It is not strings.ToValidUTF8, which replaces a whole run of bad bytes with one
// U+FFFD and so shows one box where the terminal shows three, and not a loop over
// DecodeRune either, which advances one byte per error and so shows three boxes
// for a truncated sequence where the terminal shows one.
func sanitizeUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + len(replacementChar))
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if r != utf8.RuneError || n > 1 {
			// Well-formed. n > 1 is what tells a real U+FFFD in the content from
			// the (RuneError, 1) DecodeRuneInString answers for a bad byte.
			b.WriteString(s[i : i+n])
			i += n
			continue
		}
		b.WriteString(replacementChar)
		i += illFormedRun(s[i:])
	}
	return b.String()
}

// illFormedRun is how many bytes a terminal consumes for the one replacement it
// draws, given that the first byte cannot begin a well-formed sequence — which is
// the only way a caller asks.
//
// It is written over the two things a caller can be holding, a Window's content and
// a read, because this is one rule and the type it is written over is not a reason
// for a second copy of it. The keystroke path needs it per byte of a buffer and the
// render path per run of a string, and both are asking the same question the same
// terminal was measured answering. (isPathSep in attachment_window.go is the same
// shape for the same reason: one rule, two types, and it is the rule that is the
// single thing.)
func illFormedRun[T ~string | ~[]byte](s T) int {
	// Continuations the lead byte is owed: one for a 2-byte encoding, two for a
	// 3-byte, three for a 4-byte. Taken whether or not they make the sequence
	// legal, which is how a surrogate or an overlong encoding ends up as one
	// replacement rather than one per byte.
	var want int
	switch lead := s[0]; {
	case lead >= 0xC2 && lead <= 0xDF:
		want = 1
	case lead >= 0xE0 && lead <= 0xEF:
		want = 2
	case lead >= 0xF0 && lead <= 0xF4:
		want = 3
	default:
		// 0x80..0xC1 and 0xF5..0xFF cannot lead a sequence at all: the first range
		// is a continuation with no lead plus the two leads that could only encode
		// an overlong sequence, and the second is past U+10FFFF. Such a byte is its
		// own run. This is what makes a lone C1 control one cell — the case the
		// escape parser gets wrong.
		return 1
	}
	n := 1
	for n <= want && n < len(s) && s[n]&0xC0 == 0x80 {
		n++
	}
	return n
}
