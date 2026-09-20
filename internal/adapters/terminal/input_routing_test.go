package terminal

// The keyboard routing table, asserted as one table rather than as scattered
// cases.
//
// There is exactly one question this file is about: given a piece of text the
// terminal delivered and a state the user put the program in, which box holds it?
// Every answer below is `keyboardTarget` applied, so the table is also the
// definition of that function's contract — and the reason it is a table and not a
// comment is that a comment is how the previous arrangement was documented. Four
// boxes each carried a flag meaning "am I the target", those flags were written by
// six different events including one that decides nothing about the user (the
// window's OS focus), and the result was that a paste landed in one overlay and
// not another and vanished in a menu.
//
// The discarded rows are decisions, not absences: a list holding the caret, the
// display pane, a modal and the loading screen have no text box to receive what
// was pasted, and the user's target is not moved out from under them.
//
// Every paste row runs twice, with the window focused and unfocused, because the
// two must not differ: window focus paints the frame and is not in this table.

import (
	"strings"
	"testing"
)

// routingState is one row: how to get there, and which box must hold text.
type routingState struct {
	name  string
	build func(tb *testing.T) Terminal
	// want is which box must change: "prompt", "attachment", "model", "theme",
	// "help", or "" for none of them.
	want string
}

// The overlays are entered through their openers rather than through the chords
// that reach them, because the chords need state this fixture does not carry (a
// loaded model list, a theme cache). What is under test is where text goes once a
// box is open; the chords have their own tests.
func openPrompt(tb *testing.T) Terminal        { return newTestTerminal() }
func openDisplay(tb *testing.T) Terminal       { return newTestTerminal().focusDisplay() }
func openModelSelector(tb *testing.T) Terminal { return newTestTerminal().openModelSelector() }

// openMCPInit opens the display-only MCP-init overlay, which the session closes
// (the ready frame) rather than a key.
func openMCPInit(tb *testing.T) Terminal {
	m := newTestTerminal()
	m.mcpInitOverlay = m.mcpInitOverlay.OpenMCPInit()
	return m
}

// openTheme needs a theme manager: `openThemeSelector` declines to open without
// one, and a row that silently does not open its overlay would pass by testing
// the prompt instead. `TestKeyboardTargetForEveryState` is what catches that, and
// it is why that check exists.
func openTheme(tb *testing.T) Terminal {
	m := newTestTerminal()
	m.themeManager = NewThemeManager(tb.TempDir())
	return m.openThemeSelector()
}
func openHelp(tb *testing.T) Terminal { return newTestTerminal().openHelpWindow() }

func openLoading(tb *testing.T) Terminal {
	m := newTestTerminal()
	m.loading = true
	return m
}

func openModal(tb *testing.T) Terminal {
	return newTestTerminal().openConfirmTool("id-1", "read_file", `{"path":"x"}`)
}

func openAttachment(keys ...Msg) func(*testing.T) Terminal {
	return func(tb *testing.T) Terminal {
		m := newTestTerminal()
		m = feedMsg(tb, m, KeyPressMsg{Code: 'a', Mod: ModCtrl}) // Ctrl+A: the picker
		for _, k := range keys {
			m = feedMsg(tb, m, k)
		}
		return m
	}
}

// Messages the rows send. Named because a row that reads `KeyPressMsg{Code:
// KeyTab}` is a row whose intent has to be re-derived every time it is read.
var (
	tabKey   = KeyPressMsg{Code: KeyTab}
	ctrlAKey = KeyPressMsg{Code: 'a', Mod: ModCtrl} // the picker, and its mode toggle
)

func routingStates() []routingState {
	return []routingState{
		{"prompt", openPrompt, "prompt"},
		{"display pane", openDisplay, ""},
		{"loading", openLoading, ""},
		{"attachment filter (local)", openAttachment(), "attachment"},
		{"attachment filter (url)", openAttachment(ctrlAKey), "attachment"},
		{"attachment list", openAttachment(tabKey), ""},
		{"model selector filter", openModelSelector, "model"},
		{"theme filter", openTheme, "theme"},
		{"help filter", openHelp, "help"},
		{"tool-confirm modal", openModal, ""},
		{"MCP-init modal", openMCPInit, ""},
	}
}

// feedMsg runs one message through the real Update and returns the model, so
// every row goes through the dispatch the event loop uses.
func feedMsg(tb *testing.T, m Terminal, msg Msg) Terminal {
	tb.Helper()
	next, _ := m.Update(msg)
	tm, ok := next.(Terminal)
	if !ok {
		tb.Fatalf("Update(%T) returned %T, want a Terminal", msg, next)
	}
	return tm
}

// boxOrder keeps the failure lines deterministic; map iteration would not.
var boxOrder = []string{"prompt", "attachment", "model", "theme", "help"}

// boxValues reads every text box the program owns, by the name the table uses.
func boxValues(m Terminal) map[string]string {
	return map[string]string{
		"prompt":     m.input.Value(),
		"attachment": m.attachmentWindow.FilterInput.Value(),
		"model":      m.modelSelector.FilterInput.Value(),
		"theme":      m.themeSelector.FilterInput.Value(),
		"help":       m.helpWindow.FilterInput.Value(),
	}
}

// changedBoxes names the boxes whose value differs between two snapshots. A
// delta rather than an emptiness test, because a box can start non-empty — the
// attachment filter opens holding the directory it lists — and the question is
// what the message changed, not what a box contains.
func changedBoxes(before, after map[string]string) []string {
	var out []string
	for _, box := range boxOrder {
		if before[box] != after[box] {
			out = append(out, box)
		}
	}
	return out
}

// TestKeyboardRoutingTableWithPaste is the contract in one message: a paste
// arrives as text, so nothing but the routing decision can move it.
func TestKeyboardRoutingTableWithPaste(t *testing.T) {
	const pasted = "pasted block"
	for _, st := range routingStates() {
		for _, windowFocused := range []bool{true, false} {
			name := st.name
			if !windowFocused {
				name += " (window unfocused)"
			}
			t.Run(name, func(t *testing.T) {
				m := st.build(t)
				before := boxValues(m)
				if !windowFocused {
					m = feedMsg(t, m, BlurMsg{})
				}
				m = feedMsg(t, m, PasteMsg{Content: pasted})
				changed := changedBoxes(before, boxValues(m))

				switch st.want {
				case "":
					if len(changed) != 0 {
						t.Errorf("the paste was written into %v; no box is the target in %s", changed, st.name)
					}
				default:
					if len(changed) != 1 || changed[0] != st.want {
						t.Errorf("the paste changed %v, want exactly %q (state %s)", changed, st.want, st.name)
					}
					if got := boxValues(m)[st.want]; !strings.HasSuffix(got, pasted) {
						t.Errorf("%q holds %q, which does not end with the pasted text", st.want, got)
					}
				}
			})
		}
	}
}

// TestKeyboardRoutingTableWithTypedText is the same table for text arriving one
// character at a time. 'ü' is printable and bound nowhere, so the only thing that
// can put it in a box is being the text target.
func TestKeyboardRoutingTableWithTypedText(t *testing.T) {
	for _, st := range routingStates() {
		t.Run(st.name, func(t *testing.T) {
			m := st.build(t)
			before := boxValues(m)
			m = feedMsg(t, m, KeyPressMsg{Code: 'ü'})
			changed := changedBoxes(before, boxValues(m))

			switch st.want {
			case "":
				if len(changed) != 0 {
					t.Errorf("a typed character was written into %v; no box is the target in %s", changed, st.name)
				}
			default:
				if len(changed) != 1 || changed[0] != st.want {
					t.Errorf("a typed character changed %v, want exactly %q (state %s)", changed, st.want, st.name)
				}
			}
		})
	}
}

// TestKeyboardTargetIsDerivedFromUserState is the other half of the rule: the
// target answers who the user is writing into and nothing else. Both halves are
// needed — a target that also moved with the window's focus would put this file
// back where it started.
func TestKeyboardTargetIsDerivedFromUserState(t *testing.T) {
	tests := []struct {
		name string
		pre  []Msg // how the user got into the state under test
		msg  Msg
		want inputTarget
	}{
		{"Tab to the display", nil, tabKey, targetDisplay},
		{"Tab back to the prompt", []Msg{tabKey}, tabKey, targetPrompt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Each row builds its own fixture: the states are reached, never
			// carried over from the row before.
			m := newTestTerminal()
			for _, pre := range tt.pre {
				m = feedMsg(t, m, pre)
			}
			after := feedMsg(t, m, tt.msg)
			if got := after.keyboardTarget(); got != tt.want {
				t.Fatalf("%s moved the target to %v, want %v", tt.name, got, tt.want)
			}
			// Unfocusing the window must not move it.
			unfocused := feedMsg(t, after, BlurMsg{})
			if got := unfocused.keyboardTarget(); got != tt.want {
				t.Errorf("the window's focus moved the target from %v to %v", tt.want, got)
			}
			m = unfocused
			m = feedMsg(t, m, FocusMsg{})
			if got := m.keyboardTarget(); got != tt.want {
				t.Errorf("the window's focus coming back moved the target to %v", got)
			}
		})
	}
}

// TestKeyboardTargetForEveryState cross-checks the table: each fixture really is
// in the state its row claims, so a passing row cannot be passing because the
// overlay never opened.
func TestKeyboardTargetForEveryState(t *testing.T) {
	want := map[string]inputTarget{
		"prompt":                    targetPrompt,
		"display pane":              targetDisplay,
		"loading":                   targetNothing,
		"attachment filter (local)": targetOverlayFilter,
		"attachment filter (url)":   targetOverlayFilter,
		"attachment list":           targetOverlayList,
		"model selector filter":     targetOverlayFilter,
		"theme filter":              targetOverlayFilter,
		"help filter":               targetOverlayFilter,
		"tool-confirm modal":        targetModal,
		"MCP-init modal":            targetModal,
	}
	for _, st := range routingStates() {
		t.Run(st.name, func(t *testing.T) {
			got := st.build(t).keyboardTarget()
			if got != want[st.name] {
				t.Errorf("keyboardTarget() = %v, want %v for the state %q", got, want[st.name], st.name)
			}
		})
	}
}
