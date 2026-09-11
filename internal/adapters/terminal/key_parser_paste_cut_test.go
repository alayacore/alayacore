package terminal

// A bracketed paste whose END marker is cut by a read boundary.
//
// A read stops where the platform stops it — inputReadSize bytes on Unix,
// consoleEventsPerRead events on Windows — so a paste longer than that is cut
// wherever the boundary happens to fall. program_input_cut_test.go pins the
// general rule (a sequence cut in half is completed by the next read, never
// typed as its own parameters). A paste is where that rule has to be carried
// into: paste content is read without looking for sequence structure in it, so a
// boundary inside `ESC [ 201 ~` leaves a head that is indistinguishable from
// content. Written into the content, that head is a paste that never ends — and a
// parser left in paste mode takes every keystroke that follows as pasted text,
// which is not a lost paste but a keyboard that has stopped working.

import (
	"strings"
	"testing"
)

// feedReads hands the stream to the parser as reads cut at the given byte
// offsets and returns the messages produced, along with whether the parser was
// left inside a paste. That last is always the failure: the reads ran out while
// the parser was still waiting for a marker.
func feedReads(tb *testing.T, stream string, cuts ...int) (msgs []Msg, inPaste bool) {
	tb.Helper()
	p := &InputParser{}
	prev := 0
	for _, cut := range append(cuts, len(stream)) {
		if cut <= prev || cut > len(stream) {
			tb.Fatalf("cut at %d is not after %d and inside the %d-byte stream", cut, prev, len(stream))
		}
		msgs = append(msgs, p.Parse([]byte(stream[prev:cut]))...)
		prev = cut
	}
	return msgs, p.inPaste
}

// describeMsgs renders messages for a failure line without spilling a whole paste.
func describeMsgs(msgs []Msg) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		switch v := m.(type) {
		case PasteMsg:
			out = append(out, "paste("+v.Content+")")
		case KeyPressMsg:
			out = append(out, "key:"+Key(v).String())
		default:
			out = append(out, describeMsg(m))
		}
	}
	return out
}

// onePaste asserts the messages are one paste of want followed by exactly the
// named keys, and that the parser left paste mode behind.
func onePaste(tb *testing.T, msgs []Msg, inPaste bool, want string, keys string) {
	tb.Helper()
	if inPaste {
		tb.Fatalf("parser left inside the paste: %v", describeMsgs(msgs))
	}
	if len(msgs) != 1+len(keys) {
		tb.Fatalf("got %v, want one paste and %d key(s)", describeMsgs(msgs), len(keys))
	}
	paste, ok := msgs[0].(PasteMsg)
	if !ok {
		tb.Fatalf("msg[0] = %T, want PasteMsg", msgs[0])
	}
	if paste.Content != want {
		tb.Errorf("paste content = %q, want %q", paste.Content, want)
	}
	for i, r := range keys {
		key, ok := msgs[i+1].(KeyPressMsg)
		if !ok {
			tb.Fatalf("msg[%d] = %T, want KeyPressMsg", i+1, msgs[i+1])
		}
		if got := Key(key).String(); got != string(r) {
			tb.Errorf("msg[%d] = %q, want %q", i+1, got, string(r))
		}
	}
}

// TestPasteEndMarkerSurvivesEveryCut is the sweep: the stream is cut at each of
// its byte offsets, so every position at which the end marker can be split is
// covered, along with the cuts through the content, the start marker, and the
// keystroke that follows the paste.
func TestPasteEndMarkerSurvivesEveryCut(t *testing.T) {
	const content = "one\ntwo\nthree\n"
	stream := pasteStart + content + pasteEnd + "z"
	for at := 1; at < len(stream); at++ {
		msgs, inPaste := feedReads(t, stream, at)
		onePaste(t, msgs, inPaste, content, "z")
		if t.Failed() {
			t.Fatalf("cut at offset %d of %d failed above", at, len(stream))
		}
	}
}

// TestPasteSurvivesEveryReadSize: the same stream delivered in reads of a fixed
// size, which is what a platform boundary actually is. Every size is swept, so
// the boundaries that matter are included with the ones that do not: the batch
// caps in this tree are 256 bytes (program_input_unix.go) and 128 events
// (program_input_windows.go), one event per character for a paste.
func TestPasteSurvivesEveryReadSize(t *testing.T) {
	content := strings.Repeat("x", 40)
	stream := pasteStart + content + pasteEnd + "z"
	for size := 1; size <= len(stream); size++ {
		var cuts []int
		for at := size; at < len(stream); at += size {
			cuts = append(cuts, at)
		}
		msgs, inPaste := feedReads(t, stream, cuts...)
		onePaste(t, msgs, inPaste, content, "z")
		if t.Failed() {
			t.Fatalf("reads of %d bytes failed above", size)
		}
	}
}

// TestPasteContentEndingInAMarkerHead: paste content may genuinely end with the
// head of the end marker. Held back and re-joined, it must arrive intact and
// must not eat the marker that follows it.
func TestPasteContentEndingInAMarkerHead(t *testing.T) {
	content := "ab\x1b[20"
	stream := pasteStart + content + pasteEnd
	// The boundary falls exactly where the content stops, so the first read ends
	// with the same bytes a cut marker would.
	msgs, inPaste := feedReads(t, stream, len(pasteStart)+len(content))
	onePaste(t, msgs, inPaste, content, "")
}

// TestPasteWithoutItsMarkerResolvesOnSilence: the head of an end marker is held
// for the next read, and the input loop's silence timeout is what resolves a head
// that never gets one. The head is dropped as the unknown sequence it is; what
// was pasted in front of it still reaches the prompt, and the parser goes back to
// reading keys instead of swallowing them.
func TestPasteWithoutItsMarkerResolvesOnSilence(t *testing.T) {
	p := &InputParser{}
	msgs := p.Parse([]byte(pasteStart + "pasted text" + "\x1b[20"))
	if len(msgs) != 0 {
		t.Fatalf("an open paste produced %v, want nothing until its marker", describeMsgs(msgs))
	}
	if !p.inPaste {
		t.Fatal("the parser left paste mode without an end marker")
	}
	if !p.HasPending() {
		t.Error("the marker head is not held as an outstanding sequence, so the loop's " +
			"silence timeout is never armed for it")
	}

	resolved := p.Flush()
	if len(resolved) != 1 {
		t.Fatalf("Flush returned %v, want the one paste", describeMsgs(resolved))
	}
	paste, ok := resolved[0].(PasteMsg)
	if !ok || paste.Content != "pasted text" {
		t.Fatalf("Flush returned %v, want PasteMsg{pasted text}", describeMsgs(resolved))
	}
	if p.inPaste || p.HasPending() {
		t.Errorf("after Flush: inPaste=%v, pending=%q", p.inPaste, p.pending)
	}

	// And the keyboard works again.
	typed := p.Parse([]byte("h"))
	if len(typed) != 1 {
		t.Fatalf("a byte typed after the resolved paste produced %v, want one key", describeMsgs(typed))
	}
	if key, isKey := typed[0].(KeyPressMsg); !isKey || Key(key).String() != "h" {
		t.Fatalf("a byte typed after the resolved paste arrived as %v, want the key h", describeMsgs(typed))
	}
}

// TestPasteWithNoMarkerHeadResolvesOnSilence is the case the state machine was
// added for. A paste body does not have to end in the head of its end marker, so
// there is often nothing to hold in pending — and a Flush gated on pending left
// the parser in paste mode forever, taking every keystroke that followed as
// pasted text. MidSequence (not HasPending) is the unfinished-input test, and
// Flush must resolve the open paste even though pending is empty.
func TestPasteWithNoMarkerHeadResolvesOnSilence(t *testing.T) {
	p := &InputParser{}
	// "pasted text" is not a suffix of the end marker, so nothing is held.
	msgs := p.Parse([]byte(pasteStart + "pasted text"))
	if len(msgs) != 0 {
		t.Fatalf("an open paste produced %v, want nothing until its marker", describeMsgs(msgs))
	}
	if !p.inPaste {
		t.Fatal("the parser left paste mode without an end marker")
	}
	if p.HasPending() {
		t.Fatal("content that is not a marker head must not be buffered as pending")
	}
	if !p.MidSequence() {
		t.Fatal("an open paste is unfinished input, but MidSequence reported otherwise")
	}

	resolved := p.Flush()
	if len(resolved) != 1 {
		t.Fatalf("Flush returned %v, want the one paste", describeMsgs(resolved))
	}
	paste, ok := resolved[0].(PasteMsg)
	if !ok || paste.Content != "pasted text" {
		t.Fatalf("Flush returned %v, want PasteMsg{pasted text}", describeMsgs(resolved))
	}
	if p.inPaste || p.MidSequence() {
		t.Errorf("after Flush: inPaste=%v, MidSequence=%v", p.inPaste, p.MidSequence())
	}

	// And the keyboard works again.
	typed := p.Parse([]byte("h"))
	if len(typed) != 1 {
		t.Fatalf("a byte typed after the resolved paste produced %v, want one key", describeMsgs(typed))
	}
	if key, isKey := typed[0].(KeyPressMsg); !isKey || Key(key).String() != "h" {
		t.Fatalf("a byte typed after the resolved paste arrived as %v, want the key h", describeMsgs(typed))
	}
}

// TestParserMidSequence pins the predicate the loop arms its timeout on: it must
// be false in ground and true for every unfinished state, including an open paste
// whose buffer is empty.
func TestParserMidSequence(t *testing.T) {
	p := &InputParser{}
	if p.MidSequence() {
		t.Fatal("a fresh parser is in ground, but MidSequence reported unfinished input")
	}

	// An incomplete escape sequence.
	p.Parse([]byte("\x1b["))
	if !p.MidSequence() {
		t.Fatal("a held escape sequence must count as unfinished input")
	}
	p.Flush()
	if p.MidSequence() {
		t.Fatal("MidSequence stayed true after the escape sequence was flushed")
	}

	// An open paste with no marker head buffered.
	p.Parse([]byte(pasteStart + "tail"))
	if !p.MidSequence() {
		t.Fatal("an open paste must count as unfinished input")
	}
	p.Flush()
	if p.MidSequence() {
		t.Fatal("MidSequence stayed true after the open paste was flushed")
	}
}
