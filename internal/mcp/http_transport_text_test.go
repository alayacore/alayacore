package mcp

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// TestReadTextResponseRejectsOversizedBody: a text/plain body past the cap is
// an error naming the limit, not the first 1MB of it presented as the message.
func TestReadTextResponseRejectsOversizedBody(t *testing.T) {
	tr := &HTTPTransport{}
	body := io.NopCloser(strings.NewReader(strings.Repeat("a", maxTextResponseBytes+1)))

	_, err := tr.readTextResponse(body)
	if err == nil {
		t.Fatal("oversized text/plain body was accepted")
	}
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error = %T %v, want *RPCError", err, err)
	}
	if !strings.Contains(rpcErr.Message, "exceeds") {
		t.Errorf("message = %q, want it to name the size limit", rpcErr.Message)
	}
}
