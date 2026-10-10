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
// Delivery is an admission at the splice. A part is fitted, given its user role,
// numbered, echoed to the adapter and published to Contents the moment it enters
// a request — in processPrompt's onBeforeSend, the same point in a part's life at
// which runTaskNormal does all of that for a fresh prompt (spliceUserParts is
// that one implementation). Two things follow, and they are the whole of the
// mechanism's correctness: the words are shown before the answer they steered
// (the adapter draws windows in frame order — see spliceUserParts for why the
// echo cannot wait for the step's finish), and an ID the adapter is shown always
// resolves in Contents, on any outcome — a step that fails takes nothing back,
// because processPrompt puts what the boundary appended into the returned
// Contents. :save and :fork therefore agree with the transcript at every
// instant, not merely once the step is over.
//
// One policy decides where a steering message ends up, and it turns on a single
// question: had the turn already sent it — spliced it in — by the time the turn
// ended? ("A turn" throughout this file means a userTurn, the kind processPrompt
// is told the call is; see promptKind.)
//
//   - Spliced → delivered. It was numbered, echoed and sent; it is in the
//     conversation from that moment, and a failure or a cancel afterwards does
//     not take it back — the model has been given it, and Contents holds it.
//   - Not spliced, still in the queue when the turn ended → delivered as the
//     next prompt if the turn landed (this is "type the next thing while it is
//     working", and the common case), dropped with an SM notify if it did not.
//     The words never put anything on the wire, so a turn that dies cannot
//     leave them to run on afterwards: a queue that survived a cancel would
//     start a task on its own the moment the canceled one unwound — a stop
//     button that keeps going.
//
// The drop side has one place, not two: only the queue is ever dropped, because
// it is the only thing holding words the model has not been sent. run()'s
// cancelTask drains it on a cancel and processPrompt's deferred discard drains it
// on a failure; a part a boundary had already appended left the queue and is in
// the conversation, where nothing can take it back. Every drop is announced: the
// words are in neither the transcript nor the session file, so the user has to be
// told, and retyping them is their call. Input arriving after a turn ends is
// untouched by any of this, because it was never part of the turn that ended.
//
// The queue is reachable only from a userTurn, and that follows from the kind
// rather than from any knowledge held here: processPrompt installs the
// step-boundary hook through which this queue can be reached for that kind
// alone (see promptKind), so a summarizeCall can no more take the words than it
// can publish its own steps. The gate is not bookkeeping — a summarize call's
// history is a copy that either replaces the conversation or is thrown away, so
// words consumed by it would vanish with nothing to report, because from the
// queue's point of view they had been delivered. So a summarizeCall never touches
// the queue — it is given no step-boundary hook, and cannot splice — and the
// queue's fate is still the turn's: a summarize that fails is a turn that did not
// land, so whatever is queued at that moment goes with it (for a standalone
// :summarize that task is the turn; for a mid-turn compaction it is the enclosing
// one).
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

// discardQueuedSteering drops whatever is still in the queue and says so. It is
// the "not spliced" half of the policy above, and the reason a failed or
// canceled turn does not leave its steering to run on afterwards. Only the
// queue: a part a step had already spliced was sent and echoed, so it is in the
// conversation, and nothing here can or should take it back.
//
// The notice is what keeps the drop from being the silent kind: these words are
// in neither the transcript nor the session file, so the user has to be told,
// and retyping them is their call to make.
func (s *Session) discardQueuedSteering() {
	if len(s.takeSteering()) == 0 {
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
// drained the queue. Either way what is left here is fresh intent, and it is
// delivered rather than held: this is the case that makes "type the next thing
// while it is working" work.
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
