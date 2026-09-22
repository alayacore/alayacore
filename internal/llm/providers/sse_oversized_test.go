package providers_test

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

// A provider that puts an entire oversized payload on one SSE line (a huge
// tool call) exceeds the reader's 1MB line buffer. Both scanners must report
// that clearly rather than surfacing the raw "bufio.Scanner: token too long",
// which names an internal mechanism instead of the provider behavior.
func TestOversizedSSELineIsNamed(t *testing.T) {
	oversized := strings.Repeat("x", 2<<20) // 2MB, past the 1MB line buffer

	tests := []struct {
		name   string
		stream func(*testing.T, func(io.Writer)) ([]llm.ContentPart, error)
		body   func(io.Writer)
	}{
		{
			name:   "openai",
			stream: streamOpenAI,
			body: func(w io.Writer) {
				fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", oversized)
			},
		},
		{
			name:   "anthropic",
			stream: streamAnthropic,
			body: func(w io.Writer) {
				fmt.Fprintf(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%q}}\n\n", oversized)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.stream(t, tc.body)
			if err == nil {
				t.Fatal("an over-long SSE line must not report success")
			}
			if !strings.Contains(err.Error(), "SSE line exceeded") {
				t.Errorf("error should name the oversized SSE line, got: %v", err)
			}
		})
	}
}
