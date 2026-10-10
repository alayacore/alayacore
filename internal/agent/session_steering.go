package agent

import "github.com/alayacore/alayacore/internal/llm"

// Steering: a prompt that arrives while a task is already running.
//
// The session runs one task at a time and has no task queue (see the run()
// comment in session_loop.go). Steering is not one. It does not park a second
// task — it splices the user's words into the turn that is already in flight,
// at the one point where that is both safe and useful: the boundary between two
// agent steps.
//
// Three properties decide the whole design.
//
// The boundary is the only injection point. A step boundary is the only place
// the agent loop lets a caller rewrite the history (OnBeforeSend) and the only
// place the history is a valid request — by then attachToolResults has paired
// every tool call of the previous step with its result. Words typed while a
// tool is still running therefore reach the model *after* that tool returns,
// never between a call and its answer.
//
// Delivery is a two-phase commit. A part is numbered, echoed to the adapter and
// published into Contents only at the moment the model has actually received it
// — its step's OnStepFinish. Until then it is unnumbered and lives only in this
// queue. That is what keeps a dangling history ID out: the adapter is never
// shown an ID that Contents does not hold, because the echo and the part's entry
// into Contents come from the same commit (the part rides that step's delta —
// see processPrompt's onBeforeSend for why prevLen is not advanced for it).
//
// One policy decides where a steering message ends up, and it has no exceptions:
// it is delivered if the turn it was typed into landed, and dropped if it did
// not. ("A turn" throughout this file means a userTurn — the kind
// processPrompt is told the call is; see promptKind.)
//
//   - Landed → delivered: spliced at a boundary, or — when the turn ended before
//     a boundary could carry it (the model answered without calling another
//     tool, which is what "type the next thing while it is working" means, and
//     the common case) — as the next prompt.
//   - Did not land → dropped, with an SM notify. The words are a modification of
//     the turn they were typed into, not a message with a life of their own;
//     with that turn gone (an error, a cancel) there is nothing left for them to
//     modify, and re-delivering them would either spend a turn nobody asked for
//     or fold a correction into whatever the user types next, where it would
//     read as something they never said. A canceled turn is the clearest
//     instance: a queue that survived it would start a task on its own the
//     moment the canceled one unwound — a stop button that keeps going.
//
// Both halves of an undelivered batch are covered, because they live in different
// places: what is still queued (drained by run()'s cancelTask on a cancel, by
// processPrompt's deferred discard on a failure) and what a step had already
// spliced in (that same discard). A batch whose step *did* finish is committed
// and stays — the model has read it, so a later failure cannot take it back. The
// drop is always announced: the words are in neither the transcript nor the
// session file, so the user has to be told, and retyping them is their call.
// Input arriving after a turn ends is untouched by any of this, because it was
// never part of the turn that ended.
//
// The queue is reachable only from a userTurn, and that follows from the kind
// rather than from any knowledge held here: processPrompt installs the
// step-boundary hook through which this queue can be reached for that kind
// alone (see promptKind), so a summarizeCall can no more take the words than it
// can publish its own steps. The gate is not bookkeeping — a summarize call's
// history is a copy that either replaces the conversation or is thrown away, so
// words consumed by it would vanish with nothing to report, because from the
// queue's point of view they had been delivered. The policy above then applies
// to a summarizeCall exactly as it does to a userTurn — did the call land? — so
// a summarize that fails is a call that did not land, and its queue goes with it.
//
// The acknowledgement is an SM notify, not a new SM type. messageVersion — the
// wire protocol version an adapter checks — is also the session file's
// compatibility version: parseSessionMeta demands an exact match, so an additive
// SM type would make every saved session refuse to load. The notify says the
// same thing with vocabulary that already exists, and the message itself
// announces its own arrival by being echoed with its history ID. Adding a
// dedicated `steering` type (and paying the version bump, and the migration)
// is worth doing when an adapter needs to distinguish the case mechanically
// rather than by reading a line of text.

// maxSteeringParts bounds the queue. A prompt arriving with a task in flight is
// accepted only while there is room, so a client that sends without reading
// cannot grow the session's memory without limit; past the bound the prompt is
// refused with the same BUSY error a busy session has always returned. The
// bound is on parts, not messages, because that is what the queue stores — an
// attachment-heavy prompt occupies more of it than a sentence.
const maxSteeringParts = 16

// queueSteering appends parts to the steering queue, reporting whether they
// fitted. Called from run() (submitPrompt) — never from a task goroutine.
func (s *Session) queueSteering(parts []llm.ContentPart) bool {
	s.steeringMu.Lock()
	defer s.steeringMu.Unlock()
	if len(s.steering)+len(parts) > maxSteeringParts {
		return false
	}
	s.steering = append(s.steering, parts...)
	return true
}

// takeSteering removes and returns everything currently queued, preserving
// order. Called at a step boundary (the task goroutine) and after a task ends
// (run()); the two cannot overlap — see deliverLeftoverSteering.
func (s *Session) takeSteering() []llm.ContentPart {
	s.steeringMu.Lock()
	defer s.steeringMu.Unlock()
	if len(s.steering) == 0 {
		return nil
	}
	parts := s.steering
	s.steering = nil
	return parts
}

// discardUndeliveredSteering drops every word the turn was still going to be
// told — the batch its step had already spliced in (batch) and whatever is still
// queued — and says so. It is the "did not land" half of the policy above, and
// the reason a failed turn does not leave its steering to run on afterwards.
//
// The notice is what keeps that from being the silent kind: the words are in
// neither the transcript nor the session file, so the user has to be told, and
// retyping them is their call to make.
func (s *Session) discardUndeliveredSteering(batch []llm.ContentPart) {
	if len(batch)+len(s.takeSteering()) == 0 {
		return
	}
	s.writeNotify("steering dropped — the turn ended before it could be delivered")
}

// deliverLeftoverSteering hands steering that is still queued when a task ends
// to the ordinary prompt path, so it becomes the next turn.
//
// What is queued at that point is, by construction, input that arrived after the
// turn's last step boundary — and the turn either landed (so it is the user's
// next request, typed while the last answer was still streaming) or it was
// canceled/turned out to have failed, in which case processPrompt already
// discarded everything that belonged to it. Either way what is left here is
// fresh intent, and it is delivered rather than held: this is the case that
// makes "type the next thing while it is working" work.
//
// Ordering: called from run()'s handleTaskDone, after Contents has been
// committed and the idle state broadcast. The task goroutine's last touch of the
// queue is its discard, which happens before it sends taskResultCh — and
// taskResultCh is what gets us here — so no boundary can be racing this.
func (s *Session) deliverLeftoverSteering() {
	parts := s.takeSteering()
	if len(parts) == 0 {
		return
	}
	if s.quitting {
		// The session is leaving and would refuse the task anyway; dropping
		// the parts here is what keeps them from holding the exit open.
		return
	}
	s.submitPrompt(parts)
}
