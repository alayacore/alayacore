//go:build windows

package tools

import "os"

// readUmask returns 0: Windows has no umask, so os.WriteFile applied the mode
// verbatim and newFilePerm must not narrow anything either.
func readUmask() os.FileMode { return 0 }
