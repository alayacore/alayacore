package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/stream"
)

func TestQueueItemUniqueIDs(t *testing.T) {
	// Create a minimal session
	session := &Session{
		taskQueue: make([]QueueItem, 0),
		Input:     &stream.ChanInput{},
		Output:    &MockOutput{},
	}
	session.sessionCtx, session.sessionCancel = context.WithCancel(context.Background())
	session.cond = sync.NewCond(&session.mu)

	// Submit multiple tasks and verify unique IDs
	task1 := UserPrompt{Text: "test prompt 1"}
	task2 := CommandPrompt{Command: "test command"}
	task3 := UserPrompt{Text: "test prompt 2"}

	session.submitTask(task1)
	session.submitTask(task2)
	session.submitTask(task3)

	// Get queue items
	items := session.GetQueueItems()

	if len(items) != 3 {
		t.Errorf("Expected 3 queue items, got %d", len(items))
	}

	// Verify unique IDs
	ids := make(map[string]bool)
	for _, item := range items {
		if ids[item.QueueID] {
			t.Errorf("Duplicate queue ID found: %s", item.QueueID)
		}
		ids[item.QueueID] = true
	}

	// Verify ID format (Q1, Q2, Q3)
	expectedIDs := []string{"Q1", "Q2", "Q3"}
	for i, item := range items {
		if item.QueueID != expectedIDs[i] {
			t.Errorf("Expected queue ID %s, got %s", expectedIDs[i], item.QueueID)
		}
	}
}

func TestDeleteQueueItem(t *testing.T) {
	session := &Session{
		taskQueue: make([]QueueItem, 0),
		Input:     &stream.ChanInput{},
		Output:    &MockOutput{},
	}
	session.sessionCtx, session.sessionCancel = context.WithCancel(context.Background())
	session.cond = sync.NewCond(&session.mu)

	// Submit tasks
	session.submitTask(UserPrompt{Text: "prompt 1"})
	session.submitTask(UserPrompt{Text: "prompt 2"})
	session.submitTask(UserPrompt{Text: "prompt 3"})

	// Delete middle item
	deleted := session.DeleteQueueItem("Q2")
	if !deleted {
		t.Error("Failed to delete queue item Q2")
	}

	// Verify deletion
	items := session.GetQueueItems()
	if len(items) != 2 {
		t.Errorf("Expected 2 items after deletion, got %d", len(items))
	}

	// Verify remaining items
	if items[0].QueueID != "Q1" {
		t.Errorf("Expected first item to be Q1, got %s", items[0].QueueID)
	}
	if items[1].QueueID != "Q3" {
		t.Errorf("Expected second item to be Q3, got %s", items[1].QueueID)
	}

	// Try to delete non-existent item
	deleted = session.DeleteQueueItem("Q999")
	if deleted {
		t.Error("Should not be able to delete non-existent item")
	}
}

func TestQueueItemTypes(t *testing.T) {
	session := &Session{
		taskQueue: make([]QueueItem, 0),
		Input:     &stream.ChanInput{},
		Output:    &MockOutput{},
	}
	session.sessionCtx, session.sessionCancel = context.WithCancel(context.Background())
	session.cond = sync.NewCond(&session.mu)

	// Submit different task types
	promptTask := UserPrompt{Text: "test prompt"}
	commandTask := CommandPrompt{Command: "test command"}

	session.submitTask(promptTask)
	session.submitTask(commandTask)

	items := session.GetQueueItems()

	// Verify task types are preserved
	if len(items) != 2 {
		t.Fatalf("Expected 2 items, got %d", len(items))
	}

	// Check first item is UserPrompt
	if _, ok := items[0].Task.(UserPrompt); !ok {
		t.Error("First item should be UserPrompt")
	}

	// Check second item is CommandPrompt
	if _, ok := items[1].Task.(CommandPrompt); !ok {
		t.Error("Second item should be CommandPrompt")
	}
}

func TestQueueTimestamps(t *testing.T) {
	session := &Session{
		taskQueue: make([]QueueItem, 0),
		Input:     &stream.ChanInput{},
		Output:    &MockOutput{},
	}
	session.sessionCtx, session.sessionCancel = context.WithCancel(context.Background())
	session.cond = sync.NewCond(&session.mu)

	before := time.Now()
	session.submitTask(UserPrompt{Text: "test"})
	after := time.Now()

	items := session.GetQueueItems()

	if len(items) != 1 {
		t.Fatalf("Expected 1 item, got %d", len(items))
	}

	// Verify timestamp is within expected range
	if items[0].CreatedAt.Before(before) || items[0].CreatedAt.After(after) {
		t.Errorf("Timestamp %v is not between %v and %v", items[0].CreatedAt, before, after)
	}
}

func TestCancelAllTasks(t *testing.T) {
	tests := []struct {
		name           string
		inProgress     bool
		queueSize      int
		expectError    bool
		expectMessages int // number of expected notification messages
	}{
		{
			name:           "no task running, empty queue",
			inProgress:     false,
			queueSize:      0,
			expectError:    true,
			expectMessages: 1, // error message
		},
		{
			name:           "task running, empty queue",
			inProgress:     true,
			queueSize:      0,
			expectError:    false,
			expectMessages: 1, // "Canceled current task"
		},
		{
			name:           "no task running, queue has items",
			inProgress:     false,
			queueSize:      3,
			expectError:    false,
			expectMessages: 1, // "Cleared X queued tasks"
		},
		{
			name:           "task running, queue has items",
			inProgress:     true,
			queueSize:      5,
			expectError:    false,
			expectMessages: 1, // "Canceled current task and cleared X queued tasks"
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := &MockOutput{}
			session := &Session{
				taskQueue:  make([]QueueItem, 0),
				runnerDone: make(chan struct{}),
				Input:      &stream.ChanInput{},
				Output:     output,
				inProgress: tt.inProgress,
			}
			session.sessionCtx, session.sessionCancel = context.WithCancel(context.Background())
			session.cond = sync.NewCond(&session.mu)

			// Add mock cancel function if task is in progress
			if tt.inProgress {
				canceled := false
				session.cancelCurrent = func() {
					canceled = true
					// Simulate runTask's taskWg.Done() on cancel
					session.taskWg.Done()
				}
				// Simulate runTask's taskWg.Add(1)
				session.taskWg.Add(1)
				defer func() {
					if !canceled {
						t.Error("Expected cancelCurrent to be called")
					}
				}()
			}

			// Add items to queue
			for i := 0; i < tt.queueSize; i++ {
				session.taskQueue = append(session.taskQueue, QueueItem{
					Task:      UserPrompt{Text: "test"},
					QueueID:   "Q" + string(rune('1'+i)),
					CreatedAt: time.Now(),
				})
			}

			// Execute cancelAllTasks
			session.cancelAllTasks()

			// Verify queue is cleared
			if len(session.taskQueue) != 0 {
				t.Errorf("Expected empty queue, got %d items", len(session.taskQueue))
			}

			// Verify output
			if tt.expectError {
				// Should have error message (TLV format: "SE\x00\x00\x00\x11nothing to cancel")
				found := false
				for _, msg := range output.Messages {
					if strings.Contains(msg, "nothing to cancel") {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected error message, but none found. Messages: %v", output.Messages)
				}
			} else if len(output.Messages) < tt.expectMessages {
				t.Errorf("Expected at least %d message(s), got %d", tt.expectMessages, len(output.Messages))
			}
		})
	}
}

func TestPausedOnError(t *testing.T) {
	session := &Session{
		taskQueue: make([]QueueItem, 0),
		Input:     &stream.ChanInput{},
		Output:    &MockOutput{},
	}
	session.sessionCtx, session.sessionCancel = context.WithCancel(context.Background())
	session.cond = sync.NewCond(&session.mu)

	// Initially not paused
	session.mu.Lock()
	paused := session.pausedOnError
	session.mu.Unlock()
	if paused {
		t.Error("Session should not be paused initially")
	}

	// Set paused on error
	session.mu.Lock()
	session.pausedOnError = true
	session.mu.Unlock()

	// Submit a task with empty queue — should clear the paused flag
	session.submitTask(UserPrompt{Text: "recovery prompt"})

	session.mu.Lock()
	paused = session.pausedOnError
	session.mu.Unlock()
	if paused {
		t.Error("submitTask should clear pausedOnError when queue was empty")
	}

	// Queue should contain the submitted task
	items := session.GetQueueItems()
	if len(items) != 1 {
		t.Errorf("Expected 1 item after submit, got %d", len(items))
	}
}

func TestSubmitTaskFront(t *testing.T) {
	session := &Session{
		taskQueue: make([]QueueItem, 0),
		Input:     &stream.ChanInput{},
		Output:    &MockOutput{},
	}
	session.sessionCtx, session.sessionCancel = context.WithCancel(context.Background())
	session.cond = sync.NewCond(&session.mu)

	// Submit regular tasks
	session.submitTask(UserPrompt{Text: "first"})
	session.submitTask(UserPrompt{Text: "second"})

	// Submit at front (simulates a deferred command like :retry)
	session.enqueueTask(CommandPrompt{Command: commandNameRetry}, true)

	items := session.GetQueueItems()
	if len(items) != 3 {
		t.Fatalf("Expected 3 items, got %d", len(items))
	}

	// Front item should be the command
	if cmd, ok := items[0].Task.(CommandPrompt); !ok || cmd.Command != commandNameRetry {
		t.Errorf("Expected first item to be CommandPrompt{%s}, got %v", commandNameRetry, items[0].Task)
	}
	// Original tasks should follow in order
	if p, ok := items[1].Task.(UserPrompt); !ok || p.Text != "first" {
		t.Errorf("Expected second item to be 'first', got %v", items[1].Task)
	}
	if p, ok := items[2].Task.(UserPrompt); !ok || p.Text != "second" {
		t.Errorf("Expected third item to be 'second', got %v", items[2].Task)
	}

	// enqueueTask should NOT clear pausedOnError (that's handled by specific commands)
	session.mu.Lock()
	session.pausedOnError = true
	session.mu.Unlock()

	session.enqueueTask(CommandPrompt{Command: commandNameRetry}, true)

	session.mu.Lock()
	paused := session.pausedOnError
	session.mu.Unlock()
	if !paused {
		t.Error("enqueueTask should NOT clear pausedOnError")
	}
}

func TestPausedOnErrorBlocksDequeue(t *testing.T) {
	session := &Session{
		taskQueue: make([]QueueItem, 0),
		Input:     &stream.ChanInput{},
		Output:    &MockOutput{},
	}
	session.sessionCtx, session.sessionCancel = context.WithCancel(context.Background())
	session.cond = sync.NewCond(&session.mu)

	// Add a task to the queue
	session.submitTask(UserPrompt{Text: "queued prompt"})

	// Set paused — dequeue should block
	session.mu.Lock()
	session.pausedOnError = true
	session.mu.Unlock()

	// Try to dequeue in a goroutine — it should block
	dequeued := make(chan QueueItem, 1)
	go func() {
		item, ok := session.waitForNextTask()
		if ok {
			dequeued <- item
		}
	}()

	// Give it a moment — should NOT have dequeued
	select {
	case <-dequeued:
		t.Error("waitForNextTask should block when pausedOnError is true")
	case <-time.After(100 * time.Millisecond):
		// expected — blocked
	}

	// Submitting another task should NOT clear the pause (queue is not empty)
	session.submitTask(UserPrompt{Text: "second prompt"})

	// Should still be blocked
	select {
	case <-dequeued:
		t.Error("waitForNextTask should still block when queue is not empty")
	case <-time.After(100 * time.Millisecond):
		// expected — still blocked
	}

	// Clear the pause manually (simulates :continue)
	session.mu.Lock()
	session.pausedOnError = false
	session.cond.Signal()
	session.mu.Unlock()

	// Should now dequeue (the first item)
	select {
	case item := <-dequeued:
		if _, ok := item.Task.(UserPrompt); !ok {
			t.Error("Expected UserPrompt task")
		}
	case <-time.After(2 * time.Second):
		t.Error("waitForNextTask should unblock after pausedOnError is cleared")
	}
}

func TestCommandCanRunWhilePaused(t *testing.T) {
	session := &Session{
		taskQueue: make([]QueueItem, 0),
		Input:     &stream.ChanInput{},
		Output:    &MockOutput{},
	}
	session.sessionCtx, session.sessionCancel = context.WithCancel(context.Background())
	session.cond = sync.NewCond(&session.mu)

	// Add a user prompt to the queue
	session.submitTask(UserPrompt{Text: "queued prompt"})

	// Set paused — user prompts should not dequeue
	session.mu.Lock()
	session.pausedOnError = true
	// Simulate taskRunner state when paused (inProgress stays true)
	session.inProgress = true
	session.mu.Unlock()

	// Add a command to the front of the queue (simulates submitDeferredCommand)
	session.enqueueTask(CommandPrompt{Command: commandNameSave}, true)

	// Try to dequeue in a goroutine — it should succeed even while paused
	dequeued := make(chan QueueItem, 1)
	go func() {
		item, ok := session.waitForNextTask()
		if ok {
			dequeued <- item
		}
	}()

	// Should dequeue the command immediately
	select {
	case item := <-dequeued:
		if cmd, ok := item.Task.(CommandPrompt); !ok || cmd.Command != commandNameSave {
			t.Errorf("Expected CommandPrompt{%s}, got %v", commandNameSave, item.Task)
		}
	case <-time.After(2 * time.Second):
		t.Error("waitForNextTask should dequeue commands even when paused")
	}

	// Paused state should still be set
	session.mu.Lock()
	paused := session.pausedOnError
	session.mu.Unlock()
	if !paused {
		t.Error("pausedOnError should still be true after command runs")
	}
}

func TestCommandBehindUserPromptWhilePaused(t *testing.T) {
	// Test that a command behind a user prompt cannot run while paused
	session := &Session{
		taskQueue: make([]QueueItem, 0),
		Input:     &stream.ChanInput{},
		Output:    &MockOutput{},
	}
	session.sessionCtx, session.sessionCancel = context.WithCancel(context.Background())
	session.cond = sync.NewCond(&session.mu)

	// Add a user prompt to the queue
	session.submitTask(UserPrompt{Text: "first prompt"})

	// Add a command to the back of the queue (after user prompt)
	session.enqueueTask(CommandPrompt{Command: commandNameSave}, false)

	// Set paused — the user prompt at front should block dequeue
	session.mu.Lock()
	session.pausedOnError = true
	session.inProgress = true
	session.mu.Unlock()

	// Try to dequeue in a goroutine — it should block
	dequeued := make(chan QueueItem, 1)
	go func() {
		item, ok := session.waitForNextTask()
		if ok {
			dequeued <- item
		}
	}()

	// Should NOT dequeue because user prompt is at front
	select {
	case <-dequeued:
		t.Error("waitForNextTask should block when user prompt is at front and paused")
	case <-time.After(100 * time.Millisecond):
		// expected — blocked
	}

	// Now add a command to the front
	session.enqueueTask(CommandPrompt{Command: commandNameRetry}, true)

	// Should now dequeue the command at front
	select {
	case item := <-dequeued:
		if cmd, ok := item.Task.(CommandPrompt); !ok || cmd.Command != commandNameRetry {
			t.Errorf("Expected CommandPrompt{%s}, got %v", commandNameRetry, item.Task)
		}
	case <-time.After(2 * time.Second):
		t.Error("waitForNextTask should dequeue command at front")
	}
}

func TestSubmitTaskDoesNotClearPauseWhenQueueNotEmpty(t *testing.T) {
	session := &Session{
		taskQueue: make([]QueueItem, 0),
		Input:     &stream.ChanInput{},
		Output:    &MockOutput{},
	}
	session.sessionCtx, session.sessionCancel = context.WithCancel(context.Background())
	session.cond = sync.NewCond(&session.mu)

	// Add a task to the queue
	session.submitTask(UserPrompt{Text: "first prompt"})

	// Set paused on error
	session.mu.Lock()
	session.pausedOnError = true
	session.mu.Unlock()

	// Submit another task — should NOT clear the paused flag (queue not empty)
	session.submitTask(UserPrompt{Text: "second prompt"})

	session.mu.Lock()
	paused := session.pausedOnError
	queueLen := len(session.taskQueue)
	session.mu.Unlock()

	if !paused {
		t.Error("submitTask should NOT clear pausedOnError when queue is not empty")
	}
	if queueLen != 2 {
		t.Errorf("Expected 2 items in queue, got %d", queueLen)
	}
}
