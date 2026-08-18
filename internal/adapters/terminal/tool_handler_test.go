package terminal

import (
	"strings"
	"testing"
)

func TestGenericHandler_FormatCall_WithArgs(t *testing.T) {
	h := &GenericHandler{name: "my_tool"}

	// Normal input with actual arguments should show the args
	result := h.FormatCall([]byte(`{"key":"value"}`))
	expected := "my_tool: {\"key\":\"value\"}\n"
	if result != expected {
		t.Errorf("FormatCall with args = %q, want %q", result, expected)
	}
}

// TestGenericHandlerCompactsPrettyJSON verifies pretty-printed JSON
// arguments (hard newlines inside the payload) are compacted to a single
// line — otherwise the parameter shows hard line breaks mid-parameter and
// copying yields newlines in the middle of the tool call.
func TestGenericHandlerCompactsPrettyJSON(t *testing.T) {
	h := &GenericHandler{name: "custom_tool"}

	pretty := []byte("{\n  \"command\": \"ls\",\n  \"path\": \"/tmp\"\n}")
	got := h.FormatCall(pretty)
	want := "custom_tool: {\"command\":\"ls\",\"path\":\"/tmp\"}\n"
	if got != want {
		t.Errorf("FormatCall(pretty) = %q, want %q", got, want)
	}
	if strings.Contains(got, "\n") && got != want {
		t.Errorf("FormatCall output contains unexpected newlines: %q", got)
	}

	// Non-JSON input (defensive fallback) passes through unchanged.
	if got := h.FormatCall([]byte("plain text")); got != "custom_tool: plain text\n" {
		t.Errorf("FormatCall(non-JSON) = %q", got)
	}
}

// TestComputeDiff verifies the LCS diff marks insertions, deletions, and
// unchanged lines correctly.
func TestComputeDiff(t *testing.T) {
	oldLines := []string{"a", "b", "c", "d"}
	newLines := []string{"a", "x", "c", "e"}

	pairs := computeDiff(oldLines, newLines)

	var out []string
	for _, p := range pairs {
		switch {
		case p.old == p.new:
			out = append(out, "  "+p.old)
		case p.old == "":
			out = append(out, "+ "+p.new)
		case p.new == "":
			out = append(out, "- "+p.old)
		default:
			out = append(out, "- "+p.old)
			out = append(out, "+ "+p.new)
		}
	}
	got := strings.Join(out, "\n")
	want := "  a\n- b\n+ x\n  c\n- d\n+ e"
	if got != want {
		t.Errorf("diff output:\n  got:  %q\n  want: %q", got, want)
	}
}

// TestEditFileHandlerFormatCallHugeInput guards the LCS size cap: a huge
// old/new string pair (larger than maxDiffLines) must fall back to the
// degenerate all-changed diff instead of building an m×n matrix — the
// result stays bounded and contains every line with its +/- marker.
func TestEditFileHandlerFormatCallHugeInput(t *testing.T) {
	h := &EditFileHandler{}
	makeBlock := func(prefix string, n int) string {
		var sb strings.Builder
		for i := 0; i < n; i++ {
			sb.WriteString(prefix)
			sb.WriteString("\n")
		}
		return sb.String()
	}
	// Both sides exceed the cap (2*maxDiffLines each) — the product would
	// be 16M matrix cells if the guard were missing.
	input := `{"path":"f","old_string":` +
		strings.ReplaceAll(`"`+makeBlock("old", 2*maxDiffLines+1)+`"`, "\n", `\n`) +
		`,"new_string":` +
		strings.ReplaceAll(`"`+makeBlock("new", 2*maxDiffLines+1)+`"`, "\n", `\n`) +
		`}`

	result := h.FormatCall([]byte(input))
	oldCount := strings.Count(result, "- old")
	newCount := strings.Count(result, "+ new")
	if oldCount != 2*maxDiffLines+1 {
		t.Errorf("degenerate diff old lines = %d, want %d", oldCount, 2*maxDiffLines+1)
	}
	if newCount != 2*maxDiffLines+1 {
		t.Errorf("degenerate diff new lines = %d, want %d", newCount, 2*maxDiffLines+1)
	}
}
