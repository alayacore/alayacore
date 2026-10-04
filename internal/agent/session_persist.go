package agent

// Session persistence: saving, loading, and displaying sessions.
//
// The serialization format (markdown + TLV) and low-level I/O are
// owned by persistence.go. This file contains Session-specific wrappers
// that add the session's metadata when saving.

import (
	"fmt"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
)

// ============================================================================
// Load / Save — Session wrappers
// ============================================================================

// loadSession loads a session from a file.
// Delegates to persistenceService for parsing.
func loadSession(path string) (*sessionData, error) {
	return defaultPersistence.loadSession(path)
}

// saveContentToFile saves the current session's contents with its metadata.
//
// Reads ContextTokens, so it must be called from the run() goroutine, which
// owns that field. The task goroutine must use saveContentToFileWithContext
// instead, passing a count it owns — run() writes ContextTokens while the task
// runs, so a task-goroutine read of it is a data race.
func (s *Session) saveContentToFile(path string, contents []llm.ContentPart) error {
	return s.saveContentToFileWithContext(path, contents, s.ContextTokens)
}

// saveContentToFileWithContext is saveContentToFile with an explicit context
// size, for callers that do not own — or must not read — s.ContextTokens.
func (s *Session) saveContentToFileWithContext(path string, contents []llm.ContentPart, contextTokens int64) error {
	reasoningLevel := 0
	videoFPS := 0
	videoRes := 0
	if s.modelService != nil {
		reasoningLevel = s.modelService.reasoningLevel
		videoFPS = s.modelService.videoFPS
		videoRes = s.modelService.videoRes
	}
	meta := sessionMeta{
		CreatedAt:      s.CreatedAt,
		UpdatedAt:      time.Now(),
		ActiveModel:    s.activeModelName(),
		MessageVersion: messageVersion,
		ReasoningLevel: reasoningLevel,
		ContextTokens:  contextTokens,
		VideoFPS:       videoFPS,
		VideoRes:       videoRes,
	}
	if err := defaultPersistence.SaveContentToFile(path, meta, contents); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	return nil
}
