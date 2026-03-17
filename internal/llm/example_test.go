package llm_test

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/llm/providers"
)

// Example_usage demonstrates basic usage
func Example_usage() {
	// Create Anthropic provider
	provider, err := providers.NewAnthropic(
		providers.WithAPIKey("your-api-key"),
	)
	if err != nil {
		panic(err)
	}

	// Define a simple tool
	type EchoInput struct {
		Message string `json:"message" jsonschema:"required,description=Message to echo"`
	}

	tool := llm.NewTool("echo", "Echo back the input").
		WithSchema(llm.GenerateSchema(EchoInput{})).
		WithExecute(func(ctx context.Context, input json.RawMessage) (llm.ToolResultOutput, error) {
			var params EchoInput
			if err := json.Unmarshal(input, &params); err != nil {
				return llm.NewTextErrorResponse("invalid input"), nil
			}
			return llm.NewTextResponse(fmt.Sprintf("Echo: %s", params.Message)), nil
		}).
		Build()

	// Create agent
	agent := llm.NewAgent(llm.AgentConfig{
		Provider:     provider,
		Tools:        []llm.Tool{tool},
		SystemPrompt: "You are a helpful assistant.",
	})

	// Stream with callbacks
	messages := []llm.Message{
		llm.NewUserMessage("Hello!"),
	}

	result, err := agent.Stream(context.Background(), messages, llm.StreamCallbacks{
		OnTextDelta: func(delta string) error {
			fmt.Print(delta)
			return nil
		},
		OnToolCall: func(toolCallID, toolName string, input json.RawMessage) error {
			fmt.Printf("\n[Tool: %s]\n", toolName)
			return nil
		},
	})

	if err != nil {
		panic(err)
	}

	fmt.Printf("\nTotal tokens: %d in, %d out\n",
		result.Usage.InputTokens, result.Usage.OutputTokens)
}
