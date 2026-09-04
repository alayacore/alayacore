package terminal

// Tests for the Windows console event layer (console_events.go): the record
// layout the Win32 API fills in, and the byte stream this program reads keys from.
//
// The layout is the part that cannot be debugged remotely. INPUT_RECORD is a
// C struct with a union in it, and a Go type that is the right size but has the
// members at the wrong offsets would not fail — it would mis-read every key, on
// one platform, in the field. So the size and offsets are asserted directly, and
// the decoder is asserted against bytes written the way the API would write them,
// which is the only way to know that the two agree.
//
// The encoder is asserted twice over, because it has two promises to keep. On its
// own it must produce the byte forms xterm defines; and end to end, those bytes
// must survive the real parser and come out as the key strings the application
// binds (keys.go) — which is the property that makes a Windows keystroke mean
// what the same keystroke means on Unix.
//
// Like the file it covers, this test builds for every GOOS: nothing in it needs a
// console, so nothing about it should depend on the Windows CI job to be believed.

import (
	"encoding/binary"
	"testing"
	"unicode/utf16"
	"unsafe"
)

// keyRecordOf writes a key event into the INPUT_RECORD layout, the way the
// console API would.
func keyRecordOf(k keyEvent) inputRecord {
	var rec inputRecord
	rec.kind = eventKey
	var body uint32
	if k.down {
		body = 1 // BOOL is 4 bytes, true being non-zero
	}
	binary.LittleEndian.PutUint32(rec.body[0:4], body)
	binary.LittleEndian.PutUint16(rec.body[4:6], k.repeat)
	binary.LittleEndian.PutUint16(rec.body[6:8], k.virtualKey)
	binary.LittleEndian.PutUint16(rec.body[8:10], 0) // scan code: not read back
	binary.LittleEndian.PutUint16(rec.body[10:12], k.char)
	binary.LittleEndian.PutUint32(rec.body[12:16], k.ctrlState)
	return rec
}

// TestInputRecordLayout pins the ABI: 20 bytes (2 for the kind, 2 of padding, 16
// for the union — which is MOUSE_EVENT_RECORD's size, the largest member), with
// the union starting where the decoder assumes it does.
func TestInputRecordLayout(t *testing.T) {
	if got := unsafe.Sizeof(inputRecord{}); got != 20 {
		t.Errorf("sizeof(inputRecord) = %d, want 20 (INPUT_RECORD)", got)
	}
	if got := unsafe.Offsetof(inputRecord{}.body); got != 4 {
		t.Errorf("offsetof(union) = %d, want 4 (WORD kind + padding to a DWORD)", got)
	}
}

// TestKeyEventRoundTrip reads back every member from the offsets the decoder
// claims, so a shifted field shows up as a failed assertion rather than as a
// keyboard that types the wrong things.
func TestKeyEventRoundTrip(t *testing.T) {
	want := keyEvent{
		down:       true,
		repeat:     3,
		virtualKey: vkUp,
		char:       'x',
		ctrlState:  ctrlShift | ctrlLeftCtrl,
	}
	got := keyRecordOf(want).key()
	if got != want {
		t.Errorf("key event decoded as %+v, want %+v", got, want)
	}
}

// keyEventDeclared is KEY_EVENT_RECORD written the way the header declares it —
// BOOL, WORD, WORD, WORD, WORD, DWORD, in that order. Go's alignment puts those
// members at the same offsets C does (4+2+2+2+2+4 = 16, aligned to 4), so this
// type is an independent statement of the layout, and the decoder's hand-written
// offsets can be checked against it.
type keyEventDeclared struct {
	down       uint32
	repeat     uint16
	virtualKey uint16
	scanCode   uint16
	char       uint16
	ctrlState  uint32
}

// TestKeyEventDecoderMatchesTheDeclaredLayout is the guard against the one failure
// mode of hand-written byte offsets: they stay plausible while being wrong. The
// union's size is asserted too, because INPUT_RECORD's 16-byte union is what makes
// the record 20 bytes, and a decoder that read past the end of it would be reading
// the following record's kind.
func TestKeyEventDecoderMatchesTheDeclaredLayout(t *testing.T) {
	declared := keyEventDeclared{down: 1, repeat: 2, virtualKey: vkDown, scanCode: 0x1C, char: 0x4E2D, ctrlState: ctrlShift | ctrlLeftAlt}
	if got := unsafe.Sizeof(declared); got != 16 {
		t.Fatalf("sizeof(KEY_EVENT_RECORD) = %d, want 16", got)
	}
	if got := unsafe.Offsetof(declared.char); got != 10 {
		t.Fatalf("offsetof(uChar.AsciiChar) = %d, want 10", got)
	}

	var rec inputRecord
	rec.kind = eventKey
	copy(rec.body[:], unsafe.Slice((*byte)(unsafe.Pointer(&declared)), len(rec.body)))

	want := keyEvent{down: true, repeat: 2, virtualKey: vkDown, char: 0x4E2D, ctrlState: ctrlShift | ctrlLeftAlt}
	if got := rec.key(); got != want {
		t.Errorf("decoded %+v from the declared layout, want %+v", got, want)
	}
}

// TestKeyEventUpIsNotInput: the buffer carries releases as well as presses, and a
// release has no byte form — reading it as a key would double every keystroke.
func TestKeyEventUpIsNotInput(t *testing.T) {
	rec := keyRecordOf(keyEvent{down: false, virtualKey: vkReturn, char: 0x0d})
	var enc keyEncoder
	if got := enc.append(nil, rec); len(got) != 0 {
		t.Errorf("key-up produced %q, want nothing", got)
	}
}

func TestEncoderSequences(t *testing.T) {
	tests := []struct {
		name string
		key  keyEvent
		want string
	}{
		{"letter", keyEvent{down: true, virtualKey: 'A', char: 'a'}, "a"},
		{"shifted letter", keyEvent{down: true, virtualKey: 'A', char: 'A', ctrlState: ctrlShift}, "A"},
		{"space", keyEvent{down: true, virtualKey: vkSpace, char: ' '}, " "},
		{"digit", keyEvent{down: true, virtualKey: '7', char: '7'}, "7"},
		{"punctuation", keyEvent{down: true, virtualKey: 0xBF, char: '/'}, "/"}, // VK_OEM_2
		// A CJK commit arrives as the character with no key of its own:
		// 0xDE is VK_NONAME, which is what the console reports for one.
		{"upper unicode", keyEvent{down: true, virtualKey: 0xDE, char: 0x4E2D}, "中"},

		// Ctrl is already folded into the character by the console: Ctrl+A
		// arrives as 0x01, which is what a terminal sends and what the parser
		// reads as ctrl+a.
		{"ctrl+letter", keyEvent{down: true, virtualKey: 'A', char: 0x01, ctrlState: ctrlLeftCtrl}, "\x01"},
		{"ctrl+j", keyEvent{down: true, virtualKey: 'J', char: 0x0a, ctrlState: ctrlRightCtrl}, "\n"},
		// The console normally reports Ctrl+letter as the control code in the
		// character field. When it does not — an IME state that swallows it, a
		// key injected without one — the code is derived from the key, which is
		// what a terminal sends and what the binding names.
		{"ctrl+letter with no character", keyEvent{down: true, virtualKey: 'L', ctrlState: ctrlLeftCtrl}, "\x0c"},

		{"alt+letter", keyEvent{down: true, virtualKey: 'B', char: 'b', ctrlState: ctrlLeftAlt}, "\x1bb"},
		// AltGr is Ctrl+Alt, and on the layouts that have it that is how a
		// character is typed: the console has already produced the character, so
		// this must not arrive as an Alt chord.
		{"altgr is a character", keyEvent{down: true, virtualKey: 'Q', char: '@', ctrlState: ctrlLeftCtrl | ctrlRightAlt}, "@"},

		{"up", keyEvent{down: true, virtualKey: vkUp}, "\x1b[A"},
		{"down", keyEvent{down: true, virtualKey: vkDown}, "\x1b[B"},
		{"right", keyEvent{down: true, virtualKey: vkRight}, "\x1b[C"},
		{"left", keyEvent{down: true, virtualKey: vkLeft}, "\x1b[D"},
		{"shift+up", keyEvent{down: true, virtualKey: vkUp, ctrlState: ctrlShift}, "\x1b[1;2A"},
		{"ctrl+right", keyEvent{down: true, virtualKey: vkRight, ctrlState: ctrlLeftCtrl}, "\x1b[1;5C"},
		{"ctrl+shift+left", keyEvent{down: true, virtualKey: vkLeft, ctrlState: ctrlShift | ctrlRightCtrl}, "\x1b[1;6D"},
		{"alt+down", keyEvent{down: true, virtualKey: vkDown, ctrlState: ctrlLeftAlt}, "\x1b[1;3B"},
		{"home", keyEvent{down: true, virtualKey: vkHome}, "\x1b[H"},
		{"end", keyEvent{down: true, virtualKey: vkEnd}, "\x1b[F"},
		{"ctrl+home", keyEvent{down: true, virtualKey: vkHome, ctrlState: ctrlLeftCtrl}, "\x1b[1;5H"},
		{"pgup", keyEvent{down: true, virtualKey: vkPrior}, "\x1b[5~"},
		{"pgdown", keyEvent{down: true, virtualKey: vkNext}, "\x1b[6~"},
		{"insert", keyEvent{down: true, virtualKey: vkInsert}, "\x1b[2~"},
		{"delete", keyEvent{down: true, virtualKey: vkDelete}, "\x1b[3~"},
		{"shift+delete", keyEvent{down: true, virtualKey: vkDelete, ctrlState: ctrlShift}, "\x1b[3;2~"},

		{"f1", keyEvent{down: true, virtualKey: vkF1}, "\x1bOP"},
		{"f2", keyEvent{down: true, virtualKey: vkF2}, "\x1bOQ"},
		{"ctrl+f1", keyEvent{down: true, virtualKey: vkF1, ctrlState: ctrlLeftCtrl}, "\x1b[1;5P"},
		{"f5", keyEvent{down: true, virtualKey: vkF5}, "\x1b[15~"},
		{"f6", keyEvent{down: true, virtualKey: vkF6}, "\x1b[17~"},
		{"f11", keyEvent{down: true, virtualKey: vkF11}, "\x1b[23~"},
		{"f12", keyEvent{down: true, virtualKey: vkF12}, "\x1b[24~"},

		{"tab", keyEvent{down: true, virtualKey: vkTab, char: 0x09}, "\t"},
		{"shift+tab", keyEvent{down: true, virtualKey: vkTab, char: 0x09, ctrlState: ctrlShift}, "\x1b[Z"},

		{"enter", keyEvent{down: true, virtualKey: vkReturn, char: 0x0d}, "\r"},
		// Ctrl+Enter reaches the prompt as LF, which is its line break — the
		// same byte Ctrl+J produces, and the same answer a Unix terminal gives.
		{"ctrl+enter", keyEvent{down: true, virtualKey: vkReturn, char: 0x0a, ctrlState: ctrlLeftCtrl}, "\n"},
		{"enter without a character", keyEvent{down: true, virtualKey: vkReturn}, "\r"},

		{"escape", keyEvent{down: true, virtualKey: vkEscape, char: 0x1b}, "\x1b"},
		{"escape without a character", keyEvent{down: true, virtualKey: vkEscape}, "\x1b"},
		{"alt+escape", keyEvent{down: true, virtualKey: vkEscape, char: 0x1b, ctrlState: ctrlLeftAlt}, "\x1b\x1b"},

		// Deliberately not the console's character for this key (0x08): see
		// the note in specialKeyBytes. What comes out has to be what the
		// input field binds as "backspace".
		{"backspace", keyEvent{down: true, virtualKey: vkBack, char: 0x08}, "\x7f"},
		{"alt+backspace", keyEvent{down: true, virtualKey: vkBack, char: 0x08, ctrlState: ctrlLeftAlt}, "\x1b\x7f"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var enc keyEncoder
			got := string(enc.append(nil, keyRecordOf(tt.key)))
			if got != tt.want {
				t.Errorf("encoded as %q, want %q", got, tt.want)
			}
		})
	}
}

// TestEncoderDropsKeysWithNoForm: an event contributes the sequence for its key
// code, or the character it typed, or nothing — which is also the order the
// encoder asks the questions in. A lone Shift press, a media key, and an IME
// composition marker all fall through both tests, and the reason they must be
// silent is that their *character* is absent, not that their key code is
// recognized as a modifier: VK_PROCESSKEY is the same code an IME commit arrives
// with, and dropping by code alone would delete the text a CJK user typed (see
// TestEncoderIMECommit).
func TestEncoderDropsKeysWithNoForm(t *testing.T) {
	dropped := []struct {
		name string
		key  keyEvent
	}{
		{"shift press", keyEvent{down: true, virtualKey: vkShift}},
		{"ctrl press", keyEvent{down: true, virtualKey: vkLCtrl}},
		{"alt press", keyEvent{down: true, virtualKey: vkRMenu}},
		{"caps lock", keyEvent{down: true, virtualKey: vkCapital}},
		{"ime composition", keyEvent{down: true, virtualKey: vkProcess}},
		{"media key", keyEvent{down: true, virtualKey: 0xB3}}, // VK_MEDIA_NEXT_TRACK
		{"f13 (no form here)", keyEvent{down: true, virtualKey: vkF12 + 1}},
		{"windows key", keyEvent{down: true, virtualKey: 0x5B}}, // VK_LWIN
	}
	for _, tt := range dropped {
		t.Run(tt.name, func(t *testing.T) {
			var enc keyEncoder
			if got := enc.append(nil, keyRecordOf(tt.key)); len(got) != 0 {
				t.Errorf("got %q, want nothing", got)
			}
		})
	}
}

// TestEncoderIMECommit: the case the ordering of those two questions exists for.
// Windows delivers text committed by an IME — Chinese, Japanese, Korean, and the
// dead-key accented letters of many European layouts — as a key event whose code
// is the IME marker and whose *character* is the result. A committed ideograph is
// also a surrogate pair, so both halves have to survive.
func TestEncoderIMECommit(t *testing.T) {
	var enc keyEncoder
	got := string(enc.append(nil, keyRecordOf(keyEvent{down: true, virtualKey: vkProcess, char: 0x4E2D})))
	if got != "中" {
		t.Fatalf("an IME commit encoded as %q, want %q", got, "中")
	}

	high, low := utf16.EncodeRune('🀄') // a CJK-extension tile, beyond the BMP
	half := enc.append(nil, keyRecordOf(keyEvent{down: true, virtualKey: vkProcess, char: uint16(high)}))
	if len(half) != 0 {
		t.Errorf("the first half of a committed pair encoded as %q, want it held", half)
	}
	whole := string(enc.append(nil, keyRecordOf(keyEvent{down: true, virtualKey: vkProcess, char: uint16(low)})))
	if whole != "🀄" {
		t.Errorf("the committed pair encoded as %q, want %q", whole, "🀄")
	}
}

// TestEncoderNonKeyEvents: focus is the one non-key event with a byte form this
// program reads; the rest have none, and reading them must not fabricate input.
func TestEncoderNonKeyEvents(t *testing.T) {
	var enc keyEncoder

	focusOn := inputRecord{kind: eventFocus}
	binary.LittleEndian.PutUint32(focusOn.body[0:4], 1)
	if got := string(enc.append(nil, focusOn)); got != focusInSeq {
		t.Errorf("focus-on = %q, want %q", got, focusInSeq)
	}
	focusOff := inputRecord{kind: eventFocus}
	if got := string(enc.append(nil, focusOff)); got != focusOutSeq {
		t.Errorf("focus-off = %q, want %q", got, focusOutSeq)
	}

	for _, kind := range []uint16{eventMouse, eventWindowBuffer, eventMenu} {
		rec := inputRecord{kind: kind}
		binary.LittleEndian.PutUint32(rec.body[0:4], 0x7f) // would read as DEL if mis-typed
		if got := enc.append(nil, rec); len(got) != 0 {
			t.Errorf("event kind %#04x produced %q, want nothing", kind, got)
		}
	}
}

// TestEncoderRepeatCount: a held key can arrive as one event with a count, and the
// terminal's byte stream would carry the key that many times.
func TestEncoderRepeatCount(t *testing.T) {
	var enc keyEncoder
	got := string(enc.append(nil, keyRecordOf(keyEvent{down: true, virtualKey: 'A', char: 'a', repeat: 4})))
	if got != "aaaa" {
		t.Errorf("repeat 4 encoded as %q, want %q", got, "aaaa")
	}
	// A record with no count is still one key press, not none.
	got = string(enc.append(nil, keyRecordOf(keyEvent{down: true, virtualKey: vkUp})))
	if got != "\x1b[A" {
		t.Errorf("repeat 0 encoded as %q, want one arrow", got)
	}
}

// TestEncoderSurrogatePair: an astral character — an emoji, a CJK extension-B
// ideograph — arrives as two events, and the first is not a character.
func TestEncoderSurrogatePair(t *testing.T) {
	high, low := utf16.EncodeRune('😀')
	var enc keyEncoder
	first := enc.append(nil, keyRecordOf(keyEvent{down: true, virtualKey: 0xDE, char: uint16(high)}))
	if len(first) != 0 {
		t.Fatalf("the high half encoded as %q, want it held back", first)
	}
	second := enc.append(nil, keyRecordOf(keyEvent{down: true, virtualKey: 0xDF, char: uint16(low)}))
	if string(second) != "😀" {
		t.Errorf("the pair encoded as %q, want %q", second, "😀")
	}
}

// TestEncoderStrayLowSurrogate: a lone low half cannot be paired. Emitting the
// replacement character is what every UTF-8 decoder does with it, including Go's
// own console reader; dropping it would be worse, and emitting the raw code unit
// would be invalid UTF-8.
func TestEncoderStrayLowSurrogate(t *testing.T) {
	_, low := utf16.EncodeRune('😀')
	var enc keyEncoder
	got := string(enc.append(nil, keyRecordOf(keyEvent{down: true, virtualKey: 0xDF, char: uint16(low)})))
	if got != "\uFFFD" {
		t.Errorf("stray low surrogate encoded as %q, want %q", got, "\uFFFD")
	}
}

// TestEncodedKeysAreWhatTheParserReads is the end-to-end property: every key the
// application binds, sent through the encoder and then through the real parser,
// arrives as the same key string it arrives as on Unix. It is the reason the
// encoder's contract is "what a terminal would send" rather than "something that
// seems close enough".
func TestEncodedKeysAreWhatTheParserReads(t *testing.T) {
	tests := []struct {
		name string
		key  keyEvent
		want string
	}{
		{"letters", keyEvent{down: true, virtualKey: 'A', char: 'a'}, "a"},
		{"shifted letters", keyEvent{down: true, virtualKey: 'J', char: 'J', ctrlState: ctrlShift}, "J"},
		{"space", keyEvent{down: true, virtualKey: vkSpace, char: ' '}, "space"},
		{"colon", keyEvent{down: true, virtualKey: 0xBA, char: ':'}, ":"},
		{"enter", keyEvent{down: true, virtualKey: vkReturn, char: 0x0d}, "enter"},
		{"backspace", keyEvent{down: true, virtualKey: vkBack, char: 0x08}, "backspace"},
		{"delete", keyEvent{down: true, virtualKey: vkDelete}, "delete"},
		{"tab", keyEvent{down: true, virtualKey: vkTab, char: 0x09}, "tab"},
		{"shift+tab", keyEvent{down: true, virtualKey: vkTab, char: 0x09, ctrlState: ctrlShift}, "shift+tab"},
		{"escape", keyEvent{down: true, virtualKey: vkEscape, char: 0x1b}, "esc"},
		{"f1", keyEvent{down: true, virtualKey: vkF1}, "f1"},
		{"up", keyEvent{down: true, virtualKey: vkUp}, "up"},
		{"down", keyEvent{down: true, virtualKey: vkDown}, "down"},
		{"left", keyEvent{down: true, virtualKey: vkLeft}, "left"},
		{"right", keyEvent{down: true, virtualKey: vkRight}, "right"},
		{"shift+up", keyEvent{down: true, virtualKey: vkUp, ctrlState: ctrlShift}, "shift+up"},
		{"shift+down", keyEvent{down: true, virtualKey: vkDown, ctrlState: ctrlShift}, "shift+down"},
		{"home", keyEvent{down: true, virtualKey: vkHome}, "home"},
		{"end", keyEvent{down: true, virtualKey: vkEnd}, "end"},
		{"pgup", keyEvent{down: true, virtualKey: vkPrior}, "pgup"},
		{"pgdown", keyEvent{down: true, virtualKey: vkNext}, "pgdown"},
		{"ctrl+a", keyEvent{down: true, virtualKey: 'A', char: 0x01, ctrlState: ctrlLeftCtrl}, "ctrl+a"},
		{"ctrl+c", keyEvent{down: true, virtualKey: 'C', char: 0x03, ctrlState: ctrlLeftCtrl}, "ctrl+c"},
		{"ctrl+d", keyEvent{down: true, virtualKey: 'D', char: 0x04, ctrlState: ctrlLeftCtrl}, "ctrl+d"},
		{"ctrl+j", keyEvent{down: true, virtualKey: 'J', char: 0x0a, ctrlState: ctrlLeftCtrl}, "ctrl+j"},
		{"ctrl+o", keyEvent{down: true, virtualKey: 'O', char: 0x0f, ctrlState: ctrlLeftCtrl}, "ctrl+o"},
		{"ctrl+r", keyEvent{down: true, virtualKey: 'R', char: 0x12, ctrlState: ctrlLeftCtrl}, "ctrl+r"},
		{"ctrl+z", keyEvent{down: true, virtualKey: 'Z', char: 0x1a, ctrlState: ctrlLeftCtrl}, "ctrl+z"},
		{"cjk", keyEvent{down: true, virtualKey: ' ', char: 0x4E2D}, "中"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var enc keyEncoder
			data := enc.append(nil, keyRecordOf(tt.key))
			got := parseAll(t, data)
			if len(got) != 1 {
				t.Fatalf("bytes %q produced %d messages (%v), want one key", data, len(got), got)
			}
			key, ok := got[0].(KeyMsg)
			if !ok {
				t.Fatalf("msg = %T, want KeyMsg", got[0])
			}
			if key.String() != tt.want {
				t.Errorf("parsed as %q, want %q", key.String(), tt.want)
			}
		})
	}
}

// TestEncodedEmojisAndPastes: a whole phrase — what a paste looks like on the
// console, where there is no bracketed-paste framing — must arrive as the same
// runes, in order, with the surrogate pairs joined and the control characters
// intact.
func TestEncodedEmojisAndPastes(t *testing.T) {
	phrase := "hello 世界 😀\r"
	var enc keyEncoder
	var data []byte
	for _, unit := range utf16.Encode([]rune(phrase)) {
		// VK_NONAME again: a paste or an IME commit arrives as characters
		// carrying no key of their own.
		data = enc.append(data, keyRecordOf(keyEvent{down: true, virtualKey: 0xDE, char: unit}))
	}

	// What the parser reports for each rune of the phrase: the two keys with a
	// name of their own, and every other character as itself.
	want := make([]string, 0, len(phrase))
	for _, r := range phrase {
		switch r {
		case ' ':
			want = append(want, "space")
		case '\r':
			want = append(want, "enter")
		default:
			want = append(want, string(r))
		}
	}
	got := parseAll(t, data)
	if len(got) != len(want) {
		t.Fatalf("%q produced %d messages, want %d", data, len(got), len(want))
	}
	for i, msg := range got {
		key, ok := msg.(KeyMsg)
		if !ok {
			t.Fatalf("msg %d = %T, want KeyMsg", i, msg)
		}
		if key.String() != want[i] {
			t.Errorf("msg %d = %q, want %q", i, key.String(), want[i])
		}
	}
}

// TestEncodedBracketedPasteSurvivesTheParser: a terminal that implements
// bracketed paste (DECSET 2004, which Screen.Start enables) delivers clipboard
// content as characters between the two markers, and a pseudo console hands those
// same characters to this program as key events. Encoding them back must reproduce
// the framing exactly, or a paste on Windows Terminal would arrive as keystrokes
// and the CRs inside it would submit.
func TestEncodedBracketedPasteSurvivesTheParser(t *testing.T) {
	const pasted = "\x1b[200~two\r\nlines\x1b[201~"
	var enc keyEncoder
	var data []byte
	for _, unit := range utf16.Encode([]rune(pasted)) {
		data = enc.append(data, keyRecordOf(keyEvent{down: true, virtualKey: 0xDE, char: unit}))
	}

	msgs := parseAll(t, data)
	if len(msgs) != 1 {
		t.Fatalf("paste produced %d messages (%v), want one PasteMsg", len(msgs), msgs)
	}
	paste, ok := msgs[0].(PasteMsg)
	if !ok {
		t.Fatalf("msg = %T, want PasteMsg", msgs[0])
	}
	if paste.Content != "two\r\nlines" {
		t.Errorf("paste content = %q, want %q", paste.Content, "two\r\nlines")
	}
}

// parseAll runs data through the parser the way the input loop does, including
// the flush that resolves a trailing incomplete sequence.
func parseAll(t *testing.T, data []byte) []Msg {
	t.Helper()
	p := &InputParser{}
	parsed := p.Parse(data)
	msgs := make([]Msg, 0, len(parsed))
	for _, msg := range parsed {
		msgs = append(msgs, msg)
	}
	if p.HasPending() {
		for _, msg := range p.Flush() {
			msgs = append(msgs, msg)
		}
	}
	return msgs
}
