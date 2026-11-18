package llm

import "sync/atomic"

// ReasoningLevel is the reasoning effort a request asks for: 0=off,
// 1=normal, 2=max.
//
// It is a value object shared rather than copied, and that is why it is a type.
// The level is the one request setting that changes while a request may already
// be in flight: the session stores a new one when :reasoning_level_set arrives,
// and a provider reads the current one while building the next body. As a field
// on each side those were two integers kept in step by hand — one seeding the
// other at every provider construction. As one object there is nothing to keep in
// step.
//
// The session owns the instance and a provider borrows it, so it must outlive
// every provider built from it: true by construction, since the model service
// creates them all, and protected against a copy by copylocks (the atomic below
// makes the owner non-copyable). One instance serves one session — the day two
// providers holding different levels must live at once, the level stops being a
// session setting and becomes a request parameter.
//
// Its zero value is usable and means level 0, so a provider built without one — a
// provider used outside a session, a test — carries its own and behaves exactly
// as the field it replaced did.
//
// Access is atomic because the reader is a different goroutine from the writer,
// and no invariant ties the level to another field: a request built from the old
// level and one built from the new are both coherent, and the command's promise
// is precisely "from the next request on".
type ReasoningLevel struct {
	level atomic.Int32
}

// NewReasoningLevel returns a level object holding level.
func NewReasoningLevel(level int) *ReasoningLevel {
	r := &ReasoningLevel{}
	r.Set(level)
	return r
}

// Set records a new level.
//
// Callable while a request is being built: the request already on the wire keeps
// the level it was built with, and the next one reads this.
func (r *ReasoningLevel) Set(level int) {
	r.level.Store(int32(level)) //nolint:gosec // G115: a level is 0..2, refused by every entry point (flag, session file, command)
}

// Get returns the current level.
func (r *ReasoningLevel) Get() int { return int(r.level.Load()) }
