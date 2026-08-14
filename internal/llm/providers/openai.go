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
// 3. REASONING_CONTENT IN TOOL CALL CHAINS: Per DeepSeek's documentation,
//    between two user messages all intermediate assistant reasoning_content
//    must be passed back. When reasoning mode is enabled, reasoning_content
//    is always set (even as empty string) on assistant messages so that
//    messages containing only tool calls still satisfy this requirement.
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

type openAIRequest struct {
	Model               string               `json:"model"`
	Messages            []openAIMessage      `json:"messages"`
	Tools               []openAITool         `json:"tools,omitempty"`
	Stream              bool                 `json:"stream"`
	StreamOptions       *openAIStreamOptions `json:"stream_options,omitempty"`
	MaxCompletionTokens int                  `json:"max_completion_tokens,omitempty"`
	Temperature         float64              `json:"temperature,omitempty"`
	ReasoningEffort     string               `json:"reasoning_effort,omitempty"`
	Thinking            *openAIThinkingField `json:"thinking"`
}

// openAIThinkingField maps to the OpenAI/DeepSeek `thinking` API field.
// The wire name is "thinking" (provider API convention), while the
// codebase uses "reasoning" for the domain-level concept.
type openAIThinkingField struct {
	Type string `json:"type"`
}

type openAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAIMessage struct {
	Role             string           `json:"role"`
	Content          any              `json:"content,omitempty"`
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

// openAIDelta represents the delta content from a streaming chunk.
type openAIDelta struct {
	Content          string           `json:"content"`
	ReasoningContent string           `json:"reasoning_content"`
	ToolCalls        []openAIToolCall `json:"tool_calls"`
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
	apiMessages = append(apiMessages, openaiConvertContents(contents, p.reasoningLevel, p.videoFPS, p.videoRes)...)

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

	// Build request body with thinking config
	reqBody := openAIRequest{
		Model:    p.model,
		Messages: apiMessages,
		Tools:    apiTools,
		Stream:   true,
		StreamOptions: &openAIStreamOptions{
			IncludeUsage: true,
		},
		Thinking:        computeOpenAIThinking(p.reasoningLevel),
		ReasoningEffort: computeOpenAIReasoningEffort(p.reasoningLevel),
	}
	if p.maxTokens > 0 {
		reqBody.MaxCompletionTokens = p.maxTokens
	}

	// Build and send HTTP request
	req, err := p.buildRequest(ctx, "/chat/completions", reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	body, err := p.doRequest(req)
	if err != nil {
		return nil, err
	}

	return p.parseStream(body), nil
}

// ============================================================================
// SSE Stream Parsing (OpenAI data-only format)
// ============================================================================

// openaiScanner reads data-only SSE (OpenAI format).
//
// Each line with "data:" prefix is a complete event. Lines without this
// prefix are ignored. The caller receives the trimmed data string via Data().
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

// Next advances to the next data event.
// Returns false when the stream is exhausted or an error occurs.
func (s *openaiScanner) Next() bool {
	for s.scanner.Scan() {
		line := s.scanner.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "data:") {
			if len(trimmed) > 5 && trimmed[5] == ' ' {
				s.currentData = trimmed[6:] // "data: hello" → "hello"
			} else {
				s.currentData = trimmed[5:] // "data:hello" → "hello", "data:" → ""
			}
			return true
		}
		// Ignore event: lines, comments, blank lines, etc.
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

		for scanner.Next() {
			data := scanner.Data()
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

		// Emit tool call events from accumulators
		for _, tc := range state.getToolCompleteEvents() {
			if !yield(tc, nil) {
				return
			}
		}

		// Emit complete events for text and reasoning blocks.
		if text := state.textBuilder.String(); text != "" {
			if !yield(llm.TextCompleteEvent{Text: text, Index: 1}, nil) {
				return
			}
		}
		if reasoning := state.reasoningBuilder.String(); reasoning != "" {
			if !yield(llm.ReasoningCompleteEvent{Text: reasoning, Index: 0}, nil) {
				return
			}
		}

		yield(llm.StepCompleteEvent{
			Contents:   state.getContents(),
			Usage:      state.getUsage(),
			StopReason: state.getStopReason(),
		}, nil)
	}
}

// openAIStreamState tracks state across streaming events
// openAIToolAccumulator accumulates a single tool call across streaming deltas.
// OpenAI splits tool call data across delta events: one event carries id+name,
// subsequent events carry argument fragments keyed by index.
// This struct merges them, mirroring anthropicStreamState.blockAccumulator.
type openAIToolAccumulator struct {
	id   string
	name string
	args strings.Builder
}

type openAIStreamState struct {
	usage            llm.Usage
	stopReason       string
	textBuilder      strings.Builder
	reasoningBuilder strings.Builder
	toolAccumulators map[int]*openAIToolAccumulator // tool call index -> accumulator
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

func (s *openAIStreamState) addTextDelta(delta string) {
	s.textBuilder.WriteString(delta)
}

func (s *openAIStreamState) addReasoningDelta(delta string) {
	s.reasoningBuilder.WriteString(delta)
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

func (s *openAIStreamState) appendToolCallArgs(index int, args json.RawMessage) {
	acc := s.toolAccumulator(index)
	acc.args.WriteString(unquoteToolArg(args))
}

func (s *openAIStreamState) setToolCallName(index int, id, name string) {
	acc := s.toolAccumulator(index)
	acc.id = id
	acc.name = name
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

func (s *openAIStreamState) getToolCompleteEvents() []llm.ToolInputCompleteEvent {
	indices := s.toolIndices()
	result := make([]llm.ToolInputCompleteEvent, len(indices))
	for pos, i := range indices {
		acc := s.toolAccumulators[i]
		result[pos] = llm.ToolInputCompleteEvent{
			ID:    acc.id,
			Input: json.RawMessage(acc.args.String()),
			Index: 2 + i, // content block: 0=reasoning, 1=text, 2+=tools
		}
	}
	return result
}

// getContents assembles a []ContentPart from the three parallel OpenAI stream accumulators.
// OpenAI delivers reasoning, text, and tool calls as separate flat delta fields.
// This function merges them into a flat ContentPart array,
// matching the Anthropic-inspired content block model used by the rest of the codebase.
//
// Reasoning and text are always included as content blocks (even when empty) so that
// their fixed indices (0 and 1) match the delta event indices used during streaming.
// The agent strips empty placeholders after assigning history IDs via StepCompleteEvent.
func (s *openAIStreamState) getContents() []llm.ContentPart {
	indices := s.toolIndices()
	contents := make([]llm.ContentPart, 0, 2+len(indices))
	// Always add reasoning slot (index 0), may be empty placeholder.
	contents = append(contents, &llm.ReasoningPart{
		Text:            s.reasoningBuilder.String(),
		ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant},
	})
	// Always add text slot (index 1), may be empty placeholder.
	contents = append(contents, &llm.TextPart{
		Text:            s.textBuilder.String(),
		ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant},
	})
	for _, i := range indices {
		acc := s.toolAccumulators[i]
		contents = append(contents, &llm.ToolInputPart{
			ID:              acc.id,
			Input:           json.RawMessage(acc.args.String()),
			Name:            acc.name,
			ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant},
		})
	}
	return contents
}

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
	if delta.ReasoningContent != "" {
		state.addReasoningDelta(delta.ReasoningContent)
		if !yield(llm.ReasoningDeltaEvent{Delta: delta.ReasoningContent, Index: 0}, nil) {
			return false
		}
	}

	if delta.Content != "" {
		state.addTextDelta(delta.Content)
		if !yield(llm.TextDeltaEvent{Delta: delta.Content, Index: 1}, nil) {
			return false
		}
	}

	for _, tc := range delta.ToolCalls {
		// Process name first so acc.id is set before any argument delta.
		if tc.Function.Name != "" {
			state.setToolCallName(tc.Index, tc.ID, tc.Function.Name)
			if !yield(llm.ToolInputStartEvent{
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Index: 2 + tc.Index, // content block: 0=reasoning, 1=text, 2+=tools
			}, nil) {
				return false
			}
		}
		if len(tc.Function.Arguments) > 0 {
			state.appendToolCallArgs(tc.Index, tc.Function.Arguments)
			acc := state.toolAccumulator(tc.Index)
			if !yield(llm.ToolInputDeltaEvent{
				ID:    acc.id,
				Delta: unquoteToolArg(tc.Function.Arguments),
				Index: 2 + tc.Index, // content block: 0=reasoning, 1=text, 2+=tools
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
func openaiConvertContents(contents []llm.ContentPart, reasoningLevel int, videoFPS int, videoRes int) []openAIMessage {
	chunks := llm.GroupByRole(contents)
	if len(chunks) == 0 {
		return nil
	}

	apiMessages := make([]openAIMessage, 0, len(chunks))

	for _, chunk := range chunks {
		role := chunk[0].GetRole()

		if role == llm.RoleTool {
			apiMessages = append(apiMessages, openaiConvertToolOutputs(chunk)...)
			continue
		}

		apiMsg := openAIMessage{Role: string(role)}

		if role == llm.RoleAssistant && openaiHasToolInputs(chunk) {
			openaiConvertToolInputs(&apiMsg, chunk)
		} else {
			openaiConvertRegularContent(&apiMsg, chunk, videoFPS, videoRes)
		}

		if role == llm.RoleAssistant {
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

// openaiConvertToolOutputs converts tool result content to multiple OpenAI messages.
//
// Since OpenAI's API has no native is_error field (unlike Anthropic), tool
// results are JSON-wrapped with a "status" field so the model can distinguish
// success from failure structurally rather than guessing from text content.
func openaiConvertToolOutputs(contents []llm.ContentPart) []openAIMessage {
	results := make([]openAIMessage, 0, len(contents))
	for _, part := range contents {
		tr, ok := part.(*llm.ToolOutputPart)
		if !ok {
			continue
		}
		apiMsg := openAIMessage{
			Role:       string(llm.RoleTool),
			ToolCallID: tr.ID,
		}
		// Build combined text from all content parts.
		// OpenAI tool messages only support string content, so media
		// parts (image, audio, video, document) are summarized as text.
		var textParts []string
		for _, cp := range tr.Output {
			switch v := cp.(type) {
			case *llm.TextPart:
				textParts = append(textParts, v.Text)
			default:
				textParts = append(textParts, openaiMediaSummary(v))
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
	return results
}

// openaiMediaSummary returns a text summary of a media ContentPart for use
// in contexts where the API only supports string content (e.g. tool results).
// For DataURIs it returns a structured label like "[Image (image/jpeg)]";
// for remote URLs it includes the URL.
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
func openaiConvertRegularContent(apiMsg *openAIMessage, contents []llm.ContentPart, videoFPS int, videoRes int) {
	var contentParts []map[string]any
	for _, part := range contents {
		switch v := part.(type) {
		case *llm.TextPart:
			contentParts = append(contentParts, map[string]any{
				"type": "text",
				"text": v.Text,
			})
		case *llm.ImagePart:
			contentParts = append(contentParts, map[string]any{
				"type":      "image_url",
				"image_url": map[string]string{"url": v.URI},
			})
		case *llm.AudioPart:
			// Standard OpenAI format: parse DataURI into base64 + format.
			// Remote URLs are not supported (OpenAI API has no URL audio input).
			if mediaType, b64, ok := llm.ParseDataURI(v.URI); ok {
				format := strings.TrimPrefix(mediaType, "audio/")
				contentParts = append(contentParts, map[string]any{
					"type": "input_audio",
					"input_audio": map[string]string{
						"data":   b64,
						"format": format,
					},
				})
			} else {
				// Remote URL → text placeholder.
				contentParts = append(contentParts, map[string]any{
					"type": "text",
					"text": fmt.Sprintf("[Audio file: %s (remote URL not supported)]", v.URI),
				})
			}
		case *llm.VideoPart:
			contentParts = append(contentParts, openAIVideoPart(v.URI, videoFPS, videoRes))
		case *llm.DocumentPart:
			// Document (PDF) is not natively supported by OpenAI Chat Completions API.
			// Include a text placeholder so the model knows a document was attached.
			contentParts = append(contentParts, map[string]any{
				"type": "text",
				"text": fmt.Sprintf("[Document attached: %s (PDF not supported by this API, only filename available)]", v.URI),
			})
		}
	}
	if len(contentParts) == 0 {
		apiMsg.Content = ""
	} else {
		apiMsg.Content = contentParts
	}
}

// computeOpenAIThinking returns the thinking field for an OpenAI request.
// Returns "enabled" when reasoning is on, "disabled" when off.
func computeOpenAIThinking(level int) *openAIThinkingField {
	if level > config.ReasoningLevelOff {
		return &openAIThinkingField{Type: "enabled"}
	}
	return &openAIThinkingField{Type: "disabled"}
}

// computeOpenAIReasoningEffort returns the reasoning_effort value.
// Returns "high" for level 1, "xhigh" for level 2, empty string for off.
// Empty string is dropped by omitempty in the JSON request.
func computeOpenAIReasoningEffort(level int) string {
	if level <= config.ReasoningLevelOff {
		return ""
	}
	if level >= config.ReasoningLevelMax {
		return "xhigh"
	}
	return "high"
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
