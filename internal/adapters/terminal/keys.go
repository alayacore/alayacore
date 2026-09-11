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

// Letter keys. An uppercase letter is the chord a terminal reports for
// shift+<letter> — the modifier never reaches the program, the case does.
var (
	keyJ      = Chord{Code: 'j'}
	keyK      = Chord{Code: 'k'}
	keyH      = Chord{Code: 'H'}
	keyL      = Chord{Code: 'L'}
	keyM      = Chord{Code: 'M'}
	keyG      = Chord{Code: 'G'}
	keyB      = Chord{Code: 'b'}
	keyE      = Chord{Code: 'e'}
	keyF      = Chord{Code: 'f'}
	keyQ      = Chord{Code: 'q'}
	keyR      = Chord{Code: 'r'}
	keyY      = Chord{Code: 'y'}
	keyN      = Chord{Code: 'n'}
	keyGSmall = Chord{Code: 'g'}

	keyYCapital = Chord{Code: 'Y'}
	keyNCapital = Chord{Code: 'N'}
	keyJCapital = Chord{Code: 'J'}
	keyKCapital = Chord{Code: 'K'}

	keyColon = Chord{Code: ':'}
)

// Shift plus a named key.
var (
	keyShiftDown = Chord{Code: KeyDown, Mod: ModShift}
	keyShiftUp   = Chord{Code: KeyUp, Mod: ModShift}
)

// Control combinations.
var (
	keyCtrlA = Chord{Code: 'a', Mod: ModCtrl}
	keyCtrlC = Chord{Code: 'c', Mod: ModCtrl}
	keyCtrlD = Chord{Code: 'd', Mod: ModCtrl}
	keyCtrlF = Chord{Code: 'f', Mod: ModCtrl}
	keyCtrlG = Chord{Code: 'g', Mod: ModCtrl}
	keyCtrlH = Chord{Code: 'h', Mod: ModCtrl}
	keyCtrlJ = Chord{Code: 'j', Mod: ModCtrl}
	keyCtrlL = Chord{Code: 'l', Mod: ModCtrl}
	keyCtrlO = Chord{Code: 'o', Mod: ModCtrl}
	keyCtrlP = Chord{Code: 'p', Mod: ModCtrl}
	keyCtrlR = Chord{Code: 'r', Mod: ModCtrl}
	keyCtrlS = Chord{Code: 's', Mod: ModCtrl}
	keyCtrlU = Chord{Code: 'u', Mod: ModCtrl}
	keyCtrlW = Chord{Code: 'w', Mod: ModCtrl}
	keyCtrlZ = Chord{Code: 'z', Mod: ModCtrl}
)

// Command names (used with ":" prefix in input). cmdCancel is the
// session command (shared constant); quit/q/suspend/help are
// adapter-local controls with no session command behind them.
const (
	cmdQuit    = "quit"
	cmdQShort  = "q"
	cmdCancel  = commands.CommandNameCancel
	cmdSuspend = "suspend"
	cmdHelp    = "help"
)
