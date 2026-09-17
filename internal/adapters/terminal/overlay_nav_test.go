package terminal

// One contract, four lists.
//
// A picker has one degree of freedom: its selection. `ScrollIdx` is derived from
// `SelectedIdx` (filtered_list.go → EnsureVisible), so there is no viewport for
// an arrow to move, and binding the arrows to the selection would bind one state
// twice — with the real cost that the arrows are what a mouse wheel synthesizes
// over an alternate screen, so a list that takes them takes the wheel too, one
// notch at a time, which is not what a wheel is for in a search box.
//
// `q` used to close a list too. It is gone for the same reason the arrows are:
// `Esc` already does that job, and a key that repeats another key's job is a key
// whose meaning has to be remembered twice.
//
// Each overlay keeps its own switch statement, which is exactly why this file
// drives all four: the drift this pins is one
// overlay still answering `↓` while another does not.

import (
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
)

// noKey is a chord no overlay answers to. Applying it opens a list afresh and
// reports the state before any key, which is what the arrows and `q` are
// compared against.
var noKey = Chord{}

// qKey is spelled here rather than taken from keys.go on purpose: `q` is bound
// nowhere in the UI now, and this file is what keeps it that way. It is also the
// one chord in this test that is not a bound key, so building it from the rune
// says so.
var qKey = Chord{Code: 'q'}

// keyMsg turns a chord back into the message a handler receives. A Chord and a
// Key are not the same shape (a Key also carries the text a printable key typed),
// so a bound chord cannot be converted directly.
func keyMsg(c Chord) KeyMsg { return KeyPressMsg(Key{Code: c.Code, Mod: c.Mod}) }

func TestListOverlaysTakeNoArrowsAndNoQ(t *testing.T) {
	styles := DefaultStyles()

	models := []protocol.ModelInfo{
		{ID: 1, Name: "Model A", ProtocolType: "openai", ModelName: "model-a"},
		{ID: 2, Name: "Model B", ProtocolType: "openai", ModelName: "model-b"},
		{ID: 3, Name: "Model C", ProtocolType: "openai", ModelName: "model-c"},
	}

	cases := []struct {
		name string
		// press opens the overlay with its list focused, applies one chord, and
		// reports where the selection ended up and whether the overlay survived.
		press func(chord Chord) (selected int, open bool)
		// movable is false for a list whose contents come from the filesystem:
		// the inert keys are still assertable, `j` is not.
		movable bool
	}{
		{
			name:    "model selector",
			movable: true,
			press: func(chord Chord) (int, bool) {
				ms := NewModelSelector(styles).LoadModels(models, 1).Open()
				ms.FilterInputFocused = false
				ms, _ = ms.Update(keyMsg(chord))
				return ms.SelectedIdx, ms.IsOpen()
			},
		},
		{
			name:    "theme selector",
			movable: true,
			press: func(chord Chord) (int, bool) {
				ts := NewThemeSelector(styles).Open([]ThemeEntry{
					{Name: "alpha"}, {Name: "beta"}, {Name: "gamma"},
				}, "alpha")
				ts.FilterInputFocused = false
				ts, _ = ts.Update(keyMsg(chord))
				return ts.SelectedIdx, ts.IsOpen()
			},
		},
		{
			name:    "help window",
			movable: true,
			press: func(chord Chord) (int, bool) {
				hw := NewHelpWindow(styles).Open()
				hw.FilterInputFocused = false
				hw, _ = hw.Update(keyMsg(chord))
				return hw.SelectedIdx, hw.IsOpen()
			},
		},
		{
			name: "attachment window",
			press: func(chord Chord) (int, bool) {
				aw := NewAttachmentWindow(styles).Open()
				aw.FilterInputFocused = false
				aw, _ = aw.Update(keyMsg(chord))
				return aw.SelectedIdx, aw.IsOpen()
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, open := tc.press(noKey)
			if !open {
				t.Fatal("the overlay is not open before any key is pressed")
			}

			for _, arrow := range []Chord{keyDown, keyUp} {
				sel, isOpen := tc.press(arrow)
				if sel != start {
					t.Errorf("%s moved the selection from %d to %d: a list has no viewport, so it takes no arrow", arrow, start, sel)
				}
				if !isOpen {
					t.Errorf("%s closed the overlay", arrow)
				}
			}

			if sel, isOpen := tc.press(qKey); !isOpen || sel != start {
				t.Errorf("q moved the selection to %d or closed the overlay (open %v): Esc is the way out", sel, isOpen)
			}

			if _, isOpen := tc.press(keyEsc); isOpen {
				t.Error("esc did not close the overlay")
			}

			if tc.movable {
				// `j` is the list's own motion, and the one that stays: help's
				// moveDown skips unselectable section rows, so the distance is
				// not fixed — only the direction is.
				if sel, _ := tc.press(keyJ); sel <= start {
					t.Errorf("j moved the selection to %d, want past %d: the letter keys are how a list navigates", sel, start)
				}
			}
		})
	}
}
