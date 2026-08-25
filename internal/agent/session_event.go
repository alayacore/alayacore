// session_event.go

package agent

import (
	"time"

	"github.com/alayacore/alayacore/internal/llm"
)

// Session actor model: channel-based state communication between the
// task goroutine and the run() goroutine.
//
// The task goroutine sends state mutations as typed events on taskEventCh.
// The run() goroutine processes them by type-switching in its main loop.
// This keeps all cross-goroutine communication explicit and auditable
// — the entire package uses channels and atomics for synchronization.

// taskEvent is a state mutation sent from the task goroutine to run().
// Each concrete type carries only its own fields — no shared struct.
type taskEvent interface {
	taskEvent()
}

// stepStartEvent signals that a new agent step has started.
type stepStartEvent struct {
	Step int
}

func (stepStartEvent) taskEvent() {}

// stepFinishEvent signals that an agent step has completed. It carries the
// step's newly produced content parts (NewParts) plus token usage metadata.
//
// Ownership: NewParts is a view into the agent's internal accumulation.
// The run() goroutine copies the element pointers into its own Contents
// slice and never retains the view — the agent may keep appending to the
// underlying array afterwards. Every part in NewParts is finalized
// (history ID, role, repaired tool input) and MUST NOT be mutated after
// publication; immutability-after-publish is the contract that makes the
// shared part objects race-free.
//
// The authoritative final contents still arrive via taskResultCh on task
// completion — these per-step deltas only give :save/:fork mid-task
// visibility and are self-corrected by the final replacement.
type stepFinishEvent struct {
	NewParts            []llm.ContentPart
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
}

func (stepFinishEvent) taskEvent() {}

// stepStatsEvent carries the just-finished step's speed metrics from the
// task goroutine to run(), which stores them for the status bar broadcast
// (lastStepTPS/lastTTFTMS). Totals are reset by the task's
// stepStartEvent(Step==1); no averaging is performed — the reported value
// is the latest step's simple end-to-end throughput (output tokens /
// round-trip duration). It is sent before the matching stepFinishEvent,
// so the finish broadcast always sees the updated values (single-sender
// FIFO on taskEventCh).
type stepStatsEvent struct {
	TokensPerSec     float64
	TimeToFirstToken time.Duration
}

func (stepStatsEvent) taskEvent() {}

// promptPartsEvent publishes finalized content parts that entered the
// task's working copy outside the agent loop (user prompt parts, the
// "Continue" marker). run() appends them to Contents. Parts must be
// finalized (IDs assigned) and immutable after publication.
type promptPartsEvent struct {
	Parts []llm.ContentPart
}

func (promptPartsEvent) taskEvent() {}

// contentsReplacedEvent publishes a mid-task wholesale replacement of the
// conversation (the auto-summarize result). Unlike append events, the
// replacement slice is copied before publication: the task goroutine keeps
// appending to its own copy afterwards, while run() takes full ownership of
// the published one. Sent only when auto-summarization succeeds.
type contentsReplacedEvent struct {
	Contents []llm.ContentPart
}

func (contentsReplacedEvent) taskEvent() {}

// cloneParts returns a shallow copy of parts (pointer array only — the
// ContentPart objects themselves are shared and immutable after publish).
// Used for ownership transfer on replacement events: the published slice
// must not alias a slice the task goroutine keeps mutating.
func cloneParts(parts []llm.ContentPart) []llm.ContentPart {
	return append([]llm.ContentPart(nil), parts...)
}

// setContextTokensEvent sets ContextTokens on the run() goroutine.
// Used by summarize() to correct the value after the stepFinishEvent
// from processPrompt overwrites it with the full old-context token count.
type setContextTokensEvent struct {
	Tokens int64
}

func (setContextTokensEvent) taskEvent() {}

// sendEvent sends a task event to the run() goroutine.
// Blocks until the event is received. The buffered channel (capacity 64)
// means this only blocks when run() is seriously backed up.
func (s *Session) sendEvent(ev taskEvent) {
	s.taskEventCh <- ev
}
