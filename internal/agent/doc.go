// Package agent provides the core session management for AlayaCore.
//
// The agent package implements the session layer that sits between the
// adaptors (terminal/websocket) and the AI model provider. It handles:
//
//   - Task queue management (prompts and commands)
//   - Model interaction and streaming
//   - Context management and auto-summarization
//   - Command processing (:save, :model_set, :taskqueue_*, etc.)
//   - Session persistence (save/load conversations)
//
// Architecture Overview:
//
//	Session wires together the model, tools, IO streams, and managers:
//	  model.conf --(ModelManager)--> available models
//	        ^                               |
//	        |                               v
//	  runtime.conf --(RuntimeManager)--> active model name
//	        |                               |
//	        +--------(Session)--------------+
//
// Communication Protocol:
//
//	Adaptors communicate with Session via TLV (Tag-Length-Value) streams:
//	  - Input: TagTextUser for prompts and commands
//	  - Output: TagTextAssistant, TagTextReasoning, TagFunctionCall, etc.
//
// Key Components:
//
//   - Session: Main session struct managing conversation state
//   - ModelManager: Loads and manages AI model configurations
//   - RuntimeManager: Persists runtime settings (active model)
//   - Task Queue: FIFO queue for pending prompts/commands
//   - Command Registry: Declarative command registration
//
// Key Files:
//
//   - session.go: Session struct and main loop
//   - session_prompt.go: Prompt processing and auto-summarization
//   - session_commands.go: Command handlers
//   - session_output.go: Output helpers (writeErrorf, writeNotifyf)
//   - command_registry.go: Declarative command registration
//   - model_manager.go: Model configuration management
//   - runtime_manager.go: Runtime persistence
//
// Usage:
//
//	input := stream.NewChanInput(10)
//	output := &bufferOutput{}
//	session := agent.NewSession(model, input, output, modelMgr, runtimeMgr)
//	go session.Run(ctx)
package agent
