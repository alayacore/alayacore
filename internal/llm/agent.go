package llm

// Agent Tool-Calling Gotchas:
//
// 1. ONSTEPFINISH RECEIVES FULL HISTORY: OnStepFinish callback receives
//    the complete allContents slice (full conversation history), not just
//    the current step's content parts. The session layer replaces its state
//    from this rather than appending increments. OnToolOutput should only
//    send UI notifications, not append to session contents.
//
// 2. INCOMPLETE TOOL CALLS ON CANCEL: When user cancels mid-tool-call, content may have
//    tool_use without matching tool_result. Clean up these orphaned tool uses before the
//    next API request to prevent errors.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"math"
)

// ErrMaxStepsExceeded is returned when the agent loop reaches the configured maximum number of steps
// without the model producing a final text-only response.
var ErrMaxStepsExceeded = errors.New("agent loop exceeded maximum steps")

// ErrResponseTruncated is returned when the model's response was cut short
// due to hitting the output token limit (max_tokens / length).
var ErrResponseTruncated = errors.New("response truncated: hit output token limit")

// Tool represents an executable tool
type Tool struct {
	Definition ToolDefinition
	Execute    func(ctx context.Context, input json.RawMessage) ([]ContentPart, error)
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
		config.MaxSteps = math.MaxInt
	}
	return &Agent{config: config}
}

// StreamCallbacks receives streaming events
type StreamCallbacks struct {
	OnTextDelta         func(delta string, historyID uint64) error
	OnTextComplete      func(text string, historyID uint64) error
	OnReasoningDelta    func(delta string, historyID uint64) error
	OnReasoningComplete func(text string, historyID uint64) error
	OnToolInputStart    func(toolCallID, name string, historyID uint64) error
	OnToolInputDelta    func(toolCallID, delta string, historyID uint64) error
	OnToolInputComplete func(toolCallID string, input json.RawMessage, historyID uint64) error
	OnToolOutput        func(toolCallID string, contents []ContentPart, err error, historyID uint64) error

	// OnToolConfirm is called for each tool that requires user confirmation.
	// It returns a channel that receives the user's decision (true = allowed).
	// Only tools for which ToolNeedsConfirm returned true trigger this callback.
	// The callback is called from a per-tool goroutine that blocks on the
	// returned channel until the user responds.
	OnToolConfirm func(request ToolConfirmRequest) <-chan bool

	// ToolNeedsConfirm reports whether a tool requires user confirmation.
	// If nil, no tools trigger confirmation — they all execute immediately.
	ToolNeedsConfirm func(name string) bool

	OnStepStart  func(step int) error
	OnStepFinish func(contents []ContentPart, usage Usage) error

	// IDGen provides unique history IDs. Called once per content block
	// (first delta for AT/AR, once for each AF/UF). The returned ID is
	// passed to callbacks and stored on the ContentPart.
	IDGen func() uint64
}

// ToolConfirmRequest represents a single tool call awaiting user confirmation.
type ToolConfirmRequest struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// ToConfirmRequest builds a ToolConfirmRequest from a ToolInputPart.
func (tc *ToolInputPart) ToConfirmRequest() ToolConfirmRequest {
	return ToolConfirmRequest{
		ID:    tc.ID,
		Name:  tc.Name,
		Input: tc.Input,
	}
}

// StreamResult is the final result of streaming.
// Contents is the full conversation history (allContents).
// Usage is the total token usage summed across all steps.
//
// Note: Both fields are also available per-step via OnStepFinish callback.
// StreamResult serves as a convenience return for callers that don't use
// the callback or want a final summary after Stream() returns.
type StreamResult struct {
	Contents []ContentPart
	Usage    Usage
}

// Stream executes the agent with streaming callbacks.
// Tools are confirmed and executed as soon as their arguments finish streaming
// (on ToolInputCompleteEvent), overlapping with other tools still being streamed.
func (a *Agent) Stream(ctx context.Context, contents []ContentPart, callbacks StreamCallbacks) (*StreamResult, error) {
	allContents := make([]ContentPart, len(contents))
	copy(allContents, contents)

	var totalUsage Usage

	for step := 1; step <= a.config.MaxSteps; step++ {
		if callbacks.OnStepStart != nil {
			if err := callbacks.OnStepStart(step); err != nil {
				return nil, err
			}
		}

		events, err := a.config.Provider.StreamMessages(ctx, allContents, a.toolDefinitions(), a.config.SystemPrompt, a.config.ExtraSystemPrompt)
		if err != nil {
			return nil, fmt.Errorf("provider stream failed: %w", err)
		}

		stepContents, stepUsage, truncated, err := a.streamEvents(ctx, events, callbacks)
		if err != nil {
			return nil, err
		}

		allContents = append(allContents, stepContents...)

		if callbacks.OnStepFinish != nil {
			if err := callbacks.OnStepFinish(allContents, stepUsage); err != nil {
				return nil, err
			}
		}

		totalUsage.InputTokens += stepUsage.InputTokens
		totalUsage.OutputTokens += stepUsage.OutputTokens

		if truncated {
			return &StreamResult{Contents: allContents, Usage: totalUsage}, ErrResponseTruncated
		}
		if !hasToolInputs(stepContents) {
			return &StreamResult{Contents: allContents, Usage: totalUsage}, nil
		}
	}

	return &StreamResult{Contents: allContents, Usage: totalUsage}, ErrMaxStepsExceeded
}

// streamEvents iterates streaming events, firing callbacks and collecting
// tool calls. Returns the assembled content parts (assistant response +
// tool results), usage, and whether the response was truncated.
// Assigns unique history IDs via IDGen on first touch of each content block,
// passes them to callbacks, and stores them on ContentParts.
//
//nolint:gocyclo // switch dispatch over 8 event types with callback guards
func (a *Agent) streamEvents(ctx context.Context, events iter.Seq2[StreamEvent, error], callbacks StreamCallbacks) ([]ContentPart, Usage, bool, error) {
	var (
		stepContents []ContentPart
		stepUsage    Usage
		truncated    bool
		results      []ContentPart
	)

	// Channel for collecting all tool execution results.
	// Buffered so sender goroutines exit immediately after execution.
	// Capacity 16 covers all tool results in practice.
	resultCh := make(chan ContentPart, 16)
	execCount := 0

	// Track history IDs for content blocks (keyed by index for AT/AR/AF).
	idByIndex := make(map[int]uint64)
	// Track tool names by index so ToolInputCompleteEvent can look up the name
	// it received earlier from ToolInputStartEvent.
	nameByIndex := make(map[int]string)

	for event, err := range events {
		if err != nil {
			return nil, Usage{}, false, err
		}

		switch e := event.(type) {
		case TextDeltaEvent:
			if callbacks.OnTextDelta != nil {
				if err := callbacks.OnTextDelta(e.Delta, getOrAssignID(callbacks, idByIndex, e.Index)); err != nil {
					return nil, Usage{}, false, err
				}
			}

		case TextCompleteEvent:
			if callbacks.OnTextComplete != nil {
				if err := callbacks.OnTextComplete(e.Text, getOrAssignID(callbacks, idByIndex, e.Index)); err != nil {
					return nil, Usage{}, false, err
				}
			}

		case ReasoningDeltaEvent:
			if callbacks.OnReasoningDelta != nil {
				if err := callbacks.OnReasoningDelta(e.Delta, getOrAssignID(callbacks, idByIndex, e.Index)); err != nil {
					return nil, Usage{}, false, err
				}
			}

		case ReasoningCompleteEvent:
			if callbacks.OnReasoningComplete != nil {
				if err := callbacks.OnReasoningComplete(e.Text, getOrAssignID(callbacks, idByIndex, e.Index)); err != nil {
					return nil, Usage{}, false, err
				}
			}

		case ToolInputStartEvent:
			if callbacks.OnToolInputStart != nil {
				if err := callbacks.OnToolInputStart(e.ID, e.Name, getOrAssignID(callbacks, idByIndex, e.Index)); err != nil {
					return nil, Usage{}, false, err
				}
			}
			nameByIndex[e.Index] = e.Name

		case ToolInputDeltaEvent:
			if callbacks.OnToolInputDelta != nil {
				if err := callbacks.OnToolInputDelta(e.ID, e.Delta, getOrAssignID(callbacks, idByIndex, e.Index)); err != nil {
					return nil, Usage{}, false, err
				}
			}

		case ToolInputCompleteEvent:
			id := getOrAssignID(callbacks, idByIndex, e.Index)
			// Repair tool input before processing (Patterns 1-4).
			repairToolInput(&e.Input, nameByIndex[e.Index], a.config.Tools)

			if callbacks.OnToolInputComplete != nil {
				if err := callbacks.OnToolInputComplete(e.ID, e.Input, id); err != nil {
					return nil, Usage{}, false, err
				}
			}
			execCount++
			tc := e.ToPart(id, nameByIndex[e.Index])
			a.handleStreamedToolInput(ctx, tc, callbacks, resultCh)

		case StepCompleteEvent:
			stepContents = e.Contents
			stepUsage = e.Usage
			// Set IDs on final content parts from tracked values.
			for i := range stepContents {
				if id, ok := idByIndex[i]; ok {
					stepContents[i].UpdateContentPartMeta(id, RoleAssistant)
				}
			}
			// Strip empty placeholders that providers may have inserted
			// to keep delta indices aligned with content positions.
			stepContents = stripEmptyPlaceholders(stepContents)
			// Repair tool inputs in step contents for history consistency.
			repairToolInputsInContents(stepContents, a.config.Tools)
			if e.StopReason == "max_tokens" || e.StopReason == "length" {
				truncated = true
			}
		}
	}

	// All tools (confirm and no-confirm) execute in goroutines started
	// during streaming. Collect all results.

	for i := 0; i < execCount; i++ {
		results = append(results, <-resultCh)
	}

	// Re-order results by tool call ID to match the LLM's intended order.
	// toolInputs are extracted from stepContents, which preserves the
	// SSE index order (0, 1, 2...) from the streaming response.
	reordered, reorderErr := reorderToolResults(stepContents, results)
	if reorderErr != nil {
		return nil, Usage{}, false, reorderErr
	}
	stepContents = reordered

	return stepContents, stepUsage, truncated, nil
}

// reorderToolResults re-orders tool results by their tool call ID to match
// the original order of tool calls in stepContents (which preserves the
// SSE index order from the streaming response). Each result carries its
// tool call ID, so we place them at the correct position regardless of
// execution or collection order.
//
// Before returning, the slots are checked: any tool call without a
// matching result (e.g. a non-conforming provider that reuses an empty
// tool-call ID) is reported as an error instead of leaving a nil
// ContentPart in the conversation history — a nil entry would panic
// later in GroupByRole/GetRole (method call on nil interface).
func reorderToolResults(stepContents, results []ContentPart) ([]ContentPart, error) {
	toolInputs := extractToolInputs(stepContents)
	finalResults := make([]ContentPart, len(toolInputs))
	idToTool := make(map[string]int, len(toolInputs))
	for i, tc := range toolInputs {
		idToTool[tc.ID] = i
	}
	for _, r := range results {
		if tr, ok := r.(*ToolOutputPart); ok {
			if idx, ok := idToTool[tr.ID]; ok {
				finalResults[idx] = r
			}
		}
	}

	// Check for unmatched slots before returning.
	for i, p := range finalResults {
		if p == nil {
			return nil, fmt.Errorf("tool result missing for tool call %q", toolInputs[i].ID)
		}
	}

	return append(stepContents, finalResults...), nil
}

// handleStreamedToolInput processes a completed tool use during streaming.
// If the tool requires confirmation (per ToolNeedsConfirm), it starts a
// goroutine that obtains a per-tool confirm channel and blocks until the
// user responds. Otherwise it executes immediately in a goroutine.
// All tools send exactly one result through resultCh.
func (a *Agent) handleStreamedToolInput(ctx context.Context, tc *ToolInputPart, callbacks StreamCallbacks, resultCh chan<- ContentPart) {
	if callbacks.ToolNeedsConfirm != nil && callbacks.ToolNeedsConfirm(tc.Name) {
		// Start goroutine that waits for confirmation before executing.
		historyID := genHistoryID(callbacks)
		go func(ctx context.Context, tc *ToolInputPart, historyID uint64) {
			select {
			case allowed := <-callbacks.OnToolConfirm(tc.ToConfirmRequest()):
				if !allowed {
					resultCh <- newToolOutput(callbacks, tc.ID, nil, fmt.Errorf("Tool execution denied by user"), historyID)
					return
				}
				resultCh <- a.executeTool(ctx, tc, callbacks, historyID)
			case <-ctx.Done():
				resultCh <- newToolOutput(callbacks, tc.ID, nil, ctx.Err(), historyID)
			}
		}(ctx, tc, historyID)
		return
	}

	historyID := genHistoryID(callbacks)
	go func(tc *ToolInputPart, historyID uint64) {
		resultCh <- a.executeTool(ctx, tc, callbacks, historyID)
	}(tc, historyID)
}

func (a *Agent) toolDefinitions() []ToolDefinition {
	defs := make([]ToolDefinition, len(a.config.Tools))
	for i, tool := range a.config.Tools {
		defs[i] = tool.Definition
	}
	return defs
}

// getOrAssignID returns the history ID for the given content block index.
// If no ID has been assigned yet and IDGen is available, it generates one.
func getOrAssignID(callbacks StreamCallbacks, idByIndex map[int]uint64, index int) uint64 {
	if id, ok := idByIndex[index]; ok && id != 0 {
		return id
	}
	id := genHistoryID(callbacks)
	if id != 0 {
		idByIndex[index] = id
	}
	return id
}

// genHistoryID generates a new history ID using the callback's IDGen if available.
func genHistoryID(callbacks StreamCallbacks) uint64 {
	if callbacks.IDGen != nil {
		return callbacks.IDGen()
	}
	return 0
}

// stripEmptyPlaceholders removes empty ReasoningPart and TextPart placeholders
// from the content array. OpenAI emits these slots at fixed indices (0 and 1)
// to keep delta indices aligned with content positions, even when absent.
func stripEmptyPlaceholders(contents []ContentPart) []ContentPart {
	filtered := make([]ContentPart, 0, len(contents))
	for _, part := range contents {
		switch p := part.(type) {
		case *ReasoningPart:
			if p.Text != "" {
				filtered = append(filtered, part)
			}
		case *TextPart:
			if p.Text != "" {
				filtered = append(filtered, part)
			}
		default:
			filtered = append(filtered, part)
		}
	}
	return filtered
}

// ToPart converts a ToolInputCompleteEvent to a ToolInputPart,
// carrying over the history ID assigned during streaming.
func (e ToolInputCompleteEvent) ToPart(historyID uint64, name string) *ToolInputPart {
	return &ToolInputPart{
		ID:    e.ID,
		Name:  name,
		Input: e.Input,
		ContentPartMeta: ContentPartMeta{
			HistoryID: historyID,
		},
	}
}

// executeTool executes a single tool call and returns the result.
func (a *Agent) executeTool(ctx context.Context, tc *ToolInputPart, callbacks StreamCallbacks, historyID uint64) ContentPart {
	var tool *Tool
	for _, t := range a.config.Tools {
		if t.Definition.Name == tc.Name {
			tool = &t
			break
		}
	}

	if tool == nil {
		return newToolOutput(callbacks, tc.ID, nil, fmt.Errorf("unknown tool: %s", tc.Name), historyID)
	}

	content, err := tool.Execute(ctx, tc.Input)
	return newToolOutput(callbacks, tc.ID, content, err, historyID)
}

// newToolOutput creates a ToolOutputPart and fires the OnToolOutput callback
// so the UI is notified immediately as each tool finishes.
//
// Note: content is processed (nil → empty, error → TextPart) BEFORE the
// callback fires, so the callback always receives meaningful display text.
func newToolOutput(callbacks StreamCallbacks, id string, contents []ContentPart, err error, historyID uint64) *ToolOutputPart {
	if contents == nil {
		contents = []ContentPart{}
	}
	isError := err != nil
	if isError && len(contents) == 0 {
		contents = []ContentPart{&TextPart{Text: err.Error()}}
	}
	if callbacks.OnToolOutput != nil {
		_ = callbacks.OnToolOutput(id, contents, err, historyID)
	}
	return &ToolOutputPart{ID: id, Output: contents, IsError: isError, ContentPartMeta: ContentPartMeta{HistoryID: historyID, Role: RoleTool}}
}

// extractToolInputs extracts ToolInputParts from message content.
func extractToolInputs(contents []ContentPart) []ToolInputPart {
	var uses []ToolInputPart
	for _, part := range contents {
		if tc, ok := part.(*ToolInputPart); ok {
			uses = append(uses, *tc)
		}
	}
	return uses
}

// hasToolInputs checks if content contains tool calls.
func hasToolInputs(contents []ContentPart) bool {
	for _, part := range contents {
		if _, ok := part.(*ToolInputPart); ok {
			return true
		}
	}
	return false
}

// repairToolInput repairs a tool input in-place using the tool's schema.
// If the tool is not found or has no schema, the input is left unchanged.
func repairToolInput(input *json.RawMessage, toolName string, tools []Tool) {
	if schema := findToolSchema(toolName, tools); schema != nil {
		if fixed := RepairToolInput(*input, schema); string(fixed) != string(*input) {
			*input = fixed
		}
	}
}

// repairToolInputsInContents applies repairToolInput to all ToolInputParts
// in the content slice. This ensures the history stored via OnStepFinish
// contains repaired JSON, so subsequent API requests send clean input.
func repairToolInputsInContents(contents []ContentPart, tools []Tool) {
	for _, part := range contents {
		tp, ok := part.(*ToolInputPart)
		if !ok {
			continue
		}
		repairToolInput(&tp.Input, tp.Name, tools)
	}
}

// findToolSchema looks up a tool by name and returns its JSON Schema.
// Returns nil if the tool is not found or has no schema defined.
func findToolSchema(toolName string, tools []Tool) json.RawMessage {
	for _, t := range tools {
		if t.Definition.Name == toolName && len(t.Definition.Schema) > 0 {
			return t.Definition.Schema
		}
	}
	return nil
}
