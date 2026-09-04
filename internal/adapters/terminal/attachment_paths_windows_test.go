//go:build windows

package terminal

// The cases here are only true on Windows, where '\' separates and a path can
// carry a volume: a drive root, a UNC share, and the values the old code — which
// knew only '/' — deleted whole. They run in the job that executes this package
// for GOOS=windows (.github/workflows/test.yml); the arithmetic they share with
// the POSIX cases is pinned on every platform by attachment_paths_test.go.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeletePathSegmentOnWindows(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{name: "one keystroke must not clear the path", in: `C:\Users\wallace\proj\`, want: `C:\Users\wallace\|`},
		{name: "no trailing separator", in: `C:\Users\wallace\proj`, want: `C:\Users\wallace\|`},
		{name: "back to the drive root", in: `C:\abc\`, want: `C:\|`},
		{name: "the drive root survives", in: `C:\`, want: `C:\|`},
		{name: "the drive root alone survives", in: `C:|`, want: `C:|`},
		{name: "slash separates here too", in: `C:/abc/def/`, want: `C:/abc/|`},
		{name: "mixed keeps the separator that was typed", in: `C:\abc/def\`, want: `C:\abc/|`},
		{name: "rooted on the current drive", in: `\Users\docs\`, want: `\Users\|`},
		{name: "unc share is a root", in: `\\server\share\dir\`, want: `\\server\share\|`},
		{name: "unc share survives", in: `\\server\share\`, want: `\\server\share\|`},
		{name: "unc with a trailing segment", in: `\\server\share\docs\`, want: `\\server\share\|`},
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

func TestPathRootsOnWindows(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		// rootEnd is the prefix a segment delete may never remove, dirValue the
		// text the box holds while that directory is listed, and namesADirectory
		// whether the text is read as a path rather than as a filter term.
		rootEnd         int
		dirValue        string
		namesADirectory bool
	}{
		{name: "drive root", path: `C:\`, rootEnd: 3, dirValue: `C:\`, namesADirectory: true},
		{name: "drive with a segment", path: `C:\Users`, rootEnd: 3, dirValue: `C:\Users\`, namesADirectory: true},
		{name: "drive with trailing separator", path: `C:\Users\`, rootEnd: 3, dirValue: `C:\Users\`, namesADirectory: true},
		{name: "rooted on the current drive", path: `\Users\docs`, rootEnd: 1, dirValue: `\Users\docs\`, namesADirectory: true},
		{name: "unc share", path: `\\server\share`, rootEnd: 14, dirValue: `\\server\share\`, namesADirectory: true},
		{name: "unc share with a segment", path: `\\server\share\docs`, rootEnd: 15, dirValue: `\\server\share\docs\`, namesADirectory: true},
		{name: "relative text has no root", path: `docs`, rootEnd: 0, dirValue: `docs\`, namesADirectory: false},
		{name: "drive-relative text names no directory", path: `C:file`, rootEnd: 2, dirValue: `C:file\`, namesADirectory: false},
		{name: "bare volume is not a directory", path: `C:`, rootEnd: 2, dirValue: `C:\`, namesADirectory: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := rootEnd(tc.path); got != tc.rootEnd {
				t.Errorf("rootEnd(%q) = %d, want %d", tc.path, got, tc.rootEnd)
			}
			if got := dirValue(tc.path); got != tc.dirValue {
				t.Errorf("dirValue(%q) = %q, want %q", tc.path, got, tc.dirValue)
			}
			// The first half of navigateByPath's decision. The second half is
			// os.Stat, and a runner has no UNC share to ask about.
			dir, _ := filepath.Split(tc.path)
			if got := isRooted(dir); got != tc.namesADirectory {
				t.Errorf("%q read as a directory: %v, want %v", tc.path, got, tc.namesADirectory)
			}
		})
	}
}

// TestPickerAcceptsEitherSeparatorOnWindows is the end-to-end half: the listing
// follows a real Windows directory whether the box spells it the way
// os.Getwd does or with the separator every Windows API also accepts.
func TestPickerAcceptsEitherSeparatorOnWindows(t *testing.T) {
	root := filepath.Clean(t.TempDir())
	docs := filepath.Join(root, "docs")
	if err := os.Mkdir(docs, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, spell := range []string{root, filepath.ToSlash(root)} {
		aw := NewAttachmentWindow(DefaultStyles()).Open()
		aw.FilterInput = aw.FilterInput.WithValue(dirValue(spell))
		aw = aw.updateFiltered()

		if got := filepath.Clean(aw.currentDir); got != root {
			t.Errorf("input %q lists %q, want %q", dirValue(spell), got, root)
			continue
		}
		if names := entryNames(aw.filtered); !strings.Contains(strings.Join(names, ","), "docs") {
			t.Errorf("listing of %q = %v, want it to hold docs", spell, names)
		}

		aw, _ = aw.Update(parseKeyBytes(t, []byte{0x17}, keyCtrlW))
		if aw.FilterInput.Value() == "" {
			t.Errorf("Ctrl+W cleared the whole path typed as %q", spell)
		}

		before := aw.FilterInput.Value()
		aw, _ = aw.Update(KeyPressMsg{Text: "backspace", Code: KeyBackspace})
		if got := aw.FilterInput.Value(); len([]rune(got)) != len([]rune(before))-1 {
			t.Errorf("plain Backspace on %q gave %q, want one character removed", before, got)
		}
	}
}
