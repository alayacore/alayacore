package terseio

import (
	"io"
	"strings"

	"github.com/alayacore/alayacore/internal/tlv"
)

// readAllPrompt reads the entire reader as a single prompt and emits it
// as one UT + UE pair. Trailing newlines are trimmed (a prompt piped from
// echo/printf or a file usually ends with "\n"). Empty input emits nothing.
func readAllPrompt(input io.Writer, reader io.Reader) error {
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	text := strings.TrimRight(string(data), "\r\n")
	if text == "" {
		return nil
	}
	if err := tlv.WriteTLV(input, tlv.TagUserT, text); err != nil {
		return err
	}
	return tlv.WriteTLV(input, tlv.TagUserEnd, "")
}
