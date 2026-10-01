package llm_test

// Tests for the image-fitting forms in media_fit.go: FitImagesForSend (send),
// ShrinkImages (ingest) where the two differ, and the shrink they share —
// formats, geometry, channels, alpha, EXIF/ICC carry-through, and what happens
// to an image that cannot be decoded at all.
//
// The wire-level half — that each provider actually calls the send form — lives
// in internal/llm/providers/media_fit_test.go, where the stub fixtures are.

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

// testMaxImageEdge mirrors the unexported maxImageEdge. Kept as a named
// constant so the boundary cases below read as boundary cases.
const testMaxImageEdge = 4096

// mustPNGDataURI encodes a real PNG of the given size as a data URI. Fixtures
// that stand in for "an image on the wire" must be genuine images: the gate
// reads headers and re-encodes, so a stub that is not a decodable image would
// be degraded to text.
func mustPNGDataURI(w, h int) string {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		panic(err) // a fixed-size fixture cannot fail to encode
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// asUserParts stamps the user role on each part, the way the session does
// before storing them.
func asUserParts(parts ...llm.ContentPart) []llm.ContentPart {
	for _, p := range parts {
		p.SetRole(llm.RoleUser)
	}
	return parts
}

// dataURIDims decodes just the header of a data URI image.
func dataURIDims(t *testing.T, uri string) (int, int) {
	t.Helper()
	_, b64, ok := llm.ParseDataURI(uri)
	if !ok {
		t.Fatalf("not a data URI: %.60s", uri)
	}
	cfg, _, err := image.DecodeConfig(base64.NewDecoder(base64.StdEncoding, strings.NewReader(b64)))
	if err != nil {
		t.Fatalf("decode result header: %v", err)
	}
	return cfg.Width, cfg.Height
}

// TestFitImagesTable pins the decision itself.
func TestFitImagesTable(t *testing.T) {
	cases := []struct {
		name string
		part llm.ContentPart
		want string // "unchanged", "shrunk", "note"
	}{
		{"at the limit", &llm.ImagePart{URI: mustPNGDataURI(testMaxImageEdge, 1)}, "unchanged"},
		{"over the limit", &llm.ImagePart{URI: mustPNGDataURI(testMaxImageEdge+1, 1)}, "shrunk"},
		{"over the limit on the other side", &llm.ImagePart{URI: mustPNGDataURI(1, testMaxImageEdge+1)}, "shrunk"},
		{"square, far over", &llm.ImagePart{URI: mustPNGDataURI(9000, 3000)}, "shrunk"},
		{"unmeasurable format", &llm.ImagePart{URI: "data:image/webp;base64,AAAA"}, "note"},
		{"remote url", &llm.ImagePart{URI: "https://example.com/a.png"}, "unchanged"},
		{"video untouched", &llm.VideoPart{URI: "data:video/mp4;base64,AAAA"}, "unchanged"},
		{"document untouched", &llm.DocumentPart{URI: "data:application/pdf;base64,JVBERi0="}, "unchanged"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := asUserParts(tc.part)
			out := llm.FitImagesForSend(in)

			switch tc.want {
			case "unchanged":
				if out[0] != tc.part {
					t.Fatalf("part was replaced: %T", out[0])
				}
			case "shrunk":
				img, ok := out[0].(*llm.ImagePart)
				if !ok {
					t.Fatalf("out[0] = %T, want a shrunk *llm.ImagePart", out[0])
				}
				w, h := dataURIDims(t, img.URI)
				if w > testMaxImageEdge || h > testMaxImageEdge {
					t.Fatalf("result is %dx%d, still over the %d limit", w, h, testMaxImageEdge)
				}
				if w != testMaxImageEdge && h != testMaxImageEdge {
					t.Errorf("result is %dx%d; the longest side must be exactly the limit", w, h)
				}
			case "note":
				if _, ok := out[0].(*llm.TextPart); !ok {
					t.Fatalf("out[0] = %T, want *llm.TextPart", out[0])
				}
			}
			// The input slice must still hold the original part, untouched.
			if in[0] != tc.part {
				t.Errorf("input part was replaced in the slice")
			}
		})
	}
}

// TestFitPreservesAspectRatio guards the shape of the shrink, not just its size.
func TestFitPreservesAspectRatio(t *testing.T) {
	out := llm.FitImagesForSend(asUserParts(&llm.ImagePart{URI: mustPNGDataURI(8192, 2048)}))
	img, ok := out[0].(*llm.ImagePart)
	if !ok {
		t.Fatalf("out[0] = %T, want *llm.ImagePart", out[0])
	}
	w, h := dataURIDims(t, img.URI)
	if w != testMaxImageEdge || h != testMaxImageEdge/4 {
		t.Fatalf("got %dx%d, want %dx%d (4:1 preserved)", w, h, testMaxImageEdge, testMaxImageEdge/4)
	}
}

// TestFitDoesNotMutateInput guards the layering: the function returns new parts
// and never rewrites the ones it was handed, so a caller that keeps its own
// copy (or hands the same slice to two providers) is not surprised.
func TestFitDoesNotMutateInput(t *testing.T) {
	original := mustPNGDataURI(testMaxImageEdge+1, 1)
	img := &llm.ImagePart{URI: original}
	img.SetRole(llm.RoleUser)
	in := []llm.ContentPart{img}

	out := llm.FitImagesForSend(in)

	got, ok := in[0].(*llm.ImagePart)
	if !ok || got.URI != original {
		t.Fatalf("input part was mutated: %T", in[0])
	}
	if out[0].GetRole() != llm.RoleUser {
		t.Errorf("fitted part role = %q, want %q (wire grouping depends on it)", out[0].GetRole(), llm.RoleUser)
	}
	if out[0].GetHistoryID() != img.GetHistoryID() {
		t.Errorf("fitted part lost the history ID")
	}
}

// TestFitInsideToolResult covers the nested shape: media returned by a tool
// lives inside ToolOutputPart.Output, and that slice must be rebuilt too.
func TestFitInsideToolResult(t *testing.T) {
	tr := &llm.ToolOutputPart{ID: "call-1", Output: []llm.ContentPart{
		&llm.TextPart{Text: "Read a.png"},
		&llm.ImagePart{URI: mustPNGDataURI(testMaxImageEdge+1, 1)},
	}}

	out := llm.FitImagesForSend([]llm.ContentPart{tr})
	got, ok := out[0].(*llm.ToolOutputPart)
	if !ok {
		t.Fatalf("out[0] = %T, want *llm.ToolOutputPart", out[0])
	}
	if got == tr {
		t.Fatal("tool result was not cloned — mutating it would corrupt the session")
	}
	if got.ID != tr.ID {
		t.Errorf("clone lost the tool call id: %q", got.ID)
	}
	fitted, ok := got.Output[1].(*llm.ImagePart)
	if !ok {
		t.Fatalf("nested image = %T, want a shrunk *llm.ImagePart", got.Output[1])
	}
	if w, h := dataURIDims(t, fitted.URI); w > testMaxImageEdge || h > testMaxImageEdge {
		t.Errorf("nested image is %dx%d, still over the limit", w, h)
	}
	if _, ok := tr.Output[1].(*llm.ImagePart); !ok {
		t.Error("the original tool result was mutated")
	}
}

// TestUnsendableNoteDeniesPerception guards the wording of the fallback, which
// is the substance: a model that reads "[image too large]" as "a result came
// back" will describe media it never received.
func TestUnsendableNoteDeniesPerception(t *testing.T) {
	out := llm.FitImagesForSend(asUserParts(&llm.ImagePart{URI: "data:image/webp;base64,AAAA"}))
	text := out[0].(*llm.TextPart).Text

	for _, want := range []string{"NOT delivered", "Do not describe or quote it", "execute_command"} {
		if !strings.Contains(text, want) {
			t.Errorf("note missing %q: %s", want, text)
		}
	}
	if strings.Contains(text, "AAAA") {
		t.Errorf("note leaked the media payload: %s", text)
	}
}

// jpegWithApp1 encodes a JPEG of the given size and splices an APP1 segment in
// right after its SOI, the way a camera writes EXIF.
func jpegWithApp1(t *testing.T, w, h int, payload []byte) string {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h)), &jpeg.Options{Quality: 80}); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	enc := buf.Bytes()
	if len(enc) < 2 || enc[0] != 0xFF || enc[1] != 0xD8 {
		t.Fatalf("encoder did not emit SOI first: % x", enc[:min(4, len(enc))])
	}
	seg := []byte{0xFF, 0xE1, byte((len(payload) + 2) >> 8), byte((len(payload) + 2) & 0xFF)}
	seg = append(seg, payload...)
	out := append([]byte{0xFF, 0xD8}, seg...)
	out = append(out, enc[2:]...)
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(out)
}

// resultBytes returns the raw bytes behind a shrunk part's data URI.
func resultBytes(t *testing.T, out []llm.ContentPart) []byte {
	t.Helper()
	img, ok := out[0].(*llm.ImagePart)
	if !ok {
		t.Fatalf("out[0] = %T, want a shrunk *llm.ImagePart", out[0])
	}
	_, b64, ok := llm.ParseDataURI(img.URI)
	if !ok {
		t.Fatalf("shrunk result is not a data URI: %.60s", img.URI)
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("decode shrunk result: %v", err)
	}
	if !bytes.HasPrefix(raw, []byte{0xFF, 0xD8}) {
		t.Fatal("shrunk result is not a JPEG")
	}
	return raw
}

// TestJPEGMetadataCarriedThrough is the guard for the thing a re-encode would
// otherwise silently destroy: EXIF (a phone stores the orientation tag instead
// of rotating its pixels) and the ICC profile. Both live in APP segments, which
// are copied verbatim rather than interpreted.
func TestJPEGMetadataCarriedThrough(t *testing.T) {
	payload := []byte("ALAYACORETESTEXIFPAYLOAD0123456789")
	uri := jpegWithApp1(t, testMaxImageEdge+1, 3, payload)

	out := llm.FitImagesForSend(asUserParts(&llm.ImagePart{URI: uri}))
	raw := resultBytes(t, out)

	if !bytes.Contains(raw, payload) {
		t.Fatal("APP1 segment was dropped by the re-encode — EXIF orientation and ICC would be lost")
	}
	if w, h := dataURIDims(t, out[0].(*llm.ImagePart).URI); w > testMaxImageEdge || h > testMaxImageEdge {
		t.Fatalf("result is %dx%d, still over the limit", w, h)
	}
}

// TestJPEGWithoutMetadataIsStillValid is the other side: a JPEG with nothing to
// carry must come back as a valid, shrunk JPEG rather than a broken splice.
func TestJPEGWithoutMetadataIsStillValid(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, testMaxImageEdge+1, 3)), &jpeg.Options{Quality: 80}); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	uri := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())

	out := llm.FitImagesForSend(asUserParts(&llm.ImagePart{URI: uri}))
	resultBytes(t, out) // asserts it is a JPEG

	if w, _ := dataURIDims(t, out[0].(*llm.ImagePart).URI); w != testMaxImageEdge {
		t.Fatalf("result width = %d, want %d", w, testMaxImageEdge)
	}
}

// BenchmarkFitOversizeImage measures the cost of shrinking the kind of image
// that actually triggers the gate. It is the number that decides whether the
// hot-path call sites (agent ingest) are worth having.
func BenchmarkFitOversizeImage(b *testing.B) {
	uri := mustPNGDataURI(6000, 4000) // 24 MP
	in := []llm.ContentPart{&llm.ImagePart{URI: uri}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		llm.FitImagesForSend(in)
	}
}

// decodeDataURI decodes a data URI image fully, for pixel-level assertions.
func decodeDataURI(t *testing.T, uri string) image.Image {
	t.Helper()
	_, b64, ok := llm.ParseDataURI(uri)
	if !ok {
		t.Fatalf("not a data URI: %.60s", uri)
	}
	img, _, err := image.Decode(base64.NewDecoder(base64.StdEncoding, strings.NewReader(b64)))
	if err != nil {
		t.Fatalf("decode fitted image: %v", err)
	}
	return img
}

// TestShrinkPreservesGeometryAndChannels is the sampler's shape test: a
// four-quadrant image survives the box filter with every quadrant in its own
// corner and its own color. An off-by-one in the source-rectangle arithmetic, a
// transposed pass, or swapped channels would all show up here and nowhere else
// — a size assertion cannot see any of them.
func TestShrinkPreservesGeometryAndChannels(t *testing.T) {
	const w, h = 5000, 1000
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var c color.RGBA
			switch {
			case x < w/2 && y < h/2:
				c = color.RGBA{R: 255, A: 255} // top-left: red
			case x >= w/2 && y < h/2:
				c = color.RGBA{B: 255, A: 255} // top-right: blue
			case x < w/2:
				c = color.RGBA{G: 255, A: 255} // bottom-left: green
			default:
				c = color.RGBA{R: 255, G: 255, B: 255, A: 255} // bottom-right: white
			}
			src.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())

	out := llm.FitImagesForSend(asUserParts(&llm.ImagePart{URI: uri}))
	got := decodeDataURI(t, out[0].(*llm.ImagePart).URI)

	b := got.Bounds()
	const inset = 8 // stay clear of the averaged seam between quadrants
	probe := func(name, want string, x, y int) {
		t.Helper()
		r, g, bl, _ := got.At(x, y).RGBA()
		onR, onG, onB := r >= 0xf000, g >= 0xf000, bl >= 0xf000
		var have string
		switch {
		case onR && onG && onB:
			have = "white"
		case onR:
			have = "red"
		case onG:
			have = "green"
		case onB:
			have = "blue"
		default:
			have = fmt.Sprintf("r=%#x g=%#x b=%#x", r, g, bl)
		}
		if have != want {
			t.Errorf("%s corner at (%d,%d) = %s, want %s", name, x, y, have, want)
		}
	}
	probe("top-left", "red", inset, inset)
	probe("top-right", "blue", b.Dx()-1-inset, inset)
	probe("bottom-left", "green", inset, b.Dy()-1-inset)
	probe("bottom-right", "white", b.Dx()-1-inset, b.Dy()-1-inset)

	if b.Dx() != testMaxImageEdge || b.Dy() != testMaxImageEdge*h/w {
		t.Errorf("fitted size = %dx%d, want %dx%d", b.Dx(), b.Dy(), testMaxImageEdge, testMaxImageEdge*h/w)
	}
}

// TestShrinkPreservesAlpha: alpha survives the average. image.RGBA is
// premultiplied, so a translucent pixel is checked as premultiplied — the
// average of premultiplied samples is the premultiplied average.
func TestShrinkPreservesAlpha(t *testing.T) {
	const w, h = 5000, 100
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			src.SetRGBA(x, y, color.RGBA{R: 128, A: 128}) // 50% red, premultiplied
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())

	out := llm.FitImagesForSend(asUserParts(&llm.ImagePart{URI: uri}))
	got := decodeDataURI(t, out[0].(*llm.ImagePart).URI)

	b := got.Bounds()
	r, g, bl, a := got.At(b.Dx()/2, b.Dy()/2).RGBA()
	const want, slack = 0x8080, 0x0400
	if a < want-slack || a > want+slack {
		t.Errorf("alpha = %#x, want ~%#x", a, want)
	}
	if r < want-slack || r > want+slack {
		t.Errorf("red = %#x, want ~%#x (premultiplied)", r, want)
	}
	if g > slack || bl > slack {
		t.Errorf("green/blue = %#x/%#x, want ~0", g, bl)
	}
}

// quadrants builds a four-color test image: red top-left, blue top-right,
// green bottom-left, white bottom-right.
func quadrants(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var c color.RGBA
			switch {
			case x < w/2 && y < h/2:
				c = color.RGBA{R: 255, A: 255}
			case x >= w/2 && y < h/2:
				c = color.RGBA{B: 255, A: 255}
			case x < w/2:
				c = color.RGBA{G: 255, A: 255}
			default:
				c = color.RGBA{R: 255, G: 255, B: 255, A: 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// dominantCorner names the color a probe sample landed on, tolerating the
// rounding a lossy (JPEG) or quantized (GIF) encode introduces. Only the
// dominant channel is judged, so JPEG ringing near the seams cannot fail it.
func dominantCorner(t *testing.T, img image.Image, x, y int) string {
	t.Helper()
	r, g, b, _ := img.At(x, y).RGBA()
	switch {
	case r < 0x8000 && g < 0x8000 && b < 0x8000:
		return "black"
	case r >= 0x8000 && g >= 0x8000 && b >= 0x8000:
		return "white"
	case r >= 0x8000 && g < 0x8000 && b < 0x8000:
		return "red"
	case g >= 0x8000 && r < 0x8000 && b < 0x8000:
		return "green"
	case b >= 0x8000 && r < 0x8000 && g < 0x8000:
		return "blue"
	}
	return fmt.Sprintf("mixed r=%#x g=%#x b=%#x", r, g, b)
}

// TestShrinkRealEncodedImagePerFormat is the only test that exercises every
// format the client claims to support through a real encoder and a real
// decoder, and then looks at the pixels. It answers three questions per format
// that nothing else does: does it decode at all, does it come back as a
// sendable image rather than the note, and are the colors and the layout still
// right after the resample.
func TestShrinkRealEncodedImagePerFormat(t *testing.T) {
	const w, h = 5000, 200 // over the edge limit, small enough to stay fast
	src := quadrants(w, h)

	cases := []struct {
		name     string
		encode   func(t *testing.T, img image.Image) []byte
		wantMIME string
	}{
		{"png", func(t *testing.T, img image.Image) []byte {
			var b bytes.Buffer
			if err := png.Encode(&b, img); err != nil {
				t.Fatalf("encode: %v", err)
			}
			return b.Bytes()
		}, "data:image/png;base64,"},
		{"jpeg", func(t *testing.T, img image.Image) []byte {
			var b bytes.Buffer
			if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 95}); err != nil {
				t.Fatalf("encode: %v", err)
			}
			return b.Bytes()
		}, "data:image/jpeg;base64,"},
		{"gif", func(t *testing.T, img image.Image) []byte {
			var b bytes.Buffer
			if err := gif.Encode(&b, img, &gif.Options{NumColors: 256}); err != nil {
				t.Fatalf("encode: %v", err)
			}
			return b.Bytes()
		}, "data:image/png;base64,"}, // GIF has no Go encoder on our side; PNG is the lossless stand-in
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := tc.encode(t, src)
			// The fixture must really be the format it claims to be, or the
			// test proves nothing about that decoder.
			if _, format, err := image.DecodeConfig(bytes.NewReader(raw)); err != nil || format != tc.name {
				t.Fatalf("fixture is not a valid %s: format=%q err=%v", tc.name, format, err)
			}

			uri := "data:image/" + tc.name + ";base64," + base64.StdEncoding.EncodeToString(raw)
			out := llm.FitImagesForSend(asUserParts(&llm.ImagePart{URI: uri}))

			img, ok := out[0].(*llm.ImagePart)
			if !ok {
				t.Fatalf("out[0] = %T, want an *llm.ImagePart — the %s was not decodable", out[0], tc.name)
			}
			if !strings.HasPrefix(img.URI, tc.wantMIME) {
				t.Errorf("result MIME = %.30s, want %s", img.URI, tc.wantMIME)
			}
			got := decodeDataURI(t, img.URI)

			b := got.Bounds()
			if b.Dx() > testMaxImageEdge || b.Dy() > testMaxImageEdge {
				t.Fatalf("result is %dx%d, still over the limit", b.Dx(), b.Dy())
			}
			if b.Dx() != testMaxImageEdge || b.Dy() != testMaxImageEdge*h/w {
				t.Errorf("result is %dx%d, want %dx%d", b.Dx(), b.Dy(), testMaxImageEdge, testMaxImageEdge*h/w)
			}

			const inset = 24 // clear of the seam, and of JPEG ringing near it
			for _, c := range []struct {
				want string
				x, y int
			}{
				{"red", inset, inset},
				{"blue", b.Dx() - 1 - inset, inset},
				{"green", inset, b.Dy() - 1 - inset},
				{"white", b.Dx() - 1 - inset, b.Dy() - 1 - inset},
			} {
				if have := dominantCorner(t, got, c.x, c.y); have != c.want {
					t.Errorf("%s corner at (%d,%d) = %s, want %s", tc.name, c.x, c.y, have, c.want)
				}
			}
		})
	}
}

// TestShrinkGIFTransparency: a GIF's transparent index must survive to the
// PNG that replaces it. GIF is the one supported format whose transparency
// lives in the palette, so it decodes to *image.Paletted rather than an
// RGBA-family type — a different concrete type through the conversion.
func TestShrinkGIFTransparency(t *testing.T) {
	const w, h = 5000, 200
	// The fixture must be a *image.Paletted carrying an explicit transparent
	// palette entry: gif.Encode given an RGBA source quantizes with an opaque
	// palette (palette.Plan9), which silently drops the transparency — that
	// would test the encoder's limitation, not this client's handling.
	pal := color.Palette{color.RGBA{R: 255, A: 255}, color.RGBA{}}
	src := image.NewPaletted(image.Rect(0, 0, w, h), pal)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x >= w/2 {
				src.SetColorIndex(x, y, 1) // transparent entry
			}
		}
	}
	var buf bytes.Buffer
	if err := gif.Encode(&buf, src, nil); err != nil {
		t.Fatalf("encode gif: %v", err)
	}

	uri := "data:image/gif;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	out := llm.FitImagesForSend(asUserParts(&llm.ImagePart{URI: uri}))
	img, ok := out[0].(*llm.ImagePart)
	if !ok {
		t.Fatalf("out[0] = %T, want *llm.ImagePart", out[0])
	}
	got := decodeDataURI(t, img.URI)

	b := got.Bounds()
	if have := dominantCorner(t, got, 24, b.Dy()/2); have != "red" {
		t.Errorf("opaque half = %s, want red", have)
	}
	if a := color.RGBAModel.Convert(got.At(b.Dx()-1-24, b.Dy()/2)).(color.RGBA).A; a > 4 {
		t.Errorf("alpha of the transparent half = %#x, want ~0", a)
	}
}

// TestShrinkAnimatedGIFUsesFirstFrame: an animated GIF must not fail the fit.
// The client decodes with image.Decode, which yields the first frame (the
// models treat a GIF as a still either way).
func TestShrinkAnimatedGIFUsesFirstFrame(t *testing.T) {
	const w, h = 5000, 200
	frames := make([]*image.Paletted, 2)
	for i := range frames {
		p := image.NewPaletted(image.Rect(0, 0, w, h), palette.Plan9)
		draw.Draw(p, p.Bounds(), quadrants(w, h), image.Point{}, draw.Src)
		frames[i] = p
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, &gif.GIF{Image: frames, Delay: []int{0, 0}}); err != nil {
		t.Fatalf("encode animated gif: %v", err)
	}

	uri := "data:image/gif;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	out := llm.FitImagesForSend(asUserParts(&llm.ImagePart{URI: uri}))
	img, ok := out[0].(*llm.ImagePart)
	if !ok {
		t.Fatalf("out[0] = %T, want *llm.ImagePart (animated GIF must still fit)", out[0])
	}
	got := decodeDataURI(t, img.URI)

	b := got.Bounds()
	if b.Dx() > testMaxImageEdge || b.Dy() > testMaxImageEdge {
		t.Fatalf("result is %dx%d, still over the limit", b.Dx(), b.Dy())
	}
	if have := dominantCorner(t, got, 24, 24); have != "red" {
		t.Errorf("first frame top-left = %s, want red", have)
	}
}

// TestShrinkOtherDecodeTypes closes the last gap in format coverage: PNG color
// types that decode to something outside the RGBA family. Both take
// premultipliedRGBA's copy path (image/draw), which is stdlib, but the point of
// a coverage test is to notice when that stops being true.
func TestShrinkOtherDecodeTypes(t *testing.T) {
	const w, h = 5000, 200

	t.Run("gray", func(t *testing.T) {
		g := image.NewGray(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				if x < w/2 {
					g.SetGray(x, y, color.Gray{Y: 255})
				} else {
					g.SetGray(x, y, color.Gray{Y: 0})
				}
			}
		}
		got := fitAndDecode(t, "png", g)
		b := got.Bounds()
		if have := dominantCorner(t, got, 24, b.Dy()/2); have != "white" {
			t.Errorf("light half = %s, want white", have)
		}
		if have := dominantCorner(t, got, b.Dx()-1-24, b.Dy()/2); have != "black" {
			t.Errorf("dark half = %s, want black", have)
		}
	})

	t.Run("nrgba64", func(t *testing.T) {
		n := image.NewNRGBA64(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				if x < w/2 {
					n.SetNRGBA64(x, y, color.NRGBA64{R: 0xffff, A: 0xffff})
				} else {
					n.SetNRGBA64(x, y, color.NRGBA64{B: 0xffff, A: 0xffff})
				}
			}
		}
		got := fitAndDecode(t, "png", n)
		b := got.Bounds()
		if have := dominantCorner(t, got, 24, b.Dy()/2); have != "red" {
			t.Errorf("left half = %s, want red", have)
		}
		if have := dominantCorner(t, got, b.Dx()-1-24, b.Dy()/2); have != "blue" {
			t.Errorf("right half = %s, want blue", have)
		}
	})
}

// fitAndDecode PNG-encodes img, runs the fitter over it, and returns the
// decoded result — failing if the image did not survive as an image.
func fitAndDecode(t *testing.T, ext string, img image.Image) image.Image {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	uri := "data:image/" + ext + ";base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	out := llm.FitImagesForSend(asUserParts(&llm.ImagePart{URI: uri}))
	fitted, ok := out[0].(*llm.ImagePart)
	if !ok {
		t.Fatalf("out[0] = %T, want *llm.ImagePart — the image did not fit", out[0])
	}
	got := decodeDataURI(t, fitted.URI)
	if b := got.Bounds(); b.Dx() > testMaxImageEdge || b.Dy() > testMaxImageEdge {
		t.Fatalf("result is %dx%d, still over the limit", b.Dx(), b.Dy())
	}
	return got
}

// TestShrinkGIFSubFrameIsNotUpscaled is the regression guard for a decoder that
// contradicts its own header. A GIF's DecodeConfig reports the logical screen
// while Decode returns only the first frame, so an image the header called
// oversized can reach the resampler already small. Rescaling that to the limit
// would blow a 100x50 frame up to 4096x2048: more pixels, more bytes, more
// tokens, and no detail that was ever there.
func TestShrinkGIFSubFrameIsNotUpscaled(t *testing.T) {
	pal := color.Palette{color.RGBA{R: 255, A: 255}, color.RGBA{B: 255, A: 255}}
	frame := image.NewPaletted(image.Rect(0, 0, 100, 50), pal)
	for y := 0; y < 50; y++ {
		for x := 0; x < 100; x++ {
			if x >= 50 {
				frame.SetColorIndex(x, y, 1)
			}
		}
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, &gif.GIF{
		Image:  []*image.Paletted{frame},
		Delay:  []int{0},
		Config: image.Config{ColorModel: pal, Width: 5000, Height: 200},
	}); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	// Precondition: the header really must call this oversized, or the test is
	// not exercising the disagreement it is about.
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(buf.Bytes())); err != nil || cfg.Width <= testMaxImageEdge {
		t.Fatalf("fixture is not the shape this test is about: %dx%d err=%v", cfg.Width, cfg.Height, err)
	}

	uri := "data:image/gif;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	out := llm.FitImagesForSend(asUserParts(&llm.ImagePart{URI: uri}))
	img, ok := out[0].(*llm.ImagePart)
	if !ok {
		t.Fatalf("out[0] = %T, want *llm.ImagePart", out[0])
	}
	if img.URI != uri {
		t.Fatal("a frame already inside the limit was re-encoded — it must pass through untouched")
	}
	if b := decodeDataURI(t, img.URI).Bounds(); b.Dx() != 100 || b.Dy() != 50 {
		t.Fatalf("result is %dx%d, want the untouched 100x50 (upscaling would give 4096x2048)", b.Dx(), b.Dy())
	}
}

// TestShrinkImagesAndFitImagesDifferOnlyOnAnUndecodableImage is the A/B guard
// for the two entry points. Everything else — a shrunk image, a part that is
// not an image at all — is identical; the whole difference is what happens to an
// image neither can decode.
func TestShrinkImagesAndFitImagesDifferOnlyOnAnUndecodableImage(t *testing.T) {
	cases := []struct {
		name string
		part llm.ContentPart
	}{
		{"undecodable", &llm.ImagePart{URI: "data:image/webp;base64,AAAA"}},
		{"oversize, shrinkable", &llm.ImagePart{URI: mustPNGDataURI(testMaxImageEdge+1, 1)}},
		{"not an image", &llm.VideoPart{URI: "data:video/mp4;base64,AAAA"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stored := llm.ShrinkImages(asUserParts(clonePart(tc.part)))[0]
			sent := llm.FitImagesForSend(asUserParts(clonePart(tc.part)))[0]

			switch tc.name {
			case "undecodable":
				if _, ok := stored.(*llm.ImagePart); !ok {
					t.Fatalf("ingest stored %T, want the image kept", stored)
				}
				if _, ok := sent.(*llm.TextPart); !ok {
					t.Fatalf("send produced %T, want the note", sent)
				}
			default:
				// For everything else the two forms must agree exactly.
				if reflect.TypeOf(stored) != reflect.TypeOf(sent) {
					t.Fatalf("entry points disagree: ingest %T, send %T", stored, sent)
				}
			}
		})
	}
}

// clonePart returns a fresh copy of a fixture part, so one subtest cannot
// observe another's slice.
func clonePart(p llm.ContentPart) llm.ContentPart {
	switch v := p.(type) {
	case *llm.ImagePart:
		c := *v
		return &c
	case *llm.VideoPart:
		c := *v
		return &c
	default:
		return p
	}
}

// TestShrinkRealJPEGVariants covers the two JPEG shapes a generated fixture
// cannot reach:
//
//   - progressive.jpg is interlaced, which is a branch inside the decoder that
//     a baseline fixture never takes (it still decodes to *image.YCbCr).
//   - cmyk.jpg is a 4-component JPEG, which decodes to *image.CMYK — a
//     different concrete type, and therefore a different color model through
//     premultipliedRGBA's conversion, which is the part of this package that
//     touches every pixel.
//
// Both fixtures are 4100x16, left-red/right-blue: over the edge limit, and
// small enough in pixels that the files stay tiny. They were written with
// Pillow (progressive=True, and convert("CMYK") for the second).
func TestShrinkRealJPEGVariants(t *testing.T) {
	for _, fixture := range []string{"progressive.jpg", "cmyk.jpg"} {
		t.Run(strings.TrimSuffix(fixture, ".jpg"), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", fixture))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			// The fixture must be the shape this test is about.
			cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
			if err != nil || format != "jpeg" || cfg.Width <= testMaxImageEdge {
				t.Fatalf("fixture is not an oversized jpeg: %s %dx%d err=%v", format, cfg.Width, cfg.Height, err)
			}

			uri := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(raw)
			out := llm.FitImagesForSend(asUserParts(&llm.ImagePart{URI: uri}))
			img, ok := out[0].(*llm.ImagePart)
			if !ok {
				t.Fatalf("out[0] = %T, want a shrunk *llm.ImagePart", out[0])
			}
			got := decodeDataURI(t, img.URI)

			b := got.Bounds()
			if b.Dx() > testMaxImageEdge || b.Dy() > testMaxImageEdge {
				t.Fatalf("result is %dx%d, still over the limit", b.Dx(), b.Dy())
			}
			if have := dominantCorner(t, got, 24, b.Dy()/2); have != "red" {
				t.Errorf("left half = %s, want red", have)
			}
			if have := dominantCorner(t, got, b.Dx()-1-24, b.Dy()/2); have != "blue" {
				t.Errorf("right half = %s, want blue", have)
			}
		})
	}
}

// TestShrinkImagesLeavesNestedUndecodableImageAlone is the ingest/send split
// applied to the nested shape. Media a tool returned lives inside
// ToolOutputPart.Output, and the rule is the same one level down: ingest must
// not substitute, send must.
func TestShrinkImagesLeavesNestedUndecodableImageAlone(t *testing.T) {
	fresh := func() (*llm.ToolOutputPart, llm.ContentPart) {
		nested := llm.ContentPart(&llm.ImagePart{URI: "data:image/webp;base64,AAAA"})
		return &llm.ToolOutputPart{
			ID:     "call-1",
			Output: []llm.ContentPart{&llm.TextPart{Text: "Read a.webp"}, nested},
		}, nested
	}

	tr, nested := fresh()
	stored, ok := llm.ShrinkImages([]llm.ContentPart{tr})[0].(*llm.ToolOutputPart)
	if !ok {
		t.Fatalf("ingest returned %T, want *llm.ToolOutputPart", stored)
	}
	if stored.Output[1] != nested {
		t.Fatalf("ingest replaced the nested image with %T", stored.Output[1])
	}

	tr2, _ := fresh()
	sent, ok := llm.FitImagesForSend([]llm.ContentPart{tr2})[0].(*llm.ToolOutputPart)
	if !ok {
		t.Fatalf("send returned %T, want *llm.ToolOutputPart", sent)
	}
	note, ok := sent.Output[1].(*llm.TextPart)
	if !ok {
		t.Fatalf("send left the nested image as %T, want the note", sent.Output[1])
	}
	if !strings.Contains(note.Text, "NOT delivered") {
		t.Errorf("note does not deny delivery: %s", note.Text)
	}
}

// TestSmallGIFIsStoredUntouched pins the most common GIF case, and the one the
// question "what lands in the session" turns on: a GIF at or under the limit is
// never re-encoded, so it is stored and sent as the GIF it was, animation and
// all. Only a GIF that really had to be shrunk becomes a PNG still.
func TestSmallGIFIsStoredUntouched(t *testing.T) {
	const w, h = 100, 100 // under the edge limit
	pal := color.Palette{color.RGBA{R: 255, A: 255}, color.RGBA{B: 255, A: 255}}
	frames := make([]*image.Paletted, 2)
	for i := range frames {
		f := image.NewPaletted(image.Rect(0, 0, w, h), pal)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				if x >= w/2 {
					f.SetColorIndex(x, y, 1) // the second frame's own palette entry
				}
			}
		}
		frames[i] = f
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, &gif.GIF{Image: frames, Delay: []int{10, 10}}); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	// Precondition: two frames, inside the limit — the shape the claim is about.
	if decoded, err := gif.DecodeAll(bytes.NewReader(buf.Bytes())); err != nil || len(decoded.Image) != 2 {
		t.Fatalf("fixture is not a two-frame gif: %v", err)
	}
	uri := "data:image/gif;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())

	for _, tc := range []struct {
		name string
		fit  func([]llm.ContentPart) []llm.ContentPart
	}{
		{"ingest", llm.ShrinkImages},
		{"send", llm.FitImagesForSend},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := []llm.ContentPart{&llm.ImagePart{URI: uri}}
			out := tc.fit(in)
			if out[0] != in[0] {
				t.Fatalf("a GIF inside the limit was rebuilt: %T %.30s", out[0], out[0].(*llm.ImagePart).URI)
			}
		})
	}
}
