# Development Principles

## Adapter ↔ Agent Isolation

The adapter (UI layer) and agent (core AI logic) **must be completely isolated**. They communicate exclusively through a single bidirectional frame stream (one frame per line) — no direct function calls, no shared state, no bypass.

```
┌──────────┐      frames (stdin)      ┌──────────┐
│          │ ───────────────────────▶ │          │
│ Adapter  │      UX/UE (input)       │  Agent   │
│ (TUI/    │                          │ (session │
│  plainio/│ ◀─────────────────────── │  + llm)  │
│  terseio/│                          │          │
│  rawio)  │  UX/AX/AF/UF + Ax/Af/Uf  │          │
│          │      + SM  (stdout)      │          │
└──────────┘                          └──────────┘
```

### Three Hard Rules

#### Rule 1: No direct calls from adapter to agent

Adapters may reference agent **types** (struct definitions) for convenience, but must never call agent **functions** or **methods**.

```go
// ❌ FORBIDDEN: adapter calls an agent function
blocks = append(blocks, agentpkg.SerializeModelConfig(m))

// ✅ OK: adapter uses wire types from the protocol layer
models := make([]protocol.ModelInfo, ...)
```

**Rationale:** A function call bypasses the frame boundary and creates hidden runtime coupling. An external adapter written in Python or Rust could never make that call — so built-in adapters shouldn't either.

#### Rule 2: The frame protocol must be complete

Every capability available to built-in adapters must be achievable through frames alone. If a feature cannot be exercised via `--rawio` (raw frame stdin/stdout), the protocol is incomplete.

| Direction | Tag | Covers |
|-----------|-----|--------|
| adapter → agent (stdin) | `UX` + `UE` | User content prompts (JSON content item) |
| adapter → agent (stdin) | `CE` | End of input: no more prompts (the stream stays open for commands) |
| adapter → agent (stdin) | `CI` | All commands (`save`, `cancel`, `model_set`, etc.) — JSON `{command_id, name, args}` |
| adapter → agent (stdin) | `UX` (`type` image/video/audio/document) | Media input |
| agent → adapter (stdout) | `UX` | User content echo (with assigned history id) |
| agent → adapter (stdout) | `AX` | Assistant content — text/reasoning (complete/authoritative; empty content if deltas preceded it) |
| agent → adapter (stdout) | `Ax` | Assistant content (streaming deltas; absent with `--no-delta`) |
| agent → adapter (stdout) | `AF`/`UF` | Tool calls and results (JSON) |
| agent → adapter (stdout) | `Af` | Tool call arguments (streaming delta, partial JSON; absent with `--no-delta`) |
| agent → adapter (stdout) | `Uf` | Tool result preview snapshot (ephemeral, display-only; authoritative result arrives via `UF`; absent with `--no-delta`) |
| agent → adapter (stdout) | `CO` | Command results (JSON `{command_id, result, is_error}` — one per CI) |
| agent → adapter (stdout) | `SM` | System state — task, model, theme, reasoning, mcp, session, error, notify, tool_confirm, version |

> **Note:** User content (`UX`) flows in **both** directions.
> On **stdin** they carry new user input; on **stdout** they carry the agent's
> echo of that input with an assigned history ID. Adapters must handle both.

> **Note — frame order is part of the contract.** Within one assistant step,
> the reasoning content item precedes the text item, which precedes the tool
> calls (`AX` reasoning, `AX` text, `AF`), matching the order the step's content
> parts are persisted in. Adapters rely on it: the terminal creates a window per block
> as its frame arrives (positions are fixed at creation), and `--plainio` prints
> in arrival order. They do not sort by the numeric history ID — it records
> *first touch*, so it only tracks this order while providers emit in it (see
> [providers.md](providers.md) → "Complete-event order").

**Test:** If you can't do it through raw frames, don't add it to the built-in adapter either. First extend the protocol.

#### Rule 3: Wire types live in protocol; domain types live in domain packages

Types that cross the adapter/agent boundary in frames are **wire types**:
they live in `internal/protocol` (e.g. `ModelInfo` for the `model_list`
message). Domain types used only inside the agent (e.g. `modelConfig`) stay
in their domain package (`internal/agent`). Adapters decode wire types; they
never import the agent package.

```
internal/protocol/  ← System message types, tool data structures, ModelInfo
internal/wire/      ← frame tags, encoding/decoding
internal/lines/     ← splitting a byte stream into lines (the frame boundary)
internal/theme/     ← Theme data structures (shared with adapters)
internal/commands/  ← Command name constants (CI/CO vocabulary)
internal/config/    ← Key-value parsing primitives, CLI Settings (no domain types)
internal/agent/     ← Domain types: modelConfig, runtimeConfig, ...
```

This prevents circular dependencies and keeps the boundary clean. When a
type moves between packages, keep the wire JSON tags unchanged so the frame
protocol stays byte-compatible.

### When Exceptions Apply

| Scenario | Allowed? | Reason |
|----------|----------|--------|
| `internal/app/session.go` imports agent | ✅ Yes | Bootstrap layer, not an adapter |
| Adapter imports agent (types, constants, functions) | ❌ **No** | Use wire types from `internal/protocol` — importing agent bypasses the frame boundary |
| Adapter uses command-name string literals (`"cancel"`) | ⚠️ Avoid | Use `commands.CommandNameCancel` — the shared `internal/commands` package is the single source of truth for CI/CO names |
| Adapter calls agent's functions | ❌ **No** | Bypasses the frame boundary |
| Adapter calls `session.CancelTask()` (plainio/terseio SIGINT) | ⚠️ Yes — the only such call | A *request*, not an observation: it has to work whether or not the session is reading frames, and the SIGINT exit codes (0/130) are defined around this call rather than around a CO result. Everything an adapter *observes* comes from the wire |
| `rawio` waits on `session.Done()` | ⚠️ Yes | rawio interprets no frames by definition — that is its contract — so it cannot learn "the session ended" from the wire. Waiting for the session goroutine is the process-level equivalent of the exit its controlling process observes anyway |
| Agent imports adapter | ❌ **Never** | One-way dependency — agent must not know adapters exist |

### Architecture Checklist

When reviewing a change, ask:

1. **Does this call an agent function from an adapter?** → Move the function to a neutral package or find a frame-based approach.
2. **Can a rawio client do this?** → If not, the frame protocol needs a new message type.
3. **Does this create a reverse dependency (agent → adapter)?** → Restructure immediately; this is never acceptable.
4. **Does this type cross the adapter/agent boundary?** → Define the wire type in `internal/protocol` (e.g. `ModelInfo`); domain-only types stay in the agent package.

## Code Style

### Indentation: tabs, unless the format forbids them

Go is gofmt'd, and gofmt indents with **tabs** — spaces are for *alignment*
(the columns of a struct tag, a trailing comment). Every other file this repo
owns that has a choice follows the same rule:

| File | Indent | Enforced by |
|------|--------|-------------|
| `*.go` | tab | `gofmt` — `make fmt`, `make check` |
| `Makefile` recipes | tab | make's own syntax: a recipe indented with spaces is not a recipe |
| `*.sh` | tab | `misc/check-shell-style.sh` — `make check-shell-style`, `make check`, CI; `.editorconfig` tells editors the same thing |
| `*.yml` | two spaces | the exception, and not a preference: the YAML spec forbids tabs in indentation |

**Rationale:** one rule for the whole tree means no file needs a second opinion
about what an indent is. Shell is the case worth writing down, because two-space
indentation is the shell world's convention and most editors default `*.sh` to
it — an editor's default is not a decision this repo made, and a script that
keeps it reads as a style the repo never chose.

**Indent versus alignment.** An indent is a tab. Space-aligning a column *inside*
a line — gofmt's struct tags, a run of trailing comments — is alignment, and stays
spaces. A shell script here has no such construct, so in shell the leading
whitespace is tabs and nothing else: a continuation indents like any other line.
That is exactly what `check-shell-style.sh` asserts — a space anywhere in the
indent is the finding — and it is why the check does not look at trailing
whitespace or at a tab's display width: those are different complaints about
different things.

`.editorconfig` carries the rule to editors, so a new file usually arrives
already indented the way the checker expects; the checker is what happens when it
does not.

## Documentation and Comments

### One fact, one home

State a design fact **once** — in the type or function doc where it lives, or in a
single `docs/` page — and reference it by name everywhere else. A rule restated in
four comments is a rule that disagrees with itself after the next change, and the
reader cannot tell which copy is current. When the pull is to re-explain something
nearby, link instead: a symbol name, or `docs/...md#anchor`.

Three corollaries:

- **Don't describe what this tree no longer has.** No paths to deleted files, no
  commit hashes in comments — the commit message is where history lives, and a
  pointer to a file that is gone is worse than none.
- **Keep a comment shorter than the code it explains.** Past roughly 20 lines it
  is a design note, not a line comment: it belongs in `docs/`, linked once.
- **Don't write what a command can tell you.** No line numbers (`width.go:NNN`),
  no line counts (`~N lines`): gofmt, a comment or a test split moves them, and
  the reader who wants the number runs the command. Say what is true of the
  design and let the number be derived.

### One concept, one name

[Vocabulary](vocabulary.md) names each concept the tree talks about — task,
prompt, step, window, overlay, caret, the two cursors — and the words it does
not allow for them. A run is always a task; its kind is `promptKind`
(`userPrompt` or `summarizeCall`), which is the distinction that used to be
carried by a second noun.

A synonym costs a reader a guess and an overload costs them a wrong one, and the
compiler sees neither, so `misc/check-terms.sh` (`make check-terms`, run in CI)
enforces the mechanical half: a retired name absent, a word confined to the file
whose subject it is, a wire value compared as a constant rather than a literal, a
command's usage line rendered rather than spelled, a retired CO error code gone
from prose and from Go literals, one English spelling. Its header lists the
rules; this page does not repeat them.

It cannot tell you a *new* synonym has appeared. That is where the rule about
stating a fact once meets the vocabulary: a new concept gets its name there, in
the same change that introduces it.

`misc/check-docs.sh` (`make check-docs`, run in CI) fails on every shape a tool
can decide exactly: a relative Markdown link that does not resolve from the file
that wrote it, a fragment naming a heading that does not exist in the file it
points at, a file named in a code span that is not in the tree, and a perishable
shape in prose — a line number or a line count. It guesses at nothing else; its
header says what it leaves to review, and why. The fragment half is
`misc/check-doc-anchors.go`, because a heading becomes an anchor by a Unicode
transform (the Chinese headings in `README.zh-CN.md` have anchors too) that shell
cannot do honestly.

A name that a rename leaves behind is the same defect in a place no link check
reaches: a comment still naming a file that was renamed out from under it, an
error code that protocol no longer declares. Those are retired names, so
`misc/check-terms.sh` carries them — see below — because what they need is a list
of what has been renamed, not a resolution rule.

### A commit message names the kind of change, not the size of the diff

`refactor` means the behavior did not change: the same wire bytes, the same
output, the same contract — only the shape of the code. Renaming a Go symbol is
a refactor. Renaming a field on the wire is not, because a peer parses those
bytes: an adapter written against the previous generation sends the old name and
reads the old shape, and after the change it cannot. That is a **breaking
change**, and the message says so — in the type, and in a `BREAKING CHANGE:`
footer stating what a peer at the previous generation must do. A large diff is
not a reason to reach for the word, and neither is the absence of a new command
or flag.

One question decides it: **could a program built against the previous generation
keep working unchanged?** If not, the commit is not a refactor.

The rule is not "write less" — this repo's comments explain *why*, and that is the
point. It is "do not write the same thing twice".

## Tests

### A test that cannot fail is not a test

A `Test…` function that asserts nothing — a body that only logs a measurement, or
a doc comment wearing a test signature — costs CI time, buys nothing, and reads to
the next person as coverage. Measurements belong in `*_bench_test.go`; a design
note belongs in the type's doc comment or in `docs/`.

### A skipped test is not a passing test

There are two reasons to skip — the platform cannot run it, or a prerequisite
outside the tree is absent — and both have to be visible where it matters. A skip
that fires in CI is coverage disappearing silently: `go test ./...` runs without
`-v`, so nothing says so. Every external tool the suite runs is therefore
**declared in the workflow** (tmux for the replay test, ripgrep for
`search_content`), and the test fails in CI when it is missing while still
skipping on a developer's box (`requireRipgrep`, `screen_tmux_replay_test.go`).
A precondition that quietly invalidates the property — a library that stops
honoring the variable the test injects — fails for the same reason: the
guarantee is untested, and saying so is the point.

### Test order is not a contract

The suite passes in any order, and the workflow says so with `-shuffle=on`. Two
consequences for a test that touches process state. The fixture it reads is one it
creates — `t.TempDir`, `t.Chdir`, `t.Setenv`, never the package directory, the
real `$HOME`, or whatever `os.Args` was left as. And a global it must change is
restored, or, when restoring is not enough because something caches the value on
first read, **pinned before the window opens**: `currentUmask` reads the umask
once, and a test that narrowed the umask inside that read latched it for the rest
of the binary.

### Price what the build can measure

Allocation counts come from process-wide `runtime.MemStats`, and a `-race` build
allocates on its own account mid-measurement. The cost tests therefore run in the
plain build and skip under `-race` (`cost_gate_test.go`; the standard library
makes the same split), and they compare **bounds and slopes**, never the equality
of two truncated averages — a count one allocation above its twin is noise, not a
regression. A symbol only a test calls lives beside the tests
(`window_accessors_test.go` is the worked example), where it cannot be mistaken
for the shipped surface.

Benchmarks are measurements, not gates: their numbers on a shared runner are
noise, and CI compares none of them. It does run every one **once**
(`-benchtime=1x`, `make bench`): compiling a benchmark happens with the package's
tests, but only running it finds a fixture that panics, or a case that has
quietly started skipping. That is a correctness question; the number is not.

## Releases

A release note is written **from the tree**, and the tree is read first. Every
claim in it — a flag, a default, a wire field, a limit, "nothing else moved" —
is something a user will act on without checking, because the note is the one
document read *instead of* the code.

`make check` settles the mechanical half before drafting: links, heading
anchors, file names, retired names, error codes. The other half it cannot
decide — a default that changed, a behavior a paragraph describes, one
generation's "nothing else moved" — is read out of the code, never remembered
from the commit log. A note assembled from commit subjects is a summary of what
was intended; a note assembled from the tree is a statement about what is.

**Findings go at the top.** Anything found while writing the notes that the
release would otherwise state wrongly goes *above* the draft, in its own block,
one line per finding with the file and line, each marked as introduced by this
release or already in the tree. Not a footnote, not a "by the way" at the end:

- a page still naming a retired error code, while the note's table says what it
  became;
- a table row describing an option that no longer exists;
- a sample frame in the previous generation's shape.

The person publishing is the last one who can still fix it cheaply, and they are
about to publish a document that contradicts the tree. The reason is not
tidiness: a release is when accumulated drift is read by someone who did not
write it — a user upgrading, an adapter author diffing two versions — so that is
the moment the drift is cheapest to find and most expensive to miss.
