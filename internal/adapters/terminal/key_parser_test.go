package terminal

import (
	"testing"
)

// TestKeyParserC0 verifies C0 control byte decoding, matching bubbletea's
// reported key strings ("ctrl+a", "enter", "tab", "esc", "backspace", ...).
func TestKeyParserC0(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want string
	}{
		{"ctrl+space", []byte{0x00}, "ctrl+space"},
		{"ctrl+a", []byte{0x01}, "ctrl+a"},
		{"ctrl+c", []byte{0x03}, "ctrl+c"},
		{"ctrl+z", []byte{0x1a}, "ctrl+z"},
		{"esc", []byte{0x1b}, "esc"}, // resolved via Flush (escape timeout)
		{"ctrl+backslash", []byte{0x1c}, "ctrl+\\"},
		{"ctrl+]", []byte{0x1d}, "ctrl+]"},
		{"ctrl+^", []byte{0x1e}, "ctrl+^"},
		{"ctrl+_", []byte{0x1f}, "ctrl+_"},
		{"space", []byte{0x20}, "space"},
		{"backspace", []byte{0x7f}, "backspace"},
		{"enter", []byte{0x0d}, "enter"},
		{"tab", []byte{0x09}, "tab"},
		{"ctrl+j", []byte{0x0a}, "ctrl+j"},
		{"ctrl+h", []byte{0x08}, "ctrl+h"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p InputParser
			msgs := p.Parse(tt.in)
			if tt.want == "esc" && len(msgs) == 0 && p.HasPending() {
				// Lone ESC is resolved by Flush (the program does this
				// after the escape-sequence timeout).
				msgs = p.Flush()
			}
			if len(msgs) != 1 {
				t.Fatalf("expected 1 msg, got %d: %#v", len(msgs), msgs)
			}
			km, ok := msgs[0].(KeyPressMsg)
			if !ok {
				t.Fatalf("expected KeyPressMsg, got %T", msgs[0])
			}
			if got := km.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestKeyParserPrintables verifies printable rune decoding (letters, digits,
// uppercase from Shift, UTF-8).
func TestKeyParserPrintables(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"a", "a", "a"},
		{"H (shift+h)", "H", "H"},
		{"colon", ":", ":"},
		{"digit", "5", "5"},
		{"utf8", "中", "中"},
		{"emoji", "🚀", "🚀"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p InputParser
			msgs := p.Parse([]byte(tt.in))
			if len(msgs) != 1 {
				t.Fatalf("expected 1 msg, got %d", len(msgs))
			}
			km, ok := msgs[0].(KeyPressMsg)
			if !ok {
				t.Fatalf("expected KeyPressMsg, got %T", msgs[0])
			}
			if got := km.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestKeyParserSequences verifies escape sequences, mirroring uv's key table
// (VT100/CSI/SS3/XTerm modifiers/URxvt).
func TestKeyParserSequences(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"up", "\x1b[A", "up"},
		{"down", "\x1b[B", "down"},
		{"right", "\x1b[C", "right"},
		{"left", "\x1b[D", "left"},
		{"home", "\x1b[H", "home"},
		{"end", "\x1b[F", "end"},
		{"ss3 up", "\x1bOA", "up"},
		{"ss3 home", "\x1bOH", "home"},
		{"home ~1", "\x1b[1~", "home"},
		{"insert", "\x1b[2~", "insert"},
		{"delete", "\x1b[3~", "delete"},
		{"end ~4", "\x1b[4~", "end"},
		{"pgup", "\x1b[5~", "pgup"},
		{"pgdown", "\x1b[6~", "pgdown"},
		{"home ~7", "\x1b[7~", "home"},
		{"end ~8", "\x1b[8~", "end"},
		{"shift+tab", "\x1b[Z", "shift+tab"},
		{"f1", "\x1bOP", "f1"},
		{"f2", "\x1bOQ", "f2"},
		{"f3", "\x1bOR", "f3"},
		{"f4", "\x1bOS", "f4"},
		{"f1 tilde", "\x1b[11~", "f1"},
		{"f5", "\x1b[15~", "f5"},
		{"f6", "\x1b[17~", "f6"},
		{"f7", "\x1b[18~", "f7"},
		{"f8", "\x1b[19~", "f8"},
		{"f9", "\x1b[20~", "f9"},
		{"f10", "\x1b[21~", "f10"},
		{"f11", "\x1b[23~", "f11"},
		{"f12", "\x1b[24~", "f12"},
		{"shift+up xterm", "\x1b[1;2A", "shift+up"},
		{"shift+down xterm", "\x1b[1;2B", "shift+down"},
		{"ctrl+up xterm", "\x1b[1;5A", "ctrl+up"},
		{"shift+pgup xterm", "\x1b[5;2~", "shift+pgup"},
		{"shift+up urxvt", "\x1b[a", "shift+up"},
		{"shift+down urxvt", "\x1b[b", "shift+down"},
		{"shift+right urxvt", "\x1b[c", "shift+right"},
		{"shift+left urxvt", "\x1b[d", "shift+left"},
		{"ctrl+up urxvt", "\x1bOa", "ctrl+up"},
		{"ctrl+down urxvt", "\x1bOb", "ctrl+down"},
		{"alt+a", "\x1ba", "alt+a"},
		{"alt+up", "\x1b\x1b[A", "alt+up"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p InputParser
			msgs := p.Parse([]byte(tt.in))
			if len(msgs) != 1 {
				t.Fatalf("expected 1 msg, got %d (%#v)", len(msgs), msgs)
			}
			km, ok := msgs[0].(KeyPressMsg)
			if !ok {
				t.Fatalf("expected KeyPressMsg, got %T", msgs[0])
			}
			if got := km.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestKeyParserMultiKeys verifies a stream of keys in one chunk.
func TestKeyParserMultiKeys(t *testing.T) {
	var p InputParser
	msgs := p.Parse([]byte("jk\x1b[A\x1b[B:q\x1b[1;2A"))
	wants := []string{"j", "k", "up", "down", ":", "q", "shift+up"}
	if len(msgs) != len(wants) {
		t.Fatalf("expected %d msgs, got %d: %#v", len(wants), len(msgs), msgs)
	}
	for i, want := range wants {
		km := msgs[i].(KeyPressMsg)
		if got := km.String(); got != want {
			t.Errorf("msg[%d] = %q, want %q", i, got, want)
		}
	}
}

// TestKeyParserIncomplete verifies incomplete sequences are retained across
// Parse calls.
func TestKeyParserIncomplete(t *testing.T) {
	var p InputParser
	// Lone ESC: incomplete — no message yet.
	msgs := p.Parse([]byte{0x1b})
	if len(msgs) != 0 {
		t.Fatalf("lone ESC should be retained, got %#v", msgs)
	}
	if !p.HasPending() {
		t.Fatal("expected pending bytes")
	}
	// Complete the sequence: ESC [ A → up.
	msgs = p.Parse([]byte("[A"))
	if len(msgs) != 1 || msgs[0].(KeyPressMsg).String() != "up" {
		t.Fatalf("expected up, got %#v", msgs)
	}
	if p.HasPending() {
		t.Fatal("expected no pending bytes after completion")
	}
}

// TestKeyParserFlushEsc verifies Flush resolves a lone ESC as the Escape key.
func TestKeyParserFlushEsc(t *testing.T) {
	var p InputParser
	p.Parse([]byte{0x1b})
	msgs := p.Flush()
	if len(msgs) != 1 || msgs[0].(KeyPressMsg).String() != "esc" {
		t.Fatalf("expected esc, got %#v", msgs)
	}
}

// TestKeyParserPaste verifies bracketed paste passthrough.
func TestKeyParserPaste(t *testing.T) {
	var p InputParser
	msgs := p.Parse([]byte("\x1b[200~hello \x1b[A world\x1b[201~"))
	if len(msgs) != 3 {
		t.Fatalf("expected 3 msgs (start, paste, end), got %d: %#v", len(msgs), msgs)
	}
	if _, ok := msgs[0].(PasteStartMsg); !ok {
		t.Errorf("msg[0] = %T, want PasteStartMsg", msgs[0])
	}
	pm, ok := msgs[1].(PasteMsg)
	if !ok {
		t.Fatalf("msg[1] = %T, want PasteMsg", msgs[1])
	}
	// Escape sequences inside paste content must pass through verbatim.
	if pm.Content != "hello \x1b[A world" {
		t.Errorf("paste content = %q, want verbatim bytes", pm.Content)
	}
	if _, ok := msgs[2].(PasteEndMsg); !ok {
		t.Errorf("msg[2] = %T, want PasteEndMsg", msgs[2])
	}
}

// TestKeyParserPasteSplit verifies paste spanning multiple reads.
func TestKeyParserPasteSplit(t *testing.T) {
	var p InputParser
	msgs := p.Parse([]byte("\x1b[200~part1"))
	if len(msgs) != 1 {
		t.Fatalf("expected PasteStartMsg, got %#v", msgs)
	}
	msgs = p.Parse([]byte(" part2\x1b[201~after"))
	if len(msgs) != 7 {
		t.Fatalf("expected paste+end+5 runes, got %d: %#v", len(msgs), msgs)
	}
	pm := msgs[0].(PasteMsg)
	if pm.Content != "part1 part2" {
		t.Errorf("paste content = %q", pm.Content)
	}
	if _, ok := msgs[1].(PasteEndMsg); !ok {
		t.Fatalf("msgs[1] = %T, want PasteEndMsg", msgs[1])
	}
	km := msgs[2].(KeyPressMsg)
	if km.String() != "a" {
		t.Errorf("after paste: %q, want a", km.String())
	}
}

// TestKeyParserUnknownDropped verifies unknown sequences are dropped.
func TestKeyParserUnknownDropped(t *testing.T) {
	var p InputParser
	msgs := p.Parse([]byte("\x1b[999~ab"))
	if len(msgs) != 2 {
		t.Fatalf("expected 2 msgs (unknown dropped), got %#v", msgs)
	}
	if msgs[0].(KeyPressMsg).String() != "a" || msgs[1].(KeyPressMsg).String() != "b" {
		t.Errorf("unexpected msgs: %#v", msgs)
	}
}

// TestKeyString verifies String()/Keystroke() parity with bubbletea formats.
func TestKeyString(t *testing.T) {
	tests := []struct {
		key  Key
		want string
	}{
		{Key{Code: 'a'}, "a"},
		{Key{Code: 'A'}, "A"},
		{Key{Code: 'a', Mod: ModCtrl}, "ctrl+a"},
		{Key{Code: 'a', Mod: ModAlt}, "alt+a"},
		{Key{Code: KeyUp, Mod: ModShift}, "shift+up"},
		{Key{Code: KeyUp, Mod: ModShift | ModCtrl}, "ctrl+shift+up"},
		{Key{Code: KeyEnter}, "enter"},
		{Key{Code: KeySpace}, "space"},
		{Key{Code: KeySpace, Text: " "}, "space"},
		{Key{Code: KeyEscape}, "esc"},
		{Key{Code: KeyBackspace}, "backspace"},
		{Key{Code: KeyF1}, "f1"},
		{Key{Code: KeyPgDown}, "pgdown"},
		// Shifted printable: Text wins over keystroke.
		{Key{Code: 'h', Mod: ModShift, Text: "H"}, "H"},
		{Key{Code: '1', Mod: ModShift, Text: "!"}, "!"},
	}
	for _, tt := range tests {
		if got := tt.key.String(); got != tt.want {
			t.Errorf("Key%+v.String() = %q, want %q", tt.key, got, tt.want)
		}
	}
}
