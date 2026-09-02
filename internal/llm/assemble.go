package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// streamAssembler turns one step's event stream into that step's content parts.
// It is the only place that does this: providers describe what arrived and where
// a block ended, and the record is built from those descriptions alone. The
// arrangement is deliberate — before it existed, three pieces of code assembled
// the same step (getContents() in each provider, stepTextBlocks plus
// salvageExecutedTools in llm.Agent), and nothing could keep them in agreement.
// There is now one, and it is called on every path a step can end by, which is
// the property its tests are named for.
//
// Two rules make that possible:
//
//   - Content reaches the record through the delta methods only. A provider
//     that receives a whole block at once (a buffered final chunk) calls a delta
//     method with the entire text and then closes the block. Boundary events
//     carry no content, so a block's body has one source and the record cannot
//     be told something the stream did not carry.
//   - The assembler owns each block's history ID, minting it when the block
//     first appears. Numbering and content are then the same fact about the same
//     object: the window an adapter built and the part that gets persisted are
//     two views of one block rather than two records that must be reconciled.
type streamAssembler struct {
	byKey map[string]*assembledBlock
	// nextID hands out history IDs, or is nil when nothing is numbering this
	// session's content (a caller with no IDGen).
	nextID func() uint64
	// touched records first-sight order; closed records the order blocks were
	// declared finished. Both are needed: closed is the record's layout, while
	// touched is the only order that means anything for a block the stream cut
	// off before it closed.
	touched []*assembledBlock
	closed  []*assembledBlock
}

type assembledKind uint8

const (
	textBlockKind assembledKind = iota
	reasoningBlockKind
	toolBlockKind
)

// assembledBlock is one content block of the step: its identity, what kind of
// content it carries, and the body as it arrived.
type assembledBlock struct {
	key    string
	kind   assembledKind
	id     string // tool call ID; empty for text and reasoning
	name   string // tool name; empty for text and reasoning
	body   strings.Builder
	histID uint64
	closed bool
	// part is the tool call this block became, once its arguments were complete
	// and execution started. The record emits this same object, so the input a
	// tool ran with and the input history stores cannot differ; nil for
	// non-tool blocks and for a tool the stream never finished.
	part *ToolInputPart
}

func newStreamAssembler(nextID func() uint64) *streamAssembler {
	return &streamAssembler{byKey: map[string]*assembledBlock{}, nextID: nextID}
}

// block returns the block for key, creating it on first sight — and minting the
// history ID there, so a block's number is fixed by when it appeared, whatever
// the caller's display wiring does with it. Reusing a key for a second kind of
// content is a provider mislabelling its own stream: the kind was decided at the
// first touch, so the later content is folded into the block the stream opened.
func (a *streamAssembler) block(key string, kind assembledKind) *assembledBlock {
	if b, ok := a.byKey[key]; ok {
		return b
	}
	b := &assembledBlock{key: key, kind: kind}
	if a.nextID != nil {
		b.histID = a.nextID()
	}
	a.byKey[key] = b
	a.touched = append(a.touched, b)
	return b
}

// historyID reports the ID minted for a block, or 0 if it never appeared.
func (a *streamAssembler) historyID(key string) uint64 {
	if b, ok := a.byKey[key]; ok {
		return b.histID
	}
	return 0
}

func (a *streamAssembler) text(key, delta string) {
	a.block(key, textBlockKind).body.WriteString(delta)
}

func (a *streamAssembler) reasoning(key, delta string) {
	a.block(key, reasoningBlockKind).body.WriteString(delta)
}

// toolStart names a tool call. OpenAI sends the ID and name once and then
// argument fragments keyed only by index, so identity arrives separately from
// content by protocol rather than by choice.
func (a *streamAssembler) toolStart(key, id, name string) {
	b := a.block(key, toolBlockKind)
	if id != "" {
		b.id = id
	}
	if name != "" {
		b.name = name
	}
}

func (a *streamAssembler) toolArgs(key, delta string) {
	a.block(key, toolBlockKind).body.WriteString(delta)
}

// close records that the provider declared this block finished and reports
// whether such a block exists. It is idempotent, and a boundary naming a block
// the stream never opened is refused: that block has no content, no history ID,
// and must not reach either the record or the display as an empty window.
func (a *streamAssembler) close(key string) bool {
	b, ok := a.byKey[key]
	if !ok {
		return false
	}
	if b.closed {
		return true
	}
	b.closed = true
	a.closed = append(a.closed, b)
	return true
}

// body returns what has arrived for a block so far, which is what a boundary
// handler needs: the text to hand to an adapter, the arguments to execute.
func (a *streamAssembler) body(key string) string {
	b, ok := a.byKey[key]
	if !ok {
		return ""
	}
	return b.body.String()
}

// toolCall returns a tool block's identity and assembled arguments, so the
// boundary handler can repair them and start execution without the provider
// having shipped a second copy.
func (a *streamAssembler) toolCall(key string) (id, name, args string) {
	b, ok := a.byKey[key]
	if !ok || b.kind != toolBlockKind {
		return "", "", ""
	}
	return b.id, b.name, b.body.String()
}

// beginToolCall freezes a tool block into the part its execution starts with.
// The record emits this same object, so what a tool ran with and what history
// stores are one value rather than two derived from the same fragments. Calling
// it twice for a key keeps the first part: a provider repeating a boundary must
// not run a tool twice.
func (a *streamAssembler) beginToolCall(key string, input json.RawMessage) *ToolInputPart {
	b, ok := a.byKey[key]
	if !ok || b.kind != toolBlockKind {
		return nil
	}
	if b.part != nil {
		return b.part
	}
	b.part = &ToolInputPart{
		ID:    b.id,
		Name:  b.name,
		Input: input,
		ContentPartMeta: ContentPartMeta{
			HistoryID: b.histID,
			Role:      RoleAssistant,
		},
	}
	return b.part
}

// collectResults finishes what attachToolResults needs on a path that never
// waited for the tools: whatever was already collected, plus everything still
// queued. The channel is buffered and every sender has already returned by the
// time this runs (the caller waits on the tool WaitGroup first), so the drain is
// non-blocking and complete.
func collectResults(collected []ContentPart, queue <-chan ContentPart) []ContentPart {
	out := collected
	for {
		select {
		case r := <-queue:
			out = append(out, r)
		default:
			return out
		}
	}
}

// attachToolResults pairs each recorded tool call with the result that answers
// it, appending the results in call order after the step's other content.
//
// This is the second half of the record, and it is shared by every path a step
// can end by — which is the point. It used to be two functions: one for the path
// that finished and one for salvage, agreeing on the pairing rules only because
// both were written from the same idea.
//
// The two paths still differ in what a missing result means, and that difference
// is the whole of the dropUnanswered flag. A step the provider finished must have
// an answer for every call it recorded, so a missing one is a defect worth
// failing on. A step that was cut mid-flight legitimately has calls that never
// completed, and the only safe thing to do with them is leave them out: a
// tool_use without its tool_result is a conversation the next request cannot
// build and a session file that refuses to load.
//
// A call ID appearing more than once is ambiguous either way — two calls, one
// result each, cannot be told apart — so the strict path errors on it and the
// forgiving path drops both. Results naming no recorded call are discarded: they
// answer something the record does not contain, and attaching them would invent a
// call.
func attachToolResults(record, results []ContentPart, dropUnanswered bool) ([]ContentPart, error) {
	occurrences := make(map[string]int)
	var callIDs []string
	for _, p := range record {
		if tc, ok := p.(*ToolInputPart); ok {
			occurrences[tc.ID]++
			callIDs = append(callIDs, tc.ID)
		}
	}
	answer := make(map[string]ContentPart, len(callIDs))
	for _, r := range results {
		if tr, ok := r.(*ToolOutputPart); ok && tr.ID != "" {
			answer[tr.ID] = r
		}
	}

	kept := make([]ContentPart, 0, len(record))
	answered := make([]ContentPart, 0, len(callIDs))
	for _, p := range record {
		tc, isCall := p.(*ToolInputPart)
		if !isCall {
			kept = append(kept, p)
			continue
		}
		r, ok := answer[tc.ID]
		if occurrences[tc.ID] > 1 {
			if !dropUnanswered {
				return nil, fmt.Errorf("tool call ID %q appears more than once; cannot pair its result", tc.ID)
			}
			continue // ambiguous: drop the call rather than guess a pairing
		}
		if !ok {
			if !dropUnanswered {
				return nil, fmt.Errorf("tool result missing for tool call %q", tc.ID)
			}
			continue
		}
		kept = append(kept, tc)
		answered = append(answered, r)
	}
	return append(kept, answered...), nil
}

// parts renders the step's blocks into content parts.
//
// Order is the order the provider closed them, because layout within the record
// is protocol knowledge: Anthropic closes blocks in the index order the server
// declared, and OpenAI closes its three slots in the order an assistant turn has
// in that protocol — reasoning, content, tool calls. Neither order is the order
// bytes happened to arrive in, and the record must not depend on that. Blocks
// still open when the step ended are appended afterwards in first-sight order:
// they were never declared finished, so nothing may claim where they belong.
//
// A tool block contributes only if it got a part — its arguments were complete
// and execution started. A call the stream never finished is not recorded,
// because an assistant tool_use must never appear without the tool_result that
// answers it. Empty text and reasoning blocks are skipped likewise: a slot the
// provider opened but never filled adds nothing to the conversation.
func (a *streamAssembler) parts() []ContentPart {
	out := make([]ContentPart, 0, len(a.touched))
	emit := func(b *assembledBlock) {
		var part ContentPart
		switch b.kind {
		case toolBlockKind:
			if b.part == nil {
				return
			}
			out = append(out, b.part)
			return
		case reasoningBlockKind:
			if b.body.Len() == 0 {
				return
			}
			part = &ReasoningPart{Text: b.body.String()}
		case textBlockKind:
			if b.body.Len() == 0 {
				return
			}
			part = &TextPart{Text: b.body.String()}
		}
		part.SetRole(RoleAssistant)
		part.SetHistoryID(b.histID)
		out = append(out, part)
	}
	for _, b := range a.closed {
		emit(b)
	}
	for _, b := range a.touched {
		if !b.closed {
			emit(b)
		}
	}
	return out
}
