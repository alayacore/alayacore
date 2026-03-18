// Package providers implements LLM provider clients
package providers

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

// AnthropicProvider implements the Anthropic API
type AnthropicProvider struct {
	apiKey  string
	baseURL string
	client  *http.Client
	model   string
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

// anthropicRequest represents the Anthropic API request
type anthropicRequest struct {
	Model        string                 `json:"model"`
	Messages     []anthropicMessage     `json:"messages"`
	MaxTokens    int                    `json:"max_tokens"`
	System       string                 `json:"system,omitempty"`
	Tools        []anthropicTool        `json:"tools,omitempty"`
	Stream       bool                   `json:"stream"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicCacheControl struct {
	Type string `json:"type"`
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
	case "tool_use":
		s.contentParts = append(s.contentParts, llm.ToolCallPart{
			Type:       "tool_use",
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
		InputTokens:  inputTokens + cacheReadTokens + cacheCreationTokens,
		OutputTokens: outputTokens,
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

// lastToolCall returns the last tool call if the current block is a tool_use
func (s *streamState) lastToolCall() *llm.ToolCallPart {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.currentType == "tool_use" {
		return &llm.ToolCallPart{
			Type:       "tool_use",
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
					Type:  "tool_use",
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

	// Build request
	reqBody := anthropicRequest{
		Model:     p.model,
		Messages:  apiMessages,
		MaxTokens: 4096,
		System:    systemPrompt,
		Tools:     apiTools,
		Stream:    true,
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
	case "content_block_start":
		return p.handleContentBlockStart(payload, eventChan, state)

	case "content_block_delta":
		return p.handleContentDelta(payload, eventChan, state)

	case "content_block_stop":
		return p.handleContentBlockStop(payload, eventChan, state)

	case "message_delta":
		return p.handleMessageDelta(payload, eventChan, state)

	case "message_stop":
		return p.handleMessageStop(eventChan, state)

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
	// Check for usage
	usage, ok := payload["usage"].(map[string]interface{})
	if !ok {
		// Also check in delta.usage
		delta, deltaOK := payload["delta"].(map[string]interface{})
		if deltaOK {
			usage, ok = delta["usage"].(map[string]interface{})
		}
	}

	if ok {
		inputTokens := 0.0
		if v, ok := usage["input_tokens"].(float64); ok {
			inputTokens = v
		}
		outputTokens := 0.0
		if v, ok := usage["output_tokens"].(float64); ok {
			outputTokens = v
		}
		// Cache tokens are part of input tokens
		cacheReadTokens := 0.0
		if v, ok := usage["cache_read_input_tokens"].(float64); ok {
			cacheReadTokens = v
		}
		cacheCreationTokens := 0.0
		if v, ok := usage["cache_creation_input_tokens"].(float64); ok {
			cacheCreationTokens = v
		}
		state.setUsage(int64(inputTokens), int64(outputTokens), int64(cacheReadTokens), int64(cacheCreationTokens))
	}

	return nil
}

// handleMessageStop handles message_stop events - sends final StepCompleteEvent
func (p *AnthropicProvider) handleMessageStop(eventChan chan<- llm.StreamEvent, state *streamState) error {
	// Send the accumulated message with usage
	eventChan <- llm.StepCompleteEvent{
		Messages: []llm.Message{state.getMessage()},
		Usage:    state.getUsage(),
	}
	return nil
}
