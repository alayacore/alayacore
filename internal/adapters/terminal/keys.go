package terminal

// Key bindings for the terminal UI, as structured chords (Code + Mod).
//
// A binding is a Chord, never a string: the parser produces a KeyMsg, whose
// Chord() is the key's identity, and every handler switches on that. Strings are
// for rendering and tests (Key.String), not for deciding what a key means — a
// mistyped string literal is a binding that silently never fires, which is what
// this file exists to make impossible.
//
// This is the single source of truth for the chords the UI binds.

import (
	"github.com/alayacore/alayacore/internal/commands"
)

// Navigation and editing keys.
var (
	keyUp        = Chord{Code: KeyUp}
	keyDown      = Chord{Code: KeyDown}
	keyLeft      = Chord{Code: KeyLeft}
	keyRight     = Chord{Code: KeyRight}
	keyTab       = Chord{Code: KeyTab}
	keyEnter     = Chord{Code: KeyEnter}
	keyEsc       = Chord{Code: KeyEscape}
	keySpace     = Chord{Code: KeySpace}
	keyHome      = Chord{Code: KeyHome}
	keyEnd       = Chord{Code: KeyEnd}
	keyPgUp      = Chord{Code: KeyPgUp}
	keyPgDown    = Chord{Code: KeyPgDown}
	keyF1        = Chord{Code: KeyF1}
	keyBackspace = Chord{Code: KeyBackspace}
	keyDelete    = Chord{Code: KeyDelete}
)

// Letter and punctuation keys. An uppercase letter is the chord a terminal
// reports for shift+<letter> — the modifier never reaches the program, the
// case does.
var (
	keyJ = Chord{Code: 'j'}
	keyK = Chord{Code: 'k'}
	keyH = Chord{Code: 'H'}
	keyL = Chord{Code: 'L'}
	keyM = Chord{Code: 'M'}
	keyB = Chord{Code: 'b'}
	keyF = Chord{Code: 'f'}
	keyR = Chord{Code: 'r'}
	keyY = Chord{Code: 'y'}
	keyN = Chord{Code: 'n'}
	keyC = Chord{Code: 'c'}

	keyYCapital = Chord{Code: 'Y'}
	keyNCapital = Chord{Code: 'N'}
	keyCCapital = Chord{Code: 'C'}

	// The endpoints of the transcript are `G`: bare for the last window (follow
	// it), counted for an absolute window (`1G` is the first). It is vi's own
	// letter for the last line.
	keyG = Chord{Code: 'G'}

	keyColon = Chord{Code: ':'}
)

// countDigit reports the digit a chord spells, for the count prefix typed in the
// display pane. Digits are read as themselves — no chord constant per digit —
// and a modified digit is not one: Alt+<digit> arrives ESC-prefixed and is
// decoded as its own chord, which is unbound (this file has no Alt chord).
func countDigit(c Chord) (int, bool) {
	if c.Mod != 0 || c.Code < '0' || c.Code > '9' {
		return 0, false
	}
	return int(c.Code - '0'), true
}

// Control combinations.
var (
	keyCtrlA = Chord{Code: 'a', Mod: ModCtrl}
	keyCtrlC = Chord{Code: 'c', Mod: ModCtrl}
	keyCtrlD = Chord{Code: 'd', Mod: ModCtrl}
	keyCtrlE = Chord{Code: 'e', Mod: ModCtrl}
	keyCtrlG = Chord{Code: 'g', Mod: ModCtrl}
	keyCtrlH = Chord{Code: 'h', Mod: ModCtrl}
	keyCtrlJ = Chord{Code: 'j', Mod: ModCtrl}
	keyCtrlL = Chord{Code: 'l', Mod: ModCtrl}
	keyCtrlO = Chord{Code: 'o', Mod: ModCtrl}
	keyCtrlP = Chord{Code: 'p', Mod: ModCtrl}
	keyCtrlR = Chord{Code: 'r', Mod: ModCtrl}
	keyCtrlS = Chord{Code: 's', Mod: ModCtrl}
	keyCtrlT = Chord{Code: 't', Mod: ModCtrl}
	keyCtrlU = Chord{Code: 'u', Mod: ModCtrl}
	keyCtrlW = Chord{Code: 'w', Mod: ModCtrl}
	keyCtrlX = Chord{Code: 'x', Mod: ModCtrl}
	keyCtrlY = Chord{Code: 'y', Mod: ModCtrl}
	keyCtrlZ = Chord{Code: 'z', Mod: ModCtrl}
)

// Command names (used with ":" prefix in input). cmdQuit and cmdCancel are
// session command names (shared constants) that the TUI intercepts to add its
// own behavior — the confirmation dialog, the exit code. A short form such as
// ":q" needs no constant of its own: handleCommand resolves it through
// commands.Canonical before this table is consulted. suspend/help are
// adapter-local controls with no session command behind them.
const (
	cmdQuit    = commands.CommandNameQuit
	cmdCancel  = commands.CommandNameCancel
	cmdSuspend = "suspend"
	cmdHelp    = "help"
)
