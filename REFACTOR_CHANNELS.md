# Channel Refactor Plan

Unnecessary channels add complexity and cognitive overhead. This plan systematically
replaces over-engineered channel usage with simpler synchronization primitives.

**Status Legend**: `[x]` done, `[ ]` todo, `[-]` in progress

---

## Completed

- [x] **Terminal outputWriter cleanup** (High priority)
  - Removed: `updateChan`, `done`, `pendingUpdate`, `updateMu`, `lastUpdate`
  - Removed: `updateFlusher` goroutine
  - Replaced with: `atomic.Bool` dirty flag
  - Files: `internal/adaptors/terminal/output.go`, `internal/adaptors/terminal/interfaces.go`
  - Commit: (done in latest)

---

## Completed Work

- [x] **Terminal outputWriter cleanup** (High priority)
  - Removed: `updateChan`, `done`, `pendingUpdate`, `updateMu`, `lastUpdate`
  - Removed: `updateFlusher` goroutine
  - Replaced with: `atomic.Bool` dirty flag
  - Files: `internal/adaptors/terminal/output.go`, `internal/adaptors/terminal/interfaces.go`

- [x] **Replace `taskAvailable` channel with `sync.Cond`** (Medium priority)

**Current**: Hand-rolled condition variable using buffered channel as "you-have-mail" flag.

**Location**: `internal/agent/session.go`

**Changes**:

- [x] Remove field: `taskAvailable chan struct{}`
- [x] Add field: `cond *sync.Cond`
- [x] Update constructor: `cond: sync.NewCond(&s.mu)` (shares same mutex)
- [x] Replace `signalTaskAvailable()`:
  ```go
  // Old
  select {
  case s.taskAvailable <- struct{}{}:
  default:
  }
  
  // New
  s.cond.Signal()
  ```
- [x] Replace `waitForNextTask()`:
  ```go
  // New
  s.mu.Lock()
  defer s.mu.Unlock()
  for len(s.taskQueue) == 0 {
      if s.sessionCtx.Err() != nil {
          return QueueItem{}, false
      }
      s.cond.Wait()
  }
  ```
- [x] Update all `signalTaskAvailable()` call sites
- [x] Run tests: `go test ./internal/agent/...`

**Benefits**: Simpler code, no channel allocation, atomic wait without unlock/select window.

**Effort**: Low (30 min)
**Risk**: Low (same semantics)

---

### 2. Replace `done` channel with `context.Context` (Medium priority) ✅ DONE

**Current**: `done chan struct{}` used alongside per-task contexts.

**Location**: `internal/agent/session.go`

**Changes**:

- [x] Remove field: `done chan struct{}`
- [x] Add fields:
  ```go
  sessionCtx    context.Context
  sessionCancel context.CancelFunc
  ```
- [x] Update constructor:
  ```go
  sessionCtx, sessionCancel := context.WithCancel(context.Background())
  ```
- [x] Replace duplicate-close protection in `readFromInput`:
  ```go
  // Old
  s.mu.Lock()
  select {
  case <-s.done:
  default:
      close(s.done)
  }
  s.mu.Unlock()
  
  // New (context.CancelFunc is idempotent)
  s.sessionCancel()
  s.cond.Signal()
  ```
- [x] Update `waitForNextTask` to check `s.sessionCtx.Err()`
- [x] Update `isDone()` and all other `case <-s.done:` consumers
- [x] Run tests: `go test ./internal/agent/...`

**Dependencies**: Done alongside #1 (taskAvailable refactor).

**Benefits**: Single cancellation mechanism, idempotent cancel, integrates with existing context usage.

**Effort**: Medium (1 hour)
**Risk**: Medium (touches session lifecycle)

---

### 3. Replace `taskDone` channel with `sync.WaitGroup` (Low priority) ✅ DONE

**Current**: Buffered channel used as one-shot "task finished" signal.

**Location**: `internal/agent/session.go`

**Changes**:

- [x] Remove field: `taskDone chan struct{}`
- [x] Add field: `taskWg sync.WaitGroup`
- [x] In `runTask`, add:
  ```go
  s.taskWg.Add(1)
  defer s.taskWg.Done()
  ```
- [x] In `cancelAllTasks`, replace:
  ```go
  // Old
  <-s.taskDone
  
  // New
  s.taskWg.Wait()
  ```
- [x] Remove the `select/default` lossy send in `runTask` deferred cleanup
- [x] Update tests in `queue_test.go` to use `taskWg.Add/Done` pattern
- [x] Run tests: `go test ./internal/agent/...`

**Benefits**: Purpose-built primitive for "wait for completion," no lossy signals.

**Effort**: Low (20 min)
**Risk**: Low (same semantics)

---

## Channels to Keep (No Changes)

These are correctly designed and should not be changed:

| Channel | Location | Reason |
|---------|----------|--------|
| `ChanInput.ch` | `stream/stream.go` | Core IO pipeline, cross-goroutine message passing |
| `eventChan` | `llm/providers/*.go` | HTTP streaming → consumer bridge |
| `<-chan StreamEvent` | `llm/types.go` | Provider interface contract |
| `runnerDone` | `agent/session.go` | Closed-channel broadcast for clean shutdown |
| `sigCh` | `adaptors/plainio/adaptor.go` | Required by `os/signal.Notify` |
| `exitCh` | `adaptors/plainio/adaptor.go` | Multi-goroutine exit code coordination |
| `errorCh` | `adaptors/plainio/output.go` | One-shot signal for `WaitForError()` |
| `done` | `tools/posix_shell.go` | Textbook `select` for subprocess cancellation |

---

## Future Consideration

### Iterator-based LLM streaming (Go 1.23+)

Once the project requires Go 1.23+, consider replacing:

```go
// Current
StreamMessages(...) (<-chan StreamEvent, error)

// Future (Go 1.23+)
StreamMessages(...) iter.Seq2[StreamEvent, error]
```

This would eliminate the provider channels entirely, but requires:
- [ ] Go 1.23+ in go.mod
- [ ] Refactor all provider implementations
- [ ] Update `processStreamEvents` to use range-over-func

**Priority**: Low (future optimization, not urgent)

---

## Testing Checklist

After each refactor:

- [ ] `go test ./internal/agent/...` — session tests pass
- [ ] `go test ./internal/adaptors/...` — adaptor tests pass
- [ ] Manual test: terminal UI with streaming response
- [ ] Manual test: cancel during streaming
- [ ] Manual test: cancel_all with queued tasks
- [ ] Manual test: plainio adaptor with error handling
- [ ] `go test -race ./...` — no race conditions

---

## Summary

| Item | Priority | Effort | Impact | Status |
|------|----------|--------|--------|--------|
| Terminal outputWriter | High | Medium | Remove 1 goroutine, 2 channels | ✅ Done |
| taskAvailable → Cond | Medium | Low | Cleaner wait pattern | ✅ Done |
| done → Context | Medium | Medium | Single cancellation source | ✅ Done |
| taskDone → WaitGroup | Low | Low | Simpler wait-for-completion | ✅ Done |

**Total channels removed**: 5 (3 remaining in session, 2 in terminal output)
**Total goroutines removed**: 1 (updateFlusher)
