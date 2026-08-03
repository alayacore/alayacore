// Package terseio provides a minimal stdin/stdout adapter for AlayaCore:
// read ALL of stdin as a single prompt (or command), print ONLY the final
// answer.
//
// Activate with the --terseio flag.
//
// Contract:
//   - stdin: read in full (until EOF) and treated as ONE message. Newlines
//     are preserved; trailing newlines are trimmed.
//   - If the input starts with ":", the WHOLE input is sent as a single
//     command (":continue", ":save /tmp/x", ...) — the name/args split is
//     at the first whitespace, so multi-line command input works. ":quit"
//     / ":q" exit cleanly (code 0) without sending anything. Command
//     errors are printed to stderr and set exit code 1.
//   - Otherwise the input is ONE prompt: assistant text is answered on
//     stdout, and prompt text is never echoed.
//   - stdout: contains ONLY the final assistant text answer, followed by
//     a trailing newline. No reasoning, tool calls, tool results, prompts,
//     notifications, or progress output. If the final message contains no
//     text (e.g. reasoning-only or tool-call-only), stdout is empty.
//   - stderr: errors ("[error: ...]"), notifications ("[...]"), and
//     informative command results (e.g. ":save" → "Session saved to ...").
//   - --tool-confirm is REJECTED at startup (main.go): terseio consumes
//     stdin, so tool confirmations could never be answered. With the
//     conflict rejected, tool_confirm frames cannot arrive and no
//     interactive channel is needed.
//   - Exit codes: 0 on success, 1 on session or command errors, 2 on the
//     --tool-confirm conflict (usage error), 130 on SIGINT (default
//     signal handling).
//   - --session works: the conversation is persisted; intermediate content
//     lives in the session file even though it is never printed.
//
// Communication with the session layer uses the same TLV protocol as the
// terminal, plainio, and rawio adapters.
//
// Key Files:
//   - adapter.go: Adapter struct, Start() entry point
//   - input.go: Full-stdin prompt/command reader
//   - output.go: TLV parser and answer-only renderer
package terseio
