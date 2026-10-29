package agent

import (
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/protocol"
)

// Steering: a prompt that arrives while a task is already running.
//
// The session runs one task at a time and has no task queue (see the run()
// comment in session_loop.go). Steering is not one. It does not park a second
// task — it splices the user's words into the task that is already in flight,
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
// question: had the task already sent it — spliced it in — by the time the task
// ended? Only a task of kind userPrompt has a queue at all (see promptKind), so
// "the task" here is always that one.
//
//   - Spliced → delivered. It was numbered, echoed and sent; it is in the
//     conversation from that moment, and a failure or a cancel afterwards does
//     not take it back — the model has been given it, and Contents holds it.
//   - Not spliced, still in the queue when the task ended → delivered as the
//     next prompt if the task landed (this is "type the next thing while it is
//     working", and the common case), dropped with an SM notify if it did not.
//     The words never put anything on the wire, so a task that dies cannot
//     leave them to run on afterwards: a queue that survived a cancel would
//     start a task on its own the moment the canceled one unwound — a stop
//     button that keeps going. A cancel may therefore drop the queue
//     (dropQueuedSteering, what a bare :cancel does), or — only when the user
//     says so, with :cancel keep_steering — keep it, in which case this same
//     branch promotes it: the words are the user's next request, and the task
//     that would run them is one the user asked for. The distinction is not a
//     mode of the queue but who decided: a button that keeps going on its own is
//     a bug, one the user asked to keep going is an instruction.
//
// The drop side has one place, not two: only the queue is ever dropped, because
// it is the only thing holding words the model has not been sent. run()'s
// cancelTask drains it on a cancel — unless the cancel keeps it (steeringOnCancel)
// — and processPrompt's deferred discard drains it on a failure; a part a boundary
// had already appended left the queue and is in the conversation, where nothing
// can take it back. Every drop is announced: the words are in neither the
// transcript nor the session file, so the user has to be told, and retyping them
// is their call. Input arriving after a task ends is untouched by any of this,
// because it was never part of the task that ended.
//
// A kept queue stays a queue: it is not announced at the cancel, and it ends in
// the same place a landed task's leftover does — deliverLeftoverSteering, which
// reports it "delivered" and submits it. So keeping needs no second owner and no
// new terminal phase, and the contract that every queued submission ends in
// exactly one is untouched: what a cancel decides is when the queue's fate is
// settled, not whether a second place may settle it.
//
// The queue is reachable only from a userPrompt, and that follows from the kind
// rather than from any knowledge held here: processPrompt installs the
// step-boundary hook through which this queue can be reached for that kind
// alone (see promptKind), so a summarizeCall can no more take the words than it
// can publish its own steps. The gate is not bookkeeping — a summarize call's
// history is a copy that either replaces the conversation or is thrown away, so
// words consumed by it would vanish with nothing to report, because from the
// queue's point of view they had been delivered. So a summarizeCall never touches
// the queue — it is given no step-boundary hook, and cannot splice — and the
// queue's fate is still the task's: a summarize that fails is a task that did not
// land, so whatever is queued at that moment goes with it (for a standalone
// :summarize that task is the task; for a mid-task compaction it is the enclosing
// one).
//
// The lifecycle is reported as a "steering" system message — a dedicated SM
// type (phase: queued / delivered / dropped / refused), not a line of text an
// adapter has to match. This is the case an earlier revision deferred as "worth
// doing when an adapter needs to distinguish the case mechanically": it now
// does. Queued and refused are per submission; delivered and dropped are per
// batch, because the whole queue is spliced or discarded at once (takeSteering
// drains it), so there is never a partial delivery to correlate.
//
// The type was additive, but messageVersion moved 12 → 13 with it regardless
// (the version moves for additive changes; see messageVersion). Session files
// record that number and load only on an exact match, so an older file must be
// edited to load — see parseSessionMeta.

// maxSteeringParts bounds the queue. A prompt arriving with a task in flight is
// accepted only while there is room, so a client that sends without reading
// cannot grow the session's memory without limit; past the bound the prompt is
// refused with the same `STATE_BUSY` error a busy session has always returned. The
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
// canceled task does not leave its steering to run on afterwards. Only the
// queue: a part a step had already spliced was sent and echoed, so it is in the
// conversation, and nothing here can or should take it back.
//
// It is a no-op for a task a cancel already settled (steeringDrained). Those
// words are handled by cancelSteering the moment the cancel is processed; a
// second drain here would sweep up whatever the user typed *after* the cancel,
// while the canceled task was still unwinding — input that never belonged to
// the task being discarded (see TestPostCancelSteeringSurvivesDeferredDiscard).
// Under keepQueuedSteering the no-op is load-bearing for the queue itself: the
// batch a cancel spared is the batch this would otherwise drop.
//
// The notice is what keeps the drop from being the silent kind: these words are
// in neither the transcript nor the session file, so the user has to be told,
// and retyping them is their call to make.
func (s *Session) discardQueuedSteering() {
	s.steeringMu.Lock()
	dropped := false
	if !s.steeringDrained && len(s.steering) > 0 {
		s.steering = nil
		dropped = true
	}
	s.steeringMu.Unlock()
	if dropped {
		s.writeSteering(protocol.SteeringDropped, protocol.SteeringDroppedFailed,
			"steering dropped — the task ended before it could be delivered")
	}
}

// steeringOnCancel is what a cancel does with the queue. Named rather than a
// bare bool for the reason promptKind is: the value is read at the call site,
// where "false" would say only "do not drop" and not that the queue is being
// handed to the promotion path instead.
type steeringOnCancel int

const (
	// dropQueuedSteering takes the queue with the canceled task. This is what a
	// bare :cancel does — stop means stop — and the only fate a cancel reached
	// without the user's words can have (SIGINT and CancelTask() land here).
	dropQueuedSteering steeringOnCancel = iota

	// keepQueuedSteering leaves the queue for deliverLeftoverSteering, which
	// promotes it to the next prompt (:cancel keep_steering). The user asked
	// for that task by asking for the words.
	keepQueuedSteering
)

// cancelSteering settles the queue's fate when a running task is canceled, and
// records that it did so the canceled task's deferred discard stays a no-op.
//
// The second half is the point, and it holds in both modes. The queue is
// session-wide, so a prompt the user types after ":cancel" but before the task
// goroutine finishes unwinding lands in it too. cancelTask settles at the
// instant of the cancel, which is exactly the boundary the policy draws ("only
// what is queued at this instant"); the deferred discard runs later, in that
// unwinding window, and must therefore not decide again — a drain there would
// take the post-cancel input and, under keepQueuedSteering, the very batch the
// user asked to keep.
//
// Reports whether anything was actually dropped, i.e. whether the user must be
// told. It is false under keepQueuedSteering for a reason that is not "nothing
// happened": the batch's report is deliverLeftoverSteering's to write, as
// "delivered", and two reports for one batch would break the one-terminal-phase
// contract.
func (s *Session) cancelSteering(fate steeringOnCancel) bool {
	s.steeringMu.Lock()
	defer s.steeringMu.Unlock()
	s.steeringDrained = true
	if fate == keepQueuedSteering || len(s.steering) == 0 {
		return false
	}
	s.steering = nil
	return true
}

// deliverLeftoverSteering hands steering that is still queued when a task ends
// to the ordinary prompt path, so it becomes the next task.
//
// What is queued at that point is, by construction, input that arrived after the
// task's last step boundary — and the task either landed (so it is the user's
// next request, typed while the last answer was still streaming), failed (in
// which case processPrompt already drained the queue), or was canceled (drained
// by cancelTask, unless the cancel kept it: :cancel keep_steering, whose whole
// meaning is that this function gets the batch and promotes it). Either way what
// is left here is intent the user stands behind, and it is delivered rather than
// held: this is the case that makes "type the next thing while it is working"
// work.
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
		// The session is leaving and would refuse the task anyway; dropping the
		// parts here is what keeps them from holding the exit open. It is still
		// a drop of accepted input, so it is announced like every other — the
		// steering contract is total: every queued submission ends in exactly
		// one terminal phase.
		s.writeSteering(protocol.SteeringDropped, protocol.SteeringDroppedQuit,
			"steering dropped — the session is quitting")
		return
	}
	// The batch is delivered as the next prompt. Report it before submitPrompt
	// spawns the task goroutine that echoes the words, so the report precedes
	// the echo.
	s.writeSteering(protocol.SteeringDelivered, "", "")
	s.submitPrompt(parts)
}
