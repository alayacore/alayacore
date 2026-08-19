package llm

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"testing"
	"time"
)

// blockingTool is a tool whose Execute blocks until ctx is canceled, then
// records that cancellation happened by closing canceled.
type blockingTool struct {
	canceled chan struct{}
}

func (t *blockingTool) Execute(ctx context.Context, _ json.RawMessage) ([]ContentPart, error) {
	<-ctx.Done()
	close(t.canceled)
	return nil, ctx.Err()
}

// errorMidStreamProvider emits one complete tool call and then an error,
// simulating a provider that fails mid-stream while a tool is executing.
type errorMidStreamProvider struct{}

func (m *errorMidStreamProvider) StreamMessages(_ context.Context, _ []ContentPart, _ []ToolDefinition, _, _ string) (iter.Seq2[StreamEvent, error], error) {
	return func(yield func(StreamEvent, error) bool) {
		yield(ToolInputStartEvent{ID: "call_1", Name: "block", Index: 0}, nil)
		yield(ToolInputCompleteEvent{ID: "call_1", Input: []byte(`{}`), Index: 0}, nil)
		yield(nil, errors.New("provider stream failed mid-tool"))
	}, nil
}

func (m *errorMidStreamProvider) SetReasoningLevel(_ int)                       {}
func (m *errorMidStreamProvider) SetReasoningConfigs(_ map[int]json.RawMessage) {}
func (m *errorMidStreamProvider) SetVideoConfig(_ int, _ int)                   {}

// TestStreamErrorCancelsInFlightTool verifies that when the provider
// errors while a tool is executing, Stream cancels the in-flight tool and
// waits for it to terminate BEFORE returning. Previously the tool
// goroutine leaked and kept running (side effects continued) after the
// error; now no tool outlives Stream's return.
func TestStreamErrorCancelsInFlightTool(t *testing.T) {
	tool := &blockingTool{canceled: make(chan struct{})}

	agent := NewAgent(AgentConfig{
		Provider: &errorMidStreamProvider{},
		Tools: []Tool{
			{
				Definition: ToolDefinition{Name: "block", Description: "Blocks until canceled"},
				Execute:    tool.Execute,
			},
		},
		MaxSteps: 3,
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := agent.Stream(context.Background(), []ContentPart{
			&TextPart{Text: "go", ContentPartMeta: ContentPartMeta{Role: RoleUser}},
		}, StreamCallbacks{})
		if err == nil {
			t.Error("expected error from provider, got nil")
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stream did not return after provider error — tool goroutine leaked or cleanup blocked")
	}

	// The in-flight tool must have been canceled before Stream returned
	// (Stream waits for the tool goroutine, which closes canceled first).
	select {
	case <-tool.canceled:
	default:
		t.Fatal("in-flight tool was not canceled before Stream returned")
	}
}

// TestStreamErrorUnblocksPendingConfirm verifies that a tool awaiting user
// confirmation does not leak when the stream errors: the confirmation
// goroutine exits via the canceled stream context even though the user
// never responds to the confirmation prompt.
func TestStreamErrorUnblocksPendingConfirm(t *testing.T) {
	agent := NewAgent(AgentConfig{
		Provider: &errorMidStreamProvider{},
		Tools: []Tool{
			{
				Definition: ToolDefinition{Name: "block", Description: "Block"},
				Execute: func(ctx context.Context, _ json.RawMessage) ([]ContentPart, error) {
					<-ctx.Done()
					return nil, ctx.Err()
				},
			},
		},
		MaxSteps: 3,
	})

	confirmRequests := make(chan struct{}, 1)
	callbacks := StreamCallbacks{
		ToolNeedsConfirm: func(name string) bool { return name == "block" },
		OnToolConfirm: func(_ ToolConfirmRequest) <-chan bool {
			confirmRequests <- struct{}{}
			ch := make(chan bool) // never answered — the user never responds
			return ch
		},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := agent.Stream(context.Background(), []ContentPart{
			&TextPart{Text: "go", ContentPartMeta: ContentPartMeta{Role: RoleUser}},
		}, callbacks)
		if err == nil {
			t.Error("expected error from provider, got nil")
		}
	}()

	select {
	case <-confirmRequests:
	case <-time.After(5 * time.Second):
		t.Fatal("tool confirmation was never requested")
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stream did not return after provider error with a pending confirmation — goroutine leaked")
	}
}
