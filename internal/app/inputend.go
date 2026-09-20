package app

import (
	"io"

	"github.com/alayacore/alayacore/internal/tlv"
)

// SendInputEnd tells the session that the adapter has no more input, without
// closing the stream: the same fact EOF carries, on a pipe that stays usable.
//
// That difference is the whole point. An adapter that closed its write end at
// EOF would also end the command path, and a command may still have to reach
// the session after the input is exhausted — a running OAuth callback submits
// its :mcp_confirm through this same writer, and the session holds a prompt that
// arrived before it was ready instead of refusing it.
//
// The error is deliberately dropped: a failure here means the session is gone or
// the pipe is broken, which the adapter cannot act on and would only report
// twice — Session.Done() and the exit code already cover what it can observe.
func SendInputEnd(w io.Writer) {
	_ = tlv.WriteTLV(w, tlv.TagInputEnd, "")
}
