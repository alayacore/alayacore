package llm

import (
	"strings"
	"testing"
)

// toolInputHelper builds a ToolInputPart with the given ID and role.
func toolInputHelper(id string) *ToolInputPart {
	return &ToolInputPart{
		ID:              id,
		Name:            "read_file",
		ContentPartMeta: ContentPartMeta{Role: RoleAssistant},
	}
}

// toolResultHelper builds a ToolOutputPart with the given ID and role.
func toolResultHelper(id string) *ToolOutputPart {
	return &ToolOutputPart{
		ID:              id,
		Output:          []ContentPart{&TextPart{Text: "result " + id}},
		ContentPartMeta: ContentPartMeta{Role: RoleTool},
	}
}

// resultsIn lists the tool IDs of the ToolOutputParts in out, in order.
func resultsIn(parts []ContentPart) []string {
	var ids []string
	for _, p := range parts {
		if tr, ok := p.(*ToolOutputPart); ok {
			ids = append(ids, tr.ID)
		}
	}
	return ids
}

// Results are appended in call order, not execution order: the record has to
// read as the model wrote it, since a result is matched to its call by ID and
// the conversation is replayed in this order.
func TestAttachToolResultsOrdersByCallNotExecution(t *testing.T) {
	record := []ContentPart{toolInputHelper("a"), toolInputHelper("b"), toolInputHelper("c")}
	results := []ContentPart{toolResultHelper("c"), toolResultHelper("a"), toolResultHelper("b")}

	out, err := attachToolResults(record, results, false)
	if err != nil {
		t.Fatalf("attachToolResults() error = %v", err)
	}
	if len(out) != 6 {
		t.Fatalf("len(out) = %d, want 6", len(out))
	}
	for i, want := range []string{"a", "b", "c"} {
		if got := out[i].(*ToolInputPart).ID; got != want {
			t.Errorf("input[%d].ID = %q, want %q", i, got, want)
		}
	}
	if got := strings.Join(resultsIn(out), ","); got != "a,b,c" {
		t.Errorf("results in order %q, want a,b,c", got)
	}
}

// A result answering no recorded call is discarded: attaching it would describe a
// call the model never made.
func TestAttachToolResultsDropsUnmatchedResult(t *testing.T) {
	record := []ContentPart{toolInputHelper("a")}
	results := []ContentPart{toolResultHelper("a"), toolResultHelper("ghost")}

	out, err := attachToolResults(record, results, false)
	if err != nil {
		t.Fatalf("attachToolResults() error = %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2 (input + matched result)", len(out))
	}
}

// A step that declares itself finished must have an answer for every call it
// recorded. A missing one is a defect to fail on, not content to drop.
func TestAttachToolResultsStrictOnMissingResult(t *testing.T) {
	record := []ContentPart{toolInputHelper("a"), toolInputHelper("b")}
	results := []ContentPart{toolResultHelper("a")}

	_, err := attachToolResults(record, results, false)
	if err == nil {
		t.Fatal("expected an error for the call with no result, got nil")
	}
	if !strings.Contains(err.Error(), `"b"`) {
		t.Errorf("error = %q, want it to name the unanswered call", err)
	}
}

// The same missing result on a step that was cut means the call simply never
// finished. The forgiving policy drops the call: leaving a tool_use in the record
// without its tool_result produces a conversation the next request cannot build
// and a session file that refuses to load.
func TestAttachToolResultsForgivingDropsUnansweredCalls(t *testing.T) {
	record := []ContentPart{
		&TextPart{Text: "answer", ContentPartMeta: ContentPartMeta{Role: RoleAssistant}},
		toolInputHelper("a"),
		toolInputHelper("never-answered"),
	}
	results := []ContentPart{toolResultHelper("a")}

	out, err := attachToolResults(record, results, true)
	if err != nil {
		t.Fatalf("forgiving policy must not error: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("len(out) = %d, want 3 (text, call a, its result): %#v", len(out), out)
	}
	if _, isCall := out[2].(*ToolInputPart); isCall {
		t.Errorf("out[2] = %T, want the result: an orphan tool_use must not survive", out[2])
	}
}

// Two calls sharing an ID cannot be told apart, so no pairing is safe. Strict
// fails; forgiving leaves both out rather than guess.
func TestAttachToolResultsAmbiguousID(t *testing.T) {
	record := []ContentPart{toolInputHelper("dup"), toolInputHelper("dup")}
	results := []ContentPart{toolResultHelper("dup"), toolResultHelper("dup")}

	if _, err := attachToolResults(record, results, false); err == nil {
		t.Error("strict policy accepted an ambiguous pairing")
	} else if !strings.Contains(err.Error(), "more than once") {
		t.Errorf("error = %q, want it to name the ambiguity", err)
	}

	out, err := attachToolResults(record, results, true)
	if err != nil {
		t.Fatalf("forgiving policy errored: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("forgiving policy kept %d parts, want 0", len(out))
	}
}

// A record with no calls passes through untouched — the ordinary text-only turn.
func TestAttachToolResultsNoCalls(t *testing.T) {
	record := []ContentPart{&TextPart{Text: "hi", ContentPartMeta: ContentPartMeta{Role: RoleAssistant}}}

	out, err := attachToolResults(record, nil, false)
	if err != nil {
		t.Fatalf("attachToolResults() error = %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
}

// collectResults is what lets the forgiving policy see a result that arrived
// after the stream was already abandoned.
func TestCollectResultsDrainsQueueOnce(t *testing.T) {
	queue := make(chan ContentPart, 2)
	queue <- toolResultHelper("a")
	queue <- toolResultHelper("b")

	got := collectResults([]ContentPart{toolResultHelper("already")}, queue)
	if len(got) != 3 {
		t.Fatalf("collected %d results, want 3", len(got))
	}
	if len(queue) != 0 {
		t.Errorf("queue still holds %d results", len(queue))
	}
	// A second call on an empty queue must return, not block.
	if again := collectResults(got, queue); len(again) != 3 {
		t.Errorf("draining an empty queue changed the count to %d", len(again))
	}
}
