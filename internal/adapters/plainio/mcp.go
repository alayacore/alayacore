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
// readPrompts and the SIGINT handler. The adapter wraps that writer in a
// lockedWriter (adapter.go), so every frame is written atomically and
// concurrent writes cannot interleave.

import (
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/alayacore/alayacore/internal/commands"
	"github.com/alayacore/alayacore/internal/platform"
)

// mcpAuthTimeout bounds the wait for the OAuth callback. On expiry the
// flow prints a hint and stops the callback server; the user can still
// complete authorization manually with :mcp_confirm/:mcp_decline.
const mcpAuthTimeout = 5 * time.Minute

// mcpAuthFlow runs MCP OAuth authorization flows — one goroutine per
// server. MCP servers initialize concurrently, so several servers may
// demand authorization at the same time; each gets its own callback
// server and browser window. A flow ends when its server connects (via
// any path), when MCP init completes or is canceled (broadcast abort),
// or on timeout.
type mcpAuthFlow struct {
	out         *stdoutOutput
	inputMu     sync.Mutex // guards input (set after StartSession returns)
	input       io.Writer
	startServer func(listenAddr, state, serverName string) (<-chan platform.CallbackResult, string, func())
	openURL     func(string) error
	authTimeout time.Duration

	mu   sync.Mutex
	runs map[string]*mcpAuthRun // server names → in-flight flow
}

// mcpAuthRun is the per-server cancel state of one flow. cancelFlow is
// idempotent: it may be triggered by the server's "connected" event
// (authorization completed via manual :mcp_confirm/:mcp_decline) and by
// the broadcast abort (MCP init done/canceled).
type mcpAuthRun struct {
	cancel     chan struct{}
	cancelOnce sync.Once
}

func (r *mcpAuthRun) cancelFlow() {
	r.cancelOnce.Do(func() { close(r.cancel) })
}

// newMCPAuthFlow creates a flow bound to out. The TLV input writer is
// attached later via setInput, once StartSession has returned it.
func newMCPAuthFlow(out *stdoutOutput) *mcpAuthFlow {
	return &mcpAuthFlow{
		out:         out,
		startServer: platform.StartCallbackServer,
		openURL:     platform.OpenURL,
		authTimeout: mcpAuthTimeout,
		runs:        make(map[string]*mcpAuthRun),
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
// output's deferred-hook queue — outside the output lock (see deferHook)
// — so it must not block; all work happens in the spawned goroutine.
// Printing is done in run to keep output ordering. A duplicate
// auth_required for a server that already has a flow in flight is ignored.
func (f *mcpAuthFlow) start(serverName, authURL string) {
	f.mu.Lock()
	if _, ok := f.runs[serverName]; ok {
		f.mu.Unlock()
		return
	}
	run := &mcpAuthRun{cancel: make(chan struct{})}
	f.runs[serverName] = run
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
	go f.run(serverName, authURL, input, run)
}

// run executes one server's flow: callback server, browser, CI frame.
func (f *mcpAuthFlow) run(serverName, authURL string, input io.Writer, run *mcpAuthRun) {
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
			// Only skip this server — declining keeps the rest of MCP
			// init (and any other servers' authorizations) intact.
			f.out.printLine("[mcp: authorization callback error: %v]\n", res.Err)
			f.sendCommand(input, fmt.Sprintf(":%s %s", commands.CommandNameMCPDecline, serverName))
			return
		}
		cmd := fmt.Sprintf(":%s %s %s %s", commands.CommandNameMCPConfirm, serverName, res.Code, redirectURI)
		if res.Iss != "" {
			cmd += " " + res.Iss
		}
		f.sendCommand(input, cmd)
	case <-run.cancel:
		// Server connected via another path (manual :mcp_confirm /
		// :mcp_decline) or MCP init finished/canceled — cleanup() stops
		// the callback server.
	case <-time.After(f.authTimeout):
		f.out.printLine("[mcp: authorization for %q timed out — continue manually]\n", serverName)
	}
}

// connected cancels the flow for one server: its authorization was
// completed by another path (manual :mcp_confirm/:mcp_decline), so the
// callback server is no longer needed.
func (f *mcpAuthFlow) connected(server string) {
	f.mu.Lock()
	run := f.runs[server]
	f.mu.Unlock()
	if run != nil {
		run.cancelFlow()
	}
}

// abort cancels every running flow. Called when MCP init completes
// ("done"), covering both natural completion and :mcp_cancel.
func (f *mcpAuthFlow) abort() {
	f.mu.Lock()
	for _, run := range f.runs {
		run.cancelFlow()
	}
	f.mu.Unlock()
}

// sendCommand writes a colon-command as a CI frame to the TLV input.
func (f *mcpAuthFlow) sendCommand(input io.Writer, cmd string) {
	if err := writeCommand(input, cmd); err != nil {
		f.out.printLine("[mcp: failed to send %q: %v]\n", cmd, err)
	}
}
