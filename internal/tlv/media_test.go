package tlv

import "testing"

// TestTagForPathLocal verifies local file paths map to the expected tags.
func TestTagForPathLocal(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"photo.jpg", TagUserI},
		{"photo.JPG", TagUserI}, // case-insensitive
		{"photo.png", TagUserI},
		{"photo.gif", TagUserI},
		{"photo.webp", TagUserI},
		{"photo.bmp", TagUserI},
		{"photo.svg", TagUserI},
		{"clip.mp4", TagUserV},
		{"clip.mov", TagUserV},
		{"song.mp3", TagUserA},
		{"song.wav", TagUserA},
		{"doc.pdf", TagUserD},
		{"archive.tar.gz", TagUserD}, // unknown → document
		{"noext", TagUserD},          // no extension → document
	}
	for _, tc := range cases {
		if got := TagForPath(tc.path); got != tc.want {
			t.Errorf("TagForPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// TestTagForPathURL verifies URLs are classified by their path extension,
// ignoring query strings and fragments — a URL like
// "https://example.com/a.jpg?x=1" must be an image (UI), not a document.
func TestTagForPathURL(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://example.com/a.jpg", TagUserI},
		{"https://example.com/a.jpg?x=1", TagUserI},
		{"https://example.com/a.jpg?x=1&y=2", TagUserI},
		{"https://example.com/a.jpg#fragment", TagUserI},
		{"https://example.com/a.jpg?token=abc#frag", TagUserI},
		{"https://example.com/dir/photo.PNG?raw=1", TagUserI},
		{"https://example.com/clip.mp4?token=abc", TagUserV},
		{"https://example.com/song.wav?dl=1", TagUserA},
		{"https://example.com/doc.pdf#page=3", TagUserD},
		{"https://example.com/download?id=123", TagUserD}, // no extension → document
	}
	for _, tc := range cases {
		if got := TagForPath(tc.url); got != tc.want {
			t.Errorf("TagForPath(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

// TestMimeTypeForPathURL verifies MIME detection ignores URL query strings.
func TestMimeTypeForPathURL(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"https://example.com/a.jpg?x=1", "image/jpeg"},
		{"https://example.com/a.png#frag", "image/png"},
		{"https://example.com/clip.mp4?token=abc", "video/mp4"},
		{"https://example.com/download?id=123", "application/octet-stream"},
	}
	for _, tc := range cases {
		if got := MimeTypeForPath(tc.path); got != tc.want {
			t.Errorf("MimeTypeForPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// TestMimeTypeForPathLocal verifies local file MIME detection still works.
func TestMimeTypeForPathLocal(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"photo.jpg", "image/jpeg"},
		{"photo.JPG", "image/jpeg"},
		{"photo.webp", "image/webp"},
		{"song.mp3", "audio/mpeg"},
		{"doc.pdf", "application/pdf"},
		{"unknown.xyz", "application/octet-stream"},
	}
	for _, tc := range cases {
		if got := MimeTypeForPath(tc.path); got != tc.want {
			t.Errorf("MimeTypeForPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}
