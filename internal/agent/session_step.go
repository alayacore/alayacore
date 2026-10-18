package agent

import "github.com/alayacore/alayacore/internal/llm"

// The state one prompt's steps share.
//
// processPrompt publishes a user task to run() step by step: each finished step
// contributes the parts past what was already published, and each step boundary
// may rewrite the history (a compaction) and append user parts to it (steering, or
// the synthetic "Continue" a compaction needs). Three facts have to hold together
// for that to stay consistent, and they used to be three loose variables in the
// middle of a 250-line function:
//
//   - publishedLen counts the parts run() has been told about. A step's delta is
//     the suffix past it, so nothing is published twice.
//   - base is the history the current step was sent on, *before* that step's
//     appended parts. A step that never lands still owes the conversation those
//     parts — they were numbered and echoed when they were spliced — so the
//     failure path adds them to this base. It is neither the running accumulation
//     (empty before the first step finishes, stale once a compaction replaces the
//     history at this very boundary) nor the caller's history.
//   - injected is that appended batch, held until the step lands.
//
// Putting them on one value makes finished the only place the baseline moves and
// the only place the batch is spent, instead of four statements spread across two
// closures that have to agree.
type stepRecord struct {
	publishedLen int
	base         []llm.ContentPart
	injected     []llm.ContentPart
}

// newStepRecord starts a prompt whose history is already published: everything the
// caller passed in is content run() holds, so the first step's delta begins after
// it.
func newStepRecord(history []llm.ContentPart) *stepRecord {
	return &stepRecord{publishedLen: len(history)}
}

// replaced records that a compaction swapped the history for its own replacement.
// The baseline moves to the end of the replacement: the next step's delta is
// measured from there, not from the history that was thrown away.
func (r *stepRecord) replaced(compacted []llm.ContentPart) {
	r.publishedLen = len(compacted)
}

// sentOn records the history this step is about to be sent on. It is taken after
// any compaction, so a boundary that both compacted and appended keeps the
// compacted form as the base its injected parts are salvaged onto.
func (r *stepRecord) sentOn(contents []llm.ContentPart) {
	r.base = contents
}

// spliced records the user parts appended at this boundary and returns the history
// to send. The batch is published to run() by the caller, in the same breath as
// its echo, so a :save or :fork during the step sees what the transcript already
// shows; moving the baseline past it here is what leaves the step's own delta
// carrying nothing but the step's output. Without that move the batch would enter
// Contents twice.
func (r *stepRecord) spliced(contents, parts []llm.ContentPart) []llm.ContentPart {
	next := make([]llm.ContentPart, len(contents), len(contents)+len(parts))
	copy(next, contents)
	next = append(next, parts...)
	r.publishedLen += len(parts)
	r.injected = parts
	return next
}

// finished records that a step landed with contents, returning the parts it added
// past what had been published, and moving the baseline to the whole of contents.
// Landing also spends the injected batch: it was part of what the model was sent,
// so a later failure must not add it a second time.
//
// The suffix is only taken when publishedLen <= len(contents).
// cleanIncompleteToolInputs is documented to strip from the current step's tail
// alone, which keeps that true — and slicing on that faith once turned a broken
// assumption into a panic ("slice bounds out of range") inside the task goroutine,
// so it is checked. When it does not hold nothing is published for this step, and
// run() still receives the complete history from taskResultCh at task end.
func (r *stepRecord) finished(contents []llm.ContentPart) []llm.ContentPart {
	r.injected = nil
	var parts []llm.ContentPart
	if r.publishedLen <= len(contents) {
		parts = contents[r.publishedLen:]
	}
	r.publishedLen = len(contents)
	return parts
}

// salvaged returns the history to publish for a step that never landed: the
// injected batch is added to the history it was appended to. It is nil when
// nothing was injected, which is the ordinary failure — the accumulation then
// stands as the last step left it.
func (r *stepRecord) salvaged() []llm.ContentPart {
	if len(r.injected) == 0 {
		return nil
	}
	merged := make([]llm.ContentPart, 0, len(r.base)+len(r.injected))
	merged = append(merged, r.base...)
	merged = append(merged, r.injected...)
	return merged
}
