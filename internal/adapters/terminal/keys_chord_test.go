package terminal

// The key-identity contract: a binding matches a Chord (Code + Mod), and nothing
// else. These tests exist because the whole point of the refactor is that a key
// is matched structurally — if Text could satisfy a binding, or if a constant's
// Code drifted from the key it names, the old string-matching failure would come
// back in a new shape.

import "testing"

// TestChordIgnoresText pins that identity is Code+Mod. Text is how a key is
// rendered, not what it is: shift+a arrives as Code 'A' whether or not Text was
// filled in, and a chord must be the same either way.
func TestChordIgnoresText(t *testing.T) {
	withText := KeyPressMsg(Key{Code: 'A', Text: "A"})
	withoutText := KeyPressMsg(Key{Code: 'A'})
	if withText.Chord() != withoutText.Chord() {
		t.Fatalf("Text changed the chord: %v vs %v", withText.Chord(), withoutText.Chord())
	}
	if withText.Chord() != (Chord{Code: 'A'}) {
		t.Fatalf("chord = %v, want {Code:'A'}", withText.Chord())
	}
}

// TestChordHasNoTextFallback pins that a Key with no Code is not silently
// resolved from its Text. The old code matched key.String(), which returned Text,
// so a key built only from Text "worked"; that is the round trip this refactor
// removed. A chord built that way must be empty, not the key the text names.
func TestChordHasNoTextFallback(t *testing.T) {
	if got := (KeyPressMsg(Key{Text: "left"})).Chord(); got != (Chord{}) {
		t.Fatalf("a Text-only key resolved to %v; Text must not satisfy a binding", got)
	}
}

// TestKeyChordConstants pins each bound chord's rendered form, so a mistyped Code
// in keys.go is caught here rather than by a shortcut that silently does nothing.
func TestKeyChordConstants(t *testing.T) {
	cases := []struct {
		chord Chord
		want  string
	}{
		{keyJ, "j"},
		{keyH, "H"},
		{keyGSmall, "g"},
		{keyColon, ":"},
		{keyEnter, "enter"},
		{keyEsc, "esc"},
		{keyTab, "tab"},
		{keySpace, "space"},
		{keyBackspace, "backspace"},
		{keyDelete, "delete"},
		{keyLeft, "left"},
		{keyRight, "right"},
		{keyPgUp, "pgup"},
		{keyPgDown, "pgdown"},
		{keyF1, "f1"},
		{keyCtrlA, "ctrl+a"},
		{keyCtrlZ, "ctrl+z"},
		{keyShiftUp, "shift+up"},
		{keyShiftDown, "shift+down"},
	}
	for _, tc := range cases {
		if got := tc.chord.String(); got != tc.want {
			t.Errorf("chord %+v renders as %q, want %q", tc.chord, got, tc.want)
		}
	}
}

// TestInsertionFollowsCodeNotText pins that the input field inserts the chord's
// Code. A printable key whose Text says something else inserts the Code — the
// field no longer re-parses the rendered name to recover the character.
func TestInsertionFollowsCodeNotText(t *testing.T) {
	f := NewInputField().WithWidth(40)
	after, _ := f.Update(KeyPressMsg(Key{Code: 'a', Text: "left"}))
	if got := after.Value(); got != "a" {
		t.Fatalf("value = %q, want %q: insertion must follow Code, not Text", got, "a")
	}
}
