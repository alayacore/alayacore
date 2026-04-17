package config

// Version is the current application version.
// Hard-coded here as a default; overridden at build time via:
//
//	go build -ldflags "-X github.com/alayacore/alayacore/internal/config.Version=$(git describe --tags --always)"
//
// Must be a var (not const) so the linker can patch it.
var Version = "(devel)"
