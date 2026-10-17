//go:build ignore

// session_upgrade_v16 converts one v15 session file to v16. It is the engine
// behind misc/session_upgrade_v16.sh and is not part of the build; the
// conversion itself lives in internal/sessionmigrate, which the migration test
// also exercises.
//
// v16 renamed vocabulary and no framing: the body comes through unchanged and
// only the frontmatter's message_version moves. A v14 file is not this step's
// job — it goes to v15 first, with misc/session_upgrade_v15.sh.
//
// Usage: go run ./misc/session_upgrade_v16.go <input> <output>
package main

import (
	"fmt"
	"os"

	"github.com/alayacore/alayacore/internal/sessionmigrate"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: session_upgrade_v16 <input> <output>")
		os.Exit(2)
	}
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		fatal(err)
	}
	out, err := sessionmigrate.V15ToV16(raw)
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(os.Args[2], out, 0o600); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "session_upgrade_v16:", err)
	os.Exit(1)
}
