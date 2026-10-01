package llm_test

// TestFitSurvivesCraftedHeaders feeds the image pipeline malformed and
// half-malformed JPEG bytes and requires that nothing panics.
//
// The property is not academic here: the fit runs at ingest, on the
// user-input goroutine and (in parallel tool-call mode) on a per-tool
// goroutine, and neither has a recover above it — a panic there takes the
// process with it, and the session with the process. The bytes can be corrupt
// for ordinary reasons (a truncated download, a half-written file the model
// was pointed at, a hand-edited session).
//
// The corpus is enumerated rather than random so it stays a stable regression:
// truncations, single-byte mutations across the header, injected segments with
// hostile length fields, 0xFF fill runs, plus a seeded set of random flips and
// tails. Most variants remain decodable, so they do reach the resampler and the
// APP-segment walker rather than stopping at the header check — the test logs
// how many.

import (
	"bytes"
	"encoding/base64"
	"image"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

func TestFitSurvivesCraftedHeaders(t *testing.T) {
	base, err := os.ReadFile(filepath.Join("testdata", "progressive.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	run := func(name string, raw []byte) {
		uri := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(raw)
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("PANIC on %s: %v", name, r)
			}
		}()
		llm.FitImagesForSend([]llm.ContentPart{&llm.ImagePart{URI: uri}})
	}

	variants, gotPastHeader := 0, 0
	countPast := func(raw []byte) {
		if _, _, err := image.DecodeConfig(bytes.NewReader(raw)); err == nil {
			gotPastHeader++
		}
	}
	for n := 0; n <= len(base); n += 16 {
		run("truncate", base[:n])
		countPast(base[:n])
		variants++
	}
	for i := 0; i < 96 && i < len(base); i++ {
		for _, v := range []byte{0x00, 0xFF, 0x7F} {
			m := append([]byte(nil), base...)
			m[i] = v
			run("byte-flip", m)
			countPast(m)
			variants++
		}
	}
	for _, size := range []int{0, 1, 2, 3, 4, 0xFFFF, 0xFFFE} {
		hdr := []byte{0xFF, 0xE1, byte(size >> 8), byte(size & 0xFF)}
		m := append(append([]byte{0xFF, 0xD8}, hdr...), base[2:]...)
		run("inject-app1", m)
		countPast(m)
		variants++
	}
	for n := 1; n <= 8; n++ {
		fill := make([]byte, n)
		for i := range fill {
			fill[i] = 0xFF
		}
		m := append(append([]byte{0xFF, 0xD8}, fill...), base[2:]...)
		run("fill-run", m)
		countPast(m)
		variants++
	}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 400; i++ {
		m := append([]byte(nil), base...)
		for f := 0; f < 1+rng.Intn(6); f++ {
			m[rng.Intn(len(m))] = byte(rng.Intn(256))
		}
		run("random-flip", m)
		countPast(m)
		variants++
	}
	for i := 0; i < 100; i++ {
		tail := make([]byte, rng.Intn(64))
		rng.Read(tail)
		tailRaw := append(append([]byte(nil), base...), tail...)
		run("random-tail", tailRaw)
		countPast(tailRaw)
		variants++
	}
	t.Logf("variants fed: %d; still decodable, i.e. reached the resampler and the APP walker: %d", variants, gotPastHeader)
	if gotPastHeader < 500 {
		t.Fatalf("only %d variants stayed decodable — the corpus stopped exercising the code this test is for", gotPastHeader)
	}
}
