// session_event.go

package agent

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

// stepFinishEvent signals that an agent step has completed.
// Carries only token usage metadata. The final message state and
// ContentParts are returned together via taskResultCh on task completion.
type stepFinishEvent struct {
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
}

func (stepFinishEvent) taskEvent() {}

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
