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
//    See `convertToolCalls()`.
//
// 3. REASONING SUPPORT: OpenAI-compatible APIs (DeepSeek, Qwen, etc.) use
//    `reasoning_content` field for thinking tokens. Handled in `handleEvent()`.

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

// openAIStreamState tracks state across streaming events
type openAIStreamState struct {
	mu               sync.Mutex
	textBuilder      strings.Builder
	reasoningBuilder strings.Builder
	toolCallArgs     map[int]*strings.Builder // tool call index -> arguments builder
	toolCalls        []llm.ToolCallPart
	usage            llm.Usage
}

func (s *openAIStreamState) addTextDelta(delta string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.textBuilder.WriteString(delta)
}

func (s *openAIStreamState) addReasoningDelta(delta string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reasoningBuilder.WriteString(delta)
}

func (s *openAIStreamState) appendToolCallArgs(index int, args json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.toolCallArgs == nil {
		s.toolCallArgs = make(map[int]*strings.Builder)
	}
	if _, exists := s.toolCallArgs[index]; !exists {
		s.toolCallArgs[index] = &strings.Builder{}
	}
	// Each arguments chunk may be a JSON string or raw JSON
	// If it starts with a quote, unquote it first
	if len(args) > 0 && args[0] == '"' {
		var unquoted string
		if err := json.Unmarshal(args, &unquoted); err == nil {
			s.toolCallArgs[index].WriteString(unquoted)
			return
		}
	}
	// Otherwise append as-is (building up JSON object)
	s.toolCallArgs[index].WriteString(string(args))
}

func (s *openAIStreamState) setToolCallName(index int, id, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Ensure tool calls slice is big enough
	for len(s.toolCalls) <= index {
		s.toolCalls = append(s.toolCalls, llm.ToolCallPart{
			Type: "tool_use",
		})
	}
	s.toolCalls[index].ToolCallID = id
	s.toolCalls[index].ToolName = name
}

func (s *openAIStreamState) finalizeToolCalls() {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Merge accumulated arguments into tool calls
	for i := range s.toolCalls {
		if builder, exists := s.toolCallArgs[i]; exists {
			args := builder.String()
			s.toolCalls[i].Input = json.RawMessage(args)
		}
	}
}

func (s *openAIStreamState) setUsage(usage llm.Usage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usage = usage
}

func (s *openAIStreamState) getToolCalls() []llm.ToolCallPart {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.toolCalls
}

func (s *openAIStreamState) getMessage() llm.Message {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Build content parts from accumulated text
	// Preallocate for reasoning + text + tool calls
	content := make([]llm.ContentPart, 0, 2+len(s.toolCalls))

	// Add reasoning first (thinking before response)
	if s.reasoningBuilder.Len() > 0 {
		content = append(content, llm.ReasoningPart{
			Type: "reasoning",
			Text: s.reasoningBuilder.String(),
		})
	}

	// Add text content
	if s.textBuilder.Len() > 0 {
		content = append(content, llm.TextPart{
			Type: "text",
			Text: s.textBuilder.String(),
		})
	}

	// Add tool calls
	for _, tc := range s.toolCalls {
		content = append(content, tc)
	}

	return llm.Message{
		Role:    llm.RoleAssistant,
		Content: content,
	}
}

func (s *openAIStreamState) getUsage() llm.Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usage
}

// OpenAIProvider implements the OpenAI API
type OpenAIProvider struct {
	apiKey  string
	baseURL string
	client  *http.Client
	model   string
}

// OpenAIOption configures the provider
type OpenAIOption func(*OpenAIProvider)

// NewOpenAI creates a new OpenAI provider
func NewOpenAI(opts ...OpenAIOption) (*OpenAIProvider, error) {
	p := &OpenAIProvider{
		baseURL: "https://api.openai.com/v1",
		client:  &http.Client{Timeout: 10 * time.Minute},
		model:   "gpt-4o",
	}
	for _, opt := range opts {
		opt(p)
	}
	if p.apiKey == "" {
		return nil, fmt.Errorf("API key is required")
	}
	return p, nil
}

// WithOpenAIAPIKey sets the API key
func WithOpenAIAPIKey(key string) OpenAIOption {
	return func(p *OpenAIProvider) {
		p.apiKey = key
	}
}

// WithOpenAIBaseURL sets the base URL
func WithOpenAIBaseURL(url string) OpenAIOption {
	return func(p *OpenAIProvider) {
		p.baseURL = strings.TrimSuffix(url, "/")
	}
}

// WithOpenAIHTTPClient sets the HTTP client
func WithOpenAIHTTPClient(client *http.Client) OpenAIOption {
	return func(p *OpenAIProvider) {
		p.client = client
	}
}

// WithOpenAIModel sets the model
func WithOpenAIModel(model string) OpenAIOption {
	return func(p *OpenAIProvider) {
		p.model = model
	}
}

// openAIRequest represents the OpenAI API request
type openAIRequest struct {
	Model         string               `json:"model"`
	Messages      []openAIMessage      `json:"messages"`
	Tools         []openAITool         `json:"tools,omitempty"`
	Stream        bool                 `json:"stream"`
	StreamOptions *openAIStreamOptions `json:"stream_options,omitempty"`
	MaxTokens     int                  `json:"max_tokens,omitempty"`
	Temperature   float64              `json:"temperature,omitempty"`
}

type openAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAIMessage struct {
	Role             string           `json:"role"`
	Content          interface{}      `json:"content,omitempty"`
	ReasoningContent string           `json:"reasoning_content,omitempty"`
	ToolCalls        []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string           `json:"tool_call_id,omitempty"`
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

// StreamMessages streams messages from OpenAI
func (p *OpenAIProvider) StreamMessages(
	ctx context.Context,
	messages []llm.Message,
	tools []llm.ToolDefinition,
	systemPrompt string,
	extraSystemPrompt string,
) (<-chan llm.StreamEvent, error) {
	// Convert messages to OpenAI format
	apiMessages := make([]openAIMessage, 0, len(messages)+2)

	// Add system messages separately
	if systemPrompt != "" {
		apiMessages = append(apiMessages, openAIMessage{
			Role:    "system",
			Content: systemPrompt,
		})
	}

	if extraSystemPrompt != "" {
		apiMessages = append(apiMessages, openAIMessage{
			Role:    "system",
			Content: extraSystemPrompt,
		})
	}

	// Convert conversation messages
	for _, msg := range messages {
		apiMsgs := p.convertMessages(msg)
		apiMessages = append(apiMessages, apiMsgs...)
	}

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

	// Build request
	reqBody := openAIRequest{
		Model:    p.model,
		Messages: apiMessages,
		Tools:    apiTools,
		Stream:   true,
		StreamOptions: &openAIStreamOptions{
			IncludeUsage: true,
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

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

// convertMessages converts our message to OpenAI format.
// For tool messages, returns multiple messages (one per tool result).
func (p *OpenAIProvider) convertMessages(msg llm.Message) []openAIMessage {
	// Handle tool results specially - return multiple messages, one per result
	if msg.Role == llm.RoleTool {
		return p.convertToolResults(msg.Content)
	}

	apiMsg := openAIMessage{
		Role: string(msg.Role),
	}

	// Handle assistant messages with tool calls
	if msg.Role == llm.RoleAssistant && p.hasToolCalls(msg.Content) {
		p.convertToolCalls(&apiMsg, msg.Content)
		return []openAIMessage{apiMsg}
	}

	// Regular text/reasoning content
	p.convertRegularContent(&apiMsg, msg.Content)
	return []openAIMessage{apiMsg}
}

// convertToolResults converts tool result content to multiple OpenAI messages
func (p *OpenAIProvider) convertToolResults(content []llm.ContentPart) []openAIMessage {
	results := make([]openAIMessage, 0, len(content))
	for _, part := range content {
		tr, ok := part.(llm.ToolResultPart)
		if !ok {
			continue
		}
		apiMsg := openAIMessage{
			Role:       string(llm.RoleTool),
			ToolCallID: tr.ToolCallID,
		}
		switch out := tr.Output.(type) {
		case llm.ToolResultOutputText:
			apiMsg.Content = out.Text
		case llm.ToolResultOutputError:
			apiMsg.Content = out.Error
		}
		results = append(results, apiMsg)
	}
	return results
}

// hasToolCalls checks if content contains tool calls
func (p *OpenAIProvider) hasToolCalls(content []llm.ContentPart) bool {
	for _, part := range content {
		if _, ok := part.(llm.ToolCallPart); ok {
			return true
		}
	}
	return false
}

// convertToolCalls handles conversion of assistant messages with tool calls
func (p *OpenAIProvider) convertToolCalls(apiMsg *openAIMessage, content []llm.ContentPart) {
	apiMsg.ToolCalls = make([]openAIToolCall, 0)
	for _, part := range content {
		if tc, ok := part.(llm.ToolCallPart); ok {
			// OpenAI expects arguments to be a JSON-encoded string
			// We need to marshal the raw JSON to a string
			argsStr, err := json.Marshal(string(tc.Input))
			if err != nil {
				argsStr = []byte("{}")
			}
			apiMsg.ToolCalls = append(apiMsg.ToolCalls, openAIToolCall{
				ID:   tc.ToolCallID,
				Type: "function",
				Function: openAIFunction{
					Name:      tc.ToolName,
					Arguments: argsStr,
				},
			})
		}
	}
	// Content can be nil for tool calls
	apiMsg.Content = nil
}

// convertRegularContent handles conversion of regular text/reasoning content
func (p *OpenAIProvider) convertRegularContent(apiMsg *openAIMessage, content []llm.ContentPart) {
	var contentParts []map[string]interface{}
	var reasoningText string
	for _, part := range content {
		switch v := part.(type) {
		case llm.TextPart:
			contentParts = append(contentParts, map[string]interface{}{
				"type": "text",
				"text": v.Text,
			})
		case llm.ReasoningPart:
			// Accumulate reasoning content
			reasoningText += v.Text
		}
	}

	// Set reasoning_content if present
	if reasoningText != "" {
		apiMsg.ReasoningContent = reasoningText
	}

	switch len(contentParts) {
	case 1:
		// Single text part - use simple string
		apiMsg.Content = contentParts[0]["text"]
	case 0:
		// No content parts
	default:
		apiMsg.Content = contentParts
	}
}

// parseStream parses the SSE stream from OpenAI
func (p *OpenAIProvider) parseStream(reader io.Reader, eventChan chan<- llm.StreamEvent) {
	defer close(eventChan)

	state := &openAIStreamState{}
	scanner := bufio.NewScanner(reader)

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		if err := p.handleEvent(data, eventChan, state); err != nil {
			eventChan <- llm.StreamErrorEvent{Error: err}
			return
		}
	}

	if err := scanner.Err(); err != nil {
		eventChan <- llm.StreamErrorEvent{Error: err}
		return
	}

	// Finalize tool calls and emit events
	state.finalizeToolCalls()
	for _, tc := range state.getToolCalls() {
		eventChan <- llm.ToolCallEvent{
			ToolCallID: tc.ToolCallID,
			ToolName:   tc.ToolName,
			Input:      tc.Input,
		}
	}

	// Send final StepCompleteEvent with accumulated message
	eventChan <- llm.StepCompleteEvent{
		Messages: []llm.Message{state.getMessage()},
		Usage:    state.getUsage(),
	}
}

// handleEvent handles a single SSE event
func (p *OpenAIProvider) handleEvent(data string, eventChan chan<- llm.StreamEvent, state *openAIStreamState) error {
	var streamResp struct {
		Choices []struct {
			Delta struct {
				Content          string           `json:"content"`
				ReasoningContent string           `json:"reasoning_content"`
				ToolCalls        []openAIToolCall `json:"tool_calls"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal([]byte(data), &streamResp); err != nil {
		return fmt.Errorf("failed to parse event: %w", err)
	}

	for _, choice := range streamResp.Choices {
		// Check for error finish reasons
		// Valid: "stop" (normal), "length" (truncated), "tool_calls" (function calling)
		// Error: "content_filter" (blocked by safety), anything else
		if choice.FinishReason == "content_filter" {
			return fmt.Errorf("content blocked by safety filter")
		}
		// Allow empty, "stop", "length", and "tool_calls"
		if choice.FinishReason != "" && choice.FinishReason != "stop" &&
			choice.FinishReason != "length" && choice.FinishReason != "tool_calls" {
			return fmt.Errorf("stream finished with unexpected reason: %s", choice.FinishReason)
		}

		// Handle reasoning content (DeepSeek, Qwen, etc.)
		if choice.Delta.ReasoningContent != "" {
			state.addReasoningDelta(choice.Delta.ReasoningContent)
			eventChan <- llm.ReasoningDeltaEvent{Delta: choice.Delta.ReasoningContent}
		}

		// Handle text content
		if choice.Delta.Content != "" {
			state.addTextDelta(choice.Delta.Content)
			eventChan <- llm.TextDeltaEvent{Delta: choice.Delta.Content}
		}

		// Handle tool calls - arguments may come in chunks
		for _, tc := range choice.Delta.ToolCalls {
			// Accumulate arguments (may come in multiple chunks)
			if len(tc.Function.Arguments) > 0 {
				state.appendToolCallArgs(tc.Index, tc.Function.Arguments)
			}
			// Set name and ID when available (usually first chunk)
			if tc.Function.Name != "" {
				state.setToolCallName(tc.Index, tc.ID, tc.Function.Name)
			}
		}
	}

	// Track usage if available (may come in a chunk with empty choices)
	if streamResp.Usage.PromptTokens > 0 || streamResp.Usage.CompletionTokens > 0 {
		state.setUsage(llm.Usage{
			InputTokens:  int64(streamResp.Usage.PromptTokens),
			OutputTokens: int64(streamResp.Usage.CompletionTokens),
		})
	}

	return nil
}
