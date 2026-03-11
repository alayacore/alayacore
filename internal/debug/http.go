package debug

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

var (
	debugWriter io.Writer
	initOnce    sync.Once
)

func Enable() {
	initOnce.Do(func() {
		// Try to create log file in executable directory
		execPath, err := os.Executable()
		if err != nil {
			// Fallback to current directory
			execPath = "alayacore"
		}

		execDir := filepath.Dir(execPath)
		if execDir == "." {
			execDir, _ = os.Getwd()
		}

		// Generate log file name: alayacore-debug-api-N.log
		baseName := "alayacore-debug-api"

		// Find next available log number
		logNum := 0
		var logFile *os.File
		for i := range 100 {
			logName := fmt.Sprintf("%s-%d.log", baseName, i)
			logPath := filepath.Join(execDir, logName)
			f, err := os.OpenFile(logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
			if err == nil {
				logFile = f
				logNum = i
				break
			}
		}

		if logFile != nil {
			debugWriter = logFile
			log.SetOutput(logFile)
			log.Printf("Debug log started: alayacore-debug-api-%d.log", logNum)
		} else {
			// Fallback to stderr if we can't create log file
			debugWriter = os.Stderr
		}
	})
}

func writef(format string, args ...any) {
	if debugWriter != nil {
		fmt.Fprintf(debugWriter, format, args...)
	}
}

// DebugTransport wraps an http.RoundTripper and logs requests and responses
type DebugTransport struct {
	Transport http.RoundTripper
}

// debugReader wraps an io.Reader to log each chunk of data as it's read
type debugReader struct {
	reader    io.Reader
	buf       []byte
	firstRead bool
}

func newDebugReader(r io.Reader) *debugReader {
	return &debugReader{
		reader:    r,
		buf:       make([]byte, 0, 4096),
		firstRead: true,
	}
}

func (dr *debugReader) Read(p []byte) (n int, err error) {
	n, err = dr.reader.Read(p)

	if n > 0 {
		chunk := p[:n]
		chunkStr := string(chunk)

		// Handle Server-Sent Events (SSE) format: "data: {...}\n"
		lines := strings.Split(chunkStr, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			// Skip "data: " prefix and try to parse as JSON
			jsonStr := line
			if rest, found := strings.CutPrefix(line, "data:"); found {
				jsonStr = strings.TrimSpace(rest)
			}

			// Try to parse as JSON and log it
			var jsonData map[string]any
			if json.Unmarshal([]byte(jsonStr), &jsonData) == nil {
				// Check if this is Anthropic streaming format (content as array)
				if content, ok := jsonData["content"].([]any); ok && len(content) > 0 {
					// Anthropic streaming format - check for content blocks
					for _, block := range content {
						blockMap, ok := block.(map[string]any)
						if !ok {
							continue
						}
						blockType, _ := blockMap["type"].(string)
						if blockType == "tool_use" {
							name, _ := blockMap["name"].(string)
							input, _ := blockMap["input"].(map[string]any)
							inputJson, _ := json.Marshal(input)
							writef("{ \"content\": { type: \"tool_use\", name: %q, input: %s } }\n", name, inputJson)
						} else if blockType == "thinking" {
							thinking, _ := blockMap["thinking"].(string)
							if len(thinking) > 0 && dr.firstRead {
								writef("<<< Response Stream\n")
								writef("Chunks:\n")
								dr.firstRead = false
							}
							writef("{ \"content\": { type: \"thinking\", ... } }\n")
						}
					}
				}

				// Full format for final chunks or other cases
				formatted, _ := json.MarshalIndent(jsonData, "", "  ")
				if dr.firstRead {
					writef("<<< Response Stream\n")
					writef("Chunks:\n")
					dr.firstRead = false
				}
				writef("%s\n", formatted)
			} else if jsonStr != "[DONE]" {
				// Not JSON and not [DONE], print raw line
				if dr.firstRead {
					writef("<<< Response Stream\n")
					writef("Chunks:\n")
					dr.firstRead = false
				}
				writef("%s\n", line)
			}
		}
	}

	return n, err
}

// RoundTrip implements the http.RoundTripper interface
func (t *DebugTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var requestBody []byte
	var isStreaming bool

	// Log request and check if streaming
	if req.Body != nil {
		requestBody, _ = io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(requestBody))

		var formattedBody any
		if err := json.Unmarshal(requestBody, &formattedBody); err == nil {
			// Check if streaming is enabled
			if reqBody, ok := formattedBody.(map[string]any); ok {
				if stream, ok := reqBody["stream"].(bool); ok && stream {
					isStreaming = true
				}
			}

			formattedBody, _ = json.MarshalIndent(formattedBody, "", "  ")
			writef(">>> Request\n")
			writef("%s %s %s\n", req.Method, req.URL.Path, req.URL.RawQuery)
			writef("Headers:\n")
			for k, v := range req.Header {
				if k == "Authorization" {
					writef("  %s: ***\n", k)
				} else {
					writef("  %s: %v\n", k, v)
				}
			}
			writef("Body:\n")
			writef("%s\n", formattedBody)
		} else {
			writef(">>> Request\n")
			writef("%s %s\n", req.Method, req.URL)
			writef("Body:\n")
			writef("%s\n", string(requestBody))
		}
		writef("--------------------------------------------------\n")
	}

	start := time.Now()

	// Perform the request
	resp, err := t.Transport.RoundTrip(req)
	if err != nil {
		writef("<<< Request failed after %v: %v\n", time.Since(start), err)
		return nil, err
	}

	// Log response
	writef("<<< Response\n")
	writef("%s %s\n", resp.Proto, resp.Status)
	writef("Headers:\n")
	for k, v := range resp.Header {
		writef("  %s: %v\n", k, v)
	}

	// Check response content type to confirm streaming
	contentType := resp.Header.Get("Content-Type")
	if isStreaming && strings.Contains(contentType, "text/event-stream") {
		writef("Body:\n")
		resp.Body = struct {
			io.Reader
			io.Closer
		}{
			Reader: newDebugReader(resp.Body),
			Closer: resp.Body,
		}
	} else {
		responseBody, _ := io.ReadAll(resp.Body)
		resp.Body = io.NopCloser(bytes.NewReader(responseBody))

		var formattedBody any
		if err := json.Unmarshal(responseBody, &formattedBody); err == nil {
			formattedBody, _ = json.MarshalIndent(formattedBody, "", "  ")
			writef("Body:\n")
			writef("%s\n", formattedBody)
		} else {
			dump, _ := httputil.DumpResponse(resp, false)
			writef("Body:\n")
			writef("%s\n", dump)
		}
		writef("--------------------------------------------------\n")
		writef("Time: %v\n", time.Since(start))
	}

	return resp, nil
}

// NewHTTPClient creates a new HTTP client with debug logging enabled
func NewHTTPClient() *http.Client {
	Enable()
	return &http.Client{
		Transport: &DebugTransport{
			Transport: http.DefaultTransport,
		},
	}
}

// NewHTTPClientWithProxy creates an HTTP client with proxy support
// Supports HTTP, HTTPS, and SOCKS5 proxies
func NewHTTPClientWithProxy(proxyURL string) (*http.Client, error) {
	proxyParsed, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL: %w", err)
	}

	transport := &http.Transport{}

	switch proxyParsed.Scheme {
	case "socks5", "socks5h":
		// SOCKS5 proxy
		var auth *proxy.Auth
		if proxyParsed.User != nil {
			password, _ := proxyParsed.User.Password()
			auth = &proxy.Auth{
				User:     proxyParsed.User.Username(),
				Password: password,
			}
		}
		dialer, err := proxy.SOCKS5("tcp", proxyParsed.Host, auth, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("failed to create SOCKS5 dialer: %w", err)
		}
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.Dial(network, addr)
		}
	default:
		// HTTP/HTTPS proxy
		transport.Proxy = http.ProxyURL(proxyParsed)
	}

	return &http.Client{
		Transport: transport,
	}, nil
}

// NewHTTPClientWithProxyAndDebug creates an HTTP client with both proxy and debug logging
func NewHTTPClientWithProxyAndDebug(proxyURL string) (*http.Client, error) {
	client, err := NewHTTPClientWithProxy(proxyURL)
	if err != nil {
		return nil, err
	}

	Enable()
	// Wrap the transport with debug logging
	client.Transport = &DebugTransport{
		Transport: client.Transport,
	}

	return client, nil
}
