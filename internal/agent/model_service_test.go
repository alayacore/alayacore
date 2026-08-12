package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveActiveModelSyncsContextLimit guards against the status bar
// showing context usage without the total limit (e.g. "400.0K" instead of
// "400.0K/1M 40.0%"): contextLimit must be populated from the resolved
// model during ResolveActiveModel, because agent creation (SwitchModel) is
// lazy (first task) and the startup model broadcast happens before it.
func TestResolveActiveModelSyncsContextLimit(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "model.conf")
	config := `name: "Big Model"
protocol_type: "openai"
base_url: "https://api.example.com"
api_key: "key1"
model_name: "big-model"
context_limit: 1000000
`
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	ms := newModelService(newModelManager(configPath), newRuntimeManager(""))
	ms.ResolveActiveModel()

	if ms.contextLimit != 1000000 {
		t.Errorf("contextLimit = %d, want 1000000 (must be synced from resolved model before SwitchModel)", ms.contextLimit)
	}
	if model := ms.ActiveModel(); model == nil {
		t.Fatal("expected a resolved active model")
	}
}

// TestNewSessionStartupBroadcastCarriesContextLimit reproduces the reported
// status bar bug end-to-end: the model system message broadcast at session
// construction must include the active model's context_limit so the TUI can
// render "tokens/limit (pct%)". Before the fix it broadcast context_limit 0
// (SwitchModel had not run yet), leaving the status bar at "tokens" only.
func TestNewSessionStartupBroadcastCarriesContextLimit(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "model.conf")
	config := `name: "Big Model"
protocol_type: "openai"
base_url: "https://api.example.com"
api_key: "key1"
model_name: "big-model"
context_limit: 1000000
`
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	output := &MockOutput{}
	s := newSession(SessionConfig{
		Output:          output,
		ModelConfigPath: configPath,
	})

	if s.modelService.contextLimit != 1000000 {
		t.Errorf("modelService.contextLimit = %d, want 1000000", s.modelService.contextLimit)
	}
	if s.ContextLimit != 1000000 {
		t.Errorf("s.ContextLimit = %d, want 1000000 (auto-summarize threshold source)", s.ContextLimit)
	}

	joined := strings.Join(output.Messages, "")
	if !strings.Contains(joined, `"context_limit":1000000`) {
		t.Errorf("startup model broadcast must carry context_limit 1000000, got:\n%s", joined)
	}
}
