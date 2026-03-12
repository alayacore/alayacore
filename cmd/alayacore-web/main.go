package main

import (
	"context"
	"fmt"
	"os"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
	"github.com/alayacore/alayacore/internal/adaptors/websocket"
	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/config"
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

	// If no CLI model was provided, we need to load a model from config
	if appCfg.Model == nil {
		// Create a temporary model manager to check for models
		modelManager := agentpkg.NewModelManager(cfg.ModelConfig)
		if !modelManager.HasModels() {
			modelPath := modelManager.GetFilePath()
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Error: No models configured.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Please edit the model config file:")
			fmt.Fprintf(os.Stderr, "  %s\n", modelPath)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Or provide model via CLI:")
			fmt.Fprintln(os.Stderr, "  alayacore-web --type openai --base-url <url> --api-key <key> --model <model>")
			fmt.Fprintln(os.Stderr, "")
			os.Exit(1)
		}

		// Get the active model and create provider/model
		activeModel := modelManager.GetActive()
		if activeModel != nil {
			provider, err := app.CreateProvider(activeModel.ProtocolType, activeModel.APIKey, activeModel.BaseURL, cfg.DebugAPI, cfg.Proxy)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: Failed to create provider: %v\n\n", err)
				os.Exit(1)
			}

			model, err := provider.LanguageModel(context.Background(), activeModel.ModelName)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: Failed to create language model: %v\n\n", err)
				os.Exit(1)
			}

			appCfg.Model = model
		}
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
  -type string       Provider type: anthropic, openai (optional if models.conf exists)
  -base-url string   API endpoint URL (optional if models.conf exists)
  -api-key string    API key for the provider (optional if models.conf exists)
  -model string      Model name to use
  -model-config string  Model config file path (default: ~/.alayacore/models.conf)
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
