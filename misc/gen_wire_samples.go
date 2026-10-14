//go:build ignore

// gen_wire_samples is a ONE-OFF tool that re-framed the adapter-guide sample
// frames when the wire format moved to one frame per line: it read the samples in
// their v14 framing (a 2-byte tag, a 4-byte big-endian length, then the value) and
// wrote them as lines. The samples it produced are committed; re-running it needs
// those v14 originals, so it is kept for provenance rather than for use.
//
// The v14 framing is copied here rather than imported because nothing in the
// running program reads it any more.
//
// The value is passed through encoding/json's compactor, which elides
// insignificant whitespace and keeps everything else — key order included, so a
// sample that a test compares against a re-marshalled struct still matches.
// Compacting is what guarantees the written line is one line; a sample carrying a
// raw control character inside a JSON string fails here rather than being
// published as a frame no reader accepts.
//
// The v14 originals are no longer in the tree — the samples were rewritten in
// place when the format changed — so the source directory is an argument: point
// it at a checkout of the commit that still had them.
//
// Usage: go run ./misc/gen_wire_samples.go <directory of v14 .bin samples>
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// dstDir is where the samples live now, one frame per line per file.
const dstDir = "adapter-guide/wire-samples"

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./misc/gen_wire_samples.go <directory of v14 .bin samples>")
		os.Exit(2)
	}
	srcDir := os.Args[1]

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		panic(err)
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		panic(err)
	}

	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".bin" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(srcDir, e.Name()))
		if err != nil {
			panic(err)
		}
		frame, err := reFrame(raw)
		if err != nil {
			panic(fmt.Sprintf("%s: %v", e.Name(), err))
		}
		out := filepath.Join(dstDir, e.Name()[:len(e.Name())-len(".bin")]+".txt")
		if err := os.WriteFile(out, frame, 0o644); err != nil {
			panic(err)
		}
	}
}

// reFrame reads one v14 frame and returns it as one v15 line.
func reFrame(raw []byte) ([]byte, error) {
	if len(raw) < 6 {
		return nil, fmt.Errorf("shorter than a v14 frame header (%d bytes)", len(raw))
	}
	tag := raw[:2]
	length := uint64(binary.BigEndian.Uint32(raw[2:6]))
	if 6+length != uint64(len(raw)) {
		return nil, fmt.Errorf("frame says %d value bytes but the file holds %d", length, len(raw)-6)
	}

	// A control frame (UE, CE) carries no value: its line is the tag alone, and
	// there is nothing to compact.
	value := raw[6:]
	if len(value) > 0 {
		var compact bytes.Buffer
		if err := json.Compact(&compact, value); err != nil {
			return nil, fmt.Errorf("value %q is not JSON: %w", value, err)
		}
		value = compact.Bytes()
	}

	line := make([]byte, 0, len(tag)+len(value)+1)
	line = append(line, tag...)
	line = append(line, value...)
	return append(line, '\n'), nil
}
