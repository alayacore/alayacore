package providers

// The providers' SSE vocabulary.
//
// The reader is internal/sse, shared with the MCP HTTP transport. What stays here
// is the one thing that is this package's: what an oversized event means for a
// model.

import (
	"errors"
	"fmt"

	"github.com/alayacore/alayacore/internal/sse"
)

// maxEventBytes bounds one decoded SSE event — the data accumulated between
// blank lines.
//
// It is fixed, not derived from the configured max_tokens: this is the memory
// ceiling, and a ceiling a config value can raise is not a ceiling.
//
// 8MB is far above any real single task. Provider output limits are tens of
// thousands to ~128K tokens (around half a megabyte of text), and even a
// pathological, escape-heavy JSON encoding of one stays within a few MB, so no
// legitimate task is rejected — yet a misbehaving server is still bounded.
//
// NOTE: model output limits grow. A single task today tops out around half a
// megabyte of output (provider caps of ~128K tokens), but if a task can ever
// approach 1MB or more, revisit this constant — it must stay comfortably above
// what a provider may emit atomically. It tracks what providers can emit, and
// is not to be tightened to whatever is typical today.
const maxEventBytes = 8 << 20 // 8MB

// sseErr words the shared reader's size failure in this package's terms: from
// here, an event past the bound is a model that generated an oversized tool
// call, not a peer that framed its message oddly.
func sseErr(err error) error {
	if errors.Is(err, sse.ErrTooLarge) {
		return fmt.Errorf("SSE event exceeded %dMB — the model may have generated an oversized tool call", maxEventBytes>>20)
	}
	return err
}
