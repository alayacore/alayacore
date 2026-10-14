# Raw IO Mode

`--rawio` runs AlayaCore in raw frame mode, where stdin and stdout carry
frames directly. Unlike `--plainio` which translates between human-readable
text and frames, rawio passes frames through unmodified.

This mode is designed for **programmatic control** — a parent process
(e.g., another AI agent, a script, or an orchestration tool) sends frames
to AlayaCore's stdin and reads frames from its stdout, using the wire
format directly.

## Wire Format

One frame per line:

```
[2-byte tag][value]\n
```

The tag is two ASCII letters. The value is a JSON object, except for the
control frames `UE` and `CE`, whose line is the tag alone. See the
[Adapter Guide](../adapter-guide/README.md) for the frame vocabulary, and
[Wire Format](internal/adapter-architecture.md#wire-format) for how it is
read and written.

A frame is a line, so a session's output is readable as text: `cat` shows the
whole stream, `grep '^SM'` picks out one plane of the vocabulary, and `cut -c3-`
drops the tag so `jq` can read the value. Nothing between the stream and you has
to parse it — the checking is the session's, on the way in (see Errors below).

## Input

- Raw bytes from stdin are piped directly to the session. The session's
  frame reader handles boundary detection internally.
- **Commands**: send a `CI` frame (`{"call_id":"...","name":"save","input":"..."}`)
  and receive exactly one `CO` frame back with the matching `call_id`. See
  [adapter-guide](../adapter-guide/README.md) for the full command table.
- **Ctrl-D** (EOF): closes stdin, the session finishes pending tasks, and
  the process exits with code `0`.
- **Ctrl-C** (SIGINT): terminates immediately with the default OS signal
  handling (exit code 130).

## Output

- All frames from the session are written directly to stdout with no
  formatting, interpretation, or filtering.
- The controlling process is responsible for parsing frames from stdout
  and handling any TagSystemMsg with type "error" itself.
- The session's lifecycle is reported by a TagSystemMsg of type `session`:
  `{"state":"ready"}` once, when the session is ready to accept prompts, and
  `{"state":"closed"}` once, as the last frame before the process exits — so a
  controlling process can wait for a frame instead of inferring the end from
  stdout EOF. See the
  [adapter guide](../adapter-guide/README.md#session-lifecycle-signal).
- Stderr is reserved for error messages, logging, and diagnostics.

## Errors

Rawio does not inspect or interpret frames. If the session encounters
an error, it emits a TagSystemMsg with type "error" on stdout — the
controlling process detects and handles it.

A malformed frame is one the format does not have: a line that is blank, that
does not begin with a two-letter tag, or whose value is not a JSON object. The
reader stops there rather than skipping it — the frames behind it are not read —
and reports what it found, so a writer that lost its place finds out instead of
having its input quietly discarded.

## Exit Codes

| Code | Meaning |
|------|---------|
| `0` | Normal exit — the session finished |
| `1` | Error — startup failure (config, no models) |

## Example

```sh
# Send a UX frame (one JSON content item) followed by UE (user message end)
printf 'UX{"type":"text","text":"hello"}\nUE\n' | alayacore --rawio
```

Frames come back as lines, so the shell reads them directly: `grep '^SM'` for one
plane of the vocabulary, or `cut -c3-` to drop a tag and read the value as JSON.

```sh
# Only the system messages
printf 'UX{"type":"text","text":"hello"}\nUE\n' | alayacore --rawio | grep '^SM'

# Every frame's value, as JSON
printf 'UX{"type":"text","text":"hello"}\nUE\n' | alayacore --rawio | cut -c3- | jq -c .
```

Or generate a request with images and watch what the session does with it:

```sh
# Generate a request with 2 images
go run misc/gen_wire_request.go \
  "What's in these images?" image1.jpg image2.jpg \
  | alayacore --rawio
```

## Use Cases

- **Orchestration**: A parent AI agent launches AlayaCore as a subprocess
  and communicates with it using frames.
- **Testing**: Send specific frame sequences to test session behavior.
- **Integration**: Connect AlayaCore to custom UIs, dashboards, or
  pipelines that speak the wire format.
