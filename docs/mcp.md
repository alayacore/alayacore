# MCP (Model Context Protocol)

AlayaCore can connect to external **MCP servers** to extend the agent's
toolset. MCP (Model Context Protocol) is an open standard that defines
how LLMs discover and invoke tools from external services — databases,
APIs, web scraping, code analysis, and more.

When MCP servers are configured, their tools and resources are automatically
discovered and merged with AlayaCore's built-in tools (`read_file`,
`write_file`, `execute_command`, etc.). The agent can use all of them
transparently in the same tool-calling loop.

## Configuration

MCP servers are configured via the `mcp.conf` file in your config directory
(`~/.alayacore/` by default). This is the only configuration method.

```
~/.alayacore/
├── model.conf
├── runtime.conf
├── mcp.conf         # MCP server definitions
└── themes/
```

### Format

One block per server, separated by `---`:

```
server: my-db
command: npx
args: ["@anthropic/mcp-db-server"]
proto-version: 2025-11-25
---
server: my-git
command: uvx
args: ["mcp-git"]
proto-version: 2025-11-25
---
server: remote-api
url: https://example.com/mcp
proto-version: 2026-07-28
---
server: my-service
url: https://example.com/mcp
proto-version: 2025-11-25
auth-type: static
auth-token: <your-token>
---
server: github
url: https://api.githubcopilot.com/mcp/
proto-version: 2026-07-28
auth-type: authorization_code
auth-client-id: <your-client-id>
auth-client-secret: <your-client-secret>
```

### Fields

| Field | Required | Description |
|-------|----------|-------------|
| `server` | Yes | Server name (used for tool naming: `{server}_tool`) |
| `url` | One of url/command | MCP server HTTP endpoint (Streamable HTTP transport) |
| `command` | One of url/command | Executable command for stdio transport |
| `args` | No | JSON array of command-line arguments |
| `env` | No | JSON object of environment variables (`{"KEY": "val"}`) |
| `proto-version` | Yes | MCP protocol version (see [Protocol Support](#protocol-support)) |
| `auth-type` | No | OAuth type: `authorization_code` or `static` |
| `auth-scopes` | No | JSON array of OAuth scopes to request (e.g. `["repo", "gist"]`) |
| `auth-client-id` | No* | OAuth client ID (required for `authorization_code`) |
| `auth-client-secret` | No | OAuth client secret (required by some services for `authorization_code`) |
| `auth-token` | No | Pre-obtained access token (for `static` auth only) |

> **Note:** For `authorization_code` auth, you **must** register an OAuth app
> with the service and provide `auth-client-id` (and `auth-client-secret` if
> required) in your `mcp.conf`. AlayaCore does not ship with built-in
> credentials.
>
> See [OAuth Flow](oauth.md) for the full authorization flow.
> For `static` auth, only `auth-token` is used (a pre-obtained API key or token).

### Validation

Server configurations are validated at load time. A server block is **rejected** if:

- `server` name is empty
- `server` name duplicates another block — the first occurrence is kept, subsequent duplicates are skipped
- `proto-version` is missing or unsupported — see [Protocol Support](#protocol-support) for valid versions

Rejected servers are skipped and an error is reported to the adapter. Other valid servers in the same file are unaffected.

Comments (`#`) are **line-level**: a comment line may appear anywhere inside a block — including the first line — and the remaining lines of the block are still parsed. A block containing only comments or blank lines is skipped entirely.

### Quick Start

Create `~/.alayacore/mcp.conf` with your server definitions:

```bash
# stdio transport
cat >> ~/.alayacore/mcp.conf << 'EOF'
server: my-db
command: npx
args: ["@anthropic/mcp-db-server"]
proto-version: 2025-11-25
---
server: my-git
command: uvx
args: ["mcp-git"]
proto-version: 2025-11-25
EOF

# Streamable HTTP transport
cat >> ~/.alayacore/mcp.conf << 'EOF'
server: remote-api
url: https://example.com/mcp
proto-version: 2026-07-28
EOF
```

Then run `alayacore` normally. The servers are automatically connected
and their tools are discovered at startup.

## CLI Flags

| Flag | Description |
|------|-------------|
| `--debug-log` | Debug log directory (`. = CWD, or any path). Enables both JSON-RPC and API debug logging. |

## Tool Naming

MCP tools are prefixed with the server name to avoid conflicts with
built-in tools and between servers:

```
<server>_<tool>
```

For example, with a server named `db` that exposes a `query` tool:

| Original tool name | Prefixed name | Description |
|--------------------|---------------|-------------|
| `query` | `db_query` | Run SQL query |
| `list_tables` | `db_list_tables` | List database tables |

The prefixed name is set as the tool's `name` in the API `tools` parameter,
so the LLM always uses it when making tool calls.

## Injected Tools

In addition to the tools exposed by the server's `tools/list`, AlayaCore
injects the following utility tools for each server that supports the
corresponding capability:

| Tool | Capability | Parameters | Description |
|------|-----------|------------|-------------|
| `{server}_read_resource` | Resources | `uri` (required) | Read a resource by URI |
| `{server}_get_prompt` | Prompts | `name` (required), `arguments` (optional) | Get a prompt template with arguments |

These tools allow the LLM to access server resources and prompts directly
within the conversation.

## How It Works

```
Agent Loop
  │
  ├── Built-in tools (read_file, write_file, ...)
  │
  └── MCP tools (db_query, git_status, db_read_resource, db_get_prompt, ...)
        │
        ▼
    Manager
      ├── Client "db"  ─── StdioTransport ─── npx @anthropic/mcp-db-server
      ├── Client "git" ─── StdioTransport ─── uvx mcp-git
      └── Client "mcp" ─── StreamableHTTP  ─── https://example.com/mcp
```

1. **Startup**: AlayaCore reads `mcp.conf`, creates the appropriate
   transport for each server (stdio or Streamable HTTP), performs the
   MCP handshake (`initialize` for 2025-11-25 or `server/discover` for
   2026-07-28), and calls `tools/list` to discover available tools.

2. **Tool registration**: Each discovered tool is wrapped as an
   `llm.Tool` with the prefixed name and wired to route calls back
   to the originating server. For servers that support Resources or
   Prompts, `read_resource` and `get_prompt` tools are injected.

3. **Tool execution**: When the agent calls a tool, the request is
   sent as a JSON-RPC `tools/call` message to the corresponding
   server. The response is converted back into AlayaCore's content
   format and returned to the agent.

4. **Cancellation**: If the user cancels a task while waiting for an
   MCP tool response, the pending request is unregistered and a
   `notifications/cancelled` notification is sent to the server
   (best-effort) so it can abort processing early. Whether or not
   the server receives the notification, any late response from the
   server is discarded.

5. **Server crash**: If an MCP server process exits unexpectedly, a
   monitor goroutine detects the death via the transport's Done channel,
   transitions the client to a failed state, and signals via the client's
   Done channel. Subsequent tool calls to that server return a clear
   error: `"name": server connection lost`. The agent
   handles this like any other tool failure.

6. **Stale detection**: If a server notifies that its tool list has
   changed (`notifications/tools/list_changed`), the client is marked
   stale and subsequent tool calls return a message asking the user
   to restart AlayaCore.

## Debugging

Use `--debug-log` to log all JSON-RPC messages:

```bash
alayacore --debug-log=.         # write to current directory
alayacore --debug-log=/tmp      # write to /tmp
```

This creates `alayacore-debug-mcp-N.log` (N = 0, 1, 2, ...) in the current
directory. Each run creates a new file so historical logs are preserved:

```
MCP debug log started for: npx @anthropic/mcp-db-server
>>> initialize {"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"alayacore","version":"0.1.0"}}
<<< {"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"mcp-db","version":"1.0.0"}}}
>>> notifications/initialized
>>> tools/list
<<< {"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"query","description":"Run SQL query","inputSchema":{"type":"object"}}]}}
>>> tools/call {"name":"query","arguments":{"sql":"SELECT * FROM users"}}
<<< {"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"..."}]}}
```

`>>>` marks requests sent to the server, `<<<` marks responses.
Notifications (like `notifications/initialized`) have no response.
JSON is pretty-printed for readability.

## Error Handling

- **Connection failures**: If an MCP server cannot be started or
  initialized, an error message is displayed via the adapter and the
  server is skipped. Other servers are unaffected.

- **Tool call failures**: If a tool call returns an error (JSON-RPC
  error or `isError: true`), the error message is included in the
  tool result and returned to the agent like any other tool error.

- **Server crashes**: If an MCP server process exits unexpectedly,
  subsequent tool calls to that server fail with a connection error.
  The agent handles this like any other tool failure.

- **Stale server**: If the server's tool list changes at runtime
  (`notifications/tools/list_changed`), the server is marked stale
  and tool calls return: `"name": server tool list changed,
  restart required`.

## Protocol Support

AlayaCore implements the MCP **client** side of the protocol (it acts as
an MCP Host). The protocol version is selected per-server via the
`proto-version` config field.

### 2024-11-05 ⚠️

| Feature | Notes |
|---------|-------|
| `initialize` / `initialized` | Version negotiation, capability exchange |
| `tools/list` | With cursor-based pagination |
| `tools/call` | With content type conversion (text, image, audio, resource, resource_link) |
| `resources/list` / `resources/read` | Pre-fetched at startup and injected into system prompt; exposed as `{server}_read_resource` tool |
| `prompts/list` / `prompts/get` | Pre-fetched at startup and injected into system prompt; exposed as `{server}_get_prompt` tool |
| `ping` | Both client → server and server → client |
| `notifications/cancelled` | Best-effort on context cancellation |
| `notifications/tools/list_changed` | Marks server stale, requires restart |
| Stdio transport | NDJSON, graceful shutdown (stdin→SIGTERM→SIGKILL) |

> ⚠️ **HTTP not supported.** 2024-11-05 uses the legacy HTTP+SSE transport
> pattern (client opens SSE connection first, server sends `endpoint` event,
> responses arrive on the SSE stream). AlayaCore only implements Streamable
> HTTP, so this version is restricted to stdio transport.

### 2025-03-26 ✅

| Feature | Notes |
|---------|-------|
| `initialize` / `initialized` | Version negotiation, capability exchange |
| `tools/list` | With cursor-based pagination |
| `tools/call` | With content type conversion (text, image, audio, resource, resource_link) |
| `resources/list` / `resources/read` | Pre-fetched at startup and injected into system prompt; exposed as `{server}_read_resource` tool |
| `prompts/list` / `prompts/get` | Pre-fetched at startup and injected into system prompt; exposed as `{server}_get_prompt` tool |
| `ping` | Both client → server and server → client |
| `notifications/cancelled` | Best-effort on context cancellation |
| `notifications/tools/list_changed` | Marks server stale, requires restart |
| Stdio transport | NDJSON, graceful shutdown (stdin→SIGTERM→SIGKILL) |
| Streamable HTTP transport | JSON + SSE responses, GET stream, `Mcp-Session-Id`, `MCP-Protocol-Version` header |

### 2025-06-18 ✅

| Feature | Notes |
|---------|-------|
| `initialize` / `initialized` | Version negotiation, capability exchange |
| `tools/list` | With cursor-based pagination |
| `tools/call` | With content type conversion (text, image, audio, structured content, resource, resource_link) |
| `resources/list` / `resources/read` | Pre-fetched at startup and injected into system prompt; exposed as `{server}_read_resource` tool |
| `prompts/list` / `prompts/get` | Pre-fetched at startup and injected into system prompt; exposed as `{server}_get_prompt` tool |
| `ping` | Both client → server and server → client |
| `notifications/cancelled` | Best-effort on context cancellation |
| `notifications/tools/list_changed` | Marks server stale, requires restart |
| Stdio transport | NDJSON, graceful shutdown (stdin→SIGTERM→SIGKILL) |
| Streamable HTTP transport | JSON + SSE responses, GET stream, `Mcp-Session-Id`, `MCP-Protocol-Version` header (required by spec) |

### 2025-11-25 ✅

| Feature | Notes |
|---------|-------|
| `initialize` / `initialized` | Version negotiation, capability exchange |
| `tools/list` | With cursor-based pagination |
| `tools/call` | With content type conversion (text, image, audio, resource, resource_link) |
| `resources/list` / `resources/read` | Pre-fetched at startup and injected into system prompt; exposed as `{server}_read_resource` tool |
| `prompts/list` / `prompts/get` | Pre-fetched at startup and injected into system prompt; exposed as `{server}_get_prompt` tool |
| `ping` | Both client → server and server → client |
| `notifications/cancelled` | Best-effort on context cancellation |
| `notifications/tools/list_changed` | Marks server stale, requires restart |
| Stdio transport | NDJSON, graceful shutdown (stdin→SIGTERM→SIGKILL) |
| Streamable HTTP transport | JSON + SSE responses, GET stream, `Mcp-Session-Id`, `MCP-Protocol-Version` header |
| Cursor pagination | All list methods |

### 2026-07-28 ✅

| Feature | Notes |
|---------|-------|
| `server/discover` | Version discovery and capability advertisement |
| `tools/list` | With cursor-based pagination |
| `tools/call` | With content type conversion |
| `resources/list` / `resources/read` | Same as 2025-11-25 |
| `prompts/list` / `prompts/get` | Same as 2025-11-25 |
| `_meta` per-request | Carries `protocolVersion`, `clientInfo`, `clientCapabilities` |
| `Mcp-Method`, `Mcp-Name`, `Mcp-Param-{Name}` HTTP headers | Required request metadata for intermediaries |
| `x-mcp-header` annotation support | Mirror tool parameters to HTTP headers |
| Streamable HTTP transport (POST only) | No session, no GET stream, no `Mcp-Session-Id` |

### ❌ Not Implemented

| Feature | Spec Section | Reason |
|---------|-------------|--------|
| `progressToken` | Progress | Progress notifications; optional, we don't initiate long-running ops |
| `resources/subscribe` / `resources/unsubscribe` | Resources | Resource change subscription; not needed for agent use case |
| `notifications/resources/list_changed` | Resources | Resource lists are fetched at startup and injected into the system prompt; dynamic updates not required |
| `notifications/resources/updated` | Resources | Requires subscribe; not implemented |
| `notifications/prompts/list_changed` | Prompts | Prompt lists are fetched at startup and injected into the system prompt; dynamic updates not required |
| `notifications/progress` | Progress | Requires `progressToken` in `_meta` |
| `notifications/message` (logging) | Logging | Server log messages; optional |
| `logging/setLevel` | Server → Logging | Server log level control |
| `completion/complete` | Server → Completions | Argument autocomplete suggestions |
| `roots/list` / `roots/list_changed` | Client → Roots | Not needed; agent doesn't expose file system roots |
| `sampling/createMessage` | Client → Sampling | Server-initiated LLM sampling; not needed |
| Client `elicitation` capability | Client → Elicitation | Server requests user info; not needed (agent controls the loop) |
| Client `tasks` capability | Client → Tasks | Task-augmented execution (polling pattern) |
| Server `tasks` capability | Server → Tasks | Task-augmented execution (polling pattern) |
| `subscriptions/listen` | Server → Subscriptions | Long-lived notification channel (2026-07-28+ only) |

### Schema Coverage

Our Go types match the `2025-11-25` JSON Schema for all implemented
features. Known omissions (all optional):

| Field | Types |
|-------|-------|
| `_meta map[string]any` | Tool, CallToolResult, Resource, Prompt, InitializeRequest, InitializeResult, ListToolsResult, ListResourcesResult, ListPromptsResult, ReadResourceResult, GetPromptResult, TextContent, ImageContent, AudioContent, ResourceLink, EmbeddedResource |
| `annotations` on content blocks | TextContent, ImageContent, AudioContent, ResourceLink, EmbeddedResource |

These are silently ignored by Go's JSON decoder and have no functional
impact.

## Technical Notes

- **Request/response matching**: Each JSON-RPC request gets a unique
  ID. A dedicated background goroutine (`readLoop`) reads all response
  lines from the server and dispatches them to the waiting caller by ID.
  This eliminates the risk of response desynchronization on cancellation.

- **No process killing on cancel**: If the user cancels a task during
  an MCP tool call, the tool is simply abandoned — the server process
  continues running and its response is discarded. This preserves
  server state and avoids reconnection overhead.

- **Concurrent tool execution**: Multiple MCP tools from different
  servers can execute concurrently, matching the same concurrency model
  as built-in tools.
