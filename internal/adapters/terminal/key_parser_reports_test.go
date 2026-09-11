package terminal

import (
	"strings"
	"testing"
)

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

// TestKeyParserAltChordsAndHeldControls is the other half of the contract, and
// the half that decides what the parser may not do: a byte behind `ESC` is read
// as a chord only where it cannot be the start of a reply.
//
// `ESC <` is not an introducer — an SGR mouse report is `CSI <`, so its `<`
// arrives behind `ESC [` and parses as a parameter — so Alt+< is still Alt+<.
// The other five bytes are introducers, and an unterminated one is *held* for the
// next read rather than resolved to a chord: that is what keeps a reply a read
// boundary cut from typing its own body. Those five are therefore never
// Alt+<char>, which is a trade this program can afford — keys.go binds no Alt
// chord at all, so the chord on the other side of the trade is a key name no
// handler would ever read.
func TestKeyParserAltChordsAndHeldControls(t *testing.T) {
	t.Run("chords that are not introducers survive", func(t *testing.T) {
		tests := []struct {
			in   string
			want []string
		}{
			{"\x1b<a", []string{"alt+<", "a"}},
			{"\x1ba", []string{"alt+a"}},
			{"\x1b\x1b[A", []string{"alt+up"}},
		}
		for _, tt := range tests {
			var p InputParser
			msgs := p.Parse([]byte(tt.in))
			if len(msgs) != len(tt.want) {
				t.Fatalf("Parse(%q) = %d messages %#v, want %v", tt.in, len(msgs), msgs, tt.want)
			}
			for i, m := range msgs {
				if got := m.(KeyPressMsg).String(); got != tt.want[i] {
					t.Errorf("Parse(%q)[%d] = %q, want %q", tt.in, i, got, tt.want[i])
				}
			}
			if p.HasPending() {
				t.Errorf("Parse(%q) left bytes pending", tt.in)
			}
		}
	})

	t.Run("introducers are held until their terminator", func(t *testing.T) {
		// The head alone, then the body and its terminator in the next read —
		// the shape a boundary produces. Nothing may be delivered either way,
		// and nothing may be left pending once the control has been closed.
		tests := []struct {
			name string
			head string
			tail string
		}{
			{"osc bel", "\x1b]52;c;", "aGVsbG8=\x07"},
			{"osc st", "\x1b]0;title", "\x1b\\"},
			{"dcs", "\x1bP1+r6d", "6978\x1b\\"},
			{"sos", "\x1bXpay", "load\x1b\\"},
			{"pm", "\x1b^pay", "load\x1b\\"},
			{"apc", "\x1b_pay", "load\x1b\\"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				var p InputParser
				if msgs := p.Parse([]byte(tt.head)); len(msgs) != 0 {
					t.Errorf("Parse(%q) produced %#v, want the control held as incomplete", escBytes(tt.head), msgs)
				}
				if !p.HasPending() {
					t.Errorf("Parse(%q) resolved the head instead of holding it; its body would arrive as typing", escBytes(tt.head))
				}
				if msgs := p.Parse([]byte(tt.tail)); len(msgs) != 0 {
					t.Errorf("then Parse(%q) produced %#v, want the reply consumed whole", escBytes(tt.tail), msgs)
				}
				if p.HasPending() {
					t.Errorf("the closed control left %q pending", p.pending)
				}
			})
		}
	})

	t.Run("a control that never terminates is dropped, not typed", func(t *testing.T) {
		var p InputParser
		if msgs := p.Parse([]byte("\x1b]11;rgb:1e1e/1e1e")); len(msgs) != 0 {
			t.Fatalf("an unterminated reply produced %#v", msgs)
		}
		if msgs := p.Flush(); len(msgs) != 0 {
			t.Errorf("Flush resolved the reply into %#v, want it dropped", msgs)
		}
		if p.HasPending() {
			t.Error("Flush left the dropped reply pending")
		}
		// And what the user types afterwards is a key, not a reply's tail.
		if msgs := p.Parse([]byte("k")); len(msgs) != 1 || msgs[0].(KeyPressMsg).String() != "k" {
			t.Errorf("after the drop, %q arrived as %#v, want the key k", "k", msgs)
		}
	})
}

// escBytes names control bytes in a failure line.
func escBytes(s string) string {
	return strings.NewReplacer("\x1b", "E", "\x07", "BEL").Replace(s)
}
