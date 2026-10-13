package wire

import (
	"path/filepath"
	"strings"
)

// Content kinds carried by a "type" field inside a content item (UX / AX /
// UF.output). The protocol package aliases these; the values are the wire
// vocabulary, so they must stay stable.
const (
	KindImage    = "image"
	KindVideo    = "video"
	KindAudio    = "audio"
	KindDocument = "document"
)

var mimeMap = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".png": "image/png", ".gif": "image/gif",
	".webp": "image/webp", ".bmp": "image/bmp",
	".svg": "image/svg+xml",
	".mp4": "video/mp4", ".mpeg": "video/mpeg", ".mpg": "video/mpeg",
	".avi": "video/x-msvideo", ".mov": "video/quicktime",
	".webm": "video/webm", ".mkv": "video/x-matroska",
	".mp3": "audio/mpeg", ".wav": "audio/wav",
	".ogg": "audio/ogg", ".flac": "audio/flac",
	".aac": "audio/aac", ".m4a": "audio/mp4",
	".wma": "audio/x-ms-wma",
	".pdf": "application/pdf",
	".txt": "text/plain", ".md": "text/plain",
}

// extOf returns the lowercase file extension of a local path or URL.
// URL query strings and fragments are stripped first, so that
// "https://example.com/a.jpg?x=1" yields ".jpg" instead of ".jpg?x=1" —
// the latter would not match mimeMap and misclassify the attachment as a
// document instead of an image. Harmless for local paths.
func extOf(pathOrURL string) string {
	if q := strings.IndexAny(pathOrURL, "?#"); q >= 0 {
		pathOrURL = pathOrURL[:q]
	}
	return strings.ToLower(filepath.Ext(pathOrURL))
}

// MimeTypeForPath returns the MIME type for a file based on its extension.
func MimeTypeForPath(path string) string {
	if mime, ok := mimeMap[extOf(path)]; ok {
		return mime
	}
	return "application/octet-stream"
}

// KindForPath returns the content kind ("image", "video", "audio", or
// "document") for a file, based on its extension. Unknown extensions are
// documents — the protocol's catch-all for a payload it does not model
// specifically.
func KindForPath(path string) string {
	switch extOf(path) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".svg":
		return KindImage
	case ".mp4", ".mpeg", ".mpg", ".avi", ".mov", ".webm", ".mkv":
		return KindVideo
	case ".mp3", ".wav", ".ogg", ".flac", ".aac", ".m4a", ".wma":
		return KindAudio
	default:
		return KindDocument
	}
}

// MediaLabel returns the display label for a content kind.
//
// The label is display text for an adapter (only the terminal one uses it
// today), not protocol content: keep it to single-codepoint emoji, per the
// glyph policy in internal/adapters/terminal/constants.go.
func MediaLabel(kind string) string {
	switch kind {
	case KindImage:
		return "📷 Image"
	case KindVideo:
		return "🎬 Video"
	case KindAudio:
		return "🎵 Audio"
	case KindDocument:
		return "📄 Document"
	}
	return ""
}
