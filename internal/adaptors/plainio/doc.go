// Package plainio provides a plain stdin/stdout adaptor for AlayaCore.
//
// It reads user prompts from stdin (one per newline) and prints assistant
// messages to stdout. No terminal features (ANSI codes, TTY detection, etc.)
// are used — just plain IO.
//
// Activate with the --plainio flag. Use --text-only alongside it to suppress
// everything except user prompts and assistant text.
//
// Input rules:
//   - Each line is treated as a separate prompt.
//   - A trailing backslash (\) before newline continues the prompt on the next line.
//   - Ctrl-D (EOF) closes input; the program exits after the current task finishes (code 0).
//   - Ctrl-C (SIGINT): closes input, waits for the current task to finish, then exits
//     with code 1. A second Ctrl-C forces an immediate exit.
//   - Errors during the session cause an immediate exit with code 1.
//     Remaining queued tasks are NOT executed.
//   - A clean exit (EOF with no errors) returns code 0.
//
// Output format:
//   - Assistant text/reasoning: printed directly (stream ID prefix stripped).
//     A blank line is inserted when consecutive deltas belong to different
//     stream groups (e.g. assistant text followed by reasoning, or a new
//     step's text after a tool call).
//   - User prompts: prefixed with "> ".
//   - Tool calls: shown as "[tool_name]".
//   - Tool results: printed as-is (JSON-unescaped).
//   - Errors: prefixed with "Error: ".
//   - Notifications: prefixed with "[...]".
//   - A blank line is printed after each task completes.
//
// Communication with the session layer uses the same TLV protocol as the
// terminal and plainio adaptors.
//
// Key Files:
//   - adaptor.go: Adaptor struct, Start() entry point, signal handling
//   - input.go: Stdin line reader with backslash continuation
//   - output.go: TLV parser and plain-text renderer
package plainio
