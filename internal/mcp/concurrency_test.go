package mcp

import (
	"context"
	"os"
	"strconv"
	"sync"
	"testing"
)

// TestMarkStaleIsRaceFreeWithStateError pins the absence of a race between
// MarkStale — called on the transport's read-loop goroutine when a
// *_list_changed notification arrives — and stateError, called from whatever
// goroutine is making a request. staleReason is published through an atomic
// pointer for exactly this reason; revert that and -race reports the race.
func TestMarkStaleIsRaceFreeWithStateError(t *testing.T) {
	c := NewClient(ServerConfig{Name: "test-server"})
	c.state.Store(int32(StateStale))

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 10000; i++ {
			c.MarkStale("server tool list changed")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 10000; i++ {
			_ = c.stateError("list tools")
		}
	}()
	wg.Wait()
}

// TestSetNotificationHandlerDuringReadLoop pins the absence of a race between
// SetNotificationHandler — which the client calls after NewStdioTransport has
// already started readLoop — and readLoop reading the handler on every line.
// The handler is stored in an atomic pointer for this reason; revert that and
// -race reports the race.
func TestSetNotificationHandlerDuringReadLoop(t *testing.T) {
	if os.Getenv("MCP_TEST_SERVER") == "1" {
		return // running as the test server subprocess
	}

	transport := newStdioTestTransport(t, nil, "")
	waitForReady(t, transport)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				transport.SetNotificationHandler(func(string) {})
			}
		}
	}()

	for i := 0; i < 25; i++ {
		if _, err := transport.SendReceive(context.Background(), jsonrpcRequest{
			JSONRPC: "2.0", ID: requestID(strconv.Itoa(i)), Method: "test/x",
		}); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	close(stop)
	wg.Wait()
}
