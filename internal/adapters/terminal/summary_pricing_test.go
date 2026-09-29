package terminal

// A folded text row prices its message once, from counts kept as the content
// grows, instead of walking the message every frame. Keeping a count is the kind of
// thing that is right for the content it was written against and wrong at a
// boundary nobody thought to put in a test, so the count is held against the pass
// it replaced rather than against a restatement of it.
//
// The reference here is the same code with the counts retired (summaryBytes = -1),
// which sends summaryContent down prepareContent and measure — exactly what a
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
			b.summaryBytes = -1 // the reference: measure the message every time

			// Which path each took, so a corpus that quietly retired every count
			// cannot pass by comparing the slow path with itself.
			if a.summaryBytes == len(a.rawContent()) {
				kept++
			} else {
				retired++
			}

			raw := a.rawContent()
			if raw != content {
				t.Fatalf("split %d: the renderer's content is %q, want %q", split, raw, content)
			}

			// The counts themselves, against a measurement of the same content.
			want := prepareContent(raw)
			gotM, gotNewlines := a.summaryContent(raw)
			if wantM := measure(want); gotM.cells != wantM.cells || gotM.s != want {
				t.Errorf("%q split %d: summaryContent measured %d cells of %q, want %d cells of %q",
					content, split, gotM.cells, gotM.s, wantM.cells, want)
			}
			if wantNewlines := strings.Count(want, "\n"); gotNewlines != wantNewlines {
				t.Errorf("%q split %d: summaryContent counted %d line breaks, want %d",
					content, split, gotNewlines, wantNewlines)
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

// TestHeadAndTailMeasuredIsHeadAndTailParts checks that the split hands the right
// two things over: a measurement of the content, and the count of line breaks that
// belongs to that same content. It cannot check the fit rule the two share —
// headAndTailParts delegates to headAndTailMeasured, so a change there moves both
// sides together. TestHeadAndTailFitsWhatEscapedWidthSays is the one that can.
func TestHeadAndTailMeasuredIsHeadAndTailParts(t *testing.T) {
	widths := []int{-1, 0, 1, 2, 3, 4, 5, 8, 13, 40, 79, 80, 120}
	for _, content := range summaryPricingCorpus() {
		prepared := prepareContent(content)
		m := measure(prepared)
		newlines := strings.Count(prepared, "\n")
		for _, w := range widths {
			gotHead, gotTail, gotTrunc := headAndTailMeasured(m, newlines, w)
			wantHead, wantTail, wantTrunc := headAndTailParts(prepared, w)
			if gotHead != wantHead || gotTail != wantTail || gotTrunc != wantTrunc {
				t.Errorf("%q at width %d:\n  measured handed in: (%q, %q, %v)\n  measured inside:    (%q, %q, %v)",
					content, w, gotHead, gotTail, gotTrunc, wantHead, wantTail, wantTrunc)
			}
		}
	}
}

// TestHeadAndTailFitsWhatEscapedWidthSays pins the fit rule to the function that
// states it independently. escapedWidth is what a summary's width becomes once
// escapeBreaks has run — every line break 0 cells becoming a two-cell marker — and
// tailParts still asks it directly, so it is a second statement of the same
// question rather than a restatement of the branch under test.
//
// "Fits" is observable from outside as "not truncated": the branch that finds the
// content inside the budget returns the whole of it escaped and says nothing was
// cut, and every other return says something was.
func TestHeadAndTailFitsWhatEscapedWidthSays(t *testing.T) {
	for _, content := range summaryPricingCorpus() {
		prepared := prepareContent(content)
		m := measure(prepared)
		newlines := strings.Count(prepared, "\n")
		for _, w := range []int{1, 2, 3, 4, 5, 8, 13, 40, 79, 80, 120} {
			head, tail, truncated := headAndTailMeasured(m, newlines, w)
			if fits := escapedWidth(prepared, m.cells) <= w; fits == truncated {
				t.Errorf("%q at width %d: escapedWidth says the content %s, and the summary says it %s",
					content, w,
					map[bool]string{true: "fits", false: "does not fit"}[fits],
					map[bool]string{true: "was truncated", false: "was not truncated"}[truncated])
				continue
			}
			if !truncated {
				if want := escapeBreaks(prepared); head != want || tail != "" {
					t.Errorf("%q at width %d: the content fits, so the summary is all of it escaped:\n got  (%q, %q)\n want (%q, \"\")",
						content, w, head, tail, want)
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
		beforeCells, beforeNewlines := r.summaryCells, r.summaryNewlines
		r.mergeParts()
		if r.summaryCells != beforeCells || r.summaryNewlines != beforeNewlines {
			t.Errorf("split %d: a fold moved the counts from (%d cells, %d breaks) to (%d, %d)",
				split, beforeCells, beforeNewlines, r.summaryCells, r.summaryNewlines)
		}
		ref := &textRenderer{tag: tlv.TagAssistantR, content: content}
		ref.summaryBytes = -1
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
		if r.summaryBytes < 0 {
			t.Fatalf("%q: plain ASCII retired the counts", retiring)
		}
		r.AppendFromTLV("", retiring)
		if r.summaryBytes >= 0 {
			t.Errorf("%q did not retire the counts", retiring)
		}
		before := r.summaryBytes
		r.AppendFromTLV("", "and plain ascii after, which must not revive them")
		if r.summaryBytes != before {
			t.Errorf("%q: a plain delta after it moved summaryBytes from %d to %d", retiring, before, r.summaryBytes)
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
	if r.summaryBytes != 0 {
		t.Fatalf("a fresh renderer accounts for %d bytes, want 0", r.summaryBytes)
	}
	ref := &textRenderer{tag: tlv.TagAssistantR, content: content}
	ref.summaryBytes = -1
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
	// What matters is the row it draws, not the field: noteSummaryDelta will add
	// the delta's bytes to a count that started at zero, and the length check in
	// summaryContent is what has to notice that the total no longer covers the
	// content.
	r.AppendFromTLV("", " more")
	ref.AppendFromTLV("", " more")
	ref.summaryBytes = -1
	for _, width := range []int{8, 40, 80} {
		got, _ := r.BuildCollapsed(width, DefaultStyles())
		want, _ := ref.BuildCollapsed(width, DefaultStyles())
		if got != want {
			t.Errorf("width %d: after appending to content it never counted, got %q, want %q", width, got, want)
		}
	}
}
