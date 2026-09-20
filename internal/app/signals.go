package app

import "os"

// WatchSignals runs a minimal signal loop for an adapter: onSignal is
// invoked for each delivery on sigCh, and the loop returns when sigCh is
// closed or done fires (the session has exited, so there is nothing left to
// cancel). Adapters use it to react to SIGINT without a hand-rolled select
// that competes with the rest of Start.
func WatchSignals(sigCh <-chan os.Signal, done <-chan struct{}, onSignal func()) {
	for {
		select {
		case _, ok := <-sigCh:
			if !ok {
				return
			}
			onSignal()
		case <-done:
			return
		}
	}
}
