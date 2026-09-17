// Package agent provides the core session management for AlayaCore.
//
// The agent package implements the session layer that sits between the
// adapters (terminal/plainio/terseio) and the AI model provider. It handles:
//
//   - Prompt execution and task management
//   - Model interaction and streaming
//   - Context management and auto-summarization (--auto-summarize=threshold)
//   - Command processing (:save, :model_set, :model_load, etc.)
//   - Session persistence (save/load conversations)
//
// Data Model:
//
//	The session stores conversation history as a flat, ordered slice of
//	ContentPart, where each item has a stable ID matching the TLV history ID
//	sent to the adapter. This lets the adapter reference one content block by
//	ID (e.g. ":save 5") with no secondary index. Each ContentPart carries its
//	own Role, so provider conversion groups consecutive same-role parts into
//	API messages on the fly.
//
// Architecture:
//
//	Session wires together the model service, tools, IO streams and MCP.
//	Sub-services (modelService, mcpService, persistenceService,
//	commandRegistry) own distinct concerns and are composed by the Session
//	struct. The parts that span the package boundary — the three-goroutine
//	concurrency model (run / task worker / inputPump), which state each
//	goroutine owns, and how the active model is resolved — are documented
//	where the adapters and the CLI can see them too, so they are not restated
//	here: see docs/architecture.md ("Session Layer") and
//	docs/configuration.md ("Model Selection Priority").
//
// Communication Protocol:
//
//	Adapters talk to Session over one TLV (Tag-Length-Value) byte stream. The
//	tag vocabulary (UT/UE/UI/UV/UA/UD in; AT/AR/AF/UF/CO/SM out) and the
//	frame-order contract live in docs/development-principles.md. Each frame's
//	history ID prefix corresponds to ContentPart.GetHistoryID(); those IDs are
//	a stream concern only — the session file stores none, and loading re-issues
//	them 1..N in file order (see docs/architecture.md, "Session Persistence").
//
// Usage:
//
//	pr, pw := io.Pipe()
//	cfg := agent.SessionConfig{Input: pr, Output: output, ...}
//	session, _, err := agent.LoadOrNewSession(cfg)
//	session.Start()
package agent
