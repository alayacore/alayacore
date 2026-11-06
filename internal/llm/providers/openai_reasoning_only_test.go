package providers_test

// The one assistant-message rule an endpoint here actually enforces.
//
// An assistant message carrying neither `content` nor `tool_calls` is rejected by
// DeepSeek with
//
//	Invalid assistant message: content or tool_calls must be set
//
// and that is the shape a stream cut mid-reasoning leaves in history — an
// assistant part holding nothing but thinking — so it is the rule that decides
// whether a resumed task can be sent at all. openaiConvertContents answers it by
// emitting `"content": ""`, and this test pins the bytes rather than the intent.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/llm/providers"
)

// captureRequest runs one StreamMessages against a stub that records the request
// body, so the assertion is about what the provider actually sends.
func captureRequest(t *testing.T, history []llm.ContentPart, reasoning bool) []map[string]any {
	t.Helper()

	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)

	provider, err := providers.NewOpenAI(providers.BaseConfig{
		APIKey:         "k",
		BaseURL:        server.URL,
		ReasoningField: "reasoning_content",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reasoning {
		provider.SetReasoningLevel(1)
	}

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
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("unmarshal the request body: %v\n%s", err, body)
	}
	return request.Messages
}

// A history whose assistant message holds only reasoning — the salvage of a
// stream that died mid-thinking — must go out with content present and empty.
func TestOpenAIReasoningOnlyAssistantMessageCarriesEmptyContent(t *testing.T) {
	history := []llm.ContentPart{
		&llm.TextPart{Text: "hi", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
		&llm.ReasoningPart{Text: "half a thought", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}},
		&llm.TextPart{Text: "Continue", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
	}

	for _, reasoning := range []bool{true, false} {
		messages := captureRequest(t, history, reasoning)
		if len(messages) != 3 {
			t.Fatalf("messages = %d, want 3", len(messages))
		}
		assistant := messages[1]
		content, present := assistant["content"]
		if !present {
			t.Fatalf("reasoning=%v: the assistant message carries no content key: %#v — DeepSeek rejects one with neither content nor tool_calls", reasoning, assistant)
		}
		if content != "" {
			t.Errorf("reasoning=%v: content = %#v, want an empty string", reasoning, content)
		}
		if reasoning {
			if got, ok := assistant["reasoning_content"]; !ok || got == "" {
				t.Errorf("reasoning_content = %#v, want the reasoning text when reasoning mode is on", got)
			}
		}
	}
}

// An assistant message that carries a tool call needs no content: tool_calls is
// what the endpoint asks for, and inventing an empty string there would be a
// second field saying nothing.
func TestOpenAIToolCallAssistantMessageNeedsNoContent(t *testing.T) {
	history := []llm.ContentPart{
		&llm.TextPart{Text: "hi", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
		&llm.ToolInputPart{ID: "c1", Name: "noop", Input: json.RawMessage(`{}`), ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}},
	}

	messages := captureRequest(t, history, false)
	if len(messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(messages))
	}
	assistant := messages[1]
	if _, ok := assistant["tool_calls"]; !ok {
		t.Fatalf("the assistant message lost its tool_calls: %#v", assistant)
	}
	if content, present := assistant["content"]; present && content != nil {
		t.Errorf("content = %#v, want absent or null beside tool_calls", content)
	}
}
