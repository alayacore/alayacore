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

// TestReorderToolResults_NormalOrder verifies the happy path: results
// arriving out of execution order are placed in tool-call order.
func TestReorderToolResults_NormalOrder(t *testing.T) {
	stepContents := []ContentPart{toolInputHelper("a"), toolInputHelper("b"), toolInputHelper("c")}
	// Execution order differs from call order.
	results := []ContentPart{toolResultHelper("c"), toolResultHelper("a"), toolResultHelper("b")}

	out, err := reorderToolResults(stepContents, results)
	if err != nil {
		t.Fatalf("reorderToolResults() error = %v", err)
	}

	if len(out) != 6 {
		t.Fatalf("len(out) = %d, want 6", len(out))
	}
	// First three: tool inputs in order; last three: results in order.
	for i, want := range []string{"a", "b", "c"} {
		if got := out[i].(*ToolInputPart).ID; got != want {
			t.Errorf("input[%d].ID = %q, want %q", i, got, want)
		}
		if got := out[i+3].(*ToolOutputPart).ID; got != want {
			t.Errorf("result[%d].ID = %q, want %q", i, got, want)
		}
	}
}

// TestReorderToolResults_UnmatchedResult verifies the original behavior:
// a result whose ID has no matching tool call is dropped (not placed),
// and since every tool call still has a result, no error is raised.
func TestReorderToolResults_UnmatchedResult(t *testing.T) {
	stepContents := []ContentPart{toolInputHelper("a")}
	results := []ContentPart{toolResultHelper("a"), toolResultHelper("ghost")}

	out, err := reorderToolResults(stepContents, results)
	if err != nil {
		t.Fatalf("reorderToolResults() error = %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2 (input + matched result)", len(out))
	}
}

// TestReorderToolResults_EmptyIDReuse verifies the non-conforming-provider
// case where multiple tool calls share an empty ID: the unmatched slot is
// detected and reported as an error instead of entering history as nil.
func TestReorderToolResults_EmptyIDReuse(t *testing.T) {
	stepContents := []ContentPart{toolInputHelper(""), toolInputHelper("")}
	results := []ContentPart{toolResultHelper(""), toolResultHelper("")}

	_, err := reorderToolResults(stepContents, results)
	if err == nil {
		t.Fatal("expected error for tool call without a result, got nil")
	}
	if !strings.Contains(err.Error(), "tool result missing") {
		t.Errorf("error = %q, want mention of missing tool result", err)
	}
}

// TestReorderToolResults_MissingResult verifies that a tool call whose
// result never arrived is reported as an error.
func TestReorderToolResults_MissingResult(t *testing.T) {
	stepContents := []ContentPart{toolInputHelper("a"), toolInputHelper("b")}
	results := []ContentPart{toolResultHelper("a")}

	_, err := reorderToolResults(stepContents, results)
	if err == nil {
		t.Fatal("expected error for missing result, got nil")
	}
	if !strings.Contains(err.Error(), "tool result missing for tool call \"b\"") {
		t.Errorf("error = %q, want mention of tool call b", err)
	}
}

// TestReorderToolResults_NoToolInputs verifies the original behavior:
// a step without tool calls passes through unchanged.
func TestReorderToolResults_NoToolInputs(t *testing.T) {
	stepContents := []ContentPart{&TextPart{Text: "answer", ContentPartMeta: ContentPartMeta{Role: RoleAssistant}}}
	results := []ContentPart{toolResultHelper("a")}

	out, err := reorderToolResults(stepContents, results)
	if err != nil {
		t.Fatalf("reorderToolResults() error = %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
}
