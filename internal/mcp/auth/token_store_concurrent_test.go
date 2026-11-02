package auth

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// A token file is shared by every instance started from the same config
// directory, and FileTokenStore's mutex belongs to one instance only. Two
// instances refreshing the same server's token must not leave a file that cannot
// be read: a torn one reports a parse error on the next start and sends the user
// through authorization again for no reason.
//
// Several writers, because one writer's window is short enough to miss by luck;
// interleaved ones keep the file empty or half-written for a large share of the
// time.
func TestFileTokenStoreConcurrentInstancesNeverLeaveUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	first := NewFileTokenStore(dir)
	second := NewFileTokenStore(dir)

	long := &Token{
		AccessToken:  "token-with-a-longer-value",
		TokenType:    "Bearer",
		RefreshToken: "refresh-1",
		Scopes:       []string{"read", "write"},
	}
	short := &Token{AccessToken: "t", TokenType: "Bearer"}

	if err := first.SaveToken("server", long); err != nil {
		t.Fatal(err)
	}

	const writers, readers, perWriter = 4, 2, 50

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
			// A store of its own: a shared instance's mutex would serialize the
			// reader against the writer and hide what this test is about.
			reader := NewFileTokenStore(dir)
			for {
				select {
				case <-stop:
					return
				default:
				}
				tok, err := reader.LoadToken("server")
				if err != nil {
					fail(fmt.Errorf("load: %w", err))
					return
				}
				if tok == nil {
					fail(errors.New("the token disappeared mid-write"))
					return
				}
			}
		}()
	}

	for w := 0; w < writers; w++ {
		writersDone.Add(1)
		go func(id int) {
			defer writersDone.Done()
			store := first
			if id%2 == 1 {
				store = second
			}
			for i := 0; i < perWriter; i++ {
				tok := long
				if i%2 == 1 {
					tok = short
				}
				if err := store.SaveToken("server", tok); err != nil {
					fail(fmt.Errorf("save: %w", err))
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
