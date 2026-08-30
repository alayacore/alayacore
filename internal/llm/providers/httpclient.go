package providers

// DebugTransport and debug-enabled HTTP client factories.
//
// DebugTransport wraps an http.RoundTripper to log requests and responses
// to a writer (file or stderr). Used when --debug-log is enabled.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	"github.com/alayacore/alayacore/internal/debug"
)

// DebugTransport wraps an http.RoundTripper and logs requests and responses
// to its Writer.
type DebugTransport struct {
	Transport http.RoundTripper
	Writer    io.Writer // where debug output is written
}

// debugReader wraps an io.Reader to log each chunk of data as it's read.
type debugReader struct {
	reader    io.Reader
	writer    io.Writer
	buf       []byte
	firstRead bool
}

func newDebugReader(r io.Reader, w io.Writer) *debugReader {
	return &debugReader{
		reader:    r,
		writer:    w,
		buf:       make([]byte, 0, 4096),
		firstRead: true,
	}
}

func (dr *debugReader) processContentBlock(block any) {
	blockMap, ok := block.(map[string]any)
	if !ok {
		return
	}
	blockType, _ := blockMap["type"].(string) // debug logging, optional field
	switch blockType {
	case "tool_use":
		name, _ := blockMap["name"].(string)           // debug logging
		input, _ := blockMap["input"].(map[string]any) // debug logging
		inputJSON, _ := json.Marshal(input)            // debug logging
		fmt.Fprintf(dr.writer, "{ \"content\": { type: \"tool_use\", name: %q, input: %s } }\n", name, inputJSON)
	case "thinking":
		thinking, _ := blockMap["thinking"].(string) // debug logging
		if len(thinking) > 0 && dr.firstRead {
			fmt.Fprintf(dr.writer, "<<< Response Stream\n")
			fmt.Fprintf(dr.writer, "Chunks:\n")
			dr.firstRead = false
		}
		fmt.Fprintf(dr.writer, "{ \"content\": { type: \"thinking\", ... } }\n")
	}
}

func (dr *debugReader) processJSONLine(jsonStr string) {
	var jsonData map[string]any
	if json.Unmarshal([]byte(jsonStr), &jsonData) != nil {
		return
	}

	if content, ok := jsonData["content"].([]any); ok && len(content) > 0 {
		for _, block := range content {
			dr.processContentBlock(block)
		}
	}

	formatted, _ := json.MarshalIndent(jsonData, "", "  ") // debug logging
	if dr.firstRead {
		fmt.Fprintf(dr.writer, "<<< Response Stream\n")
		fmt.Fprintf(dr.writer, "Chunks:\n")
		dr.firstRead = false
	}
	fmt.Fprintf(dr.writer, "%s\n", formatted)
}

func (dr *debugReader) ensureHeaderWritten() {
	if dr.firstRead {
		fmt.Fprintf(dr.writer, "<<< Response Stream\n")
		fmt.Fprintf(dr.writer, "Chunks:\n")
		dr.firstRead = false
	}
}

func (dr *debugReader) Read(p []byte) (n int, err error) {
	n, err = dr.reader.Read(p)

	if n > 0 {
		chunk := p[:n]
		chunkStr := string(chunk)

		for line := range strings.SplitSeq(chunkStr, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			jsonStr := line
			if rest, found := strings.CutPrefix(line, "data:"); found {
				jsonStr = strings.TrimSpace(rest)
			}

			var jsonData map[string]any
			if json.Unmarshal([]byte(jsonStr), &jsonData) == nil {
				dr.processJSONLine(jsonStr)
			} else if jsonStr != "[DONE]" {
				dr.ensureHeaderWritten()
				fmt.Fprintf(dr.writer, "%s\n", line)
			}
		}
	}

	return n, err
}

func (t *DebugTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	w := t.Writer
	var requestBody []byte
	var isStreaming bool

	if req.Body != nil {
		requestBody, _ = io.ReadAll(req.Body) // debug logging, best effort
		req.Body = io.NopCloser(bytes.NewReader(requestBody))

		var formattedBody any
		if err := json.Unmarshal(requestBody, &formattedBody); err == nil {
			if reqBody, ok := formattedBody.(map[string]any); ok {
				if stream, ok := reqBody["stream"].(bool); ok && stream {
					isStreaming = true
				}
			}

			formattedBody, _ = json.MarshalIndent(formattedBody, "", "  ") // debug logging
			fmt.Fprintf(w, ">>> Request\n")
			fmt.Fprintf(w, "%s %s %s\n", req.Method, req.URL.Path, req.URL.RawQuery)
			fmt.Fprintf(w, "Headers:\n")
			for k, v := range req.Header {
				if k == "Authorization" {
					fmt.Fprintf(w, "  %s: ***\n", k)
				} else {
					fmt.Fprintf(w, "  %s: %v\n", k, v)
				}
			}
			fmt.Fprintf(w, "Body:\n")
			fmt.Fprintf(w, "%s\n", formattedBody)
		} else {
			fmt.Fprintf(w, ">>> Request\n")
			fmt.Fprintf(w, "%s %s\n", req.Method, req.URL)
			fmt.Fprintf(w, "Body:\n")
			fmt.Fprintf(w, "%s\n", string(requestBody))
		}
	}

	start := time.Now()

	resp, err := t.Transport.RoundTrip(req)
	if err != nil {
		fmt.Fprintf(w, "<<< Request failed after %v: %v\n", time.Since(start), err)
		return nil, err
	}

	fmt.Fprintf(w, "<<< Response\n")
	fmt.Fprintf(w, "%s %s\n", resp.Proto, resp.Status)
	fmt.Fprintf(w, "Headers:\n")
	for k, v := range resp.Header {
		fmt.Fprintf(w, "  %s: %v\n", k, v)
	}

	contentType := resp.Header.Get("Content-Type")
	if isStreaming && strings.Contains(contentType, "text/event-stream") {
		fmt.Fprintf(w, "Body:\n")
		resp.Body = struct {
			io.Reader
			io.Closer
		}{
			Reader: newDebugReader(resp.Body, w),
			Closer: resp.Body,
		}
	} else {
		responseBody, _ := io.ReadAll(resp.Body) // debug logging, best effort
		resp.Body = io.NopCloser(bytes.NewReader(responseBody))

		var formattedBody any
		if err := json.Unmarshal(responseBody, &formattedBody); err == nil {
			formattedBody, _ = json.MarshalIndent(formattedBody, "", "  ") // debug logging
			fmt.Fprintf(w, "Body:\n")
			fmt.Fprintf(w, "%s\n", formattedBody)
		} else {
			dump, _ := httputil.DumpResponse(resp, false) // debug logging
			fmt.Fprintf(w, "Body:\n")
			fmt.Fprintf(w, "%s\n", dump)
		}
		fmt.Fprintf(w, "Time: %v\n", time.Since(start))
	}

	return resp, nil
}

// NewHTTPClient creates an HTTP client with optional proxy and debug logging.
//
//   - proxyURL: "" = no proxy; otherwise HTTP/HTTPS/SOCKS5 proxy URL.
//   - debugDir: "" = no debug logging; "." = write debug logs to CWD; any other
//     writable path (e.g. t.TempDir() in tests).
//
// Returns nil when both proxyURL and debugDir are empty (caller should use
// http.DefaultClient).
func NewHTTPClient(proxyURL, debugDir string) (*http.Client, error) {
	var transport http.RoundTripper

	if proxyURL != "" {
		client, err := NewHTTPClientWithProxy(proxyURL)
		if err != nil {
			return nil, err
		}
		transport = client.Transport
	}

	if debugDir != "" {
		if transport == nil {
			transport = http.DefaultTransport
		}
		// A debug log the user asked for but cannot be opened is reported,
		// not silently downgraded to stderr (see debug.NewDebugWriter).
		dw, err := debug.NewDebugWriter(debugDir, "alayacore-debug-api")
		if err != nil {
			return nil, err
		}
		transport = &DebugTransport{
			Transport: transport,
			Writer:    dw,
		}
	}

	if transport == nil {
		return nil, nil
	}

	return &http.Client{Transport: transport}, nil
}
