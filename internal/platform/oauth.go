// Package platform provides OS-level utilities for adapter implementations
// (e.g., opening a URL in the default browser, starting an OAuth callback
// server on localhost).
package platform

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"net"
	"net/http"
	"time"
)

// CallbackResult holds the result of the OAuth callback HTTP request.
type CallbackResult struct {
	Code string
	// Iss is the RFC 9207 `iss` parameter from the authorization response,
	// used to validate the issuer before redeeming the code.
	Iss string
	Err error
}

// RandomState generates a random hex string suitable for OAuth state
// (CSRF protection). Returns a 32-character hex string (128 bits).
func RandomState() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

// StartCallbackServer starts a local HTTP server to receive the OAuth
// authorization code callback. It validates the state parameter to prevent
// CSRF attacks.
//
// Only requests that carry this flow's state may conclude the flow: a valid
// authorization code (code + matching state) or a provider-reported error
// (error + matching state). Stray or stale requests to /callback (no
// parameters, wrong state, spoofed error without state) are answered with a
// notice page but never consume the result slot, so an unrelated first
// request cannot abort the real authorization. The first genuine outcome
// wins; later requests are dropped.
//
// listenAddr is the TCP address to bind to (e.g., "127.0.0.1:0" for
// loopback with random port, "0.0.0.0:0" for all interfaces).
// serverName is the human-readable name of the server being authorized
// (used in the response page shown to the user).
//
// Returns:
//   - resultCh: channel that receives the callback result (code or error)
//   - redirectURI: the full redirect URI the server is listening on
//   - cleanup: function to stop the server (must be called when done)
func StartCallbackServer(listenAddr, state, serverName string) (<-chan CallbackResult, string, func()) {
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		ch := make(chan CallbackResult, 1)
		ch <- CallbackResult{Err: fmt.Errorf("callback listener: %w", err)}
		close(ch)
		return ch, "", func() {}
	}

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		listener.Close()
		ch := make(chan CallbackResult, 1)
		ch <- CallbackResult{Err: fmt.Errorf("callback listener: unexpected addr type %T", listener.Addr())}
		close(ch)
		return ch, "", func() {}
	}
	port := addr.Port

	// Use the listen host for the redirect URI, but fall back to
	// loopback if bound to all interfaces (0.0.0.0 or empty host).
	redirectHost := addr.IP.String()
	if redirectHost == "" || addr.IP.IsUnspecified() {
		redirectHost = "127.0.0.1"
	}
	redirectURI := fmt.Sprintf("http://%s:%d/callback", redirectHost, port)

	resultCh := make(chan CallbackResult, 1)

	writePage := func(w http.ResponseWriter, title, body string) {
		html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head><meta charset="utf-8"><title>%s</title></head>
<body style="display:flex;justify-content:center;align-items:center;height:100vh;font-family:sans-serif;">
<div style="text-align:center;">
<h2>%s</h2>
<p style="color:#666;">You can close this window.</p>
</div>
</body>
</html>`, title, body)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		code := q.Get("code")
		returnedState := q.Get("state")
		errStr := q.Get("error")

		// Only a request carrying this flow's state may conclude the flow.
		// Stray requests (no parameters), stale callbacks (wrong state) and
		// spoofed error parameters (no state) are answered with a notice
		// page but never consume the result slot — otherwise the first such
		// request would abort the authorization before the real callback
		// arrives. Real callbacks always carry the flow's state, and
		// provider error responses echo it too.
		if returnedState != state || (code == "" && errStr == "") {
			writePage(w, "Not an Authorization Callback",
				fmt.Sprintf("This request is not the authorization callback for <strong>%s</strong>.", html.EscapeString(serverName)))
			return
		}
		if errStr != "" {
			// Provider-reported error (e.g. access_denied) with valid state.
			errDesc := q.Get("error_description")
			select {
			case resultCh <- CallbackResult{Err: fmt.Errorf("authorization error: %s: %s", errStr, errDesc)}:
			default:
			}
			writePage(w, "Authorization Failed",
				fmt.Sprintf("Authorization failed for <strong>%s</strong>.", html.EscapeString(serverName)))
			return
		}
		// Valid authorization code with matching state.
		select {
		case resultCh <- CallbackResult{Code: code, Iss: q.Get("iss")}:
		default:
		}
		writePage(w, "Authorization Successful",
			fmt.Sprintf("Authorization successful for <strong>%s</strong>!", html.EscapeString(serverName)))
	})

	server := &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go server.Serve(listener) //nolint:errcheck // server is closed via cleanup

	return resultCh, redirectURI, func() { server.Close() }
}
