package tools

import (
	"bytes"
	"io"
	"os"
)

// capture collects one output stream (a tool's stdout or stderr) with bounded
// memory, for commands that can produce more output than the process should
// ever hold.
//
// Tool output used to be accumulated in a bytes.Buffer and only then measured
// against maxCommandOutput, so the cap decided what the *model* saw but not
// what the *process* allocated: a command streaming 286MB peaked at ~1.1GB of
// heap to deliver a 126-byte answer, and with the default unlimited timeout a
// non-terminating producer (`yes`) grew until the OOM killer took the process
// down — losing the whole session.
//
// capture keeps the first maxBytes in memory and transparently spills the
// remainder to a file in the process temp dir, so memory is bounded by
// maxBytes plus one write while the complete output is still preserved for
// read_file. Staying in memory is the common case (almost all tool output is
// small), so the spill costs nothing until it is needed.
//
// It is not safe for two concurrent writers. exec.Cmd starts one copying
// goroutine per stream and stdout and stderr get separate captures, which is
// why that is fine here.
type capture struct {
	maxBytes int

	mem       bytes.Buffer // first maxBytes bytes
	spill     *os.File     // remainder, once the memory budget is exhausted
	spillPath string       // spill file name, removed on Close
	spillErr  error        // first spill failure: output is truncated, never grown

	total    int64 // bytes ever written
	lines    int64 // newline count, kept live so totals need no materialization
	lastByte byte  // last byte seen, to know whether a final line is unterminated
}

// newCapture creates a capture that holds up to maxBytes in memory before
// spilling to disk.
func newCapture(maxBytes int) *capture {
	return &capture{maxBytes: maxBytes}
}

var _ io.Writer = (*capture)(nil)

// Write implements io.Writer. It never reports failure: output that cannot be
// spilled to disk is truncated and flagged, because failing a tool over a
// temp file would be worse than a shorter log.
func (c *capture) Write(p []byte) (int, error) {
	n := len(p)
	c.total += int64(n)
	c.lines += int64(bytes.Count(p, newlineBytes))
	if n > 0 {
		c.lastByte = p[n-1]
	}

	if c.spill != nil {
		if _, err := c.spill.Write(p); err != nil && c.spillErr == nil {
			c.spillErr = err // recorded, not returned: see the doc comment above
		}
		return n, nil //nolint:nilerr // a spool failure must not fail a tool that ran fine
	}

	if c.spillErr != nil {
		// Opening the spill already failed once; keep dropping the excess
		// instead of retrying the filesystem on every write for a stream that
		// may never end.
		return n, nil //nolint:nilerr // spillErr is a recorded fact, not this write's error
	}

	if c.total <= int64(c.maxBytes) {
		c.mem.Write(p)
		return n, nil
	}

	// Budget crossed: fill the in-memory prefix, then stream the rest to disk.
	if room := c.maxBytes - c.mem.Len(); room > 0 {
		if room > n {
			room = n
		}
		c.mem.Write(p[:room])
		p = p[room:]
	}
	f, err := createProcTmpFile("tool-output-*.tmp")
	if err != nil {
		c.spillErr = err
		// Excess dropped; the caller reports the truncation instead.
		return n, nil //nolint:nilerr // a spool failure must not fail a tool that ran fine
	}
	c.spill, c.spillPath = f, f.Name()

	if _, err := c.spill.Write(p); err != nil && c.spillErr == nil {
		c.spillErr = err
	}
	return n, nil
}

var newlineBytes = []byte("\n")

// size returns the total number of bytes written.
func (c *capture) size() int64 { return c.total }

// lineTotal returns the number of lines a human would count: completed lines
// plus one when the stream ends without a trailing newline. Computed live so
// huge spilled output never has to be materialized to be described.
func (c *capture) lineTotal() int64 {
	if c.total > 0 && c.lastByte != '\n' {
		return c.lines + 1
	}
	return c.lines
}

// spilled reports whether the stream outgrew the in-memory budget.
func (c *capture) spilled() bool { return c.spill != nil || c.spillErr != nil }

// truncated reports whether bytes were dropped because the spill could not be
// opened or written.
func (c *capture) truncated() bool { return c.spillErr != nil }

// String returns the in-memory prefix. It is the complete content only when
// spilled() is false; callers branch on that first.
func (c *capture) String() string { return c.mem.String() }

// prefix returns the in-memory part of the stream, marked when it is only a
// part. Use it where a truncated excerpt is acceptable (an error message) and
// writeOut where the whole stream must be preserved.
func (c *capture) prefix() string {
	if !c.spilled() {
		return c.mem.String()
	}
	return c.mem.String() + "\n[…output continued…]"
}

// emit copies the stream to w — the whole thing when includeSpill, otherwise
// only the in-memory part. The narrow form exists because the inline rendering
// must never pull a spilled stream back into RAM.
func (c *capture) emit(w io.Writer, includeSpill bool) error {
	if !includeSpill {
		_, err := w.Write(c.mem.Bytes())
		return err
	}
	return c.writeOut(w)
}

// writeOut copies the whole captured stream — in-memory prefix followed by the
// spilled remainder — to w without holding it all in memory at once.
func (c *capture) writeOut(w io.Writer) error {
	if _, err := w.Write(c.mem.Bytes()); err != nil {
		return err
	}
	if c.spill == nil {
		return nil
	}
	if _, err := c.spill.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := io.Copy(w, c.spill); err != nil {
		return err
	}
	// Rewind to the end so a later write would not clobber what we stored.
	_, err := c.spill.Seek(0, io.SeekEnd)
	return err
}

// Close releases the spill descriptor and removes the spill file. Only the
// result file the caller saves (and tells the model about) is meant to
// outlive the tool call; a long session running several huge commands must not
// leave the scratch copies behind.
func (c *capture) Close() {
	if c.spill != nil {
		_ = c.spill.Close()
		c.spill = nil
	}
	if c.spillPath != "" {
		_ = os.Remove(c.spillPath) // after Close: works on Windows too
		c.spillPath = ""
	}
}
