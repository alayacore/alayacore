package terminal

// The key-identity contract: a binding matches a Chord (Code + Mod), and nothing
// else. These tests exist because the whole point of the refactor is that a key
// is matched structurally — if Text could satisfy a binding, or if a constant's
// Code drifted from the key it names, the old string-matching failure would come
// back in a new shape.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

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

// TestNoBindingCarriesAnUnreliableModifier scans this package's own non-test
// sources and fails on a Chord literal that binds a modifier other than Ctrl.
// The scan covers every file rather than a list, so the day a shifted binding
// comes back it fails where it was added, not in a review of keys.go alone.
//
// The rule is about what a terminal can be relied on to deliver, not about what
// reads well:
//
//   - Ctrl needs no argument. It arrives as a byte of its own (0x01-0x1F) on
//     every host, so a Ctrl chord means the same thing everywhere.
//   - Shift needs no bit for a *letter*: it arrives as the case, which is why
//     keys.go binds 'H' rather than shift+h. Shift plus a *named* key is the one
//     case that does require a special report — `ESC [ 1;2 A` on xterm-like
//     hosts, `ESC [ a` on urxvt — and that is the binding this table dropped.
//     A mouse wheel synthesizes the bare arrow, never the modified one (see the
//     note on the viewport cases in display.go), so a modified arrow is a
//     shortcut no gesture can reach and one that silently does nothing wherever
//     the report is not sent.
//   - Alt is not bound at all: it is ESC-prefixed and ambiguous with the head of
//     a sequence, which is why the parser resolves no Alt chord.
//
// A binding that is unreachable by any key a host actually sends is worse than
// no binding, because it also reads as if it were one.
func TestNoBindingCarriesAnUnreliableModifier(t *testing.T) {
	if offenders := modifierChordLiterals(t, "."); len(offenders) > 0 {
		t.Errorf("chords bound with a modifier other than Ctrl:\n  %s\n"+
			"bind the case for shift (a terminal reports shift+h as 'H'), bind the bare key for the wheel's "+
			"own gesture, and leave a modified arrow unbound", strings.Join(offenders, "\n  "))
	}
}

// modifierChordLiterals returns one entry per Chord literal in dir's non-test
// sources that names a modifier constant other than ModCtrl, as "file:line Mod:
// ModX". A value that is not a named constant — the parser's own Mod: k.Mod,
// which converts a Key into a Chord rather than deciding what any key means —
// names no modifier and is not reported.
func modifierChordLiterals(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	var found []string
	fileSet := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		// Build-tagged files (the Windows pair) are parsed as ordinary Go, so a
		// binding hidden behind one is covered too — same choice glyphs_test.go
		// makes for the glyph scan.
		path := filepath.Join(dir, name)
		f, err := parser.ParseFile(fileSet, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}

		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok || !isChordType(lit.Type) {
				return true
			}
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue // a positional element cannot name Mod
				}
				if key, ok := kv.Key.(*ast.Ident); !ok || key.Name != "Mod" {
					continue
				}
				for _, mod := range modifierNames(kv.Value) {
					if mod != "ModCtrl" {
						pos := fileSet.Position(kv.Pos())
						found = append(found, fmt.Sprintf("%s:%d Mod: %s", filepath.Base(pos.Filename), pos.Line, mod))
					}
				}
			}
			return true
		})
	}

	sort.Strings(found)
	return found
}

// isChordType reports whether a composite literal names the binding type. Both
// spellings count; anything else is not a binding.
func isChordType(expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name == "Chord"
	case *ast.SelectorExpr:
		return t.Sel.Name == "Chord"
	}
	return false
}

// modifierNames lists the modifier constants an expression names, so a combined
// ModCtrl | ModShift reports both and fails on the one that must not be there.
// A selector is not a constant and is not descended into: the Sel of k.Mod is
// spelled "Mod", and reading it as a modifier name would report the parser's own
// Key-to-Chord conversion as a binding.
func modifierNames(expr ast.Expr) []string {
	var names []string
	ast.Inspect(expr, func(n ast.Node) bool {
		switch t := n.(type) {
		case *ast.SelectorExpr:
			return false
		case *ast.Ident:
			if strings.HasPrefix(t.Name, "Mod") {
				names = append(names, t.Name)
			}
		}
		return true
	})
	return names
}
