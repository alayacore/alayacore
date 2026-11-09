package providers_test

// The agent carries two system prompts — its own, and the operator's `--system` text — and this
// protocol has one slot to put them in. They are sent as one message, joined by a blank line. A
// server may read the slot literally and refuse a second one: llama.cpp's `--jinja` Qwen3.5
// templates raise "System message must be at the beginning." and answer every request with 500,
// which is permanent, so it outlives the retries. The tests below pin the shape at the wire — one
// system message, both texts, in the order the agent meant them — and that neither half invents a
// separator or an empty message when the other is missing.

import (
	"testing"

	"github.com/alayacore/alayacore/internal/llm/providers"
)

// openAIMessages is the request's messages, as objects, checked rather than assumed.
func openAIMessages(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()

	raw, ok := body["messages"].([]any)
	if !ok {
		t.Fatalf("messages is not an array: %#v", body["messages"])
	}
	messages := make([]map[string]any, 0, len(raw))
	for i, item := range raw {
		message, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("messages[%d] is not an object: %#v", i, item)
		}
		messages = append(messages, message)
	}
	return messages
}

func TestOpenAISystemPromptsTravelAsOneMessage(t *testing.T) {
	body := postOpenAI(t, providers.BaseConfig{}, nil, "You are the agent.", "You work on a canvas.")
	messages := openAIMessages(t, body)

	if got, want := messages[0]["content"], "You are the agent.\n\nYou work on a canvas."; got != want {
		t.Errorf("system content = %q, want %q", got, want)
	}
	if role := messages[1]["role"]; role != "user" {
		t.Errorf("messages[1].role = %v, want user", role)
	}

	// The count, not just the first message's content: a second system message anywhere after the
	// first is the shape that fails, whatever it says.
	for i, message := range messages {
		if message["role"] != "system" {
			continue
		}
		if i != 0 {
			t.Errorf("system message at index %d; a strict server requires the one at the beginning", i)
		}
	}
	if systems := countRole(messages, "system"); systems != 1 {
		t.Errorf("got %d system messages, want 1", systems)
	}
}

func countRole(messages []map[string]any, role any) int {
	n := 0
	for _, message := range messages {
		if message["role"] == role {
			n++
		}
	}
	return n
}

// One half alone is that text and nothing else: no separator out of nowhere, and no empty system
// message when neither prompt is set.
func TestOpenAISystemPromptHalfAlone(t *testing.T) {
	tests := []struct {
		name  string
		base  string
		extra string
		want  string // the first message's role, or its content when it is the system one
	}{
		{name: "base only", base: "You are the agent.", want: "You are the agent."},
		{name: "extra only", extra: "You work on a canvas.", want: "You work on a canvas."},
		{name: "neither", want: "user"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages := openAIMessages(t, postOpenAI(t, providers.BaseConfig{}, nil, tt.base, tt.extra))

			if tt.want == "user" {
				if got := countRole(messages, "system"); got != 0 {
					t.Fatalf("got %d system messages with no prompt set, want 0", got)
				}
				return
			}
			if role := messages[0]["role"]; role != "system" {
				t.Fatalf("messages[0].role = %v, want system", role)
			}
			if got := messages[0]["content"]; got != tt.want {
				t.Errorf("system content = %q, want %q", got, tt.want)
			}
			if got := countRole(messages, "system"); got != 1 {
				t.Errorf("got %d system messages, want 1", got)
			}
		})
	}
}
