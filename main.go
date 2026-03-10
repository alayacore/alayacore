package main

import (
	"fmt"
	"os"

	"github.com/wallacegibbon/coreclaw/internal/adaptors/terminal"
	"github.com/wallacegibbon/coreclaw/internal/app"
	"github.com/wallacegibbon/coreclaw/internal/config"
)

func main() {
	cfg := config.Parse()

	if cfg.ShowVersion {
		fmt.Printf("coreclaw version %s\n", config.Version)
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
	fmt.Print(`CoreClaw - A minimal AI Agent

Usage:
  coreclaw [flags]

Flags:
  -type string        Provider type: anthropic, openai (required)
  -base-url string    API endpoint URL (required)
  -api-key string     API key for the provider (required)
  -model string       Model name to use
  -system string      Override system prompt
  -skill strings      Skills directory path (can be specified multiple times)
  -session string     Session file path to load/save conversations
  -proxy string       HTTP proxy URL (supports HTTP, HTTPS, and SOCKS5)
  -context-limit string  Provider context window size in tokens (supports K/M suffix, e.g., 200K, 1M)
  -debug-api          Write raw API requests and responses to log file
  -version            Show version information
  -help               Show help information

Examples:
  coreclaw --type openai --base-url https://api.openai.com/v1 --api-key $OPENAI_API_KEY --model gpt-4o
  coreclaw --type anthropic --base-url https://api.anthropic.com --api-key $ANTHROPIC_API_KEY --model claude-sonnet-4
  coreclaw --type openai --base-url http://localhost:11434/v1 --api-key xxx --model llama3
  coreclaw --type openai --base-url http://localhost:11434/v1 --api-key xxx --model llama3 --session ~/mysession.md
  coreclaw --type openai --base-url http://localhost:11434/v1 --api-key xxx --model llama3 --skill ./skills1 --skill ./skills2
  coreclaw --type openai --base-url https://api.openai.com/v1 --api-key $OPENAI_API_KEY --model gpt-4o --proxy http://127.0.0.1:7890

`)
}
