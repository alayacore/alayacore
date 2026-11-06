package providers_test

// The Anthropic-format wire sends the thinking blocks the record holds and
// nothing else: a reasoning part becomes a `thinking` block (first in the
// message, per the API), and a message that produced no reasoning gets no block
// at all.
//
// An earlier version padded every assistant message with an empty `thinking`
// block, working around a DeepSeek V4 failure recorded on 2026-04-24. Probing
// the three Anthropic-format endpoints this tree can reach (2026-11) could not
// reproduce it: DeepSeek, Qwen and MiniMax all accept an assistant message whose
// content is only a tool_use, with no thinking block anywhere. Padding also
// claims the model thought nothing, which is not what the record says — so the
// padding is gone and this test keeps the honest shape, in both directions.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/llm/providers"
)

// wireBlock and wireMessage are the parts of the Anthropic request these tests
// read back.
type wireBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Thinking string `json:"thinking"`
}

type wireMessage struct {
	Role    string      `json:"role"`
	Content []wireBlock `json:"content"`
}

// anthropicRequestMessages runs one StreamMessages and returns the messages the
// provider actually put on the wire.
func anthropicRequestMessages(t *testing.T, history []llm.ContentPart) []wireMessage {
	t.Helper()

	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		_, _ = io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n")
		_, _ = io.WriteString(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		_, _ = io.WriteString(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n")
		_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	t.Cleanup(server.Close)

	provider, err := providers.NewAnthropic(providers.BaseConfig{APIKey: "k", BaseURL: server.URL, Model: "m"})
	if err != nil {
		t.Fatalf("NewAnthropic: %v", err)
	}
	provider.SetReasoningLevel(1)

	events, err := provider.StreamMessages(t.Context(), history, nil, "", "")
	if err != nil {
		t.Fatalf("StreamMessages: %v", err)
	}
	for _, err := range events {
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
	}

	var request struct {
		Messages []wireMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("unmarshal the request body: %v\n%s", err, body)
	}
	return request.Messages
}

// An assistant message that produced no reasoning carries no thinking block, and
// one that did carries exactly the block it produced — first, as the API wants.
func TestAnthropicSendsNoThinkingBlockForAnAssistantMessageWithoutReasoning(t *testing.T) {
	history := []llm.ContentPart{
		&llm.TextPart{Text: "hi", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
		&llm.TextPart{Text: "hello", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}},
		&llm.TextPart{Text: "again", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
	}

	messages := anthropicRequestMessages(t, history)
	if len(messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(messages))
	}
	for _, block := range messages[1].Content {
		if block.Type == "thinking" {
			t.Fatalf("assistant message carries an invented thinking block: %+v", messages[1].Content)
		}
	}
}

func TestAnthropicSendsTheThinkingBlockItHas(t *testing.T) {
	history := []llm.ContentPart{
		&llm.TextPart{Text: "hi", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
		&llm.ReasoningPart{Text: "half a thought", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}},
		&llm.TextPart{Text: "Continue", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
	}

	messages := anthropicRequestMessages(t, history)
	if len(messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(messages))
	}
	first := messages[1].Content
	if len(first) == 0 || first[0].Type != "thinking" {
		t.Fatalf("first block = %+v, want the reasoning as a thinking block", first)
	}
	if first[0].Thinking != "half a thought" {
		t.Errorf("thinking = %q, want the reasoning text", first[0].Thinking)
	}
}
