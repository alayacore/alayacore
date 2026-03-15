package main

import (
	"fmt"
	"os"

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

	adaptor := terminal.NewTerminalAdaptor(appCfg)
	adaptor.SetSessionFile(appCfg.Cfg.Session)
	adaptor.Start()
}

func printHelp() {
	fmt.Print(`AlayaCore - A minimal AI Agent

Usage:
  alayacore [flags]

Flags:
  model-config string  Model config file path (default: ~/.alayacore/model.conf)
  runtime-config string  Runtime config file path (default: ~/.alayacore/runtime.conf)
  system string      Override system prompt
  skill strings      Skills directory path (can be specified multiple times)
  session string     Session file path to load/save conversations
  proxy string       HTTP proxy URL (supports HTTP, HTTPS, and SOCKS5)
  context-limit string  Provider context window size in tokens (supports K/M suffix, e.g., 200K, 1M)
  debug-api          Write raw API requests and responses to log file
  version            Show version information
  help               Show help information

Examples:
  # Using model config file
  alayacore

  # With optional flags
  alayacore --session ~/mysession.md
  alayacore --skill ./skills1 --skill ./skills2
  alayacore --proxy http://127.0.0.1:7890
  alayacore --model-config ./my-model.conf

`)
}
