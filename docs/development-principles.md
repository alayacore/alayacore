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

// ✅ OK: adapter uses an agent type (type sharing)
models := make([]agentpkg.ModelConfig, ...)

// ✅ BETTER: shared types live in a neutral package
models := make([]config.ModelConfig, ...)
```

**Rationale:** A function call bypasses the TLV boundary and creates hidden runtime coupling. An external adapter written in Python or Rust could never make that call — so built-in adapters shouldn't either.

#### Rule 2: TLV protocol must be complete

Every capability available to built-in adapters must be achievable through TLV frames alone. If a feature cannot be exercised via `--rawio` (raw TLV stdin/stdout), the protocol is incomplete.

| Direction | Tag | Covers |
|-----------|-----|--------|
| adapter → agent (stdin) | `UT` + `UE` | User text prompts |
| adapter → agent (stdin) | `CI` | All commands (`save`, `cancel`, `model_set`, etc.) — JSON `{id, name, input}` |
| adapter → agent (stdin) | `UI`/`UV`/`UA`/`UD` | Media input (image/video/audio/document) |
| agent → adapter (stdout) | `UT`/`UI`/`UV`/`UA`/`UD` | User message echo (with assigned history ID) |
| agent → adapter (stdout) | `AT`/`AR` | Assistant text/reasoning (complete/authoritative; empty content if deltas preceded it) |
| agent → adapter (stdout) | `At`/`Ar` | Assistant text/reasoning (streaming deltas; absent with `--no-delta`) |
| agent → adapter (stdout) | `AF`/`UF` | Tool calls and results (JSON) |
| agent → adapter (stdout) | `Af` | Tool call arguments (streaming delta, partial JSON; absent with `--no-delta`) |
| agent → adapter (stdout) | `CO` | Command results (JSON `{id, output, is_error}` — one per CI) |
| agent → adapter (stdout) | `SM` | System state — task, model, theme, reasoning, mcp, error, notify, tool_confirm, version |

> **Note:** User tags (UT, UI, UV, UA, UD) flow in **both** directions.
> On **stdin** they carry new user input; on **stdout** they carry the agent's
> echo of that input with an assigned history ID. Adapters must handle both.

**Test:** If you can't do it through raw TLV frames, don't add it to the built-in adapter either. First extend the protocol.

#### Rule 3: Shared types belong in neutral packages

Types used by both adapter and agent (e.g. `ModelConfig`) must live in a shared package that neither layer owns:

```
internal/config/    ← ModelConfig, key-value parsing/formatting
internal/tlv/       ← TLV tag constants, frame encoding/decoding
internal/protocol/  ← System message types, tool data structures
internal/theme/     ← Theme data structures
internal/commands/  ← Command name constants (CI/CO vocabulary)
```

This prevents circular dependencies and keeps the boundary clean. When moving a type out of the agent package, use a temporary type alias for transition:

```go
type ModelConfig = config.ModelConfig  // transitional, remove eventually
```

### When Exceptions Apply

| Scenario | Allowed? | Reason |
|----------|----------|--------|
| `internal/app/session.go` imports agent | ✅ Yes | Bootstrap layer, not an adapter |
| Adapter references agent's struct type | ✅ Yes | Compile-time convenience, no runtime coupling |
| Adapter uses command-name string literals (`"cancel"`) | ⚠️ Avoid | Use `commands.CommandNameCancel` — the shared `internal/commands` package is the single source of truth for CI/CO names |
| Adapter imports agent for constants (`CommandNameCancel`) | ❌ **No** | Use `internal/commands` instead — importing agent bypasses the neutral-package rule |
| Adapter calls agent's functions | ❌ **No** | Bypasses the TLV boundary |
| Agent imports adapter | ❌ **Never** | One-way dependency — agent must not know adapters exist |

### Architecture Checklist

When reviewing a change, ask:

1. **Does this call an agent function from an adapter?** → Move the function to a neutral package or find a TLV-based approach.
2. **Can a rawio client do this?** → If not, the TLV protocol needs a new message type.
3. **Does this create a reverse dependency (agent → adapter)?** → Restructure immediately; this is never acceptable.
4. **Is this type used by both sides?** → Put it in `internal/config`, `internal/tlv`, `internal/protocol`, `internal/commands`, or another shared package.
