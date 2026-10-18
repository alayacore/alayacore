package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/wire"
)

// Writer sends a command as a CI frame and remembers the name it went out under,
// because the CO that answers it carries only the id.
//
// It is the other half of RenderResult: an adapter writes a request here and
// renders the answer there, and both use this package's vocabulary rather than a
// spelling of their own.
//
// It exists because all three adapters wrote this by hand — the same
// split-canonicalize-number-store-write sequence, one per package, each with its
// own id counter and its own id→name table. Nothing tied the copies together, so
// a change to one (a different split rule, a new id prefix) left the other two
// behind. What genuinely differs between adapters is the prefix on the id, which
// is what a frame log needs to say which adapter sent a command.
//
// One instance per adapter package, created beside the code that sends: there is
// one adapter per process, so the counter and the table are process-wide facts,
// and ids cannot collide across adapters because the prefixes differ.
type Writer struct {
	prefix string
	seq    atomic.Uint64 // ids are handed out once, from the sending goroutine
	mu     sync.Mutex    // names are read back by the output goroutine
	names  map[string]string
}

// NewWriter returns a Writer whose command ids read "<prefix>-<n>".
func NewWriter(prefix string) *Writer {
	return &Writer{prefix: prefix, names: make(map[string]string)}
}

// Write sends cmd as a CI frame and returns the id it went out under.
//
// cmd is the human-facing text a user typed or an adapter built — ":save /tmp/x"
// or "save /tmp/x", the colon being presentation. It is split and canonicalized
// here, so every adapter speaks the vocabulary Defs declares no matter what was
// typed: a short form (":q") is an adapter's word and never a wire name.
func (w *Writer) Write(dst io.Writer, cmd string) (string, error) {
	name, args := SplitCommand(strings.TrimPrefix(cmd, ":"))
	name = Canonical(name)
	id := fmt.Sprintf("%s-%d", w.prefix, w.seq.Add(1))

	payload, err := json.Marshal(protocol.CmdMsg{CommandID: id, Name: name, Args: args})
	if err != nil {
		return "", err
	}

	// Recorded before the frame is written: the name must be in the table by the
	// time a CO can arrive, and a CO cannot arrive before its CI does.
	w.mu.Lock()
	w.names[id] = name
	w.mu.Unlock()

	if err := wire.WriteFrame(dst, wire.TagCommandIn, string(payload)); err != nil {
		// No CO can answer a frame that never reached the stream, so the entry
		// would only wait for nothing.
		w.Name(id)
		return id, err
	}
	return id, nil
}

// Name returns the command name id was issued for, and forgets it: the CO that
// carries the id is its only reader, and it arrives once.
//
// ok is false for an id this writer did not issue, or one already read — a second
// CO for the same request, or a result answering some other adapter's frame.
func (w *Writer) Name(id string) (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	name, ok := w.names[id]
	delete(w.names, id)
	return name, ok
}
