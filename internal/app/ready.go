package app

import "sync"

// ReadySignal broadcasts the session's one-shot "ready" event to adapter
// goroutines.
//
// The session emits exactly one SM "session" frame with state "ready"
// (see agent's sessionMsg) — the authoritative "replay and MCP init
// complete, prompts are accepted" signal. The adapter's frame parser marks
// it; the input feeder waits on it before submitting a prompt, so a piped
// prompt cannot race MCP initialization and be rejected with MCP_NOT_READY.
type ReadySignal struct {
	once sync.Once
	ch   chan struct{}
}

// NewReadySignal returns an unmarked ReadySignal.
func NewReadySignal() *ReadySignal {
	return &ReadySignal{ch: make(chan struct{})}
}

// Mark broadcasts readiness. Safe from any goroutine; idempotent, so
// repeated "ready" frames (there should be none) are harmless.
func (r *ReadySignal) Mark() {
	r.once.Do(func() { close(r.ch) })
}

// Wait returns a channel that is closed once the signal is marked.
func (r *ReadySignal) Wait() <-chan struct{} {
	return r.ch
}
