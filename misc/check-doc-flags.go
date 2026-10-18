//go:build ignore

// check-doc-flags — the flag half of misc/check-docs.sh.
//
// A page that names --terseio is asserting the parser answers to it. Three flags
// did not, for a while: --model-config, --runtime-config and --themes were retired
// when the four config files moved under one --config-path, and configuration.md
// went on listing all three in a migration note — a reader who copies one gets a
// usage error and no config directory.
//
// It asks internal/config rather than reading the source: config.FlagNames builds
// the same set the command line parses, so the list a page is checked against is
// the list the program answers to. A regex over config.go was tried first, and it
// was wrong in the ways a regex over source is always wrong — a flag registered
// with fs.Duration or fs.BoolVar was invisible to it, and fs.Var(makeList("a"),
// "tags") derived a flag called "a". The set is a fact the package owns; this
// program asks for it.
//
// Built and run by check-docs.sh the way check-doc-anchors.go is.
package main

import (
	"fmt"

	"github.com/alayacore/alayacore/internal/config"
)

func main() {
	for _, name := range config.FlagNames() {
		fmt.Println(name)
	}
}
