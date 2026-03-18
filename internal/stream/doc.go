// Package stream provides the minimal IO abstraction and TLV encoding
// used between adaptors (terminal/websocket) and the core session.
//
// The stream package defines a simple Input/Output pair plus helpers
// for reading/writing framed Tag-Length-Value (TLV) messages.
//
// TLV Protocol:
//
//	Messages are encoded as:
//	  [2-byte tag][4-byte length (big-endian)][value bytes]
//
//	Tag values are 2-character strings:
//	  - TagTextUser (TU): User text input
//	  - TagTextAssistant (TA): Assistant text output
//	  - TagTextReasoning (TR): Reasoning/thinking content
//	  - TagFunctionNotify (FN): Function call for display
//	  - TagFunctionCall (FC): Function call for session saving
//	  - TagFunctionResult (FR): Function result for session saving
//	  - TagFunctionState (FS): Function state indicator (pending/success/error)
//	  - TagSystemError (SE): System error messages
//	  - TagSystemNotify (SN): System notifications
//	  - TagSystemData (SD): System data (JSON)
//
// State Indicators:
//
// The TagFunctionState tag is used to display state indicators for tool calls:
//   - "pending": Tool is currently executing (· dot)
//   - "success": Tool executed successfully (✓ checkmark)
//   - "error": Tool execution failed (✗ cross)
//
// Key Types:
//
//   - ChanInput: Input implementation using a channel of TLV messages
//   - Input: Interface for reading bytes
//   - Output: Interface for writing bytes with Flush
//
// Usage:
//
//	// Create input channel
//	input := stream.NewChanInput(10)
//
//	// Emit a TLV message
//	input.EmitTLV(stream.TagTextUser, "Hello, AI!")
//
//	// Read TLV from session
//	tag, value, err := stream.ReadTLV(input)
//
//	// Write TLV to output
//	stream.WriteTLV(output, stream.TagTextAssistant, "Hello, human!")
package stream
