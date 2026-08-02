package plainio

// MCP OAuth authorization flow.
//
// When the session reports "auth_required" for an MCP server, the adapter
// starts a local OAuth callback server (internal/platform), opens the
// browser, and — once the authorization code arrives — sends
// ":mcp_confirm <server> <code> <redirect_uri>" as a CI frame, mirroring
// the terminal adapter's startMCPAuthFlow. The URL and the manual
// fallback commands are always printed, so users without a browser (or
// with piped stdin) can complete the flow by hand.
//
// Concurrency: the flow writes CI frames to the same TLV input writer as
// readPrompts. That writer is the io.Pipe created by StartSession, whose
// Write calls are serialized and atomic per call — WriteTLV issues a
// single Write per frame, so concurrent writes cannot interleave frames.

import (
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/alayacore/alayacore/internal/platform"
)

// mcpAuthTimeout bounds the wait for the OAuth callback. On expiry the
// flow prints a hint and stops the callback server; the user can still
// complete authorization manually with :mcp_confirm/:mcp_decline.
const mcpAuthTimeout = 5 * time.Minute

// mcpAuthFlow runs MCP OAuth authorization flows — one goroutine per
// server. MCP servers initialize concurrently, so several servers may
// demand authorization at the same time; each gets its own callback
// server and browser window, and all share one abort broadcast.
type mcpAuthFlow struct {
	out         *stdoutOutput
	inputMu     sync.Mutex // guards input (set after StartSession returns)
	input       io.Writer
	startServer func(listenAddr, state, serverName string) (<-chan platform.CallbackResult, string, func())
	openURL     func(string) error
	authTimeout time.Duration

	mu        sync.Mutex
	runs      map[string]struct{} // server names with a flow in flight
	abortCh   chan struct{}       // closed by abort() — broadcast to all flows
	abortOnce sync.Once
}

// newMCPAuthFlow creates a flow bound to out. The TLV input writer is
// attached later via setInput, once StartSession has returned it.
func newMCPAuthFlow(out *stdoutOutput) *mcpAuthFlow {
	return &mcpAuthFlow{
		out:         out,
		startServer: platform.StartCallbackServer,
		openURL:     platform.OpenURL,
		authTimeout: mcpAuthTimeout,
		runs:        make(map[string]struct{}),
		abortCh:     make(chan struct{}),
	}
}

// setInput wires the TLV input writer (the io.Pipe writer returned by
// StartSession). Called once from the adapter after the session starts.
func (f *mcpAuthFlow) setInput(input io.Writer) {
	f.inputMu.Lock()
	defer f.inputMu.Unlock()
	f.input = input
}

// start begins the OAuth flow for one MCP server. It is invoked from the
// output goroutine (with the output lock held), so it must not block or
// print — all work happens in the spawned goroutine. A duplicate
// auth_required for a server that already has a flow in flight is ignored.
func (f *mcpAuthFlow) start(serverName, authURL string) {
	f.mu.Lock()
	if _, ok := f.runs[serverName]; ok {
		f.mu.Unlock()
		return
	}
	f.runs[serverName] = struct{}{}
	f.mu.Unlock()

	f.inputMu.Lock()
	input := f.input
	f.inputMu.Unlock()
	if input == nil {
		// Input not wired yet (startup race). The URL was already printed
		// by the output, so the user can fall back to the manual flow.
		f.mu.Lock()
		delete(f.runs, serverName)
		f.mu.Unlock()
		return
	}
	go f.run(serverName, authURL, input)
}

// run executes one server's flow: callback server, browser, CI frame.
func (f *mcpAuthFlow) run(serverName, authURL string, input io.Writer) {
	defer func() {
		f.mu.Lock()
		delete(f.runs, serverName)
		f.mu.Unlock()
	}()

	state := platform.RandomState()
	resultCh, redirectURI, cleanup := f.startServer("127.0.0.1:0", state, serverName)
	defer cleanup()

	filledURL := strings.ReplaceAll(authURL, "{{redirect_uri}}", url.QueryEscape(redirectURI))
	filledURL = strings.ReplaceAll(filledURL, "{{state}}", state)

	f.out.printLine("\n[mcp: opening browser for %q…]\n", serverName)
	f.out.printLine("[mcp: if the browser doesn't open, visit:]\n%s\n", filledURL)
	f.out.printLine("[mcp: manual: :mcp_confirm %s <code> <redirect_uri> · :mcp_decline %s]\n",
		serverName, serverName)

	if err := f.openURL(filledURL); err != nil {
		f.out.printLine("[mcp: failed to open browser: %v]\n", err)
	}

	select {
	case res := <-resultCh:
		if res.Err != nil {
			f.out.printLine("[mcp: authorization callback error: %v]\n", res.Err)
			f.sendCommand(input, ":mcp_cancel")
			return
		}
		f.sendCommand(input, fmt.Sprintf(":mcp_confirm %s %s %s", serverName, res.Code, redirectURI))
	case <-f.abortCh:
		// MCP init finished or was canceled — cleanup() stops the server.
	case <-time.After(f.authTimeout):
		f.out.printLine("[mcp: authorization for %q timed out — continue manually]\n", serverName)
	}
}

// abort releases every running flow. Called when MCP init completes
// ("done"), covering both natural completion and :mcp_cancel.
func (f *mcpAuthFlow) abort() {
	f.abortOnce.Do(func() { close(f.abortCh) })
}

// sendCommand writes a colon-command as a CI frame to the TLV input.
func (f *mcpAuthFlow) sendCommand(input io.Writer, cmd string) {
	if err := writeCommand(input, cmd); err != nil {
		f.out.printLine("[mcp: failed to send %q: %v]\n", cmd, err)
	}
}
