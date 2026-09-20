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
//     / ":q" ask the session to end — they are sent as the quit command —
//     and exit cleanly (code 0). Command errors are printed to stderr and
//     set exit code 1.
//   - Otherwise the input is ONE prompt: assistant text is answered on
//     stdout, and prompt text is never echoed.
//   - stdout: contains ONLY the final assistant text answer, followed by
//     a trailing newline. No reasoning, tool calls, tool results, prompts,
//     notifications, or progress output. If the final message contains no
//     text (e.g. reasoning-only or tool-call-only), stdout is empty.
//   - stderr: errors ("[error: ...]"), notifications ("[...]"), and
//     informative command results (e.g. ":save" → "Session saved to ...").
//     MCP init progress ("[mcp: ...]") is written here too — stdout stays a
//     pure answer channel.
//   - MCP OAuth is automatic (shared with plainio via internal/mcpauth):
//     the adapter starts the callback server, opens the browser, and
//     submits the code itself — no stdin is needed. A server whose
//     automatic authorization does not complete (browser could not be
//     opened, or the 5-minute wait timed out) is DECLINED, since terseio
//     has nowhere to type a code; MCP init then settles and the prompt runs
//     without that server's tools. The session reports the declined server
//     as an MCP failure, which — like any SM error — sets exit code 1.
//   - The prompt waits for the session's authoritative "ready" frame before
//     it is sent (MCP init complete), so a piped prompt is not rejected
//     with MCP_NOT_READY. A command bypasses the wait.
//   - --tool-confirm is REJECTED at startup (main.go): terseio consumes
//     stdin, so tool confirmations could never be answered. With the
//     conflict rejected, tool_confirm frames cannot arrive and no
//     interactive channel is needed.
//   - Exit codes: 0 on success, 1 on session or command errors, 2 on the
//     --tool-confirm conflict (usage error), 130 on SIGINT.
//   - Ctrl-C (SIGINT) cancels the running task through the session
//     instead of killing the process: the task (and its detached tool
//     processes, which never receive the terminal's SIGINT) is aborted
//     cleanly, the buffered answer is discarded, and the process exits
//     130 (128+SIGINT) so scripts still detect the interruption. SIGINT
//     during the stdin read phase, or while the prompt waits for the ready
//     frame, aborts the read/wait; SIGINT after the task finished only
//     forces the exit code.
//   - --session works: the conversation is persisted; intermediate content
//     (tool calls, reasoning) is saved to the session file once the task
//     completes even though it is never printed.
//
// Communication with the session layer uses the same TLV protocol as the
// terminal, plainio, and rawio adapters.
//
// Key Files:
//   - adapter.go: Adapter struct, Start() entry point
//   - input.go: Full-stdin prompt/command reader
//   - output.go: TLV parser and answer-only renderer
package terseio
