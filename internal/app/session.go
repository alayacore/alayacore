package app

// Shared session loading for adapters.
// Terminal, plainio, and terseio adapters follow the same bootstrap sequence:
// load session, validate init errors, print config errors, check models,
// then start the session goroutine.

import (
	"fmt"
	"io"
	"os"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
	"github.com/alayacore/alayacore/internal/protocol"
)

// StartSession loads (or creates) a session, validates it, and starts the
// session goroutine. On success the returned session is ready to use.
//
// It handles:
//   - LoadOrNewSession
//   - InitError check (--model flag validation)
//   - Model load error messages (unknown protocol_type, missing fields)
//   - HasModels check (no usable models → fatal)
//   - session.Start()
//
// input is an optional pre-created input reader. If nil, an io.Pipe is
// created internally and the PipeWriter is returned so the adapter can
// feed TLV messages to the session. When input is non-nil, the returned
// WriteCloser is nil — the caller manages the pipe writer itself.
//
// Returns nil, nil, and an error if the session cannot be used.
func StartSession(cfg *Config, output io.Writer, input io.Reader) (*agentpkg.Session, io.WriteCloser, error) {
	var pipeWriter io.WriteCloser
	if input == nil {
		var r *io.PipeReader
		r, pipeWriter = io.Pipe()
		input = r
	}

	session, _, err := agentpkg.LoadOrNewSession(agentpkg.SessionConfig{
		Input:               input,
		Output:              output,
		SessionFile:         cfg.Cfg.Session,
		ModelConfigPath:     cfg.Cfg.ModelConfig,
		RuntimeConfigPath:   cfg.Cfg.RuntimeConfig,
		ThemesFolder:        cfg.Cfg.ThemesFolder,
		BaseTools:           cfg.AgentTools,
		SystemPrompt:        cfg.SystemPrompt,
		ExtraSystemPrompt:   cfg.ExtraSystemPrompt,
		MaxSteps:            cfg.MaxSteps,
		DebugLogDir:         cfg.Cfg.DebugLogDir,
		AutoSummarize:       cfg.Cfg.AutoSummarize,
		ProxyURL:            cfg.Cfg.Proxy,
		SkillsMgr:           cfg.SkillsMgr,
		OverrideActiveModel: cfg.Cfg.ModelName,
		ToolConfirmTools:    cfg.ToolConfirmTools,
		MCPInit:             cfg.MCPInit,
		NoTheme:             cfg.Cfg.RawIO || cfg.Cfg.PlainIO,
		NoDelta:             cfg.Cfg.NoDelta,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load session: %w", err)
	}

	// --model CLI flag: fail immediately if the named model doesn't exist.
	if err := session.InitError(); err != nil {
		return nil, nil, err
	}

	// Display config validation messages (unknown protocol_type, missing fields, duplicate names, etc.)
	// Must come before HasModels() check so specific errors are shown even when
	// all models are rejected.
	if msgs := session.GetLoadErrors(); len(msgs) > 0 {
		for _, m := range msgs {
			fmt.Fprintf(os.Stderr, "%s\n", m)
			// Send to adapter as system error messages for TUI/plainio display
			_ = protocol.WriteSystemMsg(output, protocol.ErrorMsg{Text: m})
		}
	}

	// Collect runtime config parse errors (typos in runtime.conf).
	if rm := session.RuntimeManager(); rm != nil {
		for _, w := range rm.GetLoadErrors() {
			_ = protocol.WriteSystemMsg(output, protocol.ErrorMsg{Text: w})
		}
	}

	// Display startup errors (theme, runtime config, MCP config) as system messages.
	for _, e := range cfg.StartupErrors {
		_ = protocol.WriteSystemMsg(output, protocol.ErrorMsg{Text: e})
	}

	// Check if we have any models available.
	if !session.HasModels() {
		return nil, nil, fmt.Errorf("%s", agentpkg.NoModelsErrorMessage(session.ModelConfigPath(), session.HasRejected()))
	}

	// Start the session's run() goroutine.
	session.Start()

	return session, pipeWriter, nil
}
