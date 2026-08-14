# Tool Output Truncation

How AlayaCore handles large outputs from tools to stay within context budgets.

## Strategy

| Tool | Behavior | File Pattern |
|------|----------|--------------|
| `read_file` | Truncates at 64KB with metadata header; media files above 16MB are reported instead of read | N/A (in-memory) |
| `execute_command` | Saves to file | `cmd-*.txt` |
| `search_content` | Saves to file when over 64KB or `max_lines` | `search-*.txt` |

## read_file

Files larger than 64KB are truncated at a line boundary with metadata:

```
[Lines 1-3375 of 10000 | 64.0KB of 760.6KB shown]

[file content...]
```

- Agent can use `start_line`/`num_lines` to read specific ranges
- No file is created; truncation happens in-memory
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

Search results exceeding `max_lines` (default 100) **or 64KB** (whichever comes first) are saved to a temp file:

```
Search found 500 matching lines (194.2KB). Results saved to: /tmp/alayacore-1234567890/search-12345.txt
Use read_file to access specific matches.
```

- Agent uses `read_file` to access the full results from the saved file
- Searches run under the same global timeout as `execute_command` (`shell.DefaultCommandTimeout`); timed-out and canceled searches are distinguished

## Temp File Location

Each process gets its own directory under the system temp directory, created atomically by `os.MkdirTemp`:

```
/tmp/alayacore-1234567890/cmd-*.txt
/tmp/alayacore-1234567890/search-*.txt
```

The random suffix guarantees no collisions between concurrently running `alayacore` instances. The returned path is absolute, so `read_file` can access it regardless of the current working directory.

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
