package terminal

// Status bar: session state display (steps, tokens, switches).
//
// Extracted from tui.go. Owns statusText and inProgress state,
// and provides rendering helpers.

import (
	"fmt"
	"math"
	"strings"

	"github.com/alayacore/alayacore/internal/config"
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
func (m Terminal) renderStatusBar() string {
	active := !m.isBlocked()

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

	if m.statusText != "" {
		text := m.statusText
		if !active {
			text = m.statusTextDim
		}
		return m.styles.Status.Render(indicator + " " + text)
	}
	return m.styles.Status.Render(indicator)
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
func (m Terminal) updateStatus() Terminal {
	snap := m.out.SnapshotStatus()

	valStyle := m.styles.Status.Foreground(m.styles.ColorMuted)
	dimValStyle := m.styles.Status.Foreground(m.styles.ColorDim)

	// Build status segments - each rendered separately with appropriate colors
	var segments []string
	var dimSegments []string

	// Switch indicators segment (compact: "R1✦ F↓" in one segment)
	var switches []string
	var dimSwitches []string
	if snap.ReasoningLevel > config.ReasoningLevelOff {
		reasonStyle := m.styles.Status.Foreground(m.styles.ColorAccent).Bold(true)
		switches = append(switches, reasonStyle.Render(fmt.Sprintf("R%d✦", snap.ReasoningLevel)))
		dimSwitches = append(dimSwitches, dimValStyle.Render(fmt.Sprintf("R%d✦", snap.ReasoningLevel)))
	}
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
