package terminal

import (
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/tlv"
)

// Streaming keeps every delta in its own string (textRenderer.contentParts) so
// that one delta costs O(delta) rather than O(content). Folding them into
// r.content is the opposite: a full copy of everything accumulated so far.
// BuildInner's fast path therefore folds only once the pending list has grown
// enough to bound (maxContentParts); it used to fold on every frame, copying
// the whole message to produce a string that frame never read.
//
// Folding changes the representation and nothing else, so these hold the three
// things that must survive it: the same text renders the same rows whatever
// its fold history, the pending list stays bounded however long the stream
// runs, and the fast path really does leave the deltas pending.

// newStreamingTextRenderer is a plain streaming text window's renderer (AT) —
// the tag whose deltas AppendFromTLV keeps incrementally wrapped.
func newStreamingTextRenderer() *textRenderer {
	return &textRenderer{tag: tlv.TagAssistantT}
}

// TestFoldHistoryIsUnobservable builds one message two ways: as a single delta
// (folded once, by the full path) and as many small ones rendered after each
// (folded several times along the way). Both then go through the full path via
// a width change, because that is the only path that reads r.content — a fold
// that lost, duplicated or reordered a delta would show up as different rows.
func TestFoldHistoryIsUnobservable(t *testing.T) {
	const (
		firstWidth  = 40
		secondWidth = 37 // a different width forces the full re-wrap
		piece       = "abc def ghi "
		repeats     = 3*maxContentParts + 5
	)
	whole := strings.Repeat(piece, repeats)

	one := newStreamingTextRenderer()
	one.AppendFromTLV(one.Tag(), whole)
	one.BuildInner(firstWidth, false, nil)

	many := newStreamingTextRenderer()
	folds := 0
	for i := 0; i < repeats; i++ {
		many.AppendFromTLV(many.Tag(), piece)
		many.BuildInner(firstWidth, false, nil) // the streaming frame
		if len(many.contentParts) == 0 {
			folds++
		}
	}
	if folds < 3 {
		t.Fatalf("the stream folded %d time(s); several folds are what makes this a test", folds)
	}

	oneLines, _ := one.BuildInner(secondWidth, false, nil)
	manyLines, _ := many.BuildInner(secondWidth, false, nil)

	if got, want := many.rawContent(), one.rawContent(); got != want {
		t.Errorf("content differs after %d folds:\n got  %d bytes\n want %d bytes\n got  %q\n want %q",
			folds, len(got), len(want), got, want)
	}
	if !sameVisualLines(manyLines, oneLines) {
		t.Errorf("rows differ after %d folds:\n got  %q\n want %q",
			folds, joinVisualLines(manyLines), joinVisualLines(oneLines))
	}
}

// TestPendingDeltasStayBounded is the invariant maxContentParts exists for:
// however long the stream runs, the pending list never grows past it, and no
// content is lost on the way.
func TestPendingDeltasStayBounded(t *testing.T) {
	const (
		width  = 40
		deltas = 20 * maxContentParts
	)
	r := newStreamingTextRenderer()
	r.AppendFromTLV(r.Tag(), "seed\n")
	r.BuildInner(width, false, nil)

	for i := 1; i <= deltas; i++ {
		r.AppendFromTLV(r.Tag(), "delta ")
		r.BuildInner(width, false, nil) // the streaming frame
		if len(r.contentParts) > maxContentParts {
			t.Fatalf("after %d deltas the pending list holds %d entries; maxContentParts is %d",
				i, len(r.contentParts), maxContentParts)
		}
	}

	if got, want := r.rawContent(), "seed\n"+strings.Repeat("delta ", deltas); got != want {
		t.Errorf("the stream lost content: got %d bytes, want %d", len(got), len(want))
	}
}

// TestFastPathLeavesDeltasPending is the optimization itself: below the
// threshold a frame folds nothing, and the full path folds whatever is there.
func TestFastPathLeavesDeltasPending(t *testing.T) {
	const width = 40
	r := newStreamingTextRenderer()
	r.AppendFromTLV(r.Tag(), "seed line\n")
	r.BuildInner(width, false, nil) // full path: folds, and arms the fast path
	if len(r.contentParts) != 0 {
		t.Fatalf("the full path left %d deltas pending", len(r.contentParts))
	}

	r.AppendFromTLV(r.Tag(), "more")
	r.BuildInner(width, false, nil)

	// One pending delta is below maxContentParts, so the fast path leaves it —
	// and its still being there proves the fast path ran, since the full path
	// folds unconditionally.
	if len(r.contentParts) != 1 {
		t.Errorf("the fast path folded a single pending delta; it must wait for maxContentParts")
	}
	if got, want := r.rawContent(), "seed line\nmore"; got != want {
		t.Errorf("rawContent = %q, want %q", got, want)
	}

	// A width change takes the full path, which folds whatever is pending.
	r.BuildInner(width+1, false, nil)
	if len(r.contentParts) != 0 {
		t.Errorf("the full path left %d deltas pending", len(r.contentParts))
	}
	if got, want := r.content, "seed line\nmore"; got != want {
		t.Errorf("r.content = %q after the fold, want %q", got, want)
	}
}
