package llm

import (
	"context"
	"encoding/json"
	"iter"
	"strings"
	"testing"
)

// modalityRecordingProvider records the parts of the request it is handed and
// answers with an empty step, so what reaches a provider is the only thing the
// test has to look at.
type modalityRecordingProvider struct{ sent []ContentPart }

func (p *modalityRecordingProvider) StreamMessages(
	_ context.Context, history []ContentPart, _ []ToolDefinition, _, _ string,
) (iter.Seq2[StreamEvent, error], error) {
	p.sent = append([]ContentPart(nil), history...)
	return func(func(StreamEvent, error) bool) {}, nil
}

func (p *modalityRecordingProvider) SetReasoningLevel(_ int)                       {}
func (p *modalityRecordingProvider) SetReasoningConfigs(_ map[int]json.RawMessage) {}

// TestAgentSendsOnlyDeclaredModalities pins the wiring, not the walk: the model's
// declaration is applied at the one place every request goes through, so a part
// the model does not accept never reaches a provider. Without this the option
// would be parsed, validated and broadcast while changing nothing.
func TestAgentSendsOnlyDeclaredModalities(t *testing.T) {
	image := func() *ImagePart {
		img := &ImagePart{URI: "data:image/png;base64,QUJD"}
		img.HistoryID = 5
		return img
	}

	tests := []struct {
		name     string
		declared []string
		wantNote bool
	}{
		{name: "not declared leaves it to the protocol", declared: nil, wantNote: false},
		{name: "declared image is sent", declared: []string{"text", "image"}, wantNote: false},
		{name: "text only degrades the image", declared: []string{"text"}, wantNote: true},
		{name: "empty declaration degrades the image", declared: []string{}, wantNote: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &modalityRecordingProvider{}
			agent := NewAgent(AgentConfig{Provider: provider, MaxSteps: 1, InputModalities: tt.declared})

			if _, err := agent.Stream(context.Background(), []ContentPart{&TextPart{Text: "hi"}, image()}, StreamCallbacks{}); err != nil {
				t.Fatalf("Stream() error = %v", err)
			}

			if len(provider.sent) != 2 {
				t.Fatalf("provider saw %d parts, want 2", len(provider.sent))
			}
			if _, ok := provider.sent[0].(*TextPart); !ok {
				t.Errorf("text must pass through, got %T", provider.sent[0])
			}

			note, isNote := provider.sent[1].(*TextPart)
			if isNote != tt.wantNote {
				t.Fatalf("second part is %T, want a note: %v", provider.sent[1], tt.wantNote)
			}
			if !tt.wantNote {
				if _, ok := provider.sent[1].(*ImagePart); !ok {
					t.Errorf("the image must be sent as it is, got %T", provider.sent[1])
				}
				return
			}
			if !strings.Contains(note.Text, "NOT delivered") {
				t.Errorf("the note must say nothing was delivered: %s", note.Text)
			}
			if note.GetHistoryID() != 5 {
				t.Errorf("the note must keep the part's history ID, got %d", note.GetHistoryID())
			}
		})
	}
}
