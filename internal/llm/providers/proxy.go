package providers

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"

	"golang.org/x/net/proxy"
)

// NewHTTPClientWithProxy creates an HTTP client with proxy support.
// Supports HTTP, HTTPS, and SOCKS5 proxies.
//
// A proxy says how to reach the endpoint and nothing else: every other property
// of the client stays what the no-proxy path gets, which is why the transport is
// a clone of http.DefaultTransport rather than a fresh value. A zero-value
// http.Transport has no dial deadline, no TLS handshake deadline and no idle
// limit — an endpoint that accepts the connection and then goes quiet would hang
// there forever, where the default transport fails in 30s (connect) or 10s
// (handshake) and the agent retries. Losing those rails is not something a user
// asked for by naming a proxy, and the failure it produces is the one the retry
// policy cannot see: no error, so no notice, no re-send, no report.
func NewHTTPClientWithProxy(proxyURL string) (*http.Client, error) {
	proxyParsed, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL: %w", err)
	}

	// The one assertion this needs: http.DefaultTransport is a *http.Transport,
	// and nothing in this program replaces it.
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("http.DefaultTransport is not an *http.Transport")
	}
	transport := base.Clone()

	switch proxyParsed.Scheme {
	case "socks5", "socks5h":
		var auth *proxy.Auth
		if proxyParsed.User != nil {
			password, _ := proxyParsed.User.Password()
			auth = &proxy.Auth{
				User:     proxyParsed.User.Username(),
				Password: password,
			}
		}
		// SOCKS5 is the one case that forces the dial function to be replaced, so
		// it is handed the dial function DefaultTransport already uses (see
		// ctxDialFunc) rather than numbers of its own; DialContext also carries
		// the request's context into the connect and the SOCKS handshake, where
		// :cancel can end a dial still under way.
		if transport.DialContext == nil {
			return nil, fmt.Errorf("http.DefaultTransport dials without a context")
		}
		dialer, err := proxy.SOCKS5("tcp", proxyParsed.Host, auth, ctxDialFunc(transport.DialContext))
		if err != nil {
			return nil, fmt.Errorf("failed to create SOCKS5 dialer: %w", err)
		}
		// The clone inherits ProxyFromEnvironment, and the proxy the user named
		// is the dialer now: leaving the environment's proxy in place would chain
		// the two, so the SOCKS dial would reach the env-var proxy instead of the
		// endpoint.
		transport.Proxy = nil
		ctxDialer, ok := dialer.(proxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("SOCKS5 dialer does not dial with a context")
		}
		transport.DialContext = ctxDialer.DialContext
	default:
		transport.Proxy = http.ProxyURL(proxyParsed)
	}

	return &http.Client{
		Transport: transport,
	}, nil
}

// ctxDialFunc adapts a DialContext-style function to the two dialer interfaces
// SOCKS5 asks of its forward dialer at once: proxy.Dialer, and proxy.ContextDialer
// so the request's context survives the trip.
//
// It exists so the SOCKS5 branch can dial with http.DefaultTransport's own dial
// function — the one place the connect deadline and the keepalive settings live —
// instead of restating them as numbers that would then have to be kept in step
// with Go's defaults by hand.
//
// Dial is required by the parameter's type (proxy.Dialer), not by this program:
// SOCKS5 prefers ContextDialer when it is given one, so DialContext is the method
// that runs, and Dial never does.
type ctxDialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

func (f ctxDialFunc) Dial(network, addr string) (net.Conn, error) {
	return f(context.Background(), network, addr)
}

func (f ctxDialFunc) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return f(ctx, network, addr)
}
