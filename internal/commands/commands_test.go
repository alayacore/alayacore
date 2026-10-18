package commands

import (
	"slices"
	"testing"
)

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

func TestDefinitionSplit(t *testing.T) {
	fork := def("fork", "", Token("history_id"), Rest("filename"))
	save := def("save", "", Optional(Rest("filename")))
	two := def("two", "", Token("a"), Token("b"))
	opt := def("opt", "", Token("a"), Optional(Token("b")))
	none := def("none", "")

	tests := []struct {
		name string
		def  Definition
		args string
		want []string
		ok   bool
	}{
		{"rest takes the remainder", fork, "1 my report.alaya", []string{"1", "my report.alaya"}, true},
		{"rest keeps doubled inner spaces", fork, "1 my  report.alaya", []string{"1", "my  report.alaya"}, true},
		{"rest trims both ends", fork, "  1   my report.alaya  ", []string{"1", "my report.alaya"}, true},
		{"both arguments required", fork, "1", nil, false},
		{"optional rest may be absent", save, "", []string{""}, true},
		{"optional rest may be present", save, "out.alaya", []string{"out.alaya"}, true},
		{"optional rest keeps spaces", save, "my out.alaya", []string{"my out.alaya"}, true},
		{"two tokens", two, "a b", []string{"a", "b"}, true},
		{"two tokens: too few", two, "a", nil, false},
		{"two tokens: too many", two, "a b c", nil, false},
		{"optional absent", opt, "a", []string{"a", ""}, true},
		{"optional present", opt, "a b", []string{"a", "b"}, true},
		{"optional: too many", opt, "a b c", nil, false},
		{"no arguments", none, "", []string{}, true},
		{"no arguments, but given one", none, "x", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tt.def.Split(tt.args)
			if ok != tt.ok {
				t.Fatalf("Split(%q) ok = %v, want %v", tt.args, ok, tt.ok)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("Split(%q) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}

// TestDefinitionSplitRejectsARestThatIsNotLast: the shape itself is checked, so
// a definition cannot declare a middle argument that swallows the line.
func TestDefinitionSplitRejectsARestThatIsNotLast(t *testing.T) {
	bad := def("bad", "", Rest("first"), Token("second"))
	if _, ok := bad.Split("a b"); ok {
		t.Error("a Rest argument in a non-final position was accepted")
	}
}

func TestSignatureRendersTheShape(t *testing.T) {
	tests := []struct {
		def  Definition
		want string
	}{
		{def("x", "", Token("a"), Rest("b")), "<a> <b>"},
		{def("x", "", Optional(Rest("f"))), "[f]"},
		{def("x", "", Token("0|1|2")), "<0|1|2>"},
		{def("x", "", Token("a"), Token("b"), Token("c"), Optional(Token("d"))), "<a> <b> <c> [d]"},
		{def("x", ""), ""},
	}
	for _, tt := range tests {
		if got := tt.def.Signature(); got != tt.want {
			t.Errorf("Signature() = %q, want %q", got, tt.want)
		}
	}
}

// TestDefsAreWellFormed is the check no Definition can pass by accident: a
// label, a unique name, a Rest and an optional only in the last position. The
// dispatcher trusts the shape, so a malformed one would be enforced as written.
func TestDefsAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range Defs {
		if d.Name == "" || d.Description == "" {
			t.Errorf("definition %q has no name or no description", d.Name)
		}
		if seen[d.Name] {
			t.Errorf("%s is defined twice", d.Name)
		}
		seen[d.Name] = true

		labels := map[string]bool{}
		for i, a := range d.Args {
			if a.label == "" {
				t.Errorf("%s: argument %d has no label", d.Name, i)
			}
			if labels[a.label] {
				t.Errorf("%s: %q is declared twice", d.Name, a.label)
			}
			labels[a.label] = true
			last := i == len(d.Args)-1
			if a.rest && !last {
				t.Errorf("%s: only the last argument may take the rest of the line", d.Name)
			}
			if a.optional && !last {
				t.Errorf("%s: optional arguments must come last", d.Name)
			}
		}
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
