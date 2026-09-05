# Provider Speed Tracking

How AlayaCore measures and displays provider speed (tokens/sec) and
time-to-first-token (TTFT).

## Overview

The session measures each LLM round trip (step) and shows the latest
step's speed in the TUI status bar, right after the context segment:

```
∙ R0 | 1.5K | 12.5 tok/s (ttft 1.2s) | 1/5
```

The leading `∙` is the status dot — accent-colored and bold while a task
runs, dim otherwise. TTFT is parenthesised rather than set off by a middle
dot because the status row is truncated to exactly the terminal width, and
a middle dot (U+00B7) is one of the East-Asian Ambiguous characters a CJK
terminal can draw two cells wide. Both marks are drawn by
`renderStatusBar`; what they may be is pinned by the glyph policy in
`internal/adapters/terminal/constants.go`.

Two metrics per step, both computed in `internal/llm` (provider-agnostic,
single implementation for Anthropic and OpenAI):

| Metric | Formula | Meaning |
|--------|---------|---------|
| `TimeToFirstToken` | first delta arrival − step start | Latency: request sent → first token received (network + prefill + first chunk). Unaffected by chunk batching. |
| `TokensPerSec` | `OutputTokens / Duration` | End-to-end throughput: total output tokens over the whole round trip (latency included). |

`Duration` is measured from before `Provider.StreamMessages` (so
request/network latency is included) to `StepCompleteEvent` (provider
stream end). Tool execution happens after the stream ends, so tool time
is excluded.

## Why end-to-end, not "decode speed"

`TokensPerSec` is deliberately the simple end-to-end throughput — NOT the
server-side decode speed (e.g. llama.cpp's `eval time`). The client
cannot observe the server's decode rate exactly: streaming servers batch
output into chunks, and the first chunk may hide several tokens'
generation time inside TTFT. Any client-side estimate that subtracts TTFT
is systematically inflated for short or burst outputs (a 100-token burst
delivered over 70ms reports ~1400 tok/s against a real ~240 tok/s — a
~6× overstatement). The end-to-end formula:

- Is **always computable** for any completed step with output tokens —
  the display never blanks out (no reliability gates);
- Is **stable and reproducible** — no heuristic thresholds to maintain;
- **Understates** the server's decode speed (latency included), so
  compare with llama.cpp logs knowing this value is lower, not higher.

The trade-off is intentional: simple and stable beats precise-but-flaky.
For exact server-side numbers, read the server's own logs/timings (e.g.
Ollama's native `eval_count`/`eval_duration`), which the OpenAI/Anthropic
wire protocols do not expose.

## Why short steps (tool calls) show low values

Tool-call steps report noticeably lower `step_tps` than long text
answers. This is a mathematical property of the end-to-end formula, not a
measurement bug:

```
step_tps = N / (TTFT + N/v)
```

where N is the step's output tokens, v the server's decode speed, and
TTFT the first-token latency. As N grows, `step_tps` approaches v (long
answers match the decode speed); as N shrinks, TTFT dominates and
`step_tps` approaches N/TTFT — which is tiny for a 20–50 token tool call
behind a multi-second prefill. The value is still *real*: the request did
take that long to deliver those few tokens; the time went into latency
(prefill), not generation.

This is the other side of the same coin as the "why end-to-end" section:
tool calls are the worst case for both client-observed formulas. They
have the fewest output tokens, so end-to-end understates them; and their
arguments are generated in a burst, so subtracting TTFT would *inflate*
them (the ~6× overstatement above). Throughput is simply not a
meaningful metric for a 30-token step — TTFT is the number that matters
for tool calls, and it is always shown alongside `step_tps`.

## Data Flow

```
Agent.Stream (step start → time.Now)
  → streamEvents records first-delta time (TTFT) and Duration at StepCompleteEvent
    → completeStepStats: TokensPerSec = OutputTokens / Duration
      → OnStepStats(StepStats) callback
        → Session.handleStepStats → stepStatsEvent (taskEventCh)
          → run() stores lastStepTPS/lastTTFTMS
            → taskMsg broadcast (SM "task"): step_tps, ttft_ms
              → terminal sessionState (mutex-protected) → status bar
```

## Wire Protocol

`taskMsg` carries two additive, `omitempty` fields (absent until the
first step with output tokens completes; older adapters ignore them):

| Field | Type | Meaning |
|-------|------|---------|
| `step_tps` | float | Latest step's end-to-end tok/s. `0` for a step with no output tokens (e.g. tool-only step) — clears the display. |
| `ttft_ms` | int | Latest step's TTFT in ms. |

The values reflect the **latest step only** — no task-level averaging is
reported. Speed persists across task completion (final step's speed stays
visible) and is cleared when the next task starts (`stepStartEvent`
step 1) so no stale data is broadcast.

## Display Rules (TUI)

- Shown right after the context segment, before the steps segment.
- Visible whenever the latest step had output tokens, including after
  task completion (final step speed) until the next task starts.
- A step with no output tokens carries `step_tps: 0` and clears the
  segment (nothing to measure; TTFT still shown).

## See Also

- [context-tracking.md](context-tracking.md) — token usage tracking
- [step-messages.md](step-messages.md) — step lifecycle
- [adapter-guide/README.md](../adapter-guide/README.md) — SM "task" wire format
