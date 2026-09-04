package llm

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// This file is the whole of "run a tool call": the two drivers that decide when a
// call starts, the lifecycle every call goes through, and the leaves that execute
// a tool and turn its output into a history part. Nothing here assembles a step
// or reads a stream — that is agent.go, and the seam between the two files is the
// three-symbol interface below.
//
// A step's tool calls are run by one of two drivers, behind this interface.
// streamEvents never asks which one it was given: it hands each completed call to
// add while the stream runs, and calls finish once the stream has ended. The mode
// is read exactly once, in newToolRunner, and has no other footprint in the
// streaming loop.
//
// Two drivers exist because the modes differ in one thing that cannot be shared:
// when a call may start. The parallel driver launches a call as soon as its
// arguments finish streaming, so calls overlap each other and the rest of the
// stream; the serial driver cannot know the model's full sequence until the stream
// is over. What a call *does* — confirmation, execution, its result, and what
// cancellation means — lives in runToolCall, shared, so neither driver can drift
// from the other on the semantics that matter.
//
// The stream's context is passed to both methods rather than stored on the
// driver: revive's context-as-argument rule is on, and this package holds no
// context in any struct — the driver borrows the stream's lifetime, it does not
// own it.
//
// One obligation comes with passing instead of holding: both methods must be
// given the same per-stream context. The parallel driver launches work under the
// one it gets in add and decides when to stop waiting under the one it gets in
// finish, so two different contexts would split its cancellation — a goroutine
// that cannot be canceled by anything the wait reacts to. streamEvents calls both
// a few lines apart with one local variable, which is why this is a comment and
// not a mechanism.
type toolRunner interface {
	// add takes one call whose arguments have completed, with the history ID
	// already minted for the result it will produce. The ID is minted by the
	// caller rather than here because numbering follows arrival, and arrival has
	// stopped by the time finish runs.
	add(ctx context.Context, tc *ToolInputPart, historyID uint64)

	// finish runs whatever has not run yet and returns every result the step
	// produced. A non-nil error means the step was cut short with calls
	// outstanding; the results produced up to that point come back with it, so
	// salvage can record the side effects that really happened.
	finish(ctx context.Context) ([]ContentPart, error)
}

// newToolRunner picks the driver for the agent's configured mode.
//
// The result channel and WaitGroup are passed in rather than owned: their
// deferred cancellation and wait are what guarantee no tool outlives the stream,
// and the salvage drain reads the channel directly, so both have to stay visible
// to streamEvents. The parallel driver uses them; the serial driver has no use
// for either — it is the only thing running, with nothing to wait for and no one
// to gather results from.
//
// The channel is bidirectional, not send-only, because the one driver that uses
// it both sends results into it and drains it in finish. sendToolResult still
// takes a send-only view, so the send half keeps its direction check.
func (a *Agent) newToolRunner(callbacks StreamCallbacks, resultCh chan ContentPart, wg *sync.WaitGroup) toolRunner {
	if a.config.SerialToolCalls {
		return &serialToolRunner{agent: a, callbacks: callbacks}
	}
	return &parallelToolRunner{agent: a, callbacks: callbacks, resultCh: resultCh, wg: wg}
}

// queuedCall is a tool call the serial driver has accepted but not started.
type queuedCall struct {
	part      *ToolInputPart
	historyID uint64
}

// serialToolRunner runs a step's calls one at a time, in the order the model made
// them, each awaited before the next begins.
//
// It exists because a great many models and servers have no notion of parallel
// tool calls. Getting those endpoints to emit one call at a time is not something
// a client can enforce; the ordering is what the model observes as a consequence,
// so that is what the client enforces.
//
// The queue arrives in the model's own call order, because both providers declare
// it rather than let transport decide: OpenAI sorts tool closures by
// `tool_calls[].index`, Anthropic delivers them by block index (see
// docs/providers.md → "Complete-event order"). The driver imposes no order of its
// own — it runs the queue as built.
//
// Starting only after the stream ends buys two properties the parallel driver
// cannot have: a stream that fails before then runs nothing at all, and a
// cancellation between two calls leaves the executed pairs recorded while the
// never-started calls are dropped. Hence finish reporting the context error
// rather than returning a short list: streamEvents' strict pairing would otherwise
// call "tool result missing" a defect over a call the cancellation legitimately
// prevented.
type serialToolRunner struct {
	agent     *Agent
	callbacks StreamCallbacks
	queue     []queuedCall
}

// The context is deliberately not taken here: nothing starts at add time, and the
// one that will run this call is the one finish is handed.
func (r *serialToolRunner) add(_ context.Context, tc *ToolInputPart, historyID uint64) {
	r.queue = append(r.queue, queuedCall{part: tc, historyID: historyID})
}

func (r *serialToolRunner) finish(ctx context.Context) ([]ContentPart, error) {
	results := make([]ContentPart, 0, len(r.queue))
	for _, q := range r.queue {
		result, ran := r.agent.runToolCall(ctx, q.part, q.historyID, r.callbacks)
		if ran {
			results = append(results, result)
		}
		// Checked after the call, not before, so a tool that ran into a
		// cancellation mid-flight still reports its result — which is what the
		// parallel driver would have delivered for the same call.
		if err := ctx.Err(); err != nil {
			return results, err
		}
	}
	return results, nil
}

// parallelToolRunner launches one goroutine per call the moment its arguments
// complete, so calls overlap each other and the rest of the stream, then gathers
// their results in the order they happened to arrive.
//
// The step's history order does not depend on that arrival order:
// attachToolResults re-pairs every result with its call by ID, in the sequence the
// model used.
type parallelToolRunner struct {
	agent     *Agent
	callbacks StreamCallbacks
	resultCh  chan ContentPart
	wg        *sync.WaitGroup

	// launched counts goroutines, and finish waits for exactly that many
	// results. A call canceled while awaiting confirmation sends none — it never
	// executed — so the wait is a select on the context rather than a bare
	// receive. Without that, the step would deadlock on a result that can never
	// arrive; the deferred cancel settles the goroutines and salvage recovers
	// whatever did.
	launched int
}

func (r *parallelToolRunner) add(ctx context.Context, tc *ToolInputPart, historyID uint64) {
	r.launched++
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		if result, ran := r.agent.runToolCall(ctx, tc, historyID, r.callbacks); ran {
			sendToolResult(ctx, r.resultCh, result)
		}
	}()
}

func (r *parallelToolRunner) finish(ctx context.Context) ([]ContentPart, error) {
	results := make([]ContentPart, 0, r.launched)
	for i := 0; i < r.launched; i++ {
		select {
		case result := <-r.resultCh:
			results = append(results, result)
		case <-ctx.Done():
			return results, ctx.Err()
		}
	}
	return results, nil
}

// ────────────────────────────────────────────────────────────────────────────
//
// What a driver runs: the life of one call, then the leaves.
//
// These four are here rather than in agent.go because nothing outside this file
// calls them — the drivers call runToolCall and sendToolResult, runToolCall calls
// executeTool and newToolOutput, and executeTool calls newToolOutput. That closes
// the group: moving one of them without the others would split the chain instead
// of gathering it.
// ────────────────────────────────────────────────────────────────────────────

// runToolCall is the whole life of one tool call: ask for confirmation when the
// tool needs it, execute, and produce the part that answers the call. It is the
// only implementation of that sequence — the parallel driver runs it in a
// goroutine per call, the serial driver runs it in a loop — so the two modes
// cannot disagree about what a denied, failed, or canceled call means.
//
// ok=false reports a call that never executed (canceled while awaiting
// confirmation), and such a call must stay out of history: an assistant
// tool_use without the tool_result that answers it is a conversation the next
// request cannot build (see gotcha 3). The output part is still built for the
// display, because the AF frame already opened a tool window that needs a UF
// frame to settle it.
func (a *Agent) runToolCall(ctx context.Context, tc *ToolInputPart, historyID uint64, callbacks StreamCallbacks) (ContentPart, bool) {
	if callbacks.ToolNeedsConfirm == nil || !callbacks.ToolNeedsConfirm(tc.Name) {
		return a.executeTool(ctx, tc, callbacks, historyID), true
	}

	select {
	case allowed := <-callbacks.OnToolConfirm(tc.ToConfirmRequest()):
		if !allowed {
			return newToolOutput(callbacks, tc.ID, nil, errors.New("Tool execution denied by user"), historyID), true
		}
		return a.executeTool(ctx, tc, callbacks, historyID), true
	case <-ctx.Done():
		// Canceled while waiting for confirmation — the tool never executed, so
		// it must NOT enter the salvaged history (it would look as if it ran and
		// failed; both collectors bail on the same cancellation, so no result can
		// deadlock either of them). But the AF start frame already created the
		// tool window in the UI, which would otherwise stay stuck spinning with
		// no UF frame to settle it. newToolOutput fires the OnToolOutput callback
		// (UF isError → ✗) as a display-only frame; the returned part is
		// deliberately discarded so the tool stays out of history.
		_ = newToolOutput(callbacks, tc.ID, nil,
			errors.New("tool execution canceled while awaiting confirmation"), historyID)
		return nil, false
	}
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
