package llm

// Image fitting: an image an endpoint would reject is shrunk so that it fits.
//
// Forwarding an oversized image is not a partial failure — a server that
// refuses it fails the entire request, and because the image is part of the
// persisted history every later request fails the same way, leaving the model
// no way to recover. Telling the model about it instead costs a whole extra
// round trip (the model must resize it and re-read) and repeats that cost on
// every turn in between. Shrinking removes both: the image goes out, the turn
// proceeds, and nothing is added to the model's context.
//
// There are two entry points, and the difference between them is the part that
// cannot be shrunk:
//
//   - ShrinkImages is the ingest form, used where a part is about to be
//     numbered, echoed and stored (agent.runTaskNormal for user input,
//     newToolOutput for tool output). It fits what it can and leaves everything
//     else exactly as it found it. It must not substitute a note: a stored note
//     would replace the user's own attachment in their session, and — because
//     the echo uses the fitted part — come back to the adapter as a wall of
//     user text instead of the attachment's label. The shrink is paid once
//     here, which is the whole point of doing it at ingest.
//   - FitImagesForSend is the send form, used by each provider's StreamMessages
//     on the way out. It also replaces an image it cannot decode with a note.
//     This is not redundant, and deleting it is a mistake: it is the only
//     implementation of the note, and it is the safety net that keeps a source
//     which forgot to fit from turning into a rejected request — such a source
//     degrades to paying the shrink on every send, which is slow, not broken.
//
// Neither mutates the parts it is handed, so a caller holding its own copy is
// not surprised.

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif" // registers the GIF decoder for image.Decode
	"image/jpeg"
	"image/png"
	"strings"
)

// maxImageEdge is the longest image side this client will hand to an endpoint.
// An image whose longest side exceeds it is shrunk to it before it is
// stored or sent.
//
// It is the largest value that is safe across the endpoints we know: GLM caps at
// 6000x6000, DeepSeek drops to 4096 px per side once a request carries 15 or
// more images, and Anthropic's many-image rule is stricter (2000) but only past
// 20 images. 4096 sits under all of them for the ordinary case.
//
// It is a constant, not configuration, on purpose. The gate runs afresh on
// every send, so a value that could change between runs would let what the
// model is shown now differ from what an already-answered turn was shown — the
// exact incoherence this design avoids by never writing the decision back. A
// session is rendered by the constant of the build that opens it.
const maxImageEdge = 4096

// maxDecodePixels bounds how many pixels this client will decode in order to
// shrink an image. It guards against a decompression bomb — a small file whose
// header declares enormous dimensions — not against ordinary large photos:
// read_file caps a media file at 16 MB, and 16 MB of JPEG is far below this.
//
// The bound is on pixels, and a decode holds several bytes per pixel at once
// (the decoder's own buffer plus the premultiplied copy), so the peak is a few
// hundred megabytes at the ceiling. It sits just above Anthropic's 8000x8000.
const maxDecodePixels = 64_000_000

// ShrinkImages is the ingest form: fit what can be fitted, and change nothing
// else. An image this client cannot decode is left exactly as it is — see the
// file header for why that substitution must not happen here.
func ShrinkImages(contents []ContentPart) []ContentPart {
	return fitImages(contents, false)
}

// FitImagesForSend is the send form: fit what can be fitted, and replace an
// image that cannot be decoded with a text note. It never mutates the parts it
// is handed and returns the input unchanged when nothing needs fitting.
func FitImagesForSend(contents []ContentPart) []ContentPart {
	return fitImages(contents, true)
}

// fitImages walks the parts — including the media nested inside a tool result —
// and returns a new slice in which every image that can be fitted has been.
// noteUnfit decides what happens to an image that cannot be: replaced by the
// note (send), or left alone (ingest).
func fitImages(contents []ContentPart, noteUnfit bool) []ContentPart {
	if !anyOversize(contents) {
		return contents
	}
	out := make([]ContentPart, len(contents))
	for i, p := range contents {
		if tr, ok := p.(*ToolOutputPart); ok {
			// A tool result's own slice has to be rebuilt too, or the nested
			// image would survive inside the copy.
			clone := *tr
			clone.Output = make([]ContentPart, len(tr.Output))
			for j, sub := range tr.Output {
				clone.Output[j] = fitPart(sub, noteUnfit)
			}
			out[i] = &clone
			continue
		}
		out[i] = fitPart(p, noteUnfit)
	}
	return out
}

// anyOversize reports whether any image, at the top level or nested inside a
// tool result, needs attention — that is, one that will not go out as it
// stands, whether because it is too big or because it cannot be decoded.
func anyOversize(contents []ContentPart) bool {
	for _, p := range contents {
		if oversizeImage(p) {
			return true
		}
		if tr, ok := p.(*ToolOutputPart); ok {
			for _, sub := range tr.Output {
				if oversizeImage(sub) {
					return true
				}
			}
		}
	}
	return false
}

// oversizeImage reports whether a part is an image this client must act on.
func oversizeImage(p ContentPart) bool {
	img, ok := p.(*ImagePart)
	return ok && !imageSendable(img.URI)
}

// fitPart returns the part as it should go out. An image that can be fitted is
// shrunk; one that cannot is replaced by the note only when noteUnfit is set,
// and kept as it is otherwise. Anything that is not an image is returned
// unchanged. A replacement keeps the original's role and history ID: the wire
// groups parts by role, and a nested part inside a tool result carries its role
// for the same reason it always did.
func fitPart(p ContentPart, noteUnfit bool) ContentPart {
	img, ok := p.(*ImagePart)
	if !ok || imageSendable(img.URI) {
		return p
	}
	if uri, shrunk := shrinkDataURI(img.URI); shrunk {
		if uri == img.URI {
			return p // the decoder handed back something already within the limit
		}
		// The whole embedded meta is copied, not its fields: a field list here
		// would silently drop whatever ContentPartMeta gains next.
		return &ImagePart{URI: uri, ContentPartMeta: img.ContentPartMeta}
	}
	if !noteUnfit {
		// Ingest. The note is a substitution of a different kind, so it is the
		// send gate's to make, per request, where nothing is written down.
		return p
	}
	return &TextPart{Text: unsendableImageNote(img.URI), ContentPartMeta: img.ContentPartMeta}
}

// imageSendable reports whether an ImagePart may be handed to a provider
// unchanged.
//
// A data URI carries its own bytes, so it is measured from them: within the
// limit it goes out as-is, and over the limit — or in a format this client
// cannot measure (webp, heic) — it is handed to shrinkDataURI, which either
// fits it or fails, and the caller then applies its own form's rule.
//
// A URI that is not a data URI is left alone. A remote URL is the server's to
// fetch, so its size is the server's to bound; the remaining shapes a URI can
// take (a bare path, file:, empty) are already degraded by each protocol's own
// handling — see openaiPromotableURI.
func imageSendable(uri string) bool {
	_, data, isData := ParseDataURI(uri)
	if !isData {
		return true
	}
	w, h, measured := imageDimensions(data)
	return measured && w <= maxImageEdge && h <= maxImageEdge
}

// imageDimensions reads width and height from a base64 image body without
// decoding any pixels: image.DecodeConfig consumes only the header it needs, so
// the image is never materialized. Only the formats the standard library
// registers (jpeg, png, gif) are measurable; anything else — webp, heic, or a
// body that is not a valid image at all — reports false.
func imageDimensions(b64 string) (int, int, bool) {
	cfg, _, err := image.DecodeConfig(base64.NewDecoder(base64.StdEncoding, strings.NewReader(b64)))
	if err != nil {
		return 0, 0, false
	}
	return cfg.Width, cfg.Height, true
}

// shrinkDataURI decodes a data URI image, scales it so neither side exceeds
// maxImageEdge, and returns a data URI in the same format (JPEG re-encodes as
// JPEG; everything else becomes PNG, which is lossless). It reports false when
// the image cannot be decoded or is too large to decode safely; what the caller
// does then depends on which form it is — the send form falls back to the note,
// the ingest form keeps the part (see the file header).
//
// JPEG re-encoding is lossy, so this runs only on images the endpoint would
// otherwise reject.
func shrinkDataURI(uri string) (string, bool) {
	mimeType, b64, ok := ParseDataURI(uri)
	if !ok {
		return "", false
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", false
	}
	// Measure from the header before decoding a single pixel: maxDecodePixels
	// exists to refuse a decompression bomb, which only helps if the refusal
	// happens before the decode.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return "", false
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxDecodePixels {
		return "", false
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", false
	}
	// A decoder can disagree with its own header: a GIF reports the logical
	// screen to DecodeConfig but hands Decode only the first frame, so an image
	// the header called oversized can arrive here already small. Re-encoding it
	// would either upscale it (never) or convert its format for nothing, so it
	// goes back untouched and the caller keeps the original part.
	if b := src.Bounds(); b.Dx() <= maxImageEdge && b.Dy() <= maxImageEdge {
		return uri, true
	}
	premul := premultipliedRGBA(src)
	// The standard library's decoders already reject a non-positive dimension
	// (verified for png; gif and jpeg do the same), so this is belt to that
	// brace: fitWithin divides by the source size, and boxDownscale indexes
	// pixels, so a degenerate image must never reach them.
	if premul.Rect.Dx() <= 0 || premul.Rect.Dy() <= 0 {
		return "", false
	}
	dstW, dstH := fitWithin(premul.Rect.Dx(), premul.Rect.Dy(), maxImageEdge)
	return encodeShrunk(boxDownscale(premul, dstW, dstH), mimeType, jpegMetadata(raw))
}

// fitWithin returns the largest size, never larger than the source, that fits
// inside maxEdge on both sides with the aspect ratio preserved. The "never
// larger" half is enforced rather than assumed: a caller that reaches this with
// an image already inside the limit must get that image back, not a blown-up
// version of it.
func fitWithin(srcW, srcH, maxEdge int) (int, int) {
	if srcW <= maxEdge && srcH <= maxEdge {
		return srcW, srcH
	}
	if srcW >= srcH {
		h := srcH * maxEdge / srcW
		if h < 1 {
			h = 1
		}
		return maxEdge, h
	}
	w := srcW * maxEdge / srcH
	if w < 1 {
		w = 1
	}
	return w, maxEdge
}

// encodeShrunk encodes a scaled image as a data URI, keeping the source format
// where the standard library can produce it. Anything that is not JPEG becomes
// PNG, which is lossless: a re-encode cannot degrade a screenshot's text the
// way JPEG would.
//
// A GIF arrives here as its first frame — image.Decode yields one frame, not a
// sequence — so an oversized animated GIF becomes a still. That is not a loss
// the endpoints share: Anthropic documents that it uses only the first frame of
// an animation, and no endpoint in the set turns an image block into a frame
// sequence the model can step through. (Kimi may decode an animated GIF as
// video and bill it as one; a still is cheaper there, not worse.) A GIF at or
// under the limit is never re-encoded and keeps its animation.
func encodeShrunk(img image.Image, srcMIME string, meta [][]byte) (string, bool) {
	var buf bytes.Buffer
	if srcMIME == "image/jpeg" {
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
			return "", false
		}
		encoded := buf.Bytes()
		out := spliceJPEGMetadata(encoded, meta)
		return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(out), true
	}
	if err := png.Encode(&buf, img); err != nil {
		return "", false
	}
	// PNG output carries no APP segments; only a JPEG source has any to keep.
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), true
}

// jpegMetadata returns the APP1 and APP2 segments of a JPEG verbatim, so a
// re-encode can carry them through: APP1 holds EXIF (including the orientation
// tag a phone writes instead of rotating its pixels) and XMP, APP2 holds the
// ICC color profile. Nothing is interpreted — the segments are bytes, and the
// consumer that honored them before the re-encode honors them after it.
//
// Only the header is walked, and only as far as the first SOS (where compressed
// data starts). A malformed or truncated file yields fewer segments, never an
// error: metadata is a nicety, and losing it must not cost the image.
func jpegMetadata(raw []byte) [][]byte {
	if len(raw) < 2 || raw[0] != 0xFF || raw[1] != 0xD8 {
		return nil
	}
	var out [][]byte
	for i := 2; i+1 < len(raw) && raw[i] == 0xFF; {
		marker := raw[i+1]
		if marker == 0xFF { // fill byte before a marker
			i++
			continue
		}
		if marker == 0xDA || marker == 0xD9 { // start of scan / end of image
			break
		}
		if i+4 > len(raw) {
			break
		}
		size := int(raw[i+2])<<8 | int(raw[i+3])
		if size < 2 || i+2+size > len(raw) {
			break
		}
		if marker == 0xE1 || marker == 0xE2 {
			out = append(out, raw[i:i+2+size])
		}
		i += 2 + size
	}
	return out
}

// spliceJPEGMetadata writes segments immediately after the SOI of an encoded
// JPEG, replacing nothing: the encoder emits no APP segments of its own, so the
// source's can simply go in front of its first marker.
func spliceJPEGMetadata(encoded []byte, segments [][]byte) []byte {
	if len(segments) == 0 || len(encoded) < 2 || encoded[0] != 0xFF || encoded[1] != 0xD8 {
		return encoded
	}
	n := 2
	for _, seg := range segments {
		n += len(seg)
	}
	out := make([]byte, 0, n+len(encoded)-2)
	out = append(out, 0xFF, 0xD8)
	for _, seg := range segments {
		out = append(out, seg...)
	}
	return append(out, encoded[2:]...)
}

// premultipliedRGBA returns src as an *image.RGBA of 8-bit alpha-premultiplied
// samples, so the resampling loop can index bytes instead of going through the
// image.Image interface once per source pixel — which is what makes the
// difference between roughly a second and a fifth of one on a 27-megapixel
// photo. image/draw does the conversion for every concrete type and every
// sub-image offset; the source is returned untouched when it is already that
// exact shape.
func premultipliedRGBA(src image.Image) *image.RGBA {
	if r, ok := src.(*image.RGBA); ok && r.Rect.Min == (image.Point{}) && r.Stride == 4*r.Rect.Dx() {
		return r
	}
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return dst
}

// boxDownscale reduces src to dstW x dstH by averaging each destination pixel's
// source rectangle — an area (box) filter. Callers only ever shrink, so the box
// is never empty and the clamp below never fires; it is kept because an empty
// box would make the average divide by zero, and a guard on a division is worth
// more than the branch it costs.
func boxDownscale(src *image.RGBA, dstW, dstH int) *image.RGBA {
	srcW, srcH := src.Rect.Dx(), src.Rect.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	pix := src.Pix

	for dy := 0; dy < dstH; dy++ {
		y0 := dy * srcH / dstH
		y1 := (dy + 1) * srcH / dstH
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for dx := 0; dx < dstW; dx++ {
			x0 := dx * srcW / dstW
			x1 := (dx + 1) * srcW / dstW
			if x1 <= x0 {
				x1 = x0 + 1
			}

			var sumR, sumG, sumB, sumA, count uint64
			for y := y0; y < y1; y++ {
				row := y * src.Stride
				for x := x0; x < x1; x++ {
					i := row + x*4
					sumR += uint64(pix[i])
					sumG += uint64(pix[i+1])
					sumB += uint64(pix[i+2])
					sumA += uint64(pix[i+3])
					count++
				}
			}
			// Averaging premultiplied samples is the premultiplied average, which
			// is exactly what image.RGBA stores.
			dst.SetRGBA(dx, dy, color.RGBA{
				R: premul8(sumR / count),
				G: premul8(sumG / count),
				B: premul8(sumB / count),
				A: premul8(sumA / count),
			})
		}
	}
	return dst
}

// premul8 narrows an averaged channel to the byte image.RGBA holds. The samples
// boxDownscale sums are the 8-bit bytes of an *image.RGBA (not the 16-bit values
// image.Image.RGBA returns), so their average is at most 255 and the conversion
// cannot overflow. There is no shift here for that reason: a shift would be
// right only for 16-bit samples, and applying it to bytes produces black.
func premul8(avg uint64) uint8 {
	return uint8(avg) //nolint:gosec // G115: avg is an average of 8-bit samples, so avg <= 0xff
}

// unsendableImageNote is the text that stands in for an image this client could
// not forward at all.
//
// The wording is load-bearing, exactly as in the audio/video degradation: a
// terse "[image too large]" reads to a model as "a result came back", and it
// will describe media it never received. The note therefore states flatly that
// nothing was delivered and names the workaround that does work. The base64
// payload is deliberately absent — it is usually the largest thing in the
// conversation, and echoing it would bill every following turn for bytes no
// model can use.
func unsendableImageNote(uri string) string {
	mediaType, data, _ := ParseDataURI(uri)
	_, _, measured := imageDimensions(data)

	desc := "image"
	if mediaType != "" {
		desc += " (" + mediaType + ")"
	}

	reason := "this client could not shrink it"
	if !measured {
		reason = "this client cannot read its dimensions (unsupported image format)"
	}

	return fmt.Sprintf(
		"[Unreadable %s: %s, so the content was NOT delivered to you and you have not perceived it. "+
			"Do not describe or quote it. To inspect it, convert it to JPEG or PNG at %d px or less "+
			"(e.g. with an image tool via execute_command), then read or attach it again.]",
		desc, reason, maxImageEdge,
	)
}
