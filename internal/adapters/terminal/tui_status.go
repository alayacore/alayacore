package terminal

// Status bar: session state display (steps, tokens, right group).
//
// Extracted from tui.go. Owns statusLeft/statusRight and inProgress state,
// and provides rendering helpers.

import (
	"fmt"
	"math"
	"strings"
)

// statusStepsSegment returns the steps status string, or "" if no activity.
// During a run it shows live progress ("3/5", "3/INF"); after completion it
// shows the last run's frozen summary ("3/5", "3/INF") until the next task
// starts. Nothing in this segment marks live vs frozen: the indicator that
// opens the bar does (the spinner turning while a task runs, the still braille
// cell (⠿) once it ends — see renderStatusBar). Tool windows carry their own
// state in the header instead ("TOOL CALL ⠋/✓/✗", see tool_render.go).
func statusStepsSegment(inProgress bool, currentStep int, maxSteps int, lastCurrentStep int, lastMaxSteps int) string {
	if inProgress && currentStep > 0 {
		if maxSteps > 0 {
			return fmt.Sprintf("%d/%d", currentStep, maxSteps)
		}
		return fmt.Sprintf("%d/INF", currentStep)
	}
	if lastCurrentStep > 0 {
		if lastMaxSteps > 0 {
			return fmt.Sprintf("%d/%d", lastCurrentStep, lastMaxSteps)
		}
		return fmt.Sprintf("%d/INF", lastCurrentStep)
	}
	return ""
}

// statusSpeedSegment renders the provider speed segment: the latest
// step's end-to-end tok/s (with TTFT when known). Shown whenever a step
// has completed with output tokens — during a run it reflects the latest
// completed step, and after the task ends it stays visible as the task's
// final step speed until the next task starts (whose stepStartEvent
// clears it). Returns "" when no step has produced output yet (e.g. a
// tool-only step with zero output tokens).
func statusSpeedSegment(stepTPS float64, ttftMS int64) string {
	if stepTPS <= 0 {
		return ""
	}
	if ttftMS > 0 {
		// TTFT qualifies the throughput figure rather than standing beside
		// it, so it goes in parentheses. This used to read
		// "12.5 tok/s · ttft 1.2s": a middle dot is East-Asian Ambiguous,
		// and the status row is truncated to exactly the terminal width, so
		// a terminal that draws Ambiguous glyphs two cells wide wrapped this
		// row's last cell onto a second row — the same failure the help bars
		// were fixed for (glyph policy, constants.go). The parentheses cost
		// the same 22 cells and say what the relationship actually is.
		return fmt.Sprintf("%.1f tok/s (ttft %.1fs)", stepTPS, float64(ttftMS)/1000)
	}
	return fmt.Sprintf("%.1f tok/s", stepTPS)
}

// statusRightSegment returns the right-aligned status group: the active
// model name with the reasoning level fused to it ("DeepSeek Flash | R2"),
// or the bare level ("R2") when no model is active — the level's column
// does not depend on whether a model happens to be set.
//
// The level rides at the tail deliberately. The group is truncated from
// its end when the line runs out of room (assembleStatusLeft), so a
// squeezed bar drops "R2" before the truncation eats into the model name:
// the model identifies the session, and the level is a setting the session
// file already records.
func statusRightSegment(model string, reasoningLevel int) string {
	if model == "" {
		return fmt.Sprintf("R%d", reasoningLevel)
	}
	return fmt.Sprintf("%s | R%d", model, reasoningLevel)
}

// renderStatusBar renders the status bar line.
// Status bar is dimmed when an overlay is active.
//
// Layout: an indicator column opens the row — the still braille cell (⠿)
// when idle, the shared spinner while a task runs (statusIdleGlyph,
// spinner.go) — then the live
// status segments (context, speed, steps, video). The right group — the
// active model name with the reasoning level fused to it ("DeepSeek Flash
// | R2"), or the bare level when no model is set — is placed by a single
// threshold (see assembleStatusLeft): with ample space it floats
// right-aligned after blank padding; when the gap would be ≤ 3 cells it
// merges into the left segments (joined by " | ", truncated together) —
// the bar never shows a squeezed right-aligned group or a 1-3 cell gap.
// The row may run right up to the terminal edge (the cap is the full
// width, not width-2 — see the hard cap below): the TUI's flush-to-edge
// design language.
//
// Color: the whole row is one foreground — muted normally, dim when an
// overlay blocks it. Segments, the " | " separators and the indicator all
// share it; the bar never mixes a second register in, and it carries no
// accent and no bold (the state is read from the spinner turning, not from
// a color).
//
// The result is truncated to at most the terminal width so a runaway
// status string — e.g. a session with a long token count + many steps
// + video config + a long model name — does not soft-wrap
// onto a second row in raw passthrough mode. Two status rows would push
// the input box's rendered content against the bottom rule and overlap
// the prompt area, even though the input box is now drawn with an
// absolute CUP (the status bar itself is anchored to the last row, so it
// would visibly wrap onto the second-to-last row).
//
// View() invokes this on every render; the cache short-circuits when
// the inputs that affect the rendered string are unchanged since the
// last call (status text, right group, in-progress flag,
// overlay-blocked state, width, theme styles). The indicator +
// truncation + style.Render pipeline otherwise rebuilds a fresh
// ANSI-encoded string every 250ms tick, only to be discarded by
// Program.render's identity check. While a task runs the indicator is the
// wall-clock spinner frame and is supposed to change every tick, so the
// cache is skipped then (useCache); the idle string is constant per width
// and stays cached.
//
// Styling model: statusLeft/statusRight are PLAIN strings (no ANSI).
// Truncation happens on the plain text, then each segment is rendered
// with its Style — the "…" inserted by truncation is inside a segment
// and inherits that segment's color from the render call, so the
// ellipsis can never fall back to the terminal default.
//
// Uses a pointer receiver so the cache map (initialized lazily and
// mutated on first call) persists across calls — value-receiver methods
// get a copy of Terminal and any cache field they mutate is discarded.
func (m *Terminal) renderStatusBar() string {
	active := !m.isBlocked()

	// The indicator column is one cell and always present: the still braille
	// cell (⠿) when idle, the shared spinner while a task runs. Reserving the
	// cell in both states keeps the segments from shifting a column when a
	// task starts or ends — the same reason the tool header keeps its label
	// column. Motion carries the state, so the bar needs no accent, no bold
	// and no second glyph (see statusIdleGlyph, spinner.go).
	indicatorGlyph := statusIdleGlyph
	if m.inProgress {
		indicatorGlyph = spinnerFrame()
	}

	cacheKey := renderStatusBarCacheKey{
		active:     active,
		inProgress: m.inProgress,
		width:      m.windowWidth,
		styles:     m.styles,
		left:       m.statusLeft,
		right:      m.statusRight,
	}
	// The rendered string is a pure function of the key — except while a task
	// runs, when the indicator is the wall-clock spinner frame and is meant to
	// change on every tick. Skip the cache then; the idle string is constant
	// per width and stays cached.
	useCache := !m.inProgress
	if useCache && m.renderedStatusBarCache != nil {
		if cached, ok := (*m.renderedStatusBarCache)[cacheKey]; ok {
			return cached
		}
	}

	// One foreground for the whole row: muted. Segments, the " | " separators
	// and the indicator all take it, so the bar is never two colors at once
	// (it used to pair muted segments with dim separators). Under an overlay
	// the row dims to a single color with the rest of the chrome rather than
	// mixing a second register in.
	barColor := m.styles.ColorMuted
	if !active {
		barColor = m.styles.ColorDim
	}
	indicatorStyle := m.styles.Status.Foreground(barColor)
	segStyle := indicatorStyle
	sepStyle := indicatorStyle

	// Hard cap: the status bar row may occupy at most the full terminal
	// width — anything wider would soft-wrap onto a second row. The cap
	// is the full width: unlike a collapsed window header, which spends
	// two cells on the fold marker and its separating space
	// (collapsedPrefixWidth), the status row reserves nothing before its
	// content. The TUI's design language is
	// flush-to-edge (input box rules, window separators all span the
	// full width), and the status content is assembled from program-
	// controlled segments (indicator, tokens, steps, video, model
	// name with its reasoning level) that contain no tabs — the one
	// case the width model documents as unreliable (ansi.Hardwrap
	// counts a tab as 0 cells).
	// So the rendered line may legitimately run right up to the edge.
	lineBudget := max(0, m.windowWidth)

	// Assemble the plain line: indicator + truncated segments +
	// right-aligned truncated group (see assembleStatusLeft).
	leftPlain := assembleStatusLeft(m.statusLeft, m.statusRight, indicatorGlyph, lineBudget)

	// Render: the indicator with its own style, then the rest per segment
	// (segments and " | " separators share one muted style now, so the whole
	// row is a single color; all dim when blocked). Sliced by indicatorGlyph's
	// byte length, not [:1]: the spinner frame is 3 bytes.
	content := indicatorStyle.Render(leftPlain[:len(indicatorGlyph)])
	if rest := leftPlain[len(indicatorGlyph):]; rest != "" {
		if strings.HasPrefix(rest, " ") {
			rest = rest[1:]
			content += " "
		}
		content += renderStatusSegments(rest, segStyle, sepStyle)
	}
	rendered := m.styles.Status.Render(content)

	if useCache {
		if m.renderedStatusBarCache == nil {
			cache := make(map[renderStatusBarCacheKey]string, 4)
			m.renderedStatusBarCache = &cache
		}
		(*m.renderedStatusBarCache)[cacheKey] = rendered
		// Bound the cache: small bounded map, drop the oldest entry when it
		// grows. Status bar inputs only flip between two states per task
		// (active/idle × blocked/unblocked) so this never grows past a
		// handful of entries in practice.
		if len(*m.renderedStatusBarCache) > 8 {
			for k := range *m.renderedStatusBarCache {
				if k != cacheKey {
					delete(*m.renderedStatusBarCache, k)
					break
				}
			}
		}
	}
	return rendered
}

// assembleStatusLeft builds the PLAIN left part of the status bar:
// the indicator glyph, the status segments truncated to the remaining
// budget (always keeping a 1-cell separator after the indicator), and
// the right group (model name + reasoning level, or the bare level when
// no model is set) right-aligned flush against the right screen edge.
//
// The group is placed by a single threshold: it floats right-aligned
// (blank padding, flush right) only when the gap exceeds 3 cells. A gap
// of ≤ 3 cells is too cramped — the group merges into the left segments
// instead, joined by " | " and truncated together (the group is last, so
// truncation eats into its tail — the reasoning level — before it costs
// the model name). The bar never shows a squeezed right-aligned group or
// a 1-3 cell gap.
//
// An empty statusRight renders the left segments alone. updateStatus
// never produces one — the group always carries at least the reasoning
// level — but renderStatusBar is also reached by a hand-built Terminal
// whose status has not been loaded yet, and by tests that pin this
// layout with bare segments.
func assembleStatusLeft(statusLeft, statusRight, indicatorGlyph string, lineBudget int) string {
	left := indicatorGlyph
	if statusLeft != "" {
		segBudget := max(0, lineBudget-Width(left)-1) // indicator + separator space
		if seg := truncateWithSuffix(statusLeft, segBudget); seg != "" {
			left += " " + seg
		}
	}
	if statusRight == "" {
		return left
	}

	// Ample space (gap > 3): the group floats flush right.
	if gap := lineBudget - Width(left) - Width(statusRight); gap > 3 {
		return left + strings.Repeat(" ", gap) + statusRight
	}

	// Tight space (gap ≤ 3): no right-aligned element — the group joins
	// the left segments, truncated together.
	merged := statusLeft
	if merged != "" {
		merged += " | "
	}
	merged += statusRight
	if merged = truncateWithSuffix(merged, max(0, lineBudget-Width(indicatorGlyph)-1)); merged != "" {
		return indicatorGlyph + " " + merged
	}
	return indicatorGlyph
}

// renderStatusSegments renders the plain joined status text ("seg | seg")
// with per-segment styles: segments in segStyle, "|" separators in
// sepStyle. The input carries no ANSI, so any "…" a truncation inserted
// inside a segment inherits segStyle from the render call — the ellipsis
// color falls out of the styling pipeline instead of needing
// escape-sequence handling.
//
// Splitting is on the bare "|", not " | ": truncation can replace the
// space after a separator with "…" (e.g. "R1 |…"), which would hide the
// separator from a " | "-based split and paint the "|" with the segment
// color. The separator's own spaces (one trailing on the left part, one
// leading on the right part) are re-emitted exactly as the plain text
// has them — a space truncated away is not restored, so the rendered
// width always matches the plain width (lineBudget). Extra spaces (the
// blank gap before the right-aligned model group) stay inside their part.
// Empty parts (dangling separators from a cut) are dropped.
func renderStatusSegments(plain string, segStyle, sepStyle Style) string {
	if plain == "" {
		return ""
	}
	parts := strings.Split(plain, "|")
	var b strings.Builder
	for i, part := range parts {
		if i > 0 && strings.HasPrefix(part, " ") {
			part = part[1:] // separator's trailing space — emitted by the previous part
		}
		sepTrail := i < len(parts)-1 && strings.HasSuffix(part, " ")
		if sepTrail {
			part = part[:len(part)-1]
		}
		if part == "" {
			continue
		}
		b.WriteString(segStyle.Render(part))
		if sepTrail {
			b.WriteString(" ")
		}
		if i < len(parts)-1 {
			b.WriteString(sepStyle.Render("|"))
			if strings.HasPrefix(parts[i+1], " ") {
				b.WriteString(" ")
			}
		}
	}
	return b.String()
}

// renderStatusBarCacheKey is the input set to the status-bar render
// pipeline. Two Terminal values with the same key produce the same
// rendered status string.
type renderStatusBarCacheKey struct {
	active     bool
	inProgress bool
	width      int
	styles     *Styles
	left       string
	right      string
}

// formatTokenCount returns a compact human-readable representation of a
// token count (e.g. 1500 → "1.5K", 1000000 → "1M").
func formatTokenCount(n int64) string {
	if n < 1_000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1_000_000 {
		v := float64(n) / 1_000
		if v == math.Floor(v) {
			return fmt.Sprintf("%.0fK", v)
		}
		return fmt.Sprintf("%.1fK", v)
	}
	v := float64(n) / 1_000_000
	if v == math.Floor(v) {
		return fmt.Sprintf("%.0fM", v)
	}
	return fmt.Sprintf("%.1fM", v)
}

// updateStatus updates the status bar state from the output writer.
//
// The status snapshot carries a monotonic Version that increments on every
// status-affecting session update (task progress, model change, MCP phase,
// theme, video config). The tick handler invokes updateStatus 4×/sec, but
// the underlying data only changes a handful of times per task — without
// the version check, updateStatus would rebuild every segment from
// scratch on every tick, allocating strings the renderer then drops on
// the same-content identity check.
//
// The early-exit fires when lastStatusVersion matches the current
// snapshot version AND we have already processed at least one version
// (lastStatusVersion != 0). The non-zero guard handles the initial call:
// before any status-affecting event has fired, both sides are 0, but
// running the first rebuild is still required to populate statusLeft /
// inProgress / appliedTheme.
func (m Terminal) updateStatus() Terminal {
	snap := m.out.SnapshotStatus()
	if m.lastStatusVersion != 0 && m.lastStatusVersion == snap.Version {
		return m
	}
	m.lastStatusVersion = snap.Version

	// Build PLAIN status segments, joined with " | " (styles are applied
	// at render time in renderStatusBar — truncation happens on the
	// plain text, so the "…" inherits the segment style naturally).
	var segments []string

	// Reasoning level ("R0".."R2") rides on the right, with the model name
	// when there is one (statusRightSegment: "DeepSeek Flash | R2") and
	// alone when there is not (a bare "R2"), never at the head of the left
	// segments. It says how the model thinks, while the left group carries
	// the live session telemetry (tokens, speed, steps) — and a field read
	// at a glance in every state must not move: pinning it to the left
	// whenever a model happened to be set and to the right whenever it did
	// not would make the same fact change address mid-session. With no
	// model the bar is the level flush right; that is the price of the
	// fixed column.
	//
	// Muted style either way: the status bar is one muted foreground and
	// carries no accent and no bold (renderStatusBar). There is
	// deliberately no glyph after the level: a marker that never changes
	// with the state (an earlier revision drew "R0✦".."R2✦", ✦ shown even
	// at 0) carries no information while looking like an indicator. The
	// auto-follow marker that used to sit here ("F↓") moved to the live edge
	// above the input box: it reports the transcript, so it belongs on the
	// transcript's own closing row rather than on the row under the prompt
	// (live_edge.go).
	m.statusRight = statusRightSegment(snap.ActiveModel, snap.ReasoningLevel)

	// Context segment
	if snap.ContextTokens > 0 {
		var ctxVal string
		if snap.ContextLimit > 0 {
			pct := float64(snap.ContextTokens) * 100.0 / float64(snap.ContextLimit)
			ctxVal = fmt.Sprintf("%s/%s %.1f%%", formatTokenCount(snap.ContextTokens), formatTokenCount(snap.ContextLimit), pct)
		} else {
			ctxVal = formatTokenCount(snap.ContextTokens)
		}
		segments = append(segments, ctxVal)
	}

	// Speed segment — right after the context segment: the latest step's
	// end-to-end tok/s (+ TTFT). Kept visible after the task ends (final
	// step speed) until the next task starts.
	if v := statusSpeedSegment(snap.StepTPS, snap.TTFTMS); v != "" {
		segments = append(segments, v)
	}

	// Steps segment (rightmost — show only when there's step activity)
	if stepVal := statusStepsSegment(snap.InProgress, snap.CurrentStep, snap.MaxSteps,
		snap.LastCurrentStep, snap.LastMaxSteps); stepVal != "" {
		segments = append(segments, stepVal)
	}

	// Video config segment (last)
	if fps := snap.VideoFPS; fps > 0 {
		segments = append(segments, fmt.Sprintf("V:%d,%d", fps, snap.VideoRes))
	}

	m.statusLeft = strings.Join(segments, " | ")
	m.inProgress = snap.InProgress

	m = m.syncThemeFromSession(snap.ActiveTheme, snap.ActiveThemeData)
	m.activeTheme = snap.ActiveTheme
	return m
}
