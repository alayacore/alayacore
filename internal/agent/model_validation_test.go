package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseModelConfig_InvalidProtocolType(t *testing.T) {
	input := `name: "Bad Proto"
protocol_type: "foobar"
base_url: "https://api.example.com"
api_key: "key"
model_name: "model"`

	models, msgs := parseModelConfig(input, "model.conf")
	if len(models) != 0 {
		t.Fatalf("expected 0 models (broken model should be skipped), got %d", len(models))
	}
	found := false
	for _, m := range msgs {
		if strings.Contains(m, "unknown protocol_type") && strings.Contains(m, "foobar") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error about unknown protocol_type, got: %v", msgs)
	}
}

func TestParseModelConfig_MissingRequiredFields(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantError   string
		dontWantErr string
		wantModels  int
	}{
		{
			name: "missing protocol_type",
			input: `name: "No Proto"
base_url: "https://api.example.com"
model_name: "model"`,
			wantError:  "missing required field protocol_type",
			wantModels: 0,
		},
		{
			name: "missing base_url",
			input: `name: "No URL"
protocol_type: "openai"
model_name: "model"`,
			wantError:  "missing required field base_url",
			wantModels: 0,
		},
		{
			name: "missing model_name",
			input: `name: "No Model"
protocol_type: "openai"
base_url: "https://api.example.com"`,
			wantError:  "missing required field model_name",
			wantModels: 0,
		},
		{
			name: "all required present",
			input: `name: "Complete"
protocol_type: "openai"
base_url: "https://api.example.com"
model_name: "model"`,
			dontWantErr: "missing required field",
			wantModels:  1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			models, msgs := parseModelConfig(tt.input, "model.conf")
			if len(models) != tt.wantModels {
				t.Errorf("expected %d models, got %d", tt.wantModels, len(models))
			}
			if tt.wantError != "" {
				found := false
				for _, m := range msgs {
					if strings.Contains(m, tt.wantError) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error containing %q, got: %v", tt.wantError, msgs)
				}
			}
			if tt.dontWantErr != "" {
				for _, m := range msgs {
					if strings.Contains(m, tt.dontWantErr) {
						t.Errorf("unexpected error: %s", m)
						break
					}
				}
			}
		})
	}
}

func TestParseModelConfig_InvalidBaseURL(t *testing.T) {
	input := `name: "Bad URL"
protocol_type: "openai"
base_url: ":://bad"
api_key: "key"
model_name: "model"`

	models, msgs := parseModelConfig(input, "model.conf")
	if len(models) != 0 {
		t.Fatalf("expected 0 models (broken model should be skipped), got %d", len(models))
	}
	found := false
	for _, m := range msgs {
		if strings.Contains(m, "invalid base_url") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error about invalid base_url, got: %v", msgs)
	}
}

func TestParseModelConfig_ParseErrors(t *testing.T) {
	input := `name: "Bad Int"
protocol_type: "openai"
base_url: "https://api.example.com"
model_name: "model"
context_limit: abc`

	_, msgs := parseModelConfig(input, "model.conf")
	found := false
	for _, m := range msgs {
		if strings.Contains(m, "context_limit") && strings.Contains(m, "invalid integer") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected parse error about invalid integer for context_limit, got: %v", msgs)
	}
}

func TestParseModelConfig_MixedValidAndInvalid(t *testing.T) {
	input := `name: "Bad"
protocol_type: "foobar"
base_url: "https://api.example.com"
model_name: "model"
---
name: "Good"
protocol_type: "openai"
base_url: "https://api.example.com"
model_name: "gpt-4"
`
	models, msgs := parseModelConfig(input, "model.conf")
	if len(models) != 1 {
		t.Fatalf("expected 1 model (bad one skipped), got %d", len(models))
	}
	if models[0].Name != "Good" {
		t.Errorf("expected model name %q, got %q", "Good", models[0].Name)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "unknown protocol_type") {
		t.Errorf("expected protocol_type error, got: %s", msgs[0])
	}
}

func TestModelManagerGetLoadErrors(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "model.conf")

	// Write a config with intentional issues
	badConfig := `name: "Bad Model"
protocol_type: "unknown_type"
base_url: ":://invalid"
model_name: ""
`
	if err := os.WriteFile(configPath, []byte(badConfig), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	mm := newModelManager(configPath)
	msgs := mm.getLoadErrors()

	if len(msgs) == 0 {
		t.Fatal("expected errors from invalid config, got none")
	}

	// Check specific errors are present
	var foundProto, foundURL, foundModelName bool
	for _, m := range msgs {
		if strings.Contains(m, "unknown protocol_type") {
			foundProto = true
		}
		if strings.Contains(m, "invalid base_url") {
			foundURL = true
		}
		if strings.Contains(m, "missing required field model_name") {
			foundModelName = true
		}
	}
	if !foundProto {
		t.Errorf("missing protocol_type error in: %v", msgs)
	}
	if !foundURL {
		t.Errorf("missing base_url error in: %v", msgs)
	}
	if !foundModelName {
		t.Errorf("missing model_name error in: %v", msgs)
	}

	// Broken model should not be in the list
	if mm.ModelCount() != 0 {
		t.Errorf("expected 0 models, got %d", mm.ModelCount())
	}
}

func TestModelManagerNoErrorsOnValidConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "model.conf")

	goodConfig := `name: "Good Model"
protocol_type: "openai"
base_url: "https://api.example.com"
api_key: "key"
model_name: "gpt-4"
`
	if err := os.WriteFile(configPath, []byte(goodConfig), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	mm := newModelManager(configPath)
	msgs := mm.getLoadErrors()

	if len(msgs) != 0 {
		t.Errorf("expected no errors from valid config, got: %v", msgs)
	}
	if mm.ModelCount() != 1 {
		t.Errorf("expected 1 model, got %d", mm.ModelCount())
	}
}
