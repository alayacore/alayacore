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

- [x] **Task 1.2**: Extract key binding configuration ✅
  - Created `keybinds.go` with declarative key binding system (174 lines)
  - Key constants for all key strings (avoids typos)
  - KeyBinding struct with descriptions for help generation
  - Grouped bindings: global, display, model-selector, queue-manager, confirm-dialog
  - Updated `keys.go` to use key constants

- [x] **Task 1.3**: Reduce `terminal.go` size ✅
  - Created `focus_manager.go` (113 lines) for focus state management
  - Moved focus functions from `keys.go` (352→281 lines) and `terminal.go` (324→293 lines)
  - Contains: toggleFocus, focusInput, focusDisplay, openModelSelector, openQueueManager, handleBlur, handleFocus

### Phase 2: Session Layer Refactoring (Priority: Medium)

- [x] **Task 2.1**: Extract streaming and output helpers ✅
  - Moved `trackUsage` and `cleanIncompleteToolCalls` from `session_prompt.go` (189→107 lines)
  - `session_output.go` now includes usage tracking and message cleanup (94→178 lines)
  - `session_prompt.go` is now focused on prompt processing logic

- [x] **Task 2.2**: Create `session_errors.go` ✅
  - Added `writeErrorf()` and `writeNotifyf()` formatted helpers in `session_output.go`
  - Updated all callers to use formatted helpers
  - Removed unused `fmt` imports from `session.go`, `session_commands.go`, `session_prompt.go`

- [x] **Task 2.3**: Create `command_registry.go` ✅
  - Registry pattern for commands with declarative registration
  - `Command` struct with name, description, usage, and handler
  - `CommandRegistry` with Register, Get, and List methods
  - `dispatchCommand` method for registry-based dispatch
  - Updated `handleCommandSync` to use registry

### Phase 3: Constants and Styles Organization (Priority: Low)

- [x] **Task 3.1**: Consolidate `constants.go` ✅
  - Added `InputPaddingH` and `SelectorMaxHeight` constants
  - Updated model_selector.go and input_component.go to use constants
  - Added `ColorSurface1` to color palette

- [x] **Task 3.2**: Refactor `styles.go` ✅
  - Moved all color constants to `constants.go`
  - `styles.go` now contains only style composition

### Phase 4: Interface Improvements (Priority: Medium)

- [ ] **Task 4.1**: Create `SessionInterface` for adaptors
  - Makes testing easier without real session

- [x] **Task 4.2**: Create `OutputWriter` interface ✅
  - Created `interfaces.go` with OutputWriter interface
  - Terminal now uses OutputWriter interface instead of concrete type
  - Added UpdateChan() and WindowBuffer() methods to outputWriter
  - Better testability for terminal adaptor tests

### Phase 5: Error Handling Standardization (Priority: Medium)

- [x] **Task 5.1**: Define domain errors in `internal/errors/errors.go` ✅
  - Created domain errors package with structured error types
  - Model errors: ErrModelNotFound, ErrModelManagerNotInitialized, ErrNoModelFilePath
  - Queue errors: ErrQueueItemNotFound
  - Session errors: ErrNoSessionFile, ErrFailedToSaveSession
  - Command errors: ErrEmptyCommand, ErrUnknownCommand, ErrNothingToCancel
  - SessionError type with operation context

- [ ] **Task 5.2**: Use structured errors
  - `SessionError` type with operation context

### Phase 6: Test Coverage Improvements (Priority: High)

- [ ] **Task 6.1**: Add terminal adaptor tests
  - `terminal_test.go`, `window_test.go`, `keys_test.go`

- [ ] **Task 6.2**: Add tool tests
  - `read_file_test.go`, `write_file_test.go`, `posix_shell_test.go`, `activate_skill_test.go`

- [x] **Task 6.3**: Add stream/TLV tests ✅
  - Created `internal/stream/stream_test.go`
  - Tests for EncodeTLV, ReadTLV, WriteTLV
  - Tests for ChanInput (emit, read, close, multiple messages)
  - Tests for unicode and long messages

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
| 1.2 | Extract key bindings | ✅ Done |
| 1.3 | Reduce terminal.go | ✅ Done |
| 2.1 | Extract streaming/output helpers | ✅ Done |
| 2.2 | Add formatted error helpers | ✅ Done |
| 2.3 | Create command_registry.go | ✅ Done |
| 3.1 | Consolidate constants.go | ✅ Done |
| 3.2 | Refactor styles.go | ✅ Done |
| 4.1 | Create SessionInterface | ⏳ Pending |
| 4.2 | Create OutputWriter interface | ✅ Done |
| 5.1 | Define domain errors | ✅ Done |
| 5.2 | Use structured errors | ⏳ Pending |
| 6.1 | Add terminal adaptor tests | ⏳ Pending |
| 6.2 | Add tool tests | ⏳ Pending (edit_file_test.go exists) |
| 6.3 | Add stream tests | ✅ Done |
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