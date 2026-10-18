// Package plainio provides a plain stdin/stdout adapter for AlayaCore: prompts
// arrive as lines on stdin, and the session transcript leaves on stdout.
//
// Activate with the --plainio flag.
//
// No terminal feature is used — no ANSI, no alternate screen, no TUI. stdin's
// TTY-ness is consulted for exactly one decision, the MCP OAuth fallback: a
// terminal can still answer a server by hand with :mcp_confirm, a pipe cannot, so
// on a pipe the server is declined and initialization settles without it.
//
// The contract this mode promises — input rules, the output layout, exit codes,
// Ctrl-C, and the MCP flow — is docs/plainio.md's subject, stated once there. The
// two facts that belong to the implementation rather than to that contract live
// where they are used: the output lock and the hooks it defers (output.go), so an
// MCP progress line can print from inside a frame handler without deadlocking, and
// the single serialized writer that the stdin reader shares with the OAuth flow
// (app.LockedWriter), because both feed the session's pipe.
//
// Key files:
//   - adapter.go: Adapter, Start(), and the signal handling that keeps Ctrl-C from
//     killing the process
//   - input.go:   the line reader: what the user types becomes frames
//   - output.go:  the frame parser and plain-text renderer
//
// This mode is a sibling of terseio, not a variant of it: see
// docs/internal/adapter-architecture.md → "Siblings, not variants".
package plainio
