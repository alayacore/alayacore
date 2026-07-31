package agent

// Type definitions for the session package.
// Kept separate for readability — no logic, just data structures.

import (
	"io"
	"time"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/mcp"
	"github.com/alayacore/alayacore/internal/skills"
	"github.com/alayacore/alayacore/internal/theme"
)

// ============================================================================
// TagSystemMsg (SM) payload types
// ============================================================================

// TaskMsg carries task progress info (type "task").
// CommandID is non-empty when the task was started by a CI command
// (continue/summarize), correlating the async completion to its request.
type TaskMsg struct {
	InProgress  bool   `json:"in_progress"`
	CurrentStep int    `json:"current_step,omitempty"`
	MaxSteps    int    `json:"max_steps,omitempty"`
	Context     int64  `json:"context"`
	TaskError   bool   `json:"task_error,omitempty"`
	CommandID   string `json:"command_id,omitempty"`
}

func (TaskMsg) SystemMsgType() string { return "task" }

// ModelMsg carries active model info (type "model").
type ModelMsg struct {
	ActiveModelID   int    `json:"active_id"`
	ActiveModelName string `json:"active_name"`
	ContextLimit    int64  `json:"context_limit"`
}

func (ModelMsg) SystemMsgType() string { return "model" }

// ModelListMsg carries the full model list (type "model_list").
// Only sent when models change.
type ModelListMsg struct {
	Models []config.ModelConfig `json:"models"`
}

func (ModelListMsg) SystemMsgType() string { return "model_list" }

// ThemeInfo carries a theme's name and full content for adapters.
type ThemeInfo struct {
	Name  string       `json:"name"`
	Theme *theme.Theme `json:"theme"`
}

// ThemeListMsg carries all available themes (type "theme_list").
// Sent once on startup so adapters can cache theme content locally.
type ThemeListMsg struct {
	Themes []ThemeInfo `json:"themes"`
}

func (ThemeListMsg) SystemMsgType() string { return "theme_list" }

// ThemeMsg carries the active theme name (type "theme").
// On startup the full Theme is included; on theme changes only the name is sent.
type ThemeMsg struct {
	Name  string       `json:"name"`
	Theme *theme.Theme `json:"theme,omitempty"`
}

func (ThemeMsg) SystemMsgType() string { return "theme" }

// ReasoningMsg carries the reasoning level (type "reasoning").
type ReasoningMsg struct {
	Level int `json:"level"`
}

func (ReasoningMsg) SystemMsgType() string { return "reasoning" }

// VideoConfigMsg carries the video FPS and resolution (type "video_config").
type VideoConfigMsg struct {
	FPS int `json:"fps"`
	Res int `json:"res"`
}

func (VideoConfigMsg) SystemMsgType() string { return "video_config" }

// MCPMsg communicates MCP initialization progress (type "mcp").
// The adapter uses these messages to show/hide init overlays.
//
// Status values (from InitEvent.Type):
//   - "connecting":    starting to connect a server (non-OAuth) or begins processing an OAuth server
//   - "connected":     server connected and initialized (both non-OAuth and OAuth)
//   - "failed":        connection or OAuth failed
//   - "auth_required":  session needs user to authorize this server
//   - "auth_running":  OAuth flow is running for this server
//   - "done":          all MCP initialization complete
type MCPMsg struct {
	Status string `json:"status"`
	Server string `json:"server,omitempty"`
	URL    string `json:"url,omitempty"`   // set for "auth_required"
	Error  string `json:"error,omitempty"` // set for "failed"
}

func (MCPMsg) SystemMsgType() string { return "mcp" }

// MessageVersionMsg carries the TLV message format version and the
// alayacore application version (type "version").
// Sent as the first TagSystemMsg frame so adapters can validate format
// compatibility and identify the core version before processing
// subsequent messages.
type MessageVersionMsg struct {
	MessageVersion int    `json:"message_version"`
	CoreVersion    string `json:"core_version"`
}

func (MessageVersionMsg) SystemMsgType() string { return "version" }

// MessageVersion is the current version of the message encoding
// used in session files and TagSystemMsg broadcasts.
// Increment when making backward-incompatible changes to the TLV
// message format within the session body.
//
// v11: commands moved to the CI/CO control plane — text commands
// (UT ':' sniffing) removed, command results now travel as CO frames,
// TaskMsg gained command_id for async command correlation.
const MessageVersion = 11

// SessionMeta is the frontmatter metadata.
type SessionMeta struct {
	CreatedAt      time.Time `config:"created_at"`
	UpdatedAt      time.Time `config:"updated_at"`
	ActiveModel    string    `config:"active_model,omitempty"`
	MessageVersion int       `config:"message_version,omitempty"`
	ReasoningLevel int       `config:"reasoning_level"`
	ContextTokens  int64     `config:"context_tokens,omitempty"`
	VideoFPS       int       `config:"video_fps"`
	VideoRes       int       `config:"video_res"`
}

// taskResultCh carries the final content list from the task goroutine to run().

// SessionData is the persisted form of a Session.
type SessionData struct {
	SessionMeta
	Contents []llm.ContentPart // source of truth on reload
}

// SessionConfig bundles all configuration for creating or restoring a session.
// This avoids passing 16+ positional parameters to NewSession / RestoreFromSession.
type SessionConfig struct {
	// IO — required, provided by the adapter.
	Input  io.Reader
	Output io.Writer

	// Files — paths to configuration and session files. Empty means default / none.
	SessionFile       string
	ModelConfigPath   string
	RuntimeConfigPath string
	ThemesFolder      string

	// Agent behavior
	BaseTools         []llm.Tool
	SystemPrompt      string
	ExtraSystemPrompt string
	MaxSteps          int
	ToolConfirmTools  []string // tool names requiring user confirmation (empty = no confirmation)

	// Feature flags
	DebugLogDir   string // "" = disabled (when flag not set), "." = write to CWD, or any path
	AutoSummarize int    // 0 = disabled, 1-100 = enabled with this threshold percentage
	ProxyURL      string
	NoTheme       bool // If true, skip all theme loading, detection, and broadcasting
	NoDelta       bool // If true, suppress delta frames (At, Ar, Af); use complete frames only

	// External dependencies
	SkillsMgr *skills.Manager

	// MCPInit handles MCP initialization lifecycle (connect, OAuth, discover).
	// When non-nil, the session reads from its Events() channel in the main
	// loop and applies results (tools, system prompt, manager) internally.
	MCPInit *mcp.Init

	// Override
	OverrideActiveModel string // If set, overrides the active model (must exist in model config)
}
