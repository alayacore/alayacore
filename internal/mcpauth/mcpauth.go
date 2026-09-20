// Package mcpauth runs the adapter-side half of MCP OAuth authorization.
//
// When the session reports that an MCP server requires authorization, the
// adapter must answer with a real authorization code (or decline it). This
// package performs that automatically and without user typing: it starts a
// loopback callback server, substitutes the {{redirect_uri}}/{{state}}
// placeholders into the authorization URL, opens the browser, and — when
// the provider redirects back — submits the code as an :mcp_confirm CI
// frame.
//
// The plainio and terseio adapters share this flow. Both read their real
// input from stdin (often a pipe that is already at EOF), so neither can
// ask the user to type a code; the automatic path is the only one that can
// finish. The terminal adapter keeps its own copy because its flow is
// interleaved with its confirmation dialog — folding it in here is a
// separate change.
//
// Concurrency: one goroutine per server. The adapter supplies the CI
// writer (a single serialized writer shared with its stdin feeder) and a
// progress printer; both must be safe for concurrent use.
package mcpauth

import (
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/alayacore/alayacore/internal/commands"
	"github.com/alayacore/alayacore/internal/platform"
)

// DefaultTimeout bounds the wait for an OAuth callback. On expiry the flow
// hands the server to the adapter's Unresolved policy.
const DefaultTimeout = 5 * time.Minute

// Printf writes one progress line to the adapter's diagnostic stream
// (stdout for plainio, stderr for terseio). The line is fully formatted —
// the flow includes the "[mcp: …]" decoration — so each adapter only
// chooses the destination.
type Printf func(format string, args ...any)

// Action tells Flow what to do after an automatic authorization could not
// complete (the browser could not be opened, or the callback timed out).
type Action int

const (
	// Decline abandons this server so MCP initialization can settle; the
	// other servers and any in-flight authorization are unaffected. This is
	// the only honest outcome for an adapter that cannot receive a typed
	// code.
	Decline Action = iota
	// WaitManual keeps the flow alive after printing manual instructions,
	// for an adapter whose user can still type :mcp_confirm (plainio on a
	// terminal). The session completes the server when that command arrives.
	WaitManual
)

// Unresolved decides a server's fate when its automatic authorization did
// not complete. reason is already worded for display. A nil Unresolved
// declines and prints the reason.
type Unresolved func(server, redirectURI, reason string) Action

// Flow runs the automatic OAuth callback flow, one goroutine per server.
type Flow struct {
	// send submits one command (no leading ':') as a CI frame. It is the
	// adapter's own command writer, so CO results correlate with the
	// adapter's name tracking. nil disables submission (tests only).
	send   func(cmd string) error
	printf Printf

	// unresolved is the adapter's policy for a failed automatic attempt.
	unresolved Unresolved

	// Seams, defaulted by New and overridden in tests.
	startServer func(listenAddr, state, serverName string) (<-chan platform.CallbackResult, string, func())
	openURL     func(string) error
	timeout     time.Duration

	mu   sync.Mutex
	runs map[string]*run
}

// run is the per-server cancel state of one flow. stop is idempotent: it is
// triggered by the server's "connected" event (authorization completed via
// another path) and by Abort (MCP init done/canceled).
type run struct {
	cancel     chan struct{}
	cancelOnce sync.Once
}

func (r *run) stop() { r.cancelOnce.Do(func() { close(r.cancel) }) }

// New creates a Flow that submits commands through send and reports
// progress through print.
func New(send func(cmd string) error, printf Printf) *Flow {
	return &Flow{
		send:        send,
		printf:      printf,
		startServer: platform.StartCallbackServer,
		openURL:     platform.OpenURL,
		timeout:     DefaultTimeout,
		runs:        make(map[string]*run),
	}
}

// SetUnresolved installs the policy for a server whose automatic
// authorization did not complete. Must be called before the first Start.
func (f *Flow) SetUnresolved(u Unresolved) { f.unresolved = u }

// Start begins the automatic flow for one server. It is non-blocking (all
// work happens in its own goroutine) and ignores a duplicate for a server
// that already has a flow in flight.
func (f *Flow) Start(server, authURL string) {
	f.mu.Lock()
	if _, ok := f.runs[server]; ok {
		f.mu.Unlock()
		return
	}
	r := &run{cancel: make(chan struct{})}
	f.runs[server] = r
	f.mu.Unlock()
	go f.run(server, authURL, r)
}

// Connected stops the flow for one server: its authorization was completed
// by another path (manual :mcp_confirm/:mcp_decline), so the callback
// server is no longer needed.
func (f *Flow) Connected(server string) {
	f.mu.Lock()
	r := f.runs[server]
	f.mu.Unlock()
	if r != nil {
		r.stop()
	}
}

// Abort stops every running flow. Called when MCP init completes — both
// natural completion and :mcp_cancel.
func (f *Flow) Abort() {
	f.mu.Lock()
	runs := make([]*run, 0, len(f.runs))
	for _, r := range f.runs {
		runs = append(runs, r)
	}
	f.mu.Unlock()
	for _, r := range runs {
		r.stop()
	}
}

// run executes one server's flow: callback server, browser, CI frame.
func (f *Flow) run(server, authURL string, r *run) {
	defer func() {
		f.mu.Lock()
		delete(f.runs, server)
		f.mu.Unlock()
	}()

	state := platform.RandomState()
	resultCh, redirectURI, cleanup := f.startServer("127.0.0.1:0", state, server)
	defer cleanup()

	filled := strings.ReplaceAll(authURL, "{{redirect_uri}}", url.QueryEscape(redirectURI))
	filled = strings.ReplaceAll(filled, "{{state}}", state)

	f.printf("\n[mcp: opening browser for %q…]\n", server)
	f.printf("[mcp: if the browser doesn't open, visit:]\n%s\n", filled)

	if err := f.openURL(filled); err != nil {
		if !f.unresolvedOrDecline(server, redirectURI, fmt.Sprintf("failed to open browser: %v", err)) {
			return
		}
	}

	select {
	case res := <-resultCh:
		if res.Err != nil {
			// Only skip this server — declining keeps the rest of MCP
			// init (and any other server's authorization) intact.
			f.printf("\n[mcp: authorization callback error: %v]\n", res.Err)
			f.Decline(server)
			return
		}
		f.Confirm(server, res.Code, redirectURI, res.Iss)
	case <-r.cancel:
		// Server connected via another path, or MCP init finished/canceled.
	case <-time.After(f.timeout):
		f.unresolvedOrDecline(server, redirectURI, fmt.Sprintf("authorization for %q timed out", server))
	}
}

// unresolvedOrDecline applies the adapter's policy for a failed automatic
// authorization. It returns true when the flow should keep waiting (the
// manual path) and false when the server was declined and the flow must
// stop.
func (f *Flow) unresolvedOrDecline(server, redirectURI, reason string) bool {
	if f.unresolved != nil {
		if f.unresolved(server, redirectURI, reason) == WaitManual {
			return true
		}
	} else {
		f.printf("\n[mcp: %s — declining %q]\n", reason, server)
	}
	f.Decline(server)
	return false
}

// Confirm submits an authorization code as a :mcp_confirm CI frame.
func (f *Flow) Confirm(server, code, redirectURI, iss string) {
	cmd := fmt.Sprintf("%s %s %s %s", commands.CommandNameMCPConfirm, server, code, redirectURI)
	if iss != "" {
		cmd += " " + iss
	}
	f.command(cmd)
}

// Decline skips one server as a :mcp_decline CI frame.
func (f *Flow) Decline(server string) {
	f.command(fmt.Sprintf("%s %s", commands.CommandNameMCPDecline, server))
}

func (f *Flow) command(cmd string) {
	if f.send == nil {
		return
	}
	if err := f.send(cmd); err != nil {
		f.printf("\n[mcp: failed to send %q: %v]\n", cmd, err)
	}
}
