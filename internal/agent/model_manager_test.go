package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A config file that exists but cannot be read is reported through the same
// list as a parse error. The session's fallback (no models, "edit this file")
// would otherwise blame the file's contents for a permission problem, and the
// creation path must not touch a path that is already there.
func TestModelManagerUnreadableFileIsReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model.conf")
	// A directory where the config file goes: it exists, and reading it as a
	// file fails the same way for root and for everyone else — a chmod-based
	// test would pass vacuously when the suite runs as root.
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}

	mm := newModelManager(path)

	errs := mm.getLoadErrors()
	if len(errs) == 0 {
		t.Fatal("an unreadable model config was not reported")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "model.conf") {
		t.Errorf("load errors = %v, want the file named", errs)
	}
	if mm.hasModels() {
		t.Error("models were loaded from a config that could not be read")
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Error("the loader replaced a path that already existed")
	}
}

// A model config that is not there yet is created, not reported: a first run
// has to leave a usable default behind, and nothing was asked of the file.
func TestModelManagerMissingFileIsCreated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model.conf")

	mm := newModelManager(path)

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("model config was not created: %v", err)
	}
	if !mm.hasModels() {
		t.Errorf("no models loaded from the created default; load errors = %v", mm.getLoadErrors())
	}
	if errs := mm.getLoadErrors(); len(errs) != 0 {
		t.Errorf("creating the file reported errors: %v", errs)
	}
}

func TestParseModelConfig(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []modelConfig
	}{
		{
			name: "single model",
			input: `name: "OpenAI-compatible"
protocol_type: "openai"
base_url: "https://api.example.com/v1"
api_key: "test-key"
model_name: "your-128k-model"
context_limit: 131072`,
			expected: []modelConfig{
				{
					Name:         "OpenAI-compatible",
					ProtocolType: "openai",
					BaseURL:      "https://api.example.com/v1",
					APIKey:       "test-key",
					ModelName:    "your-128k-model",
					ContextLimit: 131072,
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
			expected: []modelConfig{
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
			expected: []modelConfig{
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
			expected: []modelConfig{
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
			result, msgs := parseModelConfig(tt.input, "model.conf")
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
		expected modelConfig
	}{
		{
			name: "complete model block",
			input: `name: "Test Model"
protocol_type: "openai"
base_url: "https://api.example.com"
api_key: "secret-key"
model_name: "gpt-4"
context_limit: 64000`,
			expected: modelConfig{
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
			expected: modelConfig{
				Name:         "Minimal Model",
				ProtocolType: "openai",
				BaseURL:      "https://api.example.com",
				ModelName:    "mini",
			},
		},
		{
			name:     "empty block",
			input:    "",
			expected: modelConfig{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			models, msgs := parseModelConfig(tt.input, "model.conf")
			_ = msgs
			var result modelConfig
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

	mm := newModelManager(configPath)

	// Check initial IDs (IDs start from 1; 0 is reserved as "no model")
	models := mm.getModels()
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
	newID := mm.AddModel(modelConfig{
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

	models = mm.getModels()
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

// TestAllRejectedFlagAcrossLoads keeps HasRejected consistent with what the
// reload left behind. It is the flag behind NoModelsErrorMessage, and the
// failure it guards is a message that blames the wrong cause: a stale false
// makes a config whose every block was rejected read as "no models configured",
// which sends the user looking for a missing file instead of a broken one.
//
// model.conf is the only way models enter a session, so the flag has exactly
// one writer — loadFromFile — and that is what this drives.
func TestAllRejectedFlagAcrossLoads(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "model.conf")
	write := func(t *testing.T, text string) {
		t.Helper()
		if err := os.WriteFile(configPath, []byte(text), 0600); err != nil {
			t.Fatalf("write config: %v", err)
		}
	}

	validA := `name: "Model A"
protocol_type: "openai"
base_url: "https://api.example.com"
api_key: "key1"
model_name: "model-a"
`
	// Missing every required field: parseable block, rejected model.
	broken := `name: "Broken"
`
	validC := `name: "Model C"
protocol_type: "anthropic"
base_url: "https://api.example.com"
api_key: "key3"
model_name: "model-c"
`

	mm := newModelManager(configPath)

	// A clean load leaves the flag clear.
	write(t, validA)
	if err := mm.loadFromFile(configPath); err != nil {
		t.Fatalf("load: %v", err)
	}
	if !mm.hasModels() || mm.hasRejected() {
		t.Fatalf("clean load: hasModels=%v hasRejected=%v, want true/false", mm.hasModels(), mm.hasRejected())
	}

	// Every block rejected: the flag must be set, or the next error message
	// claims nothing is configured.
	write(t, broken)
	if err := mm.loadFromFile(configPath); err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(mm.getLoadErrors()) == 0 {
		t.Fatal("expected validation messages for an all-rejected config")
	}
	if !mm.hasRejected() {
		t.Fatal("HasRejected must be true when every block was rejected")
	}

	// A valid load clears it again.
	write(t, validA)
	if err := mm.loadFromFile(configPath); err != nil {
		t.Fatalf("load: %v", err)
	}
	if mm.hasRejected() {
		t.Fatal("HasRejected must be false after a clean reload")
	}

	// Mixed: the valid block is kept, the broken one is reported, and the flag
	// stays clear because a usable model exists.
	write(t, broken+"---\n"+validC)
	if err := mm.loadFromFile(configPath); err != nil {
		t.Fatalf("load: %v", err)
	}
	if mm.hasRejected() {
		t.Fatal("HasRejected must be false when at least one model is valid")
	}
	models := mm.getModels()
	if len(models) != 1 || models[0].Name != "Model C" {
		t.Fatalf("expected only Model C, got %v", models)
	}
	msgs := mm.getLoadErrors()
	if len(msgs) == 0 || !strings.Contains(strings.Join(msgs, "\n"), "Broken") {
		t.Fatalf("expected a message naming the rejected block, got %v", msgs)
	}
}
