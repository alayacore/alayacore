package main

import (
	"fmt"
	"os"

	"github.com/alayacore/alayacore/internal/adaptors/plainio"
	"github.com/alayacore/alayacore/internal/adaptors/terminal"
	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/config"
)

func main() {
	cfg := config.Parse()

	if cfg.ShowVersion {
		fmt.Printf("alayacore version %s\n", config.Version)
		os.Exit(0)
	}

	if cfg.ShowHelp {
		printHelp()
		os.Exit(0)
	}

	appCfg, err := app.Setup(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if cfg.PlainIO {
		adaptor := plainio.NewAdaptor(appCfg, cfg.TextOnly)
		os.Exit(adaptor.Start())
	}

	adaptor := terminal.NewAdaptorWithThemes(appCfg, cfg.ThemesFolder)
	adaptor.Start()
}

func printHelp() {
	fmt.Print(`AlayaCore - A minimal AI Agent

Usage:
  alayacore [flags]

Flags:
  --model-config string   Model config file path (default: ~/.alayacore/model.conf)
  --runtime-config string Runtime config file path (default: ~/.alayacore/runtime.conf)
  --system string         Extra system prompt (can be specified multiple times)
  --skill strings         Skill path (can be specified multiple times)
  --session string        Session file path to load/save conversations
  --proxy string          HTTP proxy URL (e.g., http://127.0.0.1:7890 or socks5://127.0.0.1:1080)
  --themes string         Themes folder path (default: ~/.alayacore/themes)
  --max-steps int         Maximum agent loop steps (default: 100)
  --auto-summarize        Automatically summarize conversation when context exceeds 80% of limit
  --plainio               Use plain stdin/stdout mode instead of terminal UI
  --text-only             Only show user/assistant text (requires --plainio)
  --debug-api             Write raw API requests and responses to log file
  --version               Show version information
  --help                  Show help information
`)
}
