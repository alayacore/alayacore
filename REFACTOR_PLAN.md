# AlayaCore Refactor Plan

## Progress

### Phase 1: Quick Wins ✅ COMPLETED

1. **Simplified STATE.md** - Reduced from 77 lines to 21 lines
   - Moved component-specific gotchas to code comments

2. **Removed unused code in app/app.go**
   - Removed `CreateAgent`, `AgentFactory`, deprecated aliases, unused context helpers

3. **Consolidated documentation**
   - Archived `error-handling.md`, `schema-improvements.md`
   - Merged `sessions.md`, `window-container.md`, `terminal-controls.md` into `cli-reference.md`

### Phase 2: Terminal Consolidation ✅ COMPLETED

Reduced terminal adaptor from 18 source files to 12 files:

| Before | After | Change |
|--------|-------|--------|
| 18 source files | 12 files | -33% |
| ~7,200 lines | ~7,000 lines | -3% |

### Phase 3: Session Consolidation ✅ COMPLETED

Reduced session files from 8 to 3:

| Before | After | Change |
|--------|-------|--------|
| 8 session files | 3 files | -63% |
| ~2,300 lines | ~1,530 lines | -33% |

**Files consolidated:**
- `session.go` - Merged paths.go, prompt.go, tasks.go, output.go into main session file
- `session_io.go` - Merged commands.go, toolformat.go (commands, tool formatting)
- `session_persist.go` - Merged markdown.go (save/load, TLV format)

**Final session structure:**
```
internal/agent/
├── session.go          (834 lines) - core struct, lifecycle, task queue, prompt processing
├── session_io.go       (332 lines) - commands, tool formatting
├── session_persist.go  (365 lines) - save/load, TLV encoding
├── model_manager.go    (392 lines) - model config management
├── runtime_manager.go  (164 lines) - runtime state persistence
├── command_registry.go (170 lines) - command dispatch
└── doc.go              (52 lines)  - package doc
```

---

## Summary

| Area | Before | After | Change |
|------|--------|-------|--------|
| Source files | 63 | 48 | -24% |
| Terminal files | 18 | 12 | -33% |
| Session files | 8 | 3 | -63% |
| STATE.md lines | 77 | 21 | -73% |
| Active docs | 8 | 4 | -50% |

All tests pass. The build is clean.

---

## Guiding Principles

1. **Reduce cognitive load** - Less code to understand, fewer files to navigate
2. **Make patterns obvious** - Hidden patterns in STATE.md should be encoded in code structure
3. **Consolidate related code** - Avoid file proliferation for simple concepts
4. **Keep tests close** - Tests should be in the same file or clearly adjacent

---

## Refactor Areas

### 1. Consolidate Terminal Adaptor Files (High Impact, Medium Effort)

**Problem:** 32 files for terminal UI creates navigation overhead and scatters related logic.

**Current:**
```
internal/adaptors/terminal/
├── terminal.go           (340 lines)
├── keys.go               (283 lines) 
├── keybinds.go           (176 lines)
├── commands.go           (98 lines)
├── display.go            (412 lines)
├── output.go             (505 lines)
├── window.go             (318 lines)
├── window_render.go      (255 lines)
├── window_scroll.go      (209 lines)
├── window_diff.go        (137 lines)
├── model_selector.go     (578 lines)
├── queue_manager.go      (263 lines)
├── input_component.go    (190 lines)
├── status.go             (83 lines)
├── styles.go             (108 lines)
├── theme.go              (136 lines)
├── editor.go             (132 lines)
├── focus_manager.go      (133 lines)
├── ...plus test files
```

**Proposed:**
```
internal/adaptors/terminal/
├── terminal.go          # Main model, update, view (~600 lines - merged terminal.go + adaptor_entry.go + common.go)
├── keybinds.go          # Key handling (keys.go + keybinds.go + commands.go ~550 lines)
├── display.go           # Display rendering + window buffer (~900 lines - display + window + render + scroll)
├── output.go            # TLV parsing & styling (~500 lines - output + wrap)
├── components.go        # UI components (~1000 lines - input + status + editor + focus)
├── modals.go            # Modal overlays (~800 lines - model_selector + queue_manager)
├── theme.go             # Styles & colors (~200 lines - theme + styles + constants)
├── interfaces.go        # Public interfaces (keep separate)
└── doc.go               # Package doc (keep)
```

**Rationale:**
- Reduces file count from 20+ to 8
- Groups related functionality
- Each file has a clear single responsibility
- Tests stay in separate files but match the source structure

---

### 2. Consolidate Session Files (Medium Impact, Low Effort)

**Problem:** Session logic split across 8 files with unclear boundaries.

**Current:**
```
internal/agent/
├── session.go            (393 lines)
├── session_commands.go   (185 lines)
├── session_output.go     (208 lines)
├── session_tasks.go      (180 lines)
├── session_persist.go    (274 lines)
├── session_markdown.go   (295 lines)
├── session_prompt.go     (349 lines)
├── session_toolformat.go (180 lines)
├── session_paths.go      (308 lines)
```

**Proposed:**
```
internal/agent/
├── session.go           # Core session struct, lifecycle, task queue (~600 lines)
├── session_io.go        # Input/output, TLV handling, commands (~500 lines)
├── session_persist.go   # Session save/load, markdown format (~550 lines)
├── model_manager.go     # Model config (keep as-is)
├── runtime_manager.go   # Runtime state (keep as-is)
└── command_registry.go  # Commands (keep as-is)
```

**Rationale:**
- session_paths.go, session_prompt.go, session_tasks.go, session_output.go can merge into session.go
- session_toolformat.go is small, merge into session_io.go
- 8 files → 6 files with clearer boundaries

---

### 3. Simplify STATE.md (Low Impact, Low Effort)

**Problem:** STATE.md has grown to 77 lines of "gotchas" - knowledge that should be in code or tests.

**Analysis of current STATE.md contents:**

| Category | Lines | Should Be |
|----------|-------|-----------|
| Session tool call IDs | 3 | Keep in STATE.md |
| ANSI sequences non-recursive | 3 | Add helper function + test |
| SwitchModel deadlock | 2 | Add code comment + test |
| Terminal scroll position | 2 | Add code comment |
| Anthropic prompt caching | 8 | Move to llm/providers/anthropic.go comments |
| OpenAI tool call chunking | 6 | Move to llm/providers/openai.go comments |
| Tool result ordering | 4 | Add code comment |
| Incomplete tool calls | 2 | Add code comment |
| Session loading scroll | 3 | Add code comment |
| WindowBuffer markDirty | 4 | Add code comment |

**Proposed STATE.md:**
```markdown
# AlayaCore Project Status

## Current Work
None

## Critical Gotchas

- **Session loading tool call IDs**: When displaying tool calls from a loaded session, 
  the `TagFunctionNotify` messages must include the `[:tool_call_id:]` prefix so the 
  terminal adaptor can create windows with correct IDs.

## Architecture Notes

For implementation-specific gotchas, see code comments in:
- `internal/llm/providers/anthropic.go` - Prompt caching details
- `internal/llm/providers/openai.go` - Tool call chunking details  
- `internal/adaptors/terminal/display.go` - ANSI styling rules
```

**Rationale:**
- STATE.md should only contain cross-cutting concerns
- Single-component details belong in code comments
- Reduces cognitive load when checking STATE.md

---

### 4. Reduce Abstraction Layers (Medium Impact, Low Effort)

**Problem:** Unnecessary indirection in some areas.

**4a. Simplify stream package**

Current `stream/stream.go` has both `ChanInput` and `NopInput`/`NopOutput` which are rarely used.

**Proposed:** Remove `ReadCloser`, `WriteCloser`, `NopInput`, `NopOutput` - add them only when needed.

**4b. Simplify app package**

Current `app/app.go` has:
- `CreateAgent` (unused in terminal, only for WebSocket)
- `AgentFactory` (only for WebSocket)
- `CreateProvider` variants (deprecated aliases)
- Context helpers (unused)

**Proposed:** Remove unused helpers, keep only:
- `Setup()` - initialization
- `Config` struct
- `DefaultSystemPrompt`

**4c. Simplify errors package**

`internal/errors/` is 160 lines for a simple domain error type. Consider if this abstraction is needed or if standard errors would suffice.

---

### 5. Unify Model Config Types (Low Impact, Low Effort)

**Problem:** Multiple representations of model config:
- `agent.ModelConfig` in model_manager.go
- `agent.ModelInfo` for JSON serialization  
- `app.ProviderConfig` in app.go
- `factory.ProviderConfig` in llm/factory

**Proposed:** Create a single `ModelConfig` struct in `internal/config/` that all packages use.

---

### 6. Simplify TLV Tag Handling (Low Impact, Medium Effort)

**Problem:** Tags are strings defined in `stream/stream.go` but used as string literals throughout.

**Current:**
```go
// In stream/stream.go
const TagTextUser = "TU"

// In output.go - string comparison
switch tag {
case stream.TagTextAssistant:
    // ...
}
```

**Proposed:** Keep as-is. String tags are simple and the current approach works well. Type-safe enums would add complexity without clear benefit.

---

### 7. Improve Test Organization (Medium Impact, Low Effort)

**Problem:** Test files mirror the scattered source files.

**Proposed:** After consolidating source files, consolidate test files to match:
- `terminal_test.go` - tests for terminal.go
- `display_test.go` - tests for display.go  
- etc.

Move benchmark tests into main test files with `Benchmark` prefix.

---

### 8. Documentation Improvements (Low Impact, Low Effort)

**8a. Add package-level examples**

Each package should have an example in `example_test.go`:
```go
func ExampleAgent() {
    // Shows basic agent usage
}
```

**8b. Consolidate docs/**

Current `docs/` has 9 markdown files. Consider consolidating:
- `architecture.md` - Keep (comprehensive)
- `cli-reference.md` - Keep (user-facing)
- `sessions.md` + `skills.md` + `window-container.md` → Merge into user guide
- `error-handling.md` + `schema-improvements.md` → Archive (historical design docs)
- `terminal-controls.md` → Merge into README or CLI reference

---

## Implementation Priority

### Phase 1: Quick Wins (1-2 days)
1. Simplify STATE.md - move component-specific gotchas to code comments
2. Remove unused code in `app/app.go`
3. Archive historical docs

### Phase 2: Terminal Consolidation (3-5 days)
1. Merge terminal files according to proposed structure
2. Update test file organization
3. Ensure all tests pass

### Phase 3: Session Consolidation (2-3 days)
1. Merge session files according to proposed structure
2. Update tests

### Phase 4: Polish (1-2 days)
1. Unify model config types
2. Add package examples
3. Final documentation cleanup

---

## Metrics for Success

| Metric | Before | After | Target |
|--------|--------|-------|--------|
| Source files | 63 | ~45 | -30% |
| STATE.md lines | 77 | ~15 | -80% |
| Terminal files | 32 | 8 | -75% |
| Session files | 8 | 6 | -25% |
| Gotchas in STATE.md | 10 | 1 | -90% |

---

## Risks and Mitigations

1. **Risk:** Merging files creates large files that are harder to navigate
   - **Mitigation:** Each merged file should have clear section comments; use `// --- Section Name ---` separators

2. **Risk:** Breaking existing functionality during consolidation
   - **Mitigation:** Comprehensive test runs after each merge; keep git history clean for easy reverts

3. **Risk:** Conflicts with ongoing development
   - **Mitigation:** Implement in phases; each phase is a complete, working state

---

## Conclusion

This refactor plan focuses on **reducing file proliferation** and **making hidden knowledge explicit**. The architecture is sound - the main improvement opportunity is organization rather than fundamental redesign.

The terminal adaptor is the primary target for consolidation due to its complexity. Session consolidation is secondary. STATE.md simplification provides immediate cognitive benefit with minimal risk.