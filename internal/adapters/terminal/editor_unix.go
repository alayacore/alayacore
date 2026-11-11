//go:build !windows

package terminal

// defaultEditors is the list of editor binaries to try when $EDITOR is not set,
// most vi-like first: the platform's `editor` alternative, then vi itself, then
// its BSD descendant, then vim. Ordered by preference per OS.
var defaultEditors = []string{"editor", "vi", "nvi", "vim"}
