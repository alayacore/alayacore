package terminal

// Status bar: session state display (steps, tokens, switches).
//
// Extracted from tui.go. Owns statusLeft and inProgress state,
// and provides rendering helpers.

import (
	"fmt"
	"math"
	"strings"
)

// statusStepsSegment returns the steps status string, or "" if no activity.
// During a run it shows live progress ("3/5", "3/INF"); after completion it
// shows the last run's frozen summary ("3/5", "3/INF") until the next task
// starts. The leading dot in the status bar (• live vs · idle) tells the two
// apart. Tool windows follow the same •/· convention: hollow while args
// stream in, solid while running.
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
		return fmt.Sprintf("%.1f tok/s · ttft %.1fs", stepTPS, float64(ttftMS)/1000)
	}
	return fmt.Sprintf("%.1f tok/s", stepTPS)
}

// renderStatusBar renders the status bar line.
// Status bar is dimmed when an overlay is active.
//
// Layout: the status segments (reasoning, context, steps, video) start at
// the left after the status dot; the active model name is right-aligned
// flush against the right screen edge in the remaining flexible space
// between them, truncated with "…" when it cannot fit and dropped when
// there is no room. Without a model the left-aligned segments may also
// run up to the right edge — the TUI's flush-to-edge design language.
//
// The result is truncated to at most the terminal width so a runaway
// status string — e.g. a session with every switch + a long token count
// + many steps + video config + a long model name — does not soft-wrap
// onto a second row in raw passthrough mode. Two status rows would push
// the input box's rendered content against the bottom rule and overlap
// the prompt area, even though the input box is now drawn with an
// absolute CUP (the status bar itself is anchored to the last row, so it
// would visibly wrap onto the second-to-last row).
//
// View() invokes this on every render; the cache short-circuits when
// the inputs that affect the rendered string are unchanged since the
// last call (status text, model segment, in-progress flag,
// overlay-blocked state, width, theme styles). The indicator +
// truncation + style.Render pipeline otherwise rebuilds a fresh
// ANSI-encoded string every 250ms tick, only to be discarded by
// Program.render's identity check.
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
	cacheKey := renderStatusBarCacheKey{
		active:     active,
		inProgress: m.inProgress,
		width:      m.windowWidth,
		styles:     m.styles,
		left:       m.statusLeft,
		right:      m.statusRight,
	}
	if m.renderedStatusBarCache != nil {
		if cached, ok := (*m.renderedStatusBarCache)[cacheKey]; ok {
			return cached
		}
	}

	// Indicator dot: accent while a task runs (same convention as tool
	// windows; green is reserved for success), dim otherwise.
	indicatorGlyph := "·"
	indicatorStyle := m.styles.Status.Foreground(m.styles.ColorDim)
	if m.inProgress {
		indicatorGlyph = "•"
		if active {
			indicatorStyle = m.styles.Status.Foreground(m.styles.ColorAccent)
		}
	}

	// Segment styles: muted segments with dim " | " separators when
	// active; everything dim when the bar is blocked.
	segStyle := m.styles.Status.Foreground(m.styles.ColorMuted)
	sepStyle := m.styles.Status // dim
	if !active {
		segStyle = m.styles.Status.Foreground(m.styles.ColorDim)
		sepStyle = segStyle
	}

	// Hard cap: the status bar row may occupy at most the full terminal
	// width — anything wider would soft-wrap onto a second row. The cap
	// is the full width (not width-2): the TUI's design language is
	// flush-to-edge (input box rules, window separators all span the
	// full width), and the status content is assembled from program-
	// controlled segments (indicator, reasoning, tokens, steps, video,
	// model name) that contain no tabs — the one case the width model
	// documents as unreliable (ansi.Hardwrap counts a tab as 0 cells).
	// So the rendered line may legitimately run right up to the edge.
	lineBudget := max(0, m.windowWidth)

	// Assemble the plain line: indicator + truncated segments +
	// right-aligned truncated model (see assembleStatusLeft).
	leftPlain := assembleStatusLeft(m.statusLeft, m.statusRight, indicatorGlyph, lineBudget)

	// Render: indicator with its own style, the rest per segment
	// (segments muted, " | " separators dim — all dim when blocked).
	// Sliced by indicatorGlyph's byte length, not [:1]: "•" is 3 bytes.
	content := indicatorStyle.Render(leftPlain[:len(indicatorGlyph)])
	if rest := leftPlain[len(indicatorGlyph):]; rest != "" {
		if strings.HasPrefix(rest, " ") {
			rest = rest[1:]
			content += " "
		}
		content += renderStatusSegments(rest, segStyle, sepStyle)
	}
	rendered := m.styles.Status.Render(content)

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
	return rendered
}

// assembleStatusLeft builds the PLAIN left part of the status bar:
// the indicator glyph, the status segments truncated to the remaining
// budget (always keeping a 1-cell separator after the indicator), and
// the model right-aligned flush against the right screen edge.
//
// The model is separated from the segments by the same " | " token used
// between segments: the gap is either exactly 3 cells — rendered as
// " | " — or larger (blank padding, model flush right). A gap of 1-2
// cells never renders: the model is truncated until the gap is exactly
// 3, and dropped when even one column cannot fit next to the separator.
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

	// The model needs the 3-cell " | " separator plus at least one
	// column of its own; gaps of 1-2 cells are never rendered.
	remaining := lineBudget - Width(left)
	modelWidth := min(Width(statusRight), remaining-3)
	if modelWidth < 1 {
		return left
	}
	model := truncateWithSuffix(statusRight, modelWidth)
	if gap := remaining - Width(model); gap == 3 {
		// Gap exactly the separator width: " | " keeps the model
		// flush right while reading like any other segment.
		left += " | " + model
	} else {
		// Larger gap: blank padding, model flush right.
		left += strings.Repeat(" ", gap) + model
	}
	return left
}

// renderStatusSegments renders the plain joined status text ("seg | seg")
// with per-segment styles: segments in segStyle, " | " separators in
// sepStyle. The input carries no ANSI, so any "…" a truncation inserted
// inside a segment inherits segStyle from the render call — the ellipsis
// color falls out of the styling pipeline instead of needing
// escape-sequence handling. Empty segments (e.g. a cut that left a bare
// separator) are dropped.
func renderStatusSegments(plain string, segStyle, sepStyle Style) string {
	if plain == "" {
		return ""
	}
	var b strings.Builder
	first := true
	for _, seg := range strings.Split(plain, " | ") {
		if seg == "" {
			continue
		}
		if !first {
			b.WriteString(" ")
			b.WriteString(sepStyle.Render("|"))
			b.WriteString(" ")
		}
		first = false
		b.WriteString(segStyle.Render(seg))
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
	autoFollow := m.display.shouldFollow()
	// The cached m.statusLeft embeds the auto-follow indicator ("F↓").
	// updateStatus only rebuilds when the status snapshot version changes
	// (task progress, model change, MCP phase, theme, video config). The
	// auto-follow state lives on the DisplayModel and flips when the user
	// navigates with j/k/h/l/G/space — none of those bump the version, so
	// without this second check the F↓ indicator would stay stale until
	// the next status-affecting session event.
	autoFollowChanged := m.lastStatusAutoFollow == nil || *m.lastStatusAutoFollow != autoFollow
	if m.lastStatusVersion != 0 && m.lastStatusVersion == snap.Version && !autoFollowChanged {
		return m
	}
	m.lastStatusVersion = snap.Version
	seen := autoFollow
	m.lastStatusAutoFollow = &seen

	// Build PLAIN status segments, joined with " | " (styles are applied
	// at render time in renderStatusBar — truncation happens on the
	// plain text, so the "…" inherits the segment style naturally).
	var segments []string

	// Switch indicators segment (compact: "R1✦ F↓" in one segment).
	// Reasoning level is always rendered ("R0✦".."R2✦") using the muted
	// style — the accent color and bold are reserved for the status dot,
	// which remains the only highlighted element in the status bar.
	switches := fmt.Sprintf("R%d✦", snap.ReasoningLevel)
	if m.display.shouldFollow() {
		switches += " F↓"
	}
	segments = append(segments, switches)

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
	// Model segment — not joined with the left segments; renderStatusBar
	// right-aligns it in the remaining flexible space and truncates it
	// with "…" when the left segments leave no room.
	m.statusRight = snap.ActiveModel
	m.inProgress = snap.InProgress

	m = m.syncThemeFromSession(snap.ActiveTheme, snap.ActiveThemeData)
	m.activeTheme = snap.ActiveTheme
	return m
}
