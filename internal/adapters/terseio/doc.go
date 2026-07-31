// Package terseio provides a minimal stdin/stdout adapter for AlayaCore:
// read ALL of stdin as a single prompt, print ONLY the final answer.
//
// Activate with the --terseio flag.
//
// Contract:
//   - stdin: read in full (until EOF) and treated as ONE prompt. Newlines
//     are preserved; trailing newlines are trimmed. Commands (":...") are
//     NOT supported — everything is prompt text. For interactive use or
//     commands, use --plainio.
//   - stdout: contains ONLY the final assistant text answer, followed by
//     a trailing newline. No reasoning, tool calls, tool results, prompts,
//     notifications, or progress output. If the final message contains no
//     text (e.g. reasoning-only or tool-call-only), stdout is empty.
//   - stderr: errors ("[error: ...]") and notifications ("[...]").
//   - --tool-confirm is REJECTED at startup (main.go): terseio consumes
//     stdin, so tool confirmations could never be answered. With the
//     conflict rejected, tool_confirm frames cannot arrive and no
//     interactive channel is needed.
//   - Exit codes: 0 on success, 1 on session errors, 2 on the
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
//   - input.go: Full-stdin prompt reader
//   - output.go: TLV parser and answer-only renderer
package terseio
