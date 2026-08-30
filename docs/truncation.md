# Tool Output Truncation

How AlayaCore handles large outputs from tools to stay within context budgets.

## Strategy

| Tool | Behavior | File Pattern |
|------|----------|--------------|
| `read_file` | Truncates at 64KB with metadata header; lines above 1MB are truncated and marked; media files above 16MB are reported instead of read | N/A (in-memory) |
| `execute_command` | Saves to file | `cmd-*.txt` |
| `search_content` | Saves to file when over 64KB or `max_lines` (`0` = no line limit) | `search-*.txt` |

Output size is bounded in **memory** as well as in what the model sees. A tool
stream is kept in RAM only up to the 64KB budget; beyond that it spills to a
scratch file and the remainder streams to disk, so a command that prints
gigabytes cannot grow the process — previously the cap decided what the model
saw but not what alayacore allocated, and a non-terminating producer (with the
default unlimited `--command-timeout`) would run until the OOM killer ended the
session.

## read_file

Files larger than 64KB are truncated at a line boundary with metadata:

```
[Lines 1-3375 of 10000 | 64.0KB of 760.6KB shown]

[file content...]
```

- Agent can use `start_line`/`num_lines` to read specific ranges
- No file is created; truncation happens in-memory
- **Individual lines are capped at 1MB.** A longer line is returned cut to the
  cap with a `[…line truncated: exceeds 1MB per line…]` marker, and the rest of
  it is skipped while still counting as one line. Files built from very long
  single lines (minified bundles, single-line JSON, base64 blobs) are therefore
  readable at all — they used to fail the whole read, in `start_line`/
  `num_lines` ranges too, because the line had to be tokenized just to be
  counted
- The 64KB budget is enforced even when the *first* line is larger: it is cut
  to the budget rather than emitted whole
- An empty result is explained rather than left ambiguous, so the agent cannot
  mistake a bad index for an empty file and overwrite it:
  `[start_line 99 is past the end of the file (3 lines); no content to read]`
  or `[file is empty — 0 lines]`
- Media files (image/video/audio/document) are embedded as base64 and
  cannot be truncated: files above 16MB are reported with a size-limit
  message instead of being read

## execute_command

Command output larger than 64KB is saved to a temp file:

```
Output (5000 lines, 194.2KB) saved to: /tmp/alayacore-1234567890/cmd-12345.txt
Use read_file to access specific sections.
```

Or with error:
```
Exit Code: 1
Output (5000 lines, 194.2KB) saved to: /tmp/alayacore-1234567890/cmd-12345.txt
Use read_file to access specific sections.
```

- Agent uses `read_file` with line ranges to access specific sections
- Same behavior for canceled/timed out commands
- Exit code semantics differ by platform: on Windows, canceled/timed-out commands report exit code `1` (set by `TerminateJobObject`/`taskkill`); on Unix, a `SIGKILL`-terminated command reports `137` (128+9). See [architecture.md](architecture.md) for details.

## search_content

Search results exceeding `max_lines` **or 64KB** (whichever comes first) are saved to a temp file. `max_lines` follows the same "0 = no limit" convention as `--command-timeout` / `--max-steps`: omitted or `0` means no line cap (only the 64KB byte cap remains); a positive value caps how many matching lines are returned inline.

```
Search found 500 matching lines (194.2KB). Results saved to: /tmp/alayacore-1234567890/search-12345.txt
Use read_file to access specific matches.
```

- Agent uses `read_file` to access the full results from the saved file
- Searches run under the same global timeout as `execute_command` (`shell.DefaultCommandTimeout`, default: no limit); timed-out and canceled searches are distinguished

## Temp File Location

Each process gets its own directory under the system temp directory, created atomically by `os.MkdirTemp`:

```
/tmp/alayacore-1234567890/cmd-*.txt
/tmp/alayacore-1234567890/search-*.txt
```

The random suffix guarantees no collisions between concurrently running `alayacore` instances. The returned path is absolute, so `read_file` can access it regardless of the current working directory.

Oversized streams also use a scratch file in the same directory while they are
being read (`tool-output-*.tmp`). It holds the part of the stream beyond the
64KB in-memory budget and is **deleted when the tool call finishes** — only the
`cmd-*.txt` / `search-*.txt` result file, which the agent was pointed at, is
left behind.

**Cleanup:**
- Automatic on normal exit (`tools.Cleanup()` in `main.go`)
- The OS typically cleans system temp on reboot
- For immediate cleanup of stray directories:
  ```bash
  rm -rf /tmp/alayacore-*/
  ```

## Related

- [Context Tracking](context-tracking.md) — How context tokens are tracked across API calls
- [Error Handling](error-handling.md) — `max_tokens` truncation vs. errors
