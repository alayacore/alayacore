package auth

import (
	"fmt"
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
// is rejected with an explicit error: silently cutting it off would surface as
// a JSON parse error, which misattributes the cause.
func readCapped(r io.Reader) ([]byte, error) {
	// Read one byte past the cap: a body at exactly the limit is fine, and a
	// longer one is reported as too large instead of being cut off and
	// resurfacing later as a confusing JSON parse error.
	body, err := io.ReadAll(io.LimitReader(r, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("response body exceeds %d bytes", maxResponseBytes)
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
