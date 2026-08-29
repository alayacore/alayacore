// Package providers implements LLM provider clients
package providers

// Anthropic Provider Gotchas:
//
// 1. DUAL SYSTEM PROMPT: --system flag appends extra system prompt rather than
//    replacing default. Both prompts become separate system messages.
//
// 2. EMPTY THINKING BLOCK PADDING: Per DeepSeek's documentation, between two
//    user messages all intermediate assistant reasoning_content must be passed
//    back. When reasoning mode is enabled, an empty "thinking" block is
//    prepended to every assistant message that lacks one, so that assistant
//    messages containing only tool calls still satisfy this requirement.
//    The thinking block must come first per Anthropic's API.
//    Conditional on reasoning mode to avoid wasting tokens when thinking is off.
//    See docs/architecture.md → "Empty thinking block padding".
//
// 3. AUDIO/VIDEO DEGRADATION: The Messages API defines content blocks for image
//    and document, but none for audio or video — and an unknown block type
//    fails the whole request, not just the attachment. So those parts are
//    serialized as text by anthropicUnsupportedMediaBlock. This is a workaround
//    to delete, not a policy to maintain: the moment the API gains the blocks,
//    the two cases move back onto anthropicMediaBlock and nothing else changes.
//    Contrast with the OpenAI provider, where media is *promoted* to a
//    follow-up user message instead (see openai.go gotcha 6) — that protocol has
//    native audio/video blocks, it just cannot put them on a tool message.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"sort"
	"strings"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
)

// ============================================================================
// Anthropic Wire Format Types
// ============================================================================

const (
	anthropicBlockTypeText       = "text"
	anthropicBlockTypeImage      = "image"
	anthropicBlockTypeDocument   = "document"
	anthropicBlockTypeThinking   = "thinking"
	anthropicBlockTypeToolResult = "tool_result"
	anthropicBlockTypeToolCall   = "tool_use"

	// Anthropic SSE delta types
	anthropicDeltaTypeText      = "text_delta"
	anthropicDeltaTypeThinking  = "thinking_delta"
	anthropicDeltaTypeInputJSON = "input_json_delta"
)

// anthropicRequest was removed when thinking configuration moved out
// of typed fields into user-controlled reasoning_N JSON in model.conf.
// The request body is now assembled as map[string]any in StreamMessages,
// with reasoning-specific keys merged in via
// baseProvider.mergeReasoningConfig.

type anthropicSystemMessage struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`

	// For tool calls
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// For tool result
	ToolCallID string `json:"tool_use_id,omitempty"`
	Content    any    `json:"content,omitempty"`
	IsError    bool   `json:"is_error,omitempty"`

	// For thinking (extended thinking)
	// Pointer so we can emit `"thinking": ""` (DeepSeek requires empty thinking block)
	// vs. omitting the field on non-thinking blocks.
	Thinking *string `json:"thinking,omitempty"`

	// For image
	Source *anthropicImageSource `json:"source,omitempty"`
}

type anthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// ============================================================================
// SSE Event Types (Anthropic wire format)
// ============================================================================

// anthropicUsage represents token usage in SSE events.
// Fields use pointers so absent fields stay nil (zero-value merge logic).
type anthropicUsage struct {
	InputTokens     *int64 `json:"input_tokens"`
	OutputTokens    *int64 `json:"output_tokens"`
	CacheReadTokens *int64 `json:"cache_read_input_tokens"`
	CreationTokens  *int64 `json:"cache_creation_input_tokens"`
}

// anthropicSSEMessageStart is the payload for "message_start" events.
type anthropicSSEMessageStart struct {
	Message struct {
		Usage anthropicUsage `json:"usage"`
	} `json:"message"`
}

// anthropicSSEContentBlock is a content block in "content_block_start" events.
type anthropicSSEContentBlock struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// anthropicSSEContentBlockStart is the payload for "content_block_start" events.
type anthropicSSEContentBlockStart struct {
	Index        int                      `json:"index"`
	ContentBlock anthropicSSEContentBlock `json:"content_block"`
}

// anthropicSSEDelta is the delta in "content_block_delta" events.
type anthropicSSEDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
}

// anthropicSSEContentBlockDelta is the payload for "content_block_delta" events.
type anthropicSSEContentBlockDelta struct {
	Index int               `json:"index"`
	Delta anthropicSSEDelta `json:"delta"`
}

// anthropicSSEContentBlockStop is the payload for "content_block_stop" events.
type anthropicSSEContentBlockStop struct {
	Index int `json:"index"`
}

// anthropicSSEMessageDelta is the payload for "message_delta" events.
type anthropicSSEMessageDelta struct {
	Delta struct {
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	Usage anthropicUsage `json:"usage"`
}

// anthropicSSEMessageStop is the payload for "message_stop" events.
type anthropicSSEMessageStop struct {
	Usage anthropicUsage `json:"usage"`
}

// anthropicSSEError is the payload for "error" events.
type anthropicSSEError struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// ============================================================================
// Anthropic Provider
// ============================================================================

type AnthropicProvider struct {
	baseProvider
}

// NewAnthropic creates an Anthropic provider from a BaseConfig.
// This is the primary constructor used by the provider factory.
func NewAnthropic(cfg BaseConfig) (*AnthropicProvider, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.anthropic.com"
	}
	p := &AnthropicProvider{}
	p.setBaseConfig(cfg, "claude-3-5-sonnet-20241022")
	if p.apiKey == "" {
		return nil, fmt.Errorf("API key is required")
	}
	return p, nil
}

// SetReasoningLevel sets the reasoning level for Anthropic.
// 0=off, 1=normal, 2=max.
func (p *AnthropicProvider) SetReasoningLevel(level int) {
	p.reasoningLevel = level
}

// SetVideoConfig is a no-op for Anthropic (fps/resolution are not used).
func (p *AnthropicProvider) SetVideoConfig(_ int, _ int) {}

// StreamMessages streams messages from Anthropic
func (p *AnthropicProvider) StreamMessages(
	ctx context.Context,
	contents []llm.ContentPart,
	tools []llm.ToolDefinition,
	systemPrompt string,
	extraSystemPrompt string,
) (iter.Seq2[llm.StreamEvent, error], error) {
	// Convert messages to Anthropic format
	apiMessages := anthropicConvertContents(contents, p.reasoningLevel)

	// Convert tools to Anthropic format
	apiTools := make([]anthropicTool, 0, len(tools))
	for _, tool := range tools {
		apiTools = append(apiTools, anthropicTool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.Schema,
		})
	}

	// Build system messages array
	systemMessages := make([]anthropicSystemMessage, 0, 2)
	if systemPrompt != "" {
		systemMessages = append(systemMessages, anthropicSystemMessage{Type: anthropicBlockTypeText, Text: systemPrompt})
	}
	if extraSystemPrompt != "" {
		systemMessages = append(systemMessages, anthropicSystemMessage{Type: anthropicBlockTypeText, Text: extraSystemPrompt})
	}

	// Build request body. The typed struct only covers stable fields;
	// thinking/output_config come from user-configured reasoning_N JSON
	// merged in via baseProvider.mergeReasoningConfig. With no
	// reasoning_N configured, neither field appears in the body — the
	// server falls back to its own defaults.
	body := p.mergeReasoningConfig(map[string]any{
		"model":      p.model,
		"messages":   apiMessages,
		"max_tokens": p.maxTokens,
		"stream":     true,
	})
	if len(systemMessages) > 0 {
		body["system"] = systemMessages
	}
	if len(apiTools) > 0 {
		body["tools"] = apiTools
	}

	// Build and send HTTP request
	req, err := p.buildRequest(ctx, "/v1/messages", body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

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
func (p *AnthropicProvider) SetReasoningConfigs(configs map[int]json.RawMessage) {
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
// SSE Stream Parsing (Anthropic named-event format)
// ============================================================================

// anthropicScanner reads named-event SSE (Anthropic format).
//
//   - Lines with "event:" set the event type.
//   - Lines with "data:" append to the data payload.
//   - Lines with ":" are comments.
//   - A blank line ("") terminates the current event.
//   - Unknown fields are silently ignored.
//
// The field name may optionally be followed by a space (per the SSE spec).
//
// This follows the same logic as MCP's processSSELine.
type anthropicScanner struct {
	scanner   *bufio.Scanner
	eventType string
	eventData strings.Builder
	hasData   bool // true if we've accumulated any data for the current event
}

func newAnthropicScanner(reader io.Reader) *anthropicScanner {
	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	return &anthropicScanner{scanner: scanner}
}

// Next advances to the next complete SSE event.
// Returns false when the stream is exhausted or an error occurs.
func (s *anthropicScanner) Next() bool {
	for s.scanner.Scan() {
		line := s.scanner.Text()

		// Empty line — terminate the current event if we have one.
		if line == "" {
			if s.hasData {
				s.hasData = false
				return true
			}
			// Consecutive blank lines: skip.
			continue
		}

		// Parse SSE field line (same logic as MCP's processSSELine).
		switch {
		case strings.HasPrefix(line, "event:"):
			// Reset both fields — this is the start of a new event.
			s.eventType = ""
			s.eventData.Reset()
			s.hasData = false
			if len(line) > 6 && line[6] == ' ' {
				s.eventType = line[7:]
			} else {
				s.eventType = line[6:]
			}

		case strings.HasPrefix(line, "data:"):
			if s.eventData.Len() > 0 {
				s.eventData.WriteString("\n")
			}
			if len(line) > 5 && line[5] == ' ' {
				s.eventData.WriteString(line[6:])
			} else {
				s.eventData.WriteString(line[5:])
			}
			s.hasData = true

		case len(line) > 0 && line[0] == ':':
			// Comment — ignore.

		default:
			// Unknown field — ignore per SSE spec.
		}
	}

	// EOF reached. Drain any pending event that wasn't terminated by
	// a blank line (handles truncated streams gracefully).
	if s.hasData {
		s.hasData = false
		return true
	}

	return false
}

// Err returns any error encountered during scanning.
func (s *anthropicScanner) Err() error {
	err := s.scanner.Err()
	if errors.Is(err, bufio.ErrTooLong) {
		return fmt.Errorf("SSE line exceeded 1MB limit — the model may have generated an oversized tool call")
	}
	return err
}

// Event returns the current event's type and data payload.
func (s *anthropicScanner) Event() (eventType string, data string) {
	return s.eventType, s.eventData.String()
}

// parseStream returns an iterator that yields SSE events from the Anthropic response.
func (p *AnthropicProvider) parseStream(reader io.Reader) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		defer func() {
			if closer, ok := reader.(io.Closer); ok {
				_ = closer.Close()
			}
		}()

		state := &anthropicStreamState{
			contentParts: make(map[int]llm.ContentPart),
		}
		scanner := newAnthropicScanner(reader)

		for scanner.Next() {
			eventType, data := scanner.Event()
			if data == "" {
				continue
			}
			if !p.handleEvent(eventType, data, yield, state) {
				return
			}
		}

		if err := scanner.Err(); err != nil {
			yield(nil, err)
		}
	}
}

// blockAccumulator accumulates the content of a single content block by index.
// Anthropic's wire format includes an index on every block event (start, delta, stop),
// allowing blocks to arrive interleaved — similar to how OpenAI indexes tool calls.
type blockAccumulator struct {
	blockType string          // "text" | "thinking" | "tool_use"
	id        string          // tool_use id (empty for text/thinking)
	name      string          // tool_use name (empty for text/thinking)
	buffer    strings.Builder // shared: text, thinking deltas, or tool_use partial_json
}

// anthropicStreamState tracks accumulation state during streaming
type anthropicStreamState struct {
	usage      llm.Usage
	stopReason string

	contentParts map[int]llm.ContentPart   // completed blocks by index
	blocks       map[int]*blockAccumulator // index → block being accumulated
}

func (s *anthropicStreamState) createBlock(index int, blockType, id, name string) *blockAccumulator {
	if s.blocks == nil {
		s.blocks = make(map[int]*blockAccumulator)
	}
	s.blocks[index] = &blockAccumulator{
		blockType: blockType,
		id:        id,
		name:      name,
	}
	return s.blocks[index]
}

func (s *anthropicStreamState) finishBlock(index int) {
	block, ok := s.blocks[index]
	if !ok {
		return
	}
	switch block.blockType {
	case anthropicBlockTypeText:
		s.contentParts[index] = &llm.TextPart{
			Text:            block.buffer.String(),
			ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant},
		}
	case anthropicBlockTypeThinking:
		s.contentParts[index] = &llm.ReasoningPart{
			Text:            block.buffer.String(),
			ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant},
		}
	case anthropicBlockTypeToolCall:
		s.contentParts[index] = &llm.ToolInputPart{
			ID:              block.id,
			Input:           json.RawMessage(block.buffer.String()),
			Name:            block.name,
			ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant},
		}
	}
	delete(s.blocks, index)
}

func (s *anthropicStreamState) setUsage(inputTokens, outputTokens, cacheReadTokens, cacheCreationTokens int64) {
	s.usage = llm.Usage{
		CacheCreationTokens: cacheCreationTokens,
		CacheReadTokens:     cacheReadTokens,
		InputTokens:         inputTokens,
		OutputTokens:        outputTokens,
	}
}

func (s *anthropicStreamState) getUsage() llm.Usage {
	return s.usage
}

func (s *anthropicStreamState) setStopReason(reason string) {
	s.stopReason = reason
}

// getContents returns the accumulated ContentParts sorted by index.
// finishBlock() already converted each block to the correct ContentPart type
// and stored it by index in the map. This function sorts by index to ensure
// correct ordering regardless of the order blocks finished.
func (s *anthropicStreamState) getContents() []llm.ContentPart {
	indices := make([]int, 0, len(s.contentParts))
	for i := range s.contentParts {
		indices = append(indices, i)
	}
	sort.Ints(indices)
	contents := make([]llm.ContentPart, len(indices))
	for pos, i := range indices {
		contents[pos] = s.contentParts[i]
	}
	return contents
}

// toolInputPart returns a complete ToolInputPart if the block at the given index is a tool_use.
func (s *anthropicStreamState) toolInputPart(index int) *llm.ToolInputPart {
	block, ok := s.blocks[index]
	if !ok || block.blockType != anthropicBlockTypeToolCall {
		return nil
	}
	return &llm.ToolInputPart{
		ID:    block.id,
		Name:  block.name,
		Input: json.RawMessage(block.buffer.String()),
	}
}

// ============================================================================
// Event Handlers
// ============================================================================

// handleEvent handles a single SSE event. Returns false if iteration should stop.
func (p *AnthropicProvider) handleEvent(eventType, data string, yield func(llm.StreamEvent, error) bool, state *anthropicStreamState) bool {
	switch eventType {
	case "message_start":
		event, ok := unmarshalSSE[anthropicSSEMessageStart](data, yield)
		if !ok {
			return false
		}
		p.mergeUsage(event.Message.Usage, state)

	case "content_block_start":
		return p.handleContentBlockStart(data, yield, state)

	case "content_block_delta":
		event, ok := unmarshalSSE[anthropicSSEContentBlockDelta](data, yield)
		if !ok {
			return false
		}
		return p.handleContentDelta(event.Index, event.Delta, yield, state)

	case "content_block_stop":
		event, ok := unmarshalSSE[anthropicSSEContentBlockStop](data, yield)
		if !ok {
			return false
		}
		return p.handleContentBlockStop(event.Index, yield, state)

	case "message_delta":
		return p.handleMessageDeltaEvent(data, yield, state)

	case "message_stop":
		event, ok := unmarshalSSE[anthropicSSEMessageStop](data, yield)
		if !ok {
			return false
		}
		p.mergeUsage(event.Usage, state)
		yield(llm.StepCompleteEvent{
			Contents:   state.getContents(),
			Usage:      state.getUsage(),
			StopReason: state.stopReason,
		}, nil)

	case "ping":
		// Ignore

	case "error":
		return p.handleSSEError(data, yield)
	}

	return true
}

// handleContentBlockStart handles content_block_start events.
func (p *AnthropicProvider) handleContentBlockStart(data string, yield func(llm.StreamEvent, error) bool, state *anthropicStreamState) bool {
	event, ok := unmarshalSSE[anthropicSSEContentBlockStart](data, yield)
	if !ok {
		return false
	}
	state.createBlock(event.Index, event.ContentBlock.Type, event.ContentBlock.ID, event.ContentBlock.Name)
	if event.ContentBlock.Type == anthropicBlockTypeToolCall {
		if !yield(llm.ToolInputStartEvent{
			ID:    event.ContentBlock.ID,
			Name:  event.ContentBlock.Name,
			Index: event.Index,
		}, nil) {
			return false
		}
	}
	return true
}

// handleContentDelta handles content block delta events.
//
// The index is the content block index from the Anthropic API. Blocks can
// interleave (e.g. thinking[0], text[1], thinking[2], tool_use[3]), so the
// index is critical for the adapter to distinguish multiple blocks of the
// same type within a single step.
func (p *AnthropicProvider) handleContentDelta(index int, delta anthropicSSEDelta, yield func(llm.StreamEvent, error) bool, state *anthropicStreamState) bool {
	block, ok := state.blocks[index]
	if !ok {
		return true
	}
	switch delta.Type {
	case anthropicDeltaTypeText:
		block.buffer.WriteString(delta.Text)
		if !yield(llm.TextDeltaEvent{Delta: delta.Text, Index: index}, nil) {
			return false
		}
	case anthropicDeltaTypeThinking:
		block.buffer.WriteString(delta.Thinking)
		if !yield(llm.ReasoningDeltaEvent{Delta: delta.Thinking, Index: index}, nil) {
			return false
		}
	case anthropicDeltaTypeInputJSON:
		block.buffer.WriteString(delta.PartialJSON)
		if !yield(llm.ToolInputDeltaEvent{ID: block.id, Delta: delta.PartialJSON, Index: index}, nil) {
			return false
		}
	}
	return true
}

// handleContentBlockStop handles content_block_stop events
func (p *AnthropicProvider) handleContentBlockStop(index int, yield func(llm.StreamEvent, error) bool, state *anthropicStreamState) bool {
	// Capture block info before finishing (finishBlock deletes from map).
	block, blockExists := state.blocks[index]

	// Tool input completion.
	tc := state.toolInputPart(index)
	state.finishBlock(index)
	if tc != nil {
		return yield(llm.ToolInputCompleteEvent{
			ID:    tc.ID,
			Input: tc.Input,
			Index: index,
		}, nil)
	}

	// Text and reasoning complete events.
	if blockExists {
		switch block.blockType {
		case anthropicBlockTypeText:
			if !yield(llm.TextCompleteEvent{Text: block.buffer.String(), Index: index}, nil) {
				return false
			}
		case anthropicBlockTypeThinking:
			if !yield(llm.ReasoningCompleteEvent{Text: block.buffer.String(), Index: index}, nil) {
				return false
			}
		}
	}
	return true
}

// handleMessageDeltaEvent handles "message_delta" SSE events.
func (p *AnthropicProvider) handleMessageDeltaEvent(data string, yield func(llm.StreamEvent, error) bool, state *anthropicStreamState) bool {
	event, ok := unmarshalSSE[anthropicSSEMessageDelta](data, yield)
	if !ok {
		return false
	}
	if !p.handleStopReason(event.Delta.StopReason, yield, state) {
		return false
	}
	p.mergeUsage(event.Usage, state)
	return true
}

// handleSSEError handles "error" SSE events.
func (p *AnthropicProvider) handleSSEError(data string, yield func(llm.StreamEvent, error) bool) bool {
	event, ok := unmarshalSSE[anthropicSSEError](data, yield)
	if !ok {
		return false
	}
	if event.Error.Message != "" {
		yield(nil, fmt.Errorf("API error: %s", event.Error.Message))
	} else {
		yield(nil, fmt.Errorf("unknown API error"))
	}
	return false
}

// handleStopReason validates the stop reason and updates state.
func (p *AnthropicProvider) handleStopReason(stopReason string, yield func(llm.StreamEvent, error) bool, state *anthropicStreamState) bool {
	if stopReason == "" {
		return true
	}
	if stopReason == "refusal" {
		yield(nil, fmt.Errorf("model refused to respond: content policy violation"))
		return false
	}
	valid := stopReason == "end_turn" || stopReason == "max_tokens" ||
		stopReason == "stop_sequence" || stopReason == "tool_use" || stopReason == "pause_turn"
	if !valid {
		yield(nil, fmt.Errorf("stream finished with unexpected stop reason: %s", stopReason))
		return false
	}
	state.setStopReason(stopReason)
	return true
}

// mergeUsage merges partial usage data from an SSE event into the current state.
func (p *AnthropicProvider) mergeUsage(usage anthropicUsage, state *anthropicStreamState) {
	current := state.getUsage()
	if usage.InputTokens != nil {
		current.InputTokens = *usage.InputTokens
	}
	if usage.OutputTokens != nil {
		current.OutputTokens = *usage.OutputTokens
	}
	if usage.CacheReadTokens != nil {
		current.CacheReadTokens = *usage.CacheReadTokens
	}
	if usage.CreationTokens != nil {
		current.CacheCreationTokens = *usage.CreationTokens
	}
	state.setUsage(current.InputTokens, current.OutputTokens, current.CacheReadTokens, current.CacheCreationTokens)
}

// ============================================================================
// Generic SSE Unmarshaling Helper
// ============================================================================

// unmarshalSSE is a generic helper that unmarshals SSE event data into a typed struct.
// On error, it yields the error and returns the zero value with ok=false.
func unmarshalSSE[T any](data string, yield func(llm.StreamEvent, error) bool) (T, bool) {
	var event T
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		var zero T
		yield(nil, fmt.Errorf("failed to parse SSE event: %w", err))
		return zero, false
	}
	return event, true
}

// ============================================================================
// Message Conversion (Anthropic wire format)
// ============================================================================

// anthropicConvertContents converts domain ContentParts to Anthropic wire format.
// Groups consecutive same-role parts into API messages.
//
// Wire-format mappings:
//   - llm.TextPart       → anthropicContentBlock{Type: "text"}
//   - llm.ReasoningPart  → anthropicContentBlock{Type: "thinking"}  (domain "reasoning" → wire "thinking")
//   - llm.ToolInputPart   → anthropicContentBlock{Type: "tool_use"}
//   - llm.ToolOutputPart → anthropicContentBlock{Type: "tool_result"} (role remapped to "user")
func anthropicConvertContents(contents []llm.ContentPart, reasoningLevel int) []anthropicMessage {
	chunks := llm.GroupByRole(contents)
	if len(chunks) == 0 {
		return nil
	}

	apiMessages := make([]anthropicMessage, 0, len(chunks))

	for _, chunk := range chunks {
		role := chunk[0].GetRole()

		apiMsg := anthropicMessage{
			Role:    string(role),
			Content: make([]anthropicContentBlock, 0, len(chunk)),
		}

		// In Anthropic API, tool results must be in a "user" role message
		if role == llm.RoleTool {
			apiMsg.Role = "user"
		}

		for _, part := range chunk {
			if block := anthropicPartToBlock(part); block != nil {
				apiMsg.Content = append(apiMsg.Content, *block)
			}
		}

		// Empty thinking block padding when reasoning is enabled
		if reasoningLevel > config.ReasoningLevelOff && role == llm.RoleAssistant {
			hasThinking := false
			for _, block := range apiMsg.Content {
				if block.Type == anthropicBlockTypeThinking {
					hasThinking = true
					break
				}
			}
			if !hasThinking {
				emptyStr := ""
				apiMsg.Content = append([]anthropicContentBlock{{
					Type:     anthropicBlockTypeThinking,
					Thinking: &emptyStr,
				}}, apiMsg.Content...)
			}
		}

		apiMessages = append(apiMessages, apiMsg)
	}

	return apiMessages
}

// anthropicPartToBlock converts a domain ContentPart to an Anthropic content block.
// Returns nil for unsupported parts.
//
// Recursion: When handling ToolOutputPart, the function calls itself for each
// sub-part in the result's Content slice. This allows tool results containing
// ImagePart, TextPart, etc. to be serialized as nested content blocks inside
// the tool_result block — matching Anthropic's wire format where
// tool_result.content is an array of content blocks (text, image, etc.).
func anthropicPartToBlock(part llm.ContentPart) *anthropicContentBlock {
	switch v := part.(type) {
	case *llm.TextPart:
		return &anthropicContentBlock{
			Type: anthropicBlockTypeText,
			Text: v.Text,
		}
	case *llm.ImagePart:
		return anthropicMediaBlock(anthropicBlockTypeImage, v.URI)
	case *llm.VideoPart, *llm.AudioPart:
		// No native block exists for these — see anthropicUnsupportedMediaBlock.
		return anthropicUnsupportedMediaBlock(part)
	case *llm.DocumentPart:
		return anthropicMediaBlock(anthropicBlockTypeDocument, v.URI)
	case *llm.ReasoningPart:
		text := v.Text
		return &anthropicContentBlock{
			Type:     anthropicBlockTypeThinking,
			Thinking: &text,
		}
	case *llm.ToolInputPart:
		return &anthropicContentBlock{
			Type:  anthropicBlockTypeToolCall,
			ID:    v.ID,
			Name:  v.Name,
			Input: v.Input,
		}
	case *llm.ToolOutputPart:
		block := &anthropicContentBlock{
			Type:       anthropicBlockTypeToolResult,
			ToolCallID: v.ID,
			IsError:    v.IsError,
		}
		// Recursively convert each sub-part to an Anthropic content block.
		// This handles TextPart → text block, ImagePart → image block, etc.
		// via the same anthropicPartToBlock function, enabling nested content
		// inside tool_result (e.g. text + image in a single tool result).
		blocks := make([]anthropicContentBlock, 0, len(v.Output))
		for _, part := range v.Output {
			if b := anthropicPartToBlock(part); b != nil {
				blocks = append(blocks, *b)
			}
		}
		if len(blocks) == 1 && blocks[0].Type == "text" {
			// Single text block: use string for backward compat with simpler wire format
			block.Content = blocks[0].Text
		} else {
			// Multiple blocks or non-text: use array format (supports images)
			block.Content = blocks
		}
		return block
	}
	return nil
}

// anthropicMediaBlock builds an anthropicContentBlock for the media types this
// API accepts as blocks: image and document. Accepts both data URIs and plain
// URLs. Video and audio never reach here — see anthropicUnsupportedMediaBlock.
func anthropicMediaBlock(blockType, uri string) *anthropicContentBlock {
	if mediaType, b64, ok := llm.ParseDataURI(uri); ok {
		return &anthropicContentBlock{
			Type: blockType,
			Source: &anthropicImageSource{
				Type:      "base64",
				MediaType: mediaType,
				Data:      b64,
			},
		}
	}
	// Plain URL — use as-is.
	return &anthropicContentBlock{
		Type: blockType,
		Source: &anthropicImageSource{
			Type: "url",
			URL:  uri,
		},
	}
}

// anthropicUnsupportedMediaBlock degrades media that has no content block in
// the Messages API (video, audio) into a text block.
//
// Sending the native block is not a partial failure — the API rejects the
// entire request — so before this existed, a model that called read_file on a
// clip broke its own next turn rather than merely losing an attachment. And
// read_file advertises that it handles video and audio, so the model is
// actively invited to do this.
//
// The wording is the load-bearing part. A terse "[video unsupported]" note
// reads to a model as "I asked for the file and a result came back", so it
// will happily describe media it never received. The text therefore states
// flatly that nothing was delivered, and names the workaround that does work
// on this protocol.
//
// The base64 payload is deliberately excluded: it is usually the largest thing
// in the conversation, and echoing it into a text block would bill every
// following turn for bytes no model can use.
func anthropicUnsupportedMediaBlock(part llm.ContentPart) *anthropicContentBlock {
	var kind, uri, remedy string
	switch v := part.(type) {
	case *llm.VideoPart:
		kind, uri = "video", v.URI
		remedy = "To inspect it, extract a frame as an image (e.g. ffmpeg via execute_command)."
	case *llm.AudioPart:
		kind, uri = "audio", v.URI
		remedy = "To inspect it, transcribe it to text (e.g. an available CLI via execute_command)."
	default:
		return nil
	}

	desc := kind
	if mediaType, _, ok := llm.ParseDataURI(uri); ok {
		desc += " (" + mediaType + ")"
	} else {
		desc += " at " + uri
	}
	return &anthropicContentBlock{
		Type: anthropicBlockTypeText,
		Text: fmt.Sprintf(
			"[Unreadable %s: this API has no %s input block, so the content was NOT delivered to you and you have not perceived it. Do not describe or quote it. %s]",
			desc, kind, remedy),
	}
}
