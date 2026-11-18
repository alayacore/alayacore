package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
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
	s := buildSession(SessionConfig{
		Output:          output,
		ModelConfigPath: configPath,
	}, nil)

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

// TestProviderBuiltByTheSessionReadsTheSessionLevel pins the seam that makes the
// reasoning level one value instead of two: createProviderFromConfig hands the
// provider &ms.reasoningLevel.
//
// Drop that one line and nothing fails. The provider keeps a level of its own —
// value 0, off — so :reasoning_level_set stops reaching the wire while the status
// bar, the CO and the session file all keep reporting the level the user chose.
// That is a silent setting, not a crash, which is exactly why it needs a test
// rather than a comment.
//
// The second half matters as much as the first: after the level changes, the SAME
// provider must build the next body from the new level. A copy would keep sending
// the old one for the rest of that provider's life.
func TestProviderBuiltByTheSessionReadsTheSessionLevel(t *testing.T) {
	var lastBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastBody = nil
		_ = json.NewDecoder(r.Body).Decode(&lastBody)
		w.Header().Set("Content-Type", "text/event-stream")
		// The minimum a stream needs to end cleanly: no blocks to close.
		_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()

	ms := newModelService(newModelManager(""), newRuntimeManager(""))
	ms.SetReasoningLevel(config.ReasoningLevelMax)

	provider, err := ms.createProviderFromConfig(&modelConfig{
		ProtocolType: "anthropic",
		APIKey:       "test-key",
		BaseURL:      server.URL,
		ModelName:    "test-model",
		Reasoning1:   json.RawMessage(`{"output_config":{"effort":"high"}}`),
		Reasoning2:   json.RawMessage(`{"output_config":{"effort":"max"}}`),
	})
	if err != nil {
		t.Fatalf("createProviderFromConfig: %v", err)
	}

	effortNow := func(t *testing.T) any {
		t.Helper()
		part := &llm.TextPart{Text: "hi"}
		part.SetRole(llm.RoleUser)
		events, err := provider.StreamMessages(context.Background(), []llm.ContentPart{part}, nil, "", "")
		if err != nil {
			t.Fatalf("StreamMessages: %v", err)
		}
		for range events {
		}
		outputConfig, ok := lastBody["output_config"].(map[string]any)
		if !ok {
			t.Fatalf("the request carries no output_config at all, so the provider is not reading the session's level: %v", lastBody)
		}
		return outputConfig["effort"]
	}

	if got := effortNow(t); got != "max" {
		t.Errorf("effort = %v, want \"max\" (level 2 is the session's level at construction)", got)
	}

	ms.SetReasoningLevel(config.ReasoningLevelNormal)
	if got := effortNow(t); got != "high" {
		t.Errorf("effort = %v, want \"high\" — the provider read a copy, not the shared level", got)
	}
}
