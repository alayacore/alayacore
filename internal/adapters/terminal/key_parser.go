package terminal

// Key parser: byte stream → message sequence.
// This is module 2 of the self-built TUI stack (see REFACTOR.md §8.3).
//
// The parser is a streaming state machine: incomplete escape sequences are
// retained across reads, UTF-8 is decoded rune-wise, and bracketed paste
// content (`\x1b[200~` ... `\x1b[201~`) is passed through verbatim as a
// PasteMsg instead of being parsed as keys.
//
// Key string compatibility: KeyMsg.String() must produce exactly the same
// strings bubbletea/ultraviolet produced, because the whole application
// matches keys via strings ("ctrl+a", "shift+up", "H", ":", "enter", ...).
// The String/Keystroke logic below is a faithful reimplementation of uv's.

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// KeyMod represents modifier keys.
type KeyMod int

// Modifier bits. Values mirror ultraviolet's KeyMod so that any code
// comparing them keeps working.
const (
	ModShift KeyMod = 1 << iota
	ModAlt
	ModCtrl
	ModMeta
	ModHyper
	ModSuper
	ModCapsLock
	ModNumLock
	ModScrollLock
)

// Contains reports whether m contains all bits of mods.
func (m KeyMod) Contains(mods KeyMod) bool { return m&mods == mods }

// Special key codes. Values mirror ultraviolet's Key constants so tests
// constructing Key{Code: KeyEnter} etc. keep working.
const (
	// KeyExtended is a special key code used to signify that a key event
	// contains multiple runes.
	KeyExtended = unicode.MaxRune + 1

	// Special keys.
	KeyUp rune = KeyExtended + iota + 1
	KeyDown
	KeyRight
	KeyLeft
	KeyBegin
	KeyFind
	KeyInsert
	KeyDelete
	KeySelect
	KeyPgUp
	KeyPgDown
	KeyHome
	KeyEnd
	KeyF1
	KeyF2
	KeyF3
	KeyF4
	KeyF5
	KeyF6
	KeyF7
	KeyF8
	KeyF9
	KeyF10
	KeyF11
	KeyF12

	// Special names in C0/G0 (real control characters).
	KeyBackspace = rune(0x7f) // DEL
	KeyTab       = rune(0x09) // HT
	KeyEnter     = rune(0x0d) // CR
	KeyReturn    = KeyEnter
	KeyEscape    = rune(0x1b) // ESC
	KeyEsc       = KeyEscape
	KeySpace     = rune(0x20) // SP
)

// Key represents a key press. Text is populated only for printable
// characters (e.g. "A" for shift+a); Mod holds the modifiers; Code is the
// key code (a special key constant or a rune).
type Key struct {
	Text string
	Mod  KeyMod
	Code rune
}

// String returns the textual representation of the key. It returns the
// printable text when present (so shift+a gives "A", not "shift+a"),
// otherwise the keystroke representation ("enter", "ctrl+a", "shift+up").
func (k Key) String() string {
	if len(k.Text) > 0 && k.Text != " " {
		return k.Text
	}
	return k.Keystroke()
}

// Keystroke returns the modifier-prefixed representation of the key
// ("ctrl+shift+a", "shift+up", "pgdown", ...).
func (k Key) Keystroke() string {
	var sb strings.Builder
	if k.Mod.Contains(ModCtrl) {
		sb.WriteString("ctrl+")
	}
	if k.Mod.Contains(ModAlt) {
		sb.WriteString("alt+")
	}
	if k.Mod.Contains(ModShift) {
		sb.WriteString("shift+")
	}
	if k.Mod.Contains(ModMeta) {
		sb.WriteString("meta+")
	}
	if k.Mod.Contains(ModHyper) {
		sb.WriteString("hyper+")
	}
	if k.Mod.Contains(ModSuper) {
		sb.WriteString("super+")
	}

	if kt, ok := keyTypeString[k.Code]; ok {
		sb.WriteString(kt)
	} else {
		switch k.Code {
		case KeySpace:
			// Space is the only invisible printable character.
			sb.WriteString("space")
		case KeyExtended:
			// Multiple runes: use the text.
			sb.WriteString(k.Text)
		default:
			sb.WriteRune(k.Code)
		}
	}
	return sb.String()
}

var keyTypeString = map[rune]string{
	KeyEnter:     "enter",
	KeyTab:       "tab",
	KeyBackspace: "backspace",
	KeyEscape:    "esc",
	KeySpace:     "space",
	KeyUp:        "up",
	KeyDown:      "down",
	KeyLeft:      "left",
	KeyRight:     "right",
	KeyInsert:    "insert",
	KeyDelete:    "delete",
	KeyPgUp:      "pgup",
	KeyPgDown:    "pgdown",
	KeyHome:      "home",
	KeyEnd:       "end",
	KeyF1:        "f1",
	KeyF2:        "f2",
	KeyF3:        "f3",
	KeyF4:        "f4",
	KeyF5:        "f5",
	KeyF6:        "f6",
	KeyF7:        "f7",
	KeyF8:        "f8",
	KeyF9:        "f9",
	KeyF10:       "f10",
	KeyF11:       "f11",
	KeyF12:       "f12",
}

// KeyMsg represents a key event (a key press).
type KeyMsg interface {
	fmtStringer
	Key() Key
}

// fmtStringer is a minimal Stringer so KeyMsg can be embedded anywhere.
type fmtStringer interface{ String() string }

// KeyPressMsg is a message that represents a key press.
type KeyPressMsg Key

// String implements fmt.Stringer.
func (k KeyPressMsg) String() string { return Key(k).String() }

// Key returns the underlying key event.
func (k KeyPressMsg) Key() Key { return Key(k) }

// compile-time check: KeyPressMsg implements KeyMsg.
var _ KeyMsg = KeyPressMsg{}

// PasteMsg is emitted when the terminal receives pasted text using
// bracketed paste.
type PasteMsg struct {
	Content string
}

// PasteStartMsg is emitted when bracketed paste starts.
type PasteStartMsg struct{}

// PasteEndMsg is emitted when bracketed paste ends.
type PasteEndMsg struct{}

// WindowSizeMsg is emitted when the terminal size changes.
type WindowSizeMsg struct {
	Width, Height int
}

// FocusMsg is emitted when the terminal gains focus.
type FocusMsg struct{}

// BlurMsg is emitted when the terminal loses focus.
type BlurMsg struct{}

// InputParser is a streaming key parser. It retains incomplete escape
// sequences across Parse calls and tracks bracketed paste state.
type InputParser struct {
	pending []byte // incomplete escape sequence bytes
	inPaste bool
	paste   strings.Builder
}

// Parse consumes data and returns the decoded messages. Bytes that form an
// incomplete sequence are retained internally until the next call.
func (p *InputParser) Parse(data []byte) []any {
	if len(p.pending) > 0 {
		data = append(p.pending, data...)
		p.pending = nil
	}

	var msgs []any
	for len(data) > 0 {
		if p.inPaste {
			if i := indexSeq(data, pasteEnd); i >= 0 {
				p.paste.Write(data[:i])
				p.inPaste = false
				msgs = append(msgs, PasteMsg{Content: p.paste.String()}, PasteEndMsg{})
				p.paste.Reset()
				data = data[i+len(pasteEnd):]
				continue
			}
			p.paste.Write(data)
			return msgs
		}

		if data[0] != 0x1b {
			// Fast path: C0 control or printable rune.
			if data[0] < 0x20 || data[0] == 0x7f {
				msgs = append(msgs, KeyPressMsg(decodeC0(data[0])))
				data = data[1:]
				continue
			}
			r, n := utf8.DecodeRune(data)
			if r == utf8.RuneError && n == 1 {
				// Invalid byte: swallow it (mirrors terminal behavior of
				// replacing unknown bytes with a rune error character).
				r = utf8.RuneError
			}
			msgs = append(msgs, KeyPressMsg(Key{Code: r}))
			data = data[n:]
			continue
		}

		// ESC sequence.
		seq, n, complete := consumeEscape(data)
		if !complete {
			p.pending = data
			return msgs
		}
		data = data[n:]
		if seq == pasteStart {
			p.inPaste = true
			p.paste.Reset()
			msgs = append(msgs, PasteStartMsg{})
			continue
		}
		k, ok := escapeKey(seq)
		if !ok {
			// Unknown sequence: drop it (matches uv treating unknown
			// sequences as UnknownCsiEvent, which the app ignores).
			continue
		}
		msgs = append(msgs, KeyPressMsg(k))
	}
	return msgs
}

const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// indexSeq returns the index of seq in data, or -1.
func indexSeq(data []byte, seq string) int {
	return strings.Index(string(data), seq)
}

// decodeC0 decodes a single C0/C1 control byte (not ESC).
func decodeC0(b byte) Key {
	switch b {
	case 0x00:
		return Key{Code: KeySpace, Mod: ModCtrl} // ctrl+space (ctrl+@)
	case 0x09:
		return Key{Code: KeyTab} // ctrl+i or tab → tab (default flags)
	case 0x0d:
		return Key{Code: KeyEnter} // ctrl+m or enter → enter (default flags)
	case 0x20:
		return Key{Code: KeySpace, Text: " "}
	case 0x7f:
		return Key{Code: KeyBackspace}
	}
	if b >= 0x01 && b <= 0x1a {
		return Key{Code: rune('a' + b - 0x01), Mod: ModCtrl}
	}
	switch b {
	case 0x1c:
		return Key{Code: '\\', Mod: ModCtrl}
	case 0x1d:
		return Key{Code: ']', Mod: ModCtrl}
	case 0x1e:
		return Key{Code: '^', Mod: ModCtrl}
	case 0x1f:
		return Key{Code: '_', Mod: ModCtrl}
	}
	return Key{Code: rune(b)}
}

// consumeEscape parses an escape sequence starting at data[0] (which must be
// 0x1b). It returns the raw sequence, its byte length, and whether it is
// complete. An incomplete trailing sequence returns complete=false and the
// caller retains it for the next read.
func consumeEscape(data []byte) (string, int, bool) {
	if len(data) == 1 {
		return "", 0, false // lone ESC: may be part of a sequence
	}
	switch data[1] {
	case '[': // CSI
		return consumeCSI(data)
	case 'O': // SS3
		return consumeSS3(data)
	default:
		// ESC + printable rune → Alt+key.
		if data[1] >= 0x20 && data[1] != 0x7f {
			_, n := utf8.DecodeRune(data[1:])
			return string(data[:1+n]), 1 + n, true
		}
		if data[1] == 0x1b {
			// ESC followed by another escape sequence (ESC ESC [ A →
			// alt+up). Consume the nested sequence as part of this one.
			if len(data) == 2 {
				return "", 0, false // need at least one more byte
			}
			_, n, complete := consumeEscape(data[1:])
			if !complete {
				return "", 0, false
			}
			return string(data[:1+n]), 1 + n, true
		}
		return string(data[:2]), 2, true
	}
}

// consumeCSI parses "\x1b[...<final>" where final is a letter or '~'.
func consumeCSI(data []byte) (string, int, bool) {
	for i := 2; i < len(data); i++ {
		c := data[i]
		if c >= 0x40 && c <= 0x7e {
			return string(data[:i+1]), i + 1, true
		}
	}
	return "", 0, false // incomplete
}

// consumeSS3 parses "\x1bO<final>" or "\x1bO1;<mod><final>".
func consumeSS3(data []byte) (string, int, bool) {
	for i := 2; i < len(data); i++ {
		c := data[i]
		if c >= 0x40 && c <= 0x7e {
			return string(data[:i+1]), i + 1, true
		}
	}
	return "", 0, false
}

// escapeKey maps a complete escape sequence (starting with ESC) to a Key.
// ok is false for unknown sequences (paste start is handled by the caller).
//
// Alt+key semantics (matching uv): ESC followed by a single character is
// Alt+that character ("\x1ba" → alt+a); ESC followed by a full escape
// sequence is that sequence ("\x1b[A" → up; "\x1b\x1b[A" → alt+up).
func escapeKey(seq string) (Key, bool) {
	if len(seq) == 1 {
		return Key{Code: KeyEscape}, true
	}
	inner := seq[1:]
	// ESC followed by another escape sequence (ESC ESC [ A → alt+up).
	// A double ESC alone is just the Escape key.
	if inner[0] == 0x1b {
		if k, ok := escapeKey(inner); ok && k.Code != KeyEscape {
			k.Mod |= ModAlt
			k.Text = ""
			return k, true
		}
		return escapeKey(inner)
	}
	if k, ok := escapeKeyInner(inner); ok {
		// ESC + a single character (printable, control byte, or UTF-8
		// rune) is Alt+that key; full CSI/SS3 sequences are not.
		if utf8.RuneCountInString(inner) == 1 {
			k.Mod |= ModAlt
			k.Text = ""
		}
		return k, true
	}
	return Key{}, false
}

// escapeKeyInner maps a sequence without the leading ESC.
func escapeKeyInner(seq string) (Key, bool) {
	// C0/printable single byte (e.g. ESC 'a' → alt+a).
	if len(seq) == 1 {
		b := seq[0]
		if b < 0x20 || b == 0x7f {
			if b == 0x1b {
				return Key{Code: KeyEscape}, true
			}
			return decodeC0(b), true
		}
		r, _ := utf8.DecodeRuneInString(seq)
		return Key{Code: r}, true
	}

	switch seq[0] {
	case '[':
		return parseCSI(seq)
	case 'O':
		return parseSS3(seq)
	default:
		// Multi-byte UTF-8 rune after ESC → alt+rune.
		r, _ := utf8.DecodeRuneInString(seq)
		if r != utf8.RuneError {
			return Key{Code: r, Mod: ModAlt}, true
		}
	}
	return Key{}, false
}

// parseCSI parses "[...<final>" sequences (no ESC prefix) into keys.
//
//nolint:gocyclo // CSI final-byte dispatch covers VT100/SS3/URxvt/XTerm variants
func parseCSI(seq string) (Key, bool) {
	final := seq[len(seq)-1]
	params := seq[1 : len(seq)-1] // between '[' and final

	// Shift+Tab.
	if seq == "[Z" {
		return Key{Code: KeyTab, Mod: ModShift}, true
	}

	// URxvt shifted arrows: ESC [ a/b/c/d → shift+up/down/right/left.
	if len(seq) == 2 {
		switch seq[1] {
		case 'a':
			return Key{Code: KeyUp, Mod: ModShift}, true
		case 'b':
			return Key{Code: KeyDown, Mod: ModShift}, true
		case 'c':
			return Key{Code: KeyRight, Mod: ModShift}, true
		case 'd':
			return Key{Code: KeyLeft, Mod: ModShift}, true
		}
	}

	// Split params into the main param and a modifier param ("1;2" → 1, 2).
	nums := parseParams(params)
	switch final {
	case '~':
		if len(nums) == 0 {
			return Key{}, false
		}
		switch nums[0] {
		case 200, 201:
			// bracketed paste — handled by the caller.
			return Key{}, false
		case 27:
			// XTerm modifyOtherKeys — not supported (matches app needs).
			return Key{}, false
		}
		k, ok := csiTildeKeys[nums[0]]
		if !ok {
			return Key{}, false
		}
		if len(nums) > 1 {
			k.Mod = xtermMod(nums[1])
		}
		return k, true
	case 'A', 'B', 'C', 'D', 'E', 'F', 'H', 'P', 'Q', 'R', 'S':
		k, ok := csiFuncKeys[final]
		if !ok {
			return Key{}, false
		}
		if len(nums) > 1 {
			k.Mod = xtermMod(nums[1])
		}
		return k, true
	}
	return Key{}, false
}

// parseSS3 parses "O<final>" / "O1;<mod><final>" sequences (no ESC prefix).
func parseSS3(seq string) (Key, bool) {
	final := seq[len(seq)-1]
	params := seq[1 : len(seq)-1]
	if params == "" {
		if k, ok := ss3Keys[final]; ok {
			return k, true
		}
		return Key{}, false
	}
	nums := parseParams(params)
	if len(nums) > 1 {
		if k, ok := ss3Keys[final]; ok {
			k.Mod = xtermMod(nums[1])
			return k, true
		}
	}
	return Key{}, false
}

// parseParams parses semicolon-separated decimal parameters.
func parseParams(s string) []int {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ";")
	nums := make([]int, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			nums = append(nums, 0)
			continue
		}
		n := 0
		for _, c := range part {
			if c < '0' || c > '9' {
				return nums
			}
			n = n*10 + int(c-'0')
		}
		nums = append(nums, n)
	}
	return nums
}

// xtermMod converts an XTerm modifier parameter (offset by 1) to KeyMod bits.
func xtermMod(m int) KeyMod {
	// Modifiers list matches uv: 1 shift, 2 alt, 4 ctrl, 8 meta, 16 hyper,
	// 32 super — but in uv's bit layout ModShift=1, ModAlt=2, ModCtrl=4 ...
	// the XTerm param is (bits+1).
	bits := m - 1
	var mod KeyMod
	if bits&0x01 != 0 {
		mod |= ModShift
	}
	if bits&0x02 != 0 {
		mod |= ModAlt
	}
	if bits&0x04 != 0 {
		mod |= ModCtrl
	}
	if bits&0x08 != 0 {
		mod |= ModMeta
	}
	if bits&0x10 != 0 {
		mod |= ModHyper
	}
	if bits&0x20 != 0 {
		mod |= ModSuper
	}
	return mod
}

// CSI final-byte function keys (VT100/VT200 + XTerm).
var csiFuncKeys = map[byte]Key{
	'A': {Code: KeyUp},
	'B': {Code: KeyDown},
	'C': {Code: KeyRight},
	'D': {Code: KeyLeft},
	'E': {Code: KeyBegin},
	'F': {Code: KeyEnd},
	'H': {Code: KeyHome},
	'P': {Code: KeyF1},
	'Q': {Code: KeyF2},
	'R': {Code: KeyF3},
	'S': {Code: KeyF4},
}

// CSI "~" keys.
var csiTildeKeys = map[int]Key{
	1:  {Code: KeyHome},
	2:  {Code: KeyInsert},
	3:  {Code: KeyDelete},
	4:  {Code: KeyEnd},
	5:  {Code: KeyPgUp},
	6:  {Code: KeyPgDown},
	7:  {Code: KeyHome},
	8:  {Code: KeyEnd},
	11: {Code: KeyF1},
	12: {Code: KeyF2},
	13: {Code: KeyF3},
	14: {Code: KeyF4},
	15: {Code: KeyF5},
	17: {Code: KeyF6},
	18: {Code: KeyF7},
	19: {Code: KeyF8},
	20: {Code: KeyF9},
	21: {Code: KeyF10},
	23: {Code: KeyF11},
	24: {Code: KeyF12},
}

// SS3 (ESC O) keys: application cursor mode + keypad.
var ss3Keys = map[byte]Key{
	'A': {Code: KeyUp},
	'B': {Code: KeyDown},
	'C': {Code: KeyRight},
	'D': {Code: KeyLeft},
	'E': {Code: KeyBegin},
	'F': {Code: KeyEnd},
	'H': {Code: KeyHome},
	'P': {Code: KeyF1},
	'Q': {Code: KeyF2},
	'R': {Code: KeyF3},
	'S': {Code: KeyF4},
	'M': {Code: KeyEnter}, // keypad enter

	// URxvt ctrl+arrows: ESC O a/b/c/d → ctrl+up/down/right/left.
	'a': {Code: KeyUp, Mod: ModCtrl},
	'b': {Code: KeyDown, Mod: ModCtrl},
	'c': {Code: KeyRight, Mod: ModCtrl},
	'd': {Code: KeyLeft, Mod: ModCtrl},
}

// Flush force-resolves any pending incomplete sequence. This is called by
// the program's input loop after a short escape-sequence timeout: a lone
// trailing ESC means the Escape key was pressed (uv/bubbletea treat ESC the
// same way after their esc-sequence timeout); any other incomplete sequence
// is unknown and dropped.
func (p *InputParser) Flush() []any {
	if len(p.pending) == 0 {
		return nil
	}
	pending := p.pending
	p.pending = nil
	// One or more ESC bytes: emit that many Escape keys.
	if allESC(pending) {
		msgs := make([]any, len(pending))
		for i := range pending {
			msgs[i] = KeyPressMsg(Key{Code: KeyEscape})
		}
		return msgs
	}
	return nil
}

// allESC reports whether b consists only of ESC bytes.
func allESC(b []byte) bool {
	for _, c := range b {
		if c != 0x1b {
			return false
		}
	}
	return true
}

// HasPending reports whether an incomplete escape sequence is buffered.
func (p *InputParser) HasPending() bool {
	return len(p.pending) > 0
}
