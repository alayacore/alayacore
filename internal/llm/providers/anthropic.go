// Package providers implements LLM provider clients
package providers

// Anthropic Provider Gotchas:
//
// 1. PROMPT CACHING: System message must be ≥1024 tokens for caching to activate.
//    Shorter prompts won't be cached even with cache_control set.
//
// 2. CACHE CONTROL PLACEMENT: Cache control is applied only to the first 2 system
//    messages (default prompt + --system extra). Other system messages in conversation
//    history are not modified.
//
// 3. DUAL SYSTEM PROMPT: --system flag appends extra system prompt rather than
//    replacing default. Both prompts become separate system messages, each with
//    cache_control for Anthropic APIs.
//
// 4. PROMPT CACHE PER-MODEL: prompt_cache: true in model.conf enables cache_control
//    markers for Anthropic. Other providers auto-cache and ignore this setting.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
)

const (
	blockTypeToolUse = "tool_use"
)

// AnthropicProvider implements the Anthropic API
type AnthropicProvider struct {
	apiKey      string
	baseURL     string
	client      *http.Client
	model       string
	promptCache bool
}

// AnthropicOption configures the provider
type AnthropicOption func(*AnthropicProvider)

// NewAnthropic creates a new Anthropic provider
func NewAnthropic(opts ...AnthropicOption) (*AnthropicProvider, error) {
	p := &AnthropicProvider{
		baseURL: "https://api.anthropic.com",
		client:  &http.Client{Timeout: 10 * time.Minute},
		model:   "claude-3-5-sonnet-20241022",
	}
	for _, opt := range opts {
		opt(p)
	}
	if p.apiKey == "" {
		return nil, fmt.Errorf("API key is required")
	}
	return p, nil
}

// WithAPIKey sets the API key
func WithAPIKey(key string) AnthropicOption {
	return func(p *AnthropicProvider) {
		p.apiKey = key
	}
}

// WithBaseURL sets the base URL
func WithBaseURL(url string) AnthropicOption {
	return func(p *AnthropicProvider) {
		p.baseURL = strings.TrimSuffix(url, "/")
	}
}

// WithHTTPClient sets the HTTP client
func WithHTTPClient(client *http.Client) AnthropicOption {
	return func(p *AnthropicProvider) {
		p.client = client
	}
}

// WithAnthropicModel sets the model name
func WithAnthropicModel(model string) AnthropicOption {
	return func(p *AnthropicProvider) {
		p.model = model
	}
}

// WithPromptCache enables prompt caching for Anthropic
func WithPromptCache(enabled bool) AnthropicOption {
	return func(p *AnthropicProvider) {
		p.promptCache = enabled
	}
}

// anthropicRequest represents the Anthropic API request
type anthropicRequest struct {
	Model        string                   `json:"model"`
	Messages     []anthropicMessage       `json:"messages"`
	MaxTokens    int                      `json:"max_tokens"`
	System       []anthropicSystemMessage `json:"system,omitempty"`
	Tools        []anthropicTool          `json:"tools,omitempty"`
	Stream       bool                     `json:"stream"`
	CacheControl *anthropicCacheControl   `json:"cache_control,omitempty"`
}

type anthropicCacheControl struct {
	Type string `json:"type"`
}

type anthropicSystemMessage struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`

	// For tool use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// For tool result
	ToolUseID string      `json:"tool_use_id,omitempty"`
	Content   interface{} `json:"content,omitempty"`
	IsError   bool        `json:"is_error,omitempty"`

	// For thinking (extended thinking)
	Thinking string `json:"thinking,omitempty"`

	// Cache control
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// streamState tracks accumulation state during streaming
type streamState struct {
	mu           sync.Mutex
	contentParts []llm.ContentPart
	usage        llm.Usage
	stopReason   string

	// Current block being accumulated
	currentIndex int
	currentType  string
	currentText  strings.Builder
	currentInput strings.Builder
	currentID    string
	currentName  string
}

func (s *streamState) startBlock(index int, blockType, id, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentIndex = index
	s.currentType = blockType
	s.currentID = id
	s.currentName = name
	s.currentText.Reset()
	s.currentInput.Reset()
}

func (s *streamState) appendText(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentText.WriteString(text)
}

func (s *streamState) appendInput(jsonStr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentInput.WriteString(jsonStr)
}

func (s *streamState) finishBlock() {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch s.currentType {
	case "text":
		s.contentParts = append(s.contentParts, llm.TextPart{
			Type: "text",
			Text: s.currentText.String(),
		})
	case "thinking":
		s.contentParts = append(s.contentParts, llm.ReasoningPart{
			Type: "reasoning",
			Text: s.currentText.String(),
		})
	case blockTypeToolUse:
		s.contentParts = append(s.contentParts, llm.ToolCallPart{
			Type:       blockTypeToolUse,
			ToolCallID: s.currentID,
			ToolName:   s.currentName,
			Input:      json.RawMessage(s.currentInput.String()),
		})
	}
	s.currentType = ""
}

func (s *streamState) setUsage(inputTokens, outputTokens, cacheReadTokens, cacheCreationTokens int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usage = llm.Usage{
		CacheCreationTokens: cacheCreationTokens,
		CacheReadTokens:     cacheReadTokens,
		InputTokens:         inputTokens,
		OutputTokens:        outputTokens,
	}
}

func (s *streamState) getMessage() llm.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return llm.Message{
		Role:    llm.RoleAssistant,
		Content: append([]llm.ContentPart{}, s.contentParts...),
	}
}

func (s *streamState) getUsage() llm.Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usage
}

func (s *streamState) setStopReason(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopReason = reason
}

// lastToolCall returns the last tool call if the current block is a tool_use
func (s *streamState) lastToolCall() *llm.ToolCallPart {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.currentType == blockTypeToolUse {
		return &llm.ToolCallPart{
			Type:       blockTypeToolUse,
			ToolCallID: s.currentID,
			ToolName:   s.currentName,
			Input:      json.RawMessage(s.currentInput.String()),
		}
	}
	return nil
}

// StreamMessages streams messages from Anthropic
//
//nolint:gocyclo // message conversion requires multiple type switches
func (p *AnthropicProvider) StreamMessages(
	ctx context.Context,
	messages []llm.Message,
	tools []llm.ToolDefinition,
	systemPrompt string,
	extraSystemPrompt string,
) (<-chan llm.StreamEvent, error) {
	// Convert messages to Anthropic format
	apiMessages := make([]anthropicMessage, 0, len(messages))
	for _, msg := range messages {
		apiMsg := anthropicMessage{
			Role:    string(msg.Role),
			Content: make([]anthropicContentBlock, 0, len(msg.Content)),
		}

		// In Anthropic API, tool results must be in a "user" role message
		if msg.Role == llm.RoleTool {
			apiMsg.Role = "user"
		}

		for _, part := range msg.Content {
			switch v := part.(type) {
			case llm.TextPart:
				apiMsg.Content = append(apiMsg.Content, anthropicContentBlock{
					Type: "text",
					Text: v.Text,
				})
			case llm.ReasoningPart:
				// Anthropic uses "thinking" type for extended thinking
				apiMsg.Content = append(apiMsg.Content, anthropicContentBlock{
					Type:     "thinking",
					Thinking: v.Text,
				})
			case llm.ToolCallPart:
				apiMsg.Content = append(apiMsg.Content, anthropicContentBlock{
					Type:  blockTypeToolUse,
					ID:    v.ToolCallID,
					Name:  v.ToolName,
					Input: v.Input,
				})
			case llm.ToolResultPart:
				var content interface{}
				switch out := v.Output.(type) {
				case llm.ToolResultOutputText:
					content = out.Text
				case llm.ToolResultOutputError:
					content = out.Error
					apiMsg.Content = append(apiMsg.Content, anthropicContentBlock{
						Type:      "tool_result",
						ToolUseID: v.ToolCallID,
						Content:   content,
						IsError:   true,
					})
					continue
				}
				apiMsg.Content = append(apiMsg.Content, anthropicContentBlock{
					Type:      "tool_result",
					ToolUseID: v.ToolCallID,
					Content:   content,
				})
			}
		}
		apiMessages = append(apiMessages, apiMsg)
	}

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

	// Add default system prompt
	if systemPrompt != "" {
		systemMessages = append(systemMessages, anthropicSystemMessage{
			Type: "text",
			Text: systemPrompt,
		})
	}

	// Add extra system prompt separately
	if extraSystemPrompt != "" {
		systemMessages = append(systemMessages, anthropicSystemMessage{
			Type: "text",
			Text: extraSystemPrompt,
		})
	}

	// Build request
	reqBody := anthropicRequest{
		Model:     p.model,
		Messages:  apiMessages,
		MaxTokens: 4096,
		System:    systemMessages,
		Tools:     apiTools,
		Stream:    true,
	}

	// Add top-level cache_control for automatic caching (Anthropic's automatic caching)
	if p.promptCache {
		reqBody.CacheControl = &anthropicCacheControl{Type: "ephemeral"}
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/v1/messages", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("API error (status %d): failed to read error body: %w", resp.StatusCode, err)
		}
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	// Create event channel
	eventChan := make(chan llm.StreamEvent, 100)

	// Start streaming goroutine
	go p.parseStream(resp.Body, eventChan)

	return eventChan, nil
}

// parseStream parses the SSE stream from Anthropic
func (p *AnthropicProvider) parseStream(reader io.Reader, eventChan chan<- llm.StreamEvent) {
	defer close(eventChan)

	state := &streamState{
		contentParts: make([]llm.ContentPart, 0),
	}

	scanner := bufio.NewScanner(reader)
	// Increase buffer size for large responses
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var eventType string
	var eventData strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		switch {
		case strings.HasPrefix(line, "event: "):
			eventType = strings.TrimPrefix(line, "event: ")
			eventData.Reset()
		case strings.HasPrefix(line, "data: "):
			eventData.WriteString(strings.TrimPrefix(line, "data: "))
		case line == "" && eventType != "":
			// Process complete event
			data := eventData.String()
			if err := p.handleEvent(eventType, data, eventChan, state); err != nil {
				eventChan <- llm.StreamErrorEvent{Error: err}
				return
			}
			eventType = ""
			eventData.Reset()
		}
	}

	if err := scanner.Err(); err != nil {
		eventChan <- llm.StreamErrorEvent{Error: err}
	}
}

// handleEvent handles a single SSE event
func (p *AnthropicProvider) handleEvent(eventType, data string, eventChan chan<- llm.StreamEvent, state *streamState) error {
	if data == "" {
		return nil
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return fmt.Errorf("failed to parse event data: %w", err)
	}

	switch eventType {
	case "message_start":
		return p.handleMessageStart(payload, eventChan, state)

	case "content_block_start":
		return p.handleContentBlockStart(payload, eventChan, state)

	case "content_block_delta":
		return p.handleContentDelta(payload, eventChan, state)

	case "content_block_stop":
		return p.handleContentBlockStop(payload, eventChan, state)

	case "message_delta":
		return p.handleMessageDelta(payload, eventChan, state)

	case "message_stop":
		return p.handleMessageStop(payload, eventChan, state)

	case "ping":
		// Ignore ping events
		return nil

	case "error":
		if errMsg, ok := payload["error"].(map[string]interface{}); ok {
			return fmt.Errorf("API error: %v", errMsg["message"])
		}
		return fmt.Errorf("unknown API error")
	}

	return nil
}

// handleMessageStart handles message_start events - may contain initial usage
func (p *AnthropicProvider) handleMessageStart(payload map[string]interface{}, _ chan<- llm.StreamEvent, state *streamState) error {
	// Extract usage from message_start if present
	if msg, ok := payload["message"].(map[string]interface{}); ok {
		if usage, ok := msg["usage"].(map[string]interface{}); ok {
			p.extractAndSetUsage(usage, state)
		}
	}
	return nil
}

// extractAndSetUsage extracts token counts from usage map and updates state
// Note: Usage events may come in multiple chunks (message_start, message_delta, message_stop)
// Each chunk may contain partial usage data, so we accumulate/merge with existing values.
func (p *AnthropicProvider) extractAndSetUsage(usage map[string]interface{}, state *streamState) {
	// Get current usage to preserve values not present in this chunk
	current := state.getUsage()

	// Only update fields that are present in the incoming usage map
	inputTokens := current.InputTokens
	if v, ok := usage["input_tokens"].(float64); ok {
		inputTokens = int64(v)
	}

	outputTokens := current.OutputTokens
	if v, ok := usage["output_tokens"].(float64); ok {
		outputTokens = int64(v)
	}

	// Cache tokens are part of input tokens
	cacheReadTokens := current.CacheReadTokens
	if v, ok := usage["cache_read_input_tokens"].(float64); ok {
		cacheReadTokens = int64(v)
	}

	cacheCreationTokens := current.CacheCreationTokens
	if v, ok := usage["cache_creation_input_tokens"].(float64); ok {
		cacheCreationTokens = int64(v)
	}

	state.setUsage(inputTokens, outputTokens, cacheReadTokens, cacheCreationTokens)
}

// handleContentBlockStart handles content_block_start events
func (p *AnthropicProvider) handleContentBlockStart(payload map[string]interface{}, _ chan<- llm.StreamEvent, state *streamState) error {
	index, ok := payload["index"].(float64)
	if !ok {
		return nil
	}

	contentBlock, ok := payload["content_block"].(map[string]interface{})
	if !ok {
		return nil
	}

	blockType, _ := contentBlock["type"].(string) //nolint:errcheck // type assertion for optional field
	id, _ := contentBlock["id"].(string)          //nolint:errcheck // type assertion for optional field
	name, _ := contentBlock["name"].(string)      //nolint:errcheck // type assertion for optional field

	state.startBlock(int(index), blockType, id, name)
	return nil
}

// handleContentDelta handles content block delta events
func (p *AnthropicProvider) handleContentDelta(payload map[string]interface{}, eventChan chan<- llm.StreamEvent, state *streamState) error {
	delta, ok := payload["delta"].(map[string]interface{})
	if !ok {
		return nil
	}

	// Check the delta type
	deltaType, _ := delta["type"].(string) //nolint:errcheck // type assertion for optional field

	switch deltaType {
	case "text_delta":
		if text, ok := delta["text"].(string); ok {
			state.appendText(text)
			eventChan <- llm.TextDeltaEvent{Delta: text}
		}

	case "thinking_delta":
		if thinking, ok := delta["thinking"].(string); ok {
			state.appendText(thinking)
			eventChan <- llm.ReasoningDeltaEvent{Delta: thinking}
		}

	case "input_json_delta":
		if partialJSON, ok := delta["partial_json"].(string); ok {
			state.appendInput(partialJSON)
		}
	}

	return nil
}

// handleContentBlockStop handles content_block_stop events
func (p *AnthropicProvider) handleContentBlockStop(_ map[string]interface{}, eventChan chan<- llm.StreamEvent, state *streamState) error {
	// Get the tool call info before finishBlock() clears it
	tc := state.lastToolCall()

	state.finishBlock()

	// If we just finished a tool_use block, emit ToolCallEvent
	if tc != nil {
		eventChan <- llm.ToolCallEvent{
			ToolCallID: tc.ToolCallID,
			ToolName:   tc.ToolName,
			Input:      tc.Input,
		}
	}
	return nil
}

// handleMessageDelta handles message-level delta events (usage, etc.)
func (p *AnthropicProvider) handleMessageDelta(payload map[string]interface{}, _ chan<- llm.StreamEvent, state *streamState) error {
	// Check for stop_reason in delta
	if delta, ok := payload["delta"].(map[string]interface{}); ok {
		if stopReason, ok := delta["stop_reason"].(string); ok {
			// Check for error stop reasons
			// Valid: "end_turn", "max_tokens", "stop_sequence", "tool_use", "pause_turn"
			// Error: "refusal" (model refused to respond due to safety/content policy)
			if stopReason == "refusal" {
				return fmt.Errorf("model refused to respond: content policy violation")
			}
			// Check for unknown stop reasons
			if stopReason != "" && stopReason != "end_turn" && stopReason != "max_tokens" &&
				stopReason != "stop_sequence" && stopReason != "tool_use" && stopReason != "pause_turn" {
				return fmt.Errorf("stream finished with unexpected stop reason: %s", stopReason)
			}
			state.setStopReason(stopReason)
		}
	}

	// Check for usage in payload["usage"]
	if usage, ok := payload["usage"].(map[string]interface{}); ok {
		p.extractAndSetUsage(usage, state)
	}
	return nil
}

// handleMessageStop handles message_stop events - sends final StepCompleteEvent
func (p *AnthropicProvider) handleMessageStop(payload map[string]interface{}, eventChan chan<- llm.StreamEvent, state *streamState) error {
	// Check for final usage in message_stop
	if usage, ok := payload["usage"].(map[string]interface{}); ok {
		p.extractAndSetUsage(usage, state)
	}

	// Send the accumulated message with usage
	eventChan <- llm.StepCompleteEvent{
		Messages: []llm.Message{state.getMessage()},
		Usage:    state.getUsage(),
	}
	return nil
}
