package terminal

// Key binding constants and documentation for the terminal UI.
// This file provides a single source of truth for all keyboard shortcuts.

// Key binding groups by context
type KeyBinding struct {
	Key         string // Key string as reported by tea.KeyMsg
	Description string // Human-readable description
	Context     string // Context where this binding is active
}

// Global key bindings - work from any context
var globalKeyBindings = []KeyBinding{
	{KeyTab, "Toggle focus between display and input", "global"},
	{KeyCtrlG, "Cancel current request (with confirmation)", "global"},
	{KeyCtrlC, "Clear input field", "global"},
	{KeyCtrlS, "Save session", "global"},
	{KeyCtrlO, "Open external editor", "global"},
	{KeyCtrlL, "Open model selector", "global"},
	{KeyCtrlQ, "Open queue manager", "global"},
	{KeyEnter, "Submit prompt/command", "global"},
}

// Display key bindings - only active when display is focused
var displayKeyBindings = []KeyBinding{
	{KeyJ, "Move window cursor down", "display"},
	{KeyK, "Move window cursor up", "display"},
	{KeyDown, "Move window cursor down", "display"},
	{KeyUp, "Move window cursor up", "display"},
	{KeyShiftJ, "Scroll down one line", "display"},
	{KeyShiftK, "Scroll up one line", "display"},
	{KeyG, "Go to bottom (last window)", "display"},
	{Keyg, "Go to top (first window)", "display"},
	{KeyShiftH, "Move cursor to top window", "display"},
	{KeyShiftL, "Move cursor to bottom window", "display"},
	{KeyShiftM, "Move cursor to middle window", "display"},
	{KeyColon, "Switch to input with command prefix", "display"},
	{KeySpace, "Toggle window wrap mode", "display"},
}

// Model selector key bindings
var modelSelectorKeyBindings = []KeyBinding{
	{KeyUp, "Move selection up", "model-selector"},
	{KeyDown, "Move selection down", "model-selector"},
	{KeyEnter, "Select model", "model-selector"},
	{KeyEsc, "Close model selector", "model-selector"},
	{KeyTab, "Toggle focus between search and list", "model-selector"},
	{"e", "Edit model config file", "model-selector"},
	{"r", "Reload models from file", "model-selector"},
}

// Queue manager key bindings
var queueManagerKeyBindings = []KeyBinding{
	{KeyUp, "Move selection up", "queue-manager"},
	{KeyDown, "Move selection down", "queue-manager"},
	{KeyEsc, "Close queue manager", "queue-manager"},
	{"d", "Delete selected queue item", "queue-manager"},
}

// Confirmation dialog key bindings
var confirmDialogKeyBindings = []KeyBinding{
	{"y", "Confirm action", "confirm-dialog"},
	{"n", "Cancel action", "confirm-dialog"},
	{KeyEsc, "Cancel action", "confirm-dialog"},
	{KeyCtrlC, "Cancel action", "confirm-dialog"},
}

// Key string constants (as reported by tea.KeyMsg.String())
const (
	// Navigation keys
	KeyTab   = "tab"
	KeyEnter = "enter"
	KeyEsc   = "esc"
	KeySpace = "space"
	KeyUp    = "up"
	KeyDown  = "down"
	KeyLeft  = "left"
	KeyRight = "right"

	// Letter keys
	KeyA = "a"
	KeyB = "b"
	KeyC = "c"
	KeyD = "d"
	KeyE = "e"
	KeyG = "G"
	KeyH = "h"
	KeyI = "i"
	KeyJ = "j"
	KeyK = "k"
	KeyL = "l"
	KeyM = "m"
	KeyN = "n"
	KeyO = "o"
	KeyP = "p"
	KeyQ = "q"
	KeyR = "r"
	KeyS = "s"
	KeyT = "t"
	KeyU = "u"
	KeyV = "v"
	KeyW = "w"
	KeyX = "x"
	KeyY = "y"
	KeyZ = "z"

	// Shifted letter keys
	KeyShiftA = "A"
	KeyShiftH = "H"
	KeyShiftJ = "J"
	KeyShiftK = "K"
	KeyShiftL = "L"
	KeyShiftM = "M"

	// Special keys
	KeyColon = ":"
	Keyg     = "g"

	// Control keys
	KeyCtrlA = "ctrl+a"
	KeyCtrlB = "ctrl+b"
	KeyCtrlC = "ctrl+c"
	KeyCtrlD = "ctrl+d"
	KeyCtrlE = "ctrl+e"
	KeyCtrlF = "ctrl+f"
	KeyCtrlG = "ctrl+g"
	KeyCtrlH = "ctrl+h"
	KeyCtrlI = "ctrl+i"
	KeyCtrlJ = "ctrl+j"
	KeyCtrlK = "ctrl+k"
	KeyCtrlL = "ctrl+l"
	KeyCtrlM = "ctrl+m"
	KeyCtrlN = "ctrl+n"
	KeyCtrlO = "ctrl+o"
	KeyCtrlP = "ctrl+p"
	KeyCtrlQ = "ctrl+q"
	KeyCtrlR = "ctrl+r"
	KeyCtrlS = "ctrl+s"
	KeyCtrlT = "ctrl+t"
	KeyCtrlU = "ctrl+u"
	KeyCtrlV = "ctrl+v"
	KeyCtrlW = "ctrl+w"
	KeyCtrlX = "ctrl+x"
	KeyCtrlY = "ctrl+y"
	KeyCtrlZ = "ctrl+z"
)

// GetAllKeyBindings returns all key bindings for help display
func GetAllKeyBindings() []KeyBinding {
	var all []KeyBinding
	all = append(all, globalKeyBindings...)
	all = append(all, displayKeyBindings...)
	all = append(all, modelSelectorKeyBindings...)
	all = append(all, queueManagerKeyBindings...)
	all = append(all, confirmDialogKeyBindings...)
	return all
}

// GetKeyBindingsByContext returns key bindings for a specific context
func GetKeyBindingsByContext(context string) []KeyBinding {
	switch context {
	case "global":
		return globalKeyBindings
	case "display":
		return displayKeyBindings
	case "model-selector":
		return modelSelectorKeyBindings
	case "queue-manager":
		return queueManagerKeyBindings
	case "confirm-dialog":
		return confirmDialogKeyBindings
	default:
		return nil
	}
}
