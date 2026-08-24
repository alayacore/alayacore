package terminal

// Status bar: session state display (steps, tokens, switches).
//
// Extracted from tui.go. Owns statusText and inProgress state,
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
		status:     m.statusText,
		statusDim:  m.statusTextDim,
		model:      m.statusModel,
		modelDim:   m.statusModelDim,
	}
	if m.renderedStatusBarCache != nil {
		if cached, ok := (*m.renderedStatusBarCache)[cacheKey]; ok {
			return cached
		}
	}

	var indicator string
	if m.inProgress {
		if active {
			// Running dot uses the theme's primary color — the same
			// convention as tool windows; green is reserved for success.
			indicator = m.styles.Status.Foreground(m.styles.ColorAccent).Render("•")
		} else {
			indicator = m.styles.Status.Foreground(m.styles.ColorDim).Render("•")
		}
	} else {
		indicator = m.styles.Status.Foreground(m.styles.ColorDim).Render("·")
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

	var content string
	if m.statusText != "" {
		text := m.statusText
		if !active {
			text = m.statusTextDim
		}
		content = indicator + " " + text
	} else {
		content = indicator
	}

	// Model segment: right-aligned FLUSH against the right screen edge in
	// the remaining flexible space after the status segments. The left
	// segments keep priority — the model is squeezed into whatever space
	// is left (always keeping a 1-cell separator so it reads as a
	// distinct right-aligned element), truncated with "…" when it cannot
	// fit fully, and dropped entirely when there is not even room for
	// the separator.
	model := m.statusModel
	if !active {
		model = m.statusModelDim
	}
	if model != "" {
		leftWidth := Width(content)
		model = truncateWithSuffix(model, max(0, lineBudget-leftWidth-1))
		if model != "" {
			content += strings.Repeat(" ", max(0, lineBudget-leftWidth-Width(model))) + model
		}
	}
	content = truncateWithSuffix(content, lineBudget)
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

// renderStatusBarCacheKey is the input set to the status-bar render
// pipeline. Two Terminal values with the same key produce the same
// rendered status string.
type renderStatusBarCacheKey struct {
	active     bool
	inProgress bool
	width      int
	styles     *Styles
	status     string
	statusDim  string
	model      string
	modelDim   string
}

// statusModelSegment renders the active model segment for the status bar
// in both active and dimmed variants (returned in that order). An empty
// model yields two empty strings — renderStatusBar then omits the
// right-aligned element entirely.
func statusModelSegment(model string, valStyle, dimValStyle Style) (string, string) {
	if model == "" {
		return "", ""
	}
	return valStyle.Render(model), dimValStyle.Render(model)
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
// running the first rebuild is still required to populate statusText /
// statusTextDim / inProgress / appliedTheme.
func (m Terminal) updateStatus() Terminal {
	snap := m.out.SnapshotStatus()
	autoFollow := m.display.shouldFollow()
	// The cached m.statusText embeds the auto-follow indicator ("F↓").
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

	valStyle := m.styles.Status.Foreground(m.styles.ColorMuted)
	dimValStyle := m.styles.Status.Foreground(m.styles.ColorDim)

	// Build status segments - each rendered separately with appropriate colors
	var segments []string
	var dimSegments []string

	// Switch indicators segment (compact: "R1✦ F↓" in one segment).
	// Reasoning level is always rendered ("R0✦".."R2✦") using the muted
	// style — the accent color and bold are reserved for the status dot,
	// which remains the only highlighted element in the status bar.
	var switches []string
	var dimSwitches []string
	reasonLabel := fmt.Sprintf("R%d✦", snap.ReasoningLevel)
	switches = append(switches, valStyle.Render(reasonLabel))
	dimSwitches = append(dimSwitches, dimValStyle.Render(reasonLabel))
	if m.display.shouldFollow() {
		switches = append(switches, valStyle.Render("F↓"))
		dimSwitches = append(dimSwitches, dimValStyle.Render("F↓"))
	}
	if len(switches) > 0 {
		segments = append(segments, strings.Join(switches, " "))
		dimSegments = append(dimSegments, strings.Join(dimSwitches, " "))
	}

	// Context segment
	if snap.ContextTokens > 0 {
		var ctxVal string
		if snap.ContextLimit > 0 {
			pct := float64(snap.ContextTokens) * 100.0 / float64(snap.ContextLimit)
			ctxVal = fmt.Sprintf("%s/%s %.1f%%", formatTokenCount(snap.ContextTokens), formatTokenCount(snap.ContextLimit), pct)
		} else {
			ctxVal = formatTokenCount(snap.ContextTokens)
		}
		segments = append(segments, valStyle.Render(ctxVal))
		dimSegments = append(dimSegments, dimValStyle.Render(ctxVal))
	}

	// Steps segment (rightmost — show only when there's step activity)
	if stepVal := statusStepsSegment(snap.InProgress, snap.CurrentStep, snap.MaxSteps,
		snap.LastCurrentStep, snap.LastMaxSteps); stepVal != "" {
		segments = append(segments, valStyle.Render(stepVal))
		dimSegments = append(dimSegments, dimValStyle.Render(stepVal))
	}

	// Video config segment (last)
	if fps := snap.VideoFPS; fps > 0 {
		segments = append(segments, valStyle.Render(fmt.Sprintf("V:%d,%d", fps, snap.VideoRes)))
		dimSegments = append(dimSegments, dimValStyle.Render(fmt.Sprintf("V:%d,%d", fps, snap.VideoRes)))
	}

	// Model segment — not joined with the left segments; renderStatusBar
	// right-aligns it in the remaining flexible space and truncates it
	// with "…" when the left segments leave no room.
	m.statusModel, m.statusModelDim = statusModelSegment(snap.ActiveModel, valStyle, dimValStyle)

	// Join segments with dimmed separator
	var status string
	if len(segments) > 0 {
		separator := m.styles.Status.Render("|")
		status = segments[0]
		for i := 1; i < len(segments); i++ {
			status += " " + separator + " " + segments[i]
		}
	}

	var dimStatus string
	if len(dimSegments) > 0 {
		dimSeparator := m.styles.Status.Foreground(m.styles.ColorDim).Render("|")
		dimStatus = dimSegments[0]
		for i := 1; i < len(dimSegments); i++ {
			dimStatus += " " + dimSeparator + " " + dimSegments[i]
		}
	}

	m.statusText = status
	m.statusTextDim = dimStatus
	m.inProgress = snap.InProgress

	m = m.syncThemeFromSession(snap.ActiveTheme, snap.ActiveThemeData)
	m.activeTheme = snap.ActiveTheme
	return m
}
