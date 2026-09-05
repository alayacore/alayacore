# MCP OAuth 2.1 Authorization

AlayaCore supports OAuth 2.1 **authorization_code** flow for MCP servers
that require user authentication. When a server declares `auth-type:
authorization_code`, AlayaCore handles the entire flow:

1. **Discovery** — Find the authorization server and its endpoints
2. **Authorization** — Open the user's browser to grant consent
3. **Token exchange** — Swap the authorization code for tokens
4. **Token refresh** — Automatically renew expired tokens

## Prerequisites

Before using `authorization_code` auth, you must **register an OAuth app**
with the service. For example, for GitHub Copilot:

1. Go to **GitHub Settings → Developer settings → OAuth Apps → New OAuth App**
2. Set the **Authorization callback URL** to `http://127.0.0.1` (AlayaCore
   will append the actual port and path at runtime)
3. After registration, note your **Client ID** and **Client Secret**

Then configure them in `mcp.conf`:

```ini
server: github
url: https://api.githubcopilot.com/mcp/
auth-type: authorization_code
auth-client-id: <your-client-id>
auth-client-secret: <your-client-secret>
```

> **Tip:** Some services (e.g. GitHub) support public OAuth clients that
> do not require a `client_secret` when used with PKCE. For those services,
> you can omit `auth-client-secret`.

## Configuration

```ini
server: github
url: https://api.githubcopilot.com/mcp/
auth-type: authorization_code
auth-client-id: Iv1.xxxxxxxxxxxxxxxx
auth-client-secret: xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

| Field | Required | Description |
|-------|----------|-------------|
| `auth-type` | Yes for OAuth | `authorization_code` or `static` |
| `auth-client-id` | Yes for `authorization_code` | Your OAuth app's client ID |
| `auth-client-secret` | No | OAuth client secret (required by most services) |
| `auth-scopes` | No | JSON array of OAuth scopes to request (e.g. `["repo", "gist"]`) |

For `static` auth, provide a pre-obtained token:

```ini
server: my-service
url: https://example.com/mcp
auth-type: static
auth-token: <your-token>
```

## Flow Overview

```
┌───────────┐      ┌──────────────────┐      ┌────────────────┐      ┌────────────┐
│  User     │      │  AlayaCore       │      │  Auth Server   │      │ MCP Server │
│ (Browser) │      │                  │      │  (e.g. GitHub) │      │            │
└────┬──────┘      └────────┬─────────┘      └────────┬───────┘      └─────┬──────┘
     │                      │                         │                    │
     │                      │  1. Connect             │                    │
     │                      │ ────────────────────────│───────────────────►│
     │                      │                         │                    │
     │                      │  2. 401 + WWW-Authenticate                   │
     │                      │ ◄────────────────────────────────────────────│
     │                      │                         │                    │
     │                      │  3. Discover AS metadata                     │
     │                      │ ───────────────────────►│                    │
     │                      │ ◄───────────────────────│                    │
     │                      │   (authorization_endpoint, token_endpoint)   │
     │                      │                         │                    │
     │                      │  4. Build auth URL      │                    │
     │                      │   (with PKCE challenge) │                    │
     │                      │                         │                    │
     │  5. Open browser     │                         │                    │
     │◄─────────────────────│                         │                    │
     │                      │                         │                    │
     │  6. Authorize        │                         │                    │
     │───────────────────────────────────────────────►│                    │
     │                      │                         │                    │
     │  7. Redirect to      │                         │                    │
     │     local callback   │                         │                    │
     │◄─────────────────────│                         │                    │
     │                      │                         │                    │
     │  8. Auth code        │                         │                    │
     │──────────────────────►                         │                    │
     │                      │                         │                    │
     │                      │  9. Exchange code       │                    │
     │                      │ ───────────────────────►│                    │
     │                      │ ◄───────────────────────│                    │
     │                      │   (access_token + refresh_token)             │
     │                      │                         │                    │
     │                      │ 10. Reconnect with token                     │
     │                      │ ────────────────────────│───────────────────►│
     │                      │                         │                    │
```

## Step-by-Step (GitHub Copilot Example)

### 1. Discovery

When AlayaCore connects to a server that requires OAuth, it first
discovers the authorization server metadata. For GitHub Copilot
(`https://api.githubcopilot.com/mcp`), the discovery chain is:

```
Step 2: Direct well-known at server URL
  GET https://api.githubcopilot.com/mcp/.well-known/oauth-authorization-server    → 404
  GET https://api.githubcopilot.com/mcp/.well-known/openid-configuration          → 404

Step 3: Protected Resource Discovery
  GET https://api.githubcopilot.com/mcp/.well-known/oauth-protected-resource      → 404
  POST https://api.githubcopilot.com/mcp  (unauthenticated)
       → 401 WWW-Authenticate: Bearer resource_metadata="https://api.githubcopilot.com/.well-known/oauth-protected-resource/mcp"
  GET https://api.githubcopilot.com/.well-known/oauth-protected-resource/mcp
       → 200 OK
       → authorization_servers: ["https://github.com"]

Step 4: Discover AS metadata on each authorization server
  GET https://github.com/.well-known/oauth-authorization-server                   → 404
  GET https://github.com/.well-known/openid-configuration                         → 200 OK
       → issuer: "https://github.com"
       → authorization_endpoint: "https://github.com/login/oauth/authorize"
       → token_endpoint: "https://github.com/login/oauth/access_token"
```

The full discovery chain implemented in `discoverASMetadata()` is:

1. **Configured token endpoint shortcut** — If `token_endpoint` is
   set in the server config, derive the issuer URL from it
   (e.g. `https://auth.example.com/token` → `https://auth.example.com`)
   and try `DiscoverASMetadata` on it directly.

2. **Direct well-known at server URL** — Probe
   `{serverUrl}/.well-known/oauth-authorization-server` and
   `{serverUrl}/.well-known/openid-configuration` (in that order).

3. **Protected Resource Discovery** — Call
   `DiscoverProtectedResource(serverURL)` which:
   a. Probes `{serverUrl}/.well-known/oauth-protected-resource` (root
      level) and `{baseUrl}/.well-known/oauth-protected-resource{path}`
      (path level, e.g. `/mcp`).
   b. Falls back to sending an unauthenticated POST request to the
      MCP server, which returns `401` with a `WWW-Authenticate:
      Bearer resource_metadata="<url>"` header. The resource metadata
      document is then fetched from that URL.
   c. Returns the `authorization_servers` list from the resource metadata.

4. **Authority discovery** — For each discovered authorization server
   URL, run `DiscoverASMetadata` (step 2) on it. Return the first
   successful result.

Each `DiscoverASMetadata` call probes two well-known paths in order:
`{issuer}/.well-known/oauth-authorization-server` (RFC 8414) then
`{issuer}/.well-known/openid-configuration` (OIDC fallback).

### 2. Authorization

AlayaCore builds the authorization URL with PKCE parameters and opens
it in the user's default browser:

```
https://github.com/login/oauth/authorize
  ?response_type=code
  &client_id=<your-client-id>
  &code_challenge=<SHA256 hash of code_verifier>
  &code_challenge_method=S256
  &redirect_uri=http://127.0.0.1:PORT/callback
  &state=<random CSRF token>
```

At the same time, AlayaCore starts a local HTTP server on a random
port to receive the callback (`http://127.0.0.1:PORT/callback`).

### 3. User Consent

The user logs into GitHub and grants consent in their browser.
GitHub redirects back to the local callback server:

```
HTTP 302 → http://127.0.0.1:PORT/callback
  ?code=<authorization_code>
  &state=<random CSRF token>
```

The callback server validates the `state` parameter (CSRF protection)
and extracts the authorization code.

### 4. Token Exchange

AlayaCore exchanges the authorization code for tokens:

```
POST https://github.com/login/oauth/access_token
Authorization: Basic <base64(client_id:client_secret)>
Content-Type: application/x-www-form-urlencoded
Accept: application/json

grant_type=authorization_code
&code=<authorization_code>
&redirect_uri=http://127.0.0.1:PORT/callback
&code_verifier=<original code_verifier>
&client_id=<your-client-id>

→ 200 OK
→ {
    "access_token": "ghu_...",
    "token_type": "Bearer",
    "expires_in": 28800,
    "refresh_token": "ghr_...",
    "scope": ""
  }
```

### 5. Connect with Token

AlayaCore saves the token to disk (`~/.alayacore/mcp-cache/{server}.conf`)
and reconnects to the MCP server with the access token in the
`Authorization` header:

```
Authorization: Bearer ghu_...
```

The server accepts the connection and AlayaCore proceeds to
discover tools.

## Token Refresh

When an access token expires, AlayaCore automatically refreshes it
using the stored refresh token:

```
POST https://github.com/login/oauth/access_token
Authorization: Basic <base64(client_id:client_secret)>
Content-Type: application/x-www-form-urlencoded

grant_type=refresh_token
&refresh_token=<stored refresh_token>
&client_id=<your-client-id>

→ 200 OK
→ {
    "access_token": "ghu_...",
    "token_type": "Bearer",
    "expires_in": 28800,
    "refresh_token": "ghr_..."
  }
```

If the refresh token is also expired or revoked, the entire flow
restarts from the authorization step.

## Token Storage

Tokens are persisted to disk at `~/.alayacore/mcp-cache/{server}.conf`:

```ini
access_token: "ghu_..."
token_type: "Bearer"
refresh_token: "ghr_..."
expires_at: 1744112345
scopes: ["repo", "gist"]
token_endpoint: "https://github.com/login/oauth/access_token"
client_id: "<your-client-id>"
client_auth_method: "client_secret_basic"
```

The format uses the same key-value syntax as `mcp.conf`, with JSON-encoded
values for arrays and maps.

On restart, tokens are loaded from disk. If the stored token is
expired, it is refreshed automatically without user interaction.

## The `:mcp_confirm` Command

In the **Terminal (TUI)** adapter, when a server requires OAuth
authorization, a confirmation dialog is displayed. The user can:

- **Accept**: Press Enter — opens the browser to the authorization URL
- **Decline**: Press Esc — sends `:mcp_decline <server>` to skip the server
- **Manual**: Type `:mcp_confirm <server> <code> <redirect_uri>` to
  provide an authorization code obtained out-of-band

In the **PlainIO** adapter, when a server requires OAuth authorization,
the adapter prints the authorization URL to stdout, starts a local
callback server on `127.0.0.1` (replacing the `{{redirect_uri}}` and
`{{state}}` placeholders), and tries to open the browser automatically.
Once the code arrives, the adapter sends `:mcp_confirm` itself — no typing
needed, which is what makes the flow work with a piped stdin, where there
is nothing to type into at all. The output shows the filled-in URL:

```
[mcp: server "github" requires authorization]
[mcp: opening browser for "github"…]
[mcp: if the browser doesn't open, visit:]
https://github.com/login/oauth/authorize?redirect_uri=http%3A%2F%2F127.0.0.1%3A45231%2Fcallback&state=...
```

The manual fallback commands are printed where they are needed — when the
browser could not be opened, and when the callback wait times out after 5
minutes. The timeout is also what a browser opened on **another machine**
produces: its redirect reaches that machine's `127.0.0.1`, so the callback
here never fires.

```
[mcp: authorization for "github" timed out]
[mcp: to finish by hand (code = the ?code= value in the redirect URL), type:]
:mcp_confirm github <code> http://127.0.0.1:45231/callback
[mcp: to skip this server: :mcp_decline github]
```

To finish by hand: open the printed URL, read the authorization code off
the redirect (the browser shows a connection error — the code is in the
address bar), and type that command with `<code>` replaced. The redirect
URI is spelled out rather than left as a placeholder because the adapter
knows the value it substituted into the URL, which leaves `<code>` as the
only thing to fill in; the command line carries no `[mcp: …]` prefix for
the same reason the URL is printed bare — it is meant to be selected and
typed.

`:mcp_decline <server>` skips just that server (the rest of MCP init and
any other server's authorization stay intact); `:mcp_cancel` cancels the
whole init.

## External URLs Summary

| Step | Method | URL | Description |
|------|--------|-----|-------------|
| AS Discovery (probe) | GET | `{issuer}/.well-known/oauth-authorization-server` | RFC 8414 metadata |
| AS Discovery (fallback) | GET | `{issuer}/.well-known/openid-configuration` | OIDC fallback |
| Protected Resource (well-known) | GET | `{server}/.well-known/oauth-protected-resource` | RFC 9728 metadata |
| Protected Resource (401 fallback) | POST | `{mcp-server}` | Triggers `WWW-Authenticate` header, then fetches the metadata URL from it |
| Authorization | GET | `{authorization_endpoint}` | User grants consent in browser |
| Token Exchange | POST | `{token_endpoint}` | Swap code for tokens |
| Token Refresh | POST | `{token_endpoint}` | Renew expired token |

Only two of these are user-facing interactive services:

- **Authorization endpoint** — the user authorizes in their browser
- **Token endpoint** — AlayaCore exchanges and refreshes tokens server-to-server

## Security Notes

- **PKCE** (Proof Key for Code Exchange) is always used for
  `authorization_code` flow, preventing authorization code
  interception attacks
- **State parameter** provides CSRF protection for the callback
- **Your own credentials** — you register and control your own OAuth
  app credentials, reducing the risk of shared credential revocation
  or rate-limiting
- **Tokens are stored on disk** with the same permissions as other
  config files (`~/.alayacore/mcp-cache/`)
