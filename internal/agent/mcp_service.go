package agent

// MCP service: manages the MCP initialization lifecycle.
//
// Extracted from session_loop.go and session_io.go. Owns the MCP init
// state machine (mcp.Initializer) and the ready flag. Session delegates MCP
// operations (start, cancel, confirm, event handling) to this service.

import (
	"context"
	"io"
	"sync/atomic"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/mcp"
	"github.com/alayacore/alayacore/internal/protocol"
)

// mcpService manages MCP server initialization lifecycle.
// Thread-safe: all public methods are safe to call from any goroutine.
type mcpService struct {
	initializer *mcp.Initializer
	ready       atomic.Bool

	// Output writer for system messages.
	// Set by Session during construction; must not be nil if MCP is configured.
	output io.Writer
}

// newMCPService creates an mcpService. If initializer is nil, MCP is not
// configured and IsReady() always returns true.
func newMCPService(initializer *mcp.Initializer, output io.Writer) *mcpService {
	s := &mcpService{
		initializer: initializer,
		output:      output,
	}
	if initializer == nil {
		s.ready.Store(true)
	}
	return s
}

// Start begins MCP initialization. No-op if MCP is not configured.
func (m *mcpService) Start(ctx context.Context) {
	if m.initializer != nil {
		m.initializer.Start(ctx)
	}
}

// Events returns the channel of MCP initialization events.
// Returns nil if MCP is not configured.
func (m *mcpService) Events() <-chan mcp.InitEvent {
	if m.initializer == nil {
		return nil
	}
	return m.initializer.Events()
}

// IsReady returns true if MCP initialization has completed (or was not configured).
func (m *mcpService) IsReady() bool {
	return m.ready.Load()
}

// HasInit returns true if MCP servers are configured.
func (m *mcpService) HasInit() bool {
	return m.initializer != nil
}

// Cancel aborts the entire MCP initialization.
func (m *mcpService) Cancel() {
	if m.initializer != nil {
		m.initializer.Cancel()
	}
}

// SendAuthCodeResult forwards the adapter's response (confirm + optional auth code)
// to the MCP init goroutine waiting in runOAuthForServer.
// code == "" means user declined; code != "" means confirmed + here's the code.
// iss is the RFC 9207 issuer parameter from the authorization response,
// if the callback carried one (empty on the manual path).
func (m *mcpService) SendAuthCodeResult(server, code, redirectURI, iss string) bool {
	if m.initializer == nil {
		return false
	}
	return m.initializer.SendAuthCodeResult(server, code, redirectURI, iss)
}

// ============================================================================
// Event Handling
// ============================================================================

// mcpEventResult describes what the Session should do after processing an event.
type mcpEventResult struct {
	SystemMsg *mcpMsgData

	ApplyResult bool
	Tools       []llm.Tool
	SysFragment string
	Manager     *mcp.Manager

	Aborted bool
}

// mcpMsgData carries the data for an MCP system message.
type mcpMsgData struct {
	Status string
	Server string
	URL    string
	Error  string
}

// HandleEvent processes a single MCP initialization event.
// It updates internal state (mcpReady) and returns the actions the Session
// should take (send system messages, apply tools, etc.).
func (m *mcpService) HandleEvent(evt *mcp.InitEvent) *mcpEventResult {
	if m.ready.Load() {
		// Already done — ignore stale events.
		return nil
	}

	switch evt.Type {
	case mcp.InitConnecting, mcp.InitConnected:
		return &mcpEventResult{
			SystemMsg: &mcpMsgData{
				Status: string(evt.Type),
				Server: evt.Server,
			},
		}

	case mcp.InitFailed:
		return &mcpEventResult{
			SystemMsg: &mcpMsgData{
				Status: string(evt.Type),
				Server: evt.Server,
				Error:  evt.Error,
			},
		}

	case mcp.InitAuthConfirm:
		return &mcpEventResult{
			SystemMsg: &mcpMsgData{
				Status: "auth_required",
				Server: evt.Server,
				URL:    evt.URL,
			},
		}

	case mcp.InitAuthRunning:
		return &mcpEventResult{
			SystemMsg: &mcpMsgData{
				Status: string(evt.Type),
				Server: evt.Server,
				Error:  evt.Error,
			},
		}

	case mcp.InitDone:
		m.ready.Store(true)
		return &mcpEventResult{
			SystemMsg:   &mcpMsgData{Status: "done"},
			ApplyResult: true,
			Tools:       evt.Tools,
			SysFragment: evt.SysFragment,
			Manager:     evt.Manager,
		}

	case "canceled":
		m.ready.Store(true)
		return &mcpEventResult{
			SystemMsg: &mcpMsgData{Status: "done"},
			Aborted:   true,
		}
	}

	return nil
}

// MarkAborted is called when the MCP events channel closes unexpectedly
// (without a clean "done" or "canceled" event). Sets mcpReady to true
// so the user can proceed even if MCP init was incomplete.
func (m *mcpService) MarkAborted() {
	if !m.ready.Load() {
		m.ready.Store(true)
	}
}

// sendSystemMsg writes an MCP system message to the output writer.
func (m *mcpService) sendSystemMsg(data *mcpMsgData) {
	if m.output == nil {
		return
	}
	_ = protocol.WriteSystemMsg(m.output, mcpMsg{
		Status: data.Status,
		Server: data.Server,
		URL:    data.URL,
		Error:  data.Error,
	})
}
