// Package protocol defines the high-level message types and wire format
// for the AlayaCore communication protocol.
//
// It provides the SystemMsg interface and implementations (ErrorMsg, NotifyMsg,
// ToolConfirmMsg), the tool data structures (ToolInputData, ToolOutputData), and
// helpers for reading/writing system messages as frames.
//
// The protocol layer depends on the internal/wire package for low-level frame
// encoding.
package protocol

import (
	"encoding/json"
	"io"

	"github.com/alayacore/alayacore/internal/wire"
)

// SystemMsgType constants identify the type field of a TagSystemMsg frame.
// These are used by adapters to dispatch system messages without string literals.
type SystemMsgType string

const (
	MsgTypeError          SystemMsgType = "error"
	MsgTypeNotify         SystemMsgType = "notify"
	MsgTypeTask           SystemMsgType = "task"
	MsgTypeModel          SystemMsgType = "model"
	MsgTypeModelList      SystemMsgType = "model_list"
	MsgTypeTheme          SystemMsgType = "theme"
	MsgTypeThemeList      SystemMsgType = "theme_list"
	MsgTypeReasoningLevel SystemMsgType = "reasoning_level"
	MsgTypeVideoConfig    SystemMsgType = "video_config"
	MsgTypeToolConfirm    SystemMsgType = "tool_confirm"
	MsgTypeMCP            SystemMsgType = "mcp"
	MsgTypeSession        SystemMsgType = "session"
	MsgTypeVersion        SystemMsgType = "version"
	MsgTypeSteering       SystemMsgType = "steering"
)

// Session states — the "state" field of a "session" system message. The
// session broadcasts two of them: ready (initialization settled, prompts are
// accepted) and closed (the terminal frame, sent as run() exits). starting and
// initializing are named here for completeness: they are the same field's
// vocabulary, and a reader of a frame should not have to guess them from the
// producer.
const (
	SessionStateStarting     = "starting"
	SessionStateInitializing = "initializing"
	SessionStateReady        = "ready"
	SessionStateClosed       = "closed"
)

// MCP init statuses — the "status" field of an "mcp" system message. The values
// live here so that an adapter compares a name rather than a literal and a new
// status cannot reach the wire under a spelling only its producer knows.
//
// Canceled is not one of them: a canceled initialization is reported as "done",
// because what a client does about it is the same (stop waiting). The init
// package keeps "canceled" for its own event vocabulary.
const (
	MCPStatusConnecting   = "connecting"
	MCPStatusConnected    = "connected"
	MCPStatusFailed       = "failed"
	MCPStatusAuthRequired = "auth_required"
	MCPStatusAuthRunning  = "auth_running"
	MCPStatusDone         = "done"
)

// MCPStatuses is every value the status field can carry, for the guide's check.
var MCPStatuses = []string{
	MCPStatusConnecting,
	MCPStatusConnected,
	MCPStatusFailed,
	MCPStatusAuthRequired,
	MCPStatusAuthRunning,
	MCPStatusDone,
}

// Steering phases — the "phase" field of a "steering" system message, which
// reports the lifecycle of prompts a user sends while a task is running (see
// the agent package's session_steering.go). Granularity differs by design:
// Queued and Refused are per submission (one per accepted / rejected prompt),
// while Delivered and Dropped are per batch — the whole steering queue is
// spliced or discarded together, never in part.
const (
	SteeringQueued    = "queued"    // accepted into the steering queue
	SteeringDelivered = "delivered" // spliced into the run (or promoted to the next prompt)
	SteeringDropped   = "dropped"   // discarded because the task did not land
	SteeringRefused   = "refused"   // not queued: the steering queue was full
)

// Reasons carried by a Dropped steering message.
const (
	SteeringDroppedCanceled = "canceled" // the running task was canceled
	SteeringDroppedFailed   = "failed"   // the task ended without landing
	SteeringDroppedQuit     = "quit"     // the session is quitting, so it will not run
)

// SystemMsg is implemented by all TagSystemMsg payloads.
type SystemMsg interface {
	SystemMsgType() string
}

// SystemMsgEnvelope is the wire format for TagSystemMsg.
type SystemMsgEnvelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// ParseSystemMsg parses a TagSystemMsg value into its type and payload.
// Returns the envelope on success, or an error if the JSON is malformed.
func ParseSystemMsg(value string) (SystemMsgEnvelope, error) {
	var env SystemMsgEnvelope
	if err := json.Unmarshal([]byte(value), &env); err != nil {
		return SystemMsgEnvelope{}, err
	}
	return env, nil
}

// WriteSystemMsg marshals msg as a TagSystemMsg frame.
func WriteSystemMsg(w io.Writer, msg SystemMsg) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	env, err := json.Marshal(SystemMsgEnvelope{Type: msg.SystemMsgType(), Data: data})
	if err != nil {
		return err
	}
	return wire.WriteFrame(w, wire.TagSystemMsg, string(env))
}

// ModelInfo is the wire format for a model in system messages (model_list).
// It mirrors the agent's ModelConfig JSON tags exactly (including the
// absence of omitempty on core fields) so the wire bytes stay identical
// to the previous format for backward compatibility.
//
// Reasoning0/1/2 carry raw provider-level JSON (e.g. {"thinking":{...},
// "output_config":{...}} for Anthropic or {"thinking":{...},
// "reasoning_effort":"..."} for OpenAI) that the provider merges into
// the request body at the corresponding reasoning level (0=off, 1=normal,
// 2=max). They are user-controlled because each provider family has its
// own thinking/effort vocabulary.
type ModelInfo struct {
	ID             int             `json:"id"`
	Name           string          `json:"name"`
	ProtocolType   string          `json:"protocol_type"`
	BaseURL        string          `json:"base_url"`
	APIKey         string          `json:"api_key"`
	ModelName      string          `json:"model_name"`
	ContextLimit   int             `json:"context_limit"`
	MaxTokens      int             `json:"max_tokens"`
	Reasoning0     json.RawMessage `json:"reasoning_0,omitempty"`
	Reasoning1     json.RawMessage `json:"reasoning_1,omitempty"`
	Reasoning2     json.RawMessage `json:"reasoning_2,omitempty"`
	ReasoningField string          `json:"reasoning_field,omitempty"`

	// SerialToolCalls mirrors modelConfig's json tag exactly. No omitempty:
	// model_list is what a consumer edits and posts back through :model_sync,
	// and what comes back replaces model.conf, so the broadcast has to state the
	// mode instead of leaving a default to infer.
	SerialToolCalls bool `json:"serial_tool_calls"`
}

// Content kind values carried by a content item's "type" field. The
// image/audio/video/document values mirror wire.Kind* — the wire vocabulary.
const (
	TypeText      = "text"
	TypeReasoning = "reasoning"
	TypeImage     = wire.KindImage
	TypeVideo     = wire.KindVideo
	TypeAudio     = wire.KindAudio
	TypeDocument  = wire.KindDocument
)

// ContentMeta is the frame-level metadata embedded in every content frame's
// body. These fields are orthogonal to the content itself.
//
//   - ID is the history id. It is set only on *output* content frames; it is
//     absent on input (the core assigns it) and in the session file (loading
//     re-issues it in record order, so a file carries no id — see
//     docs/architecture.md, "History IDs are not in the file").
//   - Time is the message time (RFC3339). It rides the authoritative frames and
//     the session file; deltas (ephemeral) and input do not carry it.
type ContentMeta struct {
	ID   string `json:"id,omitempty"`
	Time string `json:"time,omitempty"`
}

// ContentItem is one piece of content: the body of a UX or AX frame, and each
// element of a tool result's output. Type selects which payload field is
// meaningful; the same shape is used at the top level and when nested, so there
// is exactly one content-item vocabulary.
type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	URI  string `json:"uri,omitempty"`
	FPS  int    `json:"fps,omitempty"` // video only
	Res  int    `json:"res,omitempty"` // video only
}

// ContentData is the JSON payload for a content frame (UX/AX): a content item
// plus the frame-level metadata.
type ContentData struct {
	ContentMeta
	ContentItem
}

// ToolInputData is the JSON payload for TagAssistantF (AF).
// A frame with a non-empty CallID and Name and empty Input is a preliminary
// "start" frame that announces the tool name. All other frames carry the
// actual tool arguments.
//
// CallID is the tool_call_id — the value that pairs an AF with its UF. It is
// NOT the history id (the two coexist: the history id places the frame in the
// transcript, call_id pairs the call with its result).
type ToolInputData struct {
	CallID string          `json:"call_id"`
	Name   string          `json:"name,omitempty"`
	Input  json.RawMessage `json:"input,omitempty"`
	ContentMeta
}

// ToolOutputData is the JSON payload for TagUserF (UF).
// Output is an array of content items (text, image, etc.).
// IsError indicates whether the tool completed with an error.
type ToolOutputData struct {
	CallID  string        `json:"call_id"`
	Output  []ContentItem `json:"output"`
	IsError bool          `json:"is_error,omitempty"`
	ContentMeta
}

// ToolInputDeltaData is the JSON payload for TagAssistantFDelta (Af).
// Carries a partial JSON chunk of tool arguments during streaming.
type ToolInputDeltaData struct {
	CallID string `json:"call_id"`
	Delta  string `json:"delta"`
	ContentMeta
}

// ToolOutputDeltaData is the JSON payload for TagUserFDelta (Uf).
// Carries a tool result preview snapshot for display only. Ephemeral:
// frames may be dropped, coalesced, or ignored by adapters. The
// authoritative tool result always arrives via TagUserF (UF).
type ToolOutputDeltaData struct {
	CallID string `json:"call_id"`
	Text   string `json:"text"`
	ContentMeta
}

// Placeholders in the "mcp" system message's url field. The session builds the
// authorization URL with them left raw — only an adapter knows the port its
// callback server got and the state it generated — and the adapter substitutes
// them before opening the browser. A URL that still contains one after
// substitution is a bug: the provider would be asked to redirect to
// "{{redirect_uri}}".
const (
	MCPPlaceholderRedirectURI = "{{redirect_uri}}"
	MCPPlaceholderState       = "{{state}}"
)

// CmdMsg is the JSON payload for TagCommandIn (CI) — a command request
// from the adapter to the agent.
//
// Args is an opaque string (the command's argument text, e.g. "/tmp/x.alaya").
// Its syntax is defined by each command, not by the transport layer. It is a
// string, where AF's "input" is a JSON object of tool arguments: the two
// planes agree on what a request carries, not on how it is spelled.
//
// CommandID is generated by the adapter and echoed back in the matching
// CmdResultMsg. It is NOT a call_id: that name belongs to the tool plane,
// where it is the provider's tool_call_id (the value a model sees and an
// adapter matches AF to UF by). A command request has no provider behind it,
// and the same value already travels as "command_id" in the task SM, so one
// value has one name on the wire.
type CmdMsg struct {
	CommandID string `json:"command_id"`
	Name      string `json:"name"`           // command name, no ":" prefix (e.g. "save")
	Args      string `json:"args,omitempty"` // argument string, command-defined syntax
}

// CmdResultMsg is the JSON payload for TagCommandOut (CO) — the result of
// a command.
//
// It is not UF with different words: a command result is opaque and
// command-defined, not a content-item array, and it is not a conversation
// record, so there is no ContentMeta (no id, no time). Only IsError is shared
// with UF, because it means the same thing in both planes; "output" is not,
// because UF's is typed content and this one is whatever the command returns.
//
//   - IsError=false: Result is the command's structured result (schema defined
//     per command; may be null for fire-and-forget commands).
//   - IsError=true:  Result is a CmdError object (uniform error schema).
type CmdResultMsg struct {
	CommandID string          `json:"command_id"`
	Result    json.RawMessage `json:"result"`
	IsError   bool            `json:"is_error,omitempty"`
}

// CmdError is the uniform error object carried in CmdResultMsg.Result when
// IsError is true. Code is machine-readable, Message is human-readable for
// display.
type CmdError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Command error codes — the "code" field of a CmdError. They are wire values: a
// client branches on them, so they are written once here rather than as
// literals at each refusal, and docs/commands.md's table is checked against this
// list.
//
// One construction rule, and one axis: the first segment says what a caller can
// do about it, the rest names the cause. A client may match the prefix to get
// the coarse answer, or the whole string for the detail — so the vocabulary can
// grow a cause without teaching every client a new branch.
//
//	REQUEST_  the request cannot be satisfied as sent  → change what is asked
//	CONFIG_   the installation or model setup is at fault → fix the configuration
//	STATE_    nothing is wrong; the session is not in a position to act → wait
//	IO_       a file operation failed → check the path and permissions
//	INTERNAL_ a bug: the state a command needs was never built → report it
const (
	ErrCodeRequestInvalidArgs    = "REQUEST_INVALID_ARGS"    // the arguments do not match the usage line
	ErrCodeRequestUnknownCommand = "REQUEST_UNKNOWN_COMMAND" // the name is not a command
	ErrCodeRequestNotFound       = "REQUEST_NOT_FOUND"       // the thing named does not exist, or is not pending
	ErrCodeRequestModelUnknown   = "REQUEST_MODEL_UNKNOWN"   // no model has that id
	ErrCodeRequestInvalidState   = "REQUEST_INVALID_STATE"   // the request cannot be satisfied by this history (e.g. forking across an unpaired tool call)

	ErrCodeConfigMissing      = "CONFIG_MISSING"       // nothing is configured to act on: no model, no MCP server, no session file
	ErrCodeConfigModelInvalid = "CONFIG_MODEL_INVALID" // the config parsed but models were rejected
	ErrCodeConfigModelFailed  = "CONFIG_MODEL_FAILED"  // the model subsystem failed: switching, rebuilding, or creating the provider
	ErrCodeConfigUnsupported  = "CONFIG_UNSUPPORTED"   // the feature does not exist in this adapter mode

	ErrCodeStateBusy            = "STATE_BUSY"              // a task is already running
	ErrCodeStateShuttingDown    = "STATE_SHUTTING_DOWN"     // the session is ending
	ErrCodeStateMCPNotReady     = "STATE_MCP_NOT_READY"     // MCP initialization or authorization is pending
	ErrCodeStateNotInProgress   = "STATE_NOT_IN_PROGRESS"   // nothing is in flight to act on
	ErrCodeStateNothingToCancel = "STATE_NOTHING_TO_CANCEL" // no task and no held prompt

	ErrCodeIOReadFailed  = "IO_READ_FAILED"  // a file could not be read: the model config, a theme file
	ErrCodeIOWriteFailed = "IO_WRITE_FAILED" // a file could not be written: the session, the runtime settings

	ErrCodeInternalNotInitialized = "INTERNAL_NOT_INITIALIZED" // a subsystem that should exist does not
	// ErrCodeInternalUnclassified is the fallback for a handler that returned a
	// bare error. It is a bug when it appears: every refusal above exists so
	// that a client can act on the cause, and this one tells it nothing.
	ErrCodeInternalUnclassified = "INTERNAL_UNCLASSIFIED"
)

// CmdErrorCodes is every code above, for the docs table's check. The list and
// the constants are the same fact written twice on purpose: the check compares
// the documentation against this slice, and a constant missing from here is a
// code no test would notice.
var CmdErrorCodes = []string{
	ErrCodeRequestInvalidArgs,
	ErrCodeRequestUnknownCommand,
	ErrCodeRequestNotFound,
	ErrCodeRequestModelUnknown,
	ErrCodeRequestInvalidState,
	ErrCodeConfigMissing,
	ErrCodeConfigModelInvalid,
	ErrCodeConfigModelFailed,
	ErrCodeConfigUnsupported,
	ErrCodeStateBusy,
	ErrCodeStateShuttingDown,
	ErrCodeStateMCPNotReady,
	ErrCodeStateNotInProgress,
	ErrCodeStateNothingToCancel,
	ErrCodeIOReadFailed,
	ErrCodeIOWriteFailed,
	ErrCodeInternalNotInitialized,
	ErrCodeInternalUnclassified,
}

// ErrorMsg is a system error message (type "error").
type ErrorMsg struct {
	Text string `json:"text"`
}

func (ErrorMsg) SystemMsgType() string { return "error" }

// NotifyMsg is a system notification (type "notify").
type NotifyMsg struct {
	Text string `json:"text"`
}

func (NotifyMsg) SystemMsgType() string { return "notify" }

// ToolConfirmMsg is sent when a tool call needs user confirmation
// (type "tool_confirm"). It is a one-way notification: the user's
// decision is returned via the :tool_confirm / :tool_decline commands
// (CI frames), not via an SM response.
//
// CallID is the tool_call_id being confirmed (the same value AF carries).
type ToolConfirmMsg struct {
	CallID string `json:"call_id"`
}

func (ToolConfirmMsg) SystemMsgType() string { return "tool_confirm" }
