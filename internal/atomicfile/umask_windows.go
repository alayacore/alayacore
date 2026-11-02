//go:build windows

package atomicfile

import "os"

// currentUmask returns 0: Windows has no umask, so os.WriteFile applied perm
// verbatim and a new file must not be narrowed either.
func currentUmask() os.FileMode { return 0 }
