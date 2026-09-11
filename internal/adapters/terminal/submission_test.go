package terminal

// The submitted-line grammar: one function classifies a line as a prompt or a
// command, so the two never have to agree by convention. The table is the
// definition; the routing test is the contract applied.

import "testing"

func TestParseSubmission(t *testing.T) {
	tests := []struct {
		name string
		line string
		want Submission
	}{
		{"empty", "", PromptSubmission{Text: ""}},
		{"spaces only", "   ", PromptSubmission{Text: ""}},
		{"plain prompt", "hello world", PromptSubmission{Text: "hello world"}},
		{"trimmed prompt", "  hi  ", PromptSubmission{Text: "hi"}},
		{"bare command", ":quit", CommandSubmission{Raw: "quit", Name: "quit"}},
		{"short command", ":q", CommandSubmission{Raw: "q", Name: "q"}},
		{"command with args", ":save /tmp/x", CommandSubmission{Raw: "save /tmp/x", Name: "save", Args: "/tmp/x"}},
		{"command name with args", ":quit foo", CommandSubmission{Raw: "quit foo", Name: "quit", Args: "foo"}},
		{"colon only", ":", CommandSubmission{}},
		{"leading spaces then colon", "  :help  ", CommandSubmission{Raw: "help", Name: "help"}},
		{"args keep inner spacing", ":save  a  b ", CommandSubmission{Raw: "save  a  b", Name: "save", Args: "a  b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseSubmission(tt.line); got != tt.want {
				t.Errorf("parseSubmission(%q) = %#v, want %#v", tt.line, got, tt.want)
			}
		})
	}
}

// TestSubmissionGrammarRoutesLocally pins the split the grammar decides: a local
// command is handled by the adapter, while the same name carrying arguments is
// the session's command and never opens a local dialog.
func TestSubmissionGrammarRoutesLocally(t *testing.T) {
	m := newTestTerminal()
	m.input = m.input.WithValue(":q")
	m = feedMsg(t, m, KeyPressMsg{Code: KeyEnter})
	if !m.confirmOverlay.IsOpen() {
		t.Fatal(":q did not open the local quit confirm")
	}

	m = newTestTerminal()
	m.input = m.input.WithValue(":quit foo")
	m = feedMsg(t, m, KeyPressMsg{Code: KeyEnter})
	if m.confirmOverlay.IsOpen() {
		t.Fatal(":quit foo was treated as the local quit; arguments must go to the session")
	}
	if m.input.Value() != "" {
		t.Errorf("a command should clear the input, got %q", m.input.Value())
	}
}
