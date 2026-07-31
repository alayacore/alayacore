// Package plainio provides a plain stdin/stdout adapter for AlayaCore.
//
// It reads user prompts from stdin (one per newline) and prints assistant
// messages to stdout. No terminal features (ANSI codes, TTY detection, etc.)
// are used — just plain IO.
//
// Activate with the --plainio flag.
//
// Input rules:
//   - Each line is read as a prompt.
//   - A trailing backslash (\) before newline continues the prompt on the next line.
//   - There is no task queue — only one prompt is processed per invocation.
//     If stdin contains multiple prompts, only the first is executed; the
//     rest are rejected while the first is running.
//   - :quit / :q stops reading input; the program waits for any running
//     task, then exits with code 0.
//   - Ctrl-D (EOF) closes input; the program waits for the current task to
//     finish, then exits with code 0 — like :quit, regardless of whether
//     any task errored during the session.
//   - Ctrl-C (SIGINT): terminates immediately with default signal handling
//     (exit code 130).
//   - Task errors are reported and the session CONTINUES — plainio is an
//     interactive mode, errors never terminate the session and the user
//     can keep typing prompts. The exit code reflects process-level state
//     only (0 = clean exit, 1 = startup/stdin failure); scripts that need
//     a failure signal on task errors should use --terseio.
//
// Output format:
//   - Assistant text/reasoning: printed directly (history ID prefix stripped).
//     A blank line is inserted when consecutive deltas belong to different
//     stream groups or different message types.
//   - User prompts: rendered as a "User: <text>" block on its own line,
//     with a blank line after it (and before it when the previous output
//     ended with a newline).
//   - Tool calls: printed as raw JSON (id, name, input).
//   - Tool results: printed as raw JSON (id, output, is_error).
//   - Command results (CO): failures as "[error: ...]" (does not affect the
//     exit code), successes rendered from the structured result (e.g.
//     "Session saved to <path>"); commands whose effect is self-evident
//     (e.g. :cancel, :reason) are silent.
//   - Errors: rendered as "[error: ...]".
//   - Notifications: prefixed with "[...]".
//   - Tool confirmations: shown as "[tool_confirm: allow tool "id" to run?]".
//   - A blank line is printed after each task completes.
//
// Communication with the session layer uses the same TLV protocol as the
// terminal, terseio, and rawio adapters.
//
// Key Files:
//   - adapter.go: Adapter struct, Start() entry point
//   - input.go: Stdin line reader with backslash continuation
//   - output.go: TLV parser and plain-text renderer
package plainio
