//go:build ignore

// gen_tlv_request writes one complete user message to stdout as TLV: a UX frame
// per media file, a UX frame for the prompt, then UE to commit the message. It
// is meant for piping into `alayacore --rawio` (see docs/rawio.md); it is not
// part of the build.
//
// Usage: go run misc/gen_tlv_request.go <prompt> <media1> [media2 ...]
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// userContentJSON builds the body of a UX frame carrying one content item: the
// kind plus either the text or the URI.
func userContentJSON(kind, text, uri string) (string, error) {
	item := protocol.ContentItem{Type: kind}
	if kind == protocol.TypeText {
		item.Text = text
	} else {
		item.URI = uri
	}
	body, err := json.Marshal(protocol.ContentData{ContentItem: item})
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// writeFrame encodes one frame and writes it to stdout, or exits.
func writeFrame(tag, body string) {
	data, err := tlv.EncodeTLV(tag, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "encode: %v\n", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(data); err != nil {
		fmt.Fprintf(os.Stderr, "write: %v\n", err)
		os.Exit(1)
	}
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: go run misc/gen_tlv_request.go <prompt> <media1> [media2 ...]\n")
		os.Exit(1)
	}

	prompt := os.Args[1]

	for _, mediaPath := range os.Args[2:] {
		data, err := os.ReadFile(mediaPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		uri := fmt.Sprintf("data:%s;base64,%s",
			tlv.MimeTypeForPath(mediaPath), base64.StdEncoding.EncodeToString(data))
		body, err := userContentJSON(tlv.KindForPath(mediaPath), "", uri)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		writeFrame(tlv.TagUserX, body)
	}

	body, err := userContentJSON(protocol.TypeText, prompt, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	writeFrame(tlv.TagUserX, body)

	// Flush: everything staged above is one user message.
	writeFrame(tlv.TagUserEnd, "")
}
