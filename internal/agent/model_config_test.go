package agent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/protocol"
)

func TestToModelInfosWireCompatible(t *testing.T) {
	// The wire format for model_list must serialize to exactly the same
	// JSON bytes as modelConfig, so the frame protocol stays compatible.
	//
	// Every field is mirrored, so setting serial_tool_calls here is what makes
	// this comparison catch a forgotten line in toModelInfos: the domain would
	// say true while the wire, left at bool's zero value, said false. The case
	// where the domain carries no line at all is TestToModelInfos_SerialToolCalls
	// AlwaysOnTheWire, and a field added to only one of the two types is caught
	// generically by TestModelInfoAndModelConfigJSONKeysMatch.
	m := modelConfig{
		ID:              3,
		Name:            "Test",
		ProtocolType:    "openai",
		BaseURL:         "http://x",
		APIKey:          "k",
		ModelName:       "model-a",
		ContextLimit:    200000,
		MaxTokens:       0, // zero value must still appear in the wire bytes
		SerialToolCalls: true,
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

// TestParseModelList_ReasoningField verifies the response-key setting from the
// file that carries it. Unlike reasoning_N it is a plain string, so the config
// tag (model.conf) and the json tag (the model_list broadcast) both have to be
// right or the setting is silently lost and the provider falls back to the
// default key.
func TestParseModelList_ReasoningField(t *testing.T) {
	content := `name: "vllm"
protocol_type: "openai"
base_url: "http://127.0.0.1:8000/v1"
api_key: "k"
model_name: "deepseek-r1"
reasoning_field: "reasoning"
`
	parsed, errs := parseModelList(content, "model.conf")
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got %d: %v", len(errs), errs)
	}
	if len(parsed) != 1 {
		t.Fatalf("expected 1 model, got %d", len(parsed))
	}
	if parsed[0].ReasoningField != "reasoning" {
		t.Errorf("reasoning_field = %q, want %q", parsed[0].ReasoningField, "reasoning")
	}

	// The broadcast has to carry it: a consumer has no default to fall back on,
	// so a dropped key would be indistinguishable from "not configured".
	synced, err := json.Marshal(toModelInfos(parsed))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(synced), `"reasoning_field":"reasoning"`) {
		t.Errorf("broadcast payload missing reasoning_field: %s", synced)
	}

	// Omitted in model.conf → empty, which the provider maps to the default.
	noField, errs := parseModelList("name: x\nprotocol_type: openai\n", "model.conf")
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got %v", errs)
	}
	if noField[0].ReasoningField != "" {
		t.Errorf("ReasoningField = %q, want empty when unset", noField[0].ReasoningField)
	}
}

// ============================================================================
// parallel_tool_calls
// ============================================================================

// The option is spelled negatively so that its absent form — every model.conf
// written before it existed, and every struct literal since — is the behavior
// alayacore has always had. A positive spelling could not have said that with a
// bool, whose zero value would have made all of them serial.
func TestSerialToolCallsAbsentMeansTheHistoricalBehavior(t *testing.T) {
	var fromAFile modelConfig // as parseModelList leaves an unmentioned option
	if fromAFile.SerialToolCalls {
		t.Error("an unmentioned serial_tool_calls came through as serial")
	}

	set := modelConfig{SerialToolCalls: true}
	if !set.SerialToolCalls {
		t.Error("serial_tool_calls: true did not stick")
	}
}

// model_list is what a protocol consumer renders, and it is the only thing such
// a consumer sees: nothing on the wire can replace the model list, so the
// broadcast has to state the mode explicitly even where the config file wrote no
// line at all. A consumer is never asked to reconstruct a default it was not
// given.
//
// The "configured true" case is what catches a forgotten toModelInfos line: the
// wire would carry bool's zero value while the domain carried true.
func TestToModelInfosSerialToolCallsAlwaysOnTheWire(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  bool
	}{
		{"not configured", false},
		{"configured true", true},
		{"configured false", false},
	} {
		m := modelConfig{
			Name: "m", ProtocolType: "openai", BaseURL: "http://x",
			APIKey: "k", ModelName: "model-a", SerialToolCalls: tc.set,
		}
		wireJSON, err := json.Marshal(toModelInfos([]modelConfig{m}))
		if err != nil {
			t.Fatal(err)
		}
		var models []map[string]any
		if err := json.Unmarshal(wireJSON, &models); err != nil {
			t.Fatalf("%s: unmarshal wire: %v", tc.name, err)
		}
		got, ok := models[0]["serial_tool_calls"]
		if !ok {
			t.Errorf("%s: key absent from the broadcast (%s); a consumer must never infer it", tc.name, wireJSON)
			continue
		}
		asBool, isBool := got.(bool)
		if !isBool {
			t.Errorf("%s: serial_tool_calls = %#v, want a JSON bool", tc.name, got)
			continue
		}
		if asBool != tc.set {
			t.Errorf("%s: serial_tool_calls = %v, want %v", tc.name, asBool, tc.set)
		}
	}
}

// TestToModelInfosWireCompatible compares bytes for the fields one test case
// happens to fill. This compares the types, so the general mistake — a field
// added on one side only — fails here rather than quietly dropping a user's
// setting the next time a consumer round-trips the model list.
//
// Only the JSON names are compared. Tag *options* are not: the domain's
// `config` tag may leave an option out of model.conf when it is at its default,
// while the json forms must always state it — a difference pinned by the two
// tests above, not by a rule here.
func TestModelInfoAndModelConfigJSONKeysMatch(t *testing.T) {
	domain := jsonFieldNames(reflect.TypeOf(modelConfig{}))
	wire := jsonFieldNames(reflect.TypeOf(protocol.ModelInfo{}))

	for name := range domain {
		if _, ok := wire[name]; !ok {
			t.Errorf("modelConfig serializes %q but protocol.ModelInfo does not — the value is lost on broadcast", name)
		}
	}
	for name := range wire {
		if _, ok := domain[name]; !ok {
			t.Errorf("protocol.ModelInfo serializes %q but modelConfig does not — toModelInfos would have to invent it", name)
		}
	}
	if len(domain) == 0 || len(wire) == 0 {
		t.Fatalf("reflection found no fields (domain %d, wire %d) — the guard itself is broken", len(domain), len(wire))
	}
}

// jsonFieldNames lists the JSON keys a struct marshals to, honoring omitempty
// and the "-" skip marker.
func jsonFieldNames(typ reflect.Type) map[string]bool {
	names := make(map[string]bool)
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		key, _, _ := strings.Cut(tag, ",")
		if key == "" {
			continue
		}
		names[key] = true
	}
	return names
}

// What a consumer of model_list actually reads, end to end: the option is
// written in model.conf, loaded into the domain, and broadcast. Three different
// functions, so a value dropped anywhere shows up here rather than as a model
// list that quietly disagrees with the file the user edited.
//
// The broadcast states the mode even when the file wrote no line at all, because
// a consumer rendering this list has no way to reconstruct a default it was
// never given. (The come-back half of this loop — a consumer posting the edited
// array back — went away with :model_sync; model.conf is now the only writer.)
func TestModelListBroadcastCarriesTheModeFromTheFile(t *testing.T) {
	const head = "name: \"m\"\nprotocol_type: openai\nbase_url: http://x\napi_key: k\nmodel_name: model-a\n"

	for _, tc := range []struct {
		name string
		line string // the model.conf line, "" meaning "write no line"
		want bool
	}{
		{"parallel", "", false},
		{"serial", "serial_tool_calls: true\n", true},
	} {
		configPath := filepath.Join(t.TempDir(), "model.conf")
		if err := os.WriteFile(configPath, []byte(head+tc.line), 0600); err != nil {
			t.Fatalf("%s: write config: %v", tc.name, err)
		}

		mm := newModelManager(configPath)
		if err := mm.loadFromFile(configPath); err != nil {
			t.Fatalf("%s: load: %v", tc.name, err)
		}
		models := mm.getModels()
		if len(models) != 1 {
			t.Fatalf("%s: %d models, want 1", tc.name, len(models))
		}

		// What the session broadcasts: the wire type, not the domain one.
		broadcast, err := json.Marshal(protocol.ModelListMsg{Models: toModelInfos(models)})
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			Models []map[string]any `json:"models"`
		}
		if err := json.Unmarshal(broadcast, &envelope); err != nil {
			t.Fatalf("%s: decode broadcast: %v", tc.name, err)
		}
		got, ok := envelope.Models[0]["serial_tool_calls"]
		if !ok {
			t.Fatalf("%s: broadcast omitted the mode (%s) — a consumer cannot read what it cannot see",
				tc.name, broadcast)
		}
		if asBool, isBool := got.(bool); !isBool || asBool != tc.want {
			t.Errorf("%s: serial_tool_calls = %#v, want %v (broadcast %s)", tc.name, got, tc.want, broadcast)
		}
	}
}

// A hand-written file is the other way an option arrives, and it must accept the
// spellings every other bool in the config format accepts. `false` explicitly
// written says the same thing as the line being absent — both are the behavior
// alayacore has always had.
func TestParseModelList_SerialToolCallsSpellings(t *testing.T) {
	const head = "name: \"m\"\nprotocol_type: openai\nbase_url: http://x\napi_key: k\nmodel_name: model-a\n"
	for _, tc := range []struct{ text, want string }{
		{"true", "serial"}, {"false", "parallel"},
		{"yes", "serial"}, {"no", "parallel"},
		{"on", "serial"}, {"off", "parallel"},
		{"1", "serial"}, {"0", "parallel"},
		{"", "parallel"},
	} {
		text := head + "serial_tool_calls: " + tc.text + "\n"
		models, errs := parseModelList(text, "model.conf")
		if len(errs) != 0 {
			t.Fatalf("%q: unexpected parse errors: %v", tc.text, errs)
		}
		got := "parallel"
		if models[0].SerialToolCalls {
			got = "serial"
		}
		if got != tc.want {
			t.Errorf("serial_tool_calls: %q -> %s, want %s", tc.text, got, tc.want)
		}
	}

	// A value that is not a bool is reported, not quietly taken as false.
	_, errs := parseModelList(head+"serial_tool_calls: maybe\n", "model.conf")
	if len(errs) != 1 {
		t.Fatalf("got %d errors (%v), want 1", len(errs), errs)
	}
}

// The option's name is the one thing a user can type wrong in a way the format
// cannot fix: the positive spelling is what the OpenAI request calls it, so it
// is exactly what someone reaching for this setting will try first. The config
// format rejects unknown keys rather than ignoring them, and the message has to
// be the place that points at the real name — an unknown-key error with no hint
// here reads as "this option does not exist".
func TestParseModelList_PositiveSpellingIsRejectedAndNamed(t *testing.T) {
	const text = "name: \"m\"\nprotocol_type: openai\nbase_url: http://x\napi_key: k\nmodel_name: model-a\nparallel_tool_calls: false\n"
	_, errs := parseModelList(text, "model.conf")
	if len(errs) != 1 {
		t.Fatalf("got %d errors (%v), want the wrong name rejected with 1", len(errs), errs)
	}
	if !strings.Contains(errs[0], "parallel_tool_calls") {
		t.Errorf("error does not name the offending key: %q", errs[0])
	}
}
