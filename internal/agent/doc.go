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
//	sent to the adapter. This enables the adapter to reference individual
//	content blocks by ID (e.g. ":save 5") without any secondary index.
//	Each ContentPart carries its own Role, so provider conversion functions
//	group consecutive same-role parts into API messages on the fly.
//
// Concurrency Model:
//
//	The session uses an actor model with three goroutines:
//	  1. run() — owns all mutable session state (Contents, active task,
//	     token counts). Processes input messages and task events.
//	     When the input stream reaches EOF while a task is in progress,
//	     it drains remaining events until the task completes before
//	     exiting (see drainUntilTaskDone).
//	  2. task goroutine — spawned per task, runs in background, sends
//	     state mutations via typed channel events (taskEventCh) to run().
//	     On completion it sends the full ContentParts list back via taskResultCh.
//	  3. inputPump — reads TLV frames from input, forwards to run()
//	     via a message channel.
//
//	The only mutable state accessed from more than one goroutine are:
//	  - atomic fields for outputBroken
//	  - confirmChs map + confirmMu mutex for tool confirmation channels
//	  - A few buffered channels for cancellation, completion signaling,
//	    and system-info refresh.
//
//	MCP initialization is managed by mcpService (internal/agent/mcp_service.go),
//	which wraps mcp.Initializer and owns the ready flag. The session reads events
//	from mcpService.Events() in its main loop and reacts accordingly —
//	showing confirm dialogs for OAuth, applying tools on completion.
//	Adapter communication goes through TLV frames.
//
//	All other session state (Contents, ContextTokens, ContextLimit,
//	histCounter) is owned by a single goroutine and accessed without
//	synchronization.
//
//	Cross-goroutine
//	communication is exclusively through channels and atomics.
//
// Architecture Overview:
//
//	Session wires together the model service, tools, IO streams, and MCP.
//	Sub-services (modelService, mcpService, persistenceService, commandRegistry)
//	own distinct concerns and are composed by the Session struct.
//
//	The active model is resolved by priority (highest first):
//
//	  --model CLI flag
//	  session file frontmatter  (when loading via --session)
//	  runtime.conf              (global default)
//	  model.conf first entry    (fallback)
//
//	Model switching is scoped: sessions with a file-specified model
//	store switches in-memory (saved to the session file on :save),
//	while sessions without one write to the global runtime.conf.
//
//	  --model flag ──────────────────────┐
//	                                     │
//	  session file ──▶ sessionMeta ──────┤ modelService.ResolveActiveModel()
//	                                     │
//	  runtime.conf ──▶ runtimeManager ───┤
//	                                     │
//	  model.conf ────▶ modelManager ─────┤
//	                                     │
//	                                     ▼
//	                               modelService.ActiveModel()
//
// Communication Protocol:
//
//	Adapters communicate with Session via TLV (Tag-Length-Value) streams:
//	  - Input: TagUserT for prompts, TagCommandIn (CI) for commands,
//	    TagUserI for images, TagUserV for videos, TagUserA for audio,
//	    TagUserD for documents
//	  - Output: TagAssistantT, TagAssistantR, TagAssistantF, TagUserF,
//	    TagUserFDelta (Uf, ephemeral tool previews), TagCommandOut (CO), etc.
//
//	Each TLV frame carries a NUL-delimited history ID prefix that the
//	adapter uses to route content to display windows. These IDs correspond
//	directly to ContentPart.GetHistoryID() in the session's content store.
//
// Key Components:
//
//   - Session: Main session struct managing conversation state
//   - ContentPart: Atomic unit of conversation content with stable ID
//   - modelManager: Loads and manages AI model configurations.
//     Rejects models with invalid protocol_type, base_url, or model_name.
//     Use GetLoadErrors() to retrieve validation messages.
//   - runtimeManager: Persists runtime settings (active model)
//   - Command Registry: Declarative command registration
//     (command names from internal/commands — the shared CI/CO vocabulary)
//
// Key Files:
//
//   - session.go: Session struct, lifecycle, and cross-goroutine channels
//   - session_task.go: Prompt processing, agent loop, task runners, summarization
//   - session_loop.go: Main event loop, task start/done
//   - session_io.go: Input pump, command dispatch (all commands)
//   - session_content.go: ContentPart helpers, tag mapping, ID lookup
//   - session_persist.go: Session save/load functionality
//   - session_types.go: Type definitions (SessionConfig, etc.)
//   - command_registry.go: Declarative command registration
//   - model_manager.go: Model configuration management
//   - runtime_manager.go: Runtime persistence
//   - model_service.go: modelService (provider/agent lifecycle, model resolution)
//   - mcp_service.go: mcpService (MCP init lifecycle, event handling)
//   - persistence.go: persistenceService (session serialization)
//
// Usage:
//
//	pr, pw := io.Pipe()
//	cfg := agent.SessionConfig{Input: pr, Output: output, ...}
//	session, _, err := agent.LoadOrNewSession(cfg)
//	session.Start()
package agent
