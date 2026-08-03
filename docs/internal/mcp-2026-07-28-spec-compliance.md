# MCP 2026-07-28 Specification Compliance

> Status: tracking document — last updated 2026-08
> Applies to: `internal/mcp` (client-side 2026-07-28 protocol support)

AlayaCore implemented the MCP `2026-07-28` protocol **before its release**
(implementation landed 2026-07-14 against the draft; the spec was published
2026-07-28). This document records the differences between our 0728
implementation and the released specification.

## Summary

The draft → release transition introduced **no breaking changes** for our
implementation. The only post-release schema change (`271ecc9a` in
modelcontextprotocol) touched `subscriptions/listen` envelopes, which we do
not implement. All MUST-level compliance gaps found after release have been
fixed; what remains is a list of deliberate feature-scope decisions, all
declared in the adapter doc comment.

## Release-verified: draft vs 2026-07-28 schema

`diff schema/draft schema/2026-07-28` shows only doc-link differences.
The sole released-schema fix (`SubscriptionsListenResultMeta` rename +
`SubscriptionsListenResultResponse` envelope) affects only
`subscriptions/listen` — not implemented by AlayaCore, so no impact.

## Fixed MUST-level gaps (2026-08)

| # | Gap | Spec requirement | Fix |
|---|-----|------------------|-----|
| 1 | OAuth `resource` parameter (RFC 8707) missing from authorization / token / refresh requests | MUST be included in both authorization and token requests; MUST use the canonical MCP server URL | `4383487`, `651848a` — `AuthCodeConfig.Resource` / `RefreshConfig.Resource`, sent only for proto-version `2026-07-28` |
| 2 | OAuth `iss` parameter (RFC 9207) not validated before redeeming the authorization code | MUST validate a present `iss` against the recorded issuer (exact match, no RFC 3986 normalization); reject absent `iss` when the AS advertises `authorization_response_iss_parameter_supported` | `b069468` — `auth.ValidateIssParam`, callback captures `iss`, threaded through `:mcp_confirm` |
| 3 | `x-mcp-header` constraint violations silently skipped instead of rejecting the tool | MUST reject tool definitions with violating annotations (exclude from `tools/list`, log warning): RFC 9110 token header names, case-insensitive uniqueness, primitive types only (no `number`), statically-reachable placement, integer safe range ±2⁵³ | `08a919c` — `validateXMcpHeaderAnnotations` + `ListTools` filtering (2026-07-28 + HTTP only) |
| 4 | `Mcp-Name` header not Base64 sentinel-encoded for unsafe values | MUST encode values that are not safe plain-ASCII header values as `=?base64?...?=` (also re-encode values matching the sentinel pattern) | `296cec7` — `EnrichRequest` routes `Mcp-Name` through `encodeHeaderValue` |
| 5 | Leftovers from pre-release drafts | Released `Tool` has no `execution`/`taskSupport` (tasks → extension); `DiscoverResult` has no top-level `serverInfo` (server identity in `_meta.io.modelcontextprotocol/serverInfo`); `ping` removed from 2026-07-28; `resources` capability has `subscribe` + `listChanged` | `310e4dc` — removed `ToolExecution`, parse `_meta` serverInfo in handshake, dropped unused `Client.Ping()`, added `ServerResourceCapabilities.Subscribe` |

## Deliberate feature-scope decisions (declared in `adapter_v20260728.go`)

These are optional (or SHOULD-level) spec features that AlayaCore does not
implement. They do not violate MUST requirements.

| Feature | Spec status | Notes |
|---------|-------------|-------|
| MRTR (Multi Round-Trip Requests, `resultType: "input_required"`) | Active | Explicitly rejected with an error; AlayaCore has no multi-round-trip interaction loop |
| `subscriptions/listen` (long-lived change-notification streams) | Active | Not implemented; server-to-client notifications (`listChanged`) are not received — `notifications/subscriptions/acknowledged` and `notifications/resources/updated` also out of scope |
| Response caching (`ttlMs` / `cacheScope` on list/read results) | Active | Fields parsed but ignored; list results are not cached |
| `extensions` in `ClientCapabilities` / `ServerCapabilities` | Active | Not declared; optional MCP extensions beyond core protocol unsupported |
| OpenTelemetry trace context (`traceparent`, `tracestate`, `baggage` in `_meta`) | Documented convention | Not propagated |
| New error codes `-32020` / `-32021` / `-32022` | Active (reserved range) | Not produced by AlayaCore (client-side); received codes surface as generic `RPCError` |
| Tasks (`io.modelcontextprotocol/tasks` extension) | Extension | Not implemented (moved out of core by SEP-2663) |
| Elicitation | Active (under MRTR) | Not implemented |
| Progress notifications | Active | `progressToken` never set; incoming progress notifications discarded |
| Roots / Sampling / Logging | Deprecated (SEP-2577) | Intentionally not implemented |
| stdio process restart on unexpected termination | SHOULD | Client transitions to `StateFailed` instead; caller creates a new Client |
| `resources/templates/list` | Active (client MAY) | Not implemented |
| `completion/complete` | Active (client MAY) | Not implemented |
| Scope selection strategy (`WWW-Authenticate` scope → `scopes_supported` priority) | SHOULD | Uses configured scopes only |

## Version-boundary guarantees

Shared code paths (OAuth, callback chain, `ListTools`) are shared across all
supported protocol versions (`2024-11-05` … `2026-07-28`). Version-specific
behavior is gated as follows so legacy servers are never affected:

- RFC 8707 `resource`: sent only when `proto-version=2026-07-28`
  (`init.go`, `client.go`); empty for legacy → parameter omitted entirely
- RFC 9207 `iss`: validated only when present in the callback; legacy
  authorization servers do not send `iss`, so validation is skipped
- `x-mcp-header` tool rejection: enabled only for 2026-07-28 over the HTTP
  transport; other versions/transports keep lenient behavior (the spec
  allows ignoring the annotation entirely)
- `Mcp-Name` / `Mcp-Param-*` headers: 2026-07-28 HTTP only (legacy adapters
  have no-op `SetToolHeaderMappings`)
- `ping`: kept in the stdio transport and legacy adapters (still valid in
  `2024-11-05` … `2025-11-25`); removed only from the 2026-07-28 client path

## Known observations (accepted, no code change)

Reviewed after the fixes above; all three are intentional non-issues
documented for future maintainers:

1. **RFC 9207 `iss` not validated on callback error responses.** The
   `error`/`error_description` callback path does not parse `iss`. RFC 9207
   also covers error responses, but AlayaCore never redeems a code on that
   path — the error is only displayed — so the attack surface is limited to
   cosmetic error text. If error handling is ever extended, `iss` should be
   validated there too (`auth.ValidateIssParam`).
2. **`collectAllXMcpHeaderAnnotations` recurses into data containers.**
   The whole-schema walk also descends into data keywords (`enum`, `const`,
   `default`, …). In the pathological case where a data value contains an
   `x-mcp-header` key, the tool would be conservatively rejected. This is
   extremely unlikely and the direction is safe (reject, never allow);
   a future cleanup could skip known data keywords.
3. **`ListTools` parses the input schema twice.** `validateXMcpHeaderAnnotations`
   re-runs `parseHeaderMappings` for the reachability count. `ListTools` is
   called once per server at init (no refresh path), so the cost is
   negligible; the duplicate call could be eliminated by passing the parsed
   mappings in, but it is not worth the signature churn.

## Verification

Each change was verified with `go build ./...`, `go test ./...`,
`make lint`, and `gofmt`. Tests covering the version boundaries:

- `internal/mcp/auth/authcode_test.go` — RFC 9207 validation rules
- `internal/mcp/auth/token_refresh_test.go` — resource param present/absent
- `internal/platform/oauth_test.go` — callback `iss` capture + state check
- `internal/mcp/header_mappings_test.go` — x-mcp-header constraint cases
- `internal/mcp/xmcpheader_reject_test.go` — ListTools rejection (0728)
  vs retention (2025-11-25)
- `internal/mcp/adapter_v20260728_test.go` — Mcp-Name encoding + headers
