# Development Principles

## Adapter ↔ Agent Isolation

The adapter (UI layer) and agent (core AI logic) **must be completely isolated**. They communicate exclusively through a single bidirectional TLV (Tag-Length-Value) byte stream — no direct function calls, no shared state, no bypass.

```
┌──────────┐     TLV frames (stdin)      ┌──────────┐
│          │ ──────────────────────────▶ │          │
│ Adapter  │  UT/UE/UI/UV/UA/UD (input)  │  Agent   │
│ (TUI/    │                             │ (session │
│  plainio/│ ◀────────────────────────── │  + llm)  │
│  terseio/│                             │          │
│  rawio)  │  AT/AR/AF/UF/UT/UI/UV/UA/UD │          │
│          │     + SM  (stdout)          │          │
└──────────┘                             └──────────┘
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

**Rationale:** A function call bypasses the TLV boundary and creates hidden runtime coupling. An external adapter written in Python or Rust could never make that call — so built-in adapters shouldn't either.

#### Rule 2: TLV protocol must be complete

Every capability available to built-in adapters must be achievable through TLV frames alone. If a feature cannot be exercised via `--rawio` (raw TLV stdin/stdout), the protocol is incomplete.

| Direction | Tag | Covers |
|-----------|-----|--------|
| adapter → agent (stdin) | `UT` + `UE` | User text prompts |
| adapter → agent (stdin) | `CE` | End of input: no more prompts (the stream stays open for commands) |
| adapter → agent (stdin) | `CI` | All commands (`save`, `cancel`, `model_set`, etc.) — JSON `{id, name, input}` |
| adapter → agent (stdin) | `UI`/`UV`/`UA`/`UD` | Media input (image/video/audio/document) |
| agent → adapter (stdout) | `UT`/`UI`/`UV`/`UA`/`UD` | User message echo (with assigned history ID) |
| agent → adapter (stdout) | `AT`/`AR` | Assistant text/reasoning (complete/authoritative; empty content if deltas preceded it) |
| agent → adapter (stdout) | `At`/`Ar` | Assistant text/reasoning (streaming deltas; absent with `--no-delta`) |
| agent → adapter (stdout) | `AF`/`UF` | Tool calls and results (JSON) |
| agent → adapter (stdout) | `Af` | Tool call arguments (streaming delta, partial JSON; absent with `--no-delta`) |
| agent → adapter (stdout) | `Uf` | Tool result preview snapshot (ephemeral, display-only; authoritative result arrives via `UF`; absent with `--no-delta`) |
| agent → adapter (stdout) | `CO` | Command results (JSON `{id, output, is_error}` — one per CI) |
| agent → adapter (stdout) | `SM` | System state — task, model, theme, reasoning, mcp, session, error, notify, tool_confirm, version |

> **Note:** User tags (UT, UI, UV, UA, UD) flow in **both** directions.
> On **stdin** they carry new user input; on **stdout** they carry the agent's
> echo of that input with an assigned history ID. Adapters must handle both.

> **Note — frame order is part of the contract.** Within one assistant step,
> `AR` precedes `AT` precedes `AF`, matching the order the step's content parts
> are persisted in. Adapters rely on it: the terminal creates a window per block
> as its frame arrives (positions are fixed at creation), and `--plainio` prints
> in arrival order. They do not sort by the numeric history ID — it records
> *first touch*, so it only tracks this order while providers emit in it (see
> [providers.md](providers.md) → "Complete-event order").

**Test:** If you can't do it through raw TLV frames, don't add it to the built-in adapter either. First extend the protocol.

#### Rule 3: Wire types live in protocol; domain types live in domain packages

Types that cross the adapter/agent boundary in TLV frames are **wire types**:
they live in `internal/protocol` (e.g. `ModelInfo` for the `model_list`
message). Domain types used only inside the agent (e.g. `modelConfig`) stay
in their domain package (`internal/agent`). Adapters decode wire types; they
never import the agent package.

```
internal/protocol/  ← System message types, tool data structures, ModelInfo
internal/tlv/       ← TLV tag constants, frame encoding/decoding
internal/theme/     ← Theme data structures (shared with adapters)
internal/commands/  ← Command name constants (CI/CO vocabulary)
internal/config/    ← Key-value parsing primitives, CLI Settings (no domain types)
internal/agent/     ← Domain types: modelConfig, runtimeConfig, ...
```

This prevents circular dependencies and keeps the boundary clean. When a
type moves between packages, keep the wire JSON tags unchanged so the TLV
protocol stays byte-compatible.

### When Exceptions Apply

| Scenario | Allowed? | Reason |
|----------|----------|--------|
| `internal/app/session.go` imports agent | ✅ Yes | Bootstrap layer, not an adapter |
| Adapter imports agent (types, constants, functions) | ❌ **No** | Use wire types from `internal/protocol` — importing agent bypasses the TLV boundary |
| Adapter uses command-name string literals (`"cancel"`) | ⚠️ Avoid | Use `commands.CommandNameCancel` — the shared `internal/commands` package is the single source of truth for CI/CO names |
| Adapter calls agent's functions | ❌ **No** | Bypasses the TLV boundary |
| Adapter calls `session.CancelTask()` (plainio/terseio SIGINT) | ⚠️ Yes — the only such call | A *request*, not an observation: it has to work whether or not the session is reading frames, and the SIGINT exit codes (0/130) are defined around this call rather than around a CO result. Everything an adapter *observes* comes from the wire |
| `rawio` waits on `session.Done()` | ⚠️ Yes | rawio interprets no frames by definition — that is its contract — so it cannot learn "the session ended" from the wire. Waiting for the session goroutine is the process-level equivalent of the exit its controlling process observes anyway |
| Agent imports adapter | ❌ **Never** | One-way dependency — agent must not know adapters exist |

### Architecture Checklist

When reviewing a change, ask:

1. **Does this call an agent function from an adapter?** → Move the function to a neutral package or find a TLV-based approach.
2. **Can a rawio client do this?** → If not, the TLV protocol needs a new message type.
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

Two corollaries:

- **Don't describe what this tree no longer has.** No paths to deleted files, no
  commit hashes in comments — the commit message is where history lives, and a
  pointer to a file that is gone is worse than none.
- **Keep a comment shorter than the code it explains.** Past roughly 20 lines it
  is a design note, not a line comment: it belongs in `docs/`, linked once.

`misc/check-doc-links.sh` (`make check-doc-links`, run in CI) fails on a relative
Markdown link that does not resolve from the file that wrote it.

The rule is not "write less" — this repo's comments explain *why*, and that is the
point. It is "do not write the same thing twice".
