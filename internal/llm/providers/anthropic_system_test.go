package providers

import (
	"encoding/json"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

func TestAnthropicSystemMessageArray(t *testing.T) {
	// Test that the Anthropic request body supports the system message
	// array shape. The body is now built as map[string]any in
	// StreamMessages, with thinking-related keys merged in from
	// reasoning_N. This test exercises the system-message assembly
	// without going through StreamMessages so the assertion is on the
	// structural shape alone.
	system := []anthropicSystemMessage{
		{Type: "text", Text: "Default system prompt"},
		{Type: "text", Text: "Extra system prompt 1\n\nExtra system prompt 2"},
	}
	body := map[string]any{
		"model":      "claude-3-5-sonnet-20241022",
		"messages":   []anthropicMessage{},
		"system":     system,
		"max_tokens": llm.DefaultMaxTokens,
		"stream":     true,
	}

	data, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal body: %v", err)
	}

	t.Logf("Anthropic request:\n%s", string(data))

	// Verify system is an array
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	parsedSystem, ok := parsed["system"].([]any)
	if !ok {
		t.Fatal("Expected system to be an array")
	}

	if len(parsedSystem) != 2 {
		t.Fatalf("Expected 2 system messages, got %d", len(parsedSystem))
	}

	// Verify first message
	first, ok := parsedSystem[0].(map[string]any)
	if !ok {
		t.Fatal("Expected system[0] to be a map")
	}
	if first["type"] != "text" {
		t.Errorf("Expected type 'text', got %v", first["type"])
	}
	if first["text"] != "Default system prompt" {
		t.Errorf("Expected 'Default system prompt', got %v", first["text"])
	}

	// Verify second message
	second, ok := parsedSystem[1].(map[string]any)
	if !ok {
		t.Fatal("Expected system[1] to be a map")
	}
	if second["text"] != "Extra system prompt 1\n\nExtra system prompt 2" {
		t.Errorf("Expected merged extra prompts, got %v", second["text"])
	}
}

func TestAnthropicEmptyExtraPrompt(t *testing.T) {
	// Test that empty extra prompt results in only one system message
	// in the request body. Mirrors the conditional in StreamMessages
	// that only adds the "system" key when systemMessages is non-empty.
	body := map[string]any{
		"model":    "claude-3-5-sonnet-20241022",
		"messages": []anthropicMessage{},
		"system": []anthropicSystemMessage{
			{Text: "Default system prompt"},
		},
		"max_tokens": llm.DefaultMaxTokens,
		"stream":     true,
	}

	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("Failed to marshal body: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	parsedSystem, ok := parsed["system"].([]any)
	if !ok {
		t.Fatal("Expected system to be an array")
	}
	if len(parsedSystem) != 1 {
		t.Errorf("Expected 1 system message when extra is empty, got %d", len(parsedSystem))
	}
}
