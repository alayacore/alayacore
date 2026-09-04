package terminal

// Windows console input events: the record layout the console API fills in, and
// the translation of a record into the bytes this program reads input as.
//
// A Unix terminal delivers a byte stream; the Windows console delivers events.
// Reading the console as bytes is possible — Go's os.File.Read turns it into
// ReadConsole, and the console translates keys into VT sequences when
// ENABLE_VIRTUAL_TERMINAL_INPUT is on — but that read is unbounded: it waits for
// a whole line in cooked mode, and no API cancels a console read reliably
// (CancelIoEx and CancelSynchronousIo are both best-effort there, which is why
// Bubble Tea warns against counting on them). One pending read is enough to break
// the editor handoff twice over: the console hands the keystrokes of a foreground
// child to whoever asked for them first, and Go's os.File.Close waits for an
// in-flight read, so the process cannot exit — and the shell cannot print its
// prompt — until a later keystroke satisfies the abandoned read.
//
// Events have no such problem. GetNumberOfConsoleInputEvents counts what is
// already queued, so the read that consumes them cannot block. What comes back is
// structured key state instead of a VT byte stream, and the byte stream is what
// key_parser.go, the keymap and every other platform speak — so the byte stream
// is synthesized here, and the parser stays the single source of truth for what a
// key means.
//
// The contract of the encoder below is therefore: emit the bytes an xterm-style
// terminal emits for the same event. That is testable rather than plausible, so
// it is tested that way — console_events_test.go feeds the encoder's output
// through the real parser and asserts the key strings the application binds
// (keys.go).
//
// Nothing in this file is a syscall: it is a byte layout and a table. It is built
// for every GOOS so those can be asserted on any machine, not only in the Windows
// CI job; the syscalls that fill the layout are in program_input_windows.go.

import (
	"encoding/binary"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
)

// Event kinds (INPUT_RECORD.EventType).
const (
	eventKey          uint16 = 0x0001
	eventMouse        uint16 = 0x0002
	eventWindowBuffer uint16 = 0x0004
	eventMenu         uint16 = 0x0008
	eventFocus        uint16 = 0x0010
)

// inputRecord mirrors INPUT_RECORD: a 16-bit kind, the padding that puts the
// union on a 4-byte boundary, and the union itself — 16 bytes, the size of its
// largest member (MOUSE_EVENT_RECORD and KEY_EVENT_RECORD are both 16).
//
// The union is carried as raw bytes and decoded on demand rather than mapped onto
// Go structs, because Go's field rules are its own and a struct that is the right
// size but the wrong offsets would mis-read every key in silence. The layout is
// pinned by TestInputRecordLayout.
type inputRecord struct {
	kind uint16
	_    [2]byte
	body [16]byte
}

// key decodes the union as KEY_EVENT_RECORD. It is meaningless unless kind is
// eventKey.
func (r inputRecord) key() keyEvent {
	return keyEvent{
		down:       binary.LittleEndian.Uint32(r.body[0:4]) != 0,
		repeat:     binary.LittleEndian.Uint16(r.body[4:6]),
		virtualKey: binary.LittleEndian.Uint16(r.body[6:8]),
		char:       binary.LittleEndian.Uint16(r.body[10:12]),
		ctrlState:  binary.LittleEndian.Uint32(r.body[12:16]),
	}
}

// focus decodes the union as FOCUS_EVENT_RECORD. It is meaningless unless kind is
// eventFocus.
func (r inputRecord) focus() bool {
	return binary.LittleEndian.Uint32(r.body[0:4]) != 0
}

// keyEvent is KEY_EVENT_RECORD. VirtualScanCode is the one member left out:
// nothing here needs the hardware code.
type keyEvent struct {
	down       bool
	repeat     uint16
	virtualKey uint16
	char       uint16 // the UTF-16 unit the press produced; 0 when it produced none
	ctrlState  uint32
}

// Control-key state bits (KEY_EVENT_RECORD.dwControlKeyState).
const (
	ctrlRightAlt  uint32 = 0x0001
	ctrlLeftAlt   uint32 = 0x0002
	ctrlRightCtrl uint32 = 0x0004
	ctrlLeftCtrl  uint32 = 0x0008
	ctrlShift     uint32 = 0x0010
)

const (
	altMask  uint32 = ctrlRightAlt | ctrlLeftAlt
	ctrlMask uint32 = ctrlRightCtrl | ctrlLeftCtrl
)

// Virtual key codes (winuser.h). Only the keys with a byte form this program
// cares about, plus the ones the encoder must recognize and drop.
const (
	vkBack    uint16 = 0x08
	vkTab     uint16 = 0x09
	vkReturn  uint16 = 0x0D
	vkShift   uint16 = 0x10
	vkControl uint16 = 0x11
	vkMenu    uint16 = 0x12
	vkPause   uint16 = 0x13
	vkCapital uint16 = 0x14
	vkEscape  uint16 = 0x1B
	vkSpace   uint16 = 0x20
	vkPrior   uint16 = 0x21 // PageUp
	vkNext    uint16 = 0x22 // PageDown
	vkEnd     uint16 = 0x23
	vkHome    uint16 = 0x24
	vkLeft    uint16 = 0x25
	vkUp      uint16 = 0x26
	vkRight   uint16 = 0x27
	vkDown    uint16 = 0x28
	vkInsert  uint16 = 0x2D
	vkDelete  uint16 = 0x2E
	vkF1      uint16 = 0x70
	vkF2      uint16 = 0x71
	vkF3      uint16 = 0x72
	vkF4      uint16 = 0x73
	vkF5      uint16 = 0x74
	vkF6      uint16 = 0x75
	vkF7      uint16 = 0x76
	vkF8      uint16 = 0x77
	vkF9      uint16 = 0x78
	vkF10     uint16 = 0x79
	vkF11     uint16 = 0x7A
	vkF12     uint16 = 0x7B
	vkNumlock uint16 = 0x90
	vkScroll  uint16 = 0x91
	vkLShift  uint16 = 0xA0
	vkRShift  uint16 = 0xA1
	vkLCtrl   uint16 = 0xA2
	vkRCtrl   uint16 = 0xA3
	vkLMenu   uint16 = 0xA4
	vkRMenu   uint16 = 0xA5
	vkProcess uint16 = 0xE5 // IME composition in progress: no character yet
)

// Single-byte sequences, named because spelling 0x1b inline is how a prefix and a
// key get confused.
const (
	keyESC     byte = 0x1b
	keyTabByte byte = 0x09
	keyCRByte  byte = 0x0d
	keyDELByte byte = 0x7f
)

// The focus-reporting sequences the parser recognizes (key_parser.go).
const (
	focusInSeq  = "\x1b[I"
	focusOutSeq = "\x1b[O"
)

// functionKeyTilde[i] is xterm's "~" parameter for F5+i. The numbering is
// irregular because xterm's is: F5 is 15, F6..F10 are 17..21, and F11/F12 are
// 23/24 — 22 belongs to nothing. F1..F4 are SS3/CSI-letter sequences instead, and
// F13 and beyond have no form here: the application binds none, so they are
// dropped with the other keys nothing is bound to.
var functionKeyTilde = []int{15, 17, 18, 19, 20, 21, 23, 24}

// isModifierOnly reports whether a key code is a modifier or a lock on its own:
// pressing Shift is not an event any application binds, and the console reports
// it as one.
func isModifierOnly(virtualKey uint16) bool {
	switch virtualKey {
	case vkShift, vkControl, vkMenu, vkLShift, vkRShift, vkLCtrl, vkRCtrl, vkLMenu, vkRMenu,
		vkCapital, vkNumlock, vkScroll, vkPause, vkProcess:
		return true
	}
	return false
}

// keyEncoder converts events into bytes. It carries one piece of state across
// events: a UTF-16 high surrogate whose low half has not arrived yet. The console
// delivers an astral character — an emoji, a CJK extension-B ideograph, most of
// what an IME commits — as two separate key events, and the first is not a
// character on its own.
type keyEncoder struct {
	highSurrogate rune
}

// append adds the byte form of one event to dst.
func (e *keyEncoder) append(dst []byte, rec inputRecord) []byte {
	switch rec.kind {
	case eventKey:
		return e.appendKey(dst, rec.key())
	case eventFocus:
		// Focus reporting. Screen.Start asks for it with DECSET 1004 on every
		// platform, and the console's own focus records are that same fact
		// stated natively.
		if rec.focus() {
			return append(dst, focusInSeq...)
		}
		return append(dst, focusOutSeq...)
	default:
		// Mouse, window-buffer and menu events have no byte form this program
		// reads: mouse input is not part of this UI at all, and the window size
		// is re-read on the model tick (Program.refreshSize) rather than from an
		// event. Dropping them is what the byte path did with them too.
		return dst
	}
}

// appendKey adds the bytes for one key event.
func (e *keyEncoder) appendKey(dst []byte, k keyEvent) []byte {
	if !k.down || isModifierOnly(k.virtualKey) {
		e.highSurrogate = 0
		return dst
	}
	n := int(k.repeat)
	if n < 1 {
		n = 1
	}
	if seq, ok := specialKeyBytes(k); ok {
		e.highSurrogate = 0
		for range n {
			dst = append(dst, seq...)
		}
		return dst
	}
	return e.appendCharBytes(dst, k, n)
}

// appendCharBytes adds the bytes for a key whose form is the character it typed.
func (e *keyEncoder) appendCharBytes(dst []byte, k keyEvent, n int) []byte {
	r, ok := e.runeOf(k)
	if !ok {
		return dst
	}
	// Alt is reported as the ESC prefix, which is how the parser sees alt+<key>.
	// AltGr is the exception: on the layouts that have it, Ctrl+Alt *is* how a
	// character is typed (AltGr+q is "@" on a German keyboard), so the character
	// stands alone — which is also what it does on every other terminal.
	alt := k.ctrlState&altMask != 0 && k.ctrlState&ctrlMask == 0
	for range n {
		if alt {
			dst = append(dst, keyESC)
		}
		dst = utf8.AppendRune(dst, r)
	}
	return dst
}

// specialKeyBytes returns the terminal sequence for a key a terminal reports as a
// sequence rather than as a character — the arrows, the navigation block, the
// function keys, Tab — and reports whether the key is one of those.
//
// The forms are xterm's, because that is the vocabulary key_parser.go reads:
// "ESC [ A" for an arrow, "ESC [ 5 ~" for PageUp, and the modifier folded into a
// parameter ("ESC [ 1;5C" for ctrl+right). Every one of them is also what a real
// terminal sends for the same key press, which is the point: the Windows path and
// the Unix path agree by construction rather than by two tables that have to be
// kept in step.
//
//nolint:gocyclo // one case per key code: the branching here is the lookup table
func specialKeyBytes(k keyEvent) ([]byte, bool) {
	mods := xtermModifier(k)
	switch k.virtualKey {
	case vkUp:
		return csiLetter(mods, 'A'), true
	case vkDown:
		return csiLetter(mods, 'B'), true
	case vkRight:
		return csiLetter(mods, 'C'), true
	case vkLeft:
		return csiLetter(mods, 'D'), true
	case vkHome:
		return csiLetter(mods, 'H'), true
	case vkEnd:
		return csiLetter(mods, 'F'), true
	case vkInsert:
		return csiTilde(mods, 2), true
	case vkDelete:
		return csiTilde(mods, 3), true
	case vkPrior:
		return csiTilde(mods, 5), true
	case vkNext:
		return csiTilde(mods, 6), true
	case vkF1, vkF2, vkF3, vkF4:
		// F1–F4 are SS3 unmodified ("ESC O P") and CSI with a modifier
		// ("ESC [ 1;5P"), which is xterm's split and what both of the parser's
		// tables accept.
		final := byte('P' + (k.virtualKey - vkF1))
		if mods == 1 {
			return []byte{keyESC, 'O', final}, true
		}
		return csiLetter(mods, final), true
	case vkF5, vkF6, vkF7, vkF8, vkF9, vkF10, vkF11, vkF12:
		return csiTilde(mods, functionKeyTilde[k.virtualKey-vkF5]), true
	case vkTab:
		switch mods {
		case 1:
			return []byte{keyTabByte}, true
		case 2:
			// Shift+Tab: the bare CSI form every terminal sends.
			return []byte{keyESC, '[', 'Z'}, true
		default:
			return csiLetter(mods, 'Z'), true
		}
	case vkBack:
		// DEL, not the console's own character for this key (BS, 0x08). Every
		// terminal this application runs on reports Backspace as 0x7f, and
		// "backspace" is what the input field binds; 0x08 would read as Ctrl+H,
		// which is the help window.
		return altPrefix(k, []byte{keyDELByte}), true
	case vkReturn, vkEscape:
		// Both have a character of their own when unmodified (CR, ESC), and the
		// console's character is the one to prefer: it is what distinguishes
		// Enter (CR) from Ctrl+Enter (LF) — the second being how a Windows user
		// reaches the prompt's line break, which elsewhere is Ctrl+J.
		if k.char != 0 {
			return nil, false
		}
		if k.virtualKey == vkReturn {
			return altPrefix(k, []byte{keyCRByte}), true
		}
		return altPrefix(k, []byte{keyESC}), true
	}
	return nil, false
}

// runeOf resolves the character of a key event, pairing the UTF-16 halves the
// console sends as separate events. ok is false when the event carries no
// character to emit — which includes the first half of a pair.
func (e *keyEncoder) runeOf(k keyEvent) (rune, bool) {
	r := rune(k.char)
	if r == 0 {
		e.highSurrogate = 0
		if code, ok := controlCodeFor(k); ok {
			return code, true
		}
		// A key with no character and no sequence here: a media key, the
		// Windows key, IME bookkeeping. A terminal with no mapping for a key
		// sends nothing either.
		return 0, false
	}
	if !utf16.IsSurrogate(r) {
		e.highSurrogate = 0
		return r, true
	}
	high := e.highSurrogate
	e.highSurrogate = 0
	if high != 0 {
		if paired := utf16.DecodeRune(high, r); paired != utf8.RuneError {
			return paired, true
		}
		// Two high surrogates in a row: the first was alone after all, and this
		// one may still start a pair of its own.
		if isHighSurrogate(r) {
			e.highSurrogate = r
			return utf8.RuneError, true
		}
	}
	if isHighSurrogate(r) {
		e.highSurrogate = r
		return 0, false
	}
	// A low half with nothing before it: the replacement character is what
	// every UTF-8 decoder makes of an unpaired surrogate, Go's own console
	// reader included.
	return utf8.RuneError, true
}

func isHighSurrogate(r rune) bool { return r >= 0xD800 && r <= 0xDBFF }

// controlCodeFor recovers the control code a terminal sends for Ctrl+letter when
// the console reports the press without a character of its own — which happens for
// some IME states and for keys injected without one. The code is derived from the
// key, not the layout, which is exactly what a terminal does and what every
// `ctrl+<letter>` binding in keys.go is written against; AltGr is excluded because
// there Ctrl+Alt is how a *character* is typed, and a bare 0 from it means nothing
// this program binds.
func controlCodeFor(k keyEvent) (rune, bool) {
	if k.ctrlState&ctrlMask == 0 || k.ctrlState&altMask != 0 {
		return 0, false
	}
	letter := k.virtualKey
	if letter >= 'a' && letter <= 'z' {
		letter -= 'a' - 'A'
	}
	if letter < 'A' || letter > 'Z' {
		return 0, false
	}
	return rune(letter-'A') + 1, true
}

// xtermModifier folds the console's control-key state into xterm's modifier
// parameter: 1, plus one for shift, two for alt, four for ctrl.
func xtermModifier(k keyEvent) int {
	mods := 1
	if k.ctrlState&ctrlShift != 0 {
		mods++
	}
	if k.ctrlState&altMask != 0 {
		mods += 2
	}
	if k.ctrlState&ctrlMask != 0 {
		mods += 4
	}
	return mods
}

// csiLetter builds "ESC [ A", and with a modifier "ESC [ 1;5A".
func csiLetter(mods int, final byte) []byte {
	if mods == 1 {
		return []byte{keyESC, '[', final}
	}
	dst := append([]byte{keyESC, '[', '1', ';'}, strconv.AppendInt(nil, int64(mods), 10)...)
	return append(dst, final)
}

// csiTilde builds "ESC [ 5~", and with a modifier "ESC [ 5;5~".
func csiTilde(mods int, param int) []byte {
	dst := append([]byte{keyESC, '['}, strconv.AppendInt(nil, int64(param), 10)...)
	if mods > 1 {
		dst = append(dst, ';')
		dst = strconv.AppendInt(dst, int64(mods), 10)
	}
	return append(dst, '~')
}

// altPrefix adds the ESC with which a terminal reports Alt held with a key that
// is itself a single byte.
func altPrefix(k keyEvent, seq []byte) []byte {
	if k.ctrlState&altMask == 0 {
		return seq
	}
	return append([]byte{keyESC}, seq...)
}
