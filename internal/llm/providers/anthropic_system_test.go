package providers

import (
	"encoding/json"
	"testing"
)

func TestAnthropicSystemMessageArray(t *testing.T) {
	// Test that Anthropic API request structure supports system message array
	req := anthropicRequest{
		Model:    "claude-3-5-sonnet-20241022",
		Messages: []anthropicMessage{},
		System: []anthropicSystemMessage{
			{Type: "text", Text: "Default system prompt"},
			{Type: "text", Text: "Extra system prompt 1\n\nExtra system prompt 2"},
		},
		MaxTokens: 4096,
		Stream:    true,
	}

	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}

	t.Logf("Anthropic request:\n%s", string(data))

	// Verify system is an array
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	system, ok := parsed["system"].([]interface{})
	if !ok {
		t.Fatal("Expected system to be an array")
	}

	if len(system) != 2 {
		t.Fatalf("Expected 2 system messages, got %d", len(system))
	}

	// Verify first message
	first := system[0].(map[string]interface{})
	if first["type"] != "text" {
		t.Errorf("Expected type 'text', got %v", first["type"])
	}
	if first["text"] != "Default system prompt" {
		t.Errorf("Expected 'Default system prompt', got %v", first["text"])
	}

	// Verify second message
	second := system[1].(map[string]interface{})
	if second["text"] != "Extra system prompt 1\n\nExtra system prompt 2" {
		t.Errorf("Expected merged extra prompts, got %v", second["text"])
	}
}

func TestAnthropicEmptyExtraPrompt(t *testing.T) {
	// Test that empty extra prompt results in only one system message
	req := anthropicRequest{
		Model:    "claude-3-5-sonnet-20241022",
		Messages: []anthropicMessage{},
		System: []anthropicSystemMessage{
			{Type: "text", Text: "Default system prompt"},
		},
		MaxTokens: 4096,
		Stream:    true,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	system := parsed["system"].([]interface{})
	if len(system) != 1 {
		t.Errorf("Expected 1 system message when extra is empty, got %d", len(system))
	}
}
