package agent

// Type definitions for the session package.
// Kept separate for readability — no logic, just data structures.

import (
	"fmt"
	"io"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/mcp"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/skills"
	"github.com/alayacore/alayacore/internal/theme"
)

// ============================================================================
// SessionState — startup lifecycle
// ============================================================================

// SessionState represents the startup lifecycle phase of a Session.
//
// Initialization consists of exactly one synchronous phase (session file
// load + replay, guaranteed complete by construction) and one asynchronous
// phase (MCP init, when MCP servers are configured). State transitions are
// owned by the run() goroutine: constructors store SessionStarting,
// run() sets the initial phase, setState()/syncState() (session.go) advance
// it once MCP init settles, and run()'s exit stores SessionClosed.
//
// IMPORTANT: agent/provider creation is deliberately lazy (happens on the
// first task, and MCP init completion resets an already-created agent so it
// is rebuilt with MCP tools). It is therefore NOT part of SessionState —
// SessionReady does not imply an agent exists.
type SessionState int32

const (
	// SessionStarting: the Session has been constructed; session file load
	// and replay (if any) are complete. Fatal load errors abort startup
	// before Start(), so this phase implies a usable, replayed session.
	SessionStarting SessionState = iota

	// SessionInitializing: Start() has been called and MCP initialization
	// is in progress (only reached when MCP servers are configured).
	SessionInitializing

	// SessionReady: MCP initialization has settled (done, canceled, or
	// aborted) — or was never configured. Prompts are accepted.
	SessionReady

	// SessionClosed: the session has ended. This is a terminal phase, never
	// a startup one: run() broadcasts it once on its way out, after every
	// other frame, and nothing else is written afterwards. State() never
	// leaves it, so beginTask's "must be ready" check keeps refusing new
	// work.
	SessionClosed
)

// String returns a stable, human-readable name for the state.
func (s SessionState) String() string {
	switch s {
	case SessionStarting:
		return protocol.SessionStateStarting
	case SessionInitializing:
		return protocol.SessionStateInitializing
	case SessionReady:
		return protocol.SessionStateReady
	case SessionClosed:
		return protocol.SessionStateClosed
	default:
		return fmt.Sprintf("SessionState(%d)", int(s))
	}
}

// ============================================================================
// TagSystemMsg (SM) payload types
//
// The payloads an adapter must decode live in internal/protocol, beside the
// MsgType* constants that name them: they cross the adapter/agent boundary, and a
// shape spelled on both sides is how a field rename became a silently dropped
// frame (see protocol/sm.go). The three below stay here because they carry
// theme.Theme, which protocol must not depend on (protocol/sm.go says why), and
// the terminal's copy of them is pinned by a drift test.
// ============================================================================

// themeMsg carries the active theme's name (type "theme").
//
// The name only: the palettes travel in theme_list, which an adapter caches, so
// this frame says which entry is active and nothing else. (A `theme` object was
// documented here and never sent; the field is gone rather than populated twice.)
type themeMsg struct {
	Name string `json:"name"`
}

func (themeMsg) SystemMsgType() string { return string(protocol.MsgTypeTheme) }

// themeInfo carries a theme's name and full content for adapters.
type themeInfo struct {
	Name  string       `json:"name"`
	Theme *theme.Theme `json:"theme"`
}

// themeListMsg carries all available themes (type "theme_list").
// Sent once on startup (TUI only — skipped under NoTheme) so the
// terminal can cache theme content locally.
type themeListMsg struct {
	Themes []themeInfo `json:"themes"`
}

func (themeListMsg) SystemMsgType() string { return string(protocol.MsgTypeThemeList) }

// messageVersion is the adapter protocol version: the frame format adapters
// speak — the tags, the SM types, and the states those carry. It is broadcast
// as the first TagSystemMsg frame (protocol.MessageVersionMsg) so an out-of-process
// client can check compatibility before reading anything else.
//
// It moves for additive changes too — a new tag, a new state, a new command —
// because an adapter that knows only the older format has no other way to learn
// that the newer one exists.
//
// Session files record the number in their frontmatter and loading demands an
// exact match (see parseSessionMeta). That is a side effect of carrying the
// protocol version in the file, not a promise about the file's contents, and
// nothing here migrates an older file: editing the field to load one anyway is
// the user's call. See docs/architecture.md ("message_version").
//
// v17: the command plane's vocabulary, and what a command accepts. What moved:
// CI's "call_id"/"input" are "command_id"/"args" and CO's "call_id"/"output"
// are "command_id"/"result" — the tool plane keeps "call_id" (the provider's
// tool_call_id) and "input"/"output" (typed content); a command request has no
// provider behind it and its answer is whatever the command returns. Every CO
// error code is renamed into one vocabulary whose first segment says what a
// caller can do (REQUEST_, CONFIG_, STATE_, IO_, INTERNAL_), and the fallback
// is "INTERNAL_UNCLASSIFIED" rather than "ERROR", which told a client nothing
// is_error had not already said. A CI the agent cannot read is answered with
// an SM error instead of a CO: a CO answers exactly one command, and a request
// that could not be read has no id to echo. The empty id that used to stand in
// for one also let a well-formed CI without a command_id run and reply with a
// frame no adapter could correlate, so that is refused instead. A command now
// accepts exactly the arguments `commands.Defs` declares, while a
// readable-but-refused request (a CI sent with unsent content staged) still
// gets its CO. A canceled MCP initialization no longer reports per-server
// "failed" frames ("context canceled" for the user's own :mcp_skip), and a
// stream that ends without a terminal event says "aborted unexpectedly" rather
// than "canceled". None of it is in a file: the upgrade is the one-line
// frontmatter edit, not a conversion — sessionmigrate holds no v17 step and
// misc/ ships no v17 script, because there is nothing to convert.
// v16: the vocabulary only, with no framing change. What moved: :mcp_cancel is
// :mcp_skip ("cancel" already stops the running task); :reason is
// :reasoning_level_set, the setting's name plus the verb; the SM that reports a
// reasoning-level change is "reasoning_level", so no wire string means two
// things; the task SM's token count is "context_tokens", the name the session
// file and the adapters already used; a tool call's id is "call_id" in the CO
// result too, and the arguments that carry an id say which one (":tool_confirm
// <call_id>", ":fork <history_id> <filename>"). A short form is an adapter's
// word, not a wire name: "q" is refused like any unknown command, and the
// adapter that accepts it resolves it before writing the frame. Adapters that
// speak v15 keep working until they use one of those names; a v15 session file
// no longer loads, because loading demands this number exactly (see above) —
// and since none of these names is in a file, the upgrade is the one-line
// frontmatter edit, not a conversion step: sessionmigrate holds no v16 step and
// misc/ ships no v16 script, because there is nothing to convert.
// v15: the wire dropped its 4-byte length field. A frame is one line — a
// two-byte tag, a JSON value, an LF — so the boundary now rests on the value
// being one line, which the encoder enforces and the decoder checks, instead of
// on a length that a peer could get wrong. Session file bodies lost their blank
// line between frames with it: every line is one frame, which is also what makes
// the body readable. The frame bound is 64MB, matching what the MCP transports
// bound one of their messages by, down from 2GB.
// v14: every content frame's value is JSON. Text and media tags collapsed into
// a 2x2 — UX/AX (content) and AF/UF (function) — with the concrete kind carried
// as a content item's "type". The history id moved from a NUL-delimited prefix
// into the frame's top-level "id", so WrapID/UnwrapID are gone; the tool-call
// id became "call_id" (freeing "id" to mean history id everywhere), and
// ContentMeta gained "time" (message time, persisted and replayed). Video
// fps/resolution became per-part (llm.VideoPart), so a :video_config change no
// longer rewrites already-sent messages.
// v13: steering is reported by a dedicated "steering" SM (queued / delivered /
// dropped / refused), replacing the "steering queued" and "steering dropped"
// notify texts and the SM error a full queue used to raise for a prompt.
// Additive for adapters that ignore unknown SM types; the session-file
// version moves with it as always.
// v12: the input plane gained CE (TagInputEnd — the adapter's input is
// exhausted without closing the stream, so commands may still follow), the
// "session" message gained the terminal state "closed", and the quit command
// joined the vocabulary (with the q alias). All additive: an adapter that
// knows none of them still speaks v11.
// v11: commands moved to the CI/CO control plane — text commands
// (UT ':' sniffing) removed, command results now travel as CO frames,
// the task message gained command_id for async command correlation.
const messageVersion = 17

// sessionMeta is the frontmatter metadata.
type sessionMeta struct {
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

// sessionData is the persisted form of a Session.
type sessionData struct {
	sessionMeta
	Contents []llm.ContentPart // source of truth on reload
}

// SessionConfig bundles all configuration for creating or restoring a session.
// This avoids passing 16+ positional parameters to newSession / RestoreFromSession.
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
	ReasoningLevel    int      // startup reasoning level (0=off, 1=normal, 2=max)
	ReasoningLevelSet bool     // true when set explicitly (CLI flag wins over the session file's saved level)

	// Feature flags
	DebugLogDir   string // "" = disabled (when flag not set), "." = write to CWD, or any path
	AutoSummarize int    // 0 = disabled, 1-100 = enabled with this threshold percentage
	ProxyURL      string
	NoTheme       bool // If true, skip all theme loading, detection, and broadcasting
	NoDelta       bool // If true, suppress the delta frames (Ax, Af, Uf); use complete frames only

	// External dependencies.
	//
	// SkillsMgr is carried but not read: the prompt fragment is built once in
	// app.Setup, and nothing in a session asks the manager anything today. It is
	// the seam a /skills listing or a reload would attach to — see
	// docs/skills.md ("Skill Specification") for what the feature does not do.
	SkillsMgr *skills.Manager

	// MCPInit handles MCP initialization lifecycle (connect, OAuth, discover).
	// When non-nil, the session reads from its Events() channel in the main
	// loop and applies results (tools, system prompt, manager) internally.
	MCPInit *mcp.Initializer

	// Override
	OverrideActiveModel string // If set, overrides the active model (must exist in model config)
}
