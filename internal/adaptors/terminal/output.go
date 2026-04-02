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
	"sync"
	"time"

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
	activeModelID     int                  // Current active model ID
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

// SetStyles updates the styles for the output writer
func (to *outputWriter) SetStyles(styles *Styles) {
	to.mu.Lock()
	to.styles = styles
	to.mu.Unlock()
	to.windowBuffer.SetStyles(styles)
}

// Close stops the background goroutine and cleans up resources
func (to *outputWriter) Close() error {
	close(to.done)
	return nil
}

// updateFlusher periodically flushes pending updates
func (to *outputWriter) updateFlusher() {
	ticker := time.NewTicker(FlusherInterval)
	defer ticker.Stop()

	for {
		select {
		case <-to.done:
			return
		case <-ticker.C:
			to.updateMu.Lock()
			if to.pendingUpdate && time.Since(to.lastUpdate) >= UpdateThrottleInterval {
				to.pendingUpdate = false
				to.lastUpdate = time.Now()
				select {
				case to.updateChan <- struct{}{}:
				default:
				}
			}
			to.updateMu.Unlock()
		}
	}
}

func (to *outputWriter) Write(p []byte) (n int, err error) {
	to.mu.Lock()
	to.buffer = append(to.buffer, p...)
	to.processBuffer()
	to.mu.Unlock()
	return len(p), nil
}

func (to *outputWriter) WriteString(s string) (int, error) {
	return to.Write([]byte(s))
}

func (to *outputWriter) Flush() error {
	return nil
}

// AppendError adds an error message to the display buffer with error styling
func (to *outputWriter) AppendError(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	id := to.generateWindowID()
	to.windowBuffer.AppendOrUpdate(id, stream.TagSystemError, to.styles.Error.Render(msg))
}

// WriteNotify writes a notification message to the display
func (to *outputWriter) WriteNotify(msg string) {
	id := to.generateWindowID()
	to.windowBuffer.AppendOrUpdate(id, stream.TagSystemNotify, to.styles.System.Render(msg))
	to.triggerUpdateForTag(stream.TagSystemNotify)
}

// processBuffer parses TLV-encoded data from the buffer
func (to *outputWriter) processBuffer() {
	for len(to.buffer) >= 6 {
		tag := string(to.buffer[0:2])
		length := int(binary.BigEndian.Uint32(to.buffer[2:6]))

		if len(to.buffer) < 6+length {
			break
		}

		value := string(to.buffer[6 : 6+length])
		to.writeColored(tag, value)
		to.buffer = to.buffer[6+length:]
	}
}

// writeColored writes styled content based on the TLV tag
func (to *outputWriter) writeColored(tag string, value string) {
	to.triggerUpdateForTag(tag)

	switch tag {
	// Text content tags (delta messages with stream ID prefix)
	case stream.TagTextAssistant, stream.TagTextReasoning:
		id, content, ok := ParseStreamID(value)
		if !ok {
			// Should not happen, but fallback
			id = to.generateWindowID()
			content = value
		}
		// Pass raw content - styling is applied during render
		to.windowBuffer.AppendOrUpdate(id, tag, content)

	// Function call (JSON: id, name, input)
	case stream.TagFunctionCall:
		var tc ToolCallData
		if err := json.Unmarshal([]byte(value), &tc); err != nil {
			return
		}
		handler := GetHandler(tc.Name)
		formatted := handler.FormatCall(json.RawMessage(tc.Input), to.styles)

		// Pass formatted but unstyled content - styling is applied during render
		to.windowBuffer.AppendToolCall(tc.ID, tc.Name, formatted)

	// Function result (JSON: id, output)
	case stream.TagFunctionResult:
		var tr ToolResultData
		if err := json.Unmarshal([]byte(value), &tr); err != nil {
			return
		}
		handler := to.windowBuffer.GetHandler(tr.ID)
		if handler != nil && !handler.ShouldShowOutput() {
			// Skip output for tools that don't show it
			return
		}
		// Pass raw output - styling is applied during render
		to.windowBuffer.AppendOrUpdate(tr.ID, tag, tr.Output)

	// Function output status indicator
	case stream.TagFunctionState:
		id, content, ok := ParseStreamID(value)
		if !ok {
			return
		}
		// Update the tool window with status indicator
		to.windowBuffer.UpdateToolStatus(id, ParseToolStatus(content))

	// System tags
	case stream.TagSystemError:
		id := to.generateWindowID()
		// Pass raw value - styling is applied during render
		to.windowBuffer.AppendOrUpdate(id, tag, value)

	case stream.TagSystemNotify:
		id := to.generateWindowID()
		// Pass raw value - styling is applied during render
		to.windowBuffer.AppendOrUpdate(id, tag, value)

	case stream.TagSystemData:
		to.handleSystemTag(value)
		return

	// User text tag
	case stream.TagTextUser:
		id := to.generateWindowID()
		// Pass raw value - styling is applied during render
		to.windowBuffer.AppendOrUpdate(id, tag, value)

	default:
		id := to.generateWindowID()
		to.windowBuffer.AppendOrUpdate(id, tag, value)
	}
}

// triggerUpdateForTag sends an update signal for tags that modify the display
// Uses throttling to batch rapid updates together
func (to *outputWriter) triggerUpdateForTag(tag string) {
	switch tag {
	// Text content tags
	case stream.TagTextAssistant, stream.TagTextReasoning, stream.TagTextUser,
		stream.TagFunctionCall,
		// System tags
		stream.TagSystemError, stream.TagSystemNotify, stream.TagSystemData:
		to.updateMu.Lock()
		defer to.updateMu.Unlock()

		// If enough time has passed since last update, send immediately
		if time.Since(to.lastUpdate) >= UpdateThrottleInterval {
			to.lastUpdate = time.Now()
			to.pendingUpdate = false
			select {
			case to.updateChan <- struct{}{}:
			default:
			}
		} else {
			// Mark that we have a pending update
			to.pendingUpdate = true
		}
	}
}

// handleSystemTag processes system information tags
func (to *outputWriter) handleSystemTag(value string) {
	// Try to parse as SystemInfo
	var info agentpkg.SystemInfo
	if err := json.Unmarshal([]byte(value), &info); err == nil {
		// Save step info when task completes (transition from in-progress to done)
		if to.inProgress && !info.InProgress && to.maxSteps > 0 {
			to.lastCurrentStep = to.currentStep
			to.lastMaxSteps = to.maxSteps
		}
		// Reset last step info when new task starts (transition from not-in-progress to in-progress)
		if !to.inProgress && info.InProgress {
			to.lastCurrentStep = 0
			to.lastMaxSteps = 0
		}

		to.inProgress = info.InProgress
		to.queueCount = len(info.QueueItems)
		if info.ContextLimit > 0 {
			pct := float64(info.ContextTokens) * 100.0 / float64(info.ContextLimit)
			to.status = fmt.Sprintf("Context: %d/%d (%.1f%%)", info.ContextTokens, info.ContextLimit, pct)
		} else {
			to.status = fmt.Sprintf("Context: %d", info.ContextTokens)
		}
		// Store model info
		to.models = info.Models
		to.activeModelID = info.ActiveModelID
		to.hasModels = info.HasModels
		to.modelConfigPath = info.ModelConfigPath
		to.activeModelName = info.ActiveModelName

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
		to.pendingQueueItems = items

		// Store step info
		to.currentStep = info.CurrentStep
		to.maxSteps = info.MaxSteps

		// Signal update so tick handler picks up changes
		select {
		case to.updateChan <- struct{}{}:
		default:
		}
	}
}

// SnapshotStatus returns a consistent point-in-time view of session status.
// All fields are read under a single lock, preventing torn reads.
func (to *outputWriter) SnapshotStatus() StatusSnapshot {
	to.mu.Lock()
	defer to.mu.Unlock()
	return StatusSnapshot{
		ContextStatus:   to.status,
		QueueCount:      to.queueCount,
		InProgress:      to.inProgress,
		CurrentStep:     to.currentStep,
		MaxSteps:        to.maxSteps,
		LastCurrentStep: to.lastCurrentStep,
		LastMaxSteps:    to.lastMaxSteps,
	}
}

// SnapshotModels returns a consistent point-in-time view of model state.
// All fields are read under a single lock, preventing torn reads.
func (to *outputWriter) SnapshotModels() ModelSnapshot {
	to.mu.Lock()
	defer to.mu.Unlock()
	return ModelSnapshot{
		Models:     to.models,
		ActiveID:   to.activeModelID,
		ActiveName: to.activeModelName,
		HasModels:  to.hasModels,
		ConfigPath: to.modelConfigPath,
	}
}

// GetQueueItems returns and clears the pending queue items
func (to *outputWriter) GetQueueItems() []QueueItem {
	to.mu.Lock()
	defer to.mu.Unlock()
	items := to.pendingQueueItems
	to.pendingQueueItems = nil
	return items
}

// generateWindowID returns a unique window ID for non-delta messages.
func (to *outputWriter) generateWindowID() string {
	to.nextWindowID++
	return fmt.Sprintf("win%d", to.nextWindowID)
}

// SetWindowWidth updates the window buffer width.
func (to *outputWriter) SetWindowWidth(width int) {
	to.windowBuffer.SetWidth(width)
}
