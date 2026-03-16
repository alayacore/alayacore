// Package errors provides domain-specific error types for AlayaCore.
//
// The errors package defines structured error types that provide context
// about what operation failed, making error handling more consistent and
// enabling better error matching with errors.Is and errors.As.
//
// Error Types:
//
//   - SessionError: Base error type with operation context
//   - Op: The operation that failed (e.g., "model_set", "save")
//   - Err: The underlying error
//
// Domain Errors:
//
//	Model errors:
//	  - ErrModelNotFound: Requested model not found
//	  - ErrModelManagerNotInitialized: Model manager not ready
//	  - ErrNoModelFilePath: No model config file path
//
//	Queue errors:
//	  - ErrQueueItemNotFound: Queue item not found
//
//	Session errors:
//	  - ErrNoSessionFile: No session file set
//	  - ErrFailedToSaveSession: Failed to save session
//
//	Command errors:
//	  - ErrEmptyCommand: Empty command received
//	  - ErrUnknownCommand: Unknown command
//	  - ErrNothingToCancel: Nothing to cancel
//
// Input errors:
//   - ErrInvalidInputTag: Invalid TLV tag
//
// Helper Functions:
//
//   - NewSessionError(op, err): Create new error with operation
//   - NewSessionErrorf(op, format, args...): Create with formatted message
//   - Wrap(op, err): Wrap existing error with operation
//   - Wrapf(op, err, format, args...): Wrap with formatted message
//
// Usage:
//
//	// Check for specific error
//	if errors.Is(err, errors.ErrModelNotFound) {
//	    // Handle model not found
//	}
//
//	// Create new error
//	err := errors.NewSessionErrorf("model_set", "model %s not found", id)
//
//	// Wrap existing error
//	err := errors.Wrap("save", originalErr)
package errors
