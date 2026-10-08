package mcp

import "testing"

// Reconnecting clears the transport slot, and clearing it must be
// representable: the slot was an atomic.Value, which panics when stored nil
// ("sync/atomic: store of nil value into Value"). connectWithOAuthToken reaches
// resetState, so the state that panicked was one call away from the
// authorization flow — unreachable today only because Connect returns
// ErrNeedsAuth before it stores a transport, which is a property of the current
// call graph, not of this type.
func TestResetStateClosesAndClearsTheTransport(t *testing.T) {
	client := NewClient(ServerConfig{Name: "test"})
	mt := newMockTransport(nil)
	client.storeTransport(mt)

	client.resetState() // panicked before this change

	select {
	case <-mt.Done():
	default:
		t.Error("resetState left the transport open")
	}
	if got := client.loadTransport(); got != nil {
		t.Errorf("transport = %v after resetState, want nil", got)
	}
	if client.State() != StateDisconnected {
		t.Errorf("state = %v, want disconnected", client.State())
	}
}

// The same slot must accept a transport of another type. An atomic.Value
// demands one concrete type for its whole life, so a client that reconnected
// over a different transport kind would have panicked on the second store
// ("inconsistently typed value").
func TestTransportSlotAcceptsAnotherTransportType(t *testing.T) {
	client := NewClient(ServerConfig{Name: "test"})

	client.storeTransport(&StdioTransport{})
	if _, ok := client.loadTransport().(*StdioTransport); !ok {
		t.Fatalf("transport = %T, want the stdio transport", client.loadTransport())
	}

	client.storeTransport(nil)
	if got := client.loadTransport(); got != nil {
		t.Errorf("transport = %v after clearing, want nil", got)
	}

	client.storeTransport(newMockTransport(nil))
	if _, ok := client.loadTransport().(*mockTransport); !ok {
		t.Errorf("transport = %T, want the replacing transport", client.loadTransport())
	}
}
