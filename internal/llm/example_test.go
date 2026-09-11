package llm_test

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"

	"github.com/alayacore/alayacore/internal/llm"
)

// scriptedProvider is a hermetic stand-in for a real provider. It yields a
// scripted event stream per step so the example below runs offline with stable
// output and an `// Output:` block that `go test` actually checks. A real
// deployment builds its provider with providers.NewOpenAI or
// providers.NewAnthropic.
type scriptedProvider struct{ step int }

func (p *scriptedProvider) StreamMessages(_ context.Context, _ []llm.ContentPart, _ []llm.ToolDefinition, _, _ string) (iter.Seq2[llm.StreamEvent, error], error) {
	p.step++
	step := p.step
	return func(yield func(llm.StreamEvent, error) bool) {
		if step == 1 {
			// The model says what it is doing and asks for the echo tool.
			yield(llm.TextDeltaEvent{Delta: "Calling echo...\n", Key: "text", Position: 1}, nil)
			yield(llm.ToolInputStartEvent{ID: "call_1", Name: "echo", Key: "tool:0", Position: 2}, nil)
			yield(llm.ToolInputDeltaEvent{ID: "call_1", Delta: `{"message":"hi"}`, Key: "tool:0", Position: 2}, nil)
			yield(llm.ToolInputCompleteEvent{ID: "call_1", Key: "tool:0", Position: 2}, nil)
			yield(llm.StepCompleteEvent{Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}, StopReason: "tool_use"}, nil)
			return
		}
		// Second step: the model answers with the tool's result in hand.
		yield(llm.TextDeltaEvent{Delta: "Echo: hi\n", Key: "text", Position: 1}, nil)
		yield(llm.StepCompleteEvent{Usage: llm.Usage{InputTokens: 8, OutputTokens: 3}, StopReason: "end_turn"}, nil)
	}, nil
}

func (p *scriptedProvider) SetReasoningLevel(int)                       {}
func (p *scriptedProvider) SetReasoningConfigs(map[int]json.RawMessage) {}
func (p *scriptedProvider) SetVideoConfig(int, int)                     {}

// Example_usage demonstrates the tool-calling loop end to end: the provider asks
// for the echo tool, the agent executes it, and the next step uses the result.
func Example_usage() {
	// The tool's input type. GenerateSchema turns it into the JSON Schema the
	// model is given, and RepairToolInput validates incoming arguments against
	// it before the tool runs.
	type EchoInput struct {
		Message string `json:"message" jsonschema:"required,description=Message to echo"`
	}

	tool := llm.NewTool("echo", "Echo back the input").
		WithSchema(llm.MustGenerateSchema(EchoInput{})).
		WithExecute(func(_ context.Context, input json.RawMessage) ([]llm.ContentPart, error) {
			var params EchoInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid input: %w", err)
			}
			return []llm.ContentPart{&llm.TextPart{Text: "Echo: " + params.Message}}, nil
		}).
		Build()

	agent := llm.NewAgent(llm.AgentConfig{
		Provider: &scriptedProvider{},
		Tools:    []llm.Tool{tool},
	})

	result, err := agent.Stream(
		context.Background(),
		[]llm.ContentPart{&llm.TextPart{
			Text:            "echo hi",
			ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser},
		}},
		llm.StreamCallbacks{
			OnTextDelta: func(delta string, _ uint64) error {
				fmt.Print(delta)
				return nil
			},
			OnToolOutput: func(_ string, contents []llm.ContentPart, err error, _ uint64) error {
				if err != nil {
					fmt.Println("[tool error]", err)
					return nil
				}
				for _, part := range contents {
					if text, ok := part.(*llm.TextPart); ok {
						fmt.Println("[tool]", text.Text)
					}
				}
				return nil
			},
		},
	)
	if err != nil {
		panic(err)
	}

	fmt.Printf("tokens: %d in, %d out\n", result.Usage.InputTokens, result.Usage.OutputTokens)

	// Output:
	// Calling echo...
	// [tool] Echo: hi
	// Echo: hi
	// tokens: 18 in, 8 out
}
