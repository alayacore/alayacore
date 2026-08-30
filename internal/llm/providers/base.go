// Package providers implements LLM provider clients.
//
// This file contains the shared provider infrastructure used by
// both Anthropic and OpenAI providers. It handles:
//   - Common configuration (BaseConfig)
//   - Shared provider fields (baseProvider)
//   - HTTP request construction and response handling
//
// Provider-specific wire formats (message conversion, event parsing,
// tool formatting, and SSE scanning) live in anthropic.go and openai.go
// respectively.
package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/alayacore/alayacore/internal/llm"
)

// ============================================================================
// Shared Provider Boilerplate
// ============================================================================

// BaseConfig holds the common configuration shared by all LLM providers.
type BaseConfig struct {
	APIKey     string
	BaseURL    string
	Model      string
	HTTPClient *http.Client
	MaxTokens  int // 0 means use provider default

	// ReasoningConfigs is the user-supplied per-level provider wire-format
	// JSON merged into the request body. Keyed by reasoning level (0=off,
	// 1=normal, 2=max). Nil/empty entries mean "no fields added for this
	// level" so providers can short-circuit when nothing is configured.
	ReasoningConfigs map[int]json.RawMessage
}

// baseProvider holds the common fields shared by all LLM providers.
// Embedded by AnthropicProvider and OpenAIProvider.
type baseProvider struct {
	apiKey           string
	baseURL          string
	client           *http.Client
	model            string
	maxTokens        int
	reasoningLevel   int                     // 0=off, 1=normal, 2=max — UI display, session persistence, and message-layer empty-padding switch
	reasoningConfigs map[int]json.RawMessage // per-level raw provider JSON merged into request body; nil entries omitted
	videoFPS         int                     // frames per second for video attachments; 0 means default (2)
	videoRes         int                     // video resolution mode: 0 or 1
}

// setBaseConfig applies the common config to a baseProvider.
func (b *baseProvider) setBaseConfig(cfg BaseConfig, defaultModel string) {
	b.apiKey = cfg.APIKey
	b.baseURL = strings.TrimSuffix(cfg.BaseURL, "/")
	b.model = cfg.Model
	if b.model == "" {
		b.model = defaultModel
	}
	b.client = cfg.HTTPClient
	if b.client == nil {
		b.client = &http.Client{}
	}
	b.maxTokens = cfg.MaxTokens
	if b.maxTokens == 0 {
		b.maxTokens = llm.DefaultMaxTokens
	}
	if len(cfg.ReasoningConfigs) > 0 {
		b.reasoningConfigs = make(map[int]json.RawMessage, len(cfg.ReasoningConfigs))
		for k, v := range cfg.ReasoningConfigs {
			if len(v) > 0 {
				b.reasoningConfigs[k] = v
			}
		}
	}
}

// mergeReasoningConfig copies the top-level keys from the configured
// per-level reasoning JSON into body. Returns body unchanged when no
// reasoning config is registered for the current reasoning level.
//
// The user's JSON must be a flat object whose top-level keys are
// top-level request-body keys (e.g. {"thinking":{...},"output_config":{...}}
// for Anthropic). Keys are merged as parsed values, so nested objects
// become map[string]any — matching the rest of the body shape built by
// the provider. Unknown top-level keys pass through unchanged, so
// non-standard provider extensions don't need provider changes.
//
// Empty/whitespace-only JSON is treated as "no config for this level"
// and yields no merge — keeping model.conf backward compatible: missing
// reasoning_N fields never inject any wire fields.
func (b *baseProvider) mergeReasoningConfig(body map[string]any) map[string]any {
	raw, ok := b.reasoningConfigs[b.reasoningLevel]
	if !ok || len(raw) == 0 {
		return body
	}
	var overrides map[string]json.RawMessage
	if err := json.Unmarshal(raw, &overrides); err != nil {
		// Malformed JSON — leave body untouched. Providers don't
		// have a clean error channel during body construction, so
		// silent skip is safer than aborting the request. The
		// config parser should have caught obvious errors at
		// load time; this only triggers on genuinely invalid
		// JSON bytes (rare/impossible via config parser).
		return body
	}
	for k, v := range overrides {
		var parsed any
		if err := json.Unmarshal(v, &parsed); err != nil {
			continue
		}
		body[k] = parsed
	}
	return body
}

// buildRequest creates an HTTP POST request with common headers.
func (b *baseProvider) buildRequest(ctx context.Context, urlSuffix string, body any) (*http.Request, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", b.baseURL+urlSuffix, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// maxErrorBodyBytes bounds how much of a non-200 response body is read;
// errorSnippetMaxBytes bounds how much of it is shown. Provider error bodies
// are a few hundred bytes, so both are invisible in normal use — they exist so
// that a misbehaving endpoint (or a proxy returning an HTML error page) cannot
// allocate unbounded memory or flood the model's context through an error
// message.
const (
	maxErrorBodyBytes    = 1 << 20 // 1MB
	errorSnippetMaxBytes = 2048
)

// errorBodySnippet renders a bounded, single-line-ish excerpt of an error body.
func errorBodySnippet(body []byte) string {
	if len(body) <= errorSnippetMaxBytes {
		return string(body)
	}
	return string(body[:errorSnippetMaxBytes]) + "…[error body truncated]"
}

// doRequest sends the request and handles non-200 responses.
// Returns the response body reader (caller must close).
func (b *baseProvider) doRequest(req *http.Request) (io.ReadCloser, error) {
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Bound the read: a proxy, captive portal, or compromised endpoint
		// returning a giant body used to be allocated in full and then
		// interpolated verbatim into an error string that reaches both the
		// terminal and the model's context.
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("API error (status %d): failed to read error body: %w", resp.StatusCode, readErr)
		}
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, errorBodySnippet(body))
	}

	return resp.Body, nil
}
