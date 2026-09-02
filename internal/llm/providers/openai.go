// Package providers implements LLM provider clients
package providers

// OpenAI Provider Gotchas:
//
// 1. TOOL CALL ARGUMENTS CHUNKING: OpenAI-compatible APIs split tool call arguments
//    across multiple delta events. Critical: subsequent chunks have `"id": ""` (empty)
//    but correct `"index"`. Must use `index` (not `id`) to associate argument chunks
//    with their tool call. See `appendToolCallArgs()` in openAIStreamState.
//
// 2. TOOL CALL ARGUMENTS IN REQUESTS: When sending tool calls back in conversation
//    history, arguments must be marshaled to a JSON string (not raw JSON).
//    See `openaiConvertToolInputs()`.
//
// 3. REASONING IN TOOL CALL CHAINS: Per DeepSeek's documentation, between two
//    user messages all intermediate assistant reasoning must be passed back —
//    under `reasoning_content` there, under `reasoning_field` on a vLLM-style
//    endpoint (gotcha 7). When reasoning mode is enabled the key is always
//    present on assistant messages (even as an empty string) so that messages
//    containing only tool calls still satisfy this requirement.
//    Conditional on reasoning mode to avoid wasting tokens. The logic lives
//    in openaiConvertContents, not in the sub-converters.
//
// 4. NULL ARGUMENTS IN TOOL CALL CHUNKS: Some providers emit no-op deltas
//    with "arguments": null. Must be skipped to avoid corrupting the
//    accumulated arguments string. See docs/providers.md →
//    "Null arguments in tool call chunks".
//    See `appendToolCallArgs()`.
//
// 5. CONTENT BLOCK INDEXING: Delta event indices always use fixed positions:
//    reasoning=0, text=1, tools=2+wire_index. The final message always includes
//    reasoning and text content blocks (even if empty) so that indices match
//    content array positions. The agent strips empty placeholders in
//    StepCompleteEvent after assigning history IDs. This avoids the need for
//    dynamic index computation and works regardless of streaming order.
//
// 6. MEDIA PROMOTION: A `tool` message can only carry a string, so media
//    returned by a tool cannot ride on its own result. It is instead promoted
//    to one follow-up `user` message that does accept a content array — that
//    is what lets a model actually see the image it asked read_file to load.
//    All tool messages are emitted before the promoted one, because every
//    tool_call of the preceding assistant message must be answered before any
//    other role appears. See `openaiConvertToolOutputs()`.
//
//    Promotion inherits the block-shape asymmetry described in
//    docs/providers.md → "Multimodal support comparison": an image or audio
//    data URI is standard, but `video_url` is a non-standard extension —
//    promoting video to a strict Chat Completions endpoint can turn a request
//    that used to succeed (media flattened to text) into a 400.
//
//    Promotion is also URI-gated (openaiPromotableURI): only `data:` and
//    `http(s)://` are forwarded. A media part carrying a bare path, a `file:`
//    URI or an empty string keeps its text label instead, because an
//    unfetchable url fails the entire request — strictly worse than the missing
//    media it would have stood for.
//
// 7. REASONING OUTPUT FIELD NAME IS NOT STANDARD: No candidate key is in the
//    OpenAI schema — `ChatCompletionStreamResponseDelta` defines only
//    content/role/tool_calls/function_call, so every server that ships
//    reasoning invented one. DeepSeek named it `reasoning_content` (GLM,
//    MiniMax, Qwen and most compatible endpoints copy that); vLLM renamed it to
//    `reasoning` and stopped emitting the old name; OpenRouter serves
//    `reasoning` with `reasoning_content` as a documented alias, plus a
//    structured `reasoning_details` array.
//
//    Which name a deployment uses is a property of the serving stack, not of
//    the model — the same deepseek weights answer differently on
//    api.deepseek.com and on a self-hosted vLLM. model.conf therefore declares
//    it per entry (`reasoning_field`) instead of the reader guessing: a guess
//    has no sound tie-break when a server populates two names at once, and a
//    hardcoded candidate list only grows by shipping a new binary.
//
//    Unset means providers.DefaultReasoningField (`reasoning_content`). A
//    configured name is used and ONLY that one — reasoning is not then read
//    from any other spelling, so a wrong value shows up as empty reasoning.
//    A non-string value under that key (a `reasoning_details` array) is
//    ignored for the same reason: extracting it needs type-aware parsing, not
//    a different name.
//
//    The same key is used in BOTH directions: reasoning replayed in a
//    tool-call chain (gotcha 3) goes out under `reasoning_field` too, because
//    an endpoint speaks one vocabulary for one concept. Sending `reasoning` to
//    a vLLM server is its canonical input path (its ChatMessage field), and
//    the default keeps `reasoning_content` for the DeepSeek family, which
//    requires that spelling — so no second knob is needed. openAIMessage.
//    MarshalJSON performs the redirect; when the key equals the default the
//    struct tag path is taken untouched, so ordinary requests marshal exactly
//    as before.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"sort"
	"strings"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
)

// ============================================================================
// OpenAI Wire Format Types
// ============================================================================

// openAIRequest and openAIStreamOptions were removed when thinking
// configuration moved out of typed fields into user-controlled
// reasoning_N JSON in model.conf. The request body is now assembled
// as map[string]any in StreamMessages, with reasoning-specific keys
// merged in via baseProvider.mergeReasoningConfig.

type openAIMessage struct {
	Role             string           `json:"role"`
	Content          any              `json:"content,omitempty"`
	ReasoningContent *string          `json:"reasoning_content,omitempty"`
	ToolCalls        []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string           `json:"tool_call_id,omitempty"`

	// reasoningKey redirects the ReasoningContent key on the wire (gotcha 7).
	// A deployment that names its reasoning field `reasoning` expects the
	// replayed value back under that same name — one endpoint, one vocabulary.
	// Empty keeps the `reasoning_content` struct tag, so the default path
	// marshals byte-for-byte as before.
	reasoningKey string
}

// MarshalJSON moves the replayed reasoning text to the configured key.
func (m openAIMessage) MarshalJSON() ([]byte, error) {
	type plain openAIMessage // same fields, no MarshalJSON — breaks the recursion
	if m.reasoningKey == "" || m.reasoningKey == "reasoning_content" {
		return json.Marshal(plain(m))
	}
	reasoning := m.ReasoningContent
	out := m
	out.ReasoningContent = nil // suppress the struct-tag key
	out.reasoningKey = ""      // unexported: never serialized anyway
	raw, err := json.Marshal(plain(out))
	if err != nil {
		return nil, err
	}
	if reasoning == nil {
		return raw, nil
	}
	var obj map[string]json.RawMessage
	if err = json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	var value json.RawMessage
	if value, err = json.Marshal(reasoning); err != nil {
		return nil, err
	}
	obj[m.reasoningKey] = value
	return json.Marshal(obj)
}

type openAIToolCall struct {
	Index    int            `json:"index"`
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function openAIToolFunc `json:"function"`
}

type openAIToolFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// openAIDelta represents the delta content from a streaming chunk.
//
// Only content and tool_calls are addressed by struct field. Reasoning is not:
// its wire key is not part of the OpenAI schema and varies by server (gotcha 7),
// so it is looked up by the name the user gave in model.conf `reasoning_field`.
// Read it through reasoningText — never by guessing a key here.
type openAIDelta struct {
	Content   string           `json:"content"`
	ToolCalls []openAIToolCall `json:"tool_calls"`

	// other holds every key of the delta object except content and
	// tool_calls, still raw, so a lookup can tell "absent" from "present but
	// not a string".
	other map[string]json.RawMessage
}

// UnmarshalJSON reads content/tool_calls typed and keeps the rest raw.
func (d *openAIDelta) UnmarshalJSON(data []byte) error {
	var typed struct {
		Content   string           `json:"content"`
		ToolCalls []openAIToolCall `json:"tool_calls"`
	}
	if err := json.Unmarshal(data, &typed); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	delete(raw, "content")
	delete(raw, "tool_calls")
	d.Content = typed.Content
	d.ToolCalls = typed.ToolCalls
	d.other = raw
	return nil
}

// reasoningText returns this chunk's reasoning fragment, read from the key
// named by field.
//
// It returns "" when the key is absent or when its value is not a JSON string.
// The latter matters: some servers carry a same-named structured value
// (OpenRouter's `reasoning_details` is an array of typed blocks), and pulling
// text out of that shape needs type-aware parsing, not a name — so ignoring
// it beats guessing.
func (d openAIDelta) reasoningText(field string) string {
	raw, ok := d.other[field]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// ============================================================================
// OpenAI Provider
// ============================================================================

type OpenAIProvider struct {
	baseProvider
}

// NewOpenAI creates an OpenAI provider from a BaseConfig.
// This is the primary constructor used by the provider factory.
func NewOpenAI(cfg BaseConfig) (*OpenAIProvider, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	p := &OpenAIProvider{}
	p.setBaseConfig(cfg, "gpt-4o")
	if p.apiKey == "" {
		return nil, fmt.Errorf("API key is required")
	}
	return p, nil
}

// SetReasoningLevel sets the reasoning level for OpenAI.
// 0=off, 1=high, 2=xhigh.
func (p *OpenAIProvider) SetReasoningLevel(level int) {
	p.reasoningLevel = level
}

// SetVideoConfig sets the default FPS and resolution for video attachments.
// fps: frames per second (0 means default 2)
// resolution: 0=default, 1=max
func (p *OpenAIProvider) SetVideoConfig(fps int, resolution int) {
	p.videoFPS = fps
	p.videoRes = resolution
}

// StreamMessages streams messages from OpenAI
func (p *OpenAIProvider) StreamMessages(
	ctx context.Context,
	contents []llm.ContentPart,
	tools []llm.ToolDefinition,
	systemPrompt string,
	extraSystemPrompt string,
) (iter.Seq2[llm.StreamEvent, error], error) {
	// Convert messages to OpenAI format
	apiMessages := make([]openAIMessage, 0, len(contents)+2)
	if systemPrompt != "" {
		apiMessages = append(apiMessages, openAIMessage{Role: "system", Content: systemPrompt})
	}
	if extraSystemPrompt != "" {
		apiMessages = append(apiMessages, openAIMessage{Role: "system", Content: extraSystemPrompt})
	}
	apiMessages = append(apiMessages, openaiConvertContents(contents, p.reasoningLevel, p.videoFPS, p.videoRes, p.reasoningField)...)

	// Convert tools to OpenAI format
	apiTools := make([]openAITool, 0, len(tools))
	for _, tool := range tools {
		apiTools = append(apiTools, openAITool{
			Type: "function",
			Function: openAIToolFunc{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Schema,
			},
		})
	}

	// Build request body. The typed struct only covers stable fields;
	// thinking/reasoning_effort come from user-configured reasoning_N
	// JSON merged in via baseProvider.mergeReasoningConfig. With no
	// reasoning_N configured, neither field appears in the body — the
	// server falls back to its own defaults.
	body := p.mergeReasoningConfig(map[string]any{
		"model":          p.model,
		"messages":       apiMessages,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	})
	if len(apiTools) > 0 {
		body["tools"] = apiTools
	}
	if p.maxTokens > 0 {
		body["max_completion_tokens"] = p.maxTokens
	}

	// Build and send HTTP request
	req, err := p.buildRequest(ctx, "/chat/completions", body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	bodyReader, err := p.doRequest(req)
	if err != nil {
		return nil, err
	}

	return p.parseStream(bodyReader), nil
}

// SetReasoningConfigs stores per-level provider wire-format JSON that
// is merged into the request body at the level returned by
// SetReasoningLevel. nil/empty entries are dropped so an unset level
// produces no fields in the body.
func (p *OpenAIProvider) SetReasoningConfigs(configs map[int]json.RawMessage) {
	p.reasoningConfigs = nil
	if len(configs) == 0 {
		return
	}
	p.reasoningConfigs = make(map[int]json.RawMessage, len(configs))
	for k, v := range configs {
		if len(v) > 0 {
			p.reasoningConfigs[k] = v
		}
	}
}

// ============================================================================
// SSE Stream Parsing (OpenAI data-only format)
// ============================================================================

// openaiScanner reads SSE events in the OpenAI "data-only" format.
//
// An event's data is one or more consecutive "data:" lines joined with
// "\n" (multi-line data is legal per the SSE spec and used by some
// providers that pretty-print JSON), terminated by a blank line or EOF.
// Lines without the "data:" prefix (event:, comments, blank lines) are
// ignored, matching the Anthropic scanner's accumulation model.
type openaiScanner struct {
	scanner     *bufio.Scanner
	currentData string
	err         error
}

func newOpenAIScanner(reader io.Reader) *openaiScanner {
	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	return &openaiScanner{scanner: scanner}
}

// Next advances to the next complete SSE event.
// Returns false when the stream is exhausted or an error occurs.
func (s *openaiScanner) Next() bool {
	var data strings.Builder
	hasData := false

	for s.scanner.Scan() {
		line := strings.TrimSpace(s.scanner.Text()) // also tolerates CRLF

		// Empty line — terminate the current event if we have one
		// (per the SSE spec).
		if line == "" {
			if hasData {
				s.currentData = data.String()
				return true
			}
			continue
		}

		if strings.HasPrefix(line, "data:") {
			if hasData {
				data.WriteString("\n")
			}
			if len(line) > 5 && line[5] == ' ' {
				data.WriteString(line[6:]) // "data: hello" → "hello"
			} else {
				data.WriteString(line[5:]) // "data:hello" → "hello", "data:" → ""
			}
			hasData = true
			continue
		}

		// Other SSE fields (event:, comments) are ignored and do not
		// terminate the current data event.
	}

	// EOF reached. Drain any pending event that wasn't terminated by a
	// blank line (handles truncated streams gracefully).
	if hasData {
		s.currentData = data.String()
		return true
	}

	if err := s.scanner.Err(); err != nil {
		s.err = err
	}
	return false
}

// Data returns the current event's data payload.
func (s *openaiScanner) Data() string {
	return s.currentData
}

// Err returns any error encountered during scanning.
func (s *openaiScanner) Err() error {
	return s.err
}

// parseStream returns an iterator that yields SSE events from the OpenAI response.
func (p *OpenAIProvider) parseStream(reader io.Reader) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		defer func() {
			if closer, ok := reader.(io.Closer); ok {
				_ = closer.Close()
			}
		}()

		state := &openAIStreamState{}
		scanner := newOpenAIScanner(reader)
		sawDone := false

		for scanner.Next() {
			data := scanner.Data()
			if data == "[DONE]" {
				sawDone = true
				break
			}
			if !p.handleEvent(data, yield, state) {
				return
			}
		}

		if err := scanner.Err(); err != nil {
			yield(nil, err)
			return
		}

		// A body that simply stops — proxy timeout, upstream close — is not a
		// turn the server finished. OpenAI has no terminal *event*, but it does
		// have two terminal *signals*, and either one is enough: the
		// `finish_reason` field on the last chunk, or the `[DONE]` sentinel
		// closing the stream. Requiring `finish_reason` specifically would
		// reject endpoints that close with `[DONE]` and never name a reason;
		// requiring `[DONE]` on top of a reason would reject endpoints that end
		// the body after the final chunk. Both rejections were measured against
		// this repo's own suite — 12 tests for the first. A body carrying
		// *neither* is the one case that cannot be a completed turn, and
		// synthesizing one there presented a cut-off sentence as a concluded
		// answer, so a retry believed the assistant had finished thinking.
		//
		// Same rule Anthropic applies to `message_stop`, for the same reason:
		// the error is what lets llm.Agent's failed-step path keep the streamed
		// blocks in history as partial, rather than the stream claiming they
		// were the whole answer. See docs/providers.md → "Stream termination".
		//
		// The missing terminator forbids concluding the *step*, not delivering
		// blocks that completed individually — so it is reported here, after
		// the per-block events below and instead of StepCompleteEvent. Returning
		// early instead would drop the tool calls whose arguments had already
		// fully streamed: they were never executed, never recorded, yet the
		// adapter had already drawn them from their deltas — the same
		// display-holds-what-history-lacks shape as the original ordering bug.
		// llm.Agent's failed-step path records them paired with their results.
		incomplete := !sawDone && state.getStopReason() == ""

		// Close the step's blocks. These events carry no content — the record is
		// assembled by llm.Agent from the deltas — so what is being declared here
		// is only where each block ended, and in what order.
		//
		// ORDER MATTERS: the order these arrive in is the order the step is
		// persisted in, because the assembler lays the record out by close order.
		// It has to match the shape an assistant turn has in this protocol —
		// reasoning, content, tool calls — because it is also the order these
		// frames reach the adapters. Under --no-delta nothing is coalesced, so a
		// frame is what creates its TUI window: emitted out of record order,
		// ASSISTANT lands above the REASONING that produced it, and a saved
		// session re-lays the same turn the other way on reopen. --plainio prints
		// in the same frame order.
		//
		// This ordering does NOT decide history IDs: those are minted when a
		// block first appears, which is while the response streams. Numbering is
		// therefore arrival-based in every mode, and a provider that streams
		// against its own message shape cannot be fixed from here — that residue
		// is arrival order versus this fixed layout. See docs/providers.md →
		// "Complete-event order".
		//
		// Blocks the stream never opened are closed anyway rather than skipped by
		// a presence check here: llm.Agent ignores a boundary for a block it never
		// saw, so no phantom window and no empty part can come of it, and every
		// provider is spared tracking whether it saw one.
		if !yield(llm.ReasoningCompleteEvent{Key: openaiReasoningKey}, nil) {
			return
		}
		if !yield(llm.TextCompleteEvent{Key: openaiTextKey}, nil) {
			return
		}
		for _, tc := range state.closedToolCalls() {
			if !yield(tc, nil) {
				return
			}
		}

		if incomplete {
			yield(nil, fmt.Errorf("openai stream ended before its terminating signal"))
			return
		}

		yield(llm.StepCompleteEvent{
			Usage:      state.getUsage(),
			StopReason: state.getStopReason(),
		}, nil)
	}
}

// openAIStreamState tracks state across streaming events
// openAIToolAccumulator remembers a single tool call's identity across streaming
// deltas. OpenAI sends the ID and name on one event and argument fragments keyed
// by index afterwards, with an empty ID on the continuations, so the ID has to be
// recalled to label every later delta. The arguments themselves are not kept
// here: they reach llm.Agent as deltas and are assembled there.
type openAIToolAccumulator struct {
	id   string
	name string
}

type openAIStreamState struct {
	toolAccumulators map[int]*openAIToolAccumulator // tool call index -> identity
	usage            llm.Usage
	stopReason       string
}

func (s *openAIStreamState) setUsage(usage llm.Usage) {
	s.usage = usage
}

func (s *openAIStreamState) getUsage() llm.Usage {
	return s.usage
}

func (s *openAIStreamState) setStopReason(reason string) {
	s.stopReason = reason
}

func (s *openAIStreamState) getStopReason() string {
	return s.stopReason
}

func (s *openAIStreamState) toolAccumulator(index int) *openAIToolAccumulator {
	if s.toolAccumulators == nil {
		s.toolAccumulators = make(map[int]*openAIToolAccumulator)
	}
	acc, ok := s.toolAccumulators[index]
	if !ok {
		acc = &openAIToolAccumulator{}
		s.toolAccumulators[index] = acc
	}
	return acc
}

// unquoteToolArg unquotes a JSON string literal tool argument.
// OpenAI sends arguments as a JSON-string-encoded value (with surrounding
// quotes and escaped inner quotes). This extracts the inner string.
// Returns empty string for null/no-op arguments.
//
// Some OpenAI-compatible providers send arguments as raw JSON (a JSON
// object, not a string-encoded literal). json.Unmarshal into a string
// fails for those, so fall back to the raw bytes — returning "" would
// silently drop the fragment and corrupt every tool call from such
// providers (arguments would accumulate to an empty input).
func unquoteToolArg(args json.RawMessage) string {
	var s string
	if err := json.Unmarshal(args, &s); err == nil {
		return s
	}
	return string(args)
}

// toolIndices returns sorted accumulator indices.
func (s *openAIStreamState) toolIndices() []int {
	indices := make([]int, 0, len(s.toolAccumulators))
	for i := range s.toolAccumulators {
		indices = append(indices, i)
	}
	sort.Ints(indices)
	return indices
}

// closedToolCalls names each tool call the stream opened, as a boundary event,
// in the order the protocol declares them.
//
// The order is the wire's `tool_calls[].index`, not the order fragments
// happened to arrive in: a server that streams index 1 before index 0 still gets
// its calls laid out as it asked for them. That ordering is what llm.Agent's
// assembler uses for the persisted record, so switching this to first-appearance
// order would decide, by streaming luck, the sequence a model sees its own
// parallel calls in on the next turn. Nothing here may do that.
func (s *openAIStreamState) closedToolCalls() []llm.ToolInputCompleteEvent {
	indices := s.toolIndices()
	out := make([]llm.ToolInputCompleteEvent, len(indices))
	for pos, i := range indices {
		out[pos] = llm.ToolInputCompleteEvent{ID: s.toolAccumulators[i].id, Key: openaiToolKey(i)}
	}
	return out
}

// Block identity keys for this provider's content blocks. OpenAI's delta
// schema has no notion of a content block: reasoning and content are two flat
// fields, each accumulating into exactly one block, so their keys are fixed
// strings. Tool calls do carry a per-response index — but it is a chunk
// correlation handle, and the call ID it pairs with is frequently empty on
// continuation chunks, so the same handle is reused as the identity key: it is
// present from the block's first event and never changes mid-stream. A call ID
// would not be: a server that has not sent one yet, or never sends one, would
// make the key shift (or collide) while the block is streaming.
//
// These are opaque. Nothing may order, increment, or index into them — which is
// what the previous `2 + index` "content block index" scheme invited, and how a
// server with non-contiguous tool indices produced a persisted part with no
// history ID at all.
const (
	openaiReasoningKey = "reasoning"
	openaiTextKey      = "text"
)

func openaiToolKey(rawIndex int) string { return fmt.Sprintf("tool:%d", rawIndex) }

// ============================================================================
// Event Handlers
// ============================================================================

// handleEvent handles a single SSE data event. Returns false if iteration should stop.
func (p *OpenAIProvider) handleEvent(data string, yield func(llm.StreamEvent, error) bool, state *openAIStreamState) bool {
	var streamResp struct {
		Choices []struct {
			Delta        openAIDelta `json:"delta"`
			FinishReason string      `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal([]byte(data), &streamResp); err != nil {
		yield(nil, fmt.Errorf("failed to parse event: %w", err))
		return false
	}

	for _, choice := range streamResp.Choices {
		if ok, err := p.checkFinishReason(choice.FinishReason); !ok {
			yield(nil, err)
			return false
		}
		if choice.FinishReason != "" {
			state.setStopReason(choice.FinishReason)
		}
		if ok := p.handleDelta(choice.Delta, yield, state); !ok {
			return false
		}
	}

	if streamResp.Usage.PromptTokens > 0 || streamResp.Usage.CompletionTokens > 0 {
		state.setUsage(llm.Usage{
			InputTokens:  int64(streamResp.Usage.PromptTokens),
			OutputTokens: int64(streamResp.Usage.CompletionTokens),
		})
	}

	return true
}

// checkFinishReason validates the finish reason.
func (p *OpenAIProvider) checkFinishReason(reason string) (bool, error) {
	if reason == "content_filter" {
		return false, fmt.Errorf("content blocked by safety filter")
	}
	if reason != "" && reason != "stop" && reason != "length" && reason != "tool_calls" {
		return false, fmt.Errorf("stream finished with unexpected reason: %s", reason)
	}
	return true, nil
}

// handleDelta processes the delta content from a streaming chunk.
func (p *OpenAIProvider) handleDelta(delta openAIDelta, yield func(llm.StreamEvent, error) bool, state *openAIStreamState) bool {
	if reasoning := delta.reasoningText(p.reasoningField); reasoning != "" {
		if !yield(llm.ReasoningDeltaEvent{Delta: reasoning, Key: openaiReasoningKey}, nil) {
			return false
		}
	}

	if delta.Content != "" {
		if !yield(llm.TextDeltaEvent{Delta: delta.Content, Key: openaiTextKey}, nil) {
			return false
		}
	}

	for _, tc := range delta.ToolCalls {
		// Record identity as it arrives, before emitting anything. Some servers
		// send the call ID in a chunk that carries no name, and reading the ID
		// only on the name's chunk dropped it for the whole call — every later
		// delta and the persisted part then had an empty ID.
		acc := state.toolAccumulator(tc.Index)
		if tc.ID != "" {
			acc.id = tc.ID
		}
		if tc.Function.Name != "" {
			acc.name = tc.Function.Name
		}

		// The start event still waits for the name, which is what an adapter
		// needs to label the window, and it fires once: a name arrives on one
		// chunk and never on a continuation.
		if tc.Function.Name != "" {
			if !yield(llm.ToolInputStartEvent{
				ID:   acc.id,
				Name: acc.name,
				Key:  openaiToolKey(tc.Index),
			}, nil) {
				return false
			}
		}
		if len(tc.Function.Arguments) > 0 {
			if !yield(llm.ToolInputDeltaEvent{
				ID:    acc.id,
				Delta: unquoteToolArg(tc.Function.Arguments),
				Key:   openaiToolKey(tc.Index),
			}, nil) {
				return false
			}
		}
	}

	return true
}

// ============================================================================
// Message Conversion (OpenAI wire format)
// ============================================================================

// openaiConvertContents converts domain ContentParts to OpenAI wire format.
// It groups consecutive same-role ContentParts into API messages.
// reasoningKey names the wire key for replayed reasoning text (gotcha 7);
// empty means the reasoning_content default.
func openaiConvertContents(contents []llm.ContentPart, reasoningLevel int, videoFPS int, videoRes int, reasoningKey string) []openAIMessage {
	chunks := llm.GroupByRole(contents)
	if len(chunks) == 0 {
		return nil
	}

	apiMessages := make([]openAIMessage, 0, len(chunks))

	for _, chunk := range chunks {
		role := chunk[0].GetRole()

		if role == llm.RoleTool {
			apiMessages = append(apiMessages, openaiConvertToolOutputs(chunk, videoFPS, videoRes)...)
			continue
		}

		apiMsg := openAIMessage{Role: string(role)}

		if role == llm.RoleAssistant && openaiHasToolInputs(chunk) {
			openaiConvertToolInputs(&apiMsg, chunk)
		} else {
			openaiConvertRegularContent(&apiMsg, chunk, videoFPS, videoRes)
		}

		if role == llm.RoleAssistant {
			apiMsg.reasoningKey = reasoningKey
			reasoningText := openaiExtractReasoning(chunk)
			if reasoningText != "" || reasoningLevel > config.ReasoningLevelOff {
				apiMsg.ReasoningContent = &reasoningText
			}
			if apiMsg.Content == nil && len(apiMsg.ToolCalls) == 0 {
				apiMsg.Content = ""
			}
		}

		apiMessages = append(apiMessages, apiMsg)
	}

	return apiMessages
}

// openaiConvertToolOutputs converts tool result content to OpenAI messages.
//
// Two protocol constraints shape this function:
//
//  1. OpenAI has no native is_error field (unlike Anthropic), so results are
//     JSON-wrapped with a "status" field — the model distinguishes success
//     from failure structurally rather than by guessing from text.
//  2. A `tool` message carries only a string, so media inside a tool result
//     (read_file on an image, an MCP screenshot) cannot ride on its own
//     result. It is promoted to a single follow-up `user` message, which does
//     accept a content array. Without promotion the model sees a label like
//     "[Image (image/png)]" and can never actually look at a file it chose to
//     read — so tool-driven multimodal reading is impossible on this protocol.
//
// Order matters: every tool_call of the preceding assistant message must be
// answered before any other role appears, so all tool messages are emitted
// first and their media is aggregated into exactly one user message after
// them. Within that message each media-carrying result opens its own text
// heading, so a result returning several items (an MCP server may) stays
// attributable without the model having to count items per id.
//
// Media stays flattened to a text summary when this protocol cannot express it
// as a block at all (documents, audio given as a remote URL) or when the server
// could not fetch the URI (see openaiPromotableURI).
func openaiConvertToolOutputs(contents []llm.ContentPart, videoFPS int, videoRes int) []openAIMessage {
	results := make([]openAIMessage, 0, len(contents)+1)
	var promoted []map[string]any // blocks for the follow-up user message

	for _, part := range contents {
		tr, ok := part.(*llm.ToolOutputPart)
		if !ok {
			continue
		}
		apiMsg := openAIMessage{
			Role:       string(llm.RoleTool),
			ToolCallID: tr.ID,
		}
		// Build the combined string content. A media part becomes either a
		// pointer to the promoted copy (native types) or a summary (the rest),
		// because this message itself can only hold text.
		var textParts []string
		mediaHere := false
		for _, cp := range tr.Output {
			switch v := cp.(type) {
			case *llm.TextPart:
				textParts = append(textParts, v.Text)
			default:
				block, native := openaiMediaBlock(cp, videoFPS, videoRes)
				if !native || !openaiPromotableURI(cp) {
					textParts = append(textParts, openaiMediaSummary(v))
					continue
				}
				// Every media-carrying result opens its own section inside the
				// promoted message. Provenance has to travel as text (a media
				// block has nowhere to carry it), and a heading above a group
				// needs no arithmetic to read — a single header listing ids
				// would make the model count items per result to find the
				// boundaries, and a miscount misattributes media silently.
				if !mediaHere {
					promoted = append(promoted, openaiTextBlock(fmt.Sprintf(
						"Media returned by tool result %s:", tr.ID)))
					mediaHere = true
				}
				promoted = append(promoted, block)
				textParts = append(textParts, openaiPromotedMediaLabel(v))
			}
		}
		combined := strings.Join(textParts, "\n")
		data, _ := json.Marshal(combined) // string can't fail marshal
		if tr.IsError {
			apiMsg.Content = fmt.Sprintf(`{"status":"error","data":%s}`, data)
		} else {
			apiMsg.Content = fmt.Sprintf(`{"status":"success","data":%s}`, data)
		}
		results = append(results, apiMsg)
	}

	if len(promoted) > 0 {
		results = append(results, openAIMessage{Role: string(llm.RoleUser), Content: promoted})
	}
	return results
}

// openaiPromotableURI reports whether a media part's URI can actually be handed
// to the server inside a content block.
//
// Promotion originally trusted any URI it was given. That trust is misplaced:
// a bare filesystem path, a file:// URI, or an empty string becomes
// image_url.url "/tmp/x.png" on the wire, and an unfetchable value there does
// not fail one block quietly — it fails the whole request. The cost is
// asymmetric, so rejecting is strictly better than forwarding: before
// promotion such a part cost a harmless text label, and forwarding it would
// instead take the entire turn down with it.
//
// Only two shapes are safe to forward. A data: URI carries its own bytes, so
// the server needs nothing else. http/https are the only remote schemes a
// server will retrieve; audio additionally never reaches here as a remote URL
// (openaiMediaBlock already rejects that as non-native).
func openaiPromotableURI(part llm.ContentPart) bool {
	var uri string
	switch v := part.(type) {
	case *llm.ImagePart:
		uri = v.URI
	case *llm.VideoPart:
		uri = v.URI
	case *llm.AudioPart:
		uri = v.URI
	case *llm.DocumentPart:
		uri = v.URI
	default:
		return false
	}
	return strings.HasPrefix(uri, "data:") ||
		strings.HasPrefix(uri, "http://") ||
		strings.HasPrefix(uri, "https://")
}

// openaiPromotedMediaLabel is the tool-message text for media delivered on the
// following user message. Unlike openaiMediaSummary it must point forward: a
// bare "[Image (image/png)]" reads as "the pixels are missing", and a model
// that believes that re-reads the file or asks the user instead of looking at
// the image already in context.
func openaiPromotedMediaLabel(part llm.ContentPart) string {
	return openaiMediaSummary(part) + " — attached to the next message"
}

// openaiMediaSummary returns a text label for a media ContentPart: a
// structured "[Image (image/jpeg)]" form for a data URI, or the URL itself for
// a remote one. After media promotion (gotcha 6) it reaches the model in only
// two situations: media this protocol cannot express as a block at all
// (documents, audio behind a remote URL), which stays flattened; and as the
// base text that openaiPromotedMediaLabel turns into a forward pointer.
func openaiMediaSummary(part llm.ContentPart) string {
	switch v := part.(type) {
	case *llm.ImagePart:
		if mediaType, _, ok := llm.ParseDataURI(v.URI); ok {
			return fmt.Sprintf("[Image (%s)]", mediaType)
		}
		return fmt.Sprintf("[Image: %s]", v.URI)
	case *llm.AudioPart:
		if mediaType, _, ok := llm.ParseDataURI(v.URI); ok {
			return fmt.Sprintf("[Audio (%s)]", mediaType)
		}
		return fmt.Sprintf("[Audio: %s]", v.URI)
	case *llm.VideoPart:
		if mediaType, _, ok := llm.ParseDataURI(v.URI); ok {
			return fmt.Sprintf("[Video (%s)]", mediaType)
		}
		return fmt.Sprintf("[Video: %s]", v.URI)
	case *llm.DocumentPart:
		if mediaType, _, ok := llm.ParseDataURI(v.URI); ok {
			return fmt.Sprintf("[Document (%s)]", mediaType)
		}
		return fmt.Sprintf("[Document: %s]", v.URI)
	default:
		return ""
	}
}

func openaiHasToolInputs(contents []llm.ContentPart) bool {
	for _, part := range contents {
		if _, ok := part.(*llm.ToolInputPart); ok {
			return true
		}
	}
	return false
}

// openaiExtractReasoning returns the concatenated text of all ReasoningParts.
func openaiExtractReasoning(contents []llm.ContentPart) string {
	var text string
	for _, part := range contents {
		if r, ok := part.(*llm.ReasoningPart); ok {
			text += r.Text
		}
	}
	return text
}

// openaiConvertToolInputs handles conversion of assistant messages with tool calls.
func openaiConvertToolInputs(apiMsg *openAIMessage, contents []llm.ContentPart) {
	apiMsg.ToolCalls = make([]openAIToolCall, 0)
	var textParts []string
	for _, part := range contents {
		switch v := part.(type) {
		case *llm.ToolInputPart:
			argsStr, err := json.Marshal(string(v.Input))
			if err != nil {
				argsStr = []byte("{}")
			}
			apiMsg.ToolCalls = append(apiMsg.ToolCalls, openAIToolCall{
				ID:   v.ID,
				Type: "function",
				Function: openAIFunction{
					Name:      v.Name,
					Arguments: argsStr,
				},
			})
		case *llm.TextPart:
			textParts = append(textParts, v.Text)
		}
	}
	if len(textParts) > 0 {
		apiMsg.Content = strings.Join(textParts, "")
	}
}

// openaiConvertRegularContent handles conversion of regular text content.
// Media shapes are delegated to openaiMediaBlock so that user/assistant
// messages and promoted tool media cannot drift apart.
func openaiConvertRegularContent(apiMsg *openAIMessage, contents []llm.ContentPart, videoFPS int, videoRes int) {
	var contentParts []map[string]any
	for _, part := range contents {
		if v, ok := part.(*llm.TextPart); ok {
			contentParts = append(contentParts, openaiTextBlock(v.Text))
			continue
		}
		// nil means "not a media part" (tool/reasoning parts, which this
		// converter does not own — see openaiConvertToolInputs and
		// openaiExtractReasoning), so nothing is appended.
		if block, _ := openaiMediaBlock(part, videoFPS, videoRes); block != nil {
			contentParts = append(contentParts, block)
		}
	}
	if len(contentParts) == 0 {
		apiMsg.Content = ""
	} else {
		apiMsg.Content = contentParts
	}
}

// openaiMediaBlock builds the wire block for a media ContentPart. The bool
// reports whether the part was expressed as a native media block; when it is
// false the returned block is a text placeholder, meaning Chat Completions has
// no way to carry this media (documents have no block type at all, and
// input_audio takes raw base64 — the server will not fetch a remote audio
// URL). Callers that can move media elsewhere (see openaiConvertToolOutputs)
// use the bool to decide between promoting and summarizing.
//
// A nil block means the part is not media; callers must check for it.
func openaiMediaBlock(part llm.ContentPart, videoFPS int, videoRes int) (map[string]any, bool) {
	switch v := part.(type) {
	case *llm.ImagePart:
		// Accepts both data URIs and remote URLs via the url field.
		return map[string]any{
			"type":      "image_url",
			"image_url": map[string]string{"url": v.URI},
		}, true
	case *llm.AudioPart:
		// Standard OpenAI format: parse DataURI into base64 + format.
		// Remote URLs are not supported (OpenAI API has no URL audio input).
		if mediaType, b64, ok := llm.ParseDataURI(v.URI); ok {
			return map[string]any{
				"type": "input_audio",
				"input_audio": map[string]string{
					"data":   b64,
					"format": strings.TrimPrefix(mediaType, "audio/"),
				},
			}, true
		}
		return openaiTextBlock(fmt.Sprintf("[Audio file: %s (remote URL not supported)]", v.URI)), false
	case *llm.VideoPart:
		return openAIVideoPart(v.URI, videoFPS, videoRes), true
	case *llm.DocumentPart:
		// Document (PDF) is not natively supported by OpenAI Chat Completions
		// API. Include a text placeholder so the model knows a document was
		// attached.
		return openaiTextBlock(fmt.Sprintf(
			"[Document attached: %s (PDF not supported by this API, only filename available)]", v.URI)), false
	}
	return nil, false
}

// openaiTextBlock builds a text content block.
func openaiTextBlock(text string) map[string]any {
	return map[string]any{"type": "text", "text": text}
}

func openAIVideoPart(uri string, fps int, resolution int) map[string]any {
	if fps == 0 {
		fps = 2
	}
	res := "default"
	if resolution == 1 {
		res = "max"
	}
	return map[string]any{
		"type":             "video_url",
		"video_url":        map[string]string{"url": uri},
		"fps":              fps,
		"media_resolution": res,
	}
}
