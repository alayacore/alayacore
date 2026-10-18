package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
)

// TestConcurrentEditsToSameFileDoNotLoseUpdates is the regression test for the
// lost update fileMutationMu prevents. The concurrent tool runner starts one
// goroutine per call, so a model that emits several edit_file calls for one file
// in a single step runs them at once; without the lock each reads the same
// original bytes and every rename after the first silently discards the rest.
//
// Each edit replaces its own unique marker, so a lost update is observable as a
// marker that never became upper-case in the final content. All goroutines are
// released together to maximize overlap, which makes this a reliable guard
// against removing the lock — though, being a race, it can only fail
// probabilistically, never deterministically.
func TestConcurrentEditsToSameFileDoNotLoseUpdates(t *testing.T) {
	const edits = 16

	marker := func(i int) string { return fmt.Sprintf("marker-%02d", i) }

	var initial strings.Builder
	for i := 0; i < edits; i++ {
		initial.WriteString(marker(i))
		initial.WriteByte('\n')
	}

	path := filepath.Join(t.TempDir(), "vault.txt")
	if err := os.WriteFile(path, []byte(initial.String()), 0644); err != nil {
		t.Fatal(err)
	}

	// Hold every goroutine at the gate, then release them together so the
	// writes overlap as they do when the model emits them in one step.
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, edits)
	for i := 0; i < edits; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = executeEditFile(context.Background(), EditFileInput{
				Path:      path,
				OldString: marker(i),
				NewString: strings.ToUpper(marker(i)),
			})
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("edit %d failed: %v", i, err)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < edits; i++ {
		want := strings.ToUpper(marker(i))
		if !strings.Contains(string(data), want) {
			t.Errorf("update %q was lost — a concurrent edit overwrote it; final content:\n%s", want, data)
		}
	}
}

// TestReadFileWaitsForTheFileMutationLock guards the read side of the lock.
// edit_file's commit is a rename, which no reader can catch half-done, but
// write_file's in-place fallback (writeInPlace) truncates the target and then
// writes into it, all of it inside fileMutationMu. A read that ignored the lock
// could therefore return the new prefix as a complete, merely shorter file —
// and nothing in the result would say so.
//
// The stand-in for a mutation in flight is the lock itself: hold it, start a
// read, and require the read to still be waiting. Waiting 50ms can only be
// wrong in the direction of passing (a read that has not started is blocked
// too), so this is deterministic where the edit test above is probabilistic.
func TestReadFileWaitsForTheFileMutationLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	const content = "complete content\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	type result struct {
		parts []llm.ContentPart
		err   error
	}
	done := make(chan result, 1)

	fileMutationMu.Lock()
	go func() {
		parts, err := executeReadFile(context.Background(), ReadFileInput{Path: path})
		done <- result{parts, err}
	}()

	select {
	case got := <-done:
		fileMutationMu.Unlock()
		t.Fatalf("read_file finished while the mutation lock was held (err=%v) — it can land inside write_file's in-place truncate window and return a truncated file as if it were whole", got.err)
	case <-time.After(50 * time.Millisecond):
	}
	fileMutationMu.Unlock()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("read_file failed after the lock was released: %v", got.err)
		}
		if len(got.parts) != 1 {
			t.Fatalf("got %d parts, want 1: %#v", len(got.parts), got.parts)
		}
		if text, ok := got.parts[0].(*llm.TextPart); !ok || text.Text != content {
			t.Errorf("read_file returned %#v, want the file's content", got.parts[0])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("read_file did not resume after the mutation lock was released — it is not taking the lock, or it deadlocked")
	}
}

// TestReadFileDoesNotWaitForOtherReaders is the other half of the lock's
// contract: readers exclude mutations, not each other. A model that reads eight
// files in one step is the normal case, and with a plain Mutex those eight reads
// became one queue — measured at ~2.2x on 8x8MB, and pure loss everywhere,
// since nothing a read does can disturb another read.
//
// Holding the read lock and requiring the read to finish pins the mode: a read
// that took the write lock would block here forever.
func TestReadFileDoesNotWaitForOtherReaders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("other reader's content\n"), 0644); err != nil {
		t.Fatal(err)
	}

	fileMutationMu.RLock()
	defer fileMutationMu.RUnlock()

	done := make(chan error, 1)
	go func() {
		_, err := executeReadFile(context.Background(), ReadFileInput{Path: path})
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("read_file failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("read_file waited for another reader — a step that reads several files would run them one at a time; take the read lock, not the write lock")
	}
}
