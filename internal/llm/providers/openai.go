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
	"iter"
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
	apiKey          string
	baseURL         string
	client          *http.Client
	model           string
	thinkingEnabled bool
}

// isDeepSeek returns true if the model is a DeepSeek model.
// DeepSeek models have special requirements for reasoning_content:
// they are always in thinking mode and require reasoning_content
// to be passed back in tool call scenarios.
func (p *OpenAIProvider) isDeepSeek() bool {
	return strings.Contains(strings.ToLower(p.model), "deepseek")
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

// SetThinkingEnabled enables or disables thinking mode for OpenAI.
func (p *OpenAIProvider) SetThinkingEnabled(enabled bool) {
	p.thinkingEnabled = enabled
}

// openAIRequest represents the OpenAI API request
type openAIRequest struct {
	Model           string               `json:"model"`
	Messages        []openAIMessage      `json:"messages"`
	Tools           []openAITool         `json:"tools,omitempty"`
	Stream          bool                 `json:"stream"`
	StreamOptions   *openAIStreamOptions `json:"stream_options,omitempty"`
	MaxTokens       int                  `json:"max_tokens,omitempty"`
	Temperature     float64              `json:"temperature,omitempty"`
	ReasoningEffort string               `json:"reasoning_effort,omitempty"`
}

type openAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAIMessage struct {
	Role             string           `json:"role"`
	Content          interface{}      `json:"content,omitempty"`
	ReasoningContent *string          `json:"reasoning_content,omitempty"`
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
) (iter.Seq2[llm.StreamEvent, error], error) {
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

	// Add reasoning effort when thinking mode is enabled.
	// DeepSeek models always require reasoning_effort (always in thinking mode).
	if p.thinkingEnabled || p.isDeepSeek() {
		reqBody.ReasoningEffort = "high"
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

	return p.parseStream(resp.Body), nil
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
	var reasoningText string
	for _, part := range content {
		switch v := part.(type) {
		case llm.ToolCallPart:
			// OpenAI expects arguments to be a JSON-encoded string
			// We need to marshal the raw JSON to a string
			argsStr, err := json.Marshal(string(v.Input))
			if err != nil {
				argsStr = []byte("{}")
			}
			apiMsg.ToolCalls = append(apiMsg.ToolCalls, openAIToolCall{
				ID:   v.ToolCallID,
				Type: "function",
				Function: openAIFunction{
					Name:      v.ToolName,
					Arguments: argsStr,
				},
			})
		case llm.ReasoningPart:
			reasoningText += v.Text
		}
	}
	// DeepSeek requires reasoning_content in tool call scenarios.
	// Always include it (even if empty) when there are tool calls.
	// Don't filter based on thinkingEnabled - the reasoning was already
	// generated by a previous model call and must be preserved for context.
	// See: https://api-docs.deepseek.com/zh-cn/guides/reasoning
	apiMsg.ReasoningContent = &reasoningText
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

	// Set reasoning_content based on provider requirements.
	// DeepSeek always requires reasoning_content for assistant messages
	// when there's tool call history (model is always in thinking mode).
	// Other providers only need it when thinking is enabled and content exists.
	if p.isDeepSeek() {
		// DeepSeek: always include reasoning_content (even empty string)
		// The model is always in thinking mode and may require it for
		// proper context handling in tool call scenarios.
		apiMsg.ReasoningContent = &reasoningText // "" if no reasoning
	} else if p.thinkingEnabled && reasoningText != "" {
		// Other providers: only when thinking enabled and content exists
		apiMsg.ReasoningContent = &reasoningText
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

// parseStream returns an iterator that yields SSE events from the OpenAI response.
func (p *OpenAIProvider) parseStream(reader io.Reader) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		defer func() {
			closer, ok := reader.(io.Closer)
			if ok {
				_ = closer.Close()
			}
		}()

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

			if !p.handleEvent(data, yield, state) {
				return
			}
		}

		if err := scanner.Err(); err != nil {
			yield(nil, err)
			return
		}

		// Finalize tool calls and emit events
		state.finalizeToolCalls()
		for _, tc := range state.getToolCalls() {
			if !yield(llm.ToolCallEvent{
				ToolCallID: tc.ToolCallID,
				ToolName:   tc.ToolName,
				Input:      tc.Input,
			}, nil) {
				return
			}
		}

		// Send final StepCompleteEvent with accumulated message
		yield(llm.StepCompleteEvent{
			Messages: []llm.Message{state.getMessage()},
			Usage:    state.getUsage(),
		}, nil)
	}
}

// handleEvent handles a single SSE event. Returns false if iteration should stop.
func (p *OpenAIProvider) handleEvent(data string, yield func(llm.StreamEvent, error) bool, state *openAIStreamState) bool {
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
		yield(nil, fmt.Errorf("failed to parse event: %w", err))
		return false
	}

	for _, choice := range streamResp.Choices {
		if ok, err := p.checkFinishReason(choice.FinishReason); !ok {
			yield(nil, err)
			return false
		}

		if ok := p.handleDelta(choice.Delta, yield, state); !ok {
			return false
		}
	}

	// Track usage if available (may come in a chunk with empty choices)
	if streamResp.Usage.PromptTokens > 0 || streamResp.Usage.CompletionTokens > 0 {
		state.setUsage(llm.Usage{
			CacheCreationTokens: 0,
			CacheReadTokens:     0,
			InputTokens:         int64(streamResp.Usage.PromptTokens),
			OutputTokens:        int64(streamResp.Usage.CompletionTokens),
		})
	}

	return true
}

// checkFinishReason validates the finish reason. Returns (ok, nil) for valid reasons,
// or (false, error) for error conditions.
func (p *OpenAIProvider) checkFinishReason(reason string) (bool, error) {
	// Valid: "stop" (normal), "length" (truncated), "tool_calls" (function calling)
	// Error: "content_filter" (blocked by safety), anything else
	if reason == "content_filter" {
		return false, fmt.Errorf("content blocked by safety filter")
	}
	// Allow empty, "stop", "length", and "tool_calls"
	if reason != "" && reason != "stop" && reason != "length" && reason != "tool_calls" {
		return false, fmt.Errorf("stream finished with unexpected reason: %s", reason)
	}
	return true, nil
}

// handleDelta processes the delta content from a streaming chunk.
// Returns false if iteration should stop.
func (p *OpenAIProvider) handleDelta(delta struct {
	Content          string           `json:"content"`
	ReasoningContent string           `json:"reasoning_content"`
	ToolCalls        []openAIToolCall `json:"tool_calls"`
}, yield func(llm.StreamEvent, error) bool, state *openAIStreamState) bool {
	// Handle reasoning content (DeepSeek, Qwen, etc.)
	if delta.ReasoningContent != "" {
		state.addReasoningDelta(delta.ReasoningContent)
		if !yield(llm.ReasoningDeltaEvent{Delta: delta.ReasoningContent}, nil) {
			return false
		}
	}

	// Handle text content
	if delta.Content != "" {
		state.addTextDelta(delta.Content)
		if !yield(llm.TextDeltaEvent{Delta: delta.Content}, nil) {
			return false
		}
	}

	// Handle tool calls - arguments may come in chunks
	for _, tc := range delta.ToolCalls {
		// Accumulate arguments (may come in multiple chunks)
		if len(tc.Function.Arguments) > 0 {
			state.appendToolCallArgs(tc.Index, tc.Function.Arguments)
		}
		// Set name and ID when available (usually first chunk)
		if tc.Function.Name != "" {
			state.setToolCallName(tc.Index, tc.ID, tc.Function.Name)
		}
	}

	return true
}
