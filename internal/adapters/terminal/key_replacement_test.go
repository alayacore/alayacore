package terminal

// The input path answers the same question the output path does — these bytes are
// not UTF-8, so what do they become? — and it has to answer it the same way, for
// the same reason: the prompt is drawn, and its width and its caret column are
// arithmetic on what the prompt holds.
//
// So the table in sanitize_test.go is the oracle for both directions. This file
// asks it of the input path: the same measured inputs, through the parser, and the
// text the prompt receives has to be the text the terminal drew. The two paths are
// separate functions over separate types (a read is bytes, a Window's content is a
// string) and stay separate; what they must not be is two different rules, which is
// what this asserts against one table.

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// inputText feeds bytes through the parser the way the input loop does —
// program_input.go calls Parse with a read — and returns the text the prompt would
// receive: the keys it produced, in order.
func inputText(in string) string {
	p := &InputParser{}
	var b strings.Builder
	for _, msg := range p.Parse([]byte(in)) {
		if k, ok := msg.(KeyPressMsg); ok {
			b.WriteRune(k.Code)
		}
	}
	return b.String()
}

// TestInputReplacementsMatchWhatATerminalDraws asks the input path the question the
// table answers: the same measured inputs, and the text the prompt receives has to
// be the text the terminal drew — the table's own want column, which is the bytes
// tmux stored, not this package's idea of them. Comparing the text rather than
// counting replacements is what makes a rule that consumed one byte too many fail
// here: the byte it swallowed is missing from the same string.
func TestInputReplacementsMatchWhatATerminalDraws(t *testing.T) {
	for _, tc := range terminalCases {
		if got := inputText(tc.in); got != tc.want {
			t.Errorf("%s: the parser yields %q, the terminal drew %q for the same bytes",
				tc.name, got, tc.want)
		}
	}
}

// TestPastedContentIsReplacedByTheSameRule is the third way text gets in and the
// one with no replacement of its own: pasted bytes are copied verbatim into a
// string, and whatever first turns that string into runes — []rune, on the way
// into the prompt — replaces an ill-formed byte with one U+FFFD, because that is
// what the language does by default and not because anything decided it.
//
// It is the same rule the keystroke path uses, arriving from the other direction,
// and it is wrong the same five ways by count — measured, not reasoned: 15 of the 17
// rows came through as bytes that are not text at all, and the replacement count was
// wrong on the same five the keystroke path got wrong. Repairing it where the parser
// hands the paste over means every consumer of PasteMsg gets text, not bytes that
// each of them would have to decide about separately.
func TestPastedContentIsReplacedByTheSameRule(t *testing.T) {
	for _, tc := range terminalCases {
		p := &InputParser{}
		var content string
		var seen bool
		for _, msg := range p.Parse([]byte("\x1b[200~" + tc.in + "\x1b[201~")) {
			if pm, ok := msg.(PasteMsg); ok {
				content, seen = pm.Content, true
			}
		}
		if !seen {
			t.Fatalf("%s: the paste did not close", tc.name)
		}
		if !utf8.ValidString(content) {
			t.Errorf("%s: pasted content %q is not well-formed, so every consumer decides for itself", tc.name, content)
			continue
		}
		if content != tc.want {
			t.Errorf("%s: the paste carries %q, the terminal drew %q for the same bytes", tc.name, content, tc.want)
		}
	}
}

// TestInputHoldsASequenceAReadCutShort is the other half of the rule, and the half
// a change here is most likely to break. A sequence the read cut short is not
// ill-formed, it is unfinished: the parser holds it for the next read, because the
// replacement it would otherwise emit now is a character that is still arriving —
// a burst longer than a read puts a boundary inside a multi-byte character as a
// matter of course, and without the hold a CJK character typed into the prompt
// arrives as three boxes.
func TestInputHoldsASequenceAReadCutShort(t *testing.T) {
	p := &InputParser{}
	for _, msg := range p.Parse([]byte("a\xe4\xb8")) { // ─ of 一 (E4 B8 80)
		if k, ok := msg.(KeyPressMsg); ok && k.Code == utf8.RuneError {
			t.Fatalf("the parser replaced a character that is still arriving: %q", k.Code)
		}
	}
	if !p.MidSequence() {
		t.Error("the parser is not holding the cut-short sequence; the next read would resolve it as text")
	}

	// And the byte that completes it arrives in the next read, whole.
	msgs := p.Parse([]byte{0x80})
	key, ok := msgs[len(msgs)-1].(KeyPressMsg)
	if !ok || key.Code != '一' {
		t.Fatalf("completing the sequence yielded %#v, want 一", msgs)
	}
}
