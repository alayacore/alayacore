package mcp

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

func TestBuildToolName(t *testing.T) {
	got := buildToolName("my server", "do_something")
	want := "my_server_do_something"
	if got != want {
		t.Errorf("buildToolName() = %q, want %q", got, want)
	}
}

func TestDedupeToolNames(t *testing.T) {
	mk := func(name string) llm.Tool {
		return llm.NewTool(name, "desc").Build()
	}

	// Collision sources: sanitized server names mapping to one prefix
	// ("a/b" and "a_b"), and a server's own tool colliding with the
	// generated resource tool of the same name.
	tools := dedupeToolNames([]llm.Tool{
		mk("a_b_query"),         // server "a/b", tool "query"
		mk("a_b_query"),         // server "a_b", tool "query" → collides
		mk("git_read_resource"), // server "git" real tool
		mk("git_read_resource"), // generated resource tool → collides
		mk("unique"),
	})

	got := make([]string, len(tools))
	for i := range tools {
		got[i] = tools[i].Definition.Name
	}
	want := []string{"a_b_query", "a_b_query_2", "git_read_resource", "git_read_resource_2", "unique"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dedupeToolNames() = %v, want %v", got, want)
	}
}

func TestDedupeToolNamesTripleCollision(t *testing.T) {
	mk := func(name string) llm.Tool {
		return llm.NewTool(name, "desc").Build()
	}

	tools := dedupeToolNames([]llm.Tool{
		mk("x_t"),
		mk("x_t"),
		mk("x_t"),
	})

	got := make([]string, len(tools))
	for i := range tools {
		got[i] = tools[i].Definition.Name
	}
	want := []string{"x_t", "x_t_2", "x_t_3"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dedupeToolNames() = %v, want %v", got, want)
	}
}

func TestBuildDescription(t *testing.T) {
	got := buildDescription("db", "Query the database", nil)
	if got != `MCP tool from server "db": Query the database` {
		t.Errorf("buildDescription() = %q", got)
	}

	got = buildDescription("db", "", nil)
	if got != `MCP tool from server "db"` {
		t.Errorf("buildDescription(empty) = %q", got)
	}

	// With annotations: ReadOnly + Idempotent, others use spec defaults.
	yes := true
	no := false
	got = buildDescription("db", "Query the database", &ToolAnnotations{
		ReadOnlyHint:   &yes,
		IdempotentHint: &yes,
	})
	want := `MCP tool from server "db": Query the database [Read-only, Destructive, Idempotent, Open-world]`
	if got != want {
		t.Errorf("buildDescription(with annotations) = %q, want %q", got, want)
	}

	// Destructive only (explicit true), OpenWorld uses spec default (true).
	got = buildDescription("git", "Delete branch", &ToolAnnotations{
		DestructiveHint: &yes,
	})
	want = `MCP tool from server "git": Delete branch [Destructive, Open-world]`
	if got != want {
		t.Errorf("buildDescription(destructive) = %q, want %q", got, want)
	}

	// All explicitly false → no hint appended (overrides spec defaults).
	got = buildDescription("db", "Query", &ToolAnnotations{
		ReadOnlyHint:    &no,
		DestructiveHint: &no,
		IdempotentHint:  &no,
		OpenWorldHint:   &no,
	})
	if got != `MCP tool from server "db": Query` {
		t.Errorf("buildDescription(all false) = %q, want without hint", got)
	}

	// Empty annotations (all nil) → spec defaults apply (Destructive + Open-world).
	got = buildDescription("db", "Query", &ToolAnnotations{})
	want = `MCP tool from server "db": Query [Destructive, Open-world]`
	if got != want {
		t.Errorf("buildDescription(empty annotations) = %q, want %q", got, want)
	}
}

func TestToolJSON(t *testing.T) {
	tool := Tool{
		Name:        "read_file",
		Description: "Read contents of a file",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
	}

	data, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("Marshal Tool: %v", err)
	}

	var decoded Tool
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal Tool: %v", err)
	}

	if decoded.Name != tool.Name {
		t.Errorf("Name = %q, want %q", decoded.Name, tool.Name)
	}
	if decoded.Description != tool.Description {
		t.Errorf("Description = %q, want %q", decoded.Description, tool.Description)
	}
}

func TestCallToolResultJSON(t *testing.T) {
	result := CallToolResult{
		Content: []ToolContent{
			{Type: "text", Text: "Hello from MCP"},
		},
		IsError: false,
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal CallToolResult: %v", err)
	}

	var decoded CallToolResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal CallToolResult: %v", err)
	}

	if len(decoded.Content) != 1 {
		t.Fatalf("Content length = %d, want 1", len(decoded.Content))
	}
	if decoded.Content[0].Text != "Hello from MCP" {
		t.Errorf("Text = %q, want %q", decoded.Content[0].Text, "Hello from MCP")
	}
}
