package config

import (
	"os"
	"path/filepath"
	"testing"
)

// Only the current user's home is expandable, and everything else is left as
// written.
//
// "~otheruser/…" used to be joined onto *this* user's home: $HOME/otheruser/…,
// a path that looks like a path, exists nowhere, and fails in the one way the
// caller cannot diagnose — --session opened a new session at that name instead of
// reporting that the file it was asked for cannot be read. Leaving it alone means
// the failure comes from the OS, naming the path the user actually typed.
func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("this platform has no home directory to expand against")
	}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare tilde is home", "~", home},
		{"tilde and a path is under home", "~/skills", filepath.Join(home, "skills")},
		{"nested path under home", "~/a/b/c.conf", filepath.Join(home, "a", "b", "c.conf")},
		{"another user's home is left as written", "~root/x", "~root/x"},
		{"a tilde in the middle is not a home", "a~b/c", "a~b/c"},
		{"an absolute path is untouched", "/tmp/session.alaya", "/tmp/session.alaya"},
		{"a relative path is untouched", "sessions/x.alaya", "sessions/x.alaya"},
		{"the empty path is untouched", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExpandPath(tt.in); got != tt.want {
				t.Errorf("ExpandPath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
