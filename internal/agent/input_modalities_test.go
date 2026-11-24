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
// keep them apart, or a consumer reading the wire cannot tell a model that
// refuses media from one that never said.
func TestInputModalitiesAbsentAndEmptyStayApart(t *testing.T) {
	const head = "name: m\nprotocol_type: openai\nbase_url: https://x\napi_key: k\nmodel_name: m\n"

	for _, tc := range []struct {
		name       string
		line       string // the model.conf line, "" meaning "write no line"
		undeclared bool
	}{
		{"not declared", "", true},
		{"takes no media", "input_modalities: []\n", false},
	} {
		back, msgs := parseModelConfig(head+tc.line, "model.conf")
		if len(msgs) > 0 {
			t.Fatalf("%s: unexpected messages: %v", tc.name, msgs)
		}
		if len(back) != 1 {
			t.Fatalf("%s: %d models, want 1", tc.name, len(back))
		}
		if (back[0].InputModalities == nil) != tc.undeclared {
			t.Errorf("%s: parsed as %v, want undeclared=%v", tc.name, back[0].InputModalities, tc.undeclared)
		}

		// The wire: null for undeclared, [] for a declaration.
		broadcast, err := json.Marshal(toModelInfos(back))
		if err != nil {
			t.Fatalf("%s: marshal: %v", tc.name, err)
		}
		want := `"input_modalities":null`
		if !tc.undeclared {
			want = `"input_modalities":[]`
		}
		if !strings.Contains(string(broadcast), want) {
			t.Errorf("%s: broadcast %s, want %s", tc.name, broadcast, want)
		}
	}
}

// TestInputModalitiesRoundTripValues keeps the declared words intact through the
// file and the wire, order included.
func TestInputModalitiesRoundTripValues(t *testing.T) {
	const content = `name: m
protocol_type: openai
base_url: https://x
api_key: k
model_name: m
input_modalities: ["text", "image", "video"]
`
	want := []string{"text", "image", "video"}

	back, msgs := parseModelConfig(content, "model.conf")
	if len(msgs) > 0 {
		t.Fatalf("unexpected messages: %v", msgs)
	}
	if !slices.Equal(back[0].InputModalities, want) {
		t.Errorf("file parse: got %v, want %v", back[0].InputModalities, want)
	}
	if !slices.Equal(toModelInfos(back)[0].InputModalities, want) {
		t.Errorf("wire: got %v, want %v", toModelInfos(back)[0].InputModalities, want)
	}
}
