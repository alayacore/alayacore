package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestWriteReplacesWholeContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.conf")
	if err := Write(path, []byte("active_model: \"a very long model name\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Shorter than what is there: a truncating writer that dropped bytes would
	// leave the previous tail behind.
	if err := Write(path, []byte("active_model: \"b\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "active_model: \"b\"\n"; got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
}

func TestWriteEmptyContentTruncates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.conf")
	if err := Write(path, []byte("something\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Errorf("size = %d, want 0", info.Size())
	}
}

// A write must not leave anything behind next to the target, on either outcome:
// a stranded temp file in the config directory would be read as configuration by
// nothing and by a user as damage.
func TestWriteLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtime.conf")
	if err := Write(path, []byte("ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Failure path: a directory as the target.
	if err := Write(dir, []byte("nope"), 0o600); err == nil {
		t.Fatal("writing over a directory succeeded")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 1 || names[0] != "runtime.conf" {
		t.Errorf("files left in %s: %v, want only runtime.conf", dir, names)
	}
}

func TestWriteToDirectoryFails(t *testing.T) {
	dir := t.TempDir()
	err := Write(dir, []byte("x"), 0o600)
	if err == nil {
		t.Fatal("writing over a directory succeeded")
	}
	if !strings.Contains(err.Error(), "it is a directory") {
		t.Errorf("error = %v, want it to say the target is a directory", err)
	}
	info, statErr := os.Stat(dir)
	if statErr != nil || !info.IsDir() {
		t.Error("the directory was replaced")
	}
}

// The reason this package exists, stated as an assertion: a reader running
// alongside writers sees a whole file every time, one version or the other.
// With the previous in-place write the reader caught an empty file or a prefix,
// and with two writers it could catch a mix of both contents.
//
// Several writers, not one: the truncating write's window is short, and a single
// writer plus a single reader can miss it by luck. Interleaved writers keep the
// file empty or half-written for a large share of the time — measured on this
// machine, the same loop against a truncating writer reports a torn read in
// roughly half of its iterations.
func TestWriteIsNeverObservedHalfApplied(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.conf")
	seed := "active_model: \"seed\"\n"
	if err := Write(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	// Different lengths, so a writer that dropped bytes instead of replacing the
	// file would leave a tail of the previous content behind.
	long := "active_model: \"a much longer model name\"\n" + strings.Repeat("x", 2000) + "\n"
	short := "active_model: \"b\"\n"
	whole := map[string]bool{seed: true, long: true, short: true}

	const writers, readers, perWriter = 4, 2, 80

	stop := make(chan struct{})
	var writersDone, readersDone sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	fail := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
	}

	for r := 0; r < readers; r++ {
		readersDone.Add(1)
		go func() {
			defer readersDone.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				data, err := os.ReadFile(path)
				if err != nil {
					fail(fmt.Errorf("read: %w", err))
					return
				}
				if !whole[string(data)] {
					fail(fmt.Errorf("observed a partially written file (%d bytes): %q", len(data), data))
					return
				}
			}
		}()
	}

	for w := 0; w < writers; w++ {
		writersDone.Add(1)
		go func(id int) {
			defer writersDone.Done()
			for i := 0; i < perWriter; i++ {
				content := long
				if (i+id)%2 == 1 {
					content = short
				}
				if err := Write(path, []byte(content), 0o600); err != nil {
					fail(fmt.Errorf("write: %w", err))
					return
				}
			}
		}(w)
	}

	writersDone.Wait()
	close(stop)
	readersDone.Wait()

	if firstErr != nil {
		t.Fatal(firstErr)
	}
}
