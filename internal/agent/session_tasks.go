package agent

import (
	"context"
	"fmt"
	"time"

	"charm.land/fantasy"
)

func (s *Session) submitTask(task Task) {
	s.mu.Lock()
	queueLen := len(s.taskQueue)
	// Check queue capacity (max 10 tasks)
	if queueLen >= 10 {
		s.mu.Unlock()
		s.writeNotify("Busy. Cannot queue, try again shortly.")
		return
	}

	// Generate unique ID for the task
	s.nextQueueID++
	queueID := fmt.Sprintf("Q%d", s.nextQueueID)

	// Set queue ID on the task based on its type
	switch t := task.(type) {
	case UserPrompt:
		t.queueID = queueID
		task = t
	case CommandPrompt:
		t.queueID = queueID
		task = t
	}

	// Create queue item
	item := QueueItem{
		Task:      task,
		QueueID:   queueID,
		CreatedAt: time.Now(),
	}

	s.taskQueue = append(s.taskQueue, item)
	inProgress := s.inProgress
	s.signalTaskAvailable()
	s.mu.Unlock()

	if inProgress {
		s.writeNotify("Queued. Previous task in progress. Will run after completion.")
		s.sendSystemInfo()
	}
}

// signalTaskAvailable notifies the task runner that a new task is available
func (s *Session) signalTaskAvailable() {
	select {
	case s.taskAvailable <- struct{}{}:
	default:
	}
}

func (s *Session) taskRunner() {
	for {
		task, ok := s.waitForNextTask()
		if !ok {
			return
		}
		s.setInProgress(true)
		s.runTask(task)
		s.setInProgress(s.hasQueuedTasks())
	}
}

func (s *Session) waitForNextTask() (QueueItem, bool) {
	for {
		s.mu.Lock()
		if len(s.taskQueue) > 0 {
			item := s.taskQueue[0]
			s.taskQueue = s.taskQueue[1:]
			s.mu.Unlock()
			return item, true
		}
		s.mu.Unlock()
		select {
		case <-s.taskAvailable:
			// continue
		case <-s.done:
			return QueueItem{}, false
		}
	}
}

func (s *Session) hasQueuedTasks() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.taskQueue) > 0
}

func (s *Session) setInProgress(v bool) {
	s.mu.Lock()
	changed := s.inProgress != v
	s.inProgress = v
	s.mu.Unlock()
	if changed {
		s.sendSystemInfo()
	}
}

func (s *Session) runTask(item QueueItem) {
	s.sendSystemInfo()

	// Lazily initialize the agent if not already done
	errMsg := s.ensureAgentInitialized()
	if errMsg != "" {
		s.writeError(errMsg)
		s.sendSystemInfo()
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancelCurrent = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.cancelCurrent = nil
		s.mu.Unlock()
	}()

	task := item.Task
	switch t := task.(type) {
	case UserPrompt:
		s.signalPromptStart(t.Text)
		s.handleUserPrompt(ctx, t.Text)
	case CommandPrompt:
		s.signalCommandStart(t.Command)
		s.handleCommandSync(ctx, t.Command)
	}

	if ctx.Err() == context.Canceled {
		s.appendCancelMessage()
	}
}

func (s *Session) appendCancelMessage() {
	if len(s.Messages) == 0 {
		return
	}
	if s.Messages[len(s.Messages)-1].Role == fantasy.MessageRoleUser {
		s.Messages = append(s.Messages, fantasy.Message{
			Role:    fantasy.MessageRoleAssistant,
			Content: []fantasy.MessagePart{fantasy.TextPart{Text: "The user canceled."}},
		})
	}
}

// GetQueueItems returns all queued items
func (s *Session) GetQueueItems() []QueueItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]QueueItem, len(s.taskQueue))
	copy(items, s.taskQueue)
	return items
}

// DeleteQueueItem removes a queue item by ID and sends a notification
func (s *Session) DeleteQueueItem(queueID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, item := range s.taskQueue {
		if item.QueueID == queueID {
			// Remove the item
			s.taskQueue = append(s.taskQueue[:i], s.taskQueue[i+1:]...)
			// Send notification
			s.writeNotify(fmt.Sprintf("Queue item %s deleted", queueID))
			return true
		}
	}
	return false
}
