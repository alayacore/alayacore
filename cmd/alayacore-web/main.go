package main

import (
	"fmt"
	"os"

	"github.com/wallacegibbon/alayacore/internal/adaptors/websocket"
	"github.com/wallacegibbon/alayacore/internal/app"
	"github.com/wallacegibbon/alayacore/internal/config"
)

func main() {
	cfg := config.Parse()

	if cfg.ShowVersion {
		fmt.Printf("alayacore-web version %s\n", config.Version)
		os.Exit(0)
	}

	if cfg.ShowHelp {
		printHelp()
		os.Exit(1)
	}

	appCfg, err := app.Setup(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	port := cfg.Addr
	if port == "" {
		port = ":8080"
	}

	// Create WebSocket adaptor
	adaptor := websocket.NewWebSocketAdaptor(port, appCfg)
	adaptor.Start()

	// Wait for interrupt
	select {}
}

func printHelp() {
	fmt.Print(`AlayaCore Web - A WebSocket server for AlayaCore

Usage:
  alayacore-web [flags]

Flags:
  -type string       Provider type: anthropic, openai (required)
  -base-url string   API endpoint URL (required)
  -api-key string    API key for the provider (required)
  -model string      Model name to use
  -system string     Override system prompt
  -skill strings     Skills directory path (can be specified multiple times)
  -addr string       Server address to listen on (default ":8080")
  -session string    Session file path to load/save conversations
  -proxy string      HTTP proxy URL (supports HTTP, HTTPS, and SOCKS5)
  -debug-api         Write raw API requests and responses to log file
  -version           Show version information
  -help              Show help information

Examples:
  alayacore-web --type openai --base-url https://api.openai.com/v1 --api-key $OPENAI_API_KEY --model gpt-4o
  alayacore-web --type anthropic --base-url https://api.anthropic.com --api-key $ANTHROPIC_API_KEY --model claude-sonnet-4
  alayacore-web --type openai --base-url http://localhost:11434/v1 --api-key xxx --model llama3 --addr :9090
  alayacore-web --type openai --base-url https://api.openai.com/v1 --api-key $OPENAI_API_KEY --model gpt-4o --session ~/my-session.md
  alayacore-web --type openai --base-url http://localhost:11434/v1 --api-key xxx --model llama3 --skill ./skills1 --skill ./skills2
  alayacore-web --type openai --base-url https://api.openai.com/v1 --api-key $OPENAI_API_KEY --model gpt-4o --proxy socks5://127.0.0.1:1080
`)
}
