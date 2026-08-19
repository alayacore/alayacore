package tlv

import (
	"path/filepath"
	"strings"
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
// document (UD) instead of an image (UI). Harmless for local paths.
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

// TagForPath returns the TLV tag (UI, UV, UA, or UD) for a file based on its extension.
func TagForPath(path string) string {
	switch extOf(path) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".svg":
		return TagUserI
	case ".mp4", ".mpeg", ".mpg", ".avi", ".mov", ".webm", ".mkv":
		return TagUserV
	case ".mp3", ".wav", ".ogg", ".flac", ".aac", ".m4a", ".wma":
		return TagUserA
	default:
		return TagUserD
	}
}

// MediaLabel returns the display label for a media tag.
//
// Use only single-codepoint emoji (see package doc.go for details).
func MediaLabel(tag string) string {
	switch tag {
	case TagUserI:
		return "📷 Image"
	case TagUserV:
		return "🎬 Video"
	case TagUserA:
		return "🎵 Audio"
	case TagUserD:
		return "📄 Document"
	}
	return ""
}
