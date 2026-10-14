package protocol

// Content planes — where a content item may appear.
//
// The content vocabulary is one set of kinds (TypeText, TypeImage, …). Which of
// them a plane admits is a partition of that set, and the partition is by
// direction, not by kind: user content and tool-result content both travel into
// the model, assistant content comes out of it.
//
// planeKinds below is the single source of truth for that partition. Every
// reader and writer of a content item consults it instead of restating the sets,
// so the two can never disagree about what a frame may carry — and a kind added
// here is a kind every plane's consumer is told about.
type Plane string

const (
	PlaneUser       Plane = "user"        // the body of a UX frame
	PlaneAssistant  Plane = "assistant"   // the body of an AX frame
	PlaneToolResult Plane = "tool_result" // the elements of a UF frame's output
)

// planeKinds lists, per plane, the content kinds the protocol admits. Order is
// declaration order, for callers that range over it.
var planeKinds = map[Plane][]string{
	// User content and tool-result content are the same set — both are model
	// input. Reasoning is the one kind that belongs to the assistant side alone;
	// admitting it on either of these would let a part's role depend on which
	// representation a reader saw.
	PlaneUser:       {TypeText, TypeImage, TypeVideo, TypeAudio, TypeDocument},
	PlaneToolResult: {TypeText, TypeImage, TypeVideo, TypeAudio, TypeDocument},
	// Assistant content is what the assistant produces: text and reasoning. Tool
	// calls and their results have their own frames (AF/UF), not content items.
	PlaneAssistant: {TypeText, TypeReasoning},
}

// Unknown kinds — the contract, and why it is not symmetric.
//
// A kind that appears in none of the sets above is admitted nowhere. The core
// rejects it rather than carrying a content item it has no part type for: the
// domain's ContentPart is a sealed set (llm/types.go), so there is deliberately
// no UnknownPart to hold one. On the live input path this surfaces as an error
// message to the adapter; in a session file it fails the load — a file is
// already gated on message_version, so an unknown kind there is corruption or a
// same-version bug, never a file from a newer build.
//
// An adapter, which only draws what it is handed, degrades instead: it skips a
// kind it cannot render, or shows a label, so one unrecognized kind never breaks
// the stream. The asymmetry is the point — fail closed where a value becomes
// history, degrade where it is only displayed.

// KindAllowed reports whether the given content kind may appear on the plane.
// An unknown plane admits nothing.
func KindAllowed(plane Plane, kind string) bool {
	for _, k := range planeKinds[plane] {
		if k == kind {
			return true
		}
	}
	return false
}

// PlaneKinds returns the kinds a plane admits, in declaration order. The slice
// is a copy: callers cannot mutate the table.
func PlaneKinds(plane Plane) []string {
	kinds := planeKinds[plane]
	out := make([]string, len(kinds))
	copy(out, kinds)
	return out
}
