package llm

// Agent Tool-Calling Gotchas:
//
// 1. ONSTEPFINISH RECEIVES FULL HISTORY: OnStepFinish callback receives
//    the complete allContents slice (full conversation history), not just
//    the current step's content parts. The session layer derives the
//    step's new parts from this (the suffix after the previous step's
//    offset) and accumulates them into its Contents incrementally; the
//    authoritative final state is committed on task completion.
//    OnToolOutput should only send UI notifications, not append to
//    session contents.
//
// 2. INCOMPLETE TOOL CALLS ON CANCEL: When user cancels mid-tool-call, content may have
//    tool_use without matching tool_result. Clean up these orphaned tool calls before the
//    next API request to prevent errors.
//
// 3. TOOL RESULTS MUST MATCH TOOL CALLS 1:1: attachToolResults pairs each result
//    with its tool call by ID. A call whose result never arrives (e.g. a
//    non-conforming provider that reuses an empty tool-call ID) would leave a nil
//    ContentPart in the step history, which panics later in GroupByRole. So the
//    strict mode used by a finished step returns an error for any unmatched slot
//    instead of appending nil, and the forgiving mode used by a cut step drops the
//    call entirely — an assistant tool_use must never be recorded without the
//    tool_result that answers it.
//
// 4. TOOL GOROUTINES NEVER OUTLIVE STREAM: tool goroutines run under a per-stream
//    context tracked by a WaitGroup. On any error or early return the context is
//    canceled and all goroutines are awaited before streamEvents returns, so no
//    tool keeps executing (and no side effects happen) after the stream has
//    errored, and no goroutine leaks. See streamEvents and sendToolResult.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"math"
	"runtime/debug"
	"sync"
	"time"
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

	// ExecuteStreaming is an optional streaming variant of Execute.
	// When set, the agent calls it instead of Execute. The onDelta
	// callback delivers ephemeral tool result preview snapshots for
	// display only (TagUserFDelta/Uf frames) — never used to assemble
	// the authoritative result, which is the returned []ContentPart.
	ExecuteStreaming func(ctx context.Context, input json.RawMessage, onDelta func(text string)) ([]ContentPart, error)
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
	// <= 0 means "no limit". Treating only 0 as unlimited left a negative
	// bound (from a hand-edited session file, say) to disable the loop
	// entirely while still reporting ErrMaxStepsExceeded.
	if config.MaxSteps <= 0 {
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
	OnToolOutputDelta   func(toolCallID, text string, historyID uint64) error

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

	// OnStepStats reports per-step speed metrics (TTFT, duration, tok/s)
	// computed from the provider's authoritative usage. Fired after the
	// step's stream completes and before OnStepFinish. Not fired for
	// failed/canceled steps that never produced a StepCompleteEvent.
	OnStepStats func(StepStats) error

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

		// stepStart is recorded before StreamMessages because the HTTP
		// request happens synchronously inside it — starting here makes
		// TTFT and Duration include request/network latency.
		stepStart := time.Now()

		events, err := a.config.Provider.StreamMessages(ctx, allContents, a.toolDefinitions(), a.config.SystemPrompt, a.config.ExtraSystemPrompt)
		if err != nil {
			return nil, fmt.Errorf("provider stream failed: %w", err)
		}

		// streamEvents fills stats.TimeToFirstToken (first delta) and
		// stats.Duration (StepCompleteEvent). Step/OutputTokens/TPS are
		// completed below after the authoritative usage is known.
		var stepStats StepStats
		stepContents, stepUsage, truncated, err := a.streamEvents(ctx, events, callbacks, stepStart, &stepStats)
		if err != nil {
			// A failed step may still have executed tools whose side
			// effects already happened (file writes, commands). Fold their
			// salvaged input+result pairs into the history so a retry or
			// :continue sees a state consistent with reality.
			if len(stepContents) > 0 {
				allContents = append(allContents, stepContents...)
				if callbacks.OnStepFinish != nil {
					if cbErr := callbacks.OnStepFinish(allContents, stepUsage); cbErr != nil {
						return nil, cbErr
					}
				}
			}
			return nil, err
		}

		allContents = append(allContents, stepContents...)

		// Complete the step stats: authoritative output tokens from the
		// provider usage; TPS is 0 for tool-only steps or degenerate
		// durations. Fired before OnStepFinish so consumers can publish
		// the stats ahead of the step-finish broadcast.
		if err := a.completeStepStats(callbacks, step, stepUsage, &stepStats); err != nil {
			return nil, err
		}

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

// completeStepStats fills the authoritative fields of a step's stats
// (step number, output tokens from provider usage, tok/s) and fires
// OnStepStats.
//
// TokensPerSec is the end-to-end throughput — OutputTokens over
// the whole round-trip Duration (request/network latency and TTFT
// included). Deliberately simple and gate-free: any completed step with
// output tokens gets a speed, so the display never blanks out. It is NOT
// the server-side decode rate (that cannot be observed exactly from the
// client; subtracting TTFT would inflate short/burst outputs). Returns
// the callback error, if any.
func (a *Agent) completeStepStats(callbacks StreamCallbacks, step int, stepUsage Usage, stepStats *StepStats) error {
	stepStats.Step = step
	stepStats.OutputTokens = stepUsage.OutputTokens
	if stepStats.Duration > 0 && stepStats.OutputTokens > 0 {
		stepStats.TokensPerSec = float64(stepStats.OutputTokens) / stepStats.Duration.Seconds()
	}
	if callbacks.OnStepStats != nil {
		return callbacks.OnStepStats(*stepStats)
	}
	return nil
}

// streamEvents iterates streaming events, firing callbacks and assembling the
// step's record. Returns the assembled content parts (assistant response +
// tool results), usage, and whether the response was truncated. History IDs are
// minted by the assembler on each block's first appearance and passed to
// callbacks, so a window on screen and the part that gets persisted are the same
// block.
//
// Tool goroutines (started per ToolInputCompleteEvent) are tracked with a
// WaitGroup and run under a per-stream context. On ANY exit path — stream error,
// callback failure, pairing failure, or a panic inside the loop — the context is
// canceled first and all tool goroutines are waited on before returning, so no
// tool keeps executing (and no goroutine leaks) after the stream has errored.
// Once they settle, the deferred salvage builds the step's record from whatever
// streamed: reasoning and text as received, plus every tool call whose result
// arrived. A panic is converted into the step's error rather than allowed to
// unwind, because the task goroutine has no recover and a crash would discard
// content the user already watched.
//
//nolint:gocyclo // switch dispatch over 8 event types with callback guards
func (a *Agent) streamEvents(ctx context.Context, events iter.Seq2[StreamEvent, error], callbacks StreamCallbacks, stepStart time.Time, stats *StepStats) (stepContents []ContentPart, stepUsage Usage, truncated bool, err error) {
	var results []ContentPart // tool outputs as they are collected

	// Per-stream context, canceled on every exit path (defer below).
	// Deriving from the caller's ctx keeps task cancellation working:
	// when the caller cancels, this ctx is canceled too, and vice versa.
	streamCtx, cancelStream := context.WithCancel(ctx)

	// Channel for collecting all tool execution results.
	// Buffered so sender goroutines exit immediately after execution.
	// Capacity 16 covers all tool results in practice.
	resultCh := make(chan ContentPart, 16)

	// Tracks in-flight tool goroutines. Deferred cleanup order (LIFO):
	//   cancelStream → toolWg.Wait → salvage
	// cancelStream must run before toolWg.Wait so canceled tools terminate;
	// salvage runs LAST, after every tool goroutine has finished, so all
	// surviving sends are already in resultCh and the drain is
	// deterministic (no race with in-flight senders).
	//
	// What a failed step still contributes: the reasoning and text streamed so
	// far, plus tool pairs whose results arrived. The content a step produced is
	// part of what happened — replaying a bare tool_use to an endpoint that
	// expects the turn's reasoning alongside it (see openaiConvertContents,
	// which pads an empty reasoning field rather than omitting the key) shows
	// the model a turn it never emitted, and the longer the thinking, the worse
	// that reads. A partial answer is kept for the same reason max_tokens
	// truncation keeps one: it is what lets :continue resume the turn.

	// The step's record, assembled here from the stream: content, block
	// identity, history IDs, and layout. One assembler serves every path out of
	// this function — a step that finished, one that errored, one that was
	// canceled — so what a turn contributes to history cannot depend on how it
	// ended. Its IDs are minted when a block first appears, which is the same
	// moment an adapter first hears about it, so a window on screen and the part
	// that gets persisted are two views of one block.
	//
	// callbacks.IDGen may be nil: a caller that does not persist content asks for
	// no numbering, and the blocks still carry their content.
	assembler := newStreamAssembler(callbacks.IDGen)

	var toolWg sync.WaitGroup
	defer func() {
		// A panic in the loop below is a bug, and a bug is not a reason to take
		// the user's content with it: what they watched stream in is already in
		// the assembler, so convert the panic into the step's error and let the
		// salvage run. The caller then handles it as it handles any failed step —
		// record saved, error reported, session still alive. Without this the
		// named err stays nil, the salvage below skips itself, and the unwinding
		// reaches the task goroutine, which has no recover: the process dies
		// holding nothing.
		//
		// The stack goes into the error because recovering is what hides it, and
		// a swallowed panic nobody can diagnose is worse than a crash.
		panicked := false
		if r := recover(); r != nil {
			panicked = true
			err = fmt.Errorf("agent: panic while streaming the step: %v\n%s", r, debug.Stack())
		}
		if err == nil && !panicked {
			return
		}
		// A cut step keeps what it streamed. Calls whose results never arrived
		// are left out by the forgiving policy below, because an assistant
		// tool_use without its tool_result is a conversation the next request
		// cannot build.
		contributed, pairErr := attachToolResults(assembler.parts(), collectResults(results, resultCh), true)
		if pairErr != nil {
			return
		}
		if len(contributed) > 0 {
			stepContents = contributed
		}
	}()
	defer toolWg.Wait()
	defer cancelStream()

	execCount := 0

	for event, err := range events {
		if err != nil {
			return nil, Usage{}, false, err
		}

		// A note on every case below: the content method runs before the
		// callback guard, and so does the history ID it mints along with the
		// block. Whether the caller wants to *display* a block must not decide
		// whether that block *has an ID* or *has content* — a missing callback
		// used to leave the persisted part holding the zero value,
		// indistinguishable from "never numbered".
		switch e := event.(type) {
		case TextDeltaEvent:
			stats.setFirstToken(stepStart)
			assembler.text(e.Position, e.Key, e.Delta)
			if callbacks.OnTextDelta != nil {
				if err := callbacks.OnTextDelta(e.Delta, assembler.historyID(e.Key)); err != nil {
					return nil, Usage{}, false, err
				}
			}

		case TextCompleteEvent:
			// A boundary naming a block the stream never opened is refused here
			// and at the assembler: no content, no ID, no empty window.
			exists := assembler.close(e.Position, e.Key)
			if exists && callbacks.OnTextComplete != nil {
				if err := callbacks.OnTextComplete(assembler.body(e.Key), assembler.historyID(e.Key)); err != nil {
					return nil, Usage{}, false, err
				}
			}

		case ReasoningDeltaEvent:
			stats.setFirstToken(stepStart)
			assembler.reasoning(e.Position, e.Key, e.Delta)
			if callbacks.OnReasoningDelta != nil {
				if err := callbacks.OnReasoningDelta(e.Delta, assembler.historyID(e.Key)); err != nil {
					return nil, Usage{}, false, err
				}
			}

		case ReasoningCompleteEvent:
			exists := assembler.close(e.Position, e.Key)
			if exists && callbacks.OnReasoningComplete != nil {
				if err := callbacks.OnReasoningComplete(assembler.body(e.Key), assembler.historyID(e.Key)); err != nil {
					return nil, Usage{}, false, err
				}
			}

		case ToolInputStartEvent:
			assembler.toolStart(e.Position, e.Key, e.ID, e.Name)
			if callbacks.OnToolInputStart != nil {
				if err := callbacks.OnToolInputStart(e.ID, e.Name, assembler.historyID(e.Key)); err != nil {
					return nil, Usage{}, false, err
				}
			}

		case ToolInputDeltaEvent:
			stats.setFirstToken(stepStart)
			assembler.toolArgs(e.Position, e.Key, e.Delta)
			if callbacks.OnToolInputDelta != nil {
				if err := callbacks.OnToolInputDelta(e.ID, e.Delta, assembler.historyID(e.Key)); err != nil {
					return nil, Usage{}, false, err
				}
			}

		case ToolInputCompleteEvent:
			if !assembler.close(e.Position, e.Key) {
				break // a call with no streamed arguments is not a call to run
			}
			// The assembled arguments, repaired, become the part that both
			// execution and history use — one value for both, so a tool cannot
			// run with an input different from the one it was recorded with.
			_, name, args := assembler.toolCall(e.Key)
			input := json.RawMessage(args)
			repairToolInput(&input, name, a.config.Tools)
			tc := assembler.beginToolCall(e.Key, input)
			if tc == nil {
				// The key was opened as a different kind of block, so this
				// boundary describes something the stream never started.
				break
			}

			if callbacks.OnToolInputComplete != nil {
				if err := callbacks.OnToolInputComplete(tc.ID, tc.Input, tc.HistoryID); err != nil {
					return nil, Usage{}, false, err
				}
			}
			execCount++
			a.handleStreamedToolInput(streamCtx, tc, callbacks, resultCh, &toolWg)

		case StepCompleteEvent:
			stepUsage = e.Usage
			// The record is built here from what streamed, exactly as the
			// failed-step path above builds it from what streamed — same
			// assembler, so a step cannot be described two ways depending on how
			// it ended. Tool calls are still incomplete at this point: their
			// results are collected below and attached there.
			stepContents = assembler.parts()
			if e.StopReason == "max_tokens" || e.StopReason == "length" {
				truncated = true
			}
			// Stream ended: capture the round-trip duration (request →
			// stream end, TTFT included). Tool execution happens after
			// this event (parallel goroutines collected below), so tool
			// time is excluded from the speed measurement.
			stats.Duration = time.Since(stepStart)
		}
	}

	// All tools (confirm and no-confirm) execute in goroutines started
	// during streaming. Collect all results.
	//
	// The select on streamCtx.Done prevents a deadlock: a result may never
	// arrive for a tool that was awaiting confirmation when the task was
	// canceled (its goroutine returns without sending), so a bare receive
	// would block forever. In that case we bail out; the deferred
	// cancel+wait settles the goroutines, then salvage recovers whatever
	// results did arrive.
	for i := 0; i < execCount; i++ {
		select {
		case r := <-resultCh:
			results = append(results, r)
		case <-streamCtx.Done():
			return nil, Usage{}, false, streamCtx.Err()
		}
	}

	// Attach each result to the call it answers, in the order the model made
	// them. A step that claims it finished must have an answer for every call it
	// recorded, so this is the strict path: a missing or ambiguous pairing is a
	// defect to fail on, not content to drop.
	reordered, reorderErr := attachToolResults(stepContents, results, false)
	if reorderErr != nil {
		return nil, Usage{}, false, reorderErr
	}
	stepContents = reordered

	return stepContents, stepUsage, truncated, nil
}

// handleStreamedToolInput processes a completed tool call during streaming.
// If the tool requires confirmation (per ToolNeedsConfirm), it starts a
// goroutine that obtains a per-tool confirm channel and blocks until the
// user responds. Otherwise it executes immediately in a goroutine.
// genHistoryID numbers a part that was not streamed — a tool result, which the
// model's reply never mentions and which the step's assembler therefore never
// saw. It takes from the same counter as the streamed blocks, so the two kinds
// stay in one sequence with no gap and no collision.
func genHistoryID(callbacks StreamCallbacks) uint64 {
	if callbacks.IDGen != nil {
		return callbacks.IDGen()
	}
	return 0
}

// hasToolInputs reports whether a step's record contains a tool call, which is
// what decides whether the agent loop continues to execute it or the turn is
// over.
func hasToolInputs(contents []ContentPart) bool {
	for _, p := range contents {
		if _, ok := p.(*ToolInputPart); ok {
			return true
		}
	}
	return false
}

// All tools send exactly one result through resultCh, then call wg.Done().
func (a *Agent) handleStreamedToolInput(ctx context.Context, tc *ToolInputPart, callbacks StreamCallbacks, resultCh chan<- ContentPart, wg *sync.WaitGroup) {
	if callbacks.ToolNeedsConfirm != nil && callbacks.ToolNeedsConfirm(tc.Name) {
		// Start goroutine that waits for confirmation before executing.
		historyID := genHistoryID(callbacks)
		wg.Add(1)
		go func(ctx context.Context, tc *ToolInputPart, historyID uint64) {
			defer wg.Done()
			select {
			case allowed := <-callbacks.OnToolConfirm(tc.ToConfirmRequest()):
				if !allowed {
					sendToolResult(ctx, resultCh, newToolOutput(callbacks, tc.ID, nil, fmt.Errorf("Tool execution denied by user"), historyID))
					return
				}
				sendToolResult(ctx, resultCh, a.executeTool(ctx, tc, callbacks, historyID))
			case <-ctx.Done():
				// Canceled while waiting for confirmation — the tool never
				// executed, so it must NOT enter the salvaged history (it
				// would look as if it ran and failed; the collector bails
				// on the same cancellation, so no result can deadlock it).
				// But the AF start frame already created the tool window in
				// the UI, which would otherwise stay stuck spinning with no
				// UF frame to settle it. newToolOutput fires the
				// OnToolOutput callback (UF isError → ✗) as a display-only
				// frame; the returned part is deliberately discarded so the
				// tool stays out of history.
				_ = newToolOutput(callbacks, tc.ID, nil,
					fmt.Errorf("tool execution canceled while awaiting confirmation"), historyID)
			}
		}(ctx, tc, historyID)
		return
	}

	historyID := genHistoryID(callbacks)
	wg.Add(1)
	go func(tc *ToolInputPart, historyID uint64) {
		defer wg.Done()
		sendToolResult(ctx, resultCh, a.executeTool(ctx, tc, callbacks, historyID))
	}(tc, historyID)
}

// sendToolResult delivers a tool result to the collector. It is designed
// around two channel states, each with a distinct guarantee:
//
//  1. Room available (common case, ≤16 in-flight results): the first
//     non-blocking send delivers immediately — even if the stream context
//     is already canceled. This is what makes the salvage drain
//     deterministic: salvage runs after all tool goroutines settle, so
//     every result that had room lands in resultCh and is recovered. A
//     single select between send and ctx.Done here would randomly drop
//     results when cancellation raced a just-completed tool — the bug
//     this two-step design replaces.
//
//  2. Channel full (more in-flight results than the 16-slot buffer): the
//     sender must WAIT rather than drop. On the normal path the collector
//     is actively draining and needs exactly execCount results — dropping
//     would starve it and deadlock the step. The ctx.Done case is the
//     escape hatch for the error path: once the collector has bailed,
//     nobody drains, so a blocked send would hang this goroutine — and
//     with it toolWg.Wait and Stream — forever; cancellation releases it.
//
// Net effect: never blocks when delivery is possible, never leaks when it
// isn't, and the salvage drain always sees whatever could be delivered.
func sendToolResult(ctx context.Context, resultCh chan<- ContentPart, result ContentPart) {
	// Room available — deliver unconditionally (cancel or not). On error
	// paths the salvage drain, which runs after all tool goroutines
	// settle, picks the result up; on the normal path the collector does.
	select {
	case resultCh <- result:
		return
	default:
	}

	// Channel full — block until the collector drains (normal path), or
	// the stream is canceled (error path: nobody will drain, so drop
	// rather than hang toolWg.Wait forever).
	select {
	case resultCh <- result:
	case <-ctx.Done():
	}
}

func (a *Agent) toolDefinitions() []ToolDefinition {
	defs := make([]ToolDefinition, len(a.config.Tools))
	for i, tool := range a.config.Tools {
		defs[i] = tool.Definition
	}
	return defs
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

	var content []ContentPart
	var err error
	if tool.ExecuteStreaming != nil {
		// Streaming variant: deliver ephemeral preview snapshots via
		// onDelta while the tool runs. The authoritative result is still
		// the returned []ContentPart — deltas are display-only.
		onDelta := func(text string) {
			if callbacks.OnToolOutputDelta != nil {
				// Preview frames are best-effort: a write failure must
				// not abort the tool execution.
				_ = callbacks.OnToolOutputDelta(tc.ID, text, historyID)
			}
		}
		content, err = tool.ExecuteStreaming(ctx, tc.Input, onDelta)
	} else {
		content, err = tool.Execute(ctx, tc.Input)
	}
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

// repairToolInput repairs a tool input in-place using the tool's schema.
// If the tool is not found or has no schema, the input is left unchanged.
func repairToolInput(input *json.RawMessage, toolName string, tools []Tool) {
	if schema := findToolSchema(toolName, tools); schema != nil {
		if fixed := RepairToolInput(*input, schema); string(fixed) != string(*input) {
			*input = fixed
		}
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
