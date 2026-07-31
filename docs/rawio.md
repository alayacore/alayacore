# Raw IO Mode

`--rawio` runs AlayaCore in raw TLV mode, where stdin and stdout carry
TLV-encoded messages directly. Unlike `--plainio` which translates between
human-readable text and TLV, rawio passes TLV frames through unmodified.

This mode is designed for **programmatic control** — a parent process
(e.g., another AI agent, a script, or an orchestration tool) sends TLV
frames to AlayaCore's stdin and reads TLV frames from its stdout, using
the TLV protocol directly.

## TLV Protocol

Messages use the standard Tag-Length-Value format:

```
[2-byte tag][4-byte length (big-endian)][value bytes]
```

See [TLV Protocol](internal/adapter-architecture.md#tlv-protocol) for full details.

## Input

- Raw bytes from stdin are piped directly to the session. The session's
  `io.ReadFull` handles TLV frame boundary detection internally.
- **Commands**: send a `CI` frame (`{"id":"...","name":"save","input":"..."}`)
  and receive exactly one `CO` frame back with the matching `id`. See
  [adapter-guide](../adapter-guide/README.md) for the full command table.
- **Ctrl-D** (EOF): closes stdin, the session finishes pending tasks, and
  the process exits with code `0`.
- **Ctrl-C** (SIGINT): terminates immediately with the default OS signal
  handling (exit code 130).

## Output

- All TLV-encoded messages from the session are written directly to
  stdout with no formatting, interpretation, or filtering.
- The controlling process is responsible for parsing TLV frames from
  stdout and handling any TagSystemMsg with type "error" itself.
- Stderr is reserved for error messages, logging, and diagnostics.

## Errors

Rawio does not inspect or interpret TLV frames. If the session encounters
an error, it emits a TagSystemMsg with type "error" on stdout — the
controlling process detects and handles it.

## Exit Codes

| Code | Meaning |
|------|---------|
| `0` | Normal exit — all tasks completed successfully |
| `1` | Error — startup failure (config, no models) or task error during execution |

## Example

```sh
# Send a UT (user text) frame followed by UE (user message end) to AlayaCore
printf 'UT\x00\x00\x00\x05helloUE\x00\x00\x00\x00' | alayacore --rawio
```

Inspect the raw TLV output with `tlvcat`:

```sh
# Pipe the output through tlvcat to see tags and values
printf 'UT\x00\x00\x00\x05helloUE\x00\x00\x00\x00' | alayacore --rawio | go run ./misc/tlvcat.go
```

Or generate a TLV request with images and inspect the result:

```sh
# Generate a request with 2 images
go run misc/gen_tlv_request.go \
  "What's in these images?" image1.jpg image2.jpg \
  | alayacore --rawio | go run ./misc/tlvcat.go
```

## Use Cases

- **Orchestration**: A parent AI agent launches AlayaCore as a subprocess
  and communicates with it using TLV frames.
- **Testing**: Send specific TLV sequences to test session behavior.
- **Integration**: Connect AlayaCore to custom UIs, dashboards, or
  pipelines that speak the TLV protocol.
