package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
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
