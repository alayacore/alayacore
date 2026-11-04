package agent

// The --tool-confirm gate is the one safety setting whose failure is invisible:
// needsToolConfirm only ever asks whether a name is in a set, so a name nothing
// answers to turns the gate off and says nothing. These tests pin the two halves
// of the answer — the report when a name matches nothing, and the silence when
// every name does its job — at the moment the tool list becomes final.

import (
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

// readySessionWithTools returns a session positioned at the readiness transition
// with the given tool list and confirmation names, and the output it writes to.
func readySessionWithTools(tools []llm.Tool, confirm []string) (*Session, *MockOutput) {
	output := &MockOutput{}
	s := &Session{
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{
				Output:           output,
				BaseTools:        tools,
				ToolConfirmTools: confirm,
			},
		},
		sharedState: sharedState{},
	}
	s.initToolConfirmSet(confirm)
	// No MCP servers: the service is ready by construction, which is the
	// ordinary case and the one where the tool list is final immediately.
	s.mcpService = newMCPService(nil, output)
	s.state.Store(int32(SessionInitializing)) // the phase run() begins in
	return s, output
}

func builtinTool(name string) llm.Tool { return llm.NewTool(name, "test tool").Build() }

// A name nothing answers to is reported, and the names that do match are not
// dragged into the message: the user needs to see the typo, not their own
// working configuration.
func TestUnmatchedToolConfirmIsAnnounced(t *testing.T) {
	s, output := readySessionWithTools(
		[]llm.Tool{builtinTool("read_file"), builtinTool("execute_command")},
		[]string{"execute_command", "execute_comand", "run_command"},
	)

	s.syncState() // Initializing → Ready

	if s.State() != SessionReady {
		t.Fatalf("State() = %v, want ready", s.State())
	}
	// The names travel inside a JSON frame, so their quotes arrive escaped;
	// the plain name is what the assertions can look for, and "execute_command"
	// is not a substring of "execute_comand".
	joined := strings.Join(output.Messages, "")
	for _, want := range []string{"execute_comand", "run_command"} {
		if !strings.Contains(joined, want) {
			t.Errorf("notice does not name %s:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "execute_command") {
		t.Errorf("notice names a tool the gate does match:\n%s", joined)
	}
	if !strings.Contains(joined, "no tool will ask for confirmation") {
		t.Errorf("notice does not say what the consequence is:\n%s", joined)
	}
}

// A configuration where every name matches must produce nothing: a notice on
// every startup would train the reader to ignore the one that matters.
func TestMatchedToolConfirmNamesAreSilent(t *testing.T) {
	s, output := readySessionWithTools(
		[]llm.Tool{builtinTool("read_file"), builtinTool("execute_command")},
		[]string{"execute_command"},
	)

	s.syncState()

	joined := strings.Join(output.Messages, "")
	if strings.Contains(joined, "--tool-confirm") {
		t.Errorf("a matched name produced a notice:\n%s", joined)
	}
}

// No --tool-confirm at all is the ordinary case, and it is not a finding.
func TestNoToolConfirmNamesAreSilent(t *testing.T) {
	s, output := readySessionWithTools([]llm.Tool{builtinTool("read_file")}, nil)

	s.syncState()

	if joined := strings.Join(output.Messages, ""); strings.Contains(joined, "--tool-confirm") {
		t.Errorf("no --tool-confirm produced a notice:\n%s", joined)
	}
}

// The check is made once. syncState is called after every MCP event, so a
// notice that fired per call would repeat itself for each server that connected.
func TestUnmatchedToolConfirmNoticeIsSentOnce(t *testing.T) {
	s, output := readySessionWithTools([]llm.Tool{builtinTool("read_file")}, []string{"nope"})

	s.syncState()
	s.syncState()
	s.setState(SessionReady) // no-op: phase unchanged

	if n := strings.Count(strings.Join(output.Messages, ""), "--tool-confirm"); n != 1 {
		t.Errorf("notice appears %d times, want exactly 1", n)
	}
}

// A repeated name in the flag is one finding, not two: parseToolConfirm does not
// deduplicate, so the message is where it would otherwise be visible twice.
func TestUnmatchedToolConfirmNamesAreDeduplicated(t *testing.T) {
	s, output := readySessionWithTools([]llm.Tool{builtinTool("read_file")}, []string{"nope", "nope"})

	s.syncState()

	if n := strings.Count(strings.Join(output.Messages, ""), "nope"); n != 1 {
		t.Errorf("the same name is reported %d times, want once", n)
	}
}
