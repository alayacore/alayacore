package providers

import (
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

// TestAnthropicGroupsToolResultWithFollowingUserText pins the wire rule that a
// tool_result and a text part that follows it form ONE user turn.
//
// This is the exact shape a mid-task summarization produces: the summarize
// prompt is appended right after the pending tool result, so the model reads
// that result before summarizing it. Without the merge, GroupByRole would emit
// two consecutive user messages, which the Messages API rejects.
func TestAnthropicGroupsToolResultWithFollowingUserText(t *testing.T) {
	text := &llm.TextPart{Text: "working"}
	text.SetRole(llm.RoleAssistant)

	toolUse := &llm.ToolInputPart{ID: "c1", Name: "t", Input: []byte(`{}`)}
	toolUse.SetRole(llm.RoleAssistant)

	toolResult := &llm.ToolOutputPart{ID: "c1", Output: []llm.ContentPart{&llm.TextPart{Text: "file contents"}}}
	toolResult.SetRole(llm.RoleTool)

	prompt := &llm.TextPart{Text: "summarize the conversation"}
	prompt.SetRole(llm.RoleUser)

	msgs := anthropicConvertContents([]llm.ContentPart{text, toolUse, toolResult, prompt}, 0)
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2 (assistant text+tool_use, merged user): %+v", len(msgs), msgs)
	}

	last := msgs[1]
	if last.Role != string(llm.RoleUser) {
		t.Fatalf("last role = %q, want %q", last.Role, string(llm.RoleUser))
	}
	if len(last.Content) != 2 {
		t.Fatalf("last user turn has %d blocks, want 2 (tool_result then text): %+v", len(last.Content), last.Content)
	}
	if last.Content[0].Type != anthropicBlockTypeToolResult {
		t.Errorf("block 0 type = %q, want %q (tool results come first)", last.Content[0].Type, anthropicBlockTypeToolResult)
	}
	if last.Content[1].Type != anthropicBlockTypeText || last.Content[1].Text != "summarize the conversation" {
		t.Errorf("block 1 = %+v, want the following text part", last.Content[1])
	}
}

// TestAnthropicSplitsAssistantAndUser turns the same rule around: a plain user
// message after an assistant turn stays its own message (the merge only folds a
// user part into a tool turn, never two distinct turns).
func TestAnthropicSplitsAssistantAndUser(t *testing.T) {
	asst := &llm.TextPart{Text: "answer"}
	asst.SetRole(llm.RoleAssistant)
	user := &llm.TextPart{Text: "next question"}
	user.SetRole(llm.RoleUser)

	msgs := anthropicConvertContents([]llm.ContentPart{asst, user}, 0)
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2", len(msgs))
	}
	if msgs[0].Role != string(llm.RoleAssistant) || msgs[1].Role != string(llm.RoleUser) {
		t.Fatalf("roles = %q, %q; want assistant, user", msgs[0].Role, msgs[1].Role)
	}
}
