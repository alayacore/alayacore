package terminal

// Fuzzing the two sequence bodies whose bytes are arbitrary.
//
// The table in key_parser.go names the families the decoder recognizes; the
// report and read-invariance tests pin fixed exemplars of each. What a fixed
// exemplar cannot promise is that *any* body is safe, and those two bodies are
// exactly where "any" is real: a string control carries a color, a capability or
// a clipboard read (arbitrary bytes), and an X10 mouse report carries three raw
// coordinate bytes (arbitrary, and one of them being a byte parseCSI calls a
// final was a real bug). These fuzz targets assert the invariant over the whole
// space: no byte belonging to a recognized sequence may arrive as a KeyPressMsg.

import (
	"bytes"
	"testing"
)

// FuzzStringControlBodiesAreNotKeys: wrap an arbitrary body in each string-control
// family and require no key to come out. A body containing a terminator is skipped
// — it would end the control early, and the remainder is then legitimately typing,
// which is not the property under test.
func FuzzStringControlBodiesAreNotKeys(f *testing.F) {
	f.Add([]byte("rgb:1e1e/1e1e/1e1e"))
	f.Add([]byte("52;c;aGVsbG8="))
	f.Add([]byte("1+r6d6978"))
	f.Add([]byte("0;window title"))
	f.Add([]byte("CJK 日本語 ; payload"))
	f.Fuzz(func(t *testing.T, body []byte) {
		if bytes.ContainsAny(body, "\x07\x1b") {
			return
		}
		families := []struct{ head, tail string }{
			{"\x1b]", "\x07"},   // OSC, BEL-terminated
			{"\x1b]", "\x1b\\"}, // OSC, ST-terminated
			{"\x1bP", "\x1b\\"}, // DCS
			{"\x1bX", "\x1b\\"}, // SOS
			{"\x1b^", "\x1b\\"}, // PM
			{"\x1b_", "\x1b\\"}, // APC
		}
		for _, fam := range families {
			stream := fam.head + string(body) + fam.tail
			var p InputParser
			msgs := p.Parse([]byte(stream))
			msgs = append(msgs, p.Flush()...)
			for _, m := range msgs {
				if _, isKey := m.(KeyPressMsg); isKey {
					t.Fatalf("%q produced the key %v; a reply's body must never be typing", stream, m)
				}
			}
		}
	})
}

// FuzzX10MouseCoordinatesAreNotKeys: `CSI M` is the one CSI whose length its
// final byte does not give — three raw coordinate bytes follow. Any three bytes
// must be consumed with the report, including one that parseCSI would call a
// final (which is how a report used to arrive as an arrow key nobody pressed).
func FuzzX10MouseCoordinatesAreNotKeys(f *testing.F) {
	f.Add(byte(' '), byte('!'), byte('#'))
	f.Add(byte(' '), byte(' '), byte('A')) // the historical failure: final byte 'A'
	f.Add(byte('E'), byte('E'), byte('F'))
	f.Fuzz(func(t *testing.T, a, b, c byte) {
		stream := string([]byte{0x1b, '[', 'M', a, b, c})
		var p InputParser
		msgs := p.Parse([]byte(stream))
		msgs = append(msgs, p.Flush()...)
		for _, m := range msgs {
			if _, isKey := m.(KeyPressMsg); isKey {
				t.Fatalf("%q produced the key %v; X10 coordinates are not typing", stream, m)
			}
		}
	})
}
