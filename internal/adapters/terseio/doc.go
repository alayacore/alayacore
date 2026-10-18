// Package terseio provides an answer-only stdin/stdout adapter for AlayaCore: all
// of stdin becomes one prompt (or one command), and stdout carries nothing but the
// final answer.
//
// Activate with the --terseio flag.
//
// The contract this mode promises — what stdin means, what reaches stdout and
// stderr, the exit codes, Ctrl-C, and the MCP flow — is docs/terseio.md's subject,
// stated once there, including the exit-code table a script reads instead of this
// comment.
//
// Key files:
//   - adapter.go: Adapter, Start(), and the signal handling behind the 130 exit
//   - input.go:   the full-stdin prompt/command reader
//   - output.go:  the frame parser and answer-only renderer
//
// This mode is a sibling of plainio, not a variant of it: see
// docs/internal/adapter-architecture.md → "Siblings, not variants".
package terseio
