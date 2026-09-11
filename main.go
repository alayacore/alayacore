package main

import (
	"fmt"
	"os"

	"github.com/alayacore/alayacore/internal/adapters/plainio"
	"github.com/alayacore/alayacore/internal/adapters/rawio"
	"github.com/alayacore/alayacore/internal/adapters/terminal"
	"github.com/alayacore/alayacore/internal/adapters/terseio"
	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/tools"
	"github.com/alayacore/alayacore/internal/version"
)

func main() {
	cfg := config.Parse()

	// --version is a read-only informational flag: report and exit before
	// validation runs. Two reasons. A conflicting or out-of-range flag must not
	// turn a version request into an error, and Validate has a filesystem side
	// effect — it creates the --debug-log directory — which must not happen for
	// a command whose only job is to print a string.
	if cfg.ShowVersion {
		fmt.Printf("alayacore version %s\n", version.Version)
		os.Exit(0)
	}

	// Cross-flag consistency and value ranges. Every failure here is a
	// settings combination that cannot do what the user meant, so it is
	// reported and aborts startup rather than being silently ignored —
	// see config.Validate for the individual cases.
	if err := config.Validate(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(2)
	}

	appCfg, err := app.Setup(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var adapter app.Adapter
	switch {
	case cfg.RawIO:
		adapter = rawio.NewAdapter(appCfg)
	case cfg.TerseIO:
		adapter = terseio.NewAdapter(appCfg)
	case cfg.PlainIO:
		adapter = plainio.NewAdapter(appCfg)
	default:
		adapter = terminal.NewAdapter(appCfg)
	}

	exitCode := adapter.Start()

	// Clean up this process's temporary files under os.TempDir().
	tools.Cleanup()

	// Clean up MCP server connections (before os.Exit, which skips defers).
	// MCPInit.Manager() is always safe to call — it returns the manager even
	// before init completes, so we can close whatever connections exist.
	if appCfg.MCPInit != nil {
		appCfg.MCPInit.Manager().CloseAll()
	}

	os.Exit(exitCode)
}
