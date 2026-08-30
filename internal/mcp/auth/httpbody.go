package auth

import (
	"io"
)

// maxResponseBytes bounds how much of an OAuth/AS response body is read.
//
// Metadata documents and token responses are a few kilobytes; the previous
// io.ReadAll had no cap at all, so a hostile or misconfigured authorization
// server (or a captive portal hijacking the request) could make alayacore
// allocate as much memory as it liked while discovering endpoints — before any
// user interaction had trusted that server. The cap costs nothing legitimate.
const maxResponseBytes = 1 << 20 // 1MB

// readCapped reads at most maxResponseBytes from r. A body larger than the cap
// is cut off, which makes the JSON decode fail with a parse error rather than
// letting a server allocate unbounded memory.
func readCapped(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	return body, nil
}

// snippet renders a bounded excerpt of a response body for an error message.
// Bodies are attacker-influenced and are surfaced to both the terminal and the
// model, so an error must not carry megabytes of it.
func snippet(body []byte) string {
	const maxSnippet = 512
	if len(body) <= maxSnippet {
		return string(body)
	}
	return string(body[:maxSnippet]) + "…[truncated]"
}
