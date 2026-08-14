package commands

import "testing"

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
