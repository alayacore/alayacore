//go:build ignore

// session_upgrade_v15 converts one v14 session file to the v15 wire format — one
// frame per line, no length field. It is the engine behind
// misc/session_upgrade_v15.sh and is not part of the build; the conversion itself
// lives in internal/sessionmigrate, which the migration test also exercises.
//
// A v13 file is not this step's job: it goes to v14 first, with
// misc/session_upgrade_v14.sh.
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
	out, err := sessionmigrate.V14ToV15(raw)
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
