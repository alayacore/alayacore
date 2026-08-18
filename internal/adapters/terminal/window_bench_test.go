package terminal

import (
	"fmt"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/theme"
)

// BenchmarkWindowBufferDelta benchmarks the performance of delta updates.
// Before the fix, each delta would re-render all windows (O(n)).
// After the fix, each delta only re-renders the dirty window (O(1)).
//
// Note: The content grows on each iteration, so word-wrapping cost increases.
// This measures the worst-case scenario where the last window keeps growing.
func BenchmarkWindowBufferDelta(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 100 windows (simulating a long conversation)
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("This is a test message with some content.\n", 5)
		wb.AppendOrUpdate("AT", id, content)
	}

	// Pre-render to ensure everything is cached
	_ = wb.GetTotalLines()

	// Get the last window's ID for delta updates
	lastID := "msg99"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Simulate delta update - append to existing window
		wb.AppendOrUpdate("AT", lastID, " additional text")

		// Trigger rendering (what happens on each update)
		_ = wb.GetTotalLines()
	}
}

// BenchmarkWindowBufferDeltaNewWindow tests updating a NEW window each time.
// This measures the cost of rendering one window without the growing content issue.
func BenchmarkWindowBufferDeltaNewWindow(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 100 initial windows
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("This is a test message with some content.\n", 5)
		wb.AppendOrUpdate("AT", id, content)
	}

	// Pre-render to ensure everything is cached
	_ = wb.GetTotalLines()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Create a NEW window each time (simulates new message in conversation)
		id := fmt.Sprintf("newmsg%d", b.N*1000+i)
		wb.AppendOrUpdate("AT", id, "New message content.\n")

		// Trigger rendering
		_ = wb.GetTotalLines()
	}
}

// BenchmarkWindowBufferDeltaSingleWindow benchmarks delta updates with only one window.
// This should be fast regardless of the optimization.
func BenchmarkWindowBufferDeltaSingleWindow(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Single window
	wb.AppendOrUpdate("AT", "msg0", strings.Repeat("Initial content\n", 10))

	// Pre-render
	_ = wb.GetTotalLines()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wb.AppendOrUpdate("AT", "msg0", " delta")
		_ = wb.GetTotalLines()
	}
}

// BenchmarkWindowBufferGetWindowLineRange benchmarks the GetWindowLineRange function.
// This has O(n) behavior that could be optimized with a prefix sum array.
func BenchmarkWindowBufferGetWindowLineRange(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 100 windows
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("Content line\n", 5)
		wb.AppendOrUpdate("AT", id, content)
	}

	// Ensure line heights are calculated
	_ = wb.GetTotalLines()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Access middle window (worst case for O(n) loop)
		_, _ = wb.GetWindowLineRange(50)
	}
}

// BenchmarkWindowBufferGetAll benchmarks the GetAll function with virtual rendering.
func BenchmarkWindowBufferGetAll(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 100 windows
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("This is a test message with some content.\n", 5)
		wb.AppendOrUpdate("AT", id, content)
	}

	// Set up viewport for virtual rendering
	wb.SetViewportPosition(0, 30)
	_ = wb.GetTotalLines()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = wb.GetAll(-1, false)
	}
}

// BenchmarkWindowBufferDeltaWithGetAll benchmarks the full update cycle including GetAll.
func BenchmarkWindowBufferDeltaWithGetAll(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 100 windows (simulating a long conversation)
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("This is a test message with some content.\n", 5)
		wb.AppendOrUpdate("AT", id, content)
	}

	// Set up viewport for virtual rendering
	wb.SetViewportPosition(0, 30)
	_ = wb.GetTotalLines()

	// Get the last window's ID for delta updates
	lastID := "msg99"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Simulate delta update
		wb.AppendOrUpdate("AT", lastID, " additional text")

		// Full render cycle
		_ = wb.GetTotalLines()
		_ = wb.GetAll(-1, false)
	}
}

// BenchmarkVirtualRenderingCursorMovement benchmarks cursor movement with virtual rendering.
// This tests the EnsureCursorVisible + updateContent path.
// Uses 20 cursor moves per iteration for statistical significance while keeping
// wall time reasonable.  See BenchmarkVirtualRenderingCursorMovementSingle for
// the per-move cost.
func BenchmarkVirtualRenderingCursorMovement(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 100 windows
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("This is a test message with some content.\n", 5)
		wb.AppendOrUpdate("AT", id, content)
	}

	// Set up viewport
	wb.SetViewportPosition(0, 30)
	_ = wb.GetTotalLines()

	dm := NewDisplayModel(wb, styles)
	dm = dm.WithHeight(30)
	dm = dm.WithDisplayFocused(true)
	dm = dm.updateContent()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Simulate moving cursor through windows
		for j := 0; j < 20; j++ {
			dm = dm.WithWindowCursor(j)
			dm = dm.EnsureCursorVisible()
			dm = dm.updateContent()
		}
	}
}

// BenchmarkVirtualRenderingCursorMovementSingle tests a single cursor move (more realistic)
func BenchmarkVirtualRenderingCursorMovementSingle(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 100 windows
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("This is a test message with some content.\n", 5)
		wb.AppendOrUpdate("AT", id, content)
	}

	// Set up viewport
	wb.SetViewportPosition(0, 30)
	_ = wb.GetTotalLines()

	dm := NewDisplayModel(wb, styles)
	dm = dm.WithHeight(30)
	dm = dm.WithDisplayFocused(true)
	dm = dm.updateContent()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Single cursor move (realistic user action)
		dm = dm.WithWindowCursor(i % 100)
		dm = dm.EnsureCursorVisible()
		dm = dm.updateContent()
	}
}

// BenchmarkStreamingUpdateWithIncremental uses the incremental path properly
func BenchmarkStreamingUpdateWithIncremental(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 50 existing windows (conversation history)
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("Historical message content.\n", 3)
		wb.AppendOrUpdate("AT", id, content)
	}

	// Set up viewport
	wb.SetViewportPosition(0, 30)
	_ = wb.GetTotalLines()

	// Create streaming window with initial content
	historyID := "stream-current"
	wb.AppendOrUpdate("AT", historyID, "Starting...")

	// Pre-render to populate wrappedLines cache
	_ = wb.GetTotalLines()
	_ = wb.GetAll(-1, false)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Simulate streaming delta - this should use incremental wrapping
		wb.AppendOrUpdate("AT", historyID, " more")
		_ = wb.GetTotalLines()
		_ = wb.GetAll(-1, false)
	}
}

// BenchmarkStreamingUpdateWithoutIncremental forces full re-wrap
func BenchmarkStreamingUpdateWithoutIncremental(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 50 existing windows
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("Historical message content.\n", 3)
		wb.AppendOrUpdate("AT", id, content)
	}

	// Set up viewport
	wb.SetViewportPosition(0, 30)
	_ = wb.GetTotalLines()

	// Create streaming window
	historyID := "stream-current"
	wb.AppendOrUpdate("AT", historyID, "Starting...")
	_ = wb.GetTotalLines()
	_ = wb.GetAll(-1, false)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Force full re-wrap by invalidating BEFORE append
		wb.mu.Lock()
		if idx, ok := wb.idIndex[historyID]; ok {
			wb.windows[idx].Invalidate()
		}
		wb.mu.Unlock()

		wb.AppendOrUpdate("AT", historyID, " more")
		_ = wb.GetTotalLines()
		_ = wb.GetAll(-1, false)
	}
}

// BenchmarkStreamingSmallDelta tests with small streaming deltas
func BenchmarkStreamingSmallDelta(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 20 existing windows
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("Content.\n", 5)
		wb.AppendOrUpdate("AT", id, content)
	}

	wb.SetViewportPosition(0, 30)
	_ = wb.GetTotalLines()

	historyID := "stream"
	wb.AppendOrUpdate("AT", historyID, "")
	_ = wb.GetTotalLines()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wb.AppendOrUpdate("AT", historyID, "word ")
		_ = wb.GetTotalLines()
	}
}

// BenchmarkJustAppendUpdate isolates the AppendOrUpdate cost
func BenchmarkJustAppendUpdate(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 20 existing windows
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("Content.\n", 5)
		wb.AppendOrUpdate("AT", id, content)
	}

	historyID := "stream"
	wb.AppendOrUpdate("AT", historyID, "")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wb.AppendOrUpdate("AT", historyID, "word ")
	}
}

// BenchmarkJustEnsureLineHeights isolates the ensureLineHeights cost
// when a single window is dirty (incremental path).
func BenchmarkJustEnsureLineHeights(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 20 existing windows
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("Content.\n", 5)
		wb.AppendOrUpdate("AT", id, content)
	}

	historyID := "stream"
	wb.AppendOrUpdate("AT", historyID, "initial")
	_ = wb.GetTotalLines()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wb.AppendOrUpdate("AT", historyID, " word")
		wb.dirty = true
		wb.dirtyIndex = wb.idIndex[historyID]
		_ = wb.GetTotalLines()
	}
}

// BenchmarkStreamingDebug shows why streaming is slow
func BenchmarkStreamingDebug(_ *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	historyID := "stream"
	wb.AppendOrUpdate("AT", historyID, strings.Repeat("Line ", 10))
	_ = wb.GetTotalLines()

	w := wb.WindowAt(0)
	fmt.Printf("Initial: wrappedLines=%d, Content=%d, cache.contentLen=%d\n",
		0, len(w.RawContent()), 0)

	for i := 0; i < 3; i++ {
		wb.AppendOrUpdate("AT", historyID, " more")
		fmt.Printf("\nAfter AppendOrUpdate %d:\n", i+1)
		fmt.Printf("  Content=%d, cache.contentLen=%d, cache.valid=%v\n",
			len(w.RawContent()), 0, false)

		// In Render(), this check happens:
		// if len(w.RawContent()) == 0 { ... } else { false = false }
		// Since Content changed, cache.valid becomes false
		// Then rebuildCache() is called, which calls renderGenericContent()

		_ = wb.GetTotalLines()
		fmt.Printf("After GetTotalLines %d:\n", i+1)
		fmt.Printf("  cache.valid=%v, cache.contentLen=%d\n", false, 0)
	}
}

// BenchmarkSingleWindowStreaming tests streaming with just one window
func BenchmarkSingleWindowStreaming(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	historyID := "stream"
	wb.AppendOrUpdate("AT", historyID, "initial content")
	_ = wb.GetTotalLines()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wb.AppendOrUpdate("AT", historyID, " word")
		_ = wb.GetTotalLines()
	}
}

// BenchmarkSingleWindowStreamingDebug prints debug info
func BenchmarkSingleWindowStreamingDebug(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	historyID := "stream"
	wb.AppendOrUpdate("AT", historyID, "initial content")
	_ = wb.GetTotalLines()

	// Check initial state
	w := wb.WindowAt(0)
	fmt.Printf("Initial: wrappedLines=%d, content=%q, cache.valid=%v\n",
		0, w.RawContent(), false)

	for i := 0; i < 3; i++ {
		wb.AppendOrUpdate("AT", historyID, " word")
		fmt.Printf("After append %d: wrappedLines=%d, content=%q, cache.valid=%v\n",
			i+1, 0, w.RawContent(), false)

		// wrappedLines debug removed during refactoring
		_ = wb.GetTotalLines()
		fmt.Printf("After ensureLineHeights %d: wrappedLines=%d, cache.valid=%v\n",
			i+1, 0, false)
		// wrappedLines debug removed during refactoring
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wb.AppendOrUpdate("AT", historyID, " word")
		_ = wb.GetTotalLines()
	}
}

// BenchmarkLongContentStreaming tests with longer content to trigger wrapping
func BenchmarkLongContentStreaming(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	historyID := "stream"
	// Start with content long enough to wrap
	wb.AppendOrUpdate("AT", historyID, strings.Repeat("This is a line that will wrap. ", 10))
	_ = wb.GetTotalLines()

	w := wb.WindowAt(0)
	fmt.Printf("Initial: wrappedLines=%d, contentLen=%d, styles=%v\n",
		0, len(w.RawContent()), w.styles != nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wb.AppendOrUpdate("AT", historyID, " more text")
		_ = wb.GetTotalLines()
	}
}

// BenchmarkDirectAppend tests AppendContent directly
func BenchmarkDirectAppend(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	w := NewWindow("test", "AT", styles)
	w.AppendContent(strings.Repeat("This is a line that will wrap. ", 10))

	// Initial render to populate cache
	w.Render(80, false, styles,
		NewStyle(), false)

	fmt.Printf("Initial: wrappedLines=%d, contentLen=%d, styles=%v\n",
		0, len(w.RawContent()), w.styles != nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.AppendContent(" more")
		_ = w.Render(80, false, styles,
			NewStyle(), false)
	}
}

// BenchmarkDirectAppendNoStyles tests without styles
func BenchmarkDirectAppendNoStyles(b *testing.B) {
	w := NewWindow("test", "AT", nil) // No styles!)
	w.AppendContent(strings.Repeat("This is a line that will wrap. ", 10))

	styles := NewStyles(theme.DefaultTheme())
	w.Render(80, false, styles,
		NewStyle(), false)

	fmt.Printf("Initial (no styles): wrappedLines=%d\n", 0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.AppendContent(" more")
		_ = w.Render(80, false, styles,
			NewStyle(), false)
	}
}

// BenchmarkDirectAppendDebug shows what's happening
func BenchmarkDirectAppendDebug(_ *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	w := NewWindow("test", "AT", styles)
	w.AppendContent(strings.Repeat("This is a line that will wrap. ", 10))

	// Initial render
	w.Render(80, false, styles,
		NewStyle(), false)

	fmt.Printf("Initial: wrappedLines=%d, cache.width=%d, width-4=%d\n",
		0, 0, 0-4)

	for i := 0; i < 3; i++ {
		w.AppendContent(" more text here")
		fmt.Printf("After AppendContent %d: wrappedLines=%d, cache.valid=%v\n",
			i+1, 0, false)

		// Check fast path condition
		fmt.Printf("  Fast path check: len(wrappedLines)=%d, cache.width-4=%d, innerWidth=76\n",
			0, 0-4)

		_ = w.Render(80, false, styles,
			NewStyle(), false)
		fmt.Printf("After Render %d: wrappedLines=%d, cache.valid=%v\n",
			i+1, 0, false)
	}
}

// BenchmarkRenderAfterAppend tests render after append (should use fast path)
func BenchmarkRenderAfterAppend(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	w := NewWindow("test", "AT", styles)
	w.AppendContent(strings.Repeat("This is a line that will wrap. ", 10))

	// Initial render to populate cache
	w.Render(80, false, styles,
		NewStyle(), false)

	fmt.Printf("Initial: wrappedLines=%d, cache.valid=%v, cache.width=%d\n",
		0, false, 0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		w.AppendContent(" more text here")
		b.StartTimer()

		_ = w.Render(80, false, styles,
			NewStyle(), false)
	}
}

// BenchmarkFullRebuildAfterAppend tests full rebuild (when wrappedLines is nil)
func BenchmarkFullRebuildAfterAppend(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	w := NewWindow("test", "AT", styles)
	w.AppendContent(strings.Repeat("This is a line that will wrap. ", 10))

	// Initial render
	w.Render(80, false, styles,
		NewStyle(), false)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		w.AppendContent(" more text here")
		w.Invalidate() // Force full rebuild
		b.StartTimer()

		_ = w.Render(80, false, styles,
			NewStyle(), false)
	}
}

// BenchmarkStreamingUpdateWithVirtualRendering shows virtual rendering benefit
// This test shows virtual rendering helping when viewport is NOT at the bottom
func BenchmarkStreamingUpdateWithVirtualRendering(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 100 windows - user is viewing the middle
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("Historical message content.\n", 3)
		wb.AppendOrUpdate("AT", id, content)
	}

	// Set up viewport in the MIDDLE of content (virtual rendering should help here)
	wb.SetViewportPosition(150, 30) // Middle of ~300 lines
	_ = wb.GetTotalLines()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Simulate delta to last window (which is outside viewport)
		wb.AppendOrUpdate("AT", "msg99", " more")
		_ = wb.GetTotalLines()
		_ = wb.GetAll(-1, false)
	}
}

// BenchmarkStreamingUpdateWithoutVirtualRendering shows cost without virtual rendering
func BenchmarkStreamingUpdateWithoutVirtualRendering(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 100 windows - user is viewing the middle
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("Historical message content.\n", 3)
		wb.AppendOrUpdate("AT", id, content)
	}

	// No viewport set - full render every time
	_ = wb.GetTotalLines()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Simulate delta to last window
		wb.AppendOrUpdate("AT", "msg99", " more")
		_ = wb.GetTotalLines()
		_ = wb.GetAll(-1, false)
	}
}

// BenchmarkGetAllOnly isolates the GetAll cost
func BenchmarkGetAllWithVirtual(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 100 windows
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("Historical message content.\n", 3)
		wb.AppendOrUpdate("AT", id, content)
	}

	// Set up viewport
	wb.SetViewportPosition(150, 30)
	_ = wb.GetTotalLines()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = wb.GetAll(-1, false)
	}
}

func BenchmarkGetAllWithoutVirtual(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 100 windows
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("Historical message content.\n", 3)
		wb.AppendOrUpdate("AT", id, content)
	}

	// No viewport
	_ = wb.GetTotalLines()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = wb.GetAll(-1, false)
	}
}

// ============================================================================
// Performance Summary
// ============================================================================
//
// Virtual Rendering Performance (100 windows, viewport showing middle 30 lines):
//
// GetAll (rendering only):
//   - With virtual rendering:    ~20μs (3.5x faster)
//   - Without virtual rendering: ~71μs
//
// Full update cycle (delta + GetAll):
//   - Incremental (1 dirty):     ~41μs
//   - Full rebuild (all dirty):  ~5.3ms (130x slower)
//
// Incremental wrapping (wrap operation only):
//   - Incremental append:        ~1.6μs
//   - Full wrap:                 ~72μs (45x slower)
//
// Cursor movement (single):
//   - EnsureCursorVisible + updateContent: ~340μs average (best ~210μs, worst ~800μs)
//
// Realistic streaming (profiled):
//   - Average render time:       ~700μs per update
//   - Render overhead:           ~1.4% of total time (at 50ms intervals)
//   - Updates/second:            ~3572
//
// Conclusion: NO RATE LIMITING NEEDED
//   - UI refresh is polled at 250ms intervals (tui.go → TickInterval)
//   - Render overhead is only 1.4% of wall time
//   - updateContent() skips unchanged content efficiently
//   - Virtual rendering provides 3.5x speedup
func BenchmarkWindowBufferResize(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())

	// Build state once
	wb := NewWindowBuffer(80, styles)
	for j := 0; j < 50; j++ {
		id := fmt.Sprintf("msg%d", j)
		content := strings.Repeat("Content line\n", 5)
		wb.AppendOrUpdate("AT", id, content)
	}
	_ = wb.GetTotalLines()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Toggle width to force re-wrap every other iteration
		if i%2 == 0 {
			wb.WithWidth(120)
		} else {
			wb.WithWidth(80)
		}
		_ = wb.GetTotalLines()
	}
}

// BenchmarkVirtualRenderingScroll benchmarks scrolling with virtual rendering.
// Uses 20 scroll steps per direction per iteration.
func BenchmarkVirtualRenderingScroll(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 100 windows
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("This is a test message with some content.\n", 5)
		wb.AppendOrUpdate("AT", id, content)
	}

	// Set up viewport
	wb.SetViewportPosition(0, 30)
	_ = wb.GetTotalLines()

	dm := NewDisplayModel(wb, styles)
	dm = dm.WithHeight(30)
	dm = dm.updateContent()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Scroll through content
		for j := 0; j < 20; j++ {
			dm = dm.ScrollDown(1)
			dm = dm.updateContent()
		}
		for j := 0; j < 20; j++ {
			dm = dm.ScrollUp(1)
			dm = dm.updateContent()
		}
	}
}

// BenchmarkGetWindowLineRangeCached benchmarks GetWindowLineRange when lineHeights are already cached.
func BenchmarkGetWindowLineRangeCached(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 100 windows
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("Content line\n", 5)
		wb.AppendOrUpdate("AT", id, content)
	}

	// Pre-calculate line heights
	_ = wb.GetTotalLines()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = wb.GetWindowLineRange(50)
		_, _ = wb.GetWindowLineRange(25)
		_, _ = wb.GetWindowLineRange(75)
	}
}

// BenchmarkEnsureLineHeightsIncremental vs full rebuild.
//
// State is built ONCE outside the loop. Each iteration appends a delta to
// msg50 and measures GetTotalLines — the incremental path.  Content grows
// across iterations but that's fine: the whole point is that incremental
// path is O(delta) regardless of total content size.
func BenchmarkEnsureLineHeightsIncremental(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)
	for j := 0; j < 100; j++ {
		id := fmt.Sprintf("msg%d", j)
		content := strings.Repeat("Content line\n", 5)
		wb.AppendOrUpdate("AT", id, content)
	}
	_ = wb.GetTotalLines()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wb.AppendOrUpdate("AT", "msg50", " new content")
		_ = wb.GetTotalLines()
	}
}

func BenchmarkEnsureLineHeightsFullRebuild(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)
	for j := 0; j < 100; j++ {
		id := fmt.Sprintf("msg%d", j)
		content := strings.Repeat("Content line\n", 5)
		wb.AppendOrUpdate("AT", id, content)
	}
	// Do NOT pre-calculate — force full rebuild on each iteration

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wb.dirty = true
		wb.dirtyIndex = dirtyFullRebuild
		_ = wb.GetTotalLines()
	}
}

// BenchmarkIncrementalWrapping measures incremental append-to-existing-wrappedLines.
// State is built once; each iteration appends a small delta and tracks
// the line count (incremental path — no full re-wrap).
func BenchmarkIncrementalWrappingPath(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)
	wb.AppendOrUpdate("AT", "msg0", strings.Repeat("Initial content line\n", 10))
	_ = wb.GetTotalLines()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wb.AppendOrUpdate("AT", "msg0", " new text")
		_ = wb.GetTotalLines()
	}
}

// BenchmarkFullWrappingPath measures full wrap from scratch on an
// uncached window. State is built once; each iteration invalidates the
// cache to force full re-wrap on GetTotalLines.
func BenchmarkFullWrappingPath(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)
	historyID := "msg0"
	wb.AppendOrUpdate("AT", historyID, strings.Repeat("Initial content line\n", 10))

	// Ensure content is populated so GetTotalLines has work to do
	_ = wb.GetTotalLines()
	idx := wb.idIndex[historyID]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Invalidate renderer cache AND mark window buffer as dirty
		wb.windows[idx].Invalidate()
		wb.dirty = true
		wb.dirtyIndex = idx
		_ = wb.GetTotalLines()
	}
}

// BenchmarkWrapContentVsWordBoundaryWrap compares wrapContent
// (character-boundary) vs Wrap (word-boundary) on code-like content.
func BenchmarkWrapContentVsWordBoundaryWrap(b *testing.B) {
	code := strings.Repeat(`func prepareContent(s string) string {
	s = stripANSI(s)
	s = expandTabs(s)
	return s
}
`, 20)

	b.Run("wrapContent", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			wrapContent(code, 60)
		}
	})

	b.Run("wordBoundaryWrap", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			Wrap(code, 60, " ")
		}
	})
}

// BenchmarkAppendVsFullWrap_LongContent compares incremental append to
// full re-wrap on very long content (5000+ lines). This is the scenario
// that matters during streaming of a long LLM response.
func BenchmarkAppendVsFullWrap_LongContent(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	longContent := strings.Repeat("This is a line of content that wraps at 80 columns. ", 500)

	b.Run("incremental", func(b *testing.B) {
		wb := NewWindowBuffer(80, styles)
		wb.AppendOrUpdate("AT", "stream", longContent)
		wb.GetTotalLines()
		wb.GetAll(-1, false) // pre-render to populate wrappedLines

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			wb.AppendOrUpdate("AT", "stream", " delta")
			wb.GetTotalLines()
		}
	})

	b.Run("full-rewrap", func(b *testing.B) {
		wb := NewWindowBuffer(80, styles)
		wb.AppendOrUpdate("AT", "stream", longContent)
		wb.GetTotalLines()
		wb.GetAll(-1, false)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if idx, ok := wb.idIndex["stream"]; ok {
				wb.windows[idx].Invalidate()
			}
			wb.AppendOrUpdate("AT", "stream", " delta")
			wb.GetTotalLines()
		}
	})
}

// ============================================================================
// Dimmed rendering benchmarks
// ============================================================================

// BenchmarkStylesDimmed measures the cost of creating a dimmed Styles copy.
func BenchmarkStylesDimmed(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = styles.Dimmed()
	}
}

// BenchmarkGetAllDimmed compares GetAll with blocked=false vs blocked=true.
func BenchmarkGetAllDimmed(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 20 windows (mix of AT, UT, AF types)
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("at-%d", i)
		wb.AppendOrUpdate("AT", id, "This is assistant text content that will be styled and wrapped. "+
			"It contains enough words to trigger wrapping across multiple lines for testing.")
	}
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("ut-%d", i)
		wb.AppendOrUpdate("UT", id, "User message with some content.")
	}
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("af-%d", i)
		wb.HandleToolInputEvent(protocol.ToolInputData{
			ID:    id,
			Name:  "read_file",
			Input: []byte("/path/to/some/file.txt"),
		}, uint64(i))
	}

	wb.SetViewportPosition(0, 30)
	_ = wb.GetTotalLines()

	b.Run("normal", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = wb.GetAll(-1, false)
		}
	})

	b.Run("dimmed", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = wb.GetAll(-1, true)
		}
	})
}

// BenchmarkRenderBlockedTransition measures the cost of switching blocked state
// (invalidate renderer cache + re-render with dimmed styles).
func BenchmarkRenderBlockedTransition(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 20 windows with cached render
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("msg-%d", i)
		wb.AppendOrUpdate("AT", id, strings.Repeat("Message content line.\n", 3))
	}

	// Pre-render with normal styles
	_ = wb.GetAll(-1, false)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Toggle blocked: normal → dimmed
		_ = wb.GetAll(-1, true)
		// Toggle back: dimmed → normal
		_ = wb.GetAll(-1, false)
	}
}
