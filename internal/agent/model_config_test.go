package agent

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/protocol"
)

func TestToModelInfosWireCompatible(t *testing.T) {
	// The wire format for model_list must serialize to exactly the same
	// JSON bytes as modelConfig, so the TLV protocol stays compatible.
	m := modelConfig{
		ID:           3,
		Name:         "Test",
		ProtocolType: "openai",
		BaseURL:      "http://x",
		APIKey:       "k",
		ModelName:    "model-a",
		ContextLimit: 200000,
		MaxTokens:    0, // zero value must still appear in the wire bytes
	}

	domainJSON, err := json.Marshal([]modelConfig{m})
	if err != nil {
		t.Fatal(err)
	}
	wireJSON, err := json.Marshal(toModelInfos([]modelConfig{m}))
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(domainJSON, wireJSON) {
		t.Errorf("wire JSON differs from domain JSON:\ndomain: %s\nwire:   %s", domainJSON, wireJSON)
	}

	var wire []map[string]any
	if err := json.Unmarshal(wireJSON, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire) != 1 {
		t.Fatalf("expected 1 model info, got %d", len(wire))
	}
	// Zero-value fields must be present (no omitempty drift).
	if _, ok := wire[0]["max_tokens"]; !ok {
		t.Errorf("max_tokens missing from wire JSON: %s", wireJSON)
	}
	if _, ok := wire[0]["context_limit"]; !ok {
		t.Errorf("context_limit missing from wire JSON: %s", wireJSON)
	}
}

// TestToModelInfosReasoningFieldsPropagated verifies the per-level
// reasoning_N raw JSON survives the domain→wire conversion. Adapters
// read these fields to render the model_list and forward them when
// the user picks a model — losing them in toModelInfos would silently
// disable reasoning on switch.
func TestToModelInfosReasoningFieldsPropagated(t *testing.T) {
	m := modelConfig{
		Name:         "Test",
		ProtocolType: "anthropic",
		BaseURL:      "http://x",
		APIKey:       "k",
		ModelName:    "model-a",
		Reasoning0:   json.RawMessage(`{"thinking":{"type":"disabled"}}`),
		Reasoning1:   json.RawMessage(`{"thinking":{"type":"enabled"},"output_config":{"effort":"high"}}`),
		Reasoning2:   json.RawMessage(`{"thinking":{"type":"enabled"},"output_config":{"effort":"max"}}`),
	}

	infos := toModelInfos([]modelConfig{m})
	if len(infos) != 1 {
		t.Fatalf("expected 1 model info, got %d", len(infos))
	}
	if !bytes.Equal(infos[0].Reasoning0, m.Reasoning0) {
		t.Errorf("Reasoning0 mismatch: got %s, want %s", infos[0].Reasoning0, m.Reasoning0)
	}
	if !bytes.Equal(infos[0].Reasoning1, m.Reasoning1) {
		t.Errorf("Reasoning1 mismatch: got %s, want %s", infos[0].Reasoning1, m.Reasoning1)
	}
	if !bytes.Equal(infos[0].Reasoning2, m.Reasoning2) {
		t.Errorf("Reasoning2 mismatch: got %s, want %s", infos[0].Reasoning2, m.Reasoning2)
	}

	// Round-tripping through JSON must preserve the raw payloads byte-for-byte
	// — adapters may decode them and re-marshal, and drift here would corrupt
	// the per-level wire configuration.
	wireBytes, err := json.Marshal(infos[0])
	if err != nil {
		t.Fatal(err)
	}
	var back protocol.ModelInfo
	if err := json.Unmarshal(wireBytes, &back); err != nil {
		t.Fatal(err)
	}
	for _, pair := range []struct {
		name string
		got  json.RawMessage
		want json.RawMessage
	}{
		{"Reasoning0", back.Reasoning0, m.Reasoning0},
		{"Reasoning1", back.Reasoning1, m.Reasoning1},
		{"Reasoning2", back.Reasoning2, m.Reasoning2},
	} {
		if !bytes.Equal(pair.got, pair.want) {
			t.Errorf("%s round-trip drift: got %s, want %s", pair.name, pair.got, pair.want)
		}
	}
}

// TestReasoningConfigsMap verifies the per-level RawMessage fields are
// collected into a level→raw map the provider consumes, with empty
// levels omitted so providers can short-circuit when nothing is set.
func TestReasoningConfigsMap(t *testing.T) {
	m := &modelConfig{
		Name:       "Test",
		Reasoning0: json.RawMessage(`{"thinking":{"type":"disabled"}}`),
		Reasoning2: json.RawMessage(`{"thinking":{"type":"enabled"},"output_config":{"effort":"max"}}`),
		// Reasoning1 left empty
	}

	configs := m.ReasoningConfigs()
	if _, ok := configs[config.ReasoningLevelOff]; !ok {
		t.Errorf("ReasoningLevelOff missing")
	}
	if _, ok := configs[config.ReasoningLevelNormal]; ok {
		t.Errorf("ReasoningLevelNormal should be absent (empty field)")
	}
	if _, ok := configs[config.ReasoningLevelMax]; !ok {
		t.Errorf("ReasoningLevelMax missing")
	}
}

// TestReasoningConfigsMapAllEmpty verifies that when no reasoning_N is
// configured, ReasoningConfigs returns nil so providers can skip the
// merge entirely.
func TestReasoningConfigsMapAllEmpty(t *testing.T) {
	m := &modelConfig{Name: "Test"}
	if got := m.ReasoningConfigs(); got != nil {
		t.Errorf("expected nil map when all reasoning_N empty, got %v", got)
	}
}

func TestFormatModelList_Empty(t *testing.T) {
	out := formatModelList(nil)
	if out != "" {
		t.Errorf("expected empty string, got %q", out)
	}
	out = formatModelList([]modelConfig{})
	if out != "" {
		t.Errorf("expected empty string, got %q", out)
	}
}

func TestFormatModelList_Single(t *testing.T) {
	models := []modelConfig{
		{
			Name:         "test-model",
			ProtocolType: "anthropic",
			BaseURL:      "http://localhost:11434",
			APIKey:       "nokey",
			ModelName:    "test",
			ContextLimit: 64000,
		},
	}
	out := formatModelList(models)

	if !strings.Contains(out, `name: "test-model"`) {
		t.Errorf("missing name, got: %s", out)
	}
	if !strings.Contains(out, `protocol_type: "anthropic"`) {
		t.Errorf("missing protocol_type, got: %s", out)
	}
	if !strings.Contains(out, `base_url: "http://localhost:11434"`) {
		t.Errorf("missing base_url, got: %s", out)
	}
	if !strings.Contains(out, `api_key: "nokey"`) {
		t.Errorf("missing api_key, got: %s", out)
	}
	if !strings.Contains(out, `model_name: "test"`) {
		t.Errorf("missing model_name, got: %s", out)
	}
	if !strings.Contains(out, `context_limit: 64000`) {
		t.Errorf("missing context_limit, got: %s", out)
	}

	// No trailing blank lines
	if strings.HasSuffix(out, "\n\n") {
		t.Errorf("trailing blank line: %q", out)
	}
	// Ends with single newline
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("missing trailing newline: %q", out)
	}
	// No leading/trailing --- for single model
	if strings.HasPrefix(out, "---") {
		t.Errorf("unexpected leading ---: %q", out)
	}
	if strings.Count(out, "---") > 0 {
		t.Errorf("unexpected --- for single model: %q", out)
	}
}

func TestFormatModelList_Multiple(t *testing.T) {
	models := []modelConfig{
		{Name: "model-a", ProtocolType: "openai", BaseURL: "http://a", APIKey: "k", ModelName: "m-a"},
		{Name: "model-b", ProtocolType: "anthropic", BaseURL: "http://b", APIKey: "k", ModelName: "m-b"},
	}
	out := formatModelList(models)

	if !strings.Contains(out, `name: "model-a"`) {
		t.Errorf("missing model-a, got: %s", out)
	}
	if !strings.Contains(out, `name: "model-b"`) {
		t.Errorf("missing model-b, got: %s", out)
	}

	// Separated by ---
	if !strings.Contains(out, "\n---\n") {
		t.Errorf("missing --- separator, got: %s", out)
	}

	// No blank line before --- (FormatKeyValue trailing \n was trimmed)
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if line == "---" && i > 0 {
			if lines[i-1] == "" {
				t.Errorf("blank line before --- at line %d", i)
			}
		}
	}
}

func TestFormatModelList_OmitsZeroValues(t *testing.T) {
	models := []modelConfig{
		{
			Name:         "test",
			ProtocolType: "openai",
			BaseURL:      "http://localhost",
			APIKey:       "k",
			ModelName:    "m",
			// ContextLimit and MaxTokens are 0 (zero) → omitempty → not written
		},
	}
	out := formatModelList(models)

	if strings.Contains(out, "context_limit:") {
		t.Errorf("expected context_limit to be omitted (0 value), got: %s", out)
	}
	if strings.Contains(out, "max_tokens:") {
		t.Errorf("expected max_tokens to be omitted (0 value), got: %s", out)
	}
}

func TestFormatModelList_OmitsID(t *testing.T) {
	models := []modelConfig{
		{
			ID:           42,
			Name:         "test",
			ProtocolType: "openai",
			BaseURL:      "http://localhost",
			APIKey:       "k",
			ModelName:    "m",
		},
	}
	out := formatModelList(models)

	// ID has config:"-" tag, should never appear in output
	if strings.Contains(out, "id:") {
		t.Errorf("expected id to be omitted (config:\"-\"), got: %s", out)
	}
}

func TestParseModelList_Empty(t *testing.T) {
	models, errs := parseModelList("", "model.conf")
	if len(models) != 0 {
		t.Errorf("expected 0 models, got %d", len(models))
	}
	if len(errs) != 0 {
		t.Errorf("expected 0 errors, got %d: %v", len(errs), errs)
	}

	models, _ = parseModelList("  \n  \n  ", "model.conf")
	if len(models) != 0 {
		t.Errorf("expected 0 models for whitespace, got %d", len(models))
	}
}

func TestParseModelList_Single(t *testing.T) {
	content := `name: "test-model"
protocol_type: "anthropic"
base_url: "http://localhost:11434"
api_key: "nokey"
model_name: "test"
context_limit: 64000
`
	models, errs := parseModelList(content, "model.conf")
	if len(errs) != 0 {
		t.Errorf("expected 0 errors, got %d: %v", len(errs), errs)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	m := models[0]
	if m.Name != "test-model" {
		t.Errorf("Name = %q, want %q", m.Name, "test-model")
	}
	if m.ProtocolType != "anthropic" {
		t.Errorf("ProtocolType = %q, want %q", m.ProtocolType, "anthropic")
	}
	if m.BaseURL != "http://localhost:11434" {
		t.Errorf("BaseURL = %q, want %q", m.BaseURL, "http://localhost:11434")
	}
	if m.ModelName != "test" {
		t.Errorf("ModelName = %q, want %q", m.ModelName, "test")
	}
	if m.ContextLimit != 64000 {
		t.Errorf("ContextLimit = %d, want %d", m.ContextLimit, 64000)
	}
}

// TestParseModelList_ReasoningConfigs verifies the reasoning_N fields
// are parsed as raw provider-level JSON, parsed correctly across all
// three levels, and converted to the level→raw map the provider
// consumes. This is the entry point for the data-driven thinking config.
func TestParseModelList_ReasoningConfigs(t *testing.T) {
	content := `name: "test"
protocol_type: "anthropic"
base_url: "http://x"
api_key: "k"
model_name: "m"
reasoning_0: {"thinking":{"type":"disabled"}}
reasoning_1: {"thinking":{"type":"enabled"},"output_config":{"effort":"high"}}
reasoning_2: {"thinking":{"type":"enabled"},"output_config":{"effort":"max"}}
`
	models, errs := parseModelList(content, "model.conf")
	if len(errs) != 0 {
		t.Errorf("expected 0 errors, got %d: %v", len(errs), errs)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	m := models[0]

	wantR0 := json.RawMessage(`{"thinking":{"type":"disabled"}}`)
	wantR1 := json.RawMessage(`{"thinking":{"type":"enabled"},"output_config":{"effort":"high"}}`)
	wantR2 := json.RawMessage(`{"thinking":{"type":"enabled"},"output_config":{"effort":"max"}}`)
	if !bytes.Equal(m.Reasoning0, wantR0) {
		t.Errorf("Reasoning0 = %s, want %s", m.Reasoning0, wantR0)
	}
	if !bytes.Equal(m.Reasoning1, wantR1) {
		t.Errorf("Reasoning1 = %s, want %s", m.Reasoning1, wantR1)
	}
	if !bytes.Equal(m.Reasoning2, wantR2) {
		t.Errorf("Reasoning2 = %s, want %s", m.Reasoning2, wantR2)
	}

	configs := m.ReasoningConfigs()
	for _, c := range []struct {
		level int
		want  json.RawMessage
	}{
		{config.ReasoningLevelOff, wantR0},
		{config.ReasoningLevelNormal, wantR1},
		{config.ReasoningLevelMax, wantR2},
	} {
		got, ok := configs[c.level]
		if !ok {
			t.Errorf("configs[%d] missing", c.level)
			continue
		}
		if !bytes.Equal(got, c.want) {
			t.Errorf("configs[%d] = %s, want %s", c.level, got, c.want)
		}
	}
}

// TestParseModelList_ReasoningMissingFields verifies that omitting any
// reasoning_N field leaves it empty (no error, no synthetic value).
// This is the backward-compat path — existing model.conf files without
// reasoning_N must keep working with the server's defaults.
func TestParseModelList_ReasoningMissingFields(t *testing.T) {
	content := `name: "test"
protocol_type: "openai"
base_url: "http://x"
api_key: "k"
model_name: "m"
`
	models, errs := parseModelList(content, "model.conf")
	if len(errs) != 0 {
		t.Errorf("expected 0 errors, got %d: %v", len(errs), errs)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if len(models[0].Reasoning0) != 0 {
		t.Errorf("Reasoning0 should be empty when omitted, got %s", models[0].Reasoning0)
	}
	if len(models[0].Reasoning1) != 0 {
		t.Errorf("Reasoning1 should be empty when omitted, got %s", models[0].Reasoning1)
	}
	if len(models[0].Reasoning2) != 0 {
		t.Errorf("Reasoning2 should be empty when omitted, got %s", models[0].Reasoning2)
	}
	if got := models[0].ReasoningConfigs(); got != nil {
		t.Errorf("ReasoningConfigs should be nil when all reasoning_N absent, got %v", got)
	}
}

func TestParseModelList_Multiple(t *testing.T) {
	content := `name: "model-a"
protocol_type: "openai"
base_url: "http://a"
api_key: "k"
model_name: "m-a"
---
name: "model-b"
protocol_type: "anthropic"
base_url: "http://b"
api_key: "k"
model_name: "m-b"
`
	models, errs := parseModelList(content, "model.conf")
	if len(errs) != 0 {
		t.Errorf("expected 0 errors, got %d: %v", len(errs), errs)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].Name != "model-a" {
		t.Errorf("model[0].Name = %q, want %q", models[0].Name, "model-a")
	}
	if models[1].Name != "model-b" {
		t.Errorf("model[1].Name = %q, want %q", models[1].Name, "model-b")
	}
}

func TestParseModelList_SkipsEmptyBlocks(t *testing.T) {
	content := `name: "first"
protocol_type: "openai"
base_url: "http://a"
api_key: "k"
model_name: "f"
---


---
name: "second"
protocol_type: "openai"
base_url: "http://b"
api_key: "k"
model_name: "s"
`
	models, errs := parseModelList(content, "model.conf")
	if len(errs) != 0 {
		t.Errorf("expected 0 errors, got %d: %v", len(errs), errs)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
}

func TestParseModelList_SkipsCommentedBlocks(t *testing.T) {
	// Lines starting with # have no colon → silently ignored as unknown keys.
	// The block is skipped because Name and ModelName are empty.
	content := `name: "active"
protocol_type: "openai"
base_url: "http://a"
api_key: "k"
model_name: "active"
---
#name: "commented-out"
#protocol_type: "openai"
#base_url: "http://b"
#api_key: "k"
#model_name: "commented"
`
	models, _ := parseModelList(content, "model.conf")
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].Name != "active" {
		t.Errorf("expected 'active', got %q", models[0].Name)
	}
}

func TestParseModelList_SkipsNamelessBlocks(t *testing.T) {
	content := `name: "valid"
protocol_type: "openai"
base_url: "http://a"
api_key: "k"
model_name: "valid"
---
# This block has no name or model_name — should be skipped
protocol_type: "anthropic"
base_url: "http://b"
api_key: "k"
`
	models, _ := parseModelList(content, "model.conf")
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].Name != "valid" {
		t.Errorf("expected 'valid', got %q", models[0].Name)
	}
}

func TestParseModelList_RoundTrip(t *testing.T) {
	original := []modelConfig{
		{Name: "m1", ProtocolType: "openai", BaseURL: "http://a", APIKey: "k1", ModelName: "m1", ContextLimit: 1000},
		{Name: "m2", ProtocolType: "anthropic", BaseURL: "http://b", APIKey: "k2", ModelName: "m2", MaxTokens: 500},
	}
	formatted := formatModelList(original)
	parsed, errs := parseModelList(formatted, "model.conf")
	if len(errs) != 0 {
		t.Errorf("expected 0 errors, got %d: %v", len(errs), errs)
	}
	if len(parsed) != len(original) {
		t.Fatalf("expected %d models, got %d", len(original), len(parsed))
	}
	for i := range original {
		if parsed[i].Name != original[i].Name {
			t.Errorf("model[%d].Name = %q, want %q", i, parsed[i].Name, original[i].Name)
		}
		if parsed[i].ProtocolType != original[i].ProtocolType {
			t.Errorf("model[%d].ProtocolType = %q, want %q", i, parsed[i].ProtocolType, original[i].ProtocolType)
		}
		if parsed[i].BaseURL != original[i].BaseURL {
			t.Errorf("model[%d].BaseURL = %q, want %q", i, parsed[i].BaseURL, original[i].BaseURL)
		}
		if parsed[i].ModelName != original[i].ModelName {
			t.Errorf("model[%d].ModelName = %q, want %q", i, parsed[i].ModelName, original[i].ModelName)
		}
		if parsed[i].ContextLimit != original[i].ContextLimit {
			t.Errorf("model[%d].ContextLimit = %d, want %d", i, parsed[i].ContextLimit, original[i].ContextLimit)
		}
		if parsed[i].MaxTokens != original[i].MaxTokens {
			t.Errorf("model[%d].MaxTokens = %d, want %d", i, parsed[i].MaxTokens, original[i].MaxTokens)
		}
	}
}

func TestFormatThenParse_WithID(t *testing.T) {
	// ID (config:"-") should never survive a format+parse cycle.
	original := []modelConfig{
		{ID: 99, Name: "test", ProtocolType: "openai", BaseURL: "http://a", APIKey: "k", ModelName: "m"},
	}
	formatted := formatModelList(original)
	parsed, _ := parseModelList(formatted, "model.conf")
	if len(parsed) != 1 {
		t.Fatalf("expected 1 model, got %d", len(parsed))
	}
	if parsed[0].ID != 0 {
		t.Errorf("ID should be reset to 0 after format+parse, got %d", parsed[0].ID)
	}
}

// TestFormatThenParse_Reasoning verifies the raw JSON for each
// reasoning level survives a format→parse cycle byte-for-byte.
// Drift here would corrupt the per-level wire configuration silently.
func TestFormatThenParse_Reasoning(t *testing.T) {
	original := []modelConfig{
		{
			Name:         "test",
			ProtocolType: "anthropic",
			BaseURL:      "http://x",
			APIKey:       "k",
			ModelName:    "m",
			Reasoning0:   json.RawMessage(`{"thinking":{"type":"disabled"}}`),
			Reasoning1:   json.RawMessage(`{"thinking":{"type":"enabled"},"output_config":{"effort":"high"}}`),
			Reasoning2:   json.RawMessage(`{"thinking":{"type":"enabled"},"output_config":{"effort":"max"}}`),
		},
	}
	formatted := formatModelList(original)
	parsed, errs := parseModelList(formatted, "model.conf")
	if len(errs) != 0 {
		t.Errorf("expected 0 errors, got %d: %v", len(errs), errs)
	}
	if len(parsed) != 1 {
		t.Fatalf("expected 1 model, got %d", len(parsed))
	}
	for _, c := range []struct {
		name string
		got  json.RawMessage
		want json.RawMessage
	}{
		{"Reasoning0", parsed[0].Reasoning0, original[0].Reasoning0},
		{"Reasoning1", parsed[0].Reasoning1, original[0].Reasoning1},
		{"Reasoning2", parsed[0].Reasoning2, original[0].Reasoning2},
	} {
		if !bytes.Equal(c.got, c.want) {
			t.Errorf("%s round-trip drift: got %s, want %s", c.name, c.got, c.want)
		}
	}
}
