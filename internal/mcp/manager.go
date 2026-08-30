package mcp

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// Manager manages multiple MCP server connections.
// It provides a unified interface for tool discovery across all servers.
//
// CONCURRENCY: clients slice is stored in an atomic.Value and replaced
// atomically on CloseAll. All read paths snapshot the slice and then
// operate on that snapshot, avoiding TOCTOU races and eliminating
// the need for a mutex on the hot path.
type Manager struct {
	clients atomic.Value // stores []*Client or nil
	closed  atomic.Bool
}

// NewManager creates an MCP manager from server configurations.
func NewManager(configs []ServerConfig) *Manager {
	m := &Manager{}
	clients := make([]*Client, 0, len(configs))
	for _, cfg := range configs {
		clients = append(clients, NewClient(cfg))
	}
	m.clients.Store(clients)
	return m
}

// loadClients returns a snapshot of the clients slice.
func (m *Manager) loadClients() []*Client {
	v := m.clients.Load()
	if v == nil {
		return nil
	}
	clients, ok := v.([]*Client)
	if !ok {
		return nil
	}
	return clients
}

// CallTool invokes a tool on the specified server.
func (m *Manager) CallTool(ctx context.Context, serverName, toolName string, arguments []byte) (*CallToolResult, error) {
	client := m.findClient(serverName)
	if client == nil {
		return nil, fmt.Errorf("mcp server %q not found", serverName)
	}
	return client.CallTool(ctx, toolName, arguments)
}

// ReadResource reads a resource by URI from the specified server.
func (m *Manager) ReadResource(ctx context.Context, serverName, uri string) (*ReadResourceResult, error) {
	client := m.findClient(serverName)
	if client == nil {
		return nil, fmt.Errorf("mcp server %q not found", serverName)
	}
	return client.ReadResource(ctx, uri)
}

// GetPrompt fetches a prompt by name from the specified server.
func (m *Manager) GetPrompt(ctx context.Context, serverName, name string, args map[string]string) (*GetPromptResult, error) {
	client := m.findClient(serverName)
	if client == nil {
		return nil, fmt.Errorf("mcp server %q not found", serverName)
	}
	return client.GetPrompt(ctx, name, args)
}

// CloseAll shuts down all MCP client connections.
//
// Closes concurrently: each stdio transport escalates stdin-EOF → SIGTERM →
// SIGKILL with 2s + 3s of patience, so a server that ignores SIGTERM costs
// 5 seconds. Serializing those waits made quitting alayacore hang for 5s per
// misbehaving server (20s observed with four); in parallel the budget is 5s
// regardless of how many servers are configured.
func (m *Manager) CloseAll() {
	if !m.closed.CompareAndSwap(false, true) {
		return
	}

	clients := m.loadClients()
	var wg sync.WaitGroup
	for _, c := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Close()
		}()
	}
	wg.Wait()

	m.clients.Store([]*Client(nil))
}

// ActiveServerCount returns the number of connected (StateReady) servers.
func (m *Manager) ActiveServerCount() int {
	count := 0
	for _, c := range m.loadClients() {
		if c.State() == StateReady {
			count++
		}
	}
	return count
}

// Clients returns a snapshot of all managed clients.
func (m *Manager) Clients() []*Client {
	clients := m.loadClients()
	if clients == nil {
		return nil
	}
	result := make([]*Client, len(clients))
	copy(result, clients)
	return result
}

// findClient looks up a client by server name from a snapshot.
func (m *Manager) findClient(name string) *Client {
	for _, c := range m.loadClients() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}
