package llm

import "strings"

// GroupByRole groups consecutive ContentParts with the same role into chunks.
// Returns a slice of chunks, each chunk having a uniform role.
// This is a shared helper used by both Anthropic and OpenAI providers
// to convert the flat ContentPart list into per-role API messages.
func GroupByRole(contents []ContentPart) [][]ContentPart {
	if len(contents) == 0 {
		return nil
	}
	var chunks [][]ContentPart
	i := 0
	for i < len(contents) {
		role := contents[i].GetRole()
		j := i
		for j < len(contents) && contents[j].GetRole() == role {
			j++
		}
		chunks = append(chunks, contents[i:j])
		i = j
	}
	return chunks
}

// ParseDataURI parses a data URI into media type and base64 data.
// Input: "data:image/jpeg;base64,/9j/4AAQ..."
// Output: "image/jpeg", "/9j/4AAQ...", true
// Returns ok=false for non-data URIs (e.g. plain URLs).
func ParseDataURI(uri string) (mediaType, data string, ok bool) {
	const prefix = "data:"
	if !strings.HasPrefix(uri, prefix) {
		return "", "", false
	}
	rest := uri[len(prefix):]
	semi := strings.IndexByte(rest, ';')
	if semi == -1 {
		return "", "", false
	}
	mediaType = rest[:semi]
	rest = rest[semi+1:]
	const b64Prefix = "base64,"
	if !strings.HasPrefix(rest, b64Prefix) {
		return "", "", false
	}
	return mediaType, rest[len(b64Prefix):], true
}

// FetchableMediaURI reports whether a media part's URI is one an API server can
// retrieve on its own: a data: URI carries its own bytes, and http/https are the
// only remote schemes a server fetches. Every other shape — a bare filesystem
// path, a file: URI, an empty string — cannot be handed to a provider inside a
// content block, because the request carrying it fails whole rather than block
// by block; and since the part is persisted history, every later turn fails the
// same way. A caller that can degrade must degrade rather than forward.
//
// Both providers consult this before writing a native media block (the OpenAI
// promotion gate and the Anthropic media block); it is the one place the
// fetchable set is defined, so the two cannot disagree about it.
func FetchableMediaURI(uri string) bool {
	return strings.HasPrefix(uri, "data:") ||
		strings.HasPrefix(uri, "http://") ||
		strings.HasPrefix(uri, "https://")
}
