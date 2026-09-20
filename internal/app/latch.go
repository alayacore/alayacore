package app

import "sync"

// Latch is a one-shot broadcast: Mark closes it, once, and any number of
// goroutines can wait on Done.
//
// It exists so an adapter's frame parser can hand a fact to the goroutines
// driving Start() without those goroutines reaching into the session. The fact
// in question is the session's terminal frame (SM "session", state "closed"):
// the parser sees it, marks the latch, and Start's own selects wake up. What
// the adapter learns, it learns from the wire — which is the only thing a
// client outside this process could learn it from either.
type Latch struct {
	once sync.Once
	ch   chan struct{}
}

// NewLatch returns an unmarked Latch.
func NewLatch() *Latch {
	return &Latch{ch: make(chan struct{})}
}

// Mark closes the latch. Safe from any goroutine; idempotent, so a repeated
// frame is harmless.
func (l *Latch) Mark() {
	l.once.Do(func() { close(l.ch) })
}

// Done returns a channel that is closed once the latch is marked.
func (l *Latch) Done() <-chan struct{} {
	return l.ch
}
