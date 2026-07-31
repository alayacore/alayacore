package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/config"
)

func TestParseModelConfig(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []config.ModelConfig
	}{
		{
			name: "single model",
			input: `name: "OpenAI GPT-4o"
protocol_type: "openai"
base_url: "https://api.openai.com/v1"
api_key: "test-key"
model_name: "gpt-4o"
context_limit: 128000`,
			expected: []config.ModelConfig{
				{
					Name:         "OpenAI GPT-4o",
					ProtocolType: "openai",
					BaseURL:      "https://api.openai.com/v1",
					APIKey:       "test-key",
					ModelName:    "gpt-4o",
					ContextLimit: 128000,
				},
			},
		},
		{
			name: "multiple models",
			input: `name: "OpenAI GPT-4o"
protocol_type: "openai"
base_url: "https://api.openai.com/v1"
api_key: "key1"
model_name: "gpt-4o"
---
name: "Ollama (127.0.0.1) / GPT OSS 20B"
protocol_type: "anthropic"
base_url: "http://127.0.0.1:11434"
api_key: "key2"
model_name: "gpt-oss:20b"`,
			expected: []config.ModelConfig{
				{
					Name:         "OpenAI GPT-4o",
					ProtocolType: "openai",
					BaseURL:      "https://api.openai.com/v1",
					APIKey:       "key1",
					ModelName:    "gpt-4o",
				},
				{
					Name:         "Ollama (127.0.0.1) / GPT OSS 20B",
					ProtocolType: "anthropic",
					BaseURL:      "http://127.0.0.1:11434",
					APIKey:       "key2",
					ModelName:    "gpt-oss:20b",
				},
			},
		},
		{
			name: "with comments and empty lines",
			input: `# This is a comment
name: "Test Model"
protocol_type: "openai"

base_url: "https://api.example.com"
api_key: "secret"
model_name: "test"`,
			expected: []config.ModelConfig{
				{
					Name:         "Test Model",
					ProtocolType: "openai",
					BaseURL:      "https://api.example.com",
					APIKey:       "secret",
					ModelName:    "test",
				},
			},
		},
		{
			name: "single quotes",
			input: `name: 'Test Model'
protocol_type: 'anthropic'
base_url: 'https://api.example.com'
api_key: 'secret'
model_name: 'claude'`,
			expected: []config.ModelConfig{
				{
					Name:         "Test Model",
					ProtocolType: "anthropic",
					BaseURL:      "https://api.example.com",
					APIKey:       "secret",
					ModelName:    "claude",
				},
			},
		},
		{
			name:     "empty input",
			input:    ``,
			expected: nil,
		},
		{
			name: "only whitespace and comments",
			input: `# Comment
  
# Another comment`,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, msgs := parseModelConfig(tt.input)
			_ = msgs

			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d models, got %d", len(tt.expected), len(result))
			}

			for i, model := range result {
				if model.Name != tt.expected[i].Name {
					t.Errorf("model %d: expected Name=%q, got %q", i, tt.expected[i].Name, model.Name)
				}
				if model.ProtocolType != tt.expected[i].ProtocolType {
					t.Errorf("model %d: expected ProtocolType=%q, got %q", i, tt.expected[i].ProtocolType, model.ProtocolType)
				}
				if model.BaseURL != tt.expected[i].BaseURL {
					t.Errorf("model %d: expected BaseURL=%q, got %q", i, tt.expected[i].BaseURL, model.BaseURL)
				}
				if model.APIKey != tt.expected[i].APIKey {
					t.Errorf("model %d: expected APIKey=%q, got %q", i, tt.expected[i].APIKey, model.APIKey)
				}
				if model.ModelName != tt.expected[i].ModelName {
					t.Errorf("model %d: expected ModelName=%q, got %q", i, tt.expected[i].ModelName, model.ModelName)
				}
				if model.ContextLimit != tt.expected[i].ContextLimit {
					t.Errorf("model %d: expected ContextLimit=%d, got %d", i, tt.expected[i].ContextLimit, model.ContextLimit)
				}
			}
		})
	}
}

func TestParseModelBlock(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected config.ModelConfig
	}{
		{
			name: "complete model block",
			input: `name: "Test Model"
protocol_type: "openai"
base_url: "https://api.example.com"
api_key: "secret-key"
model_name: "gpt-4"
context_limit: 64000`,
			expected: config.ModelConfig{
				Name:         "Test Model",
				ProtocolType: "openai",
				BaseURL:      "https://api.example.com",
				APIKey:       "secret-key",
				ModelName:    "gpt-4",
				ContextLimit: 64000,
			},
		},
		{
			name: "partial model block",
			input: `name: "Minimal Model"
protocol_type: "openai"
base_url: "https://api.example.com"
model_name: "mini"`,
			expected: config.ModelConfig{
				Name:         "Minimal Model",
				ProtocolType: "openai",
				BaseURL:      "https://api.example.com",
				ModelName:    "mini",
			},
		},
		{
			name:     "empty block",
			input:    "",
			expected: config.ModelConfig{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			models, msgs := parseModelConfig(tt.input)
			_ = msgs
			var result config.ModelConfig
			if len(models) > 0 {
				result = models[0]
			}

			if result.Name != tt.expected.Name {
				t.Errorf("expected Name=%q, got %q", tt.expected.Name, result.Name)
			}
			if result.ProtocolType != tt.expected.ProtocolType {
				t.Errorf("expected ProtocolType=%q, got %q", tt.expected.ProtocolType, result.ProtocolType)
			}
			if result.BaseURL != tt.expected.BaseURL {
				t.Errorf("expected BaseURL=%q, got %q", tt.expected.BaseURL, result.BaseURL)
			}
			if result.APIKey != tt.expected.APIKey {
				t.Errorf("expected APIKey=%q, got %q", tt.expected.APIKey, result.APIKey)
			}
			if result.ModelName != tt.expected.ModelName {
				t.Errorf("expected ModelName=%q, got %q", tt.expected.ModelName, result.ModelName)
			}
			if result.ContextLimit != tt.expected.ContextLimit {
				t.Errorf("expected ContextLimit=%d, got %d", tt.expected.ContextLimit, result.ContextLimit)
			}
		})
	}
}

func TestModelManagerReloadResetsIDs(t *testing.T) {
	// Create a temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "model.conf")

	config1 := `name: "Model A"
protocol_type: "openai"
base_url: "https://api.example.com"
api_key: "key1"
model_name: "model-a"
---
name: "Model B"
protocol_type: "anthropic"
base_url: "https://api.example.com"
api_key: "key2"
model_name: "model-b"
`
	if err := os.WriteFile(configPath, []byte(config1), 0600); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	mm := NewModelManager(configPath)

	// Check initial IDs (IDs start from 1; 0 is reserved as "no model")
	models := mm.GetModels()
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].ID != 1 {
		t.Errorf("expected first model ID to be 1, got %d", models[0].ID)
	}
	if models[1].ID != 2 {
		t.Errorf("expected second model ID to be 2, got %d", models[1].ID)
	}

	// Add a model to bump the nextID counter
	newID := mm.AddModel(config.ModelConfig{
		Name:         "Model C",
		ProtocolType: "openai",
		BaseURL:      "https://api.example.com",
		APIKey:       "key3",
		ModelName:    "model-c",
	})
	if newID != 3 {
		t.Errorf("expected new model ID to be 3, got %d", newID)
	}

	// Now reload from file - IDs should reset to 1, 2
	if err := mm.Reload(); err != nil {
		t.Fatalf("failed to reload: %v", err)
	}

	models = mm.GetModels()
	if len(models) != 2 {
		t.Fatalf("expected 2 models after reload, got %d", len(models))
	}
	if models[0].ID != 1 {
		t.Errorf("expected first model ID to be 1 after reload, got %d", models[0].ID)
	}
	if models[1].ID != 2 {
		t.Errorf("expected second model ID to be 2 after reload, got %d", models[1].ID)
	}
}

// validModelJSON builds a model_sync JSON payload from the given models.
func validModelJSON(t *testing.T, models ...config.ModelConfig) string {
	t.Helper()
	data, err := json.Marshal(models)
	if err != nil {
		t.Fatalf("marshal models: %v", err)
	}
	return string(data)
}

// TestSyncFromContentRejectionFlag verifies HasRejected is kept consistent
// across :model_sync outcomes:
//   - an all-rejected sync sets HasRejected (regression: it was left stale,
//     so the error message would claim "no models configured" instead of
//     "all models rejected");
//   - a later valid sync clears it;
//   - an empty sync clears it;
//   - a mixed sync keeps the valid models and does not flag rejection.
func TestSyncFromContentRejectionFlag(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "model.conf")
	if err := os.WriteFile(configPath, []byte(
		`name: "Model A"
protocol_type: "openai"
base_url: "https://api.example.com"
api_key: "key1"
model_name: "model-a"
`), 0600); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	mm := NewModelManager(configPath)
	if !mm.HasModels() {
		t.Fatal("expected model A to load")
	}
	if mm.HasRejected() {
		t.Fatal("expected HasRejected=false after clean load")
	}

	invalid := config.ModelConfig{Name: "Broken"} // missing required fields

	// 1. All-rejected sync: flag must become true, current models untouched.
	msgs := mm.SyncFromContent(validModelJSON(t, invalid))
	if len(msgs) == 0 {
		t.Fatal("expected validation messages for all-rejected sync")
	}
	if !mm.HasRejected() {
		t.Fatal("HasRejected must be true after an all-rejected sync")
	}
	if !mm.HasModels() {
		t.Fatal("all-rejected sync must not wipe the current models")
	}

	// 2. Valid sync clears the flag.
	msgs = mm.SyncFromContent(validModelJSON(t, config.ModelConfig{
		Name:         "Model B",
		ProtocolType: "openai",
		BaseURL:      "https://api.example.com",
		APIKey:       "key2",
		ModelName:    "model-b",
	}))
	if len(msgs) != 0 {
		t.Fatalf("expected no validation messages, got %v", msgs)
	}
	if mm.HasRejected() {
		t.Fatal("HasRejected must be false after a valid sync")
	}
	if got := len(mm.GetModels()); got != 1 {
		t.Fatalf("expected 1 model after valid sync, got %d", got)
	}

	// 3. Empty sync clears the flag and leaves no models.
	msgs = mm.SyncFromContent(validModelJSON(t))
	if len(msgs) != 0 {
		t.Fatalf("expected no messages for empty sync, got %v", msgs)
	}
	if mm.HasRejected() {
		t.Fatal("HasRejected must be false after an empty sync")
	}
	if mm.HasModels() {
		t.Fatal("expected no models after empty sync")
	}

	// 4. All-rejected again from empty state — flag true, still no models.
	msgs = mm.SyncFromContent(validModelJSON(t, invalid))
	if len(msgs) == 0 {
		t.Fatal("expected validation messages for all-rejected sync from empty state")
	}
	if !mm.HasRejected() {
		t.Fatal("HasRejected must be true after all-rejected sync from empty state")
	}

	// 5. Mixed sync: valid models kept, flag cleared.
	msgs = mm.SyncFromContent(validModelJSON(t, invalid, config.ModelConfig{
		Name:         "Model C",
		ProtocolType: "anthropic",
		BaseURL:      "https://api.example.com",
		APIKey:       "key3",
		ModelName:    "model-c",
	}))
	if len(msgs) == 0 {
		t.Fatal("expected validation messages for the rejected model in mixed sync")
	}
	if mm.HasRejected() {
		t.Fatal("HasRejected must be false when at least one model is valid")
	}
	models := mm.GetModels()
	if len(models) != 1 || models[0].Name != "Model C" {
		t.Fatalf("expected only Model C, got %v", models)
	}
	if !strings.Contains(msgs[0], "Broken") && !strings.Contains(msgs[0], "missing") {
		t.Fatalf("expected rejection message mentioning the broken model, got %v", msgs)
	}
}
