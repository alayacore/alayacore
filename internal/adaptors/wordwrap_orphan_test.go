package adaptors

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestWordwrapOrphanPrevention(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		width    int
		maxLines int // maximum acceptable number of lines
	}{
		{
			name:     "short orphan word 'first.'",
			text:     "I'll help you compare the accessing speed to baidu.com and taobao.com. This is a non-trivial task that involves multiple steps, so I need to create a plan first.",
			width:    80,
			maxLines: 3, // Should not have orphan "first." on its own line
		},
		{
			name:     "short orphan word 'properly.'",
			text:     "1. **Check network connectivity and DNS resolution for both domains** - First, I'll verify that both domains are accessible and resolve their IP addresses to ensure we can test them properly.",
			width:    80,
			maxLines: 3, // Should not have orphan "properly." on its own line
		},
		{
			name:     "short orphan word 'latency.'",
			text:     "2. **Measure ping latency to baidu.com and taobao.com** - I'll use ping tests to measure the round-trip time (RTT) to both servers, which gives us the basic network latency.",
			width:    80,
			maxLines: 3, // Should not have orphan "latency." on its own line
		},
		{
			name:     "short orphan word 'sites.'",
			text:     "3. **Test HTTP response times for both websites** - I'll use tools like curl to measure the time it takes to establish connections and receive HTTP responses from both sites.",
			width:    80,
			maxLines: 3, // Should not have orphan "sites." on its own line
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := wordwrap(tt.text, tt.width)
			lines := strings.Split(strings.TrimSuffix(result, "\n"), "\n")
			numLines := len(lines)

			// Verify we don't exceed max expected lines
			if numLines > tt.maxLines {
				t.Errorf("Too many lines: got %d lines, want at most %d", numLines, tt.maxLines)
				for i, line := range lines {
					t.Logf("Line %d (%3d chars): %q", i+1, lipgloss.Width(line), line)
				}
			}

			// Check for very short lines on non-last lines (orphans)
			orphanThreshold := tt.width / 5 // 20% of width
			for i, line := range lines {
				if i < len(lines)-1 { // Don't check last line
					lineWidth := lipgloss.Width(line)
					if lineWidth > 0 && lineWidth < orphanThreshold {
						t.Errorf("Very short line detected (possible orphan): line %d has width %d: %q", i+1, lineWidth, line)
					}
				}
			}
		})
	}
}
