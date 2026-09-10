package tools

import "sync"

// fileMutationMu serializes every in-process file mutation: edit_file and
// write_file both take it, so at most one file mutation runs in the process at
// any moment.
//
// It exists because the concurrent tool runner (the default) launches one
// goroutine per tool call the moment its arguments complete, so a model that
// emits two edit_file calls for the same file in one step runs them in
// parallel. Both read the same original bytes and both rename a temp file over
// the target, so the second rename silently discards the first edit — a lost
// update neither the model nor the user is told about. The model emitting the
// pair is not something the user can prevent, so the fix belongs here, in the
// one place both tools pass through, rather than in a prompt.
//
// write_file takes the same lock chiefly to protect edit_file: a full write that
// landed between an edit's read and its rename would be swallowed by that
// rename. Its in-place fallback needs it for its own sake too — two O_TRUNC
// writers at offset 0 can interleave.
//
// It is deliberately one coarse lock, not a map keyed by path. Path is not a
// sound file identity — hard links and case-insensitive filesystems both map
// two different path strings to one file — so a keyed table would let exactly
// the writes it is meant to stop slip through, while adding an unbounded map
// that must then be pruned safely. The critical section is a few file syscalls,
// so there is nothing to gain from finer granularity and no state to get wrong.
//
// It is NOT reentrant: a mutating file tool must never call another. Today
// neither edit_file nor write_file calls the other, and the fallbacks
// (executeEditFileTolerant, writeInPlace) operate on a session already under the
// lock rather than re-entering it.
//
// Scope: this serializes writes within this process only. Concurrent writers
// outside it — another instance, an editor, git, a sync client, or a shell
// command run by execute_command — are not detected; a competing write still
// wins last. See docs/tool-execution.md.
var fileMutationMu sync.Mutex
