package terminal

// A folded text row prices its message once, from counts kept as the content
// grows, instead of walking the message every frame. Keeping a count is the kind of
// thing that is right for the content it was written against and wrong at a
// boundary nobody thought to put in a test, so the count is held against the pass
// it replaced rather than against a restatement of it.
//
// The reference here is the same code with its counts retired, which sends
// summaryContent down prepareContent and measure — exactly what a
// folded row did before. Two renderers are built from the same bytes, one keeping
// the counts and one not, and their folded rows are required to be identical.
//
// The bytes arrive in two deltas split at every offset, because the claim being
// tested is that a per-delta sum equals a whole-string measure. On the byte-wise
// route one byte is one cluster, so it must — and a combining mark, a variation
// selector or a half of a UTF-8 sequence arriving in the second delta is what would
// break that if the route were ever widened without the sum being reconsidered.
// Those are in the corpus, and each of them retires the counts, which is the other
// half of the claim: the fast path is taken exactly when it is sound.

import (
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/tlv"
)

// summaryPricingCorpus is summaryCorpus — the shapes that reach the summary paths,
// which summary_cut_order_test.go already enumerates, line breaks and CRLF and tabs
// and the clusters width libraries disagree about among them — plus the bytes that
// retire the counts and are not otherwise represented there: an escape sequence, a
// DEL, a variation selector on its own, and a content big enough that the difference
// between counting and measuring is the point of the change.
func summaryPricingCorpus() []string {
	return append(summaryCorpus(),
		"\x1b[31mred\x1b[0m and plain",
		"\x1b]0;title\x07body",
		"del\x7fchar",
		"\uFE0F on its own",
		strings.Repeat("The quick brown fox jumps over the lazy dog. ", 40),
		strings.Repeat("x", 5000)+"\n"+strings.Repeat("y", 5000),
	)
}

// newSummaryRenderer builds a text renderer whose content arrives as two deltas,
// split at offset split.
func newSummaryRenderer(content string, split int) *textRenderer {
	r := &textRenderer{tag: tlv.TagAssistantR}
	r.AppendFromTLV("", content[:split])
	r.AppendFromTLV("", content[split:])
	return r
}

// TestFoldedSummaryIsPricedTheSameWhicheverWay is the differential: same bytes,
// same widths, same style registers, one renderer keeping the counts and one
// measuring the message, and the folded row must come out identical.
func TestFoldedSummaryIsPricedTheSameWhicheverWay(t *testing.T) {
	widths := []int{0, 1, 2, 3, 4, 5, 8, 13, 40, 79, 80, 120}
	registers := []*Styles{DefaultStyles(), DefaultStyles().Dimmed(), nil}
	kept, retired := 0, 0

	for _, content := range summaryPricingCorpus() {
		// Every split for a short content, the interesting ones for a long one:
		// the sweep is about a boundary falling inside a cluster or between two
		// deltas, and a 10 KB content has no boundary that a 40-byte one lacks.
		var splits []int
		if len(content) <= 64 {
			for i := 0; i <= len(content); i++ {
				splits = append(splits, i)
			}
		} else {
			splits = []int{0, 1, 2, 7, len(content) / 3, len(content) / 2, len(content) - 2, len(content) - 1, len(content)}
		}
		for _, split := range splits {
			a := newSummaryRenderer(content, split)
			b := newSummaryRenderer(content, split)
			b.summary.retire() // the reference: measure the message every time

			raw := a.rawContent()
			if raw != content {
				t.Fatalf("split %d: the renderer's content is %q, want %q", split, raw, content)
			}

			// Which path each took, so a corpus that quietly retired every count
			// cannot pass by comparing the slow path with itself. An empty content
			// is left out of the tally and does not count as a kept one: its counts
			// are zero whatever note does with them, so a note that forgot the byte
			// total would still look kept there, the guard would be vacuous, and the
			// fast path would be silently off for every real message.
			if raw != "" && a.summary.covers(len(raw)) {
				kept++
			} else {
				retired++
			}

			// The counts themselves, against a measurement of the same content.
			want := prepareContent(raw)
			got := a.summaryContent(raw)
			if wantM := measure(want); got.m.cells != wantM.cells || got.m.s != want {
				t.Errorf("%q split %d: summaryContent measured %d cells of %q, want %d cells of %q",
					content, split, got.m.cells, got.m.s, wantM.cells, want)
			}
			// The escaped half is checked against what escaping actually draws,
			// not against escapedWidth or the counts' own arithmetic: it is the
			// number a fit check spends, and it is the half the cached path
			// assembles by hand.
			if wantEscaped := cellWidth(escapeBreaks(want)); got.escaped != wantEscaped {
				t.Errorf("%q split %d: summaryContent priced the escaped row at %d cells, want %d",
					content, split, got.escaped, wantEscaped)
			}

			for _, width := range widths {
				for si, styles := range registers {
					gotLine, gotN := a.BuildCollapsed(width, styles)
					wantLine, wantN := b.BuildCollapsed(width, styles)
					if gotLine != wantLine || gotN != wantN {
						t.Fatalf("%q split %d width %d register %d:\n  counted:  %q (marker at %d)\n  measured: %q (marker at %d)",
							content, split, width, si, gotLine, gotN, wantLine, wantN)
					}
				}
			}
		}
	}
	if kept == 0 {
		t.Fatal("no case kept the counts, so the fast path was never compared with the pass it replaced")
	}
	if retired == 0 {
		t.Fatal("no case retired the counts, so the fallback was never exercised")
	}
	t.Logf("%d cases kept the counts, %d retired them", kept, retired)
}

// TestSummaryFitsWhatEscapeBreaksDraws pins the fit rule to the row a summary
// actually draws. escapeBreaks is what the content becomes — every line break,
// 0 cells, turning into a two-cell marker — so measuring that string states
// "does it fit" while sharing no code with the branch under test: not
// escapedWidth, which priceSummary spends, and not the counts summaryContent keeps.
//
// "Fits" is observable from outside as "not truncated": the branch that finds the
// content inside the budget returns the whole of it escaped and says nothing was
// cut, and every other return says something was.
//
// This catches a fit check that forgets the line breaks — reading the budget
// against the raw cell count says a message with enough newlines fits when it
// does not — and an escapedWidth that prices a break at anything other than the
// two cells it draws.
func TestSummaryFitsWhatEscapeBreaksDraws(t *testing.T) {
	for _, content := range summaryPricingCorpus() {
		prepared := prepareContent(content)
		priced := priceSummary(prepared)
		drawn := escapeBreaks(prepared)
		drawnCells := cellWidth(drawn)
		for _, w := range []int{1, 2, 3, 4, 5, 8, 13, 40, 79, 80, 120} {
			head, tail, truncated := headAndTailParts(priced, w)
			if fits := drawnCells <= w; fits == truncated {
				t.Errorf("%q at width %d: the escaped content is %d cells, so it %s, and the summary says it %s",
					content, w, drawnCells,
					map[bool]string{true: "fits", false: "does not fit"}[fits],
					map[bool]string{true: "was truncated", false: "was not truncated"}[truncated])
				continue
			}
			if !truncated {
				if head != drawn || tail != "" {
					t.Errorf("%q at width %d: the content fits, so the summary is all of it escaped:\n got  (%q, %q)\n want (%q, \"\")",
						content, w, head, tail, drawn)
				}
			}
		}
	}
}

// TestSummaryCountsSurviveAFold is the claim mergeParts makes silently: folding the
// pending deltas into content changes where the bytes are held and not what they
// are, so a count kept per delta is still a count of the content. Every long
// streaming message passes through a fold — BuildInner does one on a full render and
// another at maxContentParts on an incremental one. Splitting the content one byte
// at a time is also the sharpest test of the per-delta sum, since it puts a boundary
// between every two bytes.
func TestSummaryCountsSurviveAFold(t *testing.T) {
	content := strings.Repeat("reasoning about the task, and planning the next steps\n", 30)
	for _, split := range []int{1, 2, 7, maxContentParts, maxContentParts + 1} {
		r := &textRenderer{tag: tlv.TagAssistantR}
		for i := 0; i < len(content); i += split {
			r.AppendFromTLV("", content[i:min(i+split, len(content))])
		}
		before := r.summary
		r.mergeParts()
		if r.summary != before {
			t.Errorf("split %d: a fold moved the counts from %+v to %+v", split, before, r.summary)
		}
		ref := &textRenderer{tag: tlv.TagAssistantR, content: content}
		ref.summary.retire()
		for _, width := range []int{8, 40, 80} {
			got, _ := r.BuildCollapsed(width, DefaultStyles())
			want, _ := ref.BuildCollapsed(width, DefaultStyles())
			if got != want {
				t.Errorf("split %d width %d: after a fold the row is %q, want %q", split, width, got, want)
			}
		}
	}
}

// TestSummaryCountsRetireForGood is the monotonicity the design rests on: content
// only grows, so a byte that retired the counts cannot be un-retired by what
// arrives after it. A renderer that recovered would price a message it had already
// admitted it could not price byte by byte.
func TestSummaryCountsRetireForGood(t *testing.T) {
	for _, retiring := range []string{"\t", "\r", "\x1b[0m", "你", "\u0301", "\x7f", "\uFE0F"} {
		r := &textRenderer{tag: tlv.TagAssistantR}
		r.AppendFromTLV("", "plain ascii before")
		if r.summary.retired() {
			t.Fatalf("%q: plain ASCII retired the counts", retiring)
		}
		r.AppendFromTLV("", retiring)
		if !r.summary.retired() {
			t.Errorf("%q did not retire the counts", retiring)
		}
		before := r.summary
		r.AppendFromTLV("", "and plain ascii after, which must not revive them")
		if r.summary != before {
			t.Errorf("%q: a plain delta after retiring moved the counts from %+v to %+v", retiring, before, r.summary)
		}
	}
}

// TestSummaryCountsAreNotTrustedForContentTheyNeverSaw is the guard on the zero
// value: a renderer built with its content already in it, which is how a fixture
// and any future restore path would make one, has counted none of it and must be
// measured rather than priced at zero cells.
func TestSummaryCountsAreNotTrustedForContentTheyNeverSaw(t *testing.T) {
	content := "content that was set rather than appended, and is long enough to truncate"
	r := &textRenderer{tag: tlv.TagAssistantR, content: content}
	if r.summary.bytes != 0 {
		t.Fatalf("a fresh renderer accounts for %d bytes, want 0", r.summary.bytes)
	}
	ref := &textRenderer{tag: tlv.TagAssistantR, content: content}
	ref.summary.retire()
	for _, width := range []int{8, 40, 80} {
		got, _ := r.BuildCollapsed(width, DefaultStyles())
		want, _ := ref.BuildCollapsed(width, DefaultStyles())
		if got != want {
			t.Errorf("width %d: content it never counted gave %q, want %q", width, got, want)
		}
		if got == "" {
			t.Errorf("width %d: the summary is empty, so the uncounted content was priced at zero cells", width)
		}
	}
	// And appending to it afterwards must not revive a count that missed the head.
	// What matters is the row it draws, not the field: summaryCounts.note will add
	// the delta's bytes to a count that started at zero, and covers is what has to
	// notice that the total no longer accounts for the content.
	r.AppendFromTLV("", " more")
	ref.AppendFromTLV("", " more")
	ref.summary.retire()
	for _, width := range []int{8, 40, 80} {
		got, _ := r.BuildCollapsed(width, DefaultStyles())
		want, _ := ref.BuildCollapsed(width, DefaultStyles())
		if got != want {
			t.Errorf("width %d: after appending to content it never counted, got %q, want %q", width, got, want)
		}
	}
}

// TestSummaryCountsCoverExactlyTheBytesTheyCounted pins covers to equality rather
// than to "at least". bytes can only equal or trail the content length today —
// content grows by exactly the deltas note sees, and mergeParts replaces it with a
// string of the same bytes — so a >= would be unobservable and this is a test of a
// state the public API cannot reach. It is here on purpose: a restore path, or a
// compaction that replaces a window's content with a shorter one, would reach it,
// and pricing a short message with a long message's counts is precisely the
// failure summaryPrice exists to make inexpressible.
func TestSummaryCountsCoverExactlyTheBytesTheyCounted(t *testing.T) {
	c := summaryCounts{cells: 5, newlines: 1, bytes: 5}
	for _, n := range []int{0, 1, 4, 5, 6, 100} {
		if got, want := c.covers(n), n == 5; got != want {
			t.Errorf("counts over 5 bytes: covers(%d) = %v, want %v", n, got, want)
		}
	}

	// The zero value accounts for an empty content and for nothing else, which is
	// what makes a renderer built with its content already in it measure instead of
	// pricing the message at zero cells.
	var zero summaryCounts
	if !zero.covers(0) {
		t.Error("the zero value does not cover an empty content, so a window that never streamed would measure a row it can price")
	}
	if zero.covers(1) {
		t.Error("the zero value covers a content it never saw")
	}

	// And retired counts cover nothing at all, including the length they had.
	c.retire()
	for _, n := range []int{0, 5, 100} {
		if c.covers(n) {
			t.Errorf("retired counts cover %d bytes", n)
		}
	}
}
