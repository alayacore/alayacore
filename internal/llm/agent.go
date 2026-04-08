package llm

// Agent Tool-Calling Gotchas:
//
// 1. TOOL RESULT MESSAGE ORDERING: OnStepFinish callback receives complete step messages.
//    For tool-using steps, this includes both the assistant message (with tool calls) AND
//    the tool result message. The OnToolResult callback should only send UI notifications,
//    not append to session messages - the agent loop handles message assembly.
//
// 2. INCOMPLETE TOOL CALLS ON CANCEL: When user cancels mid-tool-call, messages may have
//    tool_use without matching tool_result. Clean up these orphaned tool uses before the
//    next API request to prevent errors.

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"sync"
)

// Tool represents an executable tool
type Tool struct {
	Definition ToolDefinition
	Execute    func(ctx context.Context, input json.RawMessage) (ToolResultOutput, error)
}

// AgentConfig configures the agent
type AgentConfig struct {
	Provider          Provider
	Tools             []Tool
	SystemPrompt      string // Default system prompt (base)
	ExtraSystemPrompt string // User-provided extra system prompt via --system flag
	MaxSteps          int
}

// Agent orchestrates tool-calling loops
type Agent struct {
	config AgentConfig
}

// NewAgent creates a new agent
func NewAgent(config AgentConfig) *Agent {
	if config.MaxSteps == 0 {
		config.MaxSteps = 100
	}
	return &Agent{config: config}
}

// StreamCallbacks receives streaming events
type StreamCallbacks struct {
	OnTextDelta      func(delta string) error
	OnReasoningDelta func(delta string) error
	OnToolCall       func(toolCallID, toolName string, input json.RawMessage) error
	OnToolResult     func(toolCallID string, output ToolResultOutput) error
	OnStepStart      func(step int) error
	OnStepFinish     func(messages []Message, usage Usage) error
}

// StreamResult is the final result of streaming
type StreamResult struct {
	Messages []Message
	Usage    Usage
}

// Stream executes the agent with streaming callbacks
func (a *Agent) Stream(ctx context.Context, messages []Message, callbacks StreamCallbacks) (*StreamResult, error) {
	var (
		allMessages = make([]Message, len(messages))
		totalUsage  Usage
		step        int
		mu          sync.Mutex
	)

	copy(allMessages, messages)

	for step = 1; step <= a.config.MaxSteps; step++ {
		// Check for context cancellation between steps
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if callbacks.OnStepStart != nil {
			if err := callbacks.OnStepStart(step); err != nil {
				return nil, fmt.Errorf("OnStepStart callback failed: %w", err)
			}
		}

		// Convert tools to definitions
		toolDefs := make([]ToolDefinition, len(a.config.Tools))
		for i, tool := range a.config.Tools {
			toolDefs[i] = tool.Definition
		}

		// Stream from provider
		events, err := a.config.Provider.StreamMessages(
			ctx,
			allMessages,
			toolDefs,
			a.config.SystemPrompt,
			a.config.ExtraSystemPrompt,
		)
		if err != nil {
			return nil, fmt.Errorf("provider stream failed: %w", err)
		}

		// Process events
		stepMessages, stepUsage, toolCalls, err := a.processStreamEvents(events, callbacks)
		if err != nil {
			return nil, err
		}

		mu.Lock()
		totalUsage.InputTokens += stepUsage.InputTokens
		totalUsage.OutputTokens += stepUsage.OutputTokens
		mu.Unlock()

		// If no tool calls, we're done - add the step messages (assistant response)
		if len(toolCalls) == 0 {
			if callbacks.OnStepFinish != nil {
				if err := callbacks.OnStepFinish(stepMessages, stepUsage); err != nil {
					return nil, fmt.Errorf("OnStepFinish callback failed: %w", err)
				}
			}
			allMessages = append(allMessages, stepMessages...)
			break
		}

		// Execute tools and add results to messages
		toolResults := a.executeTools(ctx, toolCalls, callbacks)
		toolResultMsg := Message{
			Role:    RoleTool,
			Content: toolResults,
		}

		// Use stepMessages (contains complete assistant response with text, reasoning, AND tool calls)
		// Fall back to building from toolCalls only if stepMessages is empty (shouldn't happen)
		if len(stepMessages) == 0 {
			stepMessages = []Message{{
				Role:    RoleAssistant,
				Content: toolCallsToContent(toolCalls),
			}}
		}

		allMessages = append(allMessages, stepMessages...)
		allMessages = append(allMessages, toolResultMsg)

		// Notify callback with complete step messages (assistant + tool results)
		if callbacks.OnStepFinish != nil {
			stepWithResults := make([]Message, len(stepMessages), len(stepMessages)+1)
			copy(stepWithResults, stepMessages)
			stepWithResults = append(stepWithResults, toolResultMsg)
			if err := callbacks.OnStepFinish(stepWithResults, stepUsage); err != nil {
				return nil, fmt.Errorf("OnStepFinish callback failed: %w", err)
			}
		}
	}

	return &StreamResult{
		Messages: allMessages,
		Usage:    totalUsage,
	}, nil
}

// processStreamEvents handles streaming events from the provider
func (a *Agent) processStreamEvents(events iter.Seq2[StreamEvent, error], callbacks StreamCallbacks) ([]Message, Usage, []ToolCallPart, error) {
	var (
		stepMessages []Message
		stepUsage    Usage
		toolCalls    []ToolCallPart
	)

	for event, err := range events {
		if err != nil {
			return nil, Usage{}, nil, err
		}

		switch e := event.(type) {
		case TextDeltaEvent:
			if callbacks.OnTextDelta != nil {
				if err := callbacks.OnTextDelta(e.Delta); err != nil {
					return nil, Usage{}, nil, fmt.Errorf("OnTextDelta callback failed: %w", err)
				}
			}

		case ReasoningDeltaEvent:
			if callbacks.OnReasoningDelta != nil {
				if err := callbacks.OnReasoningDelta(e.Delta); err != nil {
					return nil, Usage{}, nil, fmt.Errorf("OnReasoningDelta callback failed: %w", err)
				}
			}

		case ToolCallEvent:
			toolCalls = append(toolCalls, ToolCallPart{
				Type:       "tool_use",
				ToolCallID: e.ToolCallID,
				ToolName:   e.ToolName,
				Input:      e.Input,
			})

			if callbacks.OnToolCall != nil {
				if err := callbacks.OnToolCall(e.ToolCallID, e.ToolName, e.Input); err != nil {
					return nil, Usage{}, nil, fmt.Errorf("OnToolCall callback failed: %w", err)
				}
			}

		case StepCompleteEvent:
			stepMessages = e.Messages
			stepUsage = e.Usage
		}
	}

	return stepMessages, stepUsage, toolCalls, nil
}

// executeTools executes all tool calls and returns the results
func (a *Agent) executeTools(ctx context.Context, toolCalls []ToolCallPart, callbacks StreamCallbacks) []ContentPart {
	toolResults := make([]ContentPart, len(toolCalls))
	for i, tc := range toolCalls {
		// Find the tool
		var tool *Tool
		for _, t := range a.config.Tools {
			if t.Definition.Name == tc.ToolName {
				tool = &t
				break
			}
		}

		if tool == nil {
			toolResults[i] = ToolResultPart{
				Type:       "tool_result",
				ToolCallID: tc.ToolCallID,
				Output: ToolResultOutputError{
					Type:  "error",
					Error: fmt.Sprintf("unknown tool: %s", tc.ToolName),
				},
			}
			continue
		}

		// Execute tool
		output, err := tool.Execute(ctx, tc.Input)
		if err != nil {
			output = ToolResultOutputError{
				Type:  "error",
				Error: err.Error(),
			}
		}

		toolResults[i] = ToolResultPart{
			Type:       "tool_result",
			ToolCallID: tc.ToolCallID,
			Output:     output,
		}

		// Notify callback about tool result
		if callbacks.OnToolResult != nil {
			//nolint:errcheck // callback error shouldn't prevent tool result from being recorded
			callbacks.OnToolResult(tc.ToolCallID, output)
		}
	}
	return toolResults
}

// toolCallsToContent converts tool calls to content parts
func toolCallsToContent(toolCalls []ToolCallPart) []ContentPart {
	content := make([]ContentPart, len(toolCalls))
	for i, tc := range toolCalls {
		content[i] = tc
	}
	return content
}
