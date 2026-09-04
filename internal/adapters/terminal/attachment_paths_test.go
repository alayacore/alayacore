package terminal

// The attachment picker edits a path, so where it thinks a segment starts and
// ends has to be the operating system's rule, not a hard-coded slash. These
// tests run on both the Unix and the Windows build: the cases below use '/' (a
// separator on both) and paths assembled with filepath, so what they pin holds
// wherever they run. What can only mean one thing on one platform — a drive
// letter, a backslash — lives in attachment_paths_windows_test.go, which the
// Windows CI job executes.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestPickerSeparatorsAreTheOperatingSystems pins the contract the rest rests
// on: the picker splits segments exactly where the os package does, which is
// also where filepath.Split, Dir and Ext split them. The test states no
// character set of its own, so it says the same thing on every platform and
// would notice drift in either direction.
func TestPickerSeparatorsAreTheOperatingSystems(t *testing.T) {
	for c := range 0x80 {
		b := byte(c)
		if got, want := isPathSep(b), os.IsPathSeparator(b); got != want {
			t.Errorf("byte %#02x (%q): picker says %v, os says %v", c, b, got, want)
		}
	}

	// isPathSep widens a byte to a rune, which is safe; converting the other way
	// truncates, and runes whose low byte is a separator would then separate
	// sections of their own name.
	for _, r := range []rune{utf8.RuneSelf, 0x12F, 0x2F5C, '中'} {
		if isPathSep(r) {
			t.Errorf("rune %#x (%q) read as a path separator", r, r)
		}
	}
}

// TestRootedTextNamesADirectory covers the half of navigateByPath's decision
// that is the same question on every platform: text that begins with a
// separator is a place, and text that does not is a term. The volume cases are
// Windows-only and live in attachment_paths_windows_test.go.
func TestRootedTextNamesADirectory(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"", false},
		{"docs", false},
		{"a/b", false},
		{"/", true},
		{"/usr", true},
		{"/usr/", true},
		{"//server", true},
	} {
		if got := isRooted(tc.path); got != tc.want {
			t.Errorf("isRooted(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestDeletePathSegment(t *testing.T) {
	// A '|' marks the cursor. In `in` it says where Backspace was pressed from
	// (absent: the end of the value); in `want` it says where the cursor must
	// land afterwards (absent: not asserted).
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{name: "root survives", in: "/", want: "/|"},
		{name: "one segment below the root", in: "/abc", want: "/|"},
		{name: "trailing separator goes with its segment", in: "/abc/", want: "/|"},
		{name: "deeper", in: "/abc/def/", want: "/abc/|"},
		{name: "deeper, no trailing separator", in: "/abc/def", want: "/abc/|"},
		{name: "relative has no root to protect", in: "abc/def", want: "abc/|"},
		{name: "relative, first segment", in: "abc/", want: "|"},
		{name: "multi-byte segments count in runes", in: "/文档/图片/", want: "/文档/|"},
		{name: "multi-byte, no trailing separator", in: "/文档/图片", want: "/文档/|"},
		{
			// After Home or left-arrow the cursor sits mid-value and only the
			// text before it is touched. The result is not a clean path, and it
			// does not have to be: navigateByPath runs the text through
			// filepath before anything reaches the filesystem.
			name: "cursor mid-value", in: "/abc/de|f/ghi", want: "/abc/|f/ghi",
		},
		{name: "cursor at the root deletes nothing", in: "/|abc", want: "/|abc"},
		{name: "cursor inside the first segment", in: "/a|bc/def", want: "/|bc/def"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, cursor := splitCursor(tc.in)
			want, wantCursor := splitCursor(tc.want)

			aw := NewAttachmentWindow(DefaultStyles()).Open()
			aw.FilterInput = aw.FilterInput.WithValue(value)
			if cursor >= 0 {
				aw.FilterInput = aw.FilterInput.WithCursorPos(cursor)
			}

			aw = aw.deletePathSegment()

			if got := aw.FilterInput.Value(); got != want {
				t.Errorf("deletePathSegment(%q) = %q, want %q", tc.in, got, want)
			}
			if wantCursor >= 0 && aw.FilterInput.CursorPos() != wantCursor {
				t.Errorf("deletePathSegment(%q) left the cursor at %d, want %d",
					tc.in, aw.FilterInput.CursorPos(), wantCursor)
			}
		})
	}
}

// splitCursor takes text with an optional '|' marking the cursor and returns
// the text without it plus the cursor position in runes (-1 when no marker).
func splitCursor(s string) (string, int) {
	i := strings.Index(s, "|")
	if i < 0 {
		return s, -1
	}
	return strings.Replace(s, "|", "", 1), len([]rune(s[:i]))
}

func TestDirValueOpensExactlyOneSegment(t *testing.T) {
	sep := string(filepath.Separator)
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"/", "/"},
		{"/abc", "/abc" + sep},
		{"abc", "abc" + sep},
		{"a/b", "a/b" + sep},
		{"/abc" + sep, "/abc" + sep}, // already open: do not double it
	} {
		if got := dirValue(tc.in); got != tc.want {
			t.Errorf("dirValue(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// Every directory the picker can arrive at yields a value that is still one
	// listing away from nothing: it ends in a separator, and never two.
	for _, in := range []string{t.TempDir(), filepath.Join(t.TempDir(), "x"), t.TempDir() + sep} {
		got := dirValue(in)
		if !strings.HasSuffix(got, sep) {
			t.Errorf("dirValue(%q) = %q, want a trailing separator", in, got)
		}
		if strings.HasSuffix(got, sep+sep) {
			t.Errorf("dirValue(%q) = %q, doubled the separator", in, got)
		}
	}
}

// TestPickerListsTheDirectoryTheInputNames covers the round trip the picker
// exists for: the text in the box decides the listing, and Backspace takes the
// listing back a segment. It goes through Update, so the key wiring is covered
// too, and it uses t.TempDir, so it exercises the running platform's separator
// rule against a real directory.
func TestPickerListsTheDirectoryTheInputNames(t *testing.T) {
	root := t.TempDir()
	docs := filepath.Join(root, "docs")
	if err := os.Mkdir(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantRoot := filepath.Clean(root)

	aw := NewAttachmentWindow(DefaultStyles()).Open()
	aw.FilterInput = aw.FilterInput.WithValue(dirValue(root))
	aw = aw.updateFiltered()

	if got := filepath.Clean(aw.currentDir); got != wantRoot {
		t.Fatalf("input %q lists %q, want %q", dirValue(root), got, wantRoot)
	}

	// Two characters: the last component becomes a filter over the listing.
	aw, _ = aw.Update(KeyPressMsg{Text: "d", Code: 'd'})
	aw, _ = aw.Update(KeyPressMsg{Text: "o", Code: 'o'})
	if names := entryNames(aw.filtered); len(names) != 1 || names[0] != "docs" {
		t.Fatalf("filtered by the typed segment = %v, want [docs]", names)
	}
	if got := filepath.Clean(aw.currentDir); got != wantRoot {
		t.Fatalf("filtering moved the listing to %q, want %q", got, wantRoot)
	}

	// Plain Backspace deletes one character, like every other box: "do" loses
	// the "o", and the listing stays where it was.
	aw, _ = aw.Update(KeyPressMsg{Text: "backspace", Code: KeyBackspace})
	if want := dirValue(root) + "d"; aw.FilterInput.Value() != want {
		t.Errorf("after Backspace the input holds %q, want %q", aw.FilterInput.Value(), want)
	}

	// Ctrl+W deletes the whole segment. It is fed from the bytes a terminal
	// actually sends, so wire form, parser and binding are covered together.
	killSegment := parseKeyBytes(t, []byte{0x17}, keyCtrlW)
	aw, _ = aw.Update(killSegment)
	if got := aw.FilterInput.Value(); got != dirValue(root) {
		t.Errorf("after Ctrl+W the input holds %q, want %q", got, dirValue(root))
	}
	if names := entryNames(aw.filtered); len(names) != 2 {
		t.Errorf("after Ctrl+W the listing is %v, want both entries of %q", names, wantRoot)
	}

	// Another one removes the temp directory's own segment. What is left must
	// still be a directory the picker can list — never an empty box.
	aw, _ = aw.Update(killSegment)
	if aw.FilterInput.Value() == "" {
		t.Fatal("Ctrl+W emptied a rooted path; the picker has nothing to list")
	}
	if info, err := os.Stat(aw.currentDir); err != nil || !info.IsDir() {
		t.Errorf("after two Ctrl+Ws the picker lists %q, which is not a directory", aw.currentDir)
	}
}

// parseKeyBytes turns the bytes a terminal sends into the single key press they
// decode to, so the picker tests ride the same path the keyboard does. It fails
// if the bytes decode to anything other than `want`, which is what keeps the
// binding honest about the wire form rather than about a hand-built Key.
func parseKeyBytes(t *testing.T, seq []byte, want string) KeyMsg {
	t.Helper()
	var p InputParser
	msgs := p.Parse(seq)
	if len(msgs) != 1 {
		t.Fatalf("%q parsed to %d messages, want one key press", seq, len(msgs))
	}
	kp, ok := msgs[0].(KeyPressMsg)
	if !ok {
		t.Fatalf("%q parsed to %T, want KeyPressMsg", seq, msgs[0])
	}
	if got := kp.String(); got != want {
		t.Fatalf("%q reads as %q, want %q", seq, got, want)
	}
	return kp
}

// TestAutocompleteDirFollowsTheListing checks that accepting a directory from
// the list is relative to the directory on screen, not to whatever text the box
// happens to hold.
func TestAutocompleteDirFollowsTheListing(t *testing.T) {
	root := t.TempDir()
	docs := filepath.Join(root, "docs")
	if err := os.Mkdir(docs, 0o755); err != nil {
		t.Fatal(err)
	}

	aw := NewAttachmentWindow(DefaultStyles()).Open()
	aw.currentDir = filepath.Clean(root)
	aw.FilterInput = aw.FilterInput.WithValue("stale text")
	aw = aw.autocompleteDir("docs")

	if want := dirValue(docs); aw.FilterInput.Value() != want {
		t.Errorf("input after accepting docs/ = %q, want %q", aw.FilterInput.Value(), want)
	}
	if got, want := filepath.Clean(aw.currentDir), filepath.Clean(docs); got != want {
		t.Errorf("listing moved to %q, want %q", got, want)
	}
}

func entryNames(entries []fileEntry) []string {
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.name
	}
	return names
}
