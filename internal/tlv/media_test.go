package tlv

import "testing"

// TestKindForPathLocal verifies local file paths map to the expected kinds.
func TestKindForPathLocal(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"photo.jpg", KindImage},
		{"photo.JPG", KindImage}, // case-insensitive
		{"photo.png", KindImage},
		{"photo.gif", KindImage},
		{"photo.webp", KindImage},
		{"photo.bmp", KindImage},
		{"photo.svg", KindImage},
		{"clip.mp4", KindVideo},
		{"clip.mov", KindVideo},
		{"song.mp3", KindAudio},
		{"song.wav", KindAudio},
		{"doc.pdf", KindDocument},
		{"archive.tar.gz", KindDocument}, // unknown → document
		{"noext", KindDocument},          // no extension → document
	}
	for _, tc := range cases {
		if got := KindForPath(tc.path); got != tc.want {
			t.Errorf("KindForPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// TestKindForPathURL verifies URLs are classified by their path extension,
// ignoring query strings and fragments — a URL like
// "https://example.com/a.jpg?x=1" must be an image, not a document.
func TestKindForPathURL(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://example.com/a.jpg", KindImage},
		{"https://example.com/a.jpg?x=1", KindImage},
		{"https://example.com/a.jpg?x=1&y=2", KindImage},
		{"https://example.com/a.jpg#fragment", KindImage},
		{"https://example.com/a.jpg?token=abc#frag", KindImage},
		{"https://example.com/dir/photo.PNG?raw=1", KindImage},
		{"https://example.com/clip.mp4?token=abc", KindVideo},
		{"https://example.com/song.wav?dl=1", KindAudio},
		{"https://example.com/doc.pdf#page=3", KindDocument},
		{"https://example.com/download?id=123", KindDocument}, // no extension → document
	}
	for _, tc := range cases {
		if got := KindForPath(tc.url); got != tc.want {
			t.Errorf("KindForPath(%q) = %q, want %q", tc.url, got, tc.want)
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
