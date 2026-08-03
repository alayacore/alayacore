package agent

// MCP service: manages the MCP initialization lifecycle.
//
// Extracted from session_loop.go and session_io.go. Owns the MCP init
// state machine (mcp.Init) and the ready flag. Session delegates MCP
// operations (start, cancel, confirm, event handling) to this service.

import (
	"context"
	"io"
	"sync/atomic"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/mcp"
	"github.com/alayacore/alayacore/internal/protocol"
)

// MCPService manages MCP server initialization lifecycle.
// Thread-safe: all public methods are safe to call from any goroutine.
type MCPService struct {
	init  *mcp.Init
	ready atomic.Bool

	// Output writer for system messages.
	// Set by Session during construction; must not be nil if MCP is configured.
	output io.Writer
}

// NewMCPService creates an MCPService. If init is nil, MCP is not configured
// and IsReady() always returns true.
func NewMCPService(init *mcp.Init, output io.Writer) *MCPService {
	s := &MCPService{
		init:   init,
		output: output,
	}
	if init == nil {
		s.ready.Store(true)
	}
	return s
}

// Start begins MCP initialization. No-op if MCP is not configured.
func (m *MCPService) Start(ctx context.Context) {
	if m.init != nil {
		m.init.Start(ctx)
	}
}

// Events returns the channel of MCP initialization events.
// Returns nil if MCP is not configured.
func (m *MCPService) Events() <-chan mcp.InitEvent {
	if m.init == nil {
		return nil
	}
	return m.init.Events()
}

// IsReady returns true if MCP initialization has completed (or was not configured).
func (m *MCPService) IsReady() bool {
	return m.ready.Load()
}

// HasInit returns true if MCP servers are configured.
func (m *MCPService) HasInit() bool {
	return m.init != nil
}

// Cancel aborts the entire MCP initialization.
func (m *MCPService) Cancel() {
	if m.init != nil {
		m.init.Cancel()
	}
}

// SendAuthCodeResult forwards the adapter's response (confirm + optional auth code)
// to the MCP init goroutine waiting in runOAuthForServer.
// code == "" means user declined; code != "" means confirmed + here's the code.
// iss is the RFC 9207 issuer parameter from the authorization response,
// if the callback carried one (empty on the manual path).
func (m *MCPService) SendAuthCodeResult(server, code, redirectURI, iss string) bool {
	if m.init == nil {
		return false
	}
	return m.init.SendAuthCodeResult(server, code, redirectURI, iss)
}

// ============================================================================
// Event Handling
// ============================================================================

// MCPEventResult describes what the Session should do after processing an event.
type MCPEventResult struct {
	SystemMsg *MCPMsgData

	ApplyResult bool
	Tools       []llm.Tool
	SysFragment string
	Manager     *mcp.Manager

	Aborted bool
}

// MCPMsgData carries the data for an MCP system message.
type MCPMsgData struct {
	Status string
	Server string
	URL    string
	Error  string
}

// HandleEvent processes a single MCP initialization event.
// It updates internal state (mcpReady) and returns the actions the Session
// should take (send system messages, apply tools, etc.).
func (m *MCPService) HandleEvent(evt *mcp.InitEvent) *MCPEventResult {
	if m.ready.Load() {
		// Already done — ignore stale events.
		return nil
	}

	switch evt.Type {
	case mcp.InitConnecting, mcp.InitConnected:
		return &MCPEventResult{
			SystemMsg: &MCPMsgData{
				Status: string(evt.Type),
				Server: evt.Server,
			},
		}

	case mcp.InitFailed:
		return &MCPEventResult{
			SystemMsg: &MCPMsgData{
				Status: string(evt.Type),
				Server: evt.Server,
				Error:  evt.Error,
			},
		}

	case mcp.InitAuthConfirm:
		return &MCPEventResult{
			SystemMsg: &MCPMsgData{
				Status: "auth_required",
				Server: evt.Server,
				URL:    evt.URL,
			},
		}

	case mcp.InitAuthRunning:
		return &MCPEventResult{
			SystemMsg: &MCPMsgData{
				Status: string(evt.Type),
				Server: evt.Server,
				Error:  evt.Error,
			},
		}

	case mcp.InitDone:
		m.ready.Store(true)
		return &MCPEventResult{
			SystemMsg:   &MCPMsgData{Status: "done"},
			ApplyResult: true,
			Tools:       evt.Tools,
			SysFragment: evt.SysFragment,
			Manager:     evt.Manager,
		}

	case "canceled":
		m.ready.Store(true)
		return &MCPEventResult{
			SystemMsg: &MCPMsgData{Status: "done"},
			Aborted:   true,
		}
	}

	return nil
}

// MarkAborted is called when the MCP events channel closes unexpectedly
// (without a clean "done" or "canceled" event). Sets mcpReady to true
// so the user can proceed even if MCP init was incomplete.
func (m *MCPService) MarkAborted() {
	if !m.ready.Load() {
		m.ready.Store(true)
	}
}

// sendSystemMsg writes an MCP system message to the output writer.
func (m *MCPService) sendSystemMsg(data *MCPMsgData) {
	if m.output == nil {
		return
	}
	_ = protocol.WriteSystemMsg(m.output, MCPMsg{
		Status: data.Status,
		Server: data.Server,
		URL:    data.URL,
		Error:  data.Error,
	})
}
