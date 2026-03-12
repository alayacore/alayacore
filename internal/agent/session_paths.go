package agent

import (
	"os/user"
	"path/filepath"
	"strings"
)

// ============================================================================
// Path Utilities
// ============================================================================

func expandPath(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	usr, err := user.Current()
	if err != nil {
		return path
	}
	if path == "~" {
		return usr.HomeDir
	}
	return filepath.Join(usr.HomeDir, path[1:])
}
