//go:build ignore

// session_upgrade_v15 converts one old session file — v13 or v14 — to the current
// wire format. It is the engine behind misc/session_upgrade_v15.sh and is not
// part of the build; the conversion itself lives in internal/sessionmigrate, which
// the migration test also exercises.
//
// Usage: go run ./misc/session_upgrade_v15.go <input> <output>
package main

import (
	"fmt"
	"os"

	"github.com/alayacore/alayacore/internal/sessionmigrate"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: session_upgrade_v15 <input> <output>")
		os.Exit(2)
	}
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		fatal(err)
	}
	out, err := sessionmigrate.Migrate(raw)
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(os.Args[2], out, 0o600); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "session_upgrade_v15:", err)
	os.Exit(1)
}
