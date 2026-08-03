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
func (s *Session) saveContentToFile(path string, contents []llm.ContentPart) error {
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
		ContextTokens:  s.ContextTokens,
		VideoFPS:       videoFPS,
		VideoRes:       videoRes,
	}
	if err := defaultPersistence.SaveContentToFile(path, meta, contents); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	return nil
}
