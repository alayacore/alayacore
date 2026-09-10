package terminal

import "testing"

// Terminal replies the program can receive without having asked for them must
// be consumed whole and never delivered as keys. These tests pin that by the
// observable outcome — how many messages come out — because the failure mode is
// exactly that a reply's bytes leak out as typing, and a message count is what a
// prompt would notice.

// TestKeyParserDropsMouseReports pins both encodings a mouse report takes — SGR
// and the older X10 form — arriving whole. A report that a read boundary cuts in
// half is program_input_cut_test.go's subject.
//
// A mouse report is `CSI < Cb;Cx;Cy M` (SGR) or `CSI M` plus three raw bytes
// (X10) — both introduced by `ESC [`, which is why the parser's CSI path is what
// consumes the first and why the second needs the explicit three bytes: `CSI M`
// looks complete, and its coordinates would otherwise be typed.
//
// X10 is worth three rows of its own because its coordinate bytes are arbitrary:
// when the last one is a byte parseCSI knows as a final (A, D, F — the arrows
// and End), a length check that is one byte out turns the report into a key
// nobody pressed. `\x1b[M !A` was `up` before that check was moved onto the
// whole sequence.
func TestKeyParserDropsMouseReports(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"sgr press", "\x1b[<35;60;10M"},
		{"sgr release", "\x1b[<35;60;10m"},
		{"sgr burst", "\x1b[<35;135;1M\x1b[<35;176;1M\x1b[<35;213;1M"},
		{"urxvt mouse", "\x1b[35;60;10M"},
		{"x10 mouse", "\x1b[M !#"},
		{"x10 mouse ending in a CSI final (up)", "\x1b[M !A"},
		{"x10 mouse ending in a CSI final (left)", "\x1b[M  D"},
		{"x10 mouse ending in a CSI final (end)", "\x1b[M  F"},
		// The guard sits on the whole sequence, so the nested `ESC ESC …` path
		// (escapeKey recursing through its own entry point) is covered too;
		// without that, this row is `alt+up` — a key nobody pressed.
		{"x10 mouse behind a nested ESC", "\x1b\x1b[M !A"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p InputParser
			if msgs := p.Parse([]byte(tt.in)); len(msgs) != 0 {
				t.Errorf("Parse(%q) produced %#v, want nothing (a mouse report is not typing)", tt.in, msgs)
			}
			if p.HasPending() {
				t.Errorf("Parse(%q) left bytes pending", tt.in)
			}
		})
	}
}

// TestKeyParserDropsStringControls pins OSC/DCS/SOS/PM/APC, whose bodies are
// terminal replies (a color, a capability, a clipboard read) and are exactly
// the text that must never reach the prompt.
func TestKeyParserDropsStringControls(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"osc bel", "\x1b]0;window title\x07"},
		{"osc 11 bg color", "\x1b]11;rgb:1e1e/1e1e/1e1e\x07"},
		{"osc 52 clipboard read", "\x1b]52;c;aGVsbG8=\x07"},
		{"osc st", "\x1b]0;title\x1b\\"},
		{"dcs xtgettcap", "\x1bP1+r6d6978\x1b\\"},
		{"dcs decrqss", "\x1bP1$r0m\x1b\\"},
		{"sos", "\x1bXpayload\x1b\\"},
		{"pm", "\x1b^payload\x1b\\"},
		{"apc", "\x1b_payload\x1b\\"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p InputParser
			if msgs := p.Parse([]byte(tt.in)); len(msgs) != 0 {
				t.Errorf("Parse(%q) produced %#v, want nothing (a reply is not typing)", tt.in, msgs)
			}
		})
	}
}

// TestKeyParserAltChordsSurvive is the other half of the contract: consuming a
// reply must not cost the chord that shares its introducer. Alt+<, Alt+] and
// Alt+P are `ESC <`, `ESC ]` and `ESC P` — the same bytes the replies start
// with — and are only recognized as replies when what follows is one.
func TestKeyParserAltChordsSurvive(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"alt+< then letter", "\x1b<a", []string{"alt+<", "a"}},
		{"alt+] then letters", "\x1b]abc", []string{"alt+]", "a", "b", "c"}},
		{"alt+P then digits", "\x1bP12", []string{"alt+P", "1", "2"}},
		{"alt+X then letters", "\x1bXy", []string{"alt+X", "y"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p InputParser
			var got []string
			for _, m := range p.Parse([]byte(tt.in)) {
				got = append(got, m.(KeyPressMsg).String())
			}
			if len(got) != len(tt.want) {
				t.Fatalf("Parse(%q) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("Parse(%q)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
				}
			}
		})
	}
}
