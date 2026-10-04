package llm

import (
	"context"
	"encoding/json"
	"iter"
	"sync"
	"testing"
)

// onBeforeSendProvider yields one tool-calling step, then a plain final answer,
// recording the history it was sent on each step.
type onBeforeSendProvider struct {
	mu       sync.Mutex
	requests [][]ContentPart
}

func (p *onBeforeSendProvider) StreamMessages(_ context.Context, history []ContentPart, _ []ToolDefinition, _, _ string) (iter.Seq2[StreamEvent, error], error) {
	p.mu.Lock()
	cp := make([]ContentPart, len(history))
	copy(cp, history)
	p.requests = append(p.requests, cp)
	n := len(p.requests)
	p.mu.Unlock()

	return func(yield func(StreamEvent, error) bool) {
		if n == 1 {
			if !yield(TextDeltaEvent{Delta: "step one", Key: "block:0"}, nil) {
				return
			}
			if !yield(TextCompleteEvent{Key: "block:0"}, nil) {
				return
			}
			if !yield(ToolInputStartEvent{ID: "c1", Name: "t", Key: "block:1"}, nil) {
				return
			}
			if !yield(ToolInputDeltaEvent{ID: "c1", Delta: `{}`, Key: "block:1"}, nil) {
				return
			}
			if !yield(ToolInputCompleteEvent{ID: "c1", Key: "block:1"}, nil) {
				return
			}
			yield(StepCompleteEvent{Usage: Usage{InputTokens: 10, OutputTokens: 5}, StopReason: "tool_use"}, nil)
			return
		}
		if !yield(TextDeltaEvent{Delta: "final", Key: "block:0"}, nil) {
			return
		}
		if !yield(TextCompleteEvent{Key: "block:0"}, nil) {
			return
		}
		yield(StepCompleteEvent{Usage: Usage{InputTokens: 10, OutputTokens: 5}}, nil)
	}, nil
}

func (p *onBeforeSendProvider) SetReasoningLevel(_ int)                       {}
func (p *onBeforeSendProvider) SetReasoningConfigs(_ map[int]json.RawMessage) {}
func (p *onBeforeSendProvider) SetVideoConfig(_ int, _ int)                   {}

// TestOnBeforeSendReplacesHistory pins the seam: a replacement returned by
// OnBeforeSend is what the next step is sent on, and it becomes the history the
// final StreamResult reports.
func TestOnBeforeSendReplacesHistory(t *testing.T) {
	provider := &onBeforeSendProvider{}
	agent := NewAgent(AgentConfig{
		Provider: provider,
		Tools: []Tool{{
			Definition: ToolDefinition{Name: "t", Description: "test", Schema: []byte(`{"type":"object"}`)},
			Execute:    func(_ context.Context, _ json.RawMessage) ([]ContentPart, error) { return nil, nil },
		}},
		MaxSteps: 5,
	})

	replacement := []ContentPart{&TextPart{Text: "compacted", ContentPartMeta: ContentPartMeta{Role: RoleUser}}}
	calls := 0
	res, err := agent.Stream(context.Background(),
		[]ContentPart{&TextPart{Text: "go", ContentPartMeta: ContentPartMeta{Role: RoleUser}}},
		StreamCallbacks{
			OnBeforeSend: func(_ []ContentPart) ([]ContentPart, error) {
				calls++
				if calls == 2 { // before the second step only
					return replacement, nil
				}
				return nil, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if calls != 2 {
		t.Fatalf("OnBeforeSend calls = %d, want 2 (once per step)", calls)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(provider.requests))
	}
	second := provider.requests[1]
	if len(second) != 1 || second[0].(*TextPart).Text != "compacted" {
		t.Fatalf("second step was sent on %v, want the replacement [compacted]", second)
	}
	if len(res.Contents) == 0 || res.Contents[0].(*TextPart).Text != "compacted" {
		t.Fatalf("StreamResult.Contents = %v, want it to start with the replacement", res.Contents)
	}
}
