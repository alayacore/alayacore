package protocol

// The system-message payloads.
//
// A TagSystemMsg frame carries {"type":…,"data":…}; data is one of the types
// below. They live here, beside the MsgType* constants that name them, because
// they cross the adapter/agent boundary: the agent encodes one, an adapter
// decodes it, and neither may hold its own copy of the shape. A field renamed on
// one side then fails to compile on the other, instead of decoding to a zero
// value that the adapter drops in silence — which is what happened when a tool
// confirmation carried `call_id` and one adapter read `id`.
//
// Every type here implements SystemMsg and is reachable from DecodeSystemMsg, so
// the vocabulary and its decoder cannot disagree about what a type is called.
//
// The two theme payloads (theme, theme_list) are the exception and stay in
// internal/agent: they carry theme.Theme, and internal/theme reaches
// internal/tools through internal/config (the .conf reader). Naming that type
// here would drag the tool layer into every package that reads a frame — and
// into the test build of the tool layer itself, which imports this package. The
// terminal's copy of those two shapes is pinned by a drift test instead.

import "encoding/json"

// TaskMsg carries task progress (type "task").
//
// CommandID is non-empty when the task was started by a CI command
// (continue/summarize), correlating the async completion to its request.
//
// Speed fields (step_tps/ttft_ms) are additive and omitempty: they are absent
// until the first step completes, so an adapter that ignores unknown JSON fields
// keeps working unchanged. They reflect the LATEST step only — a simple
// end-to-end throughput (output tokens / round-trip duration, latency included);
// no task-level averaging is reported.
type TaskMsg struct {
	InProgress    bool   `json:"in_progress"`
	CurrentStep   int    `json:"current_step,omitempty"`
	MaxSteps      int    `json:"max_steps,omitempty"`
	ContextTokens int64  `json:"context_tokens"`
	CommandID     string `json:"command_id,omitempty"`

	StepTPS float64 `json:"step_tps,omitempty"` // latest step's end-to-end tok/s
	TTFTMS  int64   `json:"ttft_ms,omitempty"`  // latest step's TTFT in ms
}

func (TaskMsg) SystemMsgType() string { return string(MsgTypeTask) }

// ModelMsg carries the active model (type "model").
type ModelMsg struct {
	ActiveModelID   int    `json:"active_id"`
	ActiveModelName string `json:"active_name"`
	ContextLimit    int64  `json:"context_limit"`
}

func (ModelMsg) SystemMsgType() string { return string(MsgTypeModel) }

// ModelListMsg carries the full model list (type "model_list"). Only sent when
// models change.
type ModelListMsg struct {
	Models []ModelInfo `json:"models"`
}

func (ModelListMsg) SystemMsgType() string { return string(MsgTypeModelList) }

// ReasoningLevelMsg carries the reasoning level (type "reasoning_level").
type ReasoningLevelMsg struct {
	Level int `json:"level"`
}

func (ReasoningLevelMsg) SystemMsgType() string { return string(MsgTypeReasoningLevel) }

// VideoConfigMsg carries the session's video defaults (type "video_config").
type VideoConfigMsg struct {
	FPS int `json:"fps"`
	Res int `json:"res"`
}

func (VideoConfigMsg) SystemMsgType() string { return string(MsgTypeVideoConfig) }

// MCPMsg reports MCP initialization progress (type "mcp").
//
// The adapter uses these messages to show init progress; overlay closure is
// driven by the authoritative "session" ready frame, not by mcp status "done"
// (see SessionMsg).
//
// Server is set for every per-server status; "done" is the only one without it,
// because it is the one event that is about the whole initialization rather than
// one server.
type MCPMsg struct {
	Status string `json:"status"`
	Server string `json:"server,omitempty"`
	URL    string `json:"url,omitempty"`   // set for "auth_required"
	Error  string `json:"error,omitempty"` // set for "failed"
}

func (MCPMsg) SystemMsgType() string { return string(MsgTypeMCP) }

// SessionMsg carries the session lifecycle state (type "session").
//
// Sent on the two transitions an adapter has a reason to observe: once on
// SessionStateReady — after replay and after MCP init has settled (done,
// canceled or aborted, or never configured), the authoritative "ready to accept
// prompts" signal — and once on SessionStateClosed, the terminal frame, written
// before the session loop returns and after all task output. State holds one of
// the SessionState* values.
type SessionMsg struct {
	State string `json:"state"`
}

func (SessionMsg) SystemMsgType() string { return string(MsgTypeSession) }

// SteeringMsg reports the lifecycle of steering — prompts a user sends while a
// task is already running (type "steering"). The agent's session_steering.go
// owns the mechanism; this only reports it.
//
// Phase is machine-readable: one of the Steering* values. Granularity is
// deliberately mixed: Queued and Refused are per submission, Delivered and
// Dropped are per batch (the whole queue is spliced or discarded together, so
// there is never a partial delivery).
//
// Reason qualifies a Dropped message (one of the SteeringDropped* values). Text
// is an optional human-readable line; an adapter may use it or ignore it
// entirely — the session prescribes no rendering for any of this.
type SteeringMsg struct {
	Phase  string `json:"phase"`
	Reason string `json:"reason,omitempty"`
	Text   string `json:"text,omitempty"`
}

func (SteeringMsg) SystemMsgType() string { return string(MsgTypeSteering) }

// MessageVersionMsg carries the adapter protocol version and the alayacore
// application version (type "version"). It is the first TagSystemMsg frame of a
// session, so a client can check format compatibility and identify the core
// version before reading anything else. See the agent's messageVersion for what
// the number means and when it moves.
type MessageVersionMsg struct {
	MessageVersion int    `json:"message_version"`
	CoreVersion    string `json:"core_version"`
}

func (MessageVersionMsg) SystemMsgType() string { return string(MsgTypeVersion) }

// DecodeSystemMsg parses a system message's payload as the type its envelope
// names.
//
// A nil msg with a nil error means "a type this build does not know", which is
// not a failure: the SM vocabulary grows without breaking readers, the same
// promise the frame tags make one level down, so an adapter built against an
// older generation keeps working and simply ignores what it cannot name. A
// non-nil error means the envelope named a type this build knows and its payload
// was not that type — the adapter's own choice is to report or drop it, because
// an adapter only draws what it is handed (see the unknown-kind note in
// kinds.go).
func DecodeSystemMsg(env SystemMsgEnvelope) (SystemMsg, error) {
	switch SystemMsgType(env.Type) {
	case MsgTypeError:
		return decodeSystemPayload[ErrorMsg](env.Data)
	case MsgTypeNotify:
		return decodeSystemPayload[NotifyMsg](env.Data)
	case MsgTypeTask:
		return decodeSystemPayload[TaskMsg](env.Data)
	case MsgTypeModel:
		return decodeSystemPayload[ModelMsg](env.Data)
	case MsgTypeModelList:
		return decodeSystemPayload[ModelListMsg](env.Data)
	case MsgTypeReasoningLevel:
		return decodeSystemPayload[ReasoningLevelMsg](env.Data)
	case MsgTypeVideoConfig:
		return decodeSystemPayload[VideoConfigMsg](env.Data)
	case MsgTypeToolConfirm:
		return decodeSystemPayload[ToolConfirmMsg](env.Data)
	case MsgTypeMCP:
		return decodeSystemPayload[MCPMsg](env.Data)
	case MsgTypeSession:
		return decodeSystemPayload[SessionMsg](env.Data)
	case MsgTypeSteering:
		return decodeSystemPayload[SteeringMsg](env.Data)
	case MsgTypeVersion:
		return decodeSystemPayload[MessageVersionMsg](env.Data)
	}
	return nil, nil
}

// decodeSystemPayload unmarshals one payload type. Generic so that each case of
// DecodeSystemMsg is a single line: the switch then reads as the vocabulary, and
// a type missing from it is visible as a gap rather than buried in boilerplate.
func decodeSystemPayload[T SystemMsg](data json.RawMessage) (SystemMsg, error) {
	var m T
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// DecodeContent parses a content frame's body (UX/AX: a content item plus the
// frame-level metadata). ok is false when the body is not one, which is the
// caller's cue to skip the frame rather than trust it.
func DecodeContent(value string) (ContentData, bool) {
	var d ContentData
	if err := json.Unmarshal([]byte(value), &d); err != nil {
		return ContentData{}, false
	}
	return d, true
}
