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
