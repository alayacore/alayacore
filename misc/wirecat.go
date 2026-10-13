//go:build ignore

// wirecat reads frames from stdin and prints one "tag<TAB>value" line per frame.
//
// It exists to inspect what a session wrote to a rawio stream (see docs/rawio.md).
// A frame is already a line, so this is close to grep — what it adds is that every
// frame goes through the same decoder the core uses, so a frame the format does
// not allow is reported here rather than read past.
//
// Usage: printf 'UX{"type":"text","text":"hi"}\nUE\n' | alayacore --rawio | go run ./misc/wirecat.go
package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/alayacore/alayacore/internal/wire"
)

func main() {
	frames := wire.NewReader(os.Stdin)
	for {
		tag, value, err := frames.ReadFrame()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if _, err := fmt.Printf("%s\t%s\n", tag, value); err != nil {
			fmt.Fprintf(os.Stderr, "write error: %v\n", err)
			os.Exit(1)
		}
	}
}
