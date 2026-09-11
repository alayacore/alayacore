package terminal

// Key parser: byte stream → message sequence.
// This is module 2 of the self-built TUI stack (see docs/tui-architecture.md).
//
// The parser is a streaming state machine: incomplete escape sequences are
// retained across reads, UTF-8 is decoded rune-wise, and bracketed paste
// content (`\x1b[200~` ... `\x1b[201~`) is passed through verbatim as a
// PasteMsg instead of being parsed as keys. Focus-reporting events
// (`\x1b[I` / `\x1b[O`, enabled by Screen.Start) become FocusMsg/BlurMsg.
//
// Every escape sequence is consumed whole, whether or not its meaning is one
// this program has: a sequence that is not a key is dropped, never split so
// that its body is typed into the prompt. That covers the terminal *replies*
// the tree never asks for but can still receive — mouse reports (both the SGR
// `CSI < ... M` form and the X10 `CSI M` form, whose three coordinate bytes the
// final byte does not delimit), and the OSC/DCS/SOS/PM/APC string controls whose
// bodies are a color, a capability or a clipboard read. A sequence that a read
// boundary cuts in half is completed by the next read rather than resolved
// early (program_input.go → readInput), which is what keeps a split report from
// arriving as its own parameters. The paste-end marker is held by the same rule
// from inside a paste (takePaste → pasteTailHold): a marker whose head is written
// into the content rather than kept is a paste that never ends, and every
// keystroke after it joins the content instead of the prompt.
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
	KeyInsert
	KeyDelete
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

	if kt, ok := keyTypeString[k.Code]; ok {
		sb.WriteString(kt)
	} else {
		switch k.Code {
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
	String() string
	// Chord is the key's identity for binding: which key, with which
	// modifiers. It deliberately excludes Text — Text is how a key is rendered
	// (shift+a is "A"), not what it is — so a binding matches the same chord
	// whichever route produced it. Handlers switch on Chord, never on String:
	// a mistyped string is a binding that silently never fires.
	Chord() Chord
}

// Chord is a key's identity: its code and modifier set, and nothing else.
// Comparable, so it can be a switch/`case` value and a map key.
type Chord struct {
	Code rune
	Mod  KeyMod
}

// KeyPressMsg is a message that represents a key press.
type KeyPressMsg Key

// String implements fmt.Stringer.
func (k KeyPressMsg) String() string { return Key(k).String() }

// Chord implements KeyMsg.
func (k KeyPressMsg) Chord() Chord { return Key(k).Chord() }

// Chord returns the key's identity (Code + Mod).
func (k Key) Chord() Chord { return Chord{Code: k.Code, Mod: k.Mod} }

// String renders the chord the way a key with no text would render: "ctrl+a",
// "shift+up", "enter". For display and test names; binding still compares Code
// and Mod, never this string.
func (c Chord) String() string { return Key{Code: c.Code, Mod: c.Mod}.String() }

// compile-time check: KeyPressMsg implements KeyMsg.
var _ KeyMsg = KeyPressMsg{}

// PasteMsg is emitted when the terminal receives pasted text using
// bracketed paste.
type PasteMsg struct {
	Content string
}

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

// parserState is the machine's coarse state, derived from the two buffers above
// rather than stored beside them: a field that every branch had to keep in step
// with pending/inPaste is one more thing that can disagree with them. It exists
// to name the state for Flush, which must resolve *any* unfinished input —
// including an open paste whose buffer holds no marker head, the case that used
// to sit here with no timer armed at all.
type parserState int

const (
	stGround parserState = iota // nothing outstanding: the next byte starts fresh
	stEscape                    // an escape sequence is held in pending
	stPaste                     // a bracketed paste is open, collecting into paste
)

// state reports which of the three the parser is in.
func (p *InputParser) state() parserState {
	switch {
	case p.inPaste:
		return stPaste
	case len(p.pending) > 0:
		return stEscape
	default:
		return stGround
	}
}

// MidSequence reports whether any input is unfinished — an incomplete escape
// sequence, or an open paste. The input loop arms its silence timeout on this,
// not on HasPending: an open paste with no marker head buffered is unfinished
// just the same, and a timeout that only saw pending bytes would leave a paste
// that never closes swallowing every keystroke that follows (see Flush).
func (p *InputParser) MidSequence() bool { return p.state() != stGround }

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
			pasted, rest, spent := p.takePaste(data)
			msgs = append(msgs, pasted...)
			if spent {
				return msgs
			}
			data = rest
			continue
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
			// Copy: these bytes belong to whoever handed them over, and the
			// input loop hands the same buffer to the terminal again on its
			// next read. An incomplete sequence is held here until the loop
			// resolves it — a timeout of silence, so at least as long as the
			// escape-sequence timeout and longer if more bytes keep arriving
			// (program_input.go → readInput) — which is long enough for the
			// buffer to be read out from under, and a split sequence that
			// resolved to the tail of the next keystroke instead of its own
			// would be wrong in a way nobody could reproduce.
			p.pending = append([]byte(nil), data...)
			return msgs
		}
		data = data[n:]
		if seq == pasteStart {
			p.inPaste = true
			p.paste.Reset()
			continue
		}
		// Focus reporting events (the terminal sends them because
		// Screen.Start enables focus reporting): "\x1b[I" on focus gain,
		// "\x1b[O" on focus loss. They are not keys — emit dedicated
		// messages so Terminal can dim/blur the UI when the app loses
		// OS-level focus.
		if seq == "\x1b[I" {
			msgs = append(msgs, FocusMsg{})
			continue
		}
		if seq == "\x1b[O" {
			msgs = append(msgs, BlurMsg{})
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

// takePaste consumes the part of an open paste that this read carries: the text
// up to the end marker, or — when the marker is not here to find — everything
// except a possible head of it.
//
// The head is the reason this is not just a write. A read stops where the
// platform stops it (inputReadSize bytes on Unix, consoleEventsPerRead events on
// Windows), so a long block is cut at an offset nobody chose, and a cut inside
// `ESC [ 201 ~` leaves that head behind looking exactly like content. Written
// into the content, it is a paste that never ends: the parser stays in paste
// mode and takes every keystroke after it as pasted text. Held in pending
// instead, it is completed by the next read like any other sequence a boundary
// splits (program_input.go → readInput).
//
// rest is what is left of the read once the paste closed, and spent reports that
// there is nothing left — the read is over, and whatever the parser is holding
// needs the next one.
func (p *InputParser) takePaste(data []byte) (msgs []any, rest []byte, spent bool) {
	if i := indexSeq(data, pasteEnd); i >= 0 {
		p.paste.Write(data[:i])
		return []any{PasteMsg{Content: p.closePaste()}}, data[i+len(pasteEnd):], false
	}
	hold := pasteTailHold(data)
	p.paste.Write(data[:len(data)-hold])
	if hold > 0 {
		p.pending = append([]byte(nil), data[len(data)-hold:]...)
	}
	return nil, nil, true
}

// pasteTailHold reports how many trailing bytes of data could be the beginning
// of the paste-end marker, which the next read may complete. At most one byte
// short of the marker is held: the whole marker is found by takePaste's search,
// and a longer tail has nothing left to become — holding it would only move
// paste content out of reach.
func pasteTailHold(data []byte) int {
	for k := min(len(data), len(pasteEnd)-1); k > 0; k-- {
		if strings.HasSuffix(string(data), pasteEnd[:k]) {
			return k
		}
	}
	return 0
}

// closePaste leaves paste mode and hands back what was collected.
func (p *InputParser) closePaste() string {
	content := p.paste.String()
	p.inPaste = false
	p.paste.Reset()
	return content
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
	switch {
	case data[1] == '[': // CSI
		// X10 mouse is the one CSI whose length its final byte does not
		// give: `CSI M` carries three raw coordinate bytes after it. A read
		// boundary inside those three must not resolve `CSI M` as a complete
		// (and meaningless) CSI — the coordinates would then be delivered as
		// keystrokes, which is the same garbage as a report with its head
		// dropped. So a head is held, like any other cut sequence.
		n, whole, coming := consumeX10Mouse(data)
		if whole {
			return string(data[:n]), n, true
		}
		if coming {
			return "", 0, false
		}
		return consumeCSI(data)
	case data[1] == 'O': // SS3
		return consumeSS3(data)
	case isStringControlHead(data[1]):
		// String-type controls: OSC (ESC ]), DCS (ESC P), SOS (ESC X),
		// PM (ESC ^), APC (ESC _). These are terminal *replies* (a color, a
		// capability, a clipboard read), and the point of reading one as a
		// sequence is that no part of its body may reach the prompt. So an
		// unterminated one is held, never resolved: the next read brings the
		// terminator (program_input.go keeps reading while a sequence is
		// outstanding and arms its timeout on every byte), and the one that
		// never terminates is dropped by the silence timeout as the unknown
		// sequence it is.
		//
		// These five bytes are therefore never Alt+<char>. That costs nothing:
		// this program binds no Alt chord at all (keys.go has none, and
		// attachment_window.go records the same fact where it explains why a
		// box binds a control byte instead), while the alternative — resolving
		// the head early — is a reply's body arriving as typing.
		if n, ok := consumeStringControl(data); ok {
			return string(data[:n]), n, true
		}
		return "", 0, false
	}
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

// stringTerminator is ST, which ends every string-type control (and, on its
// own, cancels one).
const stringTerminator = "\x1b\\"

// stringControlHeads are the bytes that begin an OSC/DCS/SOS/PM/APC control.
// Named as a set, and consulted by the Windows console encoder as well
// (console_events.go), because the two must agree exactly: the parser holds a
// sequence headed by these bytes rather than resolving it early, and an encoder
// that synthesized one from a key event would hand it that same hold — costing
// the user whatever they type next.
func isStringControlHead(b byte) bool {
	switch b {
	case ']', 'P', 'X', '^', '_':
		return true
	}
	return false
}

// startsHeldSequence reports whether an ESC followed by this byte begins a
// sequence that consumeEscape holds rather than resolves: a CSI or SS3 that has
// not reached its final byte, one of the string controls without its terminator,
// or another ESC (a nested chord, which needs at least one more byte).
//
// This is the question the event encoder has to ask. On a byte stream the same
// bytes are ambiguous — `ESC ]` is either a color reply or someone's Alt+] — and
// the parser holds them because a reply's body must never reach the prompt. On
// the event path there is no ambiguity to preserve: the console has just said
// which key was pressed, so emitting the prefix would manufacture the ambiguity
// and pay for it with the keystrokes that follow.
func startsHeldSequence(b byte) bool {
	return b == '[' || b == 'O' || b == 0x1b || isStringControlHead(b)
}

// consumeStringControl consumes an OSC/DCS/SOS/PM/APC sequence through its
// terminator, and reports whether the terminator is already in hand.
//
// When it is not, the caller holds the sequence rather than resolving it: an
// unterminated `ESC ]` is not read as Alt+], because the bytes that follow a
// reply's introducer are the reply's body and the prompt must never see them.
// The choice is made for the introducer as a class, so it does not depend on
// where a read boundary fell — which is the whole failure this closes (see
// key_parser_read_invariance_test.go). A control that never terminates is
// dropped by the loop's silence timeout, and no Alt chord is bound
// (keys.go), so nothing waits on the other reading of those five bytes.
func consumeStringControl(data []byte) (int, bool) {
	body := data[2:]
	end := -1
	if data[1] == ']' {
		// OSC alone may end with BEL instead of ST.
		if i := indexSeq(body, "\x07"); i >= 0 {
			end = i + 1
		}
	}
	if i := indexSeq(body, stringTerminator); i >= 0 {
		if n := i + len(stringTerminator); end < 0 || n < end {
			end = n
		}
	}
	if end < 0 {
		return 0, false
	}
	return 2 + end, true
}

// x10MouseLen is the byte length of an X10 mouse report on input: `CSI M` plus
// the three coordinate bytes that follow it. The length is named rather than
// spelled twice because both the consumer (consumeX10Mouse) and the decision
// that it is not a key (escapeKey) have to agree on it, and an off-by-one there
// is silent: a coordinate byte read as a CSI final is a key nobody pressed.
const x10MouseLen = 6

// consumeX10Mouse measures an X10 mouse report — `CSI M` followed by three raw
// coordinate bytes — and says which of three cases `data` is.
//
// whole: all six bytes are here, so the report is consumed as one sequence.
// Without this the parser would drop the `CSI M` (a complete-looking CSI whose
// final byte names no key it knows) and deliver the coordinates as keystrokes.
//
// coming: `ESC [ M` is here and its coordinates are not, all or part of them
// being in the next read. The caller holds it, which is what keeps a report a
// read boundary cut from typing its own body — the failure program_input.go
// exists to prevent. Holding is cheap now that the loop keeps reading while a
// sequence is outstanding and re-arms its timeout on every byte: the cost is at
// most a few bytes of a report that never finishes, and mouse reporting being
// off (screen.go → mouseReportingOff) is what makes even that unreachable
// rather than merely unlikely.
//
// Neither: not an X10 report, and the CSI grammar decides (consumeCSI).
func consumeX10Mouse(data []byte) (n int, whole, coming bool) {
	if len(data) < x10MouseLen {
		return 0, false, len(data) >= 3 && data[2] == 'M'
	}
	if data[2] != 'M' {
		return 0, false, false
	}
	return x10MouseLen, true, false
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
// ok is false for sequences that are not keys — an unknown one, and the terminal
// replies consumeEscape recognizes (mouse reports, the string controls) — which
// the caller drops (paste start is handled by the caller too).
//
// Alt+key semantics (matching uv), with one narrowing that is this program's
// own: ESC followed by a single character is Alt+that character ("\x1ba" →
// alt+a), and ESC followed by a full escape sequence is that sequence
// ("\x1b[A" → up; "\x1b\x1b[A" → alt+up). Not Alt are the five bytes that can
// begin a string control — ] P X ^ _ — which consumeEscape holds instead
// (consumeStringControl), because their other reading is a reply's body.
func escapeKey(seq string) (Key, bool) {
	if len(seq) == 1 {
		return Key{Code: KeyEscape}, true
	}
	// An X10 mouse report is `CSI M` plus three coordinate bytes, and those
	// bytes are what consumeX10Mouse took with it — not CSI parameters. The
	// check lives here, on the whole sequence, because this is the one entry
	// point every path goes through (including the nested call below for
	// `ESC ESC …`): in escapeKeyInner the same sequence has lost its ESC and
	// the length test is one byte out, which is how a report whose last
	// coordinate byte is a CSI final turns into an arrow key nobody pressed.
	if len(seq) == x10MouseLen && seq[0] == 0x1b && seq[2] == 'M' {
		return Key{}, false
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
	case ']', 'P', 'X', '^', '_':
		// A string control consumed through its terminator: a reply, not a key
		// (consumeStringControl).
		return Key{}, false
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

	// Shift+Tab: bare "[Z" (most terminals) or parameterized "[1;2Z"
	// (some terminals emit the explicit form). Any extra modifier bits
	// (e.g. "[1;4Z" = shift+alt+tab) are combined.
	if final == 'Z' {
		k := Key{Code: KeyTab, Mod: ModShift}
		if len(nums) > 1 {
			k.Mod |= xtermMod(nums[1])
		}
		return k, true
	}

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
// Per xterm, anything past the second numeric parameter is metadata and
// is ignored here — the modifier is always nums[1] when present.
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

// Flush force-resolves whatever is outstanding. The program's input loop calls
// it after the terminal has been silent for the escape-sequence timeout: a lone
// trailing ESC means the Escape key was pressed (uv/bubbletea treat ESC the same
// way after their esc-sequence timeout); an open paste means the paste is over
// without its end marker, and what was collected is delivered; any other
// incomplete sequence is unknown and dropped.
//
// The paste case is decided by state, not by a non-empty pending: a paste body
// does not have to end in the head of its marker, so a Flush gated on pending
// left the common case — content, no marker head — in paste mode forever, and a
// parser in paste mode takes every keystroke that follows as pasted text. That is
// a dead keyboard, not a lost paste; delivering the collected content and leaving
// paste mode is what keeps a broken paste from becoming one.
func (p *InputParser) Flush() []any {
	switch p.state() {
	case stPaste:
		// A held marker head is meaningless once the paste is given up: it is
		// not content and it must not be prepended to the next read.
		p.pending = nil
		if content := p.closePaste(); content != "" {
			return []any{PasteMsg{Content: content}}
		}
		return nil
	case stEscape:
		pending := p.pending
		p.pending = nil
		// One or more ESC bytes: emit that many Escape keys. Anything else
		// that could not be completed is an unknown sequence, and dropped.
		if allESC(pending) {
			msgs := make([]any, len(pending))
			for i := range pending {
				msgs[i] = KeyPressMsg(Key{Code: KeyEscape})
			}
			return msgs
		}
		return nil
	default:
		return nil
	}
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
