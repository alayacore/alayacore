package llm

import "strings"

// streamAssembler turns one step's event stream into that step's content parts.
// It is the only place that does this: providers describe what arrived and where
// a block ended, and the record is built from those descriptions alone. The
// arrangement is deliberate — before it existed, each provider assembled the
// step's parts itself and llm.Agent assembled them a second time for the
// failed-step path, and nothing could keep two implementations of the same fact
// in agreement. There is now one, and it is called on every path a step can end
// by, which is the property its tests are named for.
//
// Content reaches the record through exactly one channel: the delta methods. A
// provider that receives a whole block at once (a non-streaming response, a
// buffered final chunk) calls a delta method with the entire text and then
// closes the block. Boundary events carry no content, so a block's body has one
// source, and the assembler cannot be told something the stream did not carry.
type streamAssembler struct {
	byKey map[string]*assembledBlock
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
	closed bool
}

func newStreamAssembler() *streamAssembler {
	return &streamAssembler{byKey: map[string]*assembledBlock{}}
}

// block returns the block for key, creating it on first sight. Reusing a key
// for a second kind of content is a provider mislabelling its own stream: the
// kind and the history ID are both fixed at the first touch, so they stay
// aligned and the later content is folded into the block the stream opened.
func (a *streamAssembler) block(key string, kind assembledKind) *assembledBlock {
	if b, ok := a.byKey[key]; ok {
		return b
	}
	b := &assembledBlock{key: key, kind: kind}
	a.byKey[key] = b
	a.touched = append(a.touched, b)
	return b
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

// close records that the provider declared this block finished. It is
// idempotent, and closing a key nothing ever opened is ignored: a stream may
// report an end for a block whose content it never sent, and the step must not
// grow a part out of that.
func (a *streamAssembler) close(key string) {
	b, ok := a.byKey[key]
	if !ok || b.closed {
		return
	}
	b.closed = true
	a.closed = append(a.closed, b)
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

// toolCall returns a closed-or-open tool block's identity and assembled
// arguments, so the boundary event can start execution without the provider
// having shipped a second copy of the arguments.
func (a *streamAssembler) toolCall(key string) (id, name, args string) {
	b, ok := a.byKey[key]
	if !ok || b.kind != toolBlockKind {
		return "", "", ""
	}
	return b.id, b.name, b.body.String()
}

// parts renders the step's reasoning and text blocks into content parts.
//
// Order is the order the provider closed them, because layout within the record
// is protocol knowledge: Anthropic closes blocks in the index order the server
// declared, and OpenAI closes its three slots in the order an assistant turn has
// in that protocol — reasoning, content, tool calls. Neither order is the order
// bytes happened to arrive in, and the record must not depend on that. Blocks
// still open when the step ended are appended afterwards in first-sight order:
// they were never declared finished, so nothing may claim where they belong.
//
// Tool blocks are not produced here on purpose. A tool_use may only enter
// history together with its tool_result, which the assembler has no business
// knowing about; those pairs are built by pairToolResults and
// salvageExecutedTools from the calls execution actually started.
//
// Empty blocks are skipped: a slot the provider opened but never filled adds
// nothing to the conversation.
func (a *streamAssembler) parts(idByKey map[string]uint64) []ContentPart {
	out := make([]ContentPart, 0, len(a.touched))
	emit := func(b *assembledBlock) {
		if b.kind == toolBlockKind {
			return
		}
		text := b.body.String()
		if text == "" {
			return
		}
		var part ContentPart
		switch b.kind {
		case reasoningBlockKind:
			part = &ReasoningPart{Text: text}
		case textBlockKind:
			part = &TextPart{Text: text}
		}
		part.SetBlockKey(b.key)
		part.SetRole(RoleAssistant)
		if id, ok := idByKey[b.key]; ok {
			part.SetHistoryID(id)
		}
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
