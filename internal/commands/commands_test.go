package commands

import "testing"

func TestCanonical(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"q", CommandNameQuit},
		{"quit", CommandNameQuit},
		{"save", CommandNameSave}, // a name with no alias is its own canonical form
		{"nope", "nope"},          // unknown names are returned unchanged
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Canonical(tt.name); got != tt.want {
				t.Errorf("Canonical(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestSplitFirst(t *testing.T) {
	tests := []struct {
		name     string
		args     string
		wantHead string
		wantRest string
	}{
		{"no rest", "42", "42", ""},
		{"empty", "", "", ""},
		{"one rest token", "42 out.alaya", "42", "out.alaya"},
		{"rest keeps inner spaces", "42 my report.alaya", "42", "my report.alaya"},
		{"rest keeps doubled inner spaces", "42 my  report.alaya", "42", "my  report.alaya"},
		{"multiple separators", "42   out.alaya", "42", "out.alaya"},
		{"tab separator", "42\tout.alaya", "42", "out.alaya"},
		{"trailing space trimmed", "42 out.alaya ", "42", "out.alaya"},
		{"leading space trimmed", "  42 out.alaya", "42", "out.alaya"},
		{"trailing newline trimmed", "42 out.alaya\n", "42", "out.alaya"},
		{"newline inside rest preserved", "42 out\nline.alaya", "42", "out\nline.alaya"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			head, rest := SplitFirst(tt.args)
			if head != tt.wantHead {
				t.Errorf("SplitFirst(%q) head = %q, want %q", tt.args, head, tt.wantHead)
			}
			if rest != tt.wantRest {
				t.Errorf("SplitFirst(%q) rest = %q, want %q", tt.args, rest, tt.wantRest)
			}
		})
	}
}

func TestSplitCommand(t *testing.T) {
	tests := []struct {
		name     string
		cmd      string
		wantName string
		wantArgs string
	}{
		{"no args", "save", "save", ""},
		{"empty", "", "", ""},
		{"space", "save /tmp/x", "save", "/tmp/x"},
		{"tab", "save\t/tmp/x", "save", "/tmp/x"},
		{"multiple spaces", "save   /tmp/x", "save", "/tmp/x"},
		{"CRLF", "save\r\n/tmp/x", "save", "/tmp/x"},
		{"args keep inner spaces", "mcp_confirm srv code uri", "mcp_confirm", "srv code uri"},
		{"newline inside args preserved", "save\nline1\nline2", "save", "line1\nline2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, args := SplitCommand(tt.cmd)
			if name != tt.wantName {
				t.Errorf("SplitCommand(%q) name = %q, want %q", tt.cmd, name, tt.wantName)
			}
			if args != tt.wantArgs {
				t.Errorf("SplitCommand(%q) args = %q, want %q", tt.cmd, args, tt.wantArgs)
			}
		})
	}
}
