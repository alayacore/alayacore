package terminal

// ANSI STYLING GOTCHA:
// ANSI escape sequences are NOT recursive. When styling text with lipgloss (or any
// ANSI styling), each segment must be rendered individually before concatenation.
// You cannot render a string that already contains ANSI codes with a new style and
// expect it to work - the outer styling will not wrap the inner styled segments.
// Always render segments separately, then join them.

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
	"github.com/alayacore/alayacore/internal/stream"
)

// outputWriter parses TLV from the session and writes styled content to the WindowBuffer.
// It implements io.Writer for the agent/session output stream.
type outputWriter struct {
	windowBuffer      *WindowBuffer
	buffer            []byte
	mu                sync.Mutex
	updateChan        chan struct{}
	done              chan struct{}        // Signal goroutine to stop
	status            string               // Status bar content from TagSystem
	inProgress        bool                 // Whether session has task in progress
	styles            *Styles              // UI styles
	nextWindowID      int                  // Monotonic counter for generating window IDs
	pendingUpdate     bool                 // Whether there's a pending update to flush
	lastUpdate        time.Time            // Last time an update was sent
	updateMu          sync.Mutex           // Mutex for update throttling
	models            []agentpkg.ModelInfo // Current model list
	activeModelID     string               // Current active model ID
	hasModels         bool                 // Whether models are configured
	modelConfigPath   string               // Path to model.conf
	activeModelName   string               // Name of active model
	pendingQueueItems []QueueItem          // Queue items from taskqueue_get_all
	queueCount        int                  // Number of items in the queue
	currentStep       int                  // Current step in agent loop (1-indexed)
	maxSteps          int                  // Maximum steps allowed
	lastCurrentStep   int                  // Last step reached in completed task
	lastMaxSteps      int                  // Last max steps from completed task
}

func NewTerminalOutput(styles *Styles) *outputWriter { //nolint:revive // tests need access to internal methods
	to := &outputWriter{
		windowBuffer: NewWindowBuffer(DefaultWidth, styles),
		updateChan:   make(chan struct{}, 1),
		done:         make(chan struct{}),
		styles:       styles,
		lastUpdate:   time.Now(),
	}
	// Start background update flusher
	go to.updateFlusher()
	return to
}

// Close stops the background goroutine and cleans up resources
func (w *outputWriter) Close() error {
	close(w.done)
	return nil
}

// updateFlusher periodically flushes pending updates
func (w *outputWriter) updateFlusher() {
	ticker := time.NewTicker(FlusherInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
			w.updateMu.Lock()
			if w.pendingUpdate && time.Since(w.lastUpdate) >= UpdateThrottleInterval {
				w.pendingUpdate = false
				w.lastUpdate = time.Now()
				select {
				case w.updateChan <- struct{}{}:
				default:
				}
			}
			w.updateMu.Unlock()
		}
	}
}

func (w *outputWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	w.buffer = append(w.buffer, p...)
	w.processBuffer()
	w.mu.Unlock()
	return len(p), nil
}

func (w *outputWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *outputWriter) Flush() error {
	return nil
}

// AppendError adds an error message to the display buffer with error styling
func (w *outputWriter) AppendError(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	id := w.generateWindowID()
	w.windowBuffer.AppendOrUpdate(id, stream.TagSystemError, w.styles.Error.Render(msg))
}

// WriteNotify writes a notification message to the display
func (w *outputWriter) WriteNotify(msg string) {
	id := w.generateWindowID()
	w.windowBuffer.AppendOrUpdate(id, stream.TagSystemNotify, w.styles.System.Render(msg))
	w.triggerUpdateForTag(stream.TagSystemNotify)
}

// processBuffer parses TLV-encoded data from the buffer
func (w *outputWriter) processBuffer() {
	for len(w.buffer) >= 6 {
		tag := string(w.buffer[0:2])
		length := int(binary.BigEndian.Uint32(w.buffer[2:6]))

		if len(w.buffer) < 6+length {
			break
		}

		value := string(w.buffer[6 : 6+length])
		w.writeColored(tag, value)
		w.buffer = w.buffer[6+length:]
	}
}

// writeColored writes styled content based on the TLV tag
func (w *outputWriter) writeColored(tag string, value string) {
	w.triggerUpdateForTag(tag)

	output := func(style lipgloss.Style, text string) string {
		return w.renderMultiline(style, text, false)
	}

	switch tag {
	// Text content tags (delta messages with stream ID prefix)
	case stream.TagTextAssistant, stream.TagTextReasoning:
		id, content, ok := ParseStreamID(value)
		if !ok {
			// Should not happen, but fallback
			id = w.generateWindowID()
			content = value
		}
		var styled string
		switch tag {
		case stream.TagTextAssistant:
			styled = output(w.styles.Text, content)
		case stream.TagTextReasoning:
			styled = output(w.styles.Reasoning, content)
		}
		w.windowBuffer.AppendOrUpdate(id, tag, styled)

	// Function call (JSON: id, name, input)
	case stream.TagFunctionCall:
		var tc ToolCallData
		if err := json.Unmarshal([]byte(value), &tc); err != nil {
			return
		}
		handler := GetHandler(tc.Name)
		formatted := handler.FormatCall(json.RawMessage(tc.Input), w.styles)

		// Check if this is a write_file with path and content
		if wfPath, wfContent, ok := parseWriteFileFromFormatted(formatted); ok {
			w.windowBuffer.AppendWriteFile(tc.ID, wfPath, wfContent)
			return
		}
		// Check if this is an edit_file with raw diff data
		if diffPath, diffLines := parseDiffFromFormatted(formatted); diffLines != nil {
			w.windowBuffer.AppendDiff(tc.ID, diffPath, diffLines)
			return
		}
		styled := ColorizeTool(formatted, w.styles)
		w.windowBuffer.AppendToolCall(tc.ID, tc.Name, styled)

	// Function result (JSON: id, output)
	case stream.TagFunctionResult:
		var tr ToolResultData
		if err := json.Unmarshal([]byte(value), &tr); err != nil {
			return
		}
		handler := w.windowBuffer.GetHandler(tr.ID)
		if handler != nil && !handler.ShouldShowOutput() {
			// Skip output for tools that don't show it
			return
		}
		styled := output(w.styles.Text, tr.Output)
		w.windowBuffer.AppendOrUpdate(tr.ID, tag, styled)

	// Function output status indicator
	case stream.TagFunctionState:
		id, content, ok := ParseStreamID(value)
		if !ok {
			return
		}
		// Update the tool window with status indicator
		w.windowBuffer.UpdateToolStatus(id, ParseToolStatus(content))

	// System tags
	case stream.TagSystemError:
		id := w.generateWindowID()
		styled := output(w.styles.Error, value)
		w.windowBuffer.AppendOrUpdate(id, tag, styled)

	case stream.TagSystemNotify:
		id := w.generateWindowID()
		styled := output(w.styles.System, value)
		w.windowBuffer.AppendOrUpdate(id, tag, styled)

	case stream.TagSystemData:
		w.handleSystemTag(value)
		return

	// User text tag
	case stream.TagTextUser:
		id := w.generateWindowID()
		styled := strings.TrimRight(w.styles.Prompt.Render("> ")+w.styles.UserInput.Render(value), " ")
		w.windowBuffer.AppendOrUpdate(id, tag, styled)

	default:
		id := w.generateWindowID()
		w.windowBuffer.AppendOrUpdate(id, tag, value)
	}
}

// triggerUpdateForTag sends an update signal for tags that modify the display
// Uses throttling to batch rapid updates together
func (w *outputWriter) triggerUpdateForTag(tag string) {
	switch tag {
	// Text content tags
	case stream.TagTextAssistant, stream.TagTextReasoning, stream.TagTextUser,
		stream.TagFunctionCall,
		// System tags
		stream.TagSystemError, stream.TagSystemNotify, stream.TagSystemData:
		w.updateMu.Lock()
		defer w.updateMu.Unlock()

		// If enough time has passed since last update, send immediately
		if time.Since(w.lastUpdate) >= UpdateThrottleInterval {
			w.lastUpdate = time.Now()
			w.pendingUpdate = false
			select {
			case w.updateChan <- struct{}{}:
			default:
			}
		} else {
			// Mark that we have a pending update
			w.pendingUpdate = true
		}
	}
}

// handleSystemTag processes system information tags
func (w *outputWriter) handleSystemTag(value string) {
	// Try to parse as SystemInfo
	var info agentpkg.SystemInfo
	if err := json.Unmarshal([]byte(value), &info); err == nil {
		// Save step info when task completes (transition from in-progress to done)
		if w.inProgress && !info.InProgress && w.maxSteps > 0 {
			w.lastCurrentStep = w.currentStep
			w.lastMaxSteps = w.maxSteps
		}
		// Reset last step info when new task starts (transition from not-in-progress to in-progress)
		if !w.inProgress && info.InProgress {
			w.lastCurrentStep = 0
			w.lastMaxSteps = 0
		}

		w.inProgress = info.InProgress
		w.queueCount = len(info.QueueItems)
		if info.ContextLimit > 0 {
			pct := float64(info.ContextTokens) * 100.0 / float64(info.ContextLimit)
			w.status = fmt.Sprintf("Context: %d/%d (%.1f%%)", info.ContextTokens, info.ContextLimit, pct)
		} else {
			w.status = fmt.Sprintf("Context: %d", info.ContextTokens)
		}
		// Store model info
		w.models = info.Models
		w.activeModelID = info.ActiveModelID
		w.hasModels = info.HasModels
		w.modelConfigPath = info.ModelConfigPath
		w.activeModelName = info.ActiveModelName

		// Store queue items (always update, even if empty)
		items := make([]QueueItem, len(info.QueueItems))
		for i, item := range info.QueueItems {
			createdAt, err := time.Parse(time.RFC3339, item.CreatedAt)
			if err != nil {
				createdAt = time.Now()
			}
			items[i] = QueueItem{
				QueueID:   item.QueueID,
				Type:      item.Type,
				Content:   item.Content,
				CreatedAt: createdAt,
			}
		}
		w.pendingQueueItems = items

		// Store step info
		w.currentStep = info.CurrentStep
		w.maxSteps = info.MaxSteps

		// Signal update so tick handler picks up changes
		select {
		case w.updateChan <- struct{}{}:
		default:
		}
	}
}

// GetQueueItems returns and clears the pending queue items
func (w *outputWriter) GetQueueItems() []QueueItem {
	w.mu.Lock()
	defer w.mu.Unlock()
	items := w.pendingQueueItems
	w.pendingQueueItems = nil
	return items
}

// GetModels returns the current model list
func (w *outputWriter) GetModels() []agentpkg.ModelInfo {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.models
}

// GetActiveModelID returns the current active model ID
func (w *outputWriter) GetActiveModelID() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.activeModelID
}

// HasModels returns whether models are configured
func (w *outputWriter) HasModels() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.hasModels
}

// GetModelConfigPath returns the path to the model config file
func (w *outputWriter) GetModelConfigPath() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.modelConfigPath
}

// GetActiveModelName returns the name of the active model
func (w *outputWriter) GetActiveModelName() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.activeModelName
}

// GetQueueCount returns the current number of queued items
func (w *outputWriter) GetQueueCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.queueCount
}

// GetStatus returns the current status string
func (w *outputWriter) GetStatus() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

// IsInProgress returns whether the session has a task in progress
func (w *outputWriter) IsInProgress() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.inProgress
}

// GetCurrentStep returns the current step in the agent loop
func (w *outputWriter) GetCurrentStep() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.currentStep
}

// GetMaxSteps returns the maximum steps allowed
func (w *outputWriter) GetMaxSteps() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.maxSteps
}

// GetLastStepInfo returns the last step info from a completed task
func (w *outputWriter) GetLastStepInfo() (currentStep, maxSteps int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastCurrentStep, w.lastMaxSteps
}

// renderMultiline applies a style to each line of text
func (w *outputWriter) renderMultiline(style lipgloss.Style, value string, trimRight bool) string {
	// Expand tabs BEFORE styling to ensure correct column counting
	value = expandTabs(value)

	lines := strings.Split(value, "\n")
	for i, line := range lines {
		rendered := style.Render(line)
		if trimRight {
			rendered = strings.TrimRight(rendered, " ")
		}
		lines[i] = rendered
	}
	return strings.Join(lines, "\n")
}

// generateWindowID returns a unique window ID for non-delta messages.
func (w *outputWriter) generateWindowID() string {
	w.nextWindowID++
	return fmt.Sprintf("win%d", w.nextWindowID)
}

// SetWindowWidth updates the window buffer width.
func (w *outputWriter) SetWindowWidth(width int) {
	w.windowBuffer.SetWidth(width)
}
