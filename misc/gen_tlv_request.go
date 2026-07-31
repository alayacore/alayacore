//go:build ignore

package main

import (
	"encoding/base64"
	"fmt"
	"os"

	"github.com/alayacore/alayacore/internal/tlv"
)

// loadMedia reads a media file and returns its TLV tag and data URI.
func loadMedia(path string) (tag string, dataURI string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read %s: %w", path, err)
	}

	mime := tlv.MimeTypeForPath(path)
	b64 := base64.StdEncoding.EncodeToString(data)
	dataURI = fmt.Sprintf("data:%s;base64,%s", mime, b64)
	tag = tlv.TagForPath(path)

	return tag, dataURI, nil
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: go run misc/gen_tlv_request.go <prompt> <media1> [media2 ...]\n")
		os.Exit(1)
	}

	prompt := os.Args[1]

	for _, mediaPath := range os.Args[2:] {
		tag, dataURI, err := loadMedia(mediaPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		data, err := tlv.EncodeTLV(tag, dataURI)
		if err != nil {
			fmt.Fprintf(os.Stderr, "encode: %v\n", err)
			os.Exit(1)
		}
		if _, err := os.Stdout.Write(data); err != nil {
			fmt.Fprintf(os.Stderr, "write: %v\n", err)
			os.Exit(1)
		}
	}

	promptData, err := tlv.EncodeTLV(tlv.TagUserT, prompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "encode: %v\n", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(promptData); err != nil {
		fmt.Fprintf(os.Stderr, "write: %v\n", err)
		os.Exit(1)
	}

	// Flush with TagUserEnd to commit the user message.
	endData, err := tlv.EncodeTLV(tlv.TagUserEnd, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "encode: %v\n", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(endData); err != nil {
		fmt.Fprintf(os.Stderr, "write: %v\n", err)
		os.Exit(1)
	}
}
