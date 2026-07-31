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

	// --terseio consumes all of stdin as the prompt, so tool confirmations
	// (answered via subsequent stdin lines) can never be resolved. Fail
	// fast instead of silently declining tools mid-task.
	if cfg.TerseIO && len(cfg.ToolConfirm) > 0 {
		fmt.Fprintln(os.Stderr, "Error: --terseio and --tool-confirm are mutually exclusive: terseio consumes stdin, so tool confirmations cannot be answered. Use --plainio for interactive confirmation.")
		os.Exit(2)
	}

	if cfg.ShowVersion {
		fmt.Printf("alayacore version %s\n", version.Version)
		os.Exit(0)
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
