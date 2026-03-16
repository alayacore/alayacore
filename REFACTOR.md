# AlayaCore Refactor Plan

## Executive Summary

The project is well-structured (~11,500 lines of Go code) with clear separation of concerns. The architecture follows a clean pattern:
- **Adaptors** (terminal/websocket) → **Session** → **Agent** → **Tools**
- TLV protocol for communication between layers

---

## Task List

### Phase 1: Terminal Adaptor Refactoring (Priority: High)

- [x] **Task 1.1**: Split `window.go` into focused modules ✅
  - `window.go` - Window struct and basic operations (236 lines)
  - `window_render.go` - Rendering logic (191 lines)
  - `window_diff.go` - Diff display functionality (140 lines)
  - `window_scroll.go` - Virtual scrolling and cursor management (158 lines)

- [ ] **Task 1.2**: Extract key binding configuration
  - Create `keybinds.go` with declarative key binding system
  - Reduce repetitive patterns in `keys.go`

- [ ] **Task 1.3**: Reduce `terminal.go` size
  - Extract `focus_manager.go` for focus state management
  - Move focus-related logic from terminal.go

### Phase 2: Session Layer Refactoring (Priority: Medium)

- [ ] **Task 2.1**: Extract `session_streaming.go`
  - Move streaming-related code from `session_prompt.go`
  - `handleStreamingOutput()`, `handleToolCall()`, streaming error handling

- [ ] **Task 2.2**: Create `session_errors.go`
  - Centralize error handling patterns
  - `writeError()`, `writeErrorf()`, `writeNotify()`

- [ ] **Task 2.3**: Create `command_registry.go`
  - Registry pattern for commands
  - Declarative command registration

### Phase 3: Constants and Styles Organization (Priority: Low)

- [ ] **Task 3.1**: Consolidate `constants.go`
  - Move all magic numbers and timing constants
  - Move color palette definitions

- [ ] **Task 3.2**: Refactor `styles.go`
  - Keep only style composition
  - Remove color definitions (move to constants.go)

### Phase 4: Interface Improvements (Priority: Medium)

- [ ] **Task 4.1**: Create `SessionInterface` for adaptors
  - Makes testing easier without real session

- [ ] **Task 4.2**: Create `OutputWriter` interface
  - Abstract output writer for better testability

### Phase 5: Error Handling Standardization (Priority: Medium)

- [ ] **Task 5.1**: Define domain errors in `internal/errors/errors.go`
  - `ErrSessionNotFound`, `ErrModelNotLoaded`, etc.

- [ ] **Task 5.2**: Use structured errors
  - `SessionError` type with operation context

### Phase 6: Test Coverage Improvements (Priority: High)

- [ ] **Task 6.1**: Add terminal adaptor tests
  - `terminal_test.go`, `window_test.go`, `keys_test.go`

- [ ] **Task 6.2**: Add tool tests
  - `read_file_test.go`, `write_file_test.go`, `posix_shell_test.go`, `activate_skill_test.go`

- [ ] **Task 6.3**: Add stream/TLV tests
  - `stream_test.go`

### Phase 7: Documentation Improvements (Priority: Low)

- [ ] **Task 7.1**: Add package documentation (`doc.go` for each package)

- [ ] **Task 7.2**: Create `docs/architecture.md`
  - Data flow diagram
  - Component interaction diagram
  - TLV protocol specification

### Phase 8: Code Quality Improvements (Priority: Low)

- [ ] **Task 8.1**: Add golangci-lint configuration (`.golangci.yml`)

- [ ] **Task 8.2**: Add pre-commit hooks or Makefile targets

### Phase 9: Potential Architectural Changes (Future)

- [ ] **Task 9.1**: Extract `internal/tlv` package

- [ ] **Task 9.2**: Create `internal/protocol` package for message types

---

## Progress Tracking

| Phase | Task | Status |
|-------|------|--------|
| 1.1 | Split window.go | ✅ Done |
| 1.2 | Extract key bindings | ⏳ Pending |
| 1.3 | Reduce terminal.go | ⏳ Pending |
| 2.1 | Extract session_streaming.go | ⏳ Pending |
| 2.2 | Create session_errors.go | ⏳ Pending |
| 2.3 | Create command_registry.go | ⏳ Pending |
| 3.1 | Consolidate constants.go | ⏳ Pending |
| 3.2 | Refactor styles.go | ⏳ Pending |
| 4.1 | Create SessionInterface | ⏳ Pending |
| 4.2 | Create OutputWriter interface | ⏳ Pending |
| 5.1 | Define domain errors | ⏳ Pending |
| 5.2 | Use structured errors | ⏳ Pending |
| 6.1 | Add terminal adaptor tests | ⏳ Pending |
| 6.2 | Add tool tests | ⏳ Pending |
| 6.3 | Add stream tests | ⏳ Pending |
| 7.1 | Add package documentation | ⏳ Pending |
| 7.2 | Create architecture docs | ⏳ Pending |
| 8.1 | Add golangci-lint config | ⏳ Pending |
| 8.2 | Add pre-commit hooks | ⏳ Pending |

---

## Summary Table

| Area | Priority | Effort | Impact |
|------|----------|--------|--------|
| Window.go split | High | Medium | High |
| Test coverage | High | High | High |
| Session refactoring | Medium | Medium | Medium |
| Interface extraction | Medium | Medium | High |
| Error standardization | Medium | Low | Medium |
| Constants organization | Low | Low | Low |
| Documentation | Low | Low | Low |