package agent

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
)

// modelHead is a block that passes validateModel, so a test can vary one line
// and attribute what happens to that line.
const modelHead = `name: "m"
protocol_type: "openai"
base_url: "https://api.example.com"
api_key: "k"
model_name: "m"
`

// TestInputModalitiesVocabulary pins the accepted values to the input planes'
// own list. There is no second vocabulary to drift: the table the protocol
// validates against is the table this loop walks.
func TestInputModalitiesVocabulary(t *testing.T) {
	for _, kind := range protocol.PlaneKinds(protocol.PlaneUser) {
		m := modelConfig{Name: "m", ProtocolType: "openai", BaseURL: "https://x", ModelName: "m", InputModalities: []string{kind}}
		if errs := validateModel(m); len(errs) > 0 {
			t.Errorf("a kind the input plane admits was rejected (%q): %v", kind, errs)
		}
	}

	// reasoning is an assistant-side kind: declaring it as an input must fail,
	// which is what makes this check wider than "is it in the vocabulary".
	for _, bad := range []string{"reasoning", "vision", "pdf", "file", "IMAGE"} {
		m := modelConfig{Name: "m", ProtocolType: "openai", BaseURL: "https://x", ModelName: "m", InputModalities: []string{bad}}
		errs := validateModel(m)
		if len(errs) != 1 {
			t.Fatalf("%q must be rejected, got %v", bad, errs)
		}
		if !strings.Contains(errs[0], "unknown input_modalities value") {
			t.Errorf("%q: error does not name the field: %s", bad, errs[0])
		}
	}
}

// TestInputModalitiesEmptyIsADeclaration pins that "declared nothing" is legal.
// It is not a warning case: the model takes no media, and the empty list is how
// that is written.
func TestInputModalitiesEmptyIsADeclaration(t *testing.T) {
	m := modelConfig{Name: "m", ProtocolType: "openai", BaseURL: "https://x", ModelName: "m", InputModalities: []string{}}
	if errs := validateModel(m); len(errs) > 0 {
		t.Fatalf("an empty declaration must be accepted: %v", errs)
	}
}

// TestUnparsableFatalKeysSkipTheModel covers the keys whose zero value is a mode
// rather than "unset": an unreadable value there must not leave the model
// running with the mode silently changed.
func TestUnparsableFatalKeysSkipTheModel(t *testing.T) {
	cases := []struct{ name, line string }{
		{"input_modalities is not JSON", `input_modalities: [text, image]`},
		{"reasoning_0 is not JSON", `reasoning_0: {thinking:{"type":"disabled"}}`},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			models, msgs := parseModelConfig(modelHead+tt.line+"\n", "model.conf")

			if len(models) != 0 {
				t.Fatalf("the model must be skipped, got %d", len(models))
			}
			joined := strings.Join(msgs, "\n")
			if !strings.Contains(joined, "could not be parsed, so the model cannot be used") {
				t.Errorf("the skip is not explained: %s", joined)
			}
		})
	}
}

// TestUnparsableOtherKeyKeepsTheModel is the documented behavior for every other
// field, unchanged: the error is reported, the field keeps its zero value, and
// the model still loads (docs/configuration.md).
func TestUnparsableOtherKeyKeepsTheModel(t *testing.T) {
	models, msgs := parseModelConfig(modelHead+"max_tokens: abc\n", "model.conf")

	if len(models) != 1 {
		t.Fatalf("a bad scalar must not drop the model, got %d", len(models))
	}
	if models[0].MaxTokens != 0 {
		t.Errorf("the field must keep its zero value, got %d", models[0].MaxTokens)
	}
	if !strings.Contains(strings.Join(msgs, "\n"), "invalid integer") {
		t.Errorf("the parse error must still be reported: %v", msgs)
	}
}

// TestInputModalitiesAbsentAndEmptyStayApart is the property the whole option
// rests on: nil is "not declared" (the protocol decides) and an empty list is
// "takes no media". Both the file format and the model_list broadcast have to
// keep them apart, or :model_sync silently widens a model.
func TestInputModalitiesAbsentAndEmptyStayApart(t *testing.T) {
	for _, declared := range [][]string{nil, {}} {
		m := modelConfig{Name: "m", ProtocolType: "openai", BaseURL: "https://x", APIKey: "k", ModelName: "m", InputModalities: declared}

		// The file: nil writes no line at all, an empty declaration writes [].
		file := formatModelList([]modelConfig{m})
		hasLine := strings.Contains(file, "input_modalities")
		if hasLine != (declared != nil) {
			t.Fatalf("declared=%v wrote %q", declared, file)
		}

		back, _ := parseModelConfig(file, "model.conf")
		if len(back) != 1 {
			t.Fatalf("declared=%v: model lost on reload", declared)
		}
		if (back[0].InputModalities == nil) != (declared == nil) {
			t.Errorf("declared=%v came back as %v from the file", declared, back[0].InputModalities)
		}

		// The wire: null for undeclared, [] for a declaration, and the same
		// distinction after the round trip a :model_sync does.
		broadcast, err := json.Marshal(toModelInfos([]modelConfig{m}))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		want := `"input_modalities":null`
		if declared != nil {
			want = `"input_modalities":[]`
		}
		if !strings.Contains(string(broadcast), want) {
			t.Errorf("declared=%v broadcast %s, want %s", declared, broadcast, want)
		}

		var synced []modelConfig
		if err := json.Unmarshal(broadcast, &synced); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if (synced[0].InputModalities == nil) != (declared == nil) {
			t.Errorf("declared=%v came back as %v through :model_sync", declared, synced[0].InputModalities)
		}
	}
}

// TestInputModalitiesRoundTripValues keeps the declared words intact through the
// file and the wire, order included.
func TestInputModalitiesRoundTripValues(t *testing.T) {
	want := []string{"text", "image", "video"}
	m := modelConfig{Name: "m", ProtocolType: "openai", BaseURL: "https://x", APIKey: "k", ModelName: "m", InputModalities: want}

	back, msgs := parseModelConfig(formatModelList([]modelConfig{m}), "model.conf")
	if len(msgs) > 0 {
		t.Fatalf("unexpected messages: %v", msgs)
	}
	if !slices.Equal(back[0].InputModalities, want) {
		t.Errorf("file round trip: got %v, want %v", back[0].InputModalities, want)
	}
	if !slices.Equal(toModelInfos(back)[0].InputModalities, want) {
		t.Errorf("wire round trip: got %v, want %v", toModelInfos(back)[0].InputModalities, want)
	}
}
